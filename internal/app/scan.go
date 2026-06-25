package app

import (
	"fmt"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/prompt"
	"github.com/ghostable-dev/beta/internal/scanner"
	"github.com/ghostable-dev/beta/internal/store"
)

const scanSuppressionCode = "hardcoded_secret"

var scanCommandOptions = []commandOption{
	{Label: "run", Description: "Scan project files for hard-coded secrets"},
	{Label: "suppress", Description: "Create a signed scan finding suppression"},
}

type scanOptions struct {
	Ignores    []string
	Level      string
	MaxSize    string
	ShowValues bool
	JSON       bool
	Paths      []string
}

func (r *Runner) runScan(args []string) error {
	if len(args) == 0 && r.interactive {
		selected, err := r.selectCommand("Select a scan command", scanCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printScanHelp()
		return nil
	}
	if len(args) > 0 {
		switch args[0] {
		case "run":
			return r.runScanReport(args[1:])
		case "suppress":
			return r.runScanSuppress(args[1:])
		}
	}
	return r.runScanReport(args)
}

func (r *Runner) runScanReport(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printScanHelp()
		return nil
	}
	options, err := r.parseScanOptions("scan", args)
	if err != nil {
		return err
	}

	result, suppressed, err := r.scanProject(options, !options.JSON)
	if err != nil {
		return err
	}
	if options.JSON {
		return printJSON(r.out, result)
	}
	if len(result.Findings) == 0 {
		fmt.Fprintln(r.out, success(fmt.Sprintf("No hard-coded secrets found. Scanned %d files.", result.Scanned)))
		if suppressed > 0 {
			fmt.Fprintf(r.out, "%s %d finding%s suppressed.\n", warn("Suppressed:"), suppressed, plural(suppressed))
		}
		return nil
	}
	printScanFindings(r, result, options.ShowValues)
	if suppressed > 0 {
		fmt.Fprintf(r.out, "%s %d finding%s suppressed.\n", warn("Suppressed:"), suppressed, plural(suppressed))
	}
	return fmt.Errorf("found %d possible secret%s", len(result.Findings), plural(len(result.Findings)))
}

func (r *Runner) parseScanOptions(name string, args []string) (scanOptions, error) {
	fs := newFlagSet(name, r.errOut)
	var ignores cli.Strings
	fs.Var(&ignores, "ignore", "Ignore pattern; may be repeated or comma-separated")
	level := fs.String("level", "", "Scan level: relaxed, standard, or strict")
	maxSize := fs.String("max-size", "", "Maximum file size in bytes")
	showValues := fs.Bool("show-values", false, "Print plaintext findings")
	jsonOut := fs.Bool("json", false, "Print scan result as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("show-values", "json"))
	if err != nil {
		return scanOptions{}, err
	}
	return scanOptions{
		Ignores:    ignores,
		Level:      *level,
		MaxSize:    *maxSize,
		ShowValues: *showValues,
		JSON:       *jsonOut,
		Paths:      positionals,
	}, nil
}

func (r *Runner) scanProject(options scanOptions, progress bool) (scanner.Result, int, error) {
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
	suppressions := []domain.SuppressionRecord{}
	if repo, err := store.OpenProject("."); err == nil {
		root = repo.Root
		if len(repo.Manifest.ScanIgnores) > 0 {
			manifestIgnores = repo.Manifest.ScanIgnores
		}
		scanLevel = scanner.NormalizeLevel(repo.Manifest.ScanLevel)
		suppressions, err = activeSuppressionRecords(repo)
		if err != nil {
			return scanner.Result{}, 0, err
		}
	}
	if strings.TrimSpace(options.Level) != "" {
		scanLevel = scanner.NormalizeLevel(options.Level)
	}
	allIgnores := append([]string{}, defaultIgnores...)
	allIgnores = append(allIgnores, manifestIgnores...)
	allIgnores = append(allIgnores, options.Ignores...)

	r.printProgress(progress, "Scanning project files")
	result, err := scanner.Scan(scanner.Options{
		Root:      root,
		Paths:     options.Paths,
		Ignores:   allIgnores,
		Level:     scanLevel,
		MaxBytes:  int64(parsePositiveInt(options.MaxSize, 0)),
		ShowValue: options.ShowValues,
	})
	if err != nil {
		return scanner.Result{}, 0, err
	}
	suppressed := applyScanSuppressions(&result, suppressions)
	return result, suppressed, nil
}

func printScanFindings(r *Runner, result scanner.Result, showValues bool) {
	for _, finding := range result.Findings {
		label := finding.Redacted
		if showValues && finding.Value != "" {
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
}

func applyScanSuppressions(result *scanner.Result, suppressions []domain.SuppressionRecord) int {
	unsuppressed := []scanner.Finding{}
	suppressed := 0
	for _, finding := range result.Findings {
		target := scanSuppressionTarget(finding)
		if _, ok := matchingSuppressionTarget(target, suppressions); ok {
			suppressed++
			continue
		}
		unsuppressed = append(unsuppressed, finding)
	}
	result.Findings = unsuppressed
	result.HasSecrets = len(result.Findings) > 0
	return suppressed
}

func scanSuppressionTarget(finding scanner.Finding) suppressionTarget {
	return suppressionTarget{
		Source:      suppressionSourceScan,
		Code:        scanSuppressionCode,
		Key:         finding.Key,
		Path:        finding.Path,
		Line:        finding.Line,
		Column:      finding.Column,
		Kind:        finding.Kind,
		Fingerprint: scanSuppressionFingerprintForFinding(finding),
		AlternateFingerprints: []string{
			legacyScanSuppressionFingerprint(finding.Path, finding.Key, finding.Kind),
		},
	}
}

func scanSuppressionFingerprintForFinding(finding scanner.Finding) string {
	return scanSuppressionFingerprint(
		finding.Path,
		finding.Key,
		finding.Kind,
		finding.EvidenceDigest,
		finding.Occurrence,
	)
}

func (r *Runner) runScanSuppress(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printScanSuppressHelp()
		return nil
	}
	fs := newFlagSet("scan suppress", r.errOut)
	var ignores cli.Strings
	fs.Var(&ignores, "ignore", "Ignore pattern used while selecting a finding")
	level := fs.String("level", "", "Scan level used while selecting a finding")
	maxSize := fs.String("max-size", "", "Maximum file size in bytes while selecting a finding")
	path := fs.String("path", "", "Finding path scope")
	line := fs.Int("line", 0, "Finding line scope")
	column := fs.Int("column", 0, "Finding column scope")
	kind := fs.String("kind", "", "Finding kind scope")
	key := fs.String("key", "", "Variable key scope")
	code := fs.String("code", scanSuppressionCode, "Finding code to suppress")
	reason := fs.String("reason", "", "Suppression reason")
	expiresAt := fs.String("expires-at", "", "Expiration timestamp in RFC3339")
	expiresIn := fs.String("expires-in", "", "Expiration duration, such as 30d")
	jsonOut := fs.Bool("json", false, "Print suppression as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("json"))
	if err != nil {
		return err
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	options := scanOptions{
		Ignores: ignores,
		Level:   *level,
		MaxSize: *maxSize,
		Paths:   positionals,
	}
	input, err := r.resolveScanSuppressionInput(options, *code, *path, *line, *column, *kind, *key, *reason, *expiresAt, *expiresIn)
	if err != nil {
		return err
	}
	result, err := repo.CreateSuppression(input)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Created suppression %s.", result.Suppression.ID)))
	fmt.Fprintf(r.out, "%s %s\n", warn("File:"), result.File)
	return nil
}

func (r *Runner) resolveScanSuppressionInput(options scanOptions, code string, path string, line int, column int, kind string, key string, reason string, expiresAt string, expiresIn string) (store.CreateSuppressionInput, error) {
	selectedCode := strings.TrimSpace(code)
	if selectedCode == "" {
		selectedCode = scanSuppressionCode
	}
	selectedPath := strings.TrimSpace(path)
	selectedLine := line
	selectedColumn := column
	selectedKind := strings.TrimSpace(kind)
	selectedKey := strings.TrimSpace(key)
	selectedFingerprint := ""

	if selectedPath == "" && r.interactive {
		finding, ok, err := r.selectScanFindingForSuppression(options)
		if err != nil {
			return store.CreateSuppressionInput{}, err
		}
		if ok {
			selectedPath = finding.Path
			selectedLine = finding.Line
			selectedColumn = finding.Column
			selectedKind = finding.Kind
			selectedKey = finding.Key
			selectedFingerprint = scanSuppressionFingerprintForFinding(finding)
		}
	}
	if selectedPath == "" {
		return store.CreateSuppressionInput{}, fmt.Errorf("pass --path or run interactively to select a scan finding")
	}
	if selectedFingerprint == "" {
		finding, ok, err := r.findScanFindingForSuppression(options, selectedPath, selectedLine, selectedColumn, selectedKind, selectedKey)
		if err != nil {
			return store.CreateSuppressionInput{}, err
		}
		if ok {
			selectedPath = finding.Path
			selectedLine = finding.Line
			selectedColumn = finding.Column
			selectedKind = finding.Kind
			selectedKey = finding.Key
			selectedFingerprint = scanSuppressionFingerprintForFinding(finding)
		}
	}
	if selectedFingerprint == "" {
		selectedFingerprint = legacyScanSuppressionFingerprint(selectedPath, selectedKey, selectedKind)
	}

	selectedReason, err := r.askOptional("Suppression reason (optional)", reason)
	if err != nil {
		return store.CreateSuppressionInput{}, err
	}
	expiration, err := r.resolveSuppressionExpiration(expiresAt, expiresIn)
	if err != nil {
		return store.CreateSuppressionInput{}, err
	}
	return store.CreateSuppressionInput{
		Source:      suppressionSourceScan,
		Code:        selectedCode,
		Key:         selectedKey,
		Path:        selectedPath,
		Line:        selectedLine,
		Column:      selectedColumn,
		Kind:        selectedKind,
		Fingerprint: selectedFingerprint,
		Reason:      selectedReason,
		ExpiresAt:   expiration,
	}, nil
}

func (r *Runner) findScanFindingForSuppression(options scanOptions, path string, line int, column int, kind string, key string) (scanner.Finding, bool, error) {
	lookupOptions := options
	if len(lookupOptions.Paths) == 0 && strings.TrimSpace(path) != "" {
		lookupOptions.Paths = []string{path}
	}
	result, _, err := r.scanProject(lookupOptions, false)
	if err != nil {
		return scanner.Finding{}, false, err
	}

	matches := []scanner.Finding{}
	for _, finding := range result.Findings {
		if scanFindingMatchesSuppressionInput(finding, path, line, column, kind, key) {
			matches = append(matches, finding)
		}
	}
	if len(matches) == 0 {
		return scanner.Finding{}, false, nil
	}
	if len(matches) > 1 {
		return scanner.Finding{}, false, fmt.Errorf("multiple scan findings match suppression scope; pass --line, --column, or run interactively")
	}
	return matches[0], true, nil
}

func scanFindingMatchesSuppressionInput(finding scanner.Finding, path string, line int, column int, kind string, key string) bool {
	if strings.TrimSpace(path) != "" && normalizeSuppressionPathForMatch(finding.Path) != normalizeSuppressionPathForMatch(path) {
		return false
	}
	if line > 0 && finding.Line != line {
		return false
	}
	if column > 0 && finding.Column != column {
		return false
	}
	if strings.TrimSpace(kind) != "" && finding.Kind != strings.TrimSpace(kind) {
		return false
	}
	if strings.TrimSpace(key) != "" && finding.Key != strings.TrimSpace(key) {
		return false
	}
	return true
}

func (r *Runner) selectScanFindingForSuppression(options scanOptions) (scanner.Finding, bool, error) {
	result, _, err := r.scanProject(options, true)
	if err != nil {
		return scanner.Finding{}, false, err
	}
	if len(result.Findings) == 0 {
		return scanner.Finding{}, false, nil
	}

	choices := make([]prompt.SelectOption, 0, len(result.Findings))
	findingsByChoice := map[string]scanner.Finding{}
	for index, finding := range result.Findings {
		value := fmt.Sprintf("%d", index)
		choices = append(choices, prompt.SelectOption{
			Label:       scanFindingSuppressionLabel(finding),
			Value:       value,
			Description: scanFindingSuppressionDescription(finding),
		})
		findingsByChoice[value] = finding
	}
	selected, err := r.prompts.SelectOptions("Select finding to suppress", choices, 0)
	if err != nil {
		return scanner.Finding{}, false, err
	}
	return findingsByChoice[selected], true, nil
}

func scanFindingSuppressionLabel(finding scanner.Finding) string {
	return suppressionPromptLabel(suppressionSourceScan, finding.Kind)
}

func scanFindingSuppressionDescription(finding scanner.Finding) string {
	key := finding.Key
	if key == "" {
		key = "-"
	}
	return suppressionPromptDescription(key, suppressionPromptLocation(finding.Path, finding.Line, finding.Column), "["+finding.Confidence+"]")
}

func (r *Runner) printScanHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable scan [run|suppress] [paths...] [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Compatibility command for hard-coded secret scanning. Prefer `ghostable review --secrets-only`.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, scanCommandOptions)
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Options:"))
	fmt.Fprintln(r.out, "  --ignore <PATTERN>   Ignore pattern; may be repeated or comma-separated")
	fmt.Fprintln(r.out, "  --level <LEVEL>      Scan level: relaxed, standard, or strict")
	fmt.Fprintln(r.out, "  --max-size <BYTES>   Maximum file size in bytes")
	fmt.Fprintln(r.out, "  --show-values        Print plaintext findings")
	fmt.Fprintln(r.out, "  --json               Print scan result as JSON")
}

func (r *Runner) printScanSuppressHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable scan suppress [paths...] [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Create a signed suppression for a scan finding.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Options:"))
	fmt.Fprintln(r.out, "  --path <PATH>        Finding path scope")
	fmt.Fprintln(r.out, "  --line <N>           Finding line scope")
	fmt.Fprintln(r.out, "  --column <N>         Finding column scope")
	fmt.Fprintln(r.out, "  --kind <KIND>        Finding kind scope")
	fmt.Fprintln(r.out, "  --key <KEY>          Variable key scope")
	fmt.Fprintln(r.out, "  --reason <TEXT>      Suppression reason")
	fmt.Fprintln(r.out, "  --expires-at <TIME>  Expiration timestamp in RFC3339")
	fmt.Fprintln(r.out, "  --expires-in <DURATION>  Expiration duration, such as 30d")
	fmt.Fprintln(r.out, "  --json                   Print suppression as JSON")
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
