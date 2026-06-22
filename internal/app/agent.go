package app

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/store"
)

const agentsBlockStart = "<!-- ghostable:start -->"
const agentsBlockEnd = "<!-- ghostable:end -->"

var agentCommandOptions = []commandOption{
	{Label: "instructions", Description: "Print agent guidance"},
	{Label: "capabilities", Description: "List machine-readable capabilities"},
	{Label: "init", Description: "Write AGENTS.md guidance"},
}

func (r *Runner) runAgent(args []string) error {
	if len(args) == 0 {
		if !r.interactive {
			r.printAgentHelp()
			return nil
		}
		selected, err := r.selectCommand("Select an agent command", agentCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if isHelpArg(args[0]) {
		r.printAgentHelp()
		return nil
	}

	switch args[0] {
	case "instructions":
		fmt.Fprint(r.out, agentInstructions())
	case "capabilities":
		return r.runAgentCapabilities(args[1:])
	case "init":
		return r.runAgentInit(args[1:])
	default:
		return fmt.Errorf("unknown agent command %q", args[0])
	}
	return nil
}

func (r *Runner) printAgentHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable agent <init|instructions|capabilities> [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, agentCommandOptions)
}

func (r *Runner) runAgentCapabilities(args []string) error {
	fs := newFlagSet("agent capabilities", r.errOut)
	jsonOut := fs.Bool("json", false, "Print capabilities as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}
	capabilities := map[string]interface{}{
		"schema": "ghostable.agent-capabilities.v1",
		"commands": []string{
			"status --json",
			"env list --json",
			"env diff --json",
			"validate --json",
			"env push --json --reason",
			"env pull --json",
			"deploy <env> --json",
			"deploy vapor <env> --dry-run --json",
			"var push --json --reason",
			"var vapor-secret --env <env> --key <key> --json",
			"scan --json",
			"access create --name <name> --kind ci --grant <env>:reader --json",
		},
		"safety": []string{
			"Do not print secrets unless the user explicitly asks for --show-values.",
			"Prefer JSON output for machine-readable workflows.",
			"Record a --reason when changing encrypted state.",
			"Run validate before pushing significant changes.",
			"Treat .env files as local-only unless the repository policy says otherwise.",
		},
	}
	if *jsonOut {
		return printJSON(r.out, capabilities)
	}
	for _, command := range capabilities["commands"].([]string) {
		fmt.Fprintf(r.out, "ghostable %s\n", success(command))
	}
	return nil
}

func (r *Runner) runAgentInit(args []string) error {
	fs := newFlagSet("agent init", r.errOut)
	dryRun := fs.Bool("dry-run", false, "Print the AGENTS.md content without writing")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("dry-run")); err != nil {
		return err
	}
	path := "AGENTS.md"
	existingBytes, _ := os.ReadFile(path)
	existing := string(existingBytes)
	block := agentsBlockStart + "\n" + agentInstructions() + agentsBlockEnd + "\n"
	next := upsertBlock(existing, block)
	if *dryRun {
		fmt.Fprint(r.out, next)
		return nil
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(r.out, success("Updated AGENTS.md."))
	return nil
}

func (r *Runner) runAutomationCredentialCreate(args []string, defaultKind string, commandName string) error {
	fs := newFlagSet(commandName, r.errOut)
	name := fs.String("name", "", "Credential label")
	kind := fs.String("kind", "", "Credential use: deploy, ci, access")
	var grants cli.Strings
	fs.Var(&grants, "grant", "Environment permission ENV:reader or ENV:writer; may be repeated")
	jsonOut := fs.Bool("json", false, "Print credential creation result as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	credentialName, err := r.ask("Credential name", *name, "", "name")
	if err != nil {
		return err
	}
	selectedKind, err := r.selectChoice("Select credential kind", []string{"deploy", "ci", "access"}, *kind, defaultKind, "kind")
	if err != nil {
		return err
	}
	permissions, err := r.resolveCredentialGrants(repo, grants)
	if err != nil {
		return err
	}

	result, err := repo.CreateAutomationCredential(credentialName, selectedKind, permissions)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}

	fmt.Fprintln(r.out, success(fmt.Sprintf("Created %s credential.", credentialName)))
	printAccessDetailRows(r.out, []accessDetailRow{
		{Label: "Credential", Value: result.Credential.Name},
		{Label: "Kind", Value: result.Credential.Kind},
		{Label: "Device ID", Value: result.Credential.DeviceID},
		{Label: "Status", Value: deviceStatusDisplay(result.Credential.Status)},
		{Label: "Created", Value: historyTimeDisplay(result.Credential.CreatedAt)},
	})
	fmt.Fprintln(r.out)
	printCredentialPermissionRows(r.out, result.Credential.Permissions)
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, result.Token)
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Store this token as GHOSTABLE_CI_TOKEN. It contains the credential private keys and will not be shown again."))
	return nil
}

func (r *Runner) resolveCredentialGrants(repo store.Repository, inputs []string) ([]store.AutomationCredentialGrant, error) {
	if len(inputs) > 0 {
		grants := []store.AutomationCredentialGrant{}
		for _, input := range inputs {
			parsed, err := parseCredentialGrant(input, environmentNames(repo))
			if err != nil {
				return nil, err
			}
			grants = append(grants, parsed...)
		}
		return grants, nil
	}
	if !r.interactive {
		return nil, fmt.Errorf("pass at least one --grant ENV:reader or --grant ENV:writer")
	}

	envs := environmentNames(repo)
	if len(envs) == 0 {
		return nil, fmt.Errorf("no environments are configured")
	}
	choices := append([]string{"all"}, envs...)
	grants := []store.AutomationCredentialGrant{}
	for {
		selectedEnv, err := r.prompts.Select("Select an environment to grant", choices, 0)
		if err != nil {
			return nil, err
		}
		selectedRole, err := r.selectChoice("Select credential access", []string{"reader", "writer"}, "", "reader", "role")
		if err != nil {
			return nil, err
		}
		if selectedEnv == "all" {
			for _, env := range envs {
				grants = append(grants, store.AutomationCredentialGrant{EnvironmentName: env, Role: selectedRole})
			}
			return grants, nil
		}
		grants = append(grants, store.AutomationCredentialGrant{EnvironmentName: selectedEnv, Role: selectedRole})

		remaining := remainingCredentialEnvironmentChoices(envs, grants)
		if len(remaining) == 0 {
			return grants, nil
		}
		next, err := r.prompts.Select("Add another environment grant?", []string{"No", "Yes"}, 0)
		if err != nil {
			return nil, err
		}
		if next != "Yes" {
			return grants, nil
		}
		choices = remaining
	}
}

func parseCredentialGrant(input string, envNames []string) ([]store.AutomationCredentialGrant, error) {
	env, role, _ := strings.Cut(input, ":")
	env = strings.TrimSpace(env)
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		role = "reader"
	}
	if env == "" || strings.Contains(role, ":") {
		return nil, fmt.Errorf("invalid --grant %q; use ENV:reader or ENV:writer", input)
	}
	if !oneOfString(role, "reader", "writer") {
		return nil, fmt.Errorf("invalid credential role %q; use reader or writer", role)
	}
	if env == "all" {
		grants := make([]store.AutomationCredentialGrant, 0, len(envNames))
		for _, envName := range envNames {
			grants = append(grants, store.AutomationCredentialGrant{EnvironmentName: envName, Role: role})
		}
		return grants, nil
	}
	if !oneOfString(env, envNames...) {
		return nil, fmt.Errorf("environment %q is not defined in .ghostable/ghostable.yaml", env)
	}
	return []store.AutomationCredentialGrant{{EnvironmentName: env, Role: role}}, nil
}

func environmentNames(repo store.Repository) []string {
	envs := repo.Environments()
	names := make([]string, 0, len(envs))
	for _, env := range envs {
		names = append(names, env.Name)
	}
	return names
}

func remainingCredentialEnvironmentChoices(envs []string, grants []store.AutomationCredentialGrant) []string {
	used := map[string]bool{}
	for _, grant := range grants {
		used[grant.EnvironmentName] = true
	}
	remaining := []string{}
	for _, env := range envs {
		if !used[env] {
			remaining = append(remaining, env)
		}
	}
	return remaining
}

func oneOfString(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func printCredentialPermissionRows(out io.Writer, grants []store.AutomationCredentialGrant) {
	if len(grants) == 0 {
		fmt.Fprintln(out, warn("No permissions granted."))
		return
	}

	envWidth := len("Environment")
	roleWidth := len("Role")
	for _, grant := range grants {
		envWidth = max(envWidth, len(grant.EnvironmentName))
		roleWidth = max(roleWidth, len(grant.Role))
	}

	header := fmt.Sprintf("%-*s  %s", envWidth, "Environment", "Role")
	fmt.Fprintln(out, warn(header))
	fmt.Fprintf(out, "%-*s  %s\n", envWidth, strings.Repeat("-", envWidth), strings.Repeat("-", roleWidth))
	for _, grant := range grants {
		fmt.Fprintf(out, "%s  %s\n",
			coloredCell(grant.EnvironmentName, envWidth, success),
			coloredCell(grant.Role, roleWidth, success),
		)
	}
}

func agentInstructions() string {
	return strings.Join([]string{
		"# Ghostable",
		"",
		"Use Ghostable for local encrypted environment management.",
		"",
		"- Prefer `ghostable ... --json` for machine-readable workflows.",
		"- Do not print secret values unless the user explicitly asks for `--show-values`.",
		"- Prefer `ghostable env diff --env <env> --json` before changing encrypted state.",
		"- Include `--reason` when pushing, syncing, deleting, or promoting variables.",
		"- Run `ghostable validate --env <env> --json` before commits that change `.ghostable`.",
		"- Run `ghostable scan --json` before commits to catch hard-coded secrets.",
		"- Keep local `.env*` files out of git unless the project explicitly documents otherwise.",
		"",
	}, "\n")
}

func upsertBlock(existing string, block string) string {
	start := strings.Index(existing, agentsBlockStart)
	end := strings.Index(existing, agentsBlockEnd)
	if start >= 0 && end > start {
		end += len(agentsBlockEnd)
		next := existing[:start] + strings.TrimRight(block, "\n") + existing[end:]
		return strings.TrimRight(next, "\n") + "\n"
	}
	if strings.TrimSpace(existing) == "" {
		return block
	}
	return strings.TrimRight(existing, "\n") + "\n\n" + block
}
