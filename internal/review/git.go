package review

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type FileCategory string

const (
	FileCategoryAppCode      FileCategory = "app_code"
	FileCategoryConfig       FileCategory = "config"
	FileCategoryDeployScript FileCategory = "deploy_script"
	FileCategoryEnvExample   FileCategory = "env_example"
	FileCategoryGhostable    FileCategory = "ghostable"
	FileCategoryPlaintextEnv FileCategory = "plaintext_env"
	FileCategoryOther        FileCategory = "other"
)

type ChangedFile struct {
	Path     string       `json:"path"`
	Status   string       `json:"status"`
	Category FileCategory `json:"category"`
}

type ChangedLine struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type gitChanges struct {
	Files      []ChangedFile
	AddedLines []ChangedLine
}

const maxReviewFileBytes = 1024 * 1024

var diffHunkPattern = regexp.MustCompile(`@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func readGitChanges(ctx context.Context, root string, baseRef string, headRef string) (gitChanges, error) {
	if headRef != "" {
		revision := baseRef + "..." + headRef
		files, err := gitChangedFiles(ctx, root, revision)
		if err != nil {
			return gitChanges{}, err
		}
		lines, err := gitAddedLines(ctx, root, revision)
		if err != nil {
			return gitChanges{}, err
		}
		return combineGitChanges(files, lines), nil
	}

	baseRevision := baseRef + "...HEAD"
	baseFiles, err := gitChangedFiles(ctx, root, baseRevision)
	if err != nil {
		return gitChanges{}, err
	}
	baseLines, err := gitAddedLines(ctx, root, baseRevision)
	if err != nil {
		return gitChanges{}, err
	}

	worktreeFiles, err := gitChangedFiles(ctx, root, "HEAD")
	if err != nil {
		return gitChanges{}, err
	}
	worktreeLines, err := gitAddedLines(ctx, root, "HEAD")
	if err != nil {
		return gitChanges{}, err
	}

	untrackedFiles, untrackedLines, err := gitUntrackedChanges(ctx, root)
	if err != nil {
		return gitChanges{}, err
	}

	files := append(baseFiles, worktreeFiles...)
	files = append(files, untrackedFiles...)
	lines := append(baseLines, worktreeLines...)
	lines = append(lines, untrackedLines...)
	return combineGitChanges(files, lines), nil
}

func resolveReviewBaseRef(ctx context.Context, root string, provided string) (string, error) {
	provided = strings.TrimSpace(provided)
	if provided != "" {
		return provided, nil
	}

	for _, candidate := range reviewBaseCandidates(ctx, root) {
		if gitRefExists(ctx, root, candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no git base ref found; commit at least once or pass --base <ref>")
}

func reviewBaseCandidates(ctx context.Context, root string) []string {
	candidates := []string{}
	if upstream := currentBranchUpstream(ctx, root); upstream != "" {
		candidates = append(candidates, upstream)
	}
	candidates = append(candidates, "origin/main", "origin/master", "main", "master", "HEAD")
	return dedupeStrings(candidates)
}

func currentBranchUpstream(ctx context.Context, root string) string {
	output, err := runGit(ctx, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func gitRefExists(ctx context.Context, root string, ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	command := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	command.Dir = root
	return command.Run() == nil
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func combineGitChanges(files []ChangedFile, lines []ChangedLine) gitChanges {
	byPath := map[string]ChangedFile{}
	for _, file := range files {
		if file.Path == "" {
			continue
		}
		if existing, ok := byPath[file.Path]; ok {
			file.Status = combinedStatus(existing.Status, file.Status)
		}
		byPath[file.Path] = file
	}

	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	combined := gitChanges{AddedLines: dedupeChangedLines(lines)}
	for _, path := range paths {
		combined.Files = append(combined.Files, byPath[path])
	}
	return combined
}

func combinedStatus(first string, second string) string {
	if first == second {
		return first
	}
	if first == "added" || second == "added" {
		return "added"
	}
	if first == "deleted" || second == "deleted" {
		return "modified"
	}
	return "modified"
}

func dedupeChangedLines(lines []ChangedLine) []ChangedLine {
	seen := map[string]bool{}
	result := make([]ChangedLine, 0, len(lines))
	for _, line := range lines {
		id := fmt.Sprintf("%s:%d:%s", line.Path, line.Line, line.Text)
		if seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, line)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path == result[j].Path {
			return result[i].Line < result[j].Line
		}
		return result[i].Path < result[j].Path
	})
	return result
}

func gitChangedFiles(ctx context.Context, root string, revision string) ([]ChangedFile, error) {
	output, err := runGit(ctx, root, "diff", "--name-status", revision, "--")
	if err != nil {
		return nil, err
	}
	return parseGitNameStatus(output), nil
}

func gitAddedLines(ctx context.Context, root string, revision string) ([]ChangedLine, error) {
	output, err := runGit(ctx, root, "diff", "--unified=0", "--no-ext-diff", "--no-color", revision, "--")
	if err != nil {
		return nil, err
	}
	return parseGitPatchAddedLines(output), nil
}

func gitUntrackedChanges(ctx context.Context, root string) ([]ChangedFile, []ChangedLine, error) {
	output, err := runGit(ctx, root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, nil, err
	}

	files := []ChangedFile{}
	lines := []ChangedLine{}
	for _, rawPath := range bytes.Split([]byte(output), []byte{0}) {
		path := filepath.ToSlash(strings.TrimSpace(string(rawPath)))
		if path == "" {
			continue
		}
		absolutePath := filepath.Join(root, filepath.FromSlash(path))
		info, err := os.Lstat(absolutePath)
		if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		files = append(files, ChangedFile{
			Path:     path,
			Status:   "added",
			Category: classifyChangedFile(path),
		})
		fileLines, err := readAddedFileLines(root, path)
		if err != nil {
			continue
		}
		lines = append(lines, fileLines...)
	}
	return files, lines, nil
}

func runGit(ctx context.Context, root string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), detail)
	}
	return string(output), nil
}

func parseGitNameStatus(output string) []ChangedFile {
	files := []ChangedFile{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 2 {
			continue
		}
		status := statusName(fields[0])
		path := fields[len(fields)-1]
		files = append(files, ChangedFile{
			Path:     filepath.ToSlash(path),
			Status:   status,
			Category: classifyChangedFile(path),
		})
	}
	return files
}

func statusName(raw string) string {
	if raw == "" {
		return "modified"
	}
	switch raw[0] {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	default:
		return "modified"
	}
}

func parseGitPatchAddedLines(output string) []ChangedLine {
	lines := []ChangedLine{}
	currentPath := ""
	nextLineNumber := 0

	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			currentPath = parseDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
		case strings.HasPrefix(line, "@@ "):
			nextLineNumber = parseNewLineNumber(line)
		case currentPath != "" && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			lines = append(lines, ChangedLine{
				Path: currentPath,
				Line: nextLineNumber,
				Text: strings.TrimPrefix(line, "+"),
			})
			nextLineNumber++
		case currentPath != "" && strings.HasPrefix(line, " ") && nextLineNumber > 0:
			nextLineNumber++
		}
	}
	return lines
}

func parseDiffPath(path string) string {
	if path == "/dev/null" {
		return ""
	}
	path = strings.TrimPrefix(path, "b/")
	return filepath.ToSlash(path)
}

func parseNewLineNumber(line string) int {
	matches := diffHunkPattern.FindStringSubmatch(line)
	if len(matches) != 2 {
		return 0
	}
	var number int
	fmt.Sscanf(matches[1], "%d", &number)
	return number
}

func readAddedFileLines(root string, path string) ([]ChangedLine, error) {
	absolutePath := filepath.Join(root, filepath.FromSlash(path))
	info, err := os.Stat(absolutePath)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxReviewFileBytes {
		return nil, nil
	}

	content, err := os.ReadFile(absolutePath)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return nil, nil
	}

	lines := []ChangedLine{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		lines = append(lines, ChangedLine{
			Path: path,
			Line: lineNumber,
			Text: scanner.Text(),
		})
	}
	return lines, scanner.Err()
}

func classifyChangedFile(filePath string) FileCategory {
	path := filepath.ToSlash(strings.TrimPrefix(filePath, "./"))
	base := filepath.Base(path)

	if base == ".env.example" {
		return FileCategoryEnvExample
	}
	if isPlaintextEnvPath(path) {
		return FileCategoryPlaintextEnv
	}
	if path == ".ghostable" || strings.HasPrefix(path, ".ghostable/") {
		return FileCategoryGhostable
	}
	if isDeployScriptPath(path, base) {
		return FileCategoryDeployScript
	}
	if isConfigPath(path, base) {
		return FileCategoryConfig
	}
	if isAppCodePath(path, base) {
		return FileCategoryAppCode
	}
	return FileCategoryOther
}

func isPlaintextEnvPath(path string) bool {
	base := filepath.Base(path)
	if base == ".env.example" {
		return false
	}
	return base == ".env" || strings.HasPrefix(base, ".env.")
}

func isDeployScriptPath(path string, base string) bool {
	lowerPath := strings.ToLower(path)
	lowerBase := strings.ToLower(base)
	if strings.HasPrefix(lowerPath, ".github/workflows/") ||
		strings.Contains(lowerPath, "/deploy/") ||
		strings.Contains(lowerPath, "/deployment/") ||
		strings.Contains(lowerPath, "/scripts/deploy") ||
		strings.HasPrefix(lowerPath, "deploy/") ||
		strings.HasPrefix(lowerPath, "deployment/") {
		return true
	}
	switch lowerBase {
	case "dockerfile", "docker-compose.yml", "docker-compose.yaml", "procfile", "vapor.yml", "vapor.yaml", "forge.sh":
		return true
	default:
		return strings.HasSuffix(lowerBase, ".sh") || strings.HasSuffix(lowerBase, ".bash") || strings.HasSuffix(lowerBase, ".zsh")
	}
}

func isConfigPath(path string, base string) bool {
	lowerPath := strings.ToLower(path)
	lowerBase := strings.ToLower(base)
	if strings.HasPrefix(lowerPath, "config/") || strings.Contains(lowerPath, "/config/") {
		return true
	}
	if strings.Contains(lowerBase, ".config.") {
		return true
	}
	switch lowerBase {
	case "appsettings.json", "settings.py", "phpunit.xml", "package.json", "composer.json":
		return true
	default:
		return false
	}
}

func isAppCodePath(path string, base string) bool {
	extension := strings.ToLower(filepath.Ext(base))
	switch extension {
	case ".go", ".php", ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".py", ".rb", ".java", ".kt", ".cs", ".rs", ".swift":
		return true
	default:
		return strings.HasPrefix(path, "app/") || strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "cmd/")
	}
}
