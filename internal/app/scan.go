package app

import (
	"fmt"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/scanner"
	"github.com/ghostable-dev/beta/internal/store"
)

func (r *Runner) runScan(args []string) error {
	fs := newFlagSet("scan", r.errOut)
	var ignores cli.Strings
	fs.Var(&ignores, "ignore", "Ignore pattern; may be repeated or comma-separated")
	level := fs.String("level", "", "Scan level: relaxed, standard, or strict")
	maxSize := fs.String("max-size", "", "Maximum file size in bytes")
	showValues := fs.Bool("show-values", false, "Print plaintext findings")
	jsonOut := fs.Bool("json", false, "Print scan result as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("show-values", "json"))
	if err != nil {
		return err
	}

	root := "."
	defaultIgnores := []string{
		".git/**",
		".git",
		"node_modules/**",
		"node_modules",
		"vendor/**",
		"vendor",
		"dist/**",
		"dist",
		"build/**",
		"build",
		".ghostable/environments/**/values/**",
	}
	manifestIgnores := []string{}
	scanLevel := scanner.DefaultLevel
	if repo, err := store.Open("."); err == nil {
		root = repo.Root
		if len(repo.Manifest.ScanIgnores) > 0 {
			manifestIgnores = repo.Manifest.ScanIgnores
		}
		scanLevel = scanner.NormalizeLevel(repo.Manifest.ScanLevel)
	}
	if strings.TrimSpace(*level) != "" {
		scanLevel = scanner.NormalizeLevel(*level)
	}
	allIgnores := append([]string{}, defaultIgnores...)
	allIgnores = append(allIgnores, manifestIgnores...)
	allIgnores = append(allIgnores, ignores...)

	r.printProgress(!*jsonOut, "Scanning project files")
	result, err := scanner.Scan(scanner.Options{
		Root:      root,
		Paths:     positionals,
		Ignores:   allIgnores,
		Level:     scanLevel,
		MaxBytes:  int64(parsePositiveInt(*maxSize, 0)),
		ShowValue: *showValues,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	if len(result.Findings) == 0 {
		fmt.Fprintln(r.out, success(fmt.Sprintf("No hard-coded secrets found. Scanned %d files.", result.Scanned)))
		return nil
	}
	for _, finding := range result.Findings {
		label := finding.Redacted
		if *showValues && finding.Value != "" {
			label = finding.Value
		}
		key := ""
		if finding.Key != "" {
			key = " " + finding.Key
		}
		location := fmt.Sprintf("%s:%d:%d", finding.Path, finding.Line, finding.Column)
		fmt.Fprintf(r.out, "%s %s%s %s [%s]\n",
			danger(location),
			finding.Kind,
			key,
			label,
			finding.Confidence,
		)
	}
	return fmt.Errorf("found %d possible secret%s", len(result.Findings), plural(len(result.Findings)))
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
