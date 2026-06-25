package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/store"
)

type envCleanResult struct {
	Root           string   `json:"root"`
	Files          []string `json:"files"`
	Removed        []string `json:"removed"`
	DryRun         bool     `json:"dryRun"`
	IncludeExample bool     `json:"includeExample"`
}

func (r *Runner) runEnvClean(args []string) error {
	fs := newFlagSet("env clean", r.errOut)
	dryRun := fs.Bool("dry-run", false, "Show local env files without removing them")
	assumeYes := fs.Bool("assume-yes", false, "Skip confirmation prompt")
	fs.BoolVar(assumeYes, "y", false, "Skip confirmation prompt")
	includeExample := fs.Bool("include-example", false, "Also remove .env.example files")
	jsonOut := fs.Bool("json", false, "Print cleanup result as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("dry-run", "assume-yes", "y", "include-example", "json"))
	if err != nil {
		return err
	}
	if len(positionals) > 0 {
		return fmt.Errorf("usage: ghostable env clean [options]")
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}

	result, err := buildEnvCleanResult(repo, *includeExample, *dryRun)
	if err != nil {
		return err
	}
	if *dryRun {
		return r.printEnvCleanResult(result, *jsonOut)
	}
	if len(result.Files) == 0 {
		return r.printEnvCleanResult(result, *jsonOut)
	}

	if !*jsonOut {
		printEnvCleanFileList(r.out, result.Files)
	}
	ok, err := r.confirm(fmt.Sprintf("Remove %s from the project root?", envCleanFileCount(len(result.Files))), *assumeYes)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("canceled")
	}

	for _, file := range result.Files {
		if err := os.Remove(filepath.Join(repo.Root, file)); err != nil {
			return fmt.Errorf("remove %s: %w", file, err)
		}
		result.Removed = append(result.Removed, file)
	}

	return r.printEnvCleanResult(result, *jsonOut)
}

func buildEnvCleanResult(repo store.Repository, includeExample bool, dryRun bool) (envCleanResult, error) {
	files, err := envCleanFiles(repo.Root, includeExample)
	if err != nil {
		return envCleanResult{}, err
	}
	return envCleanResult{
		Root:           repo.Root,
		Files:          files,
		Removed:        []string{},
		DryRun:         dryRun,
		IncludeExample: includeExample,
	}, nil
}

func envCleanFiles(root string, includeExample bool) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read project root: %w", err)
	}

	files := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !isLocalEnvFileName(name, includeExample) {
			continue
		}
		if entry.IsDir() {
			continue
		}
		if entry.Type()&os.ModeSymlink == 0 {
			info, err := entry.Info()
			if err != nil {
				return nil, fmt.Errorf("inspect %s: %w", name, err)
			}
			if !info.Mode().IsRegular() {
				continue
			}
		}
		files = append(files, name)
	}
	sortStrings(files)
	return files, nil
}

func isLocalEnvFileName(name string, includeExample bool) bool {
	if name != ".env" && !strings.HasPrefix(name, ".env.") {
		return false
	}
	if includeExample {
		return true
	}
	return name != ".env.example" && !strings.HasPrefix(name, ".env.example.")
}

func (r *Runner) printEnvCleanResult(result envCleanResult, jsonOut bool) error {
	if jsonOut {
		return printJSON(r.out, result)
	}
	if len(result.Files) == 0 {
		fmt.Fprintln(r.out, warn("No local env files found."))
		return nil
	}
	if result.DryRun {
		fmt.Fprintln(r.out, warn(fmt.Sprintf("Dry run: %s would be removed.", envCleanFileCount(len(result.Files)))))
		printEnvCleanFileList(r.out, result.Files)
		return nil
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Removed %s.", envCleanFileCount(len(result.Removed)))))
	return nil
}

func printEnvCleanFileList(out io.Writer, files []string) {
	for _, file := range files {
		fmt.Fprintf(out, "  %s\n", file)
	}
}

func envCleanFileCount(count int) string {
	if count == 1 {
		return "1 local env file"
	}
	return fmt.Sprintf("%d local env files", count)
}
