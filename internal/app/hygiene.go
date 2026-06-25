package app

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/domain"
	hygienepolicy "github.com/ghostable-dev/beta/internal/hygiene"
	"github.com/ghostable-dev/beta/internal/prompt"
	"github.com/ghostable-dev/beta/internal/review"
	"github.com/ghostable-dev/beta/internal/store"
)

const hygieneReportSchema = "ghostable.hygiene-report.v1"

var hygieneCommandOptions = []commandOption{
	{Label: "report", Description: "Report rotation, stale, and optional unused-variable hygiene"},
	{Label: "rotation", Description: "Manage variable rotation hygiene rules"},
	{Label: "suppress", Description: "Create a signed hygiene finding suppression"},
	{Label: "rotate", Description: "Rotate an environment encryption key"},
}

type hygieneReport struct {
	Schema             string                   `json:"schema"`
	Root               string                   `json:"root"`
	GeneratedAt        string                   `json:"generatedAt"`
	Environments       []string                 `json:"environments"`
	StaleAfterDays     int                      `json:"staleAfterDays"`
	RotationAfterDays  int                      `json:"rotationAfterDays"`
	Summary            hygieneSummary           `json:"summary"`
	EnvironmentKeys    []hygieneEnvironmentKey  `json:"environmentKeys"`
	Variables          []hygieneVariableAge     `json:"variables"`
	Findings           []hygieneFinding         `json:"findings"`
	SuppressedFindings []hygieneFinding         `json:"suppressedFindings,omitempty"`
	Suppressions       []store.SuppressionEntry `json:"suppressions,omitempty"`
}

type hygieneSummary struct {
	Variables           int `json:"variables"`
	UnusedVariables     int `json:"unusedVariables"`
	StaleVariables      int `json:"staleVariables"`
	RotationDue         int `json:"rotationDue"`
	SuppressedFindings  int `json:"suppressedFindings"`
	InvalidSuppressions int `json:"invalidSuppressions"`
}

type hygieneEnvironmentKey struct {
	Environment string `json:"environment"`
	Version     int    `json:"version"`
	Fingerprint string `json:"fingerprint"`
	UpdatedAt   string `json:"updatedAt"`
	AgeDays     int    `json:"ageDays"`
}

type hygieneVariableAge struct {
	Environment string `json:"environment"`
	Key         string `json:"key"`
	UpdatedAt   string `json:"updatedAt"`
	AgeDays     int    `json:"ageDays"`
}

type hygieneFinding struct {
	Severity      string `json:"severity"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Environment   string `json:"environment,omitempty"`
	Key           string `json:"key,omitempty"`
	AgeDays       int    `json:"ageDays,omitempty"`
	ThresholdDays int    `json:"thresholdDays,omitempty"`
	PolicySource  string `json:"policySource,omitempty"`
	Suppressed    bool   `json:"suppressed,omitempty"`
	SuppressionID string `json:"suppressionId,omitempty"`
	Path          string `json:"path,omitempty"`
}

func (r *Runner) runHygiene(args []string) error {
	if len(args) == 0 {
		if !r.interactive {
			return r.runHygieneReport(args)
		}
		selected, err := r.selectCommand("Select a hygiene command", hygieneCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if isHelpArg(args[0]) {
		r.printHygieneHelp()
		return nil
	}

	switch args[0] {
	case "report":
		return r.runHygieneReport(args[1:])
	case "rotation":
		return r.runHygieneRotation(args[1:])
	case "suppress":
		return r.runHygieneSuppress(args[1:])
	case "rotate":
		return r.runHygieneRotate(args[1:])
	default:
		return r.runHygieneReport(args)
	}
}

func (r *Runner) printHygieneHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable hygiene [report|rotation|suppress|rotate] [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, hygieneCommandOptions)
}

func (r *Runner) runHygieneReport(args []string) error {
	fs := newFlagSet("hygiene report", r.errOut)
	staleAfterRaw := fs.String("stale-after", "0", "Legacy variable age threshold, such as 90d; disabled by default")
	rotationAfterRaw := fs.String("rotation-after", "180d", "Environment key rotation threshold, such as 180d")
	checkUnused := fs.Bool("unused", false, "Report stored variables not referenced by scanned code")
	includeSuppressed := fs.Bool("include-suppressed", false, "Include suppressed findings in human or SARIF output")
	jsonOut := fs.Bool("json", false, "Print hygiene report as JSON")
	sarifOut := fs.Bool("sarif", false, "Print hygiene report as SARIF")
	var environments cli.Strings
	fs.Var(&environments, "env", "Environment to check; may be repeated or comma-separated")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("unused", "include-suppressed", "json", "sarif")); err != nil {
		return err
	}
	if *jsonOut && *sarifOut {
		return fmt.Errorf("pass --json or --sarif, not both")
	}
	staleAfter, err := parseAgeThreshold(*staleAfterRaw)
	if err != nil {
		return err
	}
	rotationAfter, err := parseAgeThreshold(*rotationAfterRaw)
	if err != nil {
		return err
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	envs, err := resolveHygieneEnvironments(repo, environments)
	if err != nil {
		return err
	}
	report, err := buildHygieneReport(repo, envs, staleAfter, rotationAfter, *checkUnused, *includeSuppressed)
	if err != nil {
		return err
	}

	switch {
	case *jsonOut:
		return printJSON(r.out, report)
	case *sarifOut:
		return printJSON(r.out, hygieneSARIF(report, *includeSuppressed))
	default:
		printHygieneReport(r, report, *includeSuppressed)
		return nil
	}
}

func (r *Runner) runHygieneSuppress(args []string) error {
	fs := newFlagSet("hygiene suppress", r.errOut)
	code := fs.String("code", "", "Finding code to suppress")
	env := fs.String("env", "", "Environment scope")
	key := fs.String("key", "", "Variable key scope")
	reason := fs.String("reason", "", "Suppression reason")
	expiresAt := fs.String("expires-at", "", "Expiration timestamp in RFC3339")
	expiresIn := fs.String("expires-in", "", "Expiration duration, such as 30d")
	jsonOut := fs.Bool("json", false, "Print suppression as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	input, err := r.resolveHygieneSuppressionInput(repo, *code, *env, *key, *reason, *expiresAt, *expiresIn)
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

func (r *Runner) resolveHygieneSuppressionInput(repo store.Repository, code string, env string, key string, reason string, expiresAt string, expiresIn string) (store.CreateSuppressionInput, error) {
	if strings.TrimSpace(expiresAt) != "" && strings.TrimSpace(expiresIn) != "" {
		return store.CreateSuppressionInput{}, fmt.Errorf("pass --expires-at or --expires-in, not both")
	}

	selectedCode := strings.TrimSpace(code)
	selectedEnv := strings.TrimSpace(env)
	selectedKey := strings.TrimSpace(key)
	selectedFromFinding := false

	if selectedCode == "" && r.interactive {
		finding, ok, err := r.selectHygieneFindingForSuppression(repo)
		if err != nil {
			return store.CreateSuppressionInput{}, err
		}
		if ok {
			selectedCode = finding.Code
			selectedEnv = finding.Environment
			selectedKey = finding.Key
			selectedFromFinding = true
		}
	}

	var err error
	selectedCode, err = r.selectSuppressionCode(selectedCode)
	if err != nil {
		return store.CreateSuppressionInput{}, err
	}

	if !selectedFromFinding {
		selectedEnv, selectedKey, err = r.selectSuppressionScope(repo, selectedEnv, selectedKey)
		if err != nil {
			return store.CreateSuppressionInput{}, err
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
		Source:      suppressionSourceHygiene,
		Code:        selectedCode,
		Environment: selectedEnv,
		Key:         selectedKey,
		Reason:      selectedReason,
		ExpiresAt:   expiration,
	}, nil
}

func (r *Runner) selectHygieneFindingForSuppression(repo store.Repository) (hygieneFinding, bool, error) {
	envs, err := resolveHygieneEnvironments(repo, nil)
	if err != nil {
		return hygieneFinding{}, false, err
	}
	report, err := buildHygieneReport(repo, envs, 0, 180*24*time.Hour, false, false)
	if err != nil {
		return hygieneFinding{}, false, err
	}
	if len(report.Findings) == 0 {
		return hygieneFinding{}, false, nil
	}

	choices := make([]prompt.SelectOption, 0, len(report.Findings)+1)
	findingsByChoice := map[string]hygieneFinding{}
	for index, finding := range report.Findings {
		value := fmt.Sprintf("%d", index)
		choices = append(choices, prompt.SelectOption{
			Label:       hygieneFindingSuppressionLabel(finding),
			Value:       value,
			Description: hygieneFindingSuppressionDescription(finding),
		})
		findingsByChoice[value] = finding
	}
	custom := "Custom suppression"
	choices = append(choices, prompt.SelectOption{Label: custom, Value: custom})

	selected, err := r.prompts.SelectOptions("Select finding to suppress", choices, 0)
	if err != nil {
		return hygieneFinding{}, false, err
	}
	if selected == custom {
		return hygieneFinding{}, false, nil
	}
	return findingsByChoice[selected], true, nil
}

func hygieneFindingSuppressionLabel(finding hygieneFinding) string {
	return suppressionPromptLabel(finding.Code)
}

func hygieneFindingSuppressionDescription(finding hygieneFinding) string {
	scope := finding.Environment
	if finding.Key != "" {
		scope = strings.TrimSpace(scope + " " + finding.Key)
	}
	if scope == "" {
		scope = "project"
	}
	return suppressionPromptDescription(scope, suppressionPromptLocation(finding.Path, 0, 0), finding.Message)
}

func (r *Runner) selectSuppressionCode(provided string) (string, error) {
	if strings.TrimSpace(provided) != "" {
		return strings.TrimSpace(provided), nil
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --code")
	}

	choices := []string{
		"rotation_due",
		"unused_variable",
		"stale_variable",
		"environment_key_rotation_due",
		"Custom code",
	}
	selected, err := r.prompts.Select("Suppression code", choices, 0)
	if err != nil {
		return "", err
	}
	if selected == "Custom code" {
		return r.ask("Suppression code", "", "", "code")
	}
	return selected, nil
}

func (r *Runner) selectSuppressionScope(repo store.Repository, env string, key string) (string, string, error) {
	env = strings.TrimSpace(env)
	key = strings.TrimSpace(key)
	if env != "" || key != "" {
		if key == "" {
			return env, key, nil
		}
		formattedKey, _, err := formatManualVariableKey(key)
		if err != nil {
			return "", "", err
		}
		return env, formattedKey, nil
	}
	if !r.interactive {
		return env, key, nil
	}

	scope, err := r.prompts.Select("Suppression scope", []string{"Project", "Environment", "Variable"}, 0)
	if err != nil {
		return "", "", err
	}
	switch scope {
	case "Project":
		return "", "", nil
	case "Environment":
		selectedEnv, err := r.selectEnvironment(repo, "")
		return selectedEnv, "", err
	case "Variable":
		selectedEnv, err := r.selectEnvironment(repo, "")
		if err != nil {
			return "", "", err
		}
		selectedKey, err := r.selectSuppressionKey(repo, selectedEnv)
		if err != nil {
			return "", "", err
		}
		return selectedEnv, selectedKey, nil
	default:
		return "", "", fmt.Errorf("unknown suppression scope %q", scope)
	}
}

func (r *Runner) selectSuppressionKey(repo store.Repository, env string) (string, error) {
	keys, err := repo.ReadKeyMetadataKeys(env)
	if err != nil {
		return "", err
	}
	if len(keys) == 0 {
		return r.askManualVariableKey()
	}
	choices := append(keys, "Custom variable key")
	selected, err := r.prompts.Select("Select a variable", choices, 0)
	if err != nil {
		return "", err
	}
	if selected == "Custom variable key" {
		return r.askManualVariableKey()
	}
	return selected, nil
}

func (r *Runner) resolveSuppressionExpiration(expiresAt string, expiresIn string) (string, error) {
	expiration := strings.TrimSpace(expiresAt)
	if expiration != "" {
		return expiration, nil
	}
	if strings.TrimSpace(expiresIn) != "" {
		duration, err := parseAgeThreshold(expiresIn)
		if err != nil {
			return "", err
		}
		return time.Now().UTC().Add(duration).Format(time.RFC3339Nano), nil
	}
	if !r.interactive {
		return "", nil
	}

	shouldExpire, err := r.prompts.Confirm("Expire this suppression?", false)
	if err != nil {
		return "", err
	}
	if !shouldExpire {
		return "", nil
	}
	days, err := r.ask("Expires in days", "", "", "expires-in")
	if err != nil {
		return "", err
	}
	parsedDays, err := strconv.Atoi(strings.TrimSpace(days))
	if err != nil || parsedDays <= 0 {
		return "", fmt.Errorf("suppression expiration must be a whole number of days")
	}
	return time.Now().UTC().Add(time.Duration(parsedDays) * 24 * time.Hour).Format(time.RFC3339Nano), nil
}

func (r *Runner) runHygieneRotate(args []string) error {
	fs := newFlagSet("hygiene rotate", r.errOut)
	env := fs.String("env", "", "Environment name")
	reason := fs.String("reason", "", "Reason stored in signed local events")
	dryRun := fs.Bool("dry-run", false, "Show the current environment key without rotating")
	jsonOut := fs.Bool("json", false, "Print rotation result as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("dry-run", "json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	selected, err := r.selectEnvironment(repo, *env)
	if err != nil {
		return err
	}
	if *dryRun {
		metadata, err := repo.ReadEnvironmentKeyMetadata(selected)
		if err != nil {
			return err
		}
		if *jsonOut {
			return printJSON(r.out, metadata)
		}
		fmt.Fprintf(r.out, "%s key version %d (%s).\n", selected, metadata.Version, metadata.Fingerprint)
		return nil
	}

	result, err := repo.RotateEnvironmentKey(selected, *reason)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Rotated %s environment key from version %d to %d.", selected, result.PreviousVersion, result.NextVersion)))
	return nil
}

func buildHygieneReport(repo store.Repository, envs []string, staleAfter time.Duration, rotationAfter time.Duration, checkUnused bool, includeSuppressed bool) (hygieneReport, error) {
	now := time.Now().UTC()
	referencedKeys := map[string]bool{}
	if checkUnused {
		references, err := review.ScanReferences(review.ReferenceScanInput{
			Root:    repo.Root,
			Ignores: repo.Manifest.ScanIgnores,
		})
		if err != nil {
			return hygieneReport{}, err
		}
		for _, reference := range references {
			referencedKeys[reference.Key] = true
		}
	}
	suppressions, err := repo.Suppressions(now)
	if err != nil {
		return hygieneReport{}, err
	}
	policy, err := hygienepolicy.LoadPolicy(repo.Root)
	if err != nil {
		return hygieneReport{}, err
	}

	report := hygieneReport{
		Schema:            hygieneReportSchema,
		Root:              repo.Root,
		GeneratedAt:       now.Format(time.RFC3339Nano),
		Environments:      envs,
		StaleAfterDays:    durationDays(staleAfter),
		RotationAfterDays: durationDays(rotationAfter),
		Suppressions:      suppressions,
	}

	for _, entry := range suppressions {
		if entry.ValidSignature {
			continue
		}
		report.Summary.InvalidSuppressions++
		report.Findings = append(report.Findings, hygieneFinding{
			Severity: "error",
			Code:     "invalid_suppression",
			Message:  fmt.Sprintf("Suppression %s is invalid: %s", entry.File, entry.SignatureError),
			Path:     entry.File,
		})
	}

	for _, env := range envs {
		keyMetadata, err := repo.ReadEnvironmentKeyMetadata(env)
		if err != nil {
			return hygieneReport{}, err
		}
		keyAge := ageDays(keyMetadata.UpdatedAt, now)
		report.EnvironmentKeys = append(report.EnvironmentKeys, hygieneEnvironmentKey{
			Environment: keyMetadata.Environment,
			Version:     keyMetadata.Version,
			Fingerprint: keyMetadata.Fingerprint,
			UpdatedAt:   keyMetadata.UpdatedAt,
			AgeDays:     keyAge,
		})
		if rotationAfter > 0 && ageAtLeast(keyMetadata.UpdatedAt, now, rotationAfter) {
			report.Summary.RotationDue++
			report.Findings = append(report.Findings, hygieneFinding{
				Severity:      "warning",
				Code:          "environment_key_rotation_due",
				Environment:   env,
				AgeDays:       keyAge,
				ThresholdDays: durationDays(rotationAfter),
				Message:       fmt.Sprintf("%s environment key is %d days old; rotate it with `ghostable hygiene rotate --env %s`", env, keyAge, env),
			})
		}

		metadata, err := repo.ReadVariableMetadata(env)
		if err != nil {
			return hygieneReport{}, err
		}
		for _, variable := range metadata {
			variableAge := ageDays(variable.UpdatedAt, now)
			report.Summary.Variables++
			report.Variables = append(report.Variables, hygieneVariableAge{
				Environment: env,
				Key:         variable.Key,
				UpdatedAt:   variable.UpdatedAt,
				AgeDays:     variableAge,
			})
			if checkUnused && !referencedKeys[variable.Key] {
				report.Summary.UnusedVariables++
				report.Findings = append(report.Findings, hygieneFinding{
					Severity:    "warning",
					Code:        "unused_variable",
					Environment: env,
					Key:         variable.Key,
					Message:     fmt.Sprintf("%s exists in %s but is not referenced by scanned code", variable.Key, env),
				})
			}
			if staleAfter > 0 && ageAtLeast(variable.UpdatedAt, now, staleAfter) {
				report.Summary.StaleVariables++
				report.Findings = append(report.Findings, hygieneFinding{
					Severity:      "warning",
					Code:          "stale_variable",
					Environment:   env,
					Key:           variable.Key,
					AgeDays:       variableAge,
					ThresholdDays: durationDays(staleAfter),
					Message:       fmt.Sprintf("%s in %s is %d days old", variable.Key, env, variableAge),
				})
			}
			resolvedRule, ok := hygienepolicy.ResolveRotationRule(policy, env, variable.Key)
			if !ok {
				continue
			}
			threshold, err := parseRotationPolicyThreshold(env, variable.Key, resolvedRule.Rule.RotationAfterDays)
			if err != nil {
				return hygieneReport{}, err
			}
			if threshold > 0 && ageAtLeast(variable.UpdatedAt, now, threshold) {
				report.Summary.RotationDue++
				report.Findings = append(report.Findings, hygieneFinding{
					Severity:      "warning",
					Code:          "rotation_due",
					Environment:   env,
					Key:           variable.Key,
					AgeDays:       variableAge,
					ThresholdDays: durationDays(threshold),
					PolicySource:  resolvedRule.Source,
					Path:          hygienepolicy.DefaultPolicyPath,
					Message:       fmt.Sprintf("%s in %s is %d days old; rotation policy is %d days", variable.Key, env, variableAge, resolvedRule.Rule.RotationAfterDays),
				})
			}
		}
	}

	sortHygieneReport(&report)
	applyHygieneSuppressions(&report, suppressions, includeSuppressed)
	return report, nil
}

func parseRotationPolicyThreshold(env string, key string, days int) (time.Duration, error) {
	if days <= 0 {
		return 0, fmt.Errorf("rotation policy for %s in %s must have rotationAfterDays greater than zero", key, env)
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

func resolveHygieneEnvironments(repo store.Repository, requested []string) ([]string, error) {
	if len(requested) > 0 {
		return resolveSelectedEnvironments(repo, requested)
	}

	names := []string{}
	seen := map[string]bool{}
	for _, name := range repo.Manifest.AuditEnvs {
		if _, ok := repo.Manifest.Environments[name]; !ok || seen[name] {
			continue
		}
		names = append(names, name)
		seen[name] = true
	}
	if len(names) > 0 {
		sort.Strings(names)
		return names, nil
	}
	return resolveSelectedEnvironments(repo, nil)
}

func applyHygieneSuppressions(report *hygieneReport, suppressions []store.SuppressionEntry, includeSuppressed bool) {
	active := []domain.SuppressionRecord{}
	for _, entry := range suppressions {
		if entry.ValidSignature && !entry.Expired {
			active = append(active, entry.Suppression)
		}
	}
	unsuppressed := []hygieneFinding{}
	for _, finding := range report.Findings {
		suppression, ok := matchingSuppression(finding, active)
		if !ok {
			unsuppressed = append(unsuppressed, finding)
			continue
		}
		finding.Suppressed = true
		finding.SuppressionID = suppression.ID
		report.SuppressedFindings = append(report.SuppressedFindings, finding)
		report.Summary.SuppressedFindings++
		if includeSuppressed {
			unsuppressed = append(unsuppressed, finding)
		}
	}
	report.Findings = unsuppressed
}

func matchingSuppression(finding hygieneFinding, suppressions []domain.SuppressionRecord) (domain.SuppressionRecord, bool) {
	for _, suppression := range suppressions {
		if !suppressionMatchesSource(suppression, suppressionSourceHygiene) {
			continue
		}
		if suppression.Code != finding.Code {
			continue
		}
		if suppression.Environment != "" && suppression.Environment != finding.Environment {
			continue
		}
		if suppression.Key != "" && suppression.Key != finding.Key {
			continue
		}
		return suppression, true
	}
	return domain.SuppressionRecord{}, false
}

func sortHygieneReport(report *hygieneReport) {
	sort.Slice(report.EnvironmentKeys, func(i, j int) bool {
		return report.EnvironmentKeys[i].Environment < report.EnvironmentKeys[j].Environment
	})
	sort.Slice(report.Variables, func(i, j int) bool {
		if report.Variables[i].Environment == report.Variables[j].Environment {
			return report.Variables[i].Key < report.Variables[j].Key
		}
		return report.Variables[i].Environment < report.Variables[j].Environment
	})
	sort.Slice(report.Findings, func(i, j int) bool {
		if report.Findings[i].Environment == report.Findings[j].Environment {
			if report.Findings[i].Key == report.Findings[j].Key {
				return report.Findings[i].Code < report.Findings[j].Code
			}
			return report.Findings[i].Key < report.Findings[j].Key
		}
		return report.Findings[i].Environment < report.Findings[j].Environment
	})
}

func printHygieneReport(r *Runner, report hygieneReport, includeSuppressed bool) {
	if len(report.Findings) == 0 {
		fmt.Fprintln(r.out, success(fmt.Sprintf("Hygiene passed for %d variable%s.", report.Summary.Variables, plural(report.Summary.Variables))))
		if report.Summary.SuppressedFindings > 0 {
			fmt.Fprintf(r.out, "%s %d finding%s suppressed.\n", warn("Suppressed:"), report.Summary.SuppressedFindings, plural(report.Summary.SuppressedFindings))
		}
		return
	}
	fmt.Fprintln(r.out, "Ghostable hygiene")
	for _, finding := range report.Findings {
		if finding.Suppressed && !includeSuppressed {
			continue
		}
		status := warn(finding.Severity)
		if finding.Severity == "error" {
			status = danger(finding.Severity)
		}
		scope := finding.Environment
		if finding.Key != "" {
			scope = strings.TrimSpace(scope + " " + finding.Key)
		}
		if scope == "" {
			scope = "-"
		}
		suffix := ""
		if finding.Suppressed {
			suffix = " (suppressed by " + finding.SuppressionID + ")"
		}
		fmt.Fprintf(r.out, "%s %s %s: %s%s\n", status, finding.Code, scope, finding.Message, suffix)
	}
	if report.Summary.SuppressedFindings > 0 && !includeSuppressed {
		fmt.Fprintf(r.out, "%s %d finding%s suppressed. Use --include-suppressed to show them.\n", warn("Suppressed:"), report.Summary.SuppressedFindings, plural(report.Summary.SuppressedFindings))
	}
}

func parseAgeThreshold(value string) (time.Duration, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return 0, nil
	}
	if value == "0" {
		return 0, nil
	}
	if strings.HasSuffix(value, "d") {
		number := strings.TrimSuffix(value, "d")
		days, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q", value)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	if _, err := strconv.Atoi(value); err == nil {
		value += "d"
		return parseAgeThreshold(value)
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	return duration, nil
}

func durationDays(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	return int(math.Ceil(duration.Hours() / 24))
}

func ageDays(timestamp string, now time.Time) int {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(timestamp))
	if err != nil {
		return 0
	}
	if now.Before(parsed) {
		return 0
	}
	return int(now.Sub(parsed).Hours() / 24)
}

func ageAtLeast(timestamp string, now time.Time, threshold time.Duration) bool {
	if threshold <= 0 {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(timestamp))
	if err != nil {
		return false
	}
	if now.Before(parsed) {
		return false
	}
	return now.Sub(parsed) >= threshold
}
