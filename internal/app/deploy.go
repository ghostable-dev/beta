package app

import (
	"fmt"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
)

func (r *Runner) runDeploy(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printDeployHelp()
		return nil
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target, ok := normalizeDeployTarget(args[0])
		if ok {
			if target == "local" {
				return r.runDeployWrite(args[1:])
			}
			return r.runDeployProvider(target, args[1:])
		}
	}

	if target, ok := r.defaultDeployTarget(args); ok {
		return r.runDeployProvider(target, args)
	}

	return r.runDeployWrite(args)
}

func (r *Runner) runDeployProvider(target string, args []string) error {
	switch target {
	case "laravel-vapor":
		return r.runDeployVapor(args)
	case "laravel-forge":
		return r.runDeployForge(args)
	case "laravel-cloud":
		return r.runDeployCloud(args)
	default:
		return fmt.Errorf("unknown deploy target %q", target)
	}
}

func normalizeDeployTarget(target string) (string, bool) {
	switch strings.TrimSpace(strings.ToLower(target)) {
	case "local", "dotenv", "env-file":
		return "local", true
	case "laravel-vapor", "vapor":
		return "laravel-vapor", true
	case "laravel-forge", "forge":
		return "laravel-forge", true
	case "laravel-cloud", "cloud":
		return "laravel-cloud", true
	default:
		return "", false
	}
}

func (r *Runner) defaultDeployTarget(args []string) (string, bool) {
	if deployWriteOptionsRequested(args) {
		return "", false
	}
	repo, err := r.openRepo()
	if err != nil {
		return "", false
	}
	target, ok := normalizeDeployTarget(repo.Manifest.DeployTarget)
	if !ok || target == "local" {
		return "", false
	}
	return target, true
}

func deployWriteOptionsRequested(args []string) bool {
	for _, name := range []string{"file", "merge", "backup"} {
		if hasFlag(args, name) {
			return true
		}
	}
	return false
}

func (r *Runner) runDeployWrite(args []string) error {
	fs := newFlagSet("deploy", r.errOut)
	env := fs.String("env", "", "Environment name")
	file := fs.String("file", ".env", "Output file")
	var only cli.Strings
	fs.Var(&only, "only", "Only include these keys")
	dryRun := fs.Bool("dry-run", false, "Do not write the env file")
	merge := fs.Bool("merge", false, "Merge into an existing env file instead of replacing it")
	backup := fs.Bool("backup", false, "Create a backup before writing")
	jsonOut := fs.Bool("json", false, "Print deploy result as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("dry-run", "merge", "backup", "json"))
	if err != nil {
		return err
	}
	if len(positionals) > 1 {
		return fmt.Errorf("usage: ghostable deploy [environment] [options]")
	}
	if *env == "" && len(positionals) == 1 {
		*env = positionals[0]
	}

	return r.pullEnvironmentFile(environmentPullRequest{
		Environment: *env,
		File:        *file,
		Only:        only,
		DryRun:      *dryRun,
		Replace:     !*merge,
		Backup:      *backup,
		JSON:        *jsonOut,
		SkipEvent:   true,
		Deploy:      true,
	})
}

func (r *Runner) printDeployHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable deploy [target] [environment] [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Decrypt an environment into a local .env file or sync it to a supported deployment provider.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Targets:"))
	fmt.Fprintln(r.out, "  laravel-forge    Sync Laravel Forge site environment variables")
	fmt.Fprintln(r.out, "  laravel-vapor    Sync Laravel Vapor environment variables")
	fmt.Fprintln(r.out, "  laravel-cloud    Sync Laravel Cloud environment variables")
	fmt.Fprintln(r.out, "  local            Write decrypted values to a local .env file")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Options:"))
	fmt.Fprintln(r.out, "  --env <ENV>       Environment name")
	fmt.Fprintln(r.out, "  --file <PATH>     Output file (default .env)")
	fmt.Fprintln(r.out, "  --only <KEYS>     Only include these keys; may be repeated or comma-separated")
	fmt.Fprintln(r.out, "  --merge           Merge into the existing file instead of replacing it")
	fmt.Fprintln(r.out, "  --backup          Create a backup before writing")
	fmt.Fprintln(r.out, "  --dry-run         Show what would be written without changing files")
	fmt.Fprintln(r.out, "  --json            Print deploy result as JSON")
}
