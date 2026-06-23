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
	"github.com/ghostable-dev/beta/internal/review"
	"github.com/ghostable-dev/beta/internal/store"
)

const hygieneReportSchema = "ghostable.hygiene-report.v1"

var hygieneCommandOptions = []commandOption{
	{Label: "report", Description: "Report stale, unused, and rotation-due secrets"},
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
	Suppressed    bool   `json:"suppressed,omitempty"`
	SuppressionID string `json:"suppressionId,omitempty"`
	Path          string `json:"path,omitempty"`
}

func (r *Runner) runHygiene(args []string) error {
	if len(args) == 0 {
		return r.runHygieneReport(args)
	}
	if isHelpArg(args[0]) {
		r.printHygieneHelp()
		return nil
	}

	switch args[0] {
	case "report":
		return r.runHygieneReport(args[1:])
	case "suppress":
		return r.runHygieneSuppress(args[1:])
	case "rotate":
		return r.runHygieneRotate(args[1:])
	default:
		return r.runHygieneReport(args)
	}
}

func (r *Runner) printHygieneHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable hygiene [report|suppress|rotate] [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, hygieneCommandOptions)
}

func (r *Runner) runHygieneReport(args []string) error {
	fs := newFlagSet("hygiene report", r.errOut)
	staleAfterRaw := fs.String("stale-after", "90d", "Variable age threshold, such as 90d or 2160h")
	rotationAfterRaw := fs.String("rotation-after", "180d", "Environment key rotation threshold, such as 180d")
	includeSuppressed := fs.Bool("include-suppressed", false, "Include suppressed findings in human or SARIF output")
	jsonOut := fs.Bool("json", false, "Print hygiene report as JSON")
	sarifOut := fs.Bool("sarif", false, "Print hygiene report as SARIF")
	var environments cli.Strings
	fs.Var(&environments, "env", "Environment to check; may be repeated or comma-separated")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("include-suppressed", "json", "sarif")); err != nil {
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
	envs, err := resolveSelectedEnvironments(repo, environments)
	if err != nil {
		return err
	}
	report, err := buildHygieneReport(repo, envs, staleAfter, rotationAfter, *includeSuppressed)
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
	if *expiresAt != "" && *expiresIn != "" {
		return fmt.Errorf("pass --expires-at or --expires-in, not both")
	}
	expiration := strings.TrimSpace(*expiresAt)
	if expiration == "" && strings.TrimSpace(*expiresIn) != "" {
		duration, err := parseAgeThreshold(*expiresIn)
		if err != nil {
			return err
		}
		expiration = time.Now().UTC().Add(duration).Format(time.RFC3339Nano)
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	result, err := repo.CreateSuppression(store.CreateSuppressionInput{
		Code:        *code,
		Environment: *env,
		Key:         *key,
		Reason:      *reason,
		ExpiresAt:   expiration,
	})
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

func buildHygieneReport(repo store.Repository, envs []string, staleAfter time.Duration, rotationAfter time.Duration, includeSuppressed bool) (hygieneReport, error) {
	now := time.Now().UTC()
	references, err := review.ScanReferences(review.ReferenceScanInput{
		Root:    repo.Root,
		Ignores: repo.Manifest.ScanIgnores,
	})
	if err != nil {
		return hygieneReport{}, err
	}
	referencedKeys := map[string]bool{}
	for _, reference := range references {
		referencedKeys[reference.Key] = true
	}
	suppressions, err := repo.Suppressions(now)
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
			if !referencedKeys[variable.Key] {
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
		}
	}

	sortHygieneReport(&report)
	applyHygieneSuppressions(&report, suppressions, includeSuppressed)
	return report, nil
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
