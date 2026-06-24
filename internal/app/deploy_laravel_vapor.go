package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/dotenv"
	"github.com/ghostable-dev/beta/internal/store"
)

const vaporCommandTimeout = 2 * time.Minute

var vaporEnvironmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type vaporDeployPlan struct {
	Environment      string            `json:"environment"`
	VaporEnvironment string            `json:"vaporEnvironment"`
	DryRun           bool              `json:"dryRun"`
	Synced           bool              `json:"synced"`
	EnvVars          []string          `json:"envVars"`
	VaporSecrets     []string          `json:"vaporSecrets"`
	Device           string            `json:"device"`
	Source           string            `json:"source,omitempty"`
	Variables        map[string]string `json:"-"`
	Secrets          map[string]string `json:"-"`
}

func (r *Runner) runDeployVapor(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printDeployVaporHelp()
		return nil
	}

	fs := newFlagSet("deploy laravel-vapor", r.errOut)
	env := fs.String("env", "", "Ghostable environment name")
	vaporEnv := fs.String("vapor-env", "", "Laravel Vapor environment name")
	dryRun := fs.Bool("dry-run", false, "Show what would sync without invoking Vapor")
	jsonOut := fs.Bool("json", false, "Print Vapor deploy result as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("dry-run", "json"))
	if err != nil {
		return err
	}
	if len(positionals) > 1 {
		return fmt.Errorf("usage: ghostable deploy laravel-vapor [environment] [options]")
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
	vaporEnvironment := strings.TrimSpace(*vaporEnv)
	if vaporEnvironment == "" {
		vaporEnvironment = selected
	}
	if err := validateVaporEnvironment(vaporEnvironment); err != nil {
		return err
	}

	plan, err := buildVaporDeployPlan(repo, selected, vaporEnvironment)
	if err != nil {
		return err
	}
	plan.DryRun = *dryRun

	if *dryRun {
		if *jsonOut {
			return printJSON(r.out, plan)
		}
		r.printVaporDeployPlan(plan)
		return nil
	}

	vaporPath, err := resolveVaporBinary(repo.Root)
	if err != nil {
		return err
	}
	if err := syncVaporDeployPlan(plan, vaporPath); err != nil {
		return err
	}
	plan.Synced = true

	if *jsonOut {
		return printJSON(r.out, plan)
	}
	r.printVaporDeploySuccess(plan)
	return nil
}

func (r *Runner) printDeployVaporHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable deploy laravel-vapor [environment] [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Sync decrypted values to Laravel Vapor using Vapor environment variables and Vapor Secrets.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Options:"))
	fmt.Fprintln(r.out, "  --env <ENV>         Ghostable environment name")
	fmt.Fprintln(r.out, "  --vapor-env <ENV>   Laravel Vapor environment name (defaults to the Ghostable environment)")
	fmt.Fprintln(r.out, "  --dry-run           Show what would sync without invoking Vapor")
	fmt.Fprintln(r.out, "  --json              Print Vapor deploy result as JSON")
}

func buildVaporDeployPlan(repo store.Repository, env string, vaporEnv string) (vaporDeployPlan, error) {
	variables, err := repo.ReadVariables(env)
	if err != nil {
		return vaporDeployPlan{}, err
	}

	plan := vaporDeployPlan{
		Environment:      env,
		VaporEnvironment: vaporEnv,
		Device:           deployIdentityDisplay(repo),
		Source:           strings.TrimSpace(deployIdentitySource(repo)),
		Variables:        map[string]string{},
		Secrets:          map[string]string{},
	}

	for _, key := range vaporVariableKeys(variables) {
		variable := variables[key]
		if variable.VaporSecret {
			plan.Secrets[key] = variable.Value
			plan.VaporSecrets = append(plan.VaporSecrets, key)
			continue
		}
		plan.Variables[key] = variable.Value
		plan.EnvVars = append(plan.EnvVars, key)
	}

	return plan, nil
}

func syncVaporDeployPlan(plan vaporDeployPlan, vaporPath string) error {
	if len(plan.Variables) > 0 {
		if err := syncVaporEnvironmentVariables(vaporPath, plan.VaporEnvironment, plan.Variables, plan.EnvVars); err != nil {
			return err
		}
	}
	if len(plan.Secrets) > 0 {
		if err := syncVaporSecrets(vaporPath, plan.VaporEnvironment, plan.Secrets, plan.VaporSecrets); err != nil {
			return err
		}
	}
	return nil
}

func syncVaporEnvironmentVariables(vaporPath string, vaporEnv string, variables map[string]string, order []string) error {
	envPath, err := createTemporaryVaporEnvironmentFile(vaporEnv)
	if err != nil {
		return err
	}
	defer removeVaporEnvironmentFile(envPath)

	if err := runVaporCommand(vaporPath, "pull Vapor environment", "env:pull", vaporEnv, "--file="+envPath); err != nil {
		return err
	}
	if err := ensureVaporEnvironmentFileIsRegular(envPath); err != nil {
		return err
	}

	existing := ""
	if content, err := os.ReadFile(envPath); err == nil {
		existing = string(content)
	} else if !os.IsNotExist(err) {
		return err
	}
	next, err := dotenv.Merge(existing, variables, order, false)
	if err != nil {
		return err
	}
	if err := writeVaporEnvironmentFile(envPath, []byte(next)); err != nil {
		return err
	}

	if err := runVaporCommand(vaporPath, "push Vapor environment", "env:push", vaporEnv, "--file="+envPath); err != nil {
		return err
	}
	return removeVaporEnvironmentFile(envPath)
}

func createTemporaryVaporEnvironmentFile(vaporEnv string) (string, error) {
	temp, err := os.CreateTemp("", "ghostable-vapor-"+vaporEnv+"-*.env")
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

func ensureVaporEnvironmentFileIsRegular(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to use symlinked Vapor environment file %s", path)
		}
		if info.IsDir() {
			return fmt.Errorf("Vapor environment file %s is a directory", path)
		}
		return nil
	}
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func writeVaporEnvironmentFile(path string, content []byte) error {
	if err := ensureVaporEnvironmentFileIsRegular(path); err != nil {
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

func removeVaporEnvironmentFile(path string) error {
	if err := ensureVaporEnvironmentFileIsRegular(path); err != nil {
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

func syncVaporSecrets(vaporPath string, vaporEnv string, secrets map[string]string, order []string) error {
	for _, key := range order {
		tempFile, err := writeVaporSecretTempFile(secrets[key])
		if err != nil {
			return err
		}
		err = runVaporCommand(vaporPath, "sync Vapor Secret "+key, "secret", vaporEnv, "--name="+key, "--file="+tempFile)
		_ = os.Remove(tempFile)
		if err != nil {
			return err
		}
	}
	return nil
}

func writeVaporSecretTempFile(value string) (string, error) {
	file, err := os.CreateTemp("", "ghostable-vapor-secret-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := os.Chmod(path, 0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if _, err := file.WriteString(value); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func resolveVaporBinary(projectRoot string) (string, error) {
	path, err := exec.LookPath("vapor")
	if err != nil {
		return "", fmt.Errorf("Vapor CLI not found on PATH; install the Laravel Vapor CLI before running `ghostable deploy laravel-vapor`")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if binaryInsideProject(projectRoot, absolutePath) {
		return "", fmt.Errorf("refusing to run Vapor CLI from project path %s; put a trusted Vapor executable earlier on PATH outside this repository", absolutePath)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("Vapor CLI path %s is a directory", absolutePath)
	}
	return absolutePath, nil
}

func binaryInsideProject(projectRoot string, binaryPath string) bool {
	absoluteRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return false
	}
	absolutePath, err := filepath.Abs(binaryPath)
	if err != nil {
		return false
	}
	if pathInsideDirectory(absoluteRoot, absolutePath) {
		return true
	}
	realRoot, rootErr := filepath.EvalSymlinks(absoluteRoot)
	realPath, pathErr := filepath.EvalSymlinks(absolutePath)
	if rootErr != nil || pathErr != nil {
		return false
	}
	return pathInsideDirectory(realRoot, realPath)
}

func pathInsideDirectory(root string, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative == "." || (!filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func runVaporCommand(vaporPath string, action string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), vaporCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, vaporPath, args...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s: Vapor CLI timed out", action)
	}
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("%s: %s", action, detail)
	}
	return nil
}

func (r *Runner) printVaporDeployPlan(plan vaporDeployPlan) {
	fmt.Fprintln(r.out, success("👻 Ghostable Vapor deploy plan."))
	printVaporDeployDetails(r, plan)
}

func (r *Runner) printVaporDeploySuccess(plan vaporDeployPlan) {
	fmt.Fprintln(r.out, success("👻 Ghostable Vapor deploy successful."))
	printVaporDeployDetails(r, plan)
}

func printVaporDeployDetails(r *Runner, plan vaporDeployPlan) {
	printDeployDetail(r.out, "Environment", plan.Environment)
	printDeployDetail(r.out, "Vapor environment", plan.VaporEnvironment)
	printDeployDetail(r.out, "Env vars", fmt.Sprintf("%d", len(plan.EnvVars)))
	printDeployDetail(r.out, "Vapor secrets", fmt.Sprintf("%d", len(plan.VaporSecrets)))
	printDeployDetail(r.out, "Device", plan.Device)
	if plan.Source != "" {
		printDeployDetail(r.out, "Source", plan.Source)
	}
}

func validateVaporEnvironment(env string) error {
	if env == "" {
		return fmt.Errorf("Vapor environment name is required")
	}
	if !vaporEnvironmentPattern.MatchString(env) {
		return fmt.Errorf("Vapor environment name may only contain letters, numbers, dashes, underscores, and dots")
	}
	return nil
}

func vaporEnvironmentFile(vaporEnv string) string {
	return ".env." + vaporEnv
}

func vaporVariableKeys(variables map[string]domain.Variable) []string {
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
