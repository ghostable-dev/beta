package app

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/dotenv"
	"github.com/ghostable-dev/beta/internal/store"
)

var envCommandOptions = []commandOption{
	{Label: "list", Description: "Show environments"},
	{Label: "create", Description: "Add a new environment"},
	{Label: "push", Description: "Store values from an env file"},
	{Label: "sync", Description: "Push and remove missing keys"},
	{Label: "pull", Description: "Write stored values to an env file"},
	{Label: "clean", Description: "Remove local env files from the project root"},
	{Label: "run", Description: "Run a command with decrypted environment values"},
	{Label: "diff", Description: "Compare an env file or environment"},
	{Label: "history", Description: "Show signed change history"},
	{Label: "rename", Description: "Rename an environment"},
	{Label: "delete", Description: "Remove an environment"},
}

var envLayoutCommandOptions = []commandOption{
	{Label: "generate", Description: "Create a key layout from stored values"},
}

var envFileCommandOptions = []commandOption{
	{Label: "save", Description: "Write env file content to disk"},
}

var envDiffModeOptions = []commandOption{
	{Label: "two environments", Value: "environment", Description: "Compare stored values between environments"},
	{Label: "env file", Value: "file", Description: "Compare a local env file to stored values"},
}

var seedModeOptions = []commandOption{
	{Label: "non-sensitive", Value: "insensitive", Description: "Copy keys and values that do not look secret"},
	{Label: "all", Description: "Copy every key and value"},
	{Label: "keys only", Value: "keys-only", Description: "No values"},
}

type envListRow struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Variables   int    `json:"variables"`
	LastUpdated string `json:"lastUpdated,omitempty"`
}

type environmentSeedInput struct {
	target     string
	sourceEnv  string
	sourceFile string
	mode       string
	reason     string
}

type environmentSeedResult struct {
	pushResult *store.PushResult
	seededKeys int
}

func emptyEnvironmentSeedResult() environmentSeedResult {
	return environmentSeedResult{seededKeys: -1}
}

func (result environmentSeedResult) hasSeed() bool {
	return result.pushResult != nil || result.seededKeys >= 0
}

func (r *Runner) runEnv(args []string) error {
	if len(args) == 0 {
		if !r.interactive {
			r.printEnvHelp()
			return nil
		}
		selected, err := r.selectCommand("Select an environment command", envCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if isHelpArg(args[0]) {
		r.printEnvHelp()
		return nil
	}

	switch args[0] {
	case "list":
		return r.runEnvList(args[1:])
	case "create":
		return r.runEnvCreate(args[1:])
	case "delete":
		return r.runEnvDelete(args[1:])
	case "push":
		return r.runEnvPush(args[1:], false)
	case "sync":
		return r.runEnvPush(args[1:], true)
	case "pull":
		return r.runEnvPull(args[1:])
	case "clean":
		return r.runEnvClean(args[1:])
	case "run":
		return r.runEnvRun(args[1:])
	case "diff":
		return r.runEnvDiff(args[1:])
	case "history":
		return r.runEnvHistory(args[1:])
	case "duplicate":
		return r.runEnvDuplicate(args[1:])
	case "rename":
		return r.runEnvRename(args[1:])
	case "layout":
		return r.runEnvLayout(args[1:])
	case "file":
		return r.runEnvFile(args[1:])
	default:
		return unknownEnvCommandError(args[0])
	}
}

func unknownEnvCommandError(command string) error {
	suggestions := map[string]string{
		"copy":     "Run `ghostable env duplicate <source> <target>` to copy an environment.",
		"validate": "Run `ghostable validate --env <env>` to check values against schema rules.",
	}
	if suggestion, ok := suggestions[command]; ok {
		return fmt.Errorf("unknown env command %q. %s", command, suggestion)
	}
	return fmt.Errorf("unknown env command %q. Run `ghostable env --help` for available commands", command)
}

func (r *Runner) printEnvHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable env <command> [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, envCommandOptions)
}

func (r *Runner) runEnvList(args []string) error {
	fs := newFlagSet("env list", r.errOut)
	jsonOut := fs.Bool("json", false, "Print local Ghostable environments as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	envs := repo.Environments()
	rows, err := r.envListRows(repo, envs)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, rows)
	}
	printEnvListRows(r.out, rows)
	return nil
}

func (r *Runner) envListRows(repo store.Repository, envs []domain.Environment) ([]envListRow, error) {
	rows := make([]envListRow, 0, len(envs))
	for _, env := range envs {
		variables, err := repo.ReadVariables(env.Name)
		if err != nil {
			return nil, err
		}
		row := envListRow{
			Name:      env.Name,
			Type:      env.Type,
			Variables: len(variables),
		}
		row.LastUpdated = latestVariableUpdate(variables)
		rows = append(rows, row)
	}
	return rows, nil
}

func latestVariableUpdate(variables map[string]domain.Variable) string {
	var latest time.Time
	latestRaw := ""
	latestParsed := false
	for _, variable := range variables {
		updatedAt := strings.TrimSpace(variable.UpdatedAt)
		if updatedAt == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			if !latestParsed && updatedAt > latestRaw {
				latestRaw = updatedAt
			}
			continue
		}
		if !latestParsed || parsed.After(latest) {
			latest = parsed
			latestRaw = updatedAt
			latestParsed = true
		}
	}
	return latestRaw
}

func envCreateSummary(result store.EnvironmentResult, suffix string) string {
	if result.Created {
		return fmt.Sprintf("Created %q environment%s.", result.Environment.Name, suffix)
	}
	return fmt.Sprintf("%s already exists%s.", result.Environment.Name, suffix)
}

func variableCountText(count int) string {
	if count == 1 {
		return "1 variable"
	}
	return fmt.Sprintf("%d variables", count)
}

func keyCountText(count int) string {
	if count == 1 {
		return "1 key"
	}
	return fmt.Sprintf("%d keys", count)
}

func printEnvListRows(out io.Writer, rows []envListRow) {
	if len(rows) == 0 {
		fmt.Fprintln(out, warn("No environments found."))
		return
	}

	nameWidth := len("Name")
	typeWidth := len("Type")
	variableWidth := len("Variables")
	lastUpdatedWidth := len("Last updated")
	for _, row := range rows {
		nameWidth = max(nameWidth, len(row.Name))
		typeWidth = max(typeWidth, len(row.Type))
		variableWidth = max(variableWidth, len(fmt.Sprintf("%d", row.Variables)))
		lastUpdatedWidth = max(lastUpdatedWidth, len(envListLastUpdatedDisplay(row.LastUpdated)))
	}

	header := fmt.Sprintf("%-*s  %-*s  %*s  %s", nameWidth, "Name", typeWidth, "Type", variableWidth, "Variables", "Last updated")
	fmt.Fprintln(out, warn(header))
	fmt.Fprintf(out, "%-*s  %-*s  %*s  %s\n", nameWidth, strings.Repeat("-", nameWidth), typeWidth, strings.Repeat("-", typeWidth), variableWidth, strings.Repeat("-", variableWidth), strings.Repeat("-", lastUpdatedWidth))
	for _, row := range rows {
		typeCell := success(row.Type) + strings.Repeat(" ", max(0, typeWidth-len(row.Type)))
		fmt.Fprintf(out, "%-*s  %s  %*d  %s\n", nameWidth, row.Name, typeCell, variableWidth, row.Variables, envListLastUpdatedDisplay(row.LastUpdated))
	}
}

func envListLastUpdatedDisplay(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return parsed.Local().Format("2006-01-02 03:04 PM MST")
	}
	return value
}

func (r *Runner) runEnvCreate(args []string) error {
	fs := newFlagSet("env create", r.errOut)
	envType := fs.String("type", "", "Environment type label")
	fromEnv := fs.String("from-env", "", "Source environment for seeding values")
	fromFile := fs.String("from-file", "", "Source env file for seeding keys")
	seed := fs.String("seed", "none", "Seed mode: keys-only, non-sensitive, all")
	jsonOut := fs.Bool("json", false, "Print environment creation result as JSON")
	seedProvided := hasFlag(args, "seed")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("json"))
	if err != nil {
		return err
	}
	seedMode, err := normalizeSeedMode(*seed)
	if err != nil {
		return err
	}
	if *fromEnv != "" && *fromFile != "" {
		return fmt.Errorf("use --from-env or --from-file, not both")
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	if r.interactive && !*jsonOut {
		fmt.Fprintln(r.out, success("Create Environment"))
	}
	sourceEnv, seedMode, err := r.resolveCreateSeed(repo, *fromEnv, *fromFile, seedMode, seedProvided)
	if err != nil {
		return err
	}
	name := ""
	if len(positionals) > 0 {
		name = positionals[0]
	}
	name, err = r.ask("Environment name", name, "", "env")
	if err != nil {
		return err
	}
	if sourceEnv != "" && sourceEnv == name {
		return fmt.Errorf("source environment must be different from the new environment")
	}
	selectedType, promptedCustomType, err := r.chooseEnvironmentType("Environment type", name, *envType)
	if err != nil {
		return err
	}

	result, err := repo.CreateEnvironment(name, selectedType)
	if err != nil {
		return err
	}
	seedResult, err := seedEnvironment(repo, environmentSeedInput{
		target:     name,
		sourceEnv:  sourceEnv,
		sourceFile: *fromFile,
		mode:       seedMode,
		reason:     "Seeded from " + seedSourceLabel(sourceEnv, *fromFile),
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		payload := map[string]interface{}{"environment": result.Environment, "created": result.Created}
		if seedResult.hasSeed() {
			payload["seededFrom"] = seedSourceLabel(sourceEnv, *fromFile)
			payload["seedMode"] = seedMode
			if seedResult.pushResult != nil {
				payload["seeded"] = seedResult.pushResult
			}
			if seedResult.seededKeys >= 0 {
				payload["seededKeys"] = seedResult.seededKeys
			}
		}
		return printJSON(r.out, payload)
	}
	if promptedCustomType && r.interactive {
		fmt.Fprintln(r.out)
	}
	if seedResult.pushResult != nil {
		seededVariableCount := len(seedResult.pushResult.Created) + len(seedResult.pushResult.Updated)
		fmt.Fprintln(r.out, success(envCreateSummary(result, fmt.Sprintf(" and seeded %s from %s", variableCountText(seededVariableCount), seedSourceLabel(sourceEnv, *fromFile)))))
	} else if seedResult.seededKeys >= 0 {
		fmt.Fprintln(r.out, success(envCreateSummary(result, fmt.Sprintf(" and seeded key layout from %s with %s", seedSourceLabel(sourceEnv, *fromFile), keyCountText(seedResult.seededKeys)))))
	} else if result.Created {
		fmt.Fprintln(r.out, success(envCreateSummary(result, "")))
	} else {
		fmt.Fprintln(r.out, warn(fmt.Sprintf("%s already exists.", result.Environment.Name)))
	}
	return r.maybePromptGenerateExample(repo, seedResult.pushResult != nil && len(seedResult.pushResult.Created) > 0, false, *jsonOut)
}

func (r *Runner) resolveCreateSeed(repo store.Repository, fromEnv string, fromFile string, seedMode string, seedProvided bool) (string, string, error) {
	if fromFile != "" {
		return "", seedMode, nil
	}
	if fromEnv != "" {
		if !oneOfString(fromEnv, environmentNames(repo)...) {
			return "", "", fmt.Errorf("environment %q is not defined in .ghostable/ghostable.yaml", fromEnv)
		}
		if !seedProvided && seedMode == "keys-only" {
			seedMode = "insensitive"
		}
		return fromEnv, seedMode, nil
	}
	if !r.interactive {
		return "", seedMode, nil
	}

	choices := environmentNames(repo)
	if len(choices) == 0 {
		return "", seedMode, nil
	}
	baseLabel := "Base this environment on an existing environment?"
	useExisting, err := r.prompts.Confirm(baseLabel, false)
	if err != nil {
		return "", "", err
	}
	r.printPromptAnswer(baseLabel, yesNo(useExisting))
	if !useExisting {
		return "", "keys-only", nil
	}

	sourceLabel := "Select source environment"
	source, err := r.prompts.Select(sourceLabel, choices, 0)
	if err != nil {
		return "", "", err
	}
	r.printPromptAnswer(sourceLabel, source)
	copyLabel := "Copy values"
	selectedMode, err := r.selectSeedMode(copyLabel, "insensitive")
	if err != nil {
		return "", "", err
	}
	r.printPromptAnswer(copyLabel, seedModeLabel(selectedMode))
	return source, selectedMode, nil
}

func (r *Runner) runEnvDelete(args []string) error {
	fs := newFlagSet("env delete", r.errOut)
	env := fs.String("env", "", "Environment name")
	assumeYes := fs.Bool("assume-yes", false, "Skip confirmation prompt")
	fs.BoolVar(assumeYes, "y", false, "Skip confirmation prompt")
	jsonOut := fs.Bool("json", false, "Print environment deletion result as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("assume-yes", "y", "json")); err != nil {
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
	ok, err := r.confirm("Delete environment "+selected+"?", *assumeYes)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("canceled")
	}
	if err := repo.DeleteEnvironment(selected); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, map[string]interface{}{"environment": selected, "deleted": true})
	}
	fmt.Fprintln(r.out, danger(fmt.Sprintf("Deleted %s.", selected)))
	return nil
}

func (r *Runner) runEnvPush(args []string, syncMode bool) error {
	fs := newFlagSet("env push", r.errOut)
	env := fs.String("env", "", "Environment name")
	file := fs.String("file", "", "Path to .env file")
	syncFlag := fs.Bool("sync", syncMode, "Delete stored variables absent from the local file")
	reason := fs.String("reason", "", "Reason stored in signed value changes and local events")
	assumeYes := fs.Bool("assume-yes", false, "Skip summary output")
	fs.BoolVar(assumeYes, "y", false, "Skip summary output")
	jsonOut := fs.Bool("json", false, "Print transfer summary as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("sync", "assume-yes", "y", "json")); err != nil {
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
	if *file == "" {
		*file = envFileDefault(selected)
	}
	values, err := readDotenvVariableInputs(repoFilePath(repo.Root, *file))
	if err != nil {
		return err
	}
	changeReason := *reason
	if strings.TrimSpace(changeReason) == "" && r.interactive && !*jsonOut {
		current, err := repo.ReadVariables(selected)
		if err != nil {
			return err
		}
		changeReason, err = r.maybePromptValueChangeReason(changeReason, *jsonOut, variableInputsChangeStoredValues(current, values))
		if err != nil {
			return err
		}
	}
	result, err := repo.PutVariablesWithMetadata(selected, values, storePut(changeReason, *syncFlag))
	if err != nil {
		return err
	}
	result.File = *file
	if *jsonOut {
		return printJSON(r.out, result)
	}
	if !*assumeYes {
		printPushSummary(r.out, result)
	}
	return r.maybePromptGenerateExample(repo, pushResultChangedKeys(result), len(result.Deleted) > 0, *jsonOut)
}

func pushResultChangedKeys(result store.PushResult) bool {
	return len(result.Created) > 0 || len(result.Deleted) > 0
}

func variableInputsChangeStoredValues(current map[string]domain.Variable, incoming map[string]store.VariablePutInput) bool {
	for key, input := range incoming {
		variable, exists := current[key]
		if !exists || variable.Value != input.Value {
			return true
		}
	}
	return false
}

func (r *Runner) runEnvPull(args []string) error {
	fs := newFlagSet("env pull", r.errOut)
	env := fs.String("env", "", "Environment name")
	file := fs.String("file", "", "Output file")
	var only cli.Strings
	fs.Var(&only, "only", "Only include these keys")
	dryRun := fs.Bool("dry-run", false, "Do not write the env file")
	replace := fs.Bool("replace", false, "Replace local file instead of merging")
	noBackup := fs.Bool("no-backup", false, "Do not create a backup before writing")
	force := fs.Bool("force", false, "Overwrite local values without prompting")
	jsonOut := fs.Bool("json", false, "Print transfer summary as JSON")
	showValues := fs.Bool("show-values", false, "Print plaintext values in output")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("dry-run", "replace", "no-backup", "force", "json", "show-values")); err != nil {
		return err
	}
	return r.pullEnvironmentFile(environmentPullRequest{
		Environment: *env,
		File:        *file,
		Only:        only,
		DryRun:      *dryRun,
		Replace:     *replace,
		Backup:      !*noBackup,
		Force:       *force,
		ShowValues:  *showValues,
		JSON:        *jsonOut,
	})
}

func (r *Runner) runEnvDiff(args []string) error {
	fs := newFlagSet("env diff", r.errOut)
	env := fs.String("env", "", "Environment name")
	from := fs.String("from", "", "Source environment name")
	to := fs.String("to", "", "Target environment name")
	file := fs.String("file", "", "Local .env path")
	local := fs.String("local", "", "Alias for --file")
	var only cli.Strings
	fs.Var(&only, "only", "Only diff these keys")
	showValues := fs.Bool("show-values", false, "Print plaintext values in diff output")
	jsonOut := fs.Bool("json", false, "Print diff result as JSON")
	envProvided := hasFlag(args, "env")
	fromProvided := hasFlag(args, "from")
	toProvided := hasFlag(args, "to")
	fileProvided := hasFlag(args, "file") || hasFlag(args, "local")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("show-values", "json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}

	environmentDiffRequested := fromProvided || toProvided
	if environmentDiffRequested && (envProvided || fileProvided) {
		return fmt.Errorf("pass either --from/--to for environment diff or --env/--file for file diff, not both")
	}
	fileDiffRequested := envProvided || fileProvided
	diffMode, err := r.selectEnvDiffMode(repo, environmentDiffRequested, fileDiffRequested)
	if err != nil {
		return err
	}
	if diffMode == "environment" {
		source, err := r.selectEnvironmentWithLabel(repo, *from, "Select source environment", "from")
		if err != nil {
			return err
		}
		target, err := r.selectEnvironmentExcept(repo, *to, source, "Select target environment", "to")
		if err != nil {
			return err
		}
		if source == target {
			return fmt.Errorf("target environment must be different from source environment")
		}
		diff, err := repo.DiffEnvironments(source, target, only, *showValues)
		if err != nil {
			return err
		}
		if *jsonOut {
			return printJSON(r.out, diff)
		}
		printDiff(r.out, diff, *showValues)
		return nil
	}

	selected, err := r.selectEnvironment(repo, *env)
	if err != nil {
		return err
	}
	if *file == "" {
		*file = *local
	}
	diff, err := repo.Diff(selected, *file, only, *showValues)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, diff)
	}
	printDiff(r.out, diff, *showValues)
	return nil
}

func (r *Runner) selectEnvDiffMode(repo store.Repository, environmentDiffRequested bool, fileDiffRequested bool) (string, error) {
	if environmentDiffRequested {
		return "environment", nil
	}
	if fileDiffRequested || !r.interactive || len(repo.Environments()) < 2 {
		return "file", nil
	}
	return r.prompts.SelectOptions("Compare", promptOptions(envDiffModeOptions), 0)
}

func (r *Runner) runEnvHistory(args []string) error {
	fs := newFlagSet("env history", r.errOut)
	env := fs.String("env", "", "Environment name")
	key := fs.String("key", "", "Only show events for one variable")
	action := fs.String("action", "", "Only show one event action")
	limit := fs.String("limit", "50", "Limit the number of events shown")
	jsonOut := fs.Bool("json", false, "Print history as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	events, err := repo.History(*env, *key, *action, parsePositiveInt(*limit, 50))
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, events)
	}
	devices, err := repo.Devices()
	if err != nil {
		return err
	}
	printEnvHistoryRows(r.out, events, devices)
	return nil
}

func printEnvHistoryRows(out io.Writer, events []domain.Event, devices []domain.DeviceRecord) {
	if len(events) == 0 {
		fmt.Fprintln(out, warn("No history events found."))
		return
	}

	deviceNames := historyDeviceNames(devices)
	whenWidth := len("When")
	actionWidth := len("Action")
	envWidth := len("Environment")
	keyWidth := len("Key")
	deviceWidth := len("Device")
	for _, event := range events {
		whenWidth = max(whenWidth, len(historyTimeDisplay(event.OccurredAt)))
		actionWidth = max(actionWidth, len(historyCell(event.Action)))
		envWidth = max(envWidth, len(historyCell(event.Environment)))
		keyWidth = max(keyWidth, len(historyCell(event.Key)))
		deviceWidth = max(deviceWidth, len(historyDeviceDisplay(event, deviceNames)))
	}

	header := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %s", whenWidth, "When", actionWidth, "Action", envWidth, "Environment", keyWidth, "Key", "Device")
	fmt.Fprintln(out, warn(header))
	fmt.Fprintf(out, "%-*s  %-*s  %-*s  %-*s  %s\n",
		whenWidth,
		strings.Repeat("-", whenWidth),
		actionWidth,
		strings.Repeat("-", actionWidth),
		envWidth,
		strings.Repeat("-", envWidth),
		keyWidth,
		strings.Repeat("-", keyWidth),
		strings.Repeat("-", deviceWidth),
	)
	for _, event := range events {
		when := historyTimeDisplay(event.OccurredAt)
		action := historyCell(event.Action)
		env := historyCell(event.Environment)
		key := historyCell(event.Key)
		device := historyDeviceDisplay(event, deviceNames)
		fmt.Fprintf(out, "%-*s  %s  %s  %-*s  %s\n",
			whenWidth,
			when,
			success(action)+strings.Repeat(" ", actionWidth-len(action)),
			success(env)+strings.Repeat(" ", envWidth-len(env)),
			keyWidth,
			key,
			success(device)+strings.Repeat(" ", deviceWidth-len(device)),
		)
	}
}

func historyTimeDisplay(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return parsed.Local().Format("2006-01-02 03:04 PM MST")
	}
	return value
}

func historyCell(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func historyDeviceNames(devices []domain.DeviceRecord) map[string]string {
	names := make(map[string]string, len(devices))
	for _, device := range devices {
		names[device.ID] = statusDeviceName(device)
	}
	return names
}

func historyDeviceDisplay(event domain.Event, deviceNames map[string]string) string {
	if event.ClientSig == "" {
		return "unsigned"
	}
	signer := event.SignerDeviceID
	if signer == "" {
		signer = event.DeviceID
	}
	signer = strings.TrimSpace(signer)
	if signer == "" {
		return "-"
	}
	shortID := statusShortID(signer)
	if name, ok := deviceNames[signer]; ok && strings.TrimSpace(name) != "" {
		return fmt.Sprintf("%s (%s)", name, shortID)
	}
	return shortID
}

func (r *Runner) runEnvDuplicate(args []string) error {
	fs := newFlagSet("env duplicate", r.errOut)
	envType := fs.String("type", "", "Target environment type label")
	seed := fs.String("seed", "insensitive", "Variable copy mode: keys-only, non-sensitive, all")
	reason := fs.String("reason", "", "Reason stored in signed local events")
	jsonOut := fs.Bool("json", false, "Print duplicate summary as JSON")
	seedProvided := hasFlag(args, "seed")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("json"))
	if err != nil {
		return err
	}
	seedMode, err := normalizeSeedMode(*seed)
	if err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	source := ""
	target := ""
	if len(positionals) > 0 {
		source = positionals[0]
	}
	if len(positionals) > 1 {
		target = positionals[1]
	}
	source, err = r.selectEnvironment(repo, source)
	if err != nil {
		return err
	}
	target, err = r.ask("New environment name", target, "", "to")
	if err != nil {
		return err
	}
	selectedType, promptedCustomType, err := r.chooseEnvironmentType("New environment type", target, *envType)
	if err != nil {
		return err
	}
	if r.interactive && !seedProvided {
		seedMode, err = r.selectSeedMode("Copy values", seedMode)
		if err != nil {
			return err
		}
		r.printPromptAnswer("Copy values", seedModeLabel(seedMode))
	}
	created, err := repo.CreateEnvironment(target, selectedType)
	if err != nil {
		return err
	}
	seedResult, err := seedEnvironment(repo, environmentSeedInput{
		target:    target,
		sourceEnv: source,
		mode:      seedMode,
		reason:    *reason,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, created)
	}
	if promptedCustomType && r.interactive {
		fmt.Fprintln(r.out)
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Duplicated %s to %s.", source, target)))
	return r.maybePromptGenerateExample(repo, seedResult.pushResult != nil && len(seedResult.pushResult.Created) > 0, false, *jsonOut)
}

var environmentTypeChoices = []string{"local", "development", "preview", "staging", "production", "custom"}

func (r *Runner) chooseEnvironmentType(label string, envName string, provided string) (string, bool, error) {
	provided = strings.TrimSpace(provided)
	if provided != "" {
		return provided, false, nil
	}
	if !r.interactive {
		return "", false, nil
	}

	defaultType := environmentType(envName)
	defaultIndex := 0
	for index, choice := range environmentTypeChoices {
		if choice == defaultType {
			defaultIndex = index
			break
		}
	}

	selected, err := r.prompts.Select(label, environmentTypeChoices, defaultIndex)
	if err != nil {
		return "", false, err
	}
	if selected != "custom" {
		return selected, false, nil
	}

	customType, err := r.askTight("Custom environment type", "", "custom", "type")
	if err != nil {
		return "", false, err
	}
	return customType, true, nil
}

func (r *Runner) runEnvRename(args []string) error {
	fs := newFlagSet("env rename", r.errOut)
	from := fs.String("from", "", "Source environment name")
	to := fs.String("to", "", "Target environment name")
	reason := fs.String("reason", "", "Reason stored in signed local events")
	jsonOut := fs.Bool("json", false, "Print rename summary as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("json"))
	if err != nil {
		return err
	}
	if len(positionals) > 0 && *from == "" {
		*from = positionals[0]
	}
	if len(positionals) > 1 && *to == "" {
		*to = positionals[1]
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	source, err := r.selectEnvironment(repo, *from)
	if err != nil {
		return err
	}
	target, err := r.ask("New environment name", *to, "", "to")
	if err != nil {
		return err
	}
	if err := repo.RenameEnvironment(source, target, *reason); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, map[string]string{"from": source, "to": target})
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Renamed %s to %s.", source, target)))
	return nil
}

func (r *Runner) runEnvLayout(args []string) error {
	if len(args) == 0 {
		if !r.interactive {
			r.printEnvLayoutHelp()
			return nil
		}
		selected, err := r.selectCommand("Select a layout command", envLayoutCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if isHelpArg(args[0]) {
		r.printEnvLayoutHelp()
		return nil
	}
	if args[0] != "generate" {
		return fmt.Errorf("usage: ghostable env layout generate --env <ENV> [--file <PATH>]")
	}
	fs := newFlagSet("env layout generate", r.errOut)
	env := fs.String("env", "", "Environment name")
	file := fs.String("file", "", "Use a local .env file as the ordering source")
	jsonOut := fs.Bool("json", false, "Print layout generation result as JSON")
	if _, err := cli.Parse(fs, args[1:], cli.BoolFlags("json")); err != nil {
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
	keys := []string{}
	if *file != "" {
		parsed, err := readDotenvEntries(repoFilePath(repo.Root, *file))
		if err != nil {
			return err
		}
		for _, entry := range parsed.Entries {
			keys = append(keys, entry.Key)
		}
	} else {
		values, err := repo.ReadVariables(selected)
		if err != nil {
			return err
		}
		for key := range values {
			keys = append(keys, key)
		}
	}
	sortStrings(keys)
	if err := repo.GenerateLayout(selected, keys); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, map[string]interface{}{"environment": selected, "keys": keys})
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Generated layout for %s with %d keys.", selected, len(keys))))
	return nil
}

func (r *Runner) printEnvLayoutHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable env layout <command> [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, envLayoutCommandOptions)
}

func (r *Runner) runEnvFile(args []string) error {
	if len(args) == 0 {
		if !r.interactive {
			r.printEnvFileHelp()
			return nil
		}
		selected, err := r.selectCommand("Select an env file command", envFileCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if isHelpArg(args[0]) {
		r.printEnvFileHelp()
		return nil
	}
	if args[0] != "save" {
		return fmt.Errorf("usage: ghostable env file save --file <PATH> --content-base64 <BASE64>")
	}
	fs := newFlagSet("env file save", r.errOut)
	file := fs.String("file", "", "Target .env file")
	contentB64 := fs.String("content-base64", "", "Base64-encoded UTF-8 .env content")
	jsonOut := fs.Bool("json", false, "Print save result as JSON")
	if _, err := cli.Parse(fs, args[1:], cli.BoolFlags("json")); err != nil {
		return err
	}
	if *file == "" || *contentB64 == "" {
		return fmt.Errorf("--file and --content-base64 are required")
	}
	content, err := base64.StdEncoding.DecodeString(*contentB64)
	if err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	path, err := resolveEnvFileSavePath(repo.Root, *file)
	if err != nil {
		return err
	}
	if err := writeEnvFileSave(path, content); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, map[string]interface{}{"file": *file, "saved": true})
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Saved %s.", *file)))
	return nil
}

func resolveEnvFileSavePath(root string, file string) (string, error) {
	path := repoFilePath(root, file)
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !pathInsideDirectory(absoluteRoot, absolutePath) {
		return "", fmt.Errorf("env file save path %q must stay inside the project", file)
	}
	realRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", err
	}
	realParent, err := filepath.EvalSymlinks(filepath.Dir(absolutePath))
	if err != nil {
		return "", err
	}
	if !pathInsideDirectory(realRoot, realParent) {
		return "", fmt.Errorf("env file save path %q must stay inside the project", file)
	}
	info, err := os.Lstat(absolutePath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to write through symlinked env file %q", file)
		}
		if info.IsDir() {
			return "", fmt.Errorf("env file save path %q is a directory", file)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return absolutePath, nil
}

func writeEnvFileSave(path string, content []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func (r *Runner) printEnvFileHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable env file <command> [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, envFileCommandOptions)
}

func readDotenvFile(file string) (map[string]string, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return dotenv.ParseString(string(content))
}

func readDotenvVariableInputs(file string) (map[string]store.VariablePutInput, error) {
	values, _, err := readDotenvVariableInputsAndKeys(file)
	return values, err
}

func readDotenvVariableInputsAndKeys(file string) (map[string]store.VariablePutInput, []string, error) {
	parsed, err := readDotenvEntries(file)
	if err != nil {
		return nil, nil, err
	}
	values := make(map[string]store.VariablePutInput, len(parsed.Entries))
	keys := make([]string, 0, len(parsed.Entries))
	entries := make([]dotenv.Entry, 0, len(parsed.Entries))
	for _, entry := range parsed.Entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Line < entries[j].Line
	})
	for _, entry := range entries {
		commented := entry.Disabled
		values[entry.Key] = store.VariablePutInput{
			Value:     entry.Value,
			Commented: &commented,
		}
		keys = append(keys, entry.Key)
	}
	return values, keys, nil
}

func readDotenvEntries(file string) (dotenv.File, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return dotenv.File{}, err
	}
	return dotenv.Parse(strings.NewReader(string(content)))
}

func seedEnvironment(repo store.Repository, input environmentSeedInput) (environmentSeedResult, error) {
	result := emptyEnvironmentSeedResult()

	switch {
	case input.sourceEnv != "":
		variables, err := repo.ReadVariables(input.sourceEnv)
		if err != nil {
			return result, err
		}
		return seedEnvironmentFromVariables(repo, input.target, variables, input.mode, input.reason)
	case input.sourceFile != "":
		values, keys, err := readDotenvVariableInputsAndKeys(input.sourceFile)
		if err != nil {
			return result, err
		}
		return seedEnvironmentFromInputs(repo, input.target, values, keys, input.mode, input.reason)
	default:
		return result, nil
	}
}

func seedEnvironmentFromVariables(repo store.Repository, target string, variables map[string]domain.Variable, mode string, reason string) (environmentSeedResult, error) {
	return seedEnvironmentFromInputs(repo, target, variableInputsFromVariables(variables), variableKeys(variables), mode, reason)
}

func seedEnvironmentFromInputs(repo store.Repository, target string, values map[string]store.VariablePutInput, keys []string, mode string, reason string) (environmentSeedResult, error) {
	result := emptyEnvironmentSeedResult()

	if mode == "keys-only" {
		if err := repo.GenerateLayout(target, keys); err != nil {
			return result, err
		}
		result.seededKeys = len(keys)
		return result, nil
	}

	if mode == "insensitive" {
		values = filterInsensitiveVariableInputs(values)
	}
	pushed, err := repo.PutVariablesWithMetadata(target, values, storePut(reason, false))
	if err != nil {
		return result, err
	}
	result.pushResult = &pushed
	return result, nil
}

func variableInputsFromVariables(variables map[string]domain.Variable) map[string]store.VariablePutInput {
	values := make(map[string]store.VariablePutInput, len(variables))
	for key, variable := range variables {
		commented := variable.Commented
		values[key] = store.VariablePutInput{
			Value:     variable.Value,
			Commented: &commented,
		}
	}
	return values
}

func filterInsensitiveVariableInputs(values map[string]store.VariablePutInput) map[string]store.VariablePutInput {
	filtered := make(map[string]store.VariablePutInput, len(values))
	for key, value := range values {
		if looksSensitiveSeedKey(key) {
			continue
		}
		filtered[key] = value
	}
	return filtered
}

func looksSensitiveSeedKey(key string) bool {
	upperKey := strings.ToUpper(key)
	for _, marker := range []string{"SECRET", "TOKEN", "PASSWORD", "PRIVATE", "KEY"} {
		if strings.Contains(upperKey, marker) {
			return true
		}
	}
	return false
}

func normalizeSeedMode(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "none", "keys-only", "key-only", "keys only", "key only":
		return "keys-only", nil
	case "all":
		return "all", nil
	case "insensitive", "non-sensitive", "nonsensitive":
		return "insensitive", nil
	default:
		return "", fmt.Errorf("invalid seed mode %q; use keys-only, non-sensitive, or all", value)
	}
}

func (r *Runner) selectSeedMode(label string, fallback string) (string, error) {
	fallback, err := normalizeSeedMode(fallback)
	if err != nil {
		return "", err
	}
	defaultIndex := 0
	for index, option := range seedModeOptions {
		if option.Value == fallback || option.Label == fallback {
			defaultIndex = index
			break
		}
	}
	return r.prompts.SelectOptions(label, promptOptions(seedModeOptions), defaultIndex)
}

func (r *Runner) printPromptAnswer(label string, answer string) {
	separator := ": "
	if strings.HasSuffix(strings.TrimSpace(label), "?") {
		separator = " "
	}
	fmt.Fprintf(r.out, "%s%s%s\n", warn(label), separator, success(answer))
}

func yesNo(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}

func seedModeLabel(value string) string {
	switch value {
	case "insensitive":
		return "non-sensitive"
	case "keys-only":
		return "keys only"
	default:
		return value
	}
}

func seedSourceLabel(env string, file string) string {
	if env != "" {
		return env
	}
	return file
}

func environmentNamesExcept(repo store.Repository, excluded string) []string {
	names := []string{}
	for _, env := range repo.Environments() {
		if env.Name != excluded {
			names = append(names, env.Name)
		}
	}
	return names
}

func storePut(reason string, sync bool) store.PutOptions {
	return store.PutOptions{Reason: reason, Sync: sync}
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				result[part] = true
			}
		}
	}
	return result
}

func printPushSummary(out io.Writer, result store.PushResult) {
	fmt.Fprintf(out, "%s: %s, %s, %s, %d unchanged.\n",
		result.Environment,
		success(fmt.Sprintf("%d created", len(result.Created))),
		warn(fmt.Sprintf("%d updated", len(result.Updated))),
		danger(fmt.Sprintf("%d deleted", len(result.Deleted))),
		len(result.Unchanged),
	)
}

func printDiff(out io.Writer, diff domain.EnvDiff, showValues bool) {
	fmt.Fprintf(out, "%s %s -> %s\n", warn("Diff:"), success(diffSourceLabel(diff)), success(diffTargetLabel(diff)))
	for _, entry := range diff.Added {
		fmt.Fprintf(out, "%s%s\n", success("+ "+entry.Key), valueSuffix(entry.LocalValue, showValues))
	}
	for _, entry := range diff.Changed {
		fmt.Fprintf(out, "%s%s\n", warn("~ "+entry.Key), changedValueSuffix(entry, showValues))
	}
	for _, entry := range diff.Removed {
		fmt.Fprintf(out, "%s%s\n", danger("- "+entry.Key), valueSuffix(entry.StoredValue, showValues))
	}
	fmt.Fprintf(out, "%s %d added, %d changed, %d removed, %d unchanged.\n",
		warn("Summary:"),
		diff.Summary.Added,
		diff.Summary.Changed,
		diff.Summary.Removed,
		diff.Summary.Unchanged,
	)
}

func diffSourceLabel(diff domain.EnvDiff) string {
	if diff.SourceEnvironment != "" {
		return diff.SourceEnvironment
	}
	if diff.File != "" {
		return diff.File
	}
	return "source"
}

func diffTargetLabel(diff domain.EnvDiff) string {
	if diff.TargetEnvironment != "" {
		return diff.TargetEnvironment
	}
	if diff.Environment != "" {
		return diff.Environment
	}
	return "target"
}

func valueSuffix(value string, show bool) string {
	if !show || value == "" {
		return ""
	}
	return "=" + dotenv.FormatValue(value)
}

func changedValueSuffix(entry domain.DiffEntry, show bool) string {
	if !show || entry.LocalValue == "" {
		return ""
	}
	if entry.StoredValue == "" {
		return valueSuffix(entry.LocalValue, show)
	}
	return "=" + dotenv.FormatValue(entry.LocalValue) + " -> " + dotenv.FormatValue(entry.StoredValue)
}
