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
	"github.com/ghostable-dev/beta/internal/store"
)

const cloudCommandTimeout = 2 * time.Minute

type cloudDeployPlan struct {
	Target           string            `json:"target"`
	Provider         string            `json:"provider"`
	Environment      string            `json:"environment"`
	CloudEnvironment string            `json:"cloudEnvironment"`
	DryRun           bool              `json:"dryRun"`
	Synced           bool              `json:"synced"`
	Variables        []string          `json:"variables"`
	Device           string            `json:"device"`
	Source           string            `json:"source,omitempty"`
	values           map[string]string `json:"-"`
}

func (r *Runner) runDeployCloud(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printDeployCloudHelp()
		return nil
	}

	fs := newFlagSet("deploy laravel-cloud", r.errOut)
	env := fs.String("env", "", "Ghostable environment name")
	cloudEnv := fs.String("cloud-env", "", "Laravel Cloud environment ID or name")
	var only cli.Strings
	fs.Var(&only, "only", "Only include these keys")
	dryRun := fs.Bool("dry-run", false, "Show what would sync without invoking Laravel Cloud")
	jsonOut := fs.Bool("json", false, "Print Laravel Cloud deploy result as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("dry-run", "json"))
	if err != nil {
		return err
	}
	if len(positionals) > 1 {
		return fmt.Errorf("usage: ghostable deploy laravel-cloud [environment] [options]")
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

	cloudEnvironment := strings.TrimSpace(*cloudEnv)
	if cloudEnvironment == "" {
		cloudEnvironment = selected
	}
	if cloudEnvironment == "" {
		return fmt.Errorf("Laravel Cloud environment is required")
	}

	plan, err := buildCloudDeployPlan(repo, selected, cloudEnvironment, only)
	if err != nil {
		return err
	}
	plan.DryRun = *dryRun

	if *dryRun {
		if *jsonOut {
			return printJSON(r.out, plan)
		}
		r.printCloudDeployPlan(plan)
		return nil
	}

	cloudPath, err := resolveCloudBinary(repo.Root)
	if err != nil {
		return err
	}
	if err := syncCloudDeployPlan(plan, cloudPath); err != nil {
		return err
	}
	plan.Synced = true

	if *jsonOut {
		return printJSON(r.out, plan)
	}
	r.printCloudDeploySuccess(plan)
	return nil
}

func (r *Runner) printDeployCloudHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable deploy laravel-cloud [environment] [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Sync decrypted values to Laravel Cloud environment variables using the Laravel Cloud CLI.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Options:"))
	fmt.Fprintln(r.out, "  --env <ENV>         Ghostable environment name")
	fmt.Fprintln(r.out, "  --cloud-env <ENV>   Laravel Cloud environment ID or name (defaults to the Ghostable environment)")
	fmt.Fprintln(r.out, "  --only <KEYS>       Only include these keys; may be repeated or comma-separated")
	fmt.Fprintln(r.out, "  --dry-run           Show what would sync without invoking Laravel Cloud")
	fmt.Fprintln(r.out, "  --json              Print Laravel Cloud deploy result as JSON")
}

func buildCloudDeployPlan(repo store.Repository, env string, cloudEnv string, only []string) (cloudDeployPlan, error) {
	variables, err := repo.ReadVariables(env)
	if err != nil {
		return cloudDeployPlan{}, err
	}

	plan := cloudDeployPlan{
		Target:           "laravel-cloud",
		Provider:         "Laravel Cloud",
		Environment:      env,
		CloudEnvironment: cloudEnv,
		Device:           deployIdentityDisplay(repo),
		Source:           strings.TrimSpace(deployIdentitySource(repo)),
		values:           map[string]string{},
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

func deployOnlyKeySet(keys []string) map[string]bool {
	result := map[string]bool{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			result[key] = true
		}
	}
	return result
}

func syncCloudDeployPlan(plan cloudDeployPlan, cloudPath string) error {
	for _, key := range plan.Variables {
		if err := syncCloudEnvironmentVariable(cloudPath, plan.CloudEnvironment, key, plan.values[key]); err != nil {
			return err
		}
	}
	return nil
}

func syncCloudEnvironmentVariable(cloudPath string, cloudEnv string, key string, value string) error {
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("sync Laravel Cloud variable %s: value contains a NUL byte", key)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cloudCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		cloudPath,
		"environment:variables",
		cloudEnv,
		"--json",
		"--no-interaction",
		"--action=set",
		"--key="+key,
		"--value="+value,
	)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("sync Laravel Cloud variable %s: Cloud CLI timed out", key)
	}
	if err != nil {
		detail := sanitizeCloudCommandOutput(string(output), value)
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("sync Laravel Cloud variable %s: %s", key, detail)
	}
	return nil
}

func sanitizeCloudCommandOutput(output string, sensitiveValue string) string {
	detail := strings.TrimSpace(output)
	if detail == "" || sensitiveValue == "" {
		return detail
	}
	return strings.ReplaceAll(detail, sensitiveValue, "[redacted]")
}

func resolveCloudBinary(projectRoot string) (string, error) {
	path, err := exec.LookPath("cloud")
	if err != nil {
		return "", fmt.Errorf("Laravel Cloud CLI not found on PATH; install it with `composer global require laravel/cloud-cli` before running `ghostable deploy laravel-cloud`")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if binaryInsideProject(projectRoot, absolutePath) {
		return "", fmt.Errorf("refusing to run Laravel Cloud CLI from project path %s; put a trusted Cloud executable earlier on PATH outside this repository", absolutePath)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("Laravel Cloud CLI path %s is a directory", absolutePath)
	}
	return absolutePath, nil
}

func (r *Runner) printCloudDeployPlan(plan cloudDeployPlan) {
	fmt.Fprintln(r.out, success("👻 Ghostable Laravel Cloud deploy plan."))
	printCloudDeployDetails(r, plan)
}

func (r *Runner) printCloudDeploySuccess(plan cloudDeployPlan) {
	fmt.Fprintln(r.out, success("👻 Ghostable Laravel Cloud deploy successful."))
	printCloudDeployDetails(r, plan)
}

func printCloudDeployDetails(r *Runner, plan cloudDeployPlan) {
	printDeployDetail(r.out, "Environment", plan.Environment)
	printDeployDetail(r.out, "Cloud environment", plan.CloudEnvironment)
	printDeployDetail(r.out, "Variables", deployVariableCount(len(plan.Variables)))
	printDeployDetail(r.out, "Device", plan.Device)
	if plan.Source != "" {
		printDeployDetail(r.out, "Source", plan.Source)
	}
}
