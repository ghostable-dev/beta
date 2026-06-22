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
	{Label: "vapor-secret", Description: "Mark a variable for Vapor Secrets"},
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
	case "vapor-secret":
		return r.runVarVaporSecret(args[1:])
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
	reason := fs.String("reason", "", "Reason stored in signed local events")
	vaporSecret := fs.Bool("vapor-secret", false, "Mark this variable as a Laravel Vapor Secret")
	noVaporSecret := fs.Bool("no-vapor-secret", false, "Unmark this variable as a Laravel Vapor Secret")
	jsonOut := fs.Bool("json", false, "Print mutation result as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("vapor-secret", "no-vapor-secret", "json")); err != nil {
		return err
	}
	vaporSecretOverride, err := variableVaporSecretOverride(*vaporSecret, *noVaporSecret)
	if err != nil {
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

	if *file != "" {
		parsed, err := readDotenvEntries(repoFilePath(repo.Root, *file))
		if err != nil {
			return err
		}
		values := make(map[string]string, len(parsed.Entries))
		for key, entry := range parsed.Entries {
			values[key] = entry.Value
		}
		variableKey, err := r.selectKeyFromValues("Select a variable", values, *key)
		if err != nil {
			return err
		}
		entry, ok := parsed.Entries[variableKey]
		if !ok {
			return fmt.Errorf("%s was not found in %s", variableKey, *file)
		}
		if err := repo.SetVariableWithOptions(selected, variableKey, entry.Value, store.VariableWriteOptions{
			Reason:      *reason,
			Commented:   &entry.Disabled,
			VaporSecret: vaporSecretOverride,
		}); err != nil {
			return err
		}
		payload := map[string]interface{}{"environment": selected, "key": variableKey, "saved": true, "commented": entry.Disabled}
		if vaporSecretOverride != nil {
			payload["vaporSecret"] = *vaporSecretOverride
		}
		if *jsonOut {
			return printJSON(r.out, payload)
		}
		fmt.Fprintln(r.out, success(fmt.Sprintf("Saved %s in %s.", variableKey, selected)))
		return nil
	}

	variableKey, err := r.selectVariableKeyForPush(repo, selected, *key)
	if err != nil {
		return err
	}
	if !r.interactive {
		return fmt.Errorf("pass --file so the value does not appear in shell history")
	}
	value, err := r.prompts.Secret("Variable value")
	if err != nil {
		return err
	}

	if err := repo.SetVariableWithOptions(selected, variableKey, value, store.VariableWriteOptions{
		Reason:      *reason,
		VaporSecret: vaporSecretOverride,
	}); err != nil {
		return err
	}
	payload := map[string]interface{}{"environment": selected, "key": variableKey, "saved": true}
	if vaporSecretOverride != nil {
		payload["vaporSecret"] = *vaporSecretOverride
	}
	if *jsonOut {
		return printJSON(r.out, payload)
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Saved %s in %s.", variableKey, selected)))
	return nil
}

func (r *Runner) runVarVaporSecret(args []string) error {
	fs := newFlagSet("var vapor-secret", r.errOut)
	env := fs.String("env", "", "Environment name")
	key := fs.String("key", "", "Variable name")
	enabled := fs.Bool("enabled", true, "Whether this variable should be stored as a Vapor Secret")
	reason := fs.String("reason", "", "Reason stored in signed local events")
	jsonOut := fs.Bool("json", false, "Print mutation result as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("enabled", "json")); err != nil {
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
	if err := repo.SetVariableVaporSecret(selected, variableKey, *enabled, *reason); err != nil {
		return err
	}
	payload := map[string]interface{}{"environment": selected, "key": variableKey, "vaporSecret": *enabled}
	if *jsonOut {
		return printJSON(r.out, payload)
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("%s Vapor Secret is %s.", variableKey, enabledLabel(*enabled))))
	return nil
}

func variableVaporSecretOverride(vaporSecret bool, noVaporSecret bool) (*bool, error) {
	if vaporSecret && noVaporSecret {
		return nil, fmt.Errorf("pass either --vapor-secret or --no-vapor-secret, not both")
	}
	if vaporSecret {
		return &vaporSecret, nil
	}
	if noVaporSecret {
		enabled := false
		return &enabled, nil
	}
	return nil, nil
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
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
	reason := fs.String("reason", "", "Reason stored in signed local events")
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
	vaporSecret := variable.VaporSecret
	if err := repo.SetVariableWithOptions(target, variableKey, variable.Value, store.VariableWriteOptions{
		Reason:      *reason,
		Commented:   &commented,
		VaporSecret: &vaporSecret,
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
	return nil
}

func (r *Runner) runVarContext(args []string) error {
	fs := newFlagSet("var context", r.errOut)
	env := fs.String("env", "", "Environment name")
	key := fs.String("key", "", "Variable name")
	note := fs.String("note", "", "Replace the encrypted note for this variable")
	jsonOut := fs.Bool("json", false, "Print decrypted context as JSON")
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
	if *note != "" {
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
	if variable.Note == "" {
		fmt.Fprintln(r.out, warn(fmt.Sprintf("%s has no note.", variableKey)))
	} else {
		fmt.Fprintf(r.out, "%s note: %s\n", variableKey, variable.Note)
	}
	return nil
}

func (r *Runner) selectVariableKeyForPush(repo store.Repository, env string, provided string) (string, error) {
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
		return r.ask("Variable key", "", "", "key")
	}
	choices := append(keys, "New variable")
	selected, err := r.prompts.Select("Select a variable", choices, 0)
	if err != nil {
		return "", err
	}
	if selected == "New variable" {
		return r.ask("Variable key", "", "", "key")
	}
	return selected, nil
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
