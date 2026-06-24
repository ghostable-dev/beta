package app

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/dotenv"
	"github.com/ghostable-dev/beta/internal/store"
)

var variableCommandOptions = []commandOption{
	{Label: "push", Description: "Save one variable"},
	{Label: "pull", Description: "Read or write one variable"},
	{Label: "promote", Description: "Promote a variable between environments"},
	{Label: "delete", Description: "Remove one variable"},
	{Label: "history", Description: "Show change history"},
	{Label: "context", Description: "View or update the encrypted note"},
	{Label: "annotation", Description: "Manage typed key annotations"},
}

var variablePromotionModeOptions = []commandOption{
	{Label: "value", Description: "Promote the value and variable flags"},
	{Label: "key only", Value: "key-only", Description: "Add the key to target layout without copying its value"},
}

func (r *Runner) runVar(args []string) error {
	if len(args) == 0 {
		if !r.interactive {
			r.printVarHelp()
			return nil
		}
		selected, err := r.selectCommand("Select a variable command", variableCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if isHelpArg(args[0]) {
		r.printVarHelp()
		return nil
	}

	switch args[0] {
	case "push":
		return r.runVarPush(args[1:])
	case "pull":
		return r.runVarPull(args[1:])
	case "promote", "copy":
		return r.runVarPromote(args[1:])
	case "delete":
		return r.runVarDelete(args[1:])
	case "history":
		return r.runEnvHistory(args[1:])
	case "context":
		return r.runVarContext(args[1:])
	case "annotation", "annotations", "annotate":
		return r.runVarAnnotation(args[1:])
	case "rollback":
		return fmt.Errorf("variable rollback is not implemented in the Go client yet; use git history to recover the value file or restore from backup")
	default:
		return fmt.Errorf("unknown var command %q", args[0])
	}
}

func (r *Runner) printVarHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable var <command> [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, variableCommandOptions)
}

func (r *Runner) runVarPush(args []string) error {
	fs := newFlagSet("var push", r.errOut)
	env := fs.String("env", "", "Environment name")
	key := fs.String("key", "", "Variable name")
	file := fs.String("file", "", "Path to .env file")
	reason := fs.String("reason", "", "Reason stored in signed value changes and local events")
	jsonOut := fs.Bool("json", false, "Print mutation result as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
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

	newVariableOnly := false
	pushFile, inferredFile := r.varPushFile(repo, selected, *file)
	if pushFile != "" {
		result, err := r.tryVarPushFromFile(varPushFileInput{
			repo:                 repo,
			environment:          selected,
			file:                 pushFile,
			providedKey:          *key,
			reason:               *reason,
			jsonOut:              *jsonOut,
			fallbackOnMissingKey: inferredFile,
		})
		if err != nil {
			return err
		}
		if result.handled {
			return nil
		}
		newVariableOnly = result.newVariableOnly
	}

	variableKey, err := r.selectVariableKeyForPush(repo, selected, *key, newVariableOnly)
	if err != nil {
		return err
	}
	if !r.interactive {
		return fmt.Errorf("pass --file so the value does not appear in shell history")
	}
	current, existed, err := repo.GetVariable(selected, variableKey)
	if err != nil {
		return err
	}
	value, err := r.prompts.Secret("Variable value")
	if err != nil {
		return err
	}
	valueChanged := !existed || current.Value != value
	changeReason, err := r.maybePromptValueChangeReason(*reason, *jsonOut, valueChanged)
	if err != nil {
		return err
	}

	if err := repo.SetVariableWithOptions(selected, variableKey, value, store.VariableWriteOptions{
		Reason: changeReason,
	}); err != nil {
		return err
	}
	payload := map[string]interface{}{"environment": selected, "key": variableKey, "saved": true}
	if *jsonOut {
		return printJSON(r.out, payload)
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Saved %s in %s.", variableKey, selected)))
	return r.maybePromptGenerateExample(repo, !existed, false, *jsonOut)
}

type varPushFileInput struct {
	repo                 store.Repository
	environment          string
	file                 string
	providedKey          string
	reason               string
	jsonOut              bool
	fallbackOnMissingKey bool
}

type varPushFileResult struct {
	handled         bool
	newVariableOnly bool
}

func (r *Runner) varPushFile(repo store.Repository, env string, provided string) (string, bool) {
	provided = strings.TrimSpace(provided)
	if provided != "" {
		return provided, false
	}
	if !r.interactive {
		return "", false
	}
	for _, candidate := range varPushDefaultFileCandidates(env) {
		info, err := os.Stat(repoFilePath(repo.Root, candidate))
		if err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func varPushDefaultFileCandidates(env string) []string {
	candidates := []string{envFileDefault(env)}
	if env != "local" && env != "default" {
		candidates = append(candidates, env+".env")
	}
	return candidates
}

func (r *Runner) tryVarPushFromFile(input varPushFileInput) (varPushFileResult, error) {
	parsed, err := readDotenvEntries(repoFilePath(input.repo.Root, input.file))
	if err != nil {
		return varPushFileResult{}, err
	}
	values := make(map[string]string, len(parsed.Entries))
	for key, entry := range parsed.Entries {
		values[key] = entry.Value
	}

	variableKey := strings.TrimSpace(input.providedKey)
	if variableKey != "" {
		formattedKey, _, err := formatManualVariableKey(variableKey)
		if err != nil {
			return varPushFileResult{}, err
		}
		variableKey = formattedKey
	}
	if variableKey == "" {
		if len(values) == 0 && input.fallbackOnMissingKey {
			return varPushFileResult{newVariableOnly: true}, nil
		}
		selectedKey, err := r.selectKeyFromValues("Select a variable", values, "")
		if err != nil {
			return varPushFileResult{}, err
		}
		variableKey = selectedKey
	}

	entry, ok := parsed.Entries[variableKey]
	if !ok {
		if input.fallbackOnMissingKey {
			return varPushFileResult{}, nil
		}
		return varPushFileResult{}, fmt.Errorf("%s was not found in %s", variableKey, input.file)
	}
	current, existed, err := input.repo.GetVariable(input.environment, variableKey)
	if err != nil {
		return varPushFileResult{}, err
	}
	valueChanged := !existed || current.Value != entry.Value
	changeReason, err := r.maybePromptValueChangeReason(input.reason, input.jsonOut, valueChanged)
	if err != nil {
		return varPushFileResult{}, err
	}
	if err := input.repo.SetVariableWithOptions(input.environment, variableKey, entry.Value, store.VariableWriteOptions{
		Reason:    changeReason,
		Commented: &entry.Disabled,
	}); err != nil {
		return varPushFileResult{}, err
	}
	payload := map[string]interface{}{"environment": input.environment, "key": variableKey, "saved": true, "commented": entry.Disabled}
	if input.jsonOut {
		return varPushFileResult{handled: true}, printJSON(r.out, payload)
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Saved %s in %s.", variableKey, input.environment)))
	return varPushFileResult{handled: true}, r.maybePromptGenerateExample(input.repo, !existed, false, input.jsonOut)
}

func (r *Runner) runVarPull(args []string) error {
	fs := newFlagSet("var pull", r.errOut)
	env := fs.String("env", "", "Environment name")
	key := fs.String("key", "", "Variable name")
	file := fs.String("file", "", "Output .env file")
	showValues := fs.Bool("show-values", false, "Print plaintext value to stdout")
	jsonOut := fs.Bool("json", false, "Print mutation result as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("show-values", "json")); err != nil {
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
	variableKey, err := r.selectVariableKey(repo, selected, *key)
	if err != nil {
		return err
	}
	variable, exists, err := repo.GetVariable(selected, variableKey)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s was not found in %s", variableKey, selected)
	}

	if *file != "" {
		path := repoFilePath(repo.Root, *file)
		existing := ""
		if content, err := os.ReadFile(path); err == nil {
			existing = string(content)
		}
		next, err := dotenv.Merge(existing, map[string]string{variableKey: variable.Value}, []string{variableKey}, false)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(next), 0o600); err != nil {
			return err
		}
	}

	if *jsonOut {
		if !*showValues {
			variable.Value = ""
			variable.HasValue = false
		}
		return printJSON(r.out, variable)
	}
	if *file != "" {
		fmt.Fprintln(r.out, success(fmt.Sprintf("Wrote %s to %s.", variableKey, *file)))
		return nil
	}
	if *showValues {
		fmt.Fprintf(r.out, "%s=%s\n", variableKey, dotenv.FormatValue(variable.Value))
		return nil
	}
	fmt.Fprintln(r.out, warn(fmt.Sprintf("%s exists in %s. Pass --file to write it or --show-values to print it.", variableKey, selected)))
	return nil
}

func (r *Runner) runVarPromote(args []string) error {
	fs := newFlagSet("var promote", r.errOut)
	from := fs.String("from", "", "Source environment name")
	to := fs.String("to", "", "Target environment name")
	key := fs.String("key", "", "Variable name")
	mode := fs.String("mode", "value", "Promotion mode: value or key-only")
	reason := fs.String("reason", "", "Reason stored in signed value changes and local events")
	jsonOut := fs.Bool("json", false, "Print promotion result as JSON")
	modeProvided := hasFlag(args, "mode")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}
	promotionMode, err := normalizeVariablePromotionMode(*mode)
	if err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	source, err := r.selectEnvironmentWithLabel(repo, *from, "Select source environment", "from")
	if err != nil {
		return err
	}
	target, err := r.selectEnvironmentExcept(repo, *to, source, "Select target environment", "to")
	if err != nil {
		return err
	}
	variableKey, err := r.selectVariableKey(repo, source, *key)
	if err != nil {
		return err
	}
	variable, exists, err := repo.GetVariable(source, variableKey)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s was not found in %s", variableKey, source)
	}
	if r.interactive && !modeProvided {
		promotionMode, err = r.selectVariablePromotionMode("Promotion mode", promotionMode)
		if err != nil {
			return err
		}
		r.printPromptAnswer("Promotion mode", variablePromotionModeLabel(promotionMode))
	}
	if promotionMode == "key-only" {
		if err := repo.AddLayoutKey(target, variableKey); err != nil {
			return err
		}
		return r.printVariablePromotionResult(*jsonOut, source, target, variableKey, promotionMode)
	}

	commented := variable.Commented
	targetVariable, targetExists, err := repo.GetVariable(target, variableKey)
	if err != nil {
		return err
	}
	valueChanged := !targetExists || targetVariable.Value != variable.Value
	changeReason, err := r.maybePromptValueChangeReason(*reason, *jsonOut, valueChanged)
	if err != nil {
		return err
	}
	if err := repo.SetVariableWithOptions(target, variableKey, variable.Value, store.VariableWriteOptions{
		Reason:    changeReason,
		Commented: &commented,
	}); err != nil {
		return err
	}
	return r.printVariablePromotionResult(*jsonOut, source, target, variableKey, promotionMode)
}

func (r *Runner) printVariablePromotionResult(jsonOut bool, source string, target string, key string, mode string) error {
	payload := map[string]interface{}{
		"source":      source,
		"environment": target,
		"key":         key,
		"mode":        mode,
		"promoted":    true,
	}
	if jsonOut {
		return printJSON(r.out, payload)
	}
	if mode == "key-only" {
		fmt.Fprintln(r.out, success(fmt.Sprintf("Added %s to %s layout from %s.", key, target, source)))
		return nil
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Promoted %s from %s to %s.", key, source, target)))
	return nil
}

func normalizeVariablePromotionMode(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "value", "values":
		return "value", nil
	case "key-only", "key only", "keys-only", "keys only", "layout", "none":
		return "key-only", nil
	default:
		return "", fmt.Errorf("invalid variable promotion mode %q; use value or key-only", value)
	}
}

func (r *Runner) selectVariablePromotionMode(label string, fallback string) (string, error) {
	fallback, err := normalizeVariablePromotionMode(fallback)
	if err != nil {
		return "", err
	}
	defaultIndex := 0
	for index, option := range variablePromotionModeOptions {
		if option.Value == fallback || option.Label == fallback {
			defaultIndex = index
			break
		}
	}
	return r.prompts.SelectOptions(label, promptOptions(variablePromotionModeOptions), defaultIndex)
}

func variablePromotionModeLabel(value string) string {
	if value == "key-only" {
		return "key only"
	}
	return "value"
}

func (r *Runner) runVarDelete(args []string) error {
	fs := newFlagSet("var delete", r.errOut)
	env := fs.String("env", "", "Environment name")
	key := fs.String("key", "", "Variable name")
	reason := fs.String("reason", "", "Reason stored in signed local events")
	assumeYes := fs.Bool("assume-yes", false, "Skip confirmation prompt")
	fs.BoolVar(assumeYes, "y", false, "Skip confirmation prompt")
	jsonOut := fs.Bool("json", false, "Print mutation result as JSON")
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
	variableKey, err := r.selectVariableKey(repo, selected, *key)
	if err != nil {
		return err
	}
	ok, err := r.confirm("Delete "+variableKey+" from "+selected+"?", *assumeYes)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("canceled")
	}
	if err := repo.DeleteVariable(selected, variableKey, *reason); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, map[string]interface{}{"environment": selected, "key": variableKey, "deleted": true})
	}
	fmt.Fprintln(r.out, danger(fmt.Sprintf("Deleted %s from %s.", variableKey, selected)))
	return r.maybePromptGenerateExample(repo, true, true, *jsonOut)
}

func (r *Runner) runVarContext(args []string) error {
	fs := newFlagSet("var context", r.errOut)
	env := fs.String("env", "", "Environment name")
	key := fs.String("key", "", "Variable name")
	note := fs.String("note", "", "Replace the encrypted note for this variable")
	jsonOut := fs.Bool("json", false, "Print decrypted context as JSON")
	noteProvided := hasFlag(args, "note")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
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
	variableKey, err := r.selectVariableKey(repo, selected, *key)
	if err != nil {
		return err
	}
	if noteProvided {
		if err := repo.SetVariableNote(selected, variableKey, *note); err != nil {
			return err
		}
	}
	variable, exists, err := repo.GetVariable(selected, variableKey)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s was not found in %s", variableKey, selected)
	}
	variable.Value = ""
	variable.HasValue = false
	if *jsonOut {
		return printJSON(r.out, variable)
	}
	if r.interactive && !noteProvided {
		note, err := r.maybeUpdateVariableNote(repo, selected, variableKey, variable.Note)
		if err != nil {
			return err
		}
		variable.Note = note
	}
	if variable.Note == "" {
		fmt.Fprintln(r.out, warn(fmt.Sprintf("%s has no note.", variableKey)))
	} else {
		fmt.Fprintf(r.out, "%s note: %s\n", variableKey, variable.Note)
	}
	return nil
}

func (r *Runner) maybeUpdateVariableNote(repo store.Repository, env string, key string, currentNote string) (string, error) {
	if currentNote == "" {
		addNote, err := r.prompts.Confirm(fmt.Sprintf("%s has no note. Add one now?", key), false)
		if err != nil {
			return "", err
		}
		if !addNote {
			return "", nil
		}
		return r.promptAndSaveVariableNote(repo, env, key, "")
	}

	action, err := r.prompts.Select("Variable note", []string{"View note", "Update note"}, 0)
	if err != nil {
		return "", err
	}
	if action != "Update note" {
		return currentNote, nil
	}
	return r.promptAndSaveVariableNote(repo, env, key, currentNote)
}

func (r *Runner) promptAndSaveVariableNote(repo store.Repository, env string, key string, currentNote string) (string, error) {
	note, err := r.prompts.Ask("Variable note", currentNote)
	if err != nil {
		return "", err
	}
	if note == currentNote {
		return currentNote, nil
	}
	if err := repo.SetVariableNote(env, key, note); err != nil {
		return "", err
	}
	return note, nil
}

func (r *Runner) selectVariableKeyForPush(repo store.Repository, env string, provided string, newVariableOnly bool) (string, error) {
	if provided != "" {
		key, _, err := formatManualVariableKey(provided)
		return key, err
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --key")
	}
	if newVariableOnly {
		return r.askManualVariableKey()
	}

	variables, err := repo.ReadVariables(env)
	if err != nil {
		return "", err
	}
	keys := variableKeys(variables)
	if len(keys) == 0 {
		return r.askManualVariableKey()
	}
	choices := append(keys, "New variable")
	selected, err := r.prompts.Select("Select a variable", choices, 0)
	if err != nil {
		return "", err
	}
	if selected == "New variable" {
		return r.askManualVariableKey()
	}
	return selected, nil
}

func (r *Runner) askManualVariableKey() (string, error) {
	for {
		key, err := r.ask("Variable key", "", "", "key")
		if err != nil {
			return "", err
		}
		formatted, changed, err := formatManualVariableKey(key)
		if err == nil {
			if changed {
				fmt.Fprintf(r.out, "%s %s\n", warn("Formatted key:"), success(formatted))
			}
			return formatted, nil
		}
		if !r.interactive {
			return "", err
		}
		fmt.Fprintf(r.out, "%s %s\n", danger("Invalid key:"), err)
	}
}

func formatManualVariableKey(key string) (string, bool, error) {
	return dotenv.FormatKey(key)
}

func (r *Runner) selectVariableKey(repo store.Repository, env string, provided string) (string, error) {
	if provided != "" {
		return provided, nil
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --key")
	}

	variables, err := repo.ReadVariables(env)
	if err != nil {
		return "", err
	}
	keys := variableKeys(variables)
	if len(keys) == 0 {
		return "", fmt.Errorf("no variables are stored in %s", env)
	}
	return r.prompts.Select("Select a variable", keys, 0)
}

func (r *Runner) selectKeyFromValues(label string, values map[string]string, provided string) (string, error) {
	if provided != "" {
		return provided, nil
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --key")
	}

	keys := sortedMapKeys(values)
	if len(keys) == 0 {
		return "", fmt.Errorf("no variables were found")
	}
	return r.prompts.Select(label, keys, 0)
}

func variableKeys(variables map[string]domain.Variable) []string {
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
