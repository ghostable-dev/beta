package review

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

type ReferenceScanInput struct {
	Root    string
	Ignores []string
}

var defaultReferenceScanIgnores = []string{
	".git/**",
	"node_modules/**",
	"vendor/**",
	"dist/**",
	"build/**",
	".ghostable/**",
}

func ScanReferences(input ReferenceScanInput) ([]Reference, error) {
	root := strings.TrimSpace(input.Root)
	if root == "" {
		root = "."
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	ignores := append([]string{}, defaultReferenceScanIgnores...)
	ignores = append(ignores, input.Ignores...)

	files := []ChangedFile{}
	lines := []ChangedLine{}
	err = filepath.WalkDir(absoluteRoot, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == absoluteRoot {
			return nil
		}

		rel := referenceScanRelativePath(absoluteRoot, filePath)
		if referenceScanIgnored(rel, ignores) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}

		category := classifyChangedFile(rel)
		if !referenceScanCategory(category) {
			return nil
		}

		fileLines, err := readAddedFileLines(absoluteRoot, rel)
		if err != nil {
			return nil
		}
		if len(fileLines) == 0 {
			return nil
		}

		files = append(files, ChangedFile{
			Path:     rel,
			Status:   "present",
			Category: category,
		})
		lines = append(lines, fileLines...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return findChangedReferences(lines, files), nil
}

func referenceScanCategory(category FileCategory) bool {
	switch category {
	case FileCategoryAppCode, FileCategoryConfig, FileCategoryDeployScript:
		return true
	default:
		return false
	}
}

func referenceScanRelativePath(root string, filePath string) string {
	rel, err := filepath.Rel(root, filePath)
	if err != nil {
		return filepath.ToSlash(filePath)
	}
	return filepath.ToSlash(rel)
}

func referenceScanIgnored(rel string, patterns []string) bool {
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
