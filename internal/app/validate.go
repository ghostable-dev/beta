package app

import (
	"fmt"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/validation"
)

func (r *Runner) runValidate(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printValidateHelp()
		return nil
	}

	fs := newFlagSet("validate", r.errOut)
	env := fs.String("env", "", "Environment name")
	file := fs.String("file", "", "Path to .env file")
	jsonOut := fs.Bool("json", false, "Print validation result as JSON")
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
	values := map[string]string{}
	if *file != "" {
		values, err = readDotenvFile(repoFilePath(repo.Root, *file))
	} else {
		variables, err := repo.ReadVariables(selected)
		if err != nil {
			return err
		}
		for key, variable := range variables {
			values[key] = variable.Value
		}
	}
	if err != nil {
		return err
	}
	result, err := validation.Validate(repo.Root, repo, selected, values, *file)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	if result.Passed {
		fmt.Fprintln(r.out, success("Validation passed."))
		return nil
	}
	for _, failure := range result.Errors {
		fmt.Fprintf(r.out, "%s: %s (%s)\n", danger(failure.Key), failure.Message, failure.Rule)
	}
	return fmt.Errorf("validation failed")
}

func (r *Runner) printValidateHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable validate [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Check environment values against schema rules.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Options:"))
	fmt.Fprintln(r.out, "  --env <ENV>     Environment name")
	fmt.Fprintln(r.out, "  --file <PATH>   Validate values from an env file instead of stored values")
	fmt.Fprintln(r.out, "  --json          Print validation result as JSON")
}
