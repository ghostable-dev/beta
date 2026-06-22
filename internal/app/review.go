package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/review"
)

func (r *Runner) runReview(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printReviewHelp()
		return nil
	}

	fs := newFlagSet("review", r.errOut)
	baseRef := fs.String("base", "origin/main", "Base git ref")
	headRef := fs.String("head", "", "Head git ref; defaults to HEAD plus local worktree changes")
	format := fs.String("format", "human", "Output format: human or github")
	jsonOut := fs.Bool("json", false, "Print review result as JSON")
	var environments cli.Strings
	fs.Var(&environments, "env", "Environment to review; may be repeated or comma-separated")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}

	selectedFormat := normalizeReviewFormat(*format, *jsonOut)
	if selectedFormat == "" {
		return fmt.Errorf("--format must be human, github, or json")
	}

	report, err := review.Review(context.Background(), review.ReviewInput{
		Root:         ".",
		BaseRef:      *baseRef,
		HeadRef:      *headRef,
		Environments: environments,
		Format:       selectedFormat,
		Status:       r.progressReporter(selectedFormat == "human"),
	})
	if err != nil {
		return err
	}

	switch selectedFormat {
	case "json":
		if err := printJSON(r.out, report); err != nil {
			return err
		}
	case "github":
		review.PrintGitHub(r.out, report)
	default:
		review.PrintHuman(r.out, report)
	}

	if report.HasErrors() {
		return fmt.Errorf("review failed with %d error%s", len(report.Errors), plural(len(report.Errors)))
	}
	return nil
}

func (r *Runner) printReviewHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable review --base <ref> [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Review code changes against encrypted Ghostable ENV metadata.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Options:"))
	fmt.Fprintln(r.out, "  --base <REF>        Base git ref (default: origin/main)")
	fmt.Fprintln(r.out, "  --head <REF>        Head git ref (defaults to HEAD plus local worktree changes)")
	fmt.Fprintln(r.out, "  --env <ENV>         Environment to review; may be repeated or comma-separated")
	fmt.Fprintln(r.out, "  --format <FORMAT>   Output format: human, github, or json")
	fmt.Fprintln(r.out, "  --json              Print review result as JSON")
}

func normalizeReviewFormat(format string, jsonOut bool) string {
	if jsonOut {
		return "json"
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "human", "text":
		return "human"
	case "github":
		return "github"
	case "json":
		return "json"
	default:
		return ""
	}
}
