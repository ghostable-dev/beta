package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/dotenv"
	"github.com/ghostable-dev/beta/internal/store"
)

const forgeCommandTimeout = 2 * time.Minute

type forgeDeployPlan struct {
	Target      string            `json:"target"`
	Provider    string            `json:"provider"`
	Environment string            `json:"environment"`
	ForgeSite   string            `json:"forgeSite"`
	DryRun      bool              `json:"dryRun"`
	Synced      bool              `json:"synced"`
	Variables   []string          `json:"variables"`
	Device      string            `json:"device"`
	Source      string            `json:"source,omitempty"`
	values      map[string]string `json:"-"`
}

func (r *Runner) runDeployForge(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printDeployForgeHelp()
		return nil
	}

	fs := newFlagSet("deploy laravel-forge", r.errOut)
	env := fs.String("env", "", "Ghostable environment name")
	forgeSite := fs.String("forge-site", "", "Laravel Forge site name")
	var only cli.Strings
	fs.Var(&only, "only", "Only include these keys")
	dryRun := fs.Bool("dry-run", false, "Show what would sync without invoking Laravel Forge")
	jsonOut := fs.Bool("json", false, "Print Laravel Forge deploy result as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("dry-run", "json"))
	if err != nil {
		return err
	}
	if len(positionals) > 1 {
		return fmt.Errorf("usage: ghostable deploy laravel-forge [environment] [options]")
	}
	if *env == "" && len(positionals) == 1 {
		*env = positionals[0]
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	selected, err := r.selectEnvironment(repo, *env)
	if err != nil {
		return err
	}

	site := strings.TrimSpace(*forgeSite)
	if site == "" {
		return fmt.Errorf("Laravel Forge site is required; pass --forge-site <SITE>")
	}

	plan, err := buildForgeDeployPlan(repo, selected, site, only)
	if err != nil {
		return err
	}
	plan.DryRun = *dryRun

	if *dryRun {
		if *jsonOut {
			return printJSON(r.out, plan)
		}
		r.printForgeDeployPlan(plan)
		return nil
	}

	forgePath, err := resolveForgeBinary(repo.Root)
	if err != nil {
		return err
	}
	if err := syncForgeDeployPlan(plan, forgePath); err != nil {
		return err
	}
	plan.Synced = true

	if *jsonOut {
		return printJSON(r.out, plan)
	}
	r.printForgeDeploySuccess(plan)
	return nil
}

func (r *Runner) printDeployForgeHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable deploy laravel-forge [environment] [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Sync decrypted values to a Laravel Forge site environment file using the Laravel Forge CLI.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Options:"))
	fmt.Fprintln(r.out, "  --env <ENV>          Ghostable environment name")
	fmt.Fprintln(r.out, "  --forge-site <SITE>  Laravel Forge site name")
	fmt.Fprintln(r.out, "  --only <KEYS>        Only include these keys; may be repeated or comma-separated")
	fmt.Fprintln(r.out, "  --dry-run            Show what would sync without invoking Laravel Forge")
	fmt.Fprintln(r.out, "  --json               Print Laravel Forge deploy result as JSON")
}

func buildForgeDeployPlan(repo store.Repository, env string, forgeSite string, only []string) (forgeDeployPlan, error) {
	variables, err := repo.ReadVariables(env)
	if err != nil {
		return forgeDeployPlan{}, err
	}

	plan := forgeDeployPlan{
		Target:      "laravel-forge",
		Provider:    "Laravel Forge",
		Environment: env,
		ForgeSite:   forgeSite,
		Device:      deployIdentityDisplay(repo),
		Source:      strings.TrimSpace(deployIdentitySource(repo)),
		values:      map[string]string{},
	}

	onlyKeys := deployOnlyKeySet(only)
	for _, key := range vaporVariableKeys(variables) {
		if len(onlyKeys) > 0 && !onlyKeys[key] {
			continue
		}
		variable := variables[key]
		plan.values[key] = variable.Value
		plan.Variables = append(plan.Variables, key)
	}

	return plan, nil
}

func syncForgeDeployPlan(plan forgeDeployPlan, forgePath string) error {
	envPath, err := createTemporaryForgeEnvironmentFile(plan.ForgeSite)
	if err != nil {
		return err
	}
	defer removeForgeEnvironmentFile(envPath)

	if err := runForgeCommand(plan, forgePath, "pull Forge environment", "env:pull", plan.ForgeSite, envPath); err != nil {
		return err
	}
	if err := ensureForgeEnvironmentFileIsRegular(envPath); err != nil {
		return err
	}

	existing := ""
	if content, err := os.ReadFile(envPath); err == nil {
		existing = string(content)
	} else if !os.IsNotExist(err) {
		return err
	}

	next, err := dotenv.Merge(existing, plan.values, plan.Variables, false)
	if err != nil {
		return err
	}
	if err := writeForgeEnvironmentFile(envPath, []byte(next)); err != nil {
		return err
	}

	if err := runForgeCommand(plan, forgePath, "push Forge environment", "env:push", plan.ForgeSite, envPath); err != nil {
		return err
	}
	return removeForgeEnvironmentFile(envPath)
}

func createTemporaryForgeEnvironmentFile(forgeSite string) (string, error) {
	temp, err := os.CreateTemp("", "ghostable-forge-"+safeTempFileSegment(forgeSite)+"-*.env")
	if err != nil {
		return "", err
	}
	path := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func safeTempFileSegment(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	result := strings.Trim(builder.String(), "-.")
	if result == "" {
		return "site"
	}
	return result
}

func ensureForgeEnvironmentFileIsRegular(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to use symlinked Forge environment file %s", path)
		}
		if info.IsDir() {
			return fmt.Errorf("Forge environment file %s is a directory", path)
		}
		return nil
	}
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func writeForgeEnvironmentFile(path string, content []byte) error {
	if err := ensureForgeEnvironmentFileIsRegular(path); err != nil {
		return err
	}
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

func removeForgeEnvironmentFile(path string) error {
	if err := ensureForgeEnvironmentFileIsRegular(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func resolveForgeBinary(projectRoot string) (string, error) {
	path, err := exec.LookPath("forge")
	if err != nil {
		return "", fmt.Errorf("Laravel Forge CLI not found on PATH; install it with `composer global require laravel/forge-cli` before running `ghostable deploy laravel-forge`")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if binaryInsideProject(projectRoot, absolutePath) {
		return "", fmt.Errorf("refusing to run Laravel Forge CLI from project path %s; put a trusted Forge executable earlier on PATH outside this repository", absolutePath)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("Laravel Forge CLI path %s is a directory", absolutePath)
	}
	return absolutePath, nil
}

func runForgeCommand(plan forgeDeployPlan, forgePath string, action string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), forgeCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, forgePath, args...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s: Forge CLI timed out", action)
	}
	if err != nil {
		detail := sanitizeForgeCommandOutput(string(output), plan.values)
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("%s: %s", action, detail)
	}
	return nil
}

func sanitizeForgeCommandOutput(output string, values map[string]string) string {
	detail := strings.TrimSpace(output)
	for _, value := range values {
		if value != "" {
			detail = strings.ReplaceAll(detail, value, "[redacted]")
		}
	}
	return detail
}

func (r *Runner) printForgeDeployPlan(plan forgeDeployPlan) {
	fmt.Fprintln(r.out, success("👻 Ghostable Laravel Forge deploy plan."))
	printForgeDeployDetails(r, plan)
}

func (r *Runner) printForgeDeploySuccess(plan forgeDeployPlan) {
	fmt.Fprintln(r.out, success("👻 Ghostable Laravel Forge deploy successful."))
	printForgeDeployDetails(r, plan)
}

func printForgeDeployDetails(r *Runner, plan forgeDeployPlan) {
	printDeployDetail(r.out, "Environment", plan.Environment)
	printDeployDetail(r.out, "Forge site", plan.ForgeSite)
	printDeployDetail(r.out, "Variables", deployVariableCount(len(plan.Variables)))
	printDeployDetail(r.out, "Device", plan.Device)
	if plan.Source != "" {
		printDeployDetail(r.out, "Source", plan.Source)
	}
}
