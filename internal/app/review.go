package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/prompt"
	"github.com/ghostable-dev/beta/internal/review"
	"github.com/ghostable-dev/beta/internal/scanner"
	"github.com/ghostable-dev/beta/internal/store"
)

var reviewCommandOptions = []commandOption{
	{Label: "run", Description: "Review ENV state and hard-coded secrets"},
	{Label: "suppress", Description: "Create a signed review finding suppression"},
}

type reviewCheckMode string

const (
	reviewCheckModeAll     reviewCheckMode = "all"
	reviewCheckModeEnv     reviewCheckMode = "env"
	reviewCheckModeSecrets reviewCheckMode = "secrets"
)

type reviewOptions struct {
	BaseRef      string
	HeadRef      string
	Format       string
	JSON         bool
	Environments []string
	Mode         reviewCheckMode
	SecretScan   scanOptions
}

type reviewRunResult struct {
	Mode       reviewCheckMode          `json:"mode"`
	Env        *review.Report           `json:"env,omitempty"`
	Secrets    *scanner.Result          `json:"secrets,omitempty"`
	Suppressed reviewSuppressionSummary `json:"suppressed"`
	Passed     bool                     `json:"passed"`
}

type reviewSuppressionSummary struct {
	Env     int `json:"env"`
	Secrets int `json:"secrets"`
}

func (r *Runner) runReview(args []string) error {
	if len(args) == 0 && r.interactive {
		selected, err := r.selectCommand("Select a review command", reviewCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printReviewHelp()
		return nil
	}
	if len(args) > 0 {
		switch args[0] {
		case "run":
			return r.runReviewReport(args[1:])
		case "suppress":
			return r.runReviewSuppress(args[1:])
		}
	}
	return r.runReviewReport(args)
}

func (r *Runner) runReviewReport(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printReviewHelp()
		return nil
	}
	options, err := r.parseReviewOptions("review", args)
	if err != nil {
		return err
	}

	selectedFormat := normalizeReviewFormat(options.Format, options.JSON)
	if selectedFormat == "" {
		return fmt.Errorf("--format must be human, github, or json")
	}

	result, err := r.reviewProjectChecks(options, selectedFormat == "human")
	if err != nil {
		return err
	}

	switch selectedFormat {
	case "json":
		if err := printJSON(r.out, result); err != nil {
			return err
		}
	case "github":
		printReviewRunGitHub(r.out, result)
	default:
		printReviewRunHuman(r, result, options.SecretScan.ShowValues)
	}

	if err := reviewRunFailure(result); err != nil {
		return err
	}
	return nil
}

func (r *Runner) parseReviewOptions(name string, args []string) (reviewOptions, error) {
	fs := newFlagSet(name, r.errOut)
	baseRef := fs.String("base", "", "Base git ref; defaults to upstream, origin/main, or HEAD")
	headRef := fs.String("head", "", "Head git ref; defaults to HEAD plus local worktree changes")
	format := fs.String("format", "human", "Output format: human or github")
	jsonOut := fs.Bool("json", false, "Print review result as JSON")
	all := fs.Bool("all", false, "Run ENV and hard-coded secret checks")
	envOnly := fs.Bool("env-only", false, "Only review encrypted ENV state")
	secretsOnly := fs.Bool("secrets-only", false, "Only scan for hard-coded secrets")
	var ignores cli.Strings
	fs.Var(&ignores, "ignore", "Secret scan ignore pattern; may be repeated or comma-separated")
	level := fs.String("level", "", "Secret scan level: relaxed, standard, or strict")
	maxSize := fs.String("max-size", "", "Secret scan maximum file size in bytes")
	showValues := fs.Bool("show-values", false, "Print plaintext secret scan findings")
	var environments cli.Strings
	fs.Var(&environments, "env", "Environment to review; may be repeated or comma-separated")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("json", "all", "env-only", "secrets-only", "show-values"))
	if err != nil {
		return reviewOptions{}, err
	}
	mode, err := resolveReviewCheckMode(*all, *envOnly, *secretsOnly)
	if err != nil {
		return reviewOptions{}, err
	}
	return reviewOptions{
		BaseRef:      *baseRef,
		HeadRef:      *headRef,
		Format:       *format,
		JSON:         *jsonOut,
		Environments: environments,
		Mode:         mode,
		SecretScan: scanOptions{
			Ignores:    ignores,
			Level:      *level,
			MaxSize:    *maxSize,
			ShowValues: *showValues,
			Paths:      positionals,
		},
	}, nil
}

func resolveReviewCheckMode(all bool, envOnly bool, secretsOnly bool) (reviewCheckMode, error) {
	selected := 0
	for _, value := range []bool{all, envOnly, secretsOnly} {
		if value {
			selected++
		}
	}
	if selected > 1 {
		return "", fmt.Errorf("pass only one of --all, --env-only, or --secrets-only")
	}
	switch {
	case envOnly:
		return reviewCheckModeEnv, nil
	case secretsOnly:
		return reviewCheckModeSecrets, nil
	default:
		return reviewCheckModeAll, nil
	}
}

func (r *Runner) reviewProject(options reviewOptions, progress bool) (review.Report, error) {
	return review.Review(context.Background(), review.ReviewInput{
		Root:         ".",
		BaseRef:      options.BaseRef,
		HeadRef:      options.HeadRef,
		Environments: options.Environments,
		Format:       normalizeReviewFormat(options.Format, options.JSON),
		Status:       r.progressReporter(progress),
	})
}

func (r *Runner) reviewProjectChecks(options reviewOptions, progress bool) (reviewRunResult, error) {
	result := reviewRunResult{Mode: options.Mode}
	if options.Mode == "" {
		result.Mode = reviewCheckModeAll
	}
	if result.Mode == reviewCheckModeAll || result.Mode == reviewCheckModeEnv {
		envReport, err := r.reviewProject(options, progress)
		if err != nil {
			return reviewRunResult{}, err
		}
		repo, err := store.OpenProject(envReport.Root)
		if err != nil {
			return reviewRunResult{}, err
		}
		suppressed, err := applyReviewSuppressions(repo, &envReport)
		if err != nil {
			return reviewRunResult{}, err
		}
		result.Env = &envReport
		result.Suppressed.Env = suppressed
	}
	if result.Mode == reviewCheckModeAll || result.Mode == reviewCheckModeSecrets {
		secretResult, suppressed, err := r.scanProject(options.SecretScan, progress)
		if err != nil {
			return reviewRunResult{}, err
		}
		result.Secrets = &secretResult
		result.Suppressed.Secrets = suppressed
	}
	result.Passed = reviewRunPassed(result)
	return result, nil
}

func reviewRunPassed(result reviewRunResult) bool {
	if result.Env != nil && result.Env.HasErrors() {
		return false
	}
	return result.Secrets == nil || !result.Secrets.HasSecrets
}

func reviewRunFailure(result reviewRunResult) error {
	envErrors := 0
	if result.Env != nil {
		envErrors = len(result.Env.Errors)
	}
	secretFindings := 0
	if result.Secrets != nil {
		secretFindings = len(result.Secrets.Findings)
	}
	switch {
	case envErrors > 0 && secretFindings > 0:
		return fmt.Errorf("review failed with %d error%s and %d possible secret%s", envErrors, plural(envErrors), secretFindings, plural(secretFindings))
	case envErrors > 0:
		return fmt.Errorf("review failed with %d error%s", envErrors, plural(envErrors))
	case secretFindings > 0:
		return fmt.Errorf("review failed with %d possible secret%s", secretFindings, plural(secretFindings))
	default:
		return nil
	}
}

func printReviewRunHuman(r *Runner, result reviewRunResult, showValues bool) {
	if result.Env != nil {
		review.PrintHuman(r.out, *result.Env)
		printReviewNextSteps(r.out, *result.Env)
		if result.Suppressed.Env > 0 {
			fmt.Fprintf(r.out, "%s %d ENV finding%s suppressed.\n", warn("Suppressed:"), result.Suppressed.Env, plural(result.Suppressed.Env))
		}
	}
	if result.Secrets != nil {
		if result.Env != nil {
			fmt.Fprintln(r.out)
		}
		printReviewSecretScanHuman(r, *result.Secrets, result.Suppressed.Secrets, showValues)
	}
}

func printReviewNextSteps(out io.Writer, report review.Report) {
	if !reviewHasFindingCode(report, "plaintext_env_secret") {
		return
	}
	fmt.Fprintln(out, warn("Next:"))
	fmt.Fprintln(out, "  Run `ghostable env clean --dry-run` to review local env files, then `ghostable env clean` when safe.")
	fmt.Fprintln(out)
}

func reviewHasFindingCode(report review.Report, code string) bool {
	for _, finding := range report.Errors {
		if finding.Code == code {
			return true
		}
	}
	for _, finding := range report.Warnings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func printReviewSecretScanHuman(r *Runner, result scanner.Result, suppressed int, showValues bool) {
	fmt.Fprintln(r.out, "Hard-coded secret scan")
	if len(result.Findings) == 0 {
		fmt.Fprintf(r.out, "%s\n", success(fmt.Sprintf("No hard-coded secrets found. Scanned %d files.", result.Scanned)))
		if suppressed > 0 {
			fmt.Fprintf(r.out, "%s %d secret finding%s suppressed.\n", warn("Suppressed:"), suppressed, plural(suppressed))
		}
		return
	}
	printScanFindings(r, result, showValues)
	if suppressed > 0 {
		fmt.Fprintf(r.out, "%s %d secret finding%s suppressed.\n", warn("Suppressed:"), suppressed, plural(suppressed))
	}
}

func printReviewRunGitHub(out io.Writer, result reviewRunResult) {
	if result.Env != nil {
		review.PrintGitHub(out, *result.Env)
	}
	if result.Secrets != nil {
		printScanGitHub(out, *result.Secrets)
	}
}

func printScanGitHub(out io.Writer, result scanner.Result) {
	for _, finding := range result.Findings {
		properties := []string{"title=Ghostable secret scan"}
		if finding.Path != "" {
			properties = append(properties, "file="+escapeGitHubAnnotationProperty(finding.Path))
		}
		if finding.Line > 0 {
			properties = append(properties, fmt.Sprintf("line=%d", finding.Line))
		}
		if finding.Column > 0 {
			properties = append(properties, fmt.Sprintf("col=%d", finding.Column))
		}
		message := fmt.Sprintf("Possible hard-coded secret: %s [%s]", finding.Kind, finding.Confidence)
		if finding.Key != "" {
			message = fmt.Sprintf("Possible hard-coded secret: %s %s [%s]", finding.Kind, finding.Key, finding.Confidence)
		}
		fmt.Fprintf(out, "::error %s::%s\n", strings.Join(properties, ","), escapeGitHubAnnotationMessage(message))
	}
}

func escapeGitHubAnnotationMessage(value string) string {
	value = strings.ReplaceAll(value, "%", "%25")
	value = strings.ReplaceAll(value, "\r", "%0D")
	value = strings.ReplaceAll(value, "\n", "%0A")
	return value
}

func escapeGitHubAnnotationProperty(value string) string {
	value = escapeGitHubAnnotationMessage(value)
	value = strings.ReplaceAll(value, ":", "%3A")
	value = strings.ReplaceAll(value, ",", "%2C")
	return value
}

func (r *Runner) runReviewSuppress(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printReviewSuppressHelp()
		return nil
	}
	fs := newFlagSet("review suppress", r.errOut)
	baseRef := fs.String("base", "", "Base git ref used while selecting a finding; defaults to upstream, origin/main, or HEAD")
	headRef := fs.String("head", "", "Head git ref used while selecting a finding")
	all := fs.Bool("all", false, "Select from ENV and hard-coded secret findings")
	envOnly := fs.Bool("env-only", false, "Only select from encrypted ENV findings")
	secretsOnly := fs.Bool("secrets-only", false, "Only select from hard-coded secret findings")
	var ignores cli.Strings
	fs.Var(&ignores, "ignore", "Secret scan ignore pattern used while selecting a finding")
	level := fs.String("level", "", "Secret scan level used while selecting a finding")
	maxSize := fs.String("max-size", "", "Secret scan maximum file size in bytes while selecting a finding")
	code := fs.String("code", "", "Finding code to suppress")
	path := fs.String("path", "", "Finding path scope")
	line := fs.Int("line", 0, "Finding line scope")
	column := fs.Int("column", 0, "Finding column scope")
	kind := fs.String("kind", "", "Finding kind scope")
	key := fs.String("key", "", "Variable key scope")
	reason := fs.String("reason", "", "Suppression reason")
	expiresAt := fs.String("expires-at", "", "Expiration timestamp in RFC3339")
	expiresIn := fs.String("expires-in", "", "Expiration duration, such as 30d")
	jsonOut := fs.Bool("json", false, "Print suppression as JSON")
	var environments cli.Strings
	fs.Var(&environments, "env", "Environment to review and suppress; may be repeated or comma-separated")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("json", "all", "env-only", "secrets-only"))
	if err != nil {
		return err
	}
	mode, err := resolveReviewCheckMode(*all, *envOnly, *secretsOnly)
	if err != nil {
		return err
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	options := reviewOptions{
		BaseRef:      *baseRef,
		HeadRef:      *headRef,
		Format:       "human",
		Environments: environments,
		Mode:         mode,
		SecretScan: scanOptions{
			Ignores: ignores,
			Level:   *level,
			MaxSize: *maxSize,
			Paths:   positionals,
		},
	}
	input, err := r.resolveReviewSuppressionInput(options, *code, *path, *line, *column, *kind, *key, *reason, *expiresAt, *expiresIn)
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

func (r *Runner) printReviewHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable review [paths...] [options]")
	fmt.Fprintln(r.out, "       ghostable review run [paths...] [options]")
	fmt.Fprintln(r.out, "       ghostable review suppress [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Review encrypted ENV state and hard-coded secrets.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, reviewCommandOptions)
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Options:"))
	fmt.Fprintln(r.out, "  --base <REF>        Base git ref (defaults to upstream, origin/main, or HEAD)")
	fmt.Fprintln(r.out, "  --head <REF>        Head git ref (defaults to HEAD plus local worktree changes)")
	fmt.Fprintln(r.out, "  --env <ENV>         Environment to review; may be repeated or comma-separated")
	fmt.Fprintln(r.out, "  --all               Run ENV and hard-coded secret checks (default)")
	fmt.Fprintln(r.out, "  --env-only          Only review encrypted ENV state")
	fmt.Fprintln(r.out, "  --secrets-only      Only scan for hard-coded secrets")
	fmt.Fprintln(r.out, "  --ignore <PATTERN>  Secret scan ignore pattern")
	fmt.Fprintln(r.out, "  --level <LEVEL>     Secret scan level: relaxed, standard, or strict")
	fmt.Fprintln(r.out, "  --max-size <BYTES>  Secret scan maximum file size in bytes")
	fmt.Fprintln(r.out, "  --show-values       Print plaintext secret scan findings")
	fmt.Fprintln(r.out, "  --format <FORMAT>   Output format: human, github, or json")
	fmt.Fprintln(r.out, "  --json              Print review result as JSON")
}

func (r *Runner) printReviewSuppressHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable review suppress [paths...] [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Create a signed suppression for a review finding.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Options:"))
	fmt.Fprintln(r.out, "  --base <REF>        Base git ref used while selecting a finding")
	fmt.Fprintln(r.out, "  --head <REF>        Head git ref used while selecting a finding")
	fmt.Fprintln(r.out, "  --env <ENV>         Environment to review and suppress")
	fmt.Fprintln(r.out, "  --all               Select from ENV and hard-coded secret findings (default)")
	fmt.Fprintln(r.out, "  --env-only          Only select from encrypted ENV findings")
	fmt.Fprintln(r.out, "  --secrets-only      Only select from hard-coded secret findings")
	fmt.Fprintln(r.out, "  --ignore <PATTERN>  Secret scan ignore pattern")
	fmt.Fprintln(r.out, "  --level <LEVEL>     Secret scan level: relaxed, standard, or strict")
	fmt.Fprintln(r.out, "  --max-size <BYTES>  Secret scan maximum file size in bytes")
	fmt.Fprintln(r.out, "  --code <CODE>       Finding code to suppress")
	fmt.Fprintln(r.out, "  --path <PATH>       Finding path scope")
	fmt.Fprintln(r.out, "  --line <N>          Finding line scope")
	fmt.Fprintln(r.out, "  --column <N>        Finding column scope")
	fmt.Fprintln(r.out, "  --kind <KIND>       Finding kind scope")
	fmt.Fprintln(r.out, "  --key <KEY>         Variable key scope")
	fmt.Fprintln(r.out, "  --reason <TEXT>     Suppression reason")
	fmt.Fprintln(r.out, "  --expires-at <TIME> Expiration timestamp in RFC3339")
	fmt.Fprintln(r.out, "  --expires-in <DURATION> Expiration duration, such as 30d")
	fmt.Fprintln(r.out, "  --json                  Print suppression as JSON")
}

type reviewSuppressionFinding struct {
	Source      string
	Code        string
	Environment string
	Key         string
	Path        string
	Line        int
	Column      int
	Kind        string
	Fingerprint string
	Message     string
}

func (r *Runner) resolveReviewSuppressionInput(options reviewOptions, code string, path string, line int, column int, kind string, key string, reason string, expiresAt string, expiresIn string) (store.CreateSuppressionInput, error) {
	selectedCode := strings.TrimSpace(code)
	selectedPath := strings.TrimSpace(path)
	selectedLine := line
	selectedColumn := column
	selectedKind := strings.TrimSpace(kind)
	selectedKey := strings.TrimSpace(key)
	selectedEnv := reviewSuppressionEnvironment(options.Environments)
	selectedSource := inferReviewSuppressionSource(options.Mode, selectedCode, selectedColumn, selectedKind)
	selectedFingerprint := ""

	if selectedCode == "" && r.interactive {
		finding, ok, err := r.selectReviewFindingForSuppression(options)
		if err != nil {
			return store.CreateSuppressionInput{}, err
		}
		if ok {
			selectedSource = finding.Source
			selectedCode = finding.Code
			selectedPath = finding.Path
			selectedLine = finding.Line
			selectedColumn = finding.Column
			selectedKind = finding.Kind
			selectedKey = finding.Key
			selectedEnv = finding.Environment
			selectedFingerprint = finding.Fingerprint
		}
	}
	if selectedCode == "" {
		if selectedSource == suppressionSourceScan {
			selectedCode = scanSuppressionCode
		} else {
			return store.CreateSuppressionInput{}, fmt.Errorf("pass --code or run interactively to select a review finding")
		}
	}
	if selectedSource == suppressionSourceScan {
		if selectedPath == "" {
			return store.CreateSuppressionInput{}, fmt.Errorf("pass --path or run interactively to select a secret finding")
		}
		selectedEnv = ""
		if selectedFingerprint == "" {
			finding, ok, err := r.findScanFindingForSuppression(options.SecretScan, selectedPath, selectedLine, selectedColumn, selectedKind, selectedKey)
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
	}
	if selectedFingerprint == "" {
		selectedFingerprint = reviewSuppressionFingerprint(selectedCode, selectedEnv, selectedKey, selectedPath)
		if selectedSource == suppressionSourceScan {
			selectedFingerprint = legacyScanSuppressionFingerprint(selectedPath, selectedKey, selectedKind)
		}
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
		Source:      selectedSource,
		Code:        selectedCode,
		Environment: selectedEnv,
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

func inferReviewSuppressionSource(mode reviewCheckMode, code string, column int, kind string) string {
	if mode == reviewCheckModeSecrets || strings.TrimSpace(code) == scanSuppressionCode || column > 0 || strings.TrimSpace(kind) != "" {
		return suppressionSourceScan
	}
	return suppressionSourceReview
}

func reviewSuppressionEnvironment(environments []string) string {
	if len(environments) == 1 {
		return strings.TrimSpace(environments[0])
	}
	return ""
}

func (r *Runner) selectReviewFindingForSuppression(options reviewOptions) (reviewSuppressionFinding, bool, error) {
	result, err := r.reviewProjectChecks(options, true)
	if err != nil {
		return reviewSuppressionFinding{}, false, err
	}
	findings := reviewSuppressionFindings(result)
	if len(findings) == 0 {
		return reviewSuppressionFinding{}, false, nil
	}

	choices := make([]prompt.SelectOption, 0, len(findings))
	findingsByChoice := map[string]reviewSuppressionFinding{}
	for index, finding := range findings {
		value := fmt.Sprintf("%d", index)
		choices = append(choices, prompt.SelectOption{
			Label:       reviewFindingSuppressionLabel(finding),
			Value:       value,
			Description: reviewFindingSuppressionDescription(finding),
		})
		findingsByChoice[value] = finding
	}
	selected, err := r.prompts.SelectOptions("Select finding to suppress", choices, 0)
	if err != nil {
		return reviewSuppressionFinding{}, false, err
	}
	return findingsByChoice[selected], true, nil
}

func reviewSuppressionFindings(result reviewRunResult) []reviewSuppressionFinding {
	findings := []reviewSuppressionFinding{}
	if result.Env != nil {
		for _, finding := range result.Env.Errors {
			findings = append(findings, reviewSuppressionFindingFromReview(finding))
		}
		for _, finding := range result.Env.Warnings {
			findings = append(findings, reviewSuppressionFindingFromReview(finding))
		}
	}
	if result.Secrets != nil {
		for _, finding := range result.Secrets.Findings {
			findings = append(findings, reviewSuppressionFindingFromScan(finding))
		}
	}
	return findings
}

func reviewSuppressionFindingFromReview(finding review.Finding) reviewSuppressionFinding {
	return reviewSuppressionFinding{
		Source:      suppressionSourceReview,
		Code:        finding.Code,
		Environment: finding.Environment,
		Key:         finding.Key,
		Path:        finding.Path,
		Line:        finding.Line,
		Message:     finding.Message,
	}
}

func reviewSuppressionFindingFromScan(finding scanner.Finding) reviewSuppressionFinding {
	message := fmt.Sprintf("Possible hard-coded secret: %s [%s]", finding.Kind, finding.Confidence)
	if finding.Key != "" {
		message = fmt.Sprintf("Possible hard-coded secret: %s %s [%s]", finding.Kind, finding.Key, finding.Confidence)
	}
	return reviewSuppressionFinding{
		Source:      suppressionSourceScan,
		Code:        scanSuppressionCode,
		Key:         finding.Key,
		Path:        finding.Path,
		Line:        finding.Line,
		Column:      finding.Column,
		Kind:        finding.Kind,
		Fingerprint: scanSuppressionFingerprintForFinding(finding),
		Message:     message,
	}
}

func reviewFindingSuppressionLabel(finding reviewSuppressionFinding) string {
	return suppressionPromptLabel(finding.Source, finding.Code)
}

func reviewFindingSuppressionDescription(finding reviewSuppressionFinding) string {
	scope := joinPromptParts(finding.Environment, finding.Key)
	if scope == "" {
		scope = "project"
	}
	return suppressionPromptDescription(scope, suppressionPromptLocation(finding.Path, finding.Line, finding.Column), finding.Message)
}

func applyReviewSuppressions(repo store.Repository, report *review.Report) (int, error) {
	suppressions, err := activeSuppressionRecords(repo)
	if err != nil {
		return 0, err
	}
	errors, suppressedErrors := filterReviewFindings(report.Errors, suppressions)
	warnings, suppressedWarnings := filterReviewFindings(report.Warnings, suppressions)
	report.Errors = errors
	report.Warnings = warnings
	report.Passed = len(report.Errors) == 0
	return suppressedErrors + suppressedWarnings, nil
}

func filterReviewFindings(findings []review.Finding, suppressions []domain.SuppressionRecord) ([]review.Finding, int) {
	unsuppressed := []review.Finding{}
	suppressed := 0
	for _, finding := range findings {
		if _, ok := matchingSuppressionTarget(reviewSuppressionTarget(finding), suppressions); ok {
			suppressed++
			continue
		}
		unsuppressed = append(unsuppressed, finding)
	}
	return unsuppressed, suppressed
}

func reviewSuppressionTarget(finding review.Finding) suppressionTarget {
	return suppressionTarget{
		Source:      suppressionSourceReview,
		Code:        finding.Code,
		Environment: finding.Environment,
		Key:         finding.Key,
		Path:        finding.Path,
		Line:        finding.Line,
		Fingerprint: reviewSuppressionFingerprint(
			finding.Code,
			finding.Environment,
			finding.Key,
			finding.Path,
		),
	}
}

func normalizeReviewFormat(format string, jsonOut bool) string {
	if jsonOut {
		return "json"
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "human", "text":
		return "human"
	case "github":
		return "github"
	case "json":
		return "json"
	default:
		return ""
	}
}
