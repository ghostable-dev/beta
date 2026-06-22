package review

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Reference struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Pattern string `json:"pattern"`
}

type referencePattern struct {
	name      string
	expr      *regexp.Regexp
	group     int
	shellOnly bool
}

var referencePatterns = []referencePattern{
	{name: "php env", expr: regexp.MustCompile(`(?i)\benv\(\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]`), group: 1},
	{name: "php getenv", expr: regexp.MustCompile(`(?i)\bgetenv\(\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]`), group: 1},
	{name: "php $_ENV", expr: regexp.MustCompile(`\$_ENV\s*\[\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]\s*\]`), group: 1},
	{name: "javascript process.env", expr: regexp.MustCompile(`\bprocess\.env\.([A-Za-z_][A-Za-z0-9_]*)\b`), group: 1},
	{name: "javascript process.env[]", expr: regexp.MustCompile(`\bprocess\.env\s*\[\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]\s*\]`), group: 1},
	{name: "go os env", expr: regexp.MustCompile(`\bos\.(?:Getenv|LookupEnv)\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\)`), group: 1},
	{name: "python os.environ[]", expr: regexp.MustCompile(`\bos\.environ\s*\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\]`), group: 1},
	{name: "python os.environ.get", expr: regexp.MustCompile(`\bos\.environ\.get\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']`), group: 1},
	{name: "python os.getenv", expr: regexp.MustCompile(`\bos\.getenv\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']`), group: 1},
	{name: "python environ[]", expr: regexp.MustCompile(`\benviron\s*\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\]`), group: 1},
	{name: "python environ.get", expr: regexp.MustCompile(`\benviron\.get\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']`), group: 1},
	{name: "ruby ENV[]", expr: regexp.MustCompile(`\bENV\s*\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\]`), group: 1},
	{name: "ruby ENV.fetch", expr: regexp.MustCompile(`\bENV\.fetch\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']`), group: 1},
	{name: "java System.getenv", expr: regexp.MustCompile(`\bSystem\.getenv\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\)`), group: 1},
	{name: "csharp Environment.GetEnvironmentVariable", expr: regexp.MustCompile(`\b(?:System\.)?Environment\.GetEnvironmentVariable\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']`), group: 1},
	{name: "rust env var", expr: regexp.MustCompile(`\b(?:std::)?env::var(?:_os)?\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\)`), group: 1},
	{name: "swift ProcessInfo environment", expr: regexp.MustCompile(`\bProcessInfo\.processInfo\.environment\s*\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\]`), group: 1},
	{name: "shell braced env", expr: regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`), group: 1, shellOnly: true},
	{name: "shell env", expr: regexp.MustCompile(`(^|[^A-Za-z0-9_])\$([A-Za-z_][A-Za-z0-9_]*)\b`), group: 2, shellOnly: true},
}

var dotenvKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var shellEnvKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func findChangedReferences(lines []ChangedLine, files []ChangedFile) []Reference {
	categories := map[string]FileCategory{}
	for _, file := range files {
		categories[file.Path] = file.Category
	}

	seen := map[string]bool{}
	references := []Reference{}
	for _, line := range lines {
		if isExternalWorkflowPath(line.Path) {
			continue
		}
		category := categories[line.Path]
		for _, pattern := range referencePatterns {
			if pattern.shellOnly && !isShellReferenceContext(line.Path, category) {
				continue
			}
			for _, match := range pattern.expr.FindAllStringSubmatch(line.Text, -1) {
				if len(match) <= pattern.group {
					continue
				}
				key := strings.TrimSpace(match[pattern.group])
				if !isReviewEnvironmentKey(key, pattern.shellOnly) {
					continue
				}
				id := referenceID(line.Path, line.Line, key, pattern.name)
				if seen[id] {
					continue
				}
				seen[id] = true
				references = append(references, Reference{
					Key:     key,
					Path:    line.Path,
					Line:    line.Line,
					Pattern: pattern.name,
				})
			}
		}
	}

	sort.Slice(references, func(i, j int) bool {
		if references[i].Key == references[j].Key {
			if references[i].Path == references[j].Path {
				return references[i].Line < references[j].Line
			}
			return references[i].Path < references[j].Path
		}
		return references[i].Key < references[j].Key
	})
	return references
}

func isExternalWorkflowPath(path string) bool {
	path = strings.TrimPrefix(filepathSlash(path), "./")
	return path == ".github" || strings.HasPrefix(path, ".github/")
}

func isShellReferenceContext(path string, category FileCategory) bool {
	if category == FileCategoryDeployScript {
		return true
	}
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".sh") ||
		strings.HasSuffix(lower, ".bash") ||
		strings.HasSuffix(lower, ".zsh") ||
		strings.HasSuffix(lower, ".envrc")
}

func isReviewEnvironmentKey(key string, shellReference bool) bool {
	key = strings.TrimSpace(key)
	if shellReference {
		return shellEnvKeyPattern.MatchString(key) && !ignoredReferenceKey(key)
	}
	return dotenvKeyPattern.MatchString(key) && !ignoredReferenceKey(key)
}

func ignoredReferenceKey(key string) bool {
	key = strings.ToUpper(key)
	switch key {
	case "CI", "HOME", "LOGNAME", "NODE_ENV", "OLDPWD", "PATH", "PWD", "SHELL", "TEMP", "TERM", "TMP", "TMPDIR", "USER":
		return true
	case "GITHUB_ACTIONS", "GITHUB_ACTOR", "GITHUB_BASE_REF", "GITHUB_EVENT_NAME", "GITHUB_HEAD_REF", "GITHUB_REF", "GITHUB_REPOSITORY", "GITHUB_SHA", "GITHUB_TOKEN", "GITHUB_WORKFLOW":
		return true
	default:
		return false
	}
}

func referenceID(path string, line int, key string, pattern string) string {
	return path + ":" + strconv.Itoa(line) + ":" + key + ":" + pattern
}

func filepathSlash(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}
