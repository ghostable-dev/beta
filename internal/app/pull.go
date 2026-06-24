package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/ghostable-dev/beta/internal/store"
)

type environmentPullRequest struct {
	Environment string
	File        string
	Only        []string
	DryRun      bool
	Replace     bool
	Backup      bool
	Force       bool
	ShowValues  bool
	JSON        bool
	SkipEvent   bool
	Deploy      bool
}

func (r *Runner) pullEnvironmentFile(request environmentPullRequest) error {
	repo, result, rendered, err := r.writeEnvironmentFile(request)
	if err != nil {
		return err
	}
	if request.JSON {
		return printJSON(r.out, result)
	}
	if request.DryRun {
		if request.ShowValues {
			fmt.Fprint(r.out, rendered)
		} else {
			fmt.Fprintln(r.out, warn(fmt.Sprintf("Dry run: %d variables would be written to %s.", result.Written, result.File)))
		}
		return nil
	}
	if request.Deploy {
		r.printDeploySuccess(repo, result)
	} else {
		fmt.Fprintln(r.out, success(fmt.Sprintf("Wrote %d variables to %s.", result.Written, result.File)))
	}
	if result.BackupFile != "" {
		fmt.Fprintf(r.out, "%s %s\n", warn("Backup:"), result.BackupFile)
	}
	return nil
}

func (r *Runner) writeEnvironmentFile(request environmentPullRequest) (store.Repository, store.PullResult, string, error) {
	repo, err := r.openRepo()
	if err != nil {
		return store.Repository{}, store.PullResult{}, "", err
	}
	selected, err := r.selectEnvironment(repo, request.Environment)
	if err != nil {
		return store.Repository{}, store.PullResult{}, "", err
	}
	if request.File == "" {
		request.File = envFileDefault(selected)
	}
	result, rendered, err := repo.Pull(selected, store.PullOptions{
		File:      request.File,
		Only:      request.Only,
		DryRun:    request.DryRun,
		Replace:   request.Replace,
		Backup:    request.Backup,
		Force:     request.Force,
		ShowValue: request.ShowValues,
		SkipEvent: request.SkipEvent,
	})
	if err != nil {
		return store.Repository{}, store.PullResult{}, "", err
	}
	return repo, result, rendered, nil
}

func (r *Runner) printDeploySuccess(repo store.Repository, result store.PullResult) {
	fmt.Fprintln(r.out, success("👻 Ghostable deploy successful."))
	printDeployDetail(r.out, "Environment", result.Environment)
	printDeployDetail(r.out, "File", result.File)
	printDeployDetail(r.out, "Variables", deployVariableCount(result.Written))
	printDeployDetail(r.out, "Device", deployIdentityDisplay(repo))
	if source := deployIdentitySource(repo); source != "" {
		printDeployDetail(r.out, "Source", source)
	}
}

func printDeployDetail(out io.Writer, label string, value string) {
	fmt.Fprintf(out, "%s %s\n", warn(label+":"), value)
}

func deployVariableCount(count int) string {
	if count == 1 {
		return "1 variable"
	}
	return fmt.Sprintf("%d variables", count)
}

func deployIdentityDisplay(repo store.Repository) string {
	name := strings.TrimSpace(repo.Identity.Name)
	if name == "" {
		name = "Unnamed device"
	}

	deviceID := strings.TrimSpace(repo.DeviceID())
	if deviceID == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, deviceID)
}

func deployIdentitySource(repo store.Repository) string {
	if repo.KeyPath() == "GHOSTABLE_CI_TOKEN" {
		return "GHOSTABLE_CI_TOKEN"
	}
	return ""
}
