package app

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/dotenv"
	"github.com/ghostable-dev/beta/internal/review"
	"github.com/ghostable-dev/beta/internal/store"
	"github.com/ghostable-dev/beta/internal/validation"
)

var exampleCommandOptions = []commandOption{
	{Label: "generate", Description: "Create or update .env.example"},
}

type exampleGenerateResult struct {
	File         string   `json:"file"`
	Environments []string `json:"environments"`
	ValueMode    string   `json:"valueMode"`
	Keys         []string `json:"keys"`
	Added        []string `json:"added"`
	Existing     []string `json:"existing"`
	Removed      []string `json:"removed,omitempty"`
	DryRun       bool     `json:"dryRun"`
	Written      bool     `json:"written"`
}

func (r *Runner) runExample(args []string) error {
	if len(args) == 0 {
		if !r.interactive {
			r.printExampleHelp()
			return nil
		}
		selected, err := r.selectCommand("Select an example command", exampleCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if isHelpArg(args[0]) {
		r.printExampleHelp()
		return nil
	}

	switch args[0] {
	case "generate":
		return r.runExampleGenerate(args[1:])
	default:
		return fmt.Errorf("unknown example command %q", args[0])
	}
}

func (r *Runner) printExampleHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable example <command> [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, exampleCommandOptions)
}

func (r *Runner) runExampleGenerate(args []string) error {
	fs := newFlagSet("example generate", r.errOut)
	file := fs.String("file", ".env.example", "Example env file path")
	valueMode := fs.String("values", "non-sensitive", "Value mode: blank, non-sensitive, or all")
	replace := fs.Bool("replace", false, "Replace the file with only discovered keys")
	prune := fs.Bool("prune", false, "Remove existing keys that are not discovered")
	noPrune := fs.Bool("no-prune", false, "Keep existing keys that are not discovered")
	dryRun := fs.Bool("dry-run", false, "Print generated content without writing")
	jsonOut := fs.Bool("json", false, "Print generation result as JSON")
	var environments cli.Strings
	fs.Var(&environments, "env", "Environment to include; may be repeated or comma-separated")
	pruneProvided := hasFlag(args, "prune")
	noPruneProvided := hasFlag(args, "no-prune")
	replaceProvided := hasFlag(args, "replace")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("replace", "prune", "no-prune", "dry-run", "json")); err != nil {
		return err
	}
	if *prune && *noPrune {
		return fmt.Errorf("pass either --prune or --no-prune, not both")
	}
	if *replace && *noPrune {
		return fmt.Errorf("--replace removes undiscovered keys; pass --prune or --replace without --no-prune")
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	envs, err := resolveSelectedEnvironments(repo, environments)
	if err != nil {
		return err
	}
	selectedValueMode, err := normalizeExampleValueMode(*valueMode)
	if err != nil {
		return err
	}
	pruneMissing := *prune || *replace
	if r.shouldPromptForExamplePruning(*dryRun, *jsonOut, pruneProvided, noPruneProvided, replaceProvided) {
		prunableKeys, err := examplePrunableKeys(repo, *file, envs, selectedValueMode)
		if err != nil {
			return err
		}
		if len(prunableKeys) > 0 {
			fmt.Fprintf(r.out, "%s %s\n", warn("Stale example keys:"), strings.Join(prunableKeys, ", "))
			label := fmt.Sprintf("Prune %s from %s?", keyCountText(len(prunableKeys)), *file)
			pruneMissing, err = r.prompts.Confirm(label, false)
			if err != nil {
				return err
			}
			r.printPromptAnswer(label, yesNo(pruneMissing))
		}
	}

	content, result, err := generateExampleFile(repo, exampleGenerateOptions{
		File:         *file,
		Environments: envs,
		Replace:      *replace,
		Prune:        pruneMissing,
		Write:        !*dryRun,
		ValueMode:    selectedValueMode,
	})
	if err != nil {
		return err
	}
	result.DryRun = *dryRun

	if *dryRun {
		if *jsonOut {
			return printJSON(r.out, result)
		}
		fmt.Fprint(r.out, content)
		return nil
	}

	if *jsonOut {
		return printJSON(r.out, result)
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Generated %s with %s.", *file, keyCountText(len(result.Keys)))))
	if len(result.Added) > 0 {
		fmt.Fprintf(r.out, "%s %s\n", warn("Added:"), strings.Join(result.Added, ", "))
	}
	if len(result.Removed) > 0 {
		fmt.Fprintf(r.out, "%s %s\n", warn("Removed:"), strings.Join(result.Removed, ", "))
	}
	return nil
}

func (r *Runner) shouldPromptForExamplePruning(dryRun bool, jsonOut bool, pruneProvided bool, noPruneProvided bool, replaceProvided bool) bool {
	if dryRun || jsonOut || !r.interactive {
		return false
	}
	return !pruneProvided && !noPruneProvided && !replaceProvided
}

func (r *Runner) maybePromptGenerateExample(repo store.Repository, keysChanged bool, removeMissing bool, jsonOut bool) error {
	if !keysChanged || !r.interactive || jsonOut {
		return nil
	}

	label := "Update .env.example now?"
	updateExample, err := r.prompts.Confirm(label, true)
	if err != nil {
		return err
	}
	r.printPromptAnswer(label, yesNo(updateExample))
	if !updateExample {
		return nil
	}

	_, result, err := generateExampleFile(repo, exampleGenerateOptions{
		File:      ".env.example",
		Prune:     removeMissing,
		Write:     true,
		ValueMode: exampleValuesNonSensitive,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Generated .env.example with %s.", keyCountText(len(result.Keys)))))
	if len(result.Added) > 0 {
		fmt.Fprintf(r.out, "%s %s\n", warn("Added:"), strings.Join(result.Added, ", "))
	}
	if len(result.Removed) > 0 {
		fmt.Fprintf(r.out, "%s %s\n", warn("Removed:"), strings.Join(result.Removed, ", "))
	}
	return nil
}

type exampleGenerateOptions struct {
	File         string
	Environments []string
	Replace      bool
	Prune        bool
	Write        bool
	ValueMode    string
}

func generateExampleFile(repo store.Repository, options exampleGenerateOptions) (string, exampleGenerateResult, error) {
	file := options.File
	if file == "" {
		file = ".env.example"
	}
	envs := options.Environments
	if len(envs) == 0 {
		var err error
		envs, err = resolveSelectedEnvironments(repo, nil)
		if err != nil {
			return "", exampleGenerateResult{}, err
		}
	}
	valueMode, err := normalizeExampleValueMode(options.ValueMode)
	if err != nil {
		return "", exampleGenerateResult{}, err
	}
	data, err := collectExampleData(repo, envs, valueMode)
	if err != nil {
		return "", exampleGenerateResult{}, err
	}

	path, err := resolveEnvFileSavePath(repo.Root, file)
	if err != nil {
		return "", exampleGenerateResult{}, err
	}
	existing := ""
	if content, err := os.ReadFile(path); err == nil {
		existing = string(content)
	} else if !os.IsNotExist(err) {
		return "", exampleGenerateResult{}, err
	}

	content, result, err := buildExampleFileContent(file, existing, data.Keys, data.Values, options.Replace, options.Prune)
	if err != nil {
		return "", exampleGenerateResult{}, err
	}
	result.Environments = envs
	result.ValueMode = valueMode
	if options.Write {
		if err := writeEnvFileSave(path, []byte(content)); err != nil {
			return "", exampleGenerateResult{}, err
		}
		result.Written = true
	}
	return content, result, nil
}

func examplePrunableKeys(repo store.Repository, file string, envs []string, valueMode string) ([]string, error) {
	if len(envs) == 0 {
		var err error
		envs, err = resolveSelectedEnvironments(repo, nil)
		if err != nil {
			return nil, err
		}
	}
	valueMode, err := normalizeExampleValueMode(valueMode)
	if err != nil {
		return nil, err
	}
	data, err := collectExampleData(repo, envs, valueMode)
	if err != nil {
		return nil, err
	}
	path, err := resolveEnvFileSavePath(repo.Root, file)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(content)) == "" {
		return nil, nil
	}
	existingKeys, err := parseExampleKeys(string(content))
	if err != nil {
		return nil, err
	}
	keySet := map[string]bool{}
	for _, key := range data.Keys {
		keySet[key] = true
	}
	prunableKeys := []string{}
	for _, key := range existingKeys {
		if !keySet[key] {
			prunableKeys = append(prunableKeys, key)
		}
	}
	return prunableKeys, nil
}

const (
	exampleValuesBlank        = "blank"
	exampleValuesNonSensitive = "non-sensitive"
	exampleValuesAll          = "all"
)

type exampleData struct {
	Keys   []string
	Values map[string]string
}

func resolveSelectedEnvironments(repo store.Repository, requested []string) ([]string, error) {
	if len(requested) == 0 {
		envs := repo.Environments()
		names := make([]string, 0, len(envs))
		for _, env := range envs {
			names = append(names, env.Name)
		}
		sort.Strings(names)
		return names, nil
	}

	names := uniqueAppStrings(requested)
	for _, name := range names {
		if _, ok := repo.Manifest.Environments[name]; !ok {
			return nil, fmt.Errorf("environment %q was not found", name)
		}
	}
	return names, nil
}

func collectExampleData(repo store.Repository, envs []string, valueMode string) (exampleData, error) {
	keySet := map[string]bool{}
	valueCandidates := map[string]string{}
	valueConflicts := map[string]bool{}
	for _, env := range envs {
		metadata, err := repo.ReadVariableMetadata(env)
		if err != nil {
			return exampleData{}, err
		}
		for _, variable := range metadata {
			keySet[variable.Key] = true
		}
		if valueMode != exampleValuesBlank {
			variables, err := repo.ReadVariables(env)
			if err != nil {
				continue
			}
			for _, variable := range variables {
				if !exampleVariableValueAllowed(variable, valueMode) {
					continue
				}
				previous, exists := valueCandidates[variable.Key]
				if exists && previous != variable.Value {
					valueConflicts[variable.Key] = true
					continue
				}
				valueCandidates[variable.Key] = variable.Value
			}
		}

		rules, _, err := validation.LoadRules(repo.Root, env)
		if err != nil {
			return exampleData{}, err
		}
		for key := range rules {
			keySet[key] = true
		}
	}

	references, err := review.ScanReferences(review.ReferenceScanInput{
		Root:    repo.Root,
		Ignores: repo.Manifest.ScanIgnores,
	})
	if err != nil {
		return exampleData{}, err
	}
	for _, reference := range references {
		keySet[reference.Key] = true
	}

	keys := make([]string, 0, len(keySet))
	for key := range keySet {
		if dotenv.IsValidKey(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	values := map[string]string{}
	for key, value := range valueCandidates {
		if valueConflicts[key] || !keySet[key] {
			continue
		}
		values[key] = value
	}
	return exampleData{Keys: keys, Values: values}, nil
}

func buildExampleFileContent(file string, existing string, keys []string, values map[string]string, replace bool, prune bool) (string, exampleGenerateResult, error) {
	keySet := map[string]bool{}
	for _, key := range keys {
		keySet[key] = true
	}
	result := exampleGenerateResult{
		File:     file,
		Keys:     append([]string{}, keys...),
		Added:    []string{},
		Existing: []string{},
		Removed:  []string{},
	}

	if replace || strings.TrimSpace(existing) == "" {
		if strings.TrimSpace(existing) != "" {
			existingKeys, err := parseExampleKeys(existing)
			if err != nil {
				return "", result, err
			}
			for _, key := range existingKeys {
				if !keySet[key] {
					result.Removed = append(result.Removed, key)
				} else {
					result.Existing = append(result.Existing, key)
				}
			}
		}
		result.Added = difference(keys, result.Existing)
		return renderExampleKeys(keys, values), result, nil
	}

	parsed, err := dotenv.Parse(strings.NewReader(existing))
	if err != nil {
		return "", result, err
	}
	existingSet := map[string]bool{}
	removeLines := map[int]bool{}
	for key := range parsed.Entries {
		entry := parsed.Entries[key]
		existingSet[key] = true
		if keySet[key] {
			result.Existing = append(result.Existing, key)
			continue
		}
		if prune {
			result.Removed = append(result.Removed, key)
			removeLines[entry.Line] = true
		}
	}
	sort.Strings(result.Existing)
	sort.Strings(result.Removed)

	lines := make([]string, 0, len(parsed.Lines))
	for index, line := range parsed.Lines {
		if removeLines[index+1] {
			continue
		}
		lines = append(lines, line)
	}
	for _, key := range keys {
		value, hasExampleValue := values[key]
		if existingSet[key] {
			entry := parsed.Entries[key]
			if hasExampleValue && entry.Value == "" {
				lines[adjustedExampleLineIndex(entry.Line, removeLines)-1] = formatExampleEntry(entry, value)
			}
			continue
		}
		lines = append(lines, formatExampleKeyValue(key, value, hasExampleValue))
		result.Added = append(result.Added, key)
	}
	return strings.Join(lines, "\n") + "\n", result, nil
}

func adjustedExampleLineIndex(line int, removedLines map[int]bool) int {
	adjusted := line
	for removedLine := range removedLines {
		if removedLine < line {
			adjusted--
		}
	}
	return adjusted
}

func parseExampleKeys(content string) ([]string, error) {
	parsed, err := dotenv.Parse(strings.NewReader(content))
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(parsed.Entries))
	for key := range parsed.Entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func renderExampleKeys(keys []string, values map[string]string) string {
	if len(keys) == 0 {
		return ""
	}
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		value, ok := values[key]
		lines = append(lines, formatExampleKeyValue(key, value, ok))
	}
	return strings.Join(lines, "\n") + "\n"
}

func formatExampleEntry(entry dotenv.Entry, value string) string {
	prefix := ""
	if entry.Disabled {
		prefix = "# "
	}
	if entry.Export {
		prefix += "export "
	}
	return prefix + formatExampleKeyValue(entry.Key, value, true)
}

func formatExampleKeyValue(key string, value string, hasValue bool) string {
	if !hasValue || value == "" {
		return key + "="
	}
	return key + "=" + dotenv.FormatValue(value)
}

func normalizeExampleValueMode(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "non-sensitive", "nonsensitive", "safe":
		return exampleValuesNonSensitive, nil
	case "blank", "none", "empty", "keys-only", "key-only":
		return exampleValuesBlank, nil
	case "all":
		return exampleValuesAll, nil
	default:
		return "", fmt.Errorf("invalid example value mode %q; use blank, non-sensitive, or all", value)
	}
}

func exampleVariableValueAllowed(variable domain.Variable, mode string) bool {
	if mode == exampleValuesAll {
		return true
	}
	if mode == exampleValuesBlank {
		return false
	}
	if looksSensitiveSeedKey(variable.Key) || looksSensitiveExampleValue(variable.Value) {
		return false
	}
	return true
}

func looksSensitiveExampleValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{"sk-", "ghp_", "github_pat_", "xoxb-", "xoxp-", "whsec_"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if strings.Contains(trimmed, "://") && strings.Contains(trimmed, "@") {
		beforeHost := strings.SplitN(trimmed, "@", 2)[0]
		if strings.Contains(beforeHost, ":") {
			return true
		}
	}
	return false
}

func difference(values []string, existing []string) []string {
	seen := map[string]bool{}
	for _, value := range existing {
		seen[value] = true
	}
	result := []string{}
	for _, value := range values {
		if !seen[value] {
			result = append(result, value)
		}
	}
	return result
}

func uniqueAppStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
