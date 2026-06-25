package scanner

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type Options struct {
	Root      string
	Paths     []string
	Ignores   []string
	Level     string
	MaxBytes  int64
	ShowValue bool
}

type Finding struct {
	Path       string  `json:"path"`
	Line       int     `json:"line"`
	Column     int     `json:"column"`
	Kind       string  `json:"kind"`
	Key        string  `json:"key,omitempty"`
	Value      string  `json:"value,omitempty"`
	Redacted   string  `json:"redacted"`
	Entropy    float64 `json:"entropy,omitempty"`
	Confidence string  `json:"confidence"`

	EvidenceDigest string `json:"-"`
	Occurrence     int    `json:"-"`
}

type Result struct {
	Root       string    `json:"root"`
	Level      string    `json:"level"`
	Scanned    int       `json:"scanned"`
	Skipped    int       `json:"skipped"`
	Findings   []Finding `json:"findings"`
	HasSecrets bool      `json:"hasSecrets"`
}

const (
	DefaultLevel        = "standard"
	defaultMaxFileBytes = 1024 * 1024
)

var levels = map[string]bool{
	"relaxed":  true,
	"standard": true,
	"strict":   true,
}

var assignmentExpr = regexp.MustCompile("(?i)\\b([A-Z0-9_]*(?:API[_-]?KEY|SECRET|TOKEN|PASSWORD|PRIVATE[_-]?KEY|CLIENT[_-]?SECRET|ACCESS[_-]?KEY)[A-Z0-9_]*)\\b\\s*[:=]\\s*[\"']?([^\"'\\s#`,]+)")

func Scan(options Options) (Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return Result{}, err
	}

	result := Result{Root: options.Root, Level: options.Level}
	for _, path := range options.Paths {
		target := absoluteScanTarget(options.Root, path)
		if err := scanPath(options.Root, target, options, &result); err != nil {
			return result, err
		}
	}

	sort.Slice(result.Findings, func(i, j int) bool {
		if result.Findings[i].Path == result.Findings[j].Path {
			return result.Findings[i].Line < result.Findings[j].Line
		}
		return result.Findings[i].Path < result.Findings[j].Path
	})
	assignFindingOccurrences(result.Findings)
	result.HasSecrets = len(result.Findings) > 0
	return result, nil
}

func normalizeOptions(options Options) (Options, error) {
	root := options.Root
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Options{}, err
	}
	options.Root = absRoot

	if options.MaxBytes <= 0 {
		options.MaxBytes = defaultMaxFileBytes
	}
	if len(options.Paths) == 0 {
		options.Paths = []string{"."}
	}
	options.Level = NormalizeLevel(options.Level)

	return options, nil
}

func absoluteScanTarget(root string, scanPath string) string {
	if filepath.IsAbs(scanPath) {
		return scanPath
	}
	return filepath.Join(root, scanPath)
}

func scanPath(root string, target string, options Options, result *Result) error {
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		result.Skipped++
		return nil
	}

	if !info.IsDir() {
		return scanFile(root, target, info, options, result)
	}

	return filepath.WalkDir(target, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == target {
			return nil
		}
		rel := relPath(root, filePath)
		if ignored(rel, options.Ignores) {
			result.Skipped++
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			result.Skipped++
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			result.Skipped++
			return nil
		}
		if info.IsDir() {
			result.Skipped++
			return nil
		}
		return scanFile(root, filePath, info, options, result)
	})
}

func scanFile(root string, filePath string, info os.FileInfo, options Options, result *Result) error {
	if !scanPathContained(root, filePath) {
		result.Skipped++
		return nil
	}
	if info.Size() > options.MaxBytes {
		result.Skipped++
		return nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		result.Skipped++
		return nil
	}
	defer file.Close()

	sample := make([]byte, 4096)
	n, _ := file.Read(sample)
	if bytes.IndexByte(sample[:n], 0) >= 0 {
		result.Skipped++
		return nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	result.Scanned++
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		for _, finding := range inspectLine(relPath(root, filePath), lineNumber, line, options.Level, options.ShowValue) {
			result.Findings = append(result.Findings, finding)
		}
	}
	return scanner.Err()
}

func scanPathContained(root string, filePath string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	realPath, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(realRoot, realPath)
	if err != nil {
		return false
	}
	return relative == "." || (!filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func NormalizeLevel(value string) string {
	level := strings.ToLower(strings.TrimSpace(value))
	if levels[level] {
		return level
	}
	return DefaultLevel
}

func inspectLine(filePath string, lineNumber int, line string, level string, showValue bool) []Finding {
	level = NormalizeLevel(level)
	findings := inspectDetectorFindings(filePath, lineNumber, line, level, showValue)

	if level == "relaxed" {
		return findings
	}

	return append(findings, inspectAssignmentFindings(filePath, lineNumber, line, level, showValue)...)
}

func inspectDetectorFindings(filePath string, lineNumber int, line string, level string, showValue bool) []Finding {
	findings := []Finding{}
	seen := map[[2]int]bool{}
	for _, detector := range detectors {
		if !detectorEnabledForLevel(detector, level) {
			continue
		}
		matches := detector.expr.FindAllStringIndex(line, -1)
		for _, match := range matches {
			value := line[match[0]:match[1]]
			id := [2]int{match[0], match[1]}
			if seen[id] || placeholder(value) {
				continue
			}
			seen[id] = true
			findings = append(findings, Finding{
				Path:           filePath,
				Line:           lineNumber,
				Column:         match[0] + 1,
				Kind:           detector.kind,
				Value:          maybeValue(value, showValue),
				Redacted:       redact(value),
				Entropy:        shannon(value),
				Confidence:     detector.confidence,
				EvidenceDigest: evidenceDigest(value),
			})
		}
	}
	return findings
}

func detectorEnabledForLevel(detector compiledDetector, level string) bool {
	return level != "relaxed" || detector.confidence == "high"
}

func inspectAssignmentFindings(filePath string, lineNumber int, line string, level string, showValue bool) []Finding {
	findings := []Finding{}
	minLength, minEntropy := assignmentThresholds(level)

	matches := assignmentExpr.FindAllStringSubmatchIndex(line, -1)
	for _, match := range matches {
		key := line[match[2]:match[3]]
		value := strings.TrimSpace(line[match[4]:match[5]])
		if shouldSkipAssignmentValue(value, minLength, minEntropy) {
			continue
		}

		entropy := shannon(value)
		findings = append(findings, Finding{
			Path:           filePath,
			Line:           lineNumber,
			Column:         match[4] + 1,
			Kind:           "Secret assignment",
			Key:            key,
			Value:          maybeValue(value, showValue),
			Redacted:       redact(value),
			Entropy:        entropy,
			Confidence:     assignmentConfidence(level, entropy),
			EvidenceDigest: evidenceDigest(value),
		})
	}

	return findings
}

func assignFindingOccurrences(findings []Finding) {
	counts := map[string]int{}
	for index := range findings {
		key := findingOccurrenceKey(findings[index])
		counts[key]++
		findings[index].Occurrence = counts[key]
	}
}

func findingOccurrenceKey(finding Finding) string {
	parts := []string{
		finding.Path,
		finding.Kind,
		finding.Key,
		finding.EvidenceDigest,
	}
	return strings.Join(parts, "\x00")
}

func evidenceDigest(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func assignmentThresholds(level string) (int, float64) {
	if level == "strict" {
		return 8, 2.8
	}
	return 12, 3.2
}

func shouldSkipAssignmentValue(value string, minLength int, minEntropy float64) bool {
	if looksCodeExpression(value) {
		return true
	}
	if placeholder(value) || len(value) < minLength {
		return true
	}
	return shannon(value) < minEntropy && !looksCredentialish(value)
}

func assignmentConfidence(level string, entropy float64) string {
	if level == "strict" && entropy < 3.2 {
		return "low"
	}
	return "medium"
}

func looksCodeExpression(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.ContainsAny(trimmed, "(){}[]") {
		return true
	}

	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"document.", "window.", "process.", "this.", "event.", "payload.", "options."} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func ignored(rel string, patterns []string) bool {
	rel = filepath.ToSlash(strings.TrimPrefix(rel, "./"))
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(strings.TrimSpace(pattern))
		pattern = strings.TrimPrefix(pattern, "./")
		if pattern == "" {
			continue
		}
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
				return true
			}
		}
		if ok, _ := path.Match(pattern, rel); ok {
			return true
		}
		if !strings.Contains(pattern, "/") {
			for _, segment := range strings.Split(rel, "/") {
				if ok, _ := path.Match(pattern, segment); ok {
					return true
				}
			}
		}
	}
	return false
}

func relPath(root string, filePath string) string {
	rel, err := filepath.Rel(root, filePath)
	if err != nil {
		return filepath.ToSlash(filePath)
	}
	return filepath.ToSlash(rel)
}

func redact(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 8 {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[:4]) + strings.Repeat("*", min(24, len(runes)-8)) + string(runes[len(runes)-4:])
}

func maybeValue(value string, show bool) string {
	if show {
		return value
	}
	return ""
}

func placeholder(value string) bool {
	value = strings.Trim(strings.ToLower(value), `"'`)
	if value == "" {
		return true
	}
	placeholders := []string{
		"your_", "example", "placeholder", "changeme", "change-me", "todo", "xxx", "dummy", "sample",
		"test", "not-a-secret", "secret", "password", "token", "apikey", "api_key",
	}
	for _, item := range placeholders {
		if strings.Contains(value, item) {
			return true
		}
	}
	return false
}

func looksCredentialish(value string) bool {
	hasLetter := false
	hasDigit := false
	hasSymbol := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		default:
			hasSymbol = true
		}
	}
	return hasLetter && hasDigit && hasSymbol
}

func shannon(value string) float64 {
	if value == "" {
		return 0
	}
	counts := map[rune]float64{}
	total := 0.0
	for _, r := range value {
		counts[r]++
		total++
	}
	entropy := 0.0
	for _, count := range counts {
		p := count / total
		entropy -= p * math.Log2(p)
	}
	return math.Round(entropy*100) / 100
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
