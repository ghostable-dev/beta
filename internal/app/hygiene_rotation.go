package app

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	hygienepolicy "github.com/ghostable-dev/beta/internal/hygiene"
	"github.com/ghostable-dev/beta/internal/store"
)

var hygieneRotationCommandOptions = []commandOption{
	{Label: "list", Description: "List variable rotation hygiene rules"},
	{Label: "set", Description: "Set a project or environment rotation rule"},
	{Label: "remove", Description: "Remove a project or environment rotation rule"},
}

type hygieneRotationRuleEntry struct {
	Scope             string `json:"scope"`
	Environment       string `json:"environment,omitempty"`
	Key               string `json:"key"`
	RotationAfterDays int    `json:"rotationAfterDays"`
}

type hygieneRotationMutationResult struct {
	File string                   `json:"file"`
	Rule hygieneRotationRuleEntry `json:"rule"`
}

func (r *Runner) runHygieneRotation(args []string) error {
	if len(args) == 0 {
		if !r.interactive {
			r.printHygieneRotationHelp()
			return nil
		}
		selected, err := r.selectCommand("Select a rotation command", hygieneRotationCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if isHelpArg(args[0]) {
		r.printHygieneRotationHelp()
		return nil
	}

	switch args[0] {
	case "list", "show":
		return r.runHygieneRotationList(args[1:])
	case "set", "add", "update":
		return r.runHygieneRotationSet(args[1:])
	case "remove", "delete", "unset":
		return r.runHygieneRotationRemove(args[1:])
	default:
		return fmt.Errorf("unknown hygiene rotation command %q", args[0])
	}
}

func (r *Runner) printHygieneRotationHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable hygiene rotation <list|set|remove> [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, hygieneRotationCommandOptions)
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Examples:")
	fmt.Fprintln(r.out, "  ghostable hygiene rotation set --key STRIPE_SECRET_KEY --days 90")
	fmt.Fprintln(r.out, "  ghostable hygiene rotation set --env production --key STRIPE_SECRET_KEY --days 60")
	fmt.Fprintln(r.out, "  ghostable hygiene rotation list")
}

func (r *Runner) runHygieneRotationList(args []string) error {
	fs := newFlagSet("hygiene rotation list", r.errOut)
	jsonOut := fs.Bool("json", false, "Print rotation rules as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}

	_, policy, _, err := openHygienePolicy()
	if err != nil {
		return err
	}
	entries := hygieneRotationRuleEntries(policy)
	if *jsonOut {
		return printJSON(r.out, entries)
	}
	if len(entries) == 0 {
		fmt.Fprintln(r.out, warn("No rotation hygiene rules found."))
		return nil
	}
	for _, entry := range entries {
		scope := entry.Scope
		if entry.Environment != "" {
			scope = entry.Environment
		}
		fmt.Fprintf(r.out, "%s %s rotates after %s\n", scope, entry.Key, rotationDaysText(entry.RotationAfterDays))
	}
	return nil
}

func (r *Runner) runHygieneRotationSet(args []string) error {
	fs := newFlagSet("hygiene rotation set", r.errOut)
	env := fs.String("env", "", "Environment override scope; omit for project default")
	key := fs.String("key", "", "Variable key")
	days := fs.String("days", "", "Rotation interval in days, such as 90")
	jsonOut := fs.Bool("json", false, "Print mutation result as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}

	repo, policy, path, err := openHygienePolicy()
	if err != nil {
		return err
	}
	scope, selectedEnv, err := r.selectRotationRuleScope(repo, *env)
	if err != nil {
		return err
	}
	selectedKey, err := r.rotationRuleKey(*key)
	if err != nil {
		return err
	}
	rotationAfterDays, err := r.rotationRuleDays(*days)
	if err != nil {
		return err
	}

	rule := hygienepolicy.RotationRule{RotationAfterDays: rotationAfterDays}
	if scope == "environment" {
		hygienepolicy.SetEnvironmentRotationRule(&policy, selectedEnv, selectedKey, rule)
	} else {
		hygienepolicy.SetProjectRotationRule(&policy, selectedKey, rule)
	}
	if err := hygienepolicy.WriteFile(path, policy); err != nil {
		return err
	}

	entry := hygieneRotationRuleEntry{
		Scope:             scope,
		Environment:       selectedEnv,
		Key:               selectedKey,
		RotationAfterDays: rotationAfterDays,
	}
	result := hygieneRotationMutationResult{
		File: hygienepolicy.DefaultPolicyPath,
		Rule: entry,
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	fmt.Fprintf(r.out, "%s %s rotates after %s.\n", success("Set"), rotationRuleDisplay(entry), rotationDaysText(rotationAfterDays))
	return nil
}

func (r *Runner) runHygieneRotationRemove(args []string) error {
	fs := newFlagSet("hygiene rotation remove", r.errOut)
	env := fs.String("env", "", "Environment override scope; omit for project default")
	key := fs.String("key", "", "Variable key")
	jsonOut := fs.Bool("json", false, "Print mutation result as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}

	repo, policy, path, err := openHygienePolicy()
	if err != nil {
		return err
	}
	scope, selectedEnv, selectedKey, err := r.selectRotationRuleForRemoval(repo, policy, *env, *key)
	if err != nil {
		return err
	}
	removed := false
	if scope == "environment" {
		removed = hygienepolicy.RemoveEnvironmentRotationRule(&policy, selectedEnv, selectedKey)
	} else {
		removed = hygienepolicy.RemoveProjectRotationRule(&policy, selectedKey)
	}
	if !removed {
		return fmt.Errorf("rotation rule for %s was not found", selectedKey)
	}
	if err := hygienepolicy.WriteFile(path, policy); err != nil {
		return err
	}

	entry := hygieneRotationRuleEntry{
		Scope:       scope,
		Environment: selectedEnv,
		Key:         selectedKey,
	}
	result := hygieneRotationMutationResult{
		File: hygienepolicy.DefaultPolicyPath,
		Rule: entry,
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	fmt.Fprintf(r.out, "%s %s rotation rule.\n", success("Removed"), rotationRuleDisplay(entry))
	return nil
}

func openHygienePolicy() (store.Repository, hygienepolicy.Policy, string, error) {
	repo, err := store.OpenProject(".")
	if err != nil {
		return store.Repository{}, hygienepolicy.Policy{}, "", err
	}
	path := filepath.Join(repo.Root, hygienepolicy.DefaultPolicyPath)
	policy, err := hygienepolicy.LoadPolicy(repo.Root)
	if err != nil {
		return store.Repository{}, hygienepolicy.Policy{}, "", err
	}
	return repo, policy, path, nil
}

func hygieneRotationRuleEntries(policy hygienepolicy.Policy) []hygieneRotationRuleEntry {
	entries := []hygieneRotationRuleEntry{}
	keys := make([]string, 0, len(policy.Rotation.Keys))
	for key := range policy.Rotation.Keys {
		keys = append(keys, key)
	}
	sortStrings(keys)
	for _, key := range keys {
		rule := policy.Rotation.Keys[key]
		if rule.RotationAfterDays <= 0 {
			continue
		}
		entries = append(entries, hygieneRotationRuleEntry{
			Scope:             "project",
			Key:               key,
			RotationAfterDays: rule.RotationAfterDays,
		})
	}

	environments := make([]string, 0, len(policy.Rotation.Environments))
	for env := range policy.Rotation.Environments {
		environments = append(environments, env)
	}
	sortStrings(environments)
	for _, env := range environments {
		envPolicy := policy.Rotation.Environments[env]
		keys := make([]string, 0, len(envPolicy.Keys))
		for key := range envPolicy.Keys {
			keys = append(keys, key)
		}
		sortStrings(keys)
		for _, key := range keys {
			rule := envPolicy.Keys[key]
			if rule.RotationAfterDays <= 0 {
				continue
			}
			entries = append(entries, hygieneRotationRuleEntry{
				Scope:             "environment",
				Environment:       env,
				Key:               key,
				RotationAfterDays: rule.RotationAfterDays,
			})
		}
	}
	return entries
}

func (r *Runner) selectRotationRuleScope(repo store.Repository, providedEnv string) (string, string, error) {
	providedEnv = strings.TrimSpace(providedEnv)
	if providedEnv != "" {
		if _, ok := repo.Manifest.Environments[providedEnv]; !ok {
			return "", "", fmt.Errorf("environment %q was not found", providedEnv)
		}
		return "environment", providedEnv, nil
	}
	if !r.interactive {
		return "project", "", nil
	}

	scope, err := r.prompts.Select("Rotation rule scope", []string{"Project default", "Environment override"}, 0)
	if err != nil {
		return "", "", err
	}
	if scope == "Project default" {
		return "project", "", nil
	}
	env, err := r.selectEnvironment(repo, "")
	if err != nil {
		return "", "", err
	}
	return "environment", env, nil
}

func (r *Runner) selectRotationRuleForRemoval(repo store.Repository, policy hygienepolicy.Policy, providedEnv string, providedKey string) (string, string, string, error) {
	providedEnv = strings.TrimSpace(providedEnv)
	if providedEnv != "" {
		if _, ok := repo.Manifest.Environments[providedEnv]; !ok {
			return "", "", "", fmt.Errorf("environment %q was not found", providedEnv)
		}
	}
	if providedKey != "" {
		key, err := r.rotationRuleKey(providedKey)
		if err != nil {
			return "", "", "", err
		}
		if providedEnv != "" {
			return "environment", providedEnv, key, nil
		}
		return "project", "", key, nil
	}
	if !r.interactive {
		return "", "", "", fmt.Errorf("pass --key")
	}

	entries := hygieneRotationRuleEntries(policy)
	if providedEnv != "" {
		entries = filterRotationRuleEntriesByEnvironment(entries, providedEnv)
	}
	if len(entries) == 0 {
		return "", "", "", fmt.Errorf("no rotation hygiene rules were found")
	}
	choices := make([]string, 0, len(entries))
	entriesByChoice := map[string]hygieneRotationRuleEntry{}
	for _, entry := range entries {
		label := rotationRuleDisplay(entry) + " after " + rotationDaysText(entry.RotationAfterDays)
		choices = append(choices, label)
		entriesByChoice[label] = entry
	}
	selected, err := r.prompts.Select("Select a rotation rule", choices, 0)
	if err != nil {
		return "", "", "", err
	}
	entry := entriesByChoice[selected]
	return entry.Scope, entry.Environment, entry.Key, nil
}

func filterRotationRuleEntriesByEnvironment(entries []hygieneRotationRuleEntry, env string) []hygieneRotationRuleEntry {
	filtered := []hygieneRotationRuleEntry{}
	for _, entry := range entries {
		if entry.Scope == "environment" && entry.Environment == env {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func (r *Runner) rotationRuleKey(provided string) (string, error) {
	if strings.TrimSpace(provided) != "" {
		key, _, err := formatManualVariableKey(provided)
		return key, err
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --key")
	}
	return r.askManualVariableKey()
}

func (r *Runner) rotationRuleDays(provided string) (int, error) {
	value, err := r.ask("Rotation interval (days)", provided, "", "days")
	if err != nil {
		return 0, err
	}
	return parseRotationDays(value)
}

func parseRotationDays(value string) (int, error) {
	days, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("rotation interval must be a whole number of days")
	}
	if days <= 0 {
		return 0, fmt.Errorf("rotation interval must be greater than zero days")
	}
	return days, nil
}

func rotationRuleDisplay(entry hygieneRotationRuleEntry) string {
	if entry.Scope == "environment" {
		return entry.Environment + " " + entry.Key
	}
	return "project " + entry.Key
}

func rotationDaysText(days int) string {
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}
