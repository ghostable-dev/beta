package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/store"
)

func (r *Runner) runSetup(args []string) error {
	fs := newFlagSet("setup", r.errOut)
	var envNames cli.Strings
	name := fs.String("name", "", "Project name")
	fs.Var(&envNames, "env", "Initial environment name; may be repeated or comma-separated")
	deviceName := fs.String("device-name", "", "Device label")
	platform := fs.String("platform", "", "Device platform label")
	language := fs.String("language", "", "Project language hint")
	framework := fs.String("framework", "", "Project framework hint")
	packageManager := fs.String("package-manager", "", "Project package manager hint")
	deployTarget := fs.String("deploy-target", "", "Project deployment target hint")
	activityMode := fs.String("activity-mode", domain.DefaultActivity, "Signed activity mode: off, minimal, or full")
	force := fs.Bool("force", false, "Overwrite an existing Ghostable manifest")
	fs.Bool("no-metadata", false, "Deprecated; metadata prompts are not used by this client")
	jsonOut := fs.Bool("json", false, "Print setup result as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("force", "no-metadata", "json")); err != nil {
		return err
	}

	if !*force {
		manifestPath := filepath.Join(mustGetwd(), ".ghostable", "ghostable.yaml")
		if _, err := os.Stat(manifestPath); err == nil {
			return fmt.Errorf("Ghostable is already initialized; pass --force to replace the local manifest")
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	defaultName := filepath.Base(mustGetwd())
	projectName, err := r.ask("Project name", *name, defaultName, "name")
	if err != nil {
		return err
	}

	if len(envNames) == 0 {
		envNames = append(envNames, domain.DefaultEnvName)
	}

	device, err := r.ask("Device label", *deviceName, defaultDeviceName(), "device-name")
	if err != nil {
		return err
	}

	envs := make([]domain.Environment, 0, len(envNames))
	for _, name := range envNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		envs = append(envs, domain.Environment{Name: name, Type: environmentType(name)})
	}

	dotenvSeed, err := r.promptDefaultDotenvSeed(envs, *jsonOut)
	if err != nil {
		return err
	}

	repo, created, err := store.Setup(".", store.SetupOptions{
		Name:           projectName,
		Environments:   envs,
		DeviceName:     device,
		Platform:       *platform,
		Language:       *language,
		Framework:      *framework,
		PackageManager: *packageManager,
		DeployTarget:   *deployTarget,
		ActivityMode:   *activityMode,
		Force:          *force,
	})
	if err != nil {
		return err
	}

	var seedResult *store.PushResult
	if dotenvSeed != nil && len(dotenvSeed.values) > 0 {
		result, err := repo.PutVariables(domain.DefaultEnvName, dotenvSeed.values, store.PutOptions{Reason: "Seeded from .env during setup"})
		if err != nil {
			return err
		}
		result.File = dotenvSeed.file
		seedResult = &result
	}

	if *jsonOut {
		return printJSON(r.out, setupResultPayload(repo, created, seedResult))
	}

	r.printSetupResult(repo, dotenvSeed, seedResult)
	return nil
}

func setupResultPayload(repo store.Repository, created bool, seedResult *store.PushResult) map[string]interface{} {
	payload := map[string]interface{}{
		"project":      repo.Manifest,
		"manifestPath": repo.ManifestPath,
		"keyPath":      repo.KeyPath(),
		"deviceId":     repo.DeviceID(),
		"created":      created,
	}
	if seedResult != nil {
		payload["seededFrom"] = seedResult
	}
	return payload
}

func (r *Runner) printSetupResult(repo store.Repository, dotenvSeed *defaultDotenvSeed, seedResult *store.PushResult) {
	fmt.Fprintln(r.out, strings.TrimRight(renderGhostableBanner(), "\n"))
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "------------------------------------------------------------")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, success(fmt.Sprintf("Initialized Ghostable for %s.", repo.Manifest.Name)))
	fmt.Fprintf(r.out, "%s %s\n", warn("Manifest:"), repo.ManifestPath)
	fmt.Fprintf(r.out, "%s %s\n", warn("Local key:"), repo.KeyPath())
	if seedResult != nil {
		fmt.Fprintln(r.out, success(fmt.Sprintf("Imported %d variables from %s into default.", len(dotenvSeed.values), dotenvSeed.file)))
		fmt.Fprintln(r.out, warn("Next: review with `ghostable env diff --env default --file .env`."))
	} else if dotenvSeed != nil {
		fmt.Fprintln(r.out, warn(fmt.Sprintf("No variables found in %s.", dotenvSeed.file)))
		fmt.Fprintln(r.out, warn("Next: add variables with `ghostable env push --env default --file .env`."))
	} else {
		fmt.Fprintln(r.out, warn("Next: add variables with `ghostable env push --env default --file .env`."))
	}
}

type defaultDotenvSeed struct {
	file   string
	values map[string]string
}

func (r *Runner) promptDefaultDotenvSeed(envs []domain.Environment, jsonOut bool) (*defaultDotenvSeed, error) {
	if jsonOut || !r.interactive || !setupHasDefaultEnvironment(envs) {
		return nil, nil
	}

	path := filepath.Join(mustGetwd(), ".env")
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}

	useDotenv, err := r.prompts.Confirm("A .env file was detected in this project directory. Use it as the starting values for the default environment?", true)
	if err != nil {
		return nil, err
	}
	if !useDotenv {
		return nil, nil
	}

	values, err := readDotenvFile(path)
	if err != nil {
		return nil, err
	}
	return &defaultDotenvSeed{file: ".env", values: values}, nil
}

func setupHasDefaultEnvironment(envs []domain.Environment) bool {
	for _, env := range envs {
		if env.Name == domain.DefaultEnvName {
			return true
		}
	}
	return false
}

func (r *Runner) runStatus(args []string) error {
	fs := newFlagSet("status", r.errOut)
	jsonOut := fs.Bool("json", false, "Print local status as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}

	devices, _ := repo.Devices()
	accessRequests, _ := repo.ListAccessRequests(false)
	envs := repo.Environments()
	counts := map[string]int{}
	for _, env := range envs {
		values, err := repo.ReadVariables(env.Name)
		if err == nil {
			counts[env.Name] = len(values)
		}
	}

	payload := map[string]interface{}{
		"project":        repo.Manifest,
		"root":           repo.Root,
		"manifestPath":   repo.ManifestPath,
		"keyPath":        repo.KeyPath(),
		"deviceId":       repo.DeviceID(),
		"devices":        devices,
		"valueCounts":    counts,
		"accessRequests": accessRequests,
	}
	if *jsonOut {
		return printJSON(r.out, payload)
	}

	r.printStatus(repo, envs, devices, counts, accessRequests)
	return nil
}

func (r *Runner) printStatus(repo store.Repository, envs []domain.Environment, devices []domain.DeviceRecord, counts map[string]int, accessRequests store.AccessRequestList) {
	totalValues := 0
	for _, env := range envs {
		totalValues += counts[env.Name]
	}
	pendingRequests := len(accessRequests.Valid)

	fmt.Fprintln(r.out, strings.TrimRight(renderGhostableBanner(), "\n"))
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "------------------------------------------------------------")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Ghostable status"))
	r.printStatusField("Project", repo.Manifest.Name)
	r.printStatusField("Root", repo.Root)
	r.printStatusField("Manifest", repo.ManifestPath)
	r.printStatusField("Local key", repo.KeyPath())
	r.printStatusField("Device", statusLocalDevice(repo, devices))
	if stack := statusStack(repo.Manifest); stack != "" {
		r.printStatusField("Stack", stack)
	}
	if repo.Manifest.DeployTarget != "" {
		r.printStatusField("Deploy target", repo.Manifest.DeployTarget)
	}
	if repo.Manifest.ActivityMode != "" {
		r.printStatusField("Activity", repo.Manifest.ActivityMode)
	}

	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Inventory"))
	printStatusInventoryRow(r, "Environments", fmt.Sprintf("%d", len(envs)))
	printStatusInventoryRow(r, "Devices", fmt.Sprintf("%d", len(devices)))
	printStatusInventoryRow(r, "Values", fmt.Sprintf("%d", totalValues))
	printStatusInventoryRow(r, "Requests", fmt.Sprintf("%d pending", pendingRequests))

	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Environments"))
	printStatusEnvironmentRows(r, envs, counts)

	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Devices"))
	printStatusDeviceRows(r, repo, devices)

	if len(accessRequests.Valid) > 0 || len(accessRequests.Invalid) > 0 {
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, warn("Pending requests"))
		printAccessRequestRows(r.out, accessRequests.Valid, false)
		if len(accessRequests.Invalid) > 0 {
			fmt.Fprintln(r.out)
			fmt.Fprintln(r.out, warn("Ignored requests:"))
			for _, entry := range accessRequests.Invalid {
				fmt.Fprintf(r.out, "  %s  %s\n", statusShortID(entry.Request.ID), danger(entry.Error))
			}
		}
	}
}

func (r *Runner) printStatusField(label string, value string) {
	fmt.Fprintf(r.out, "%s  %s\n", warn(fmt.Sprintf("%-13s", label)), success(value))
}

func printStatusInventoryRow(r *Runner, label string, value string) {
	fmt.Fprintf(r.out, "  %s  %s\n", warn(fmt.Sprintf("%-12s", label)), success(value))
}

func printStatusEnvironmentRows(r *Runner, envs []domain.Environment, counts map[string]int) {
	if len(envs) == 0 {
		fmt.Fprintln(r.out, "  "+muted("none"))
		return
	}

	nameWidth := len("Name")
	typeWidth := len("Type")
	for _, env := range envs {
		nameWidth = max(nameWidth, len(env.Name))
		typeWidth = max(typeWidth, len(env.Type))
	}

	fmt.Fprintln(r.out, warn(fmt.Sprintf("  %-*s  %-*s  %s", nameWidth, "Name", typeWidth, "Type", "Values")))
	fmt.Fprintln(r.out, muted(fmt.Sprintf("  %-*s  %-*s  %s", nameWidth, strings.Repeat("-", nameWidth), typeWidth, strings.Repeat("-", typeWidth), "------")))
	for _, env := range envs {
		fmt.Fprintf(r.out, "  %s  %-*s  %s\n",
			coloredCell(env.Name, nameWidth, success),
			typeWidth,
			env.Type,
			success(fmt.Sprintf("%d", counts[env.Name])),
		)
	}
}

func printStatusDeviceRows(r *Runner, repo store.Repository, devices []domain.DeviceRecord) {
	if len(devices) == 0 {
		fmt.Fprintln(r.out, "  "+muted("none"))
		return
	}

	nameWidth := len("Name")
	platformWidth := len("Platform")
	statusWidth := len("Status")
	for _, device := range devices {
		nameWidth = max(nameWidth, len(statusDeviceName(device)))
		platformWidth = max(platformWidth, len(statusDevicePlatform(device)))
		statusWidth = max(statusWidth, len(deviceStatusDisplay(device.Status)))
	}

	fmt.Fprintln(r.out, warn(fmt.Sprintf("  %-*s  %-*s  %-*s  %-7s  %s", nameWidth, "Name", platformWidth, "Platform", statusWidth, "Status", "Current", "ID")))
	fmt.Fprintln(r.out, muted(fmt.Sprintf("  %-*s  %-*s  %-*s  %-7s  %s", nameWidth, strings.Repeat("-", nameWidth), platformWidth, strings.Repeat("-", platformWidth), statusWidth, strings.Repeat("-", statusWidth), "-------", "--")))
	for _, device := range devices {
		current := ""
		if device.ID == repo.DeviceID() {
			current = "yes"
		}
		status := deviceStatusDisplay(device.Status)
		fmt.Fprintf(r.out, "  %s  %-*s  %s  %s  %s\n",
			coloredCell(statusDeviceName(device), nameWidth, success),
			platformWidth,
			statusDevicePlatform(device),
			coloredCell(status, statusWidth, deviceStatusColor),
			coloredCell(current, 7, currentColor),
			muted(statusShortID(device.ID)),
		)
	}
}

func statusLocalDevice(repo store.Repository, devices []domain.DeviceRecord) string {
	for _, device := range devices {
		if device.ID == repo.DeviceID() {
			return fmt.Sprintf("%s (%s)", statusDeviceName(device), statusShortID(device.ID))
		}
	}
	return repo.DeviceID()
}

func statusStack(project domain.ProjectManifest) string {
	parts := []string{}
	if project.Language != "" {
		parts = append(parts, project.Language)
	}
	if project.Framework != "" {
		parts = append(parts, project.Framework)
	}
	if project.PackageManager != "" {
		parts = append(parts, project.PackageManager)
	}
	return strings.Join(parts, " / ")
}

func statusDeviceName(device domain.DeviceRecord) string {
	name := strings.TrimSpace(terminalSafeText(device.Name))
	if name == "" {
		return "Unnamed device"
	}
	return name
}

func statusDevicePlatform(device domain.DeviceRecord) string {
	platform := strings.TrimSpace(terminalSafeText(device.Platform))
	if platform == "" {
		return "-"
	}
	return platform
}

func statusShortID(id string) string {
	if len(id) > 18 {
		return id[:18] + "..."
	}
	return id
}

func defaultDeviceName() string {
	for _, candidate := range defaultDeviceNameCandidates() {
		if name := normalizeDefaultDeviceName(candidate); name != "" {
			return name
		}
	}
	return domain.DefaultDeviceName
}

func defaultDeviceNameCandidates() []string {
	candidates := []string{}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, macOSComputerName())
	}
	if host, err := os.Hostname(); err == nil {
		candidates = append(candidates, host)
	}
	return candidates
}

func macOSComputerName() string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	output, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--get", "ComputerName").Output()
	if err != nil {
		return ""
	}
	return string(output)
}

func normalizeDefaultDeviceName(value string) string {
	name := strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
	if len(name) > len(".local") && strings.EqualFold(name[len(name)-len(".local"):], ".local") {
		name = name[:len(name)-len(".local")]
	}
	return strings.TrimSpace(name)
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func environmentType(name string) string {
	switch strings.ToLower(name) {
	case "local", "default":
		return "local"
	case "dev", "development":
		return "development"
	case "preview":
		return "preview"
	case "stage", "staging":
		return "staging"
	case "prod", "production":
		return "production"
	default:
		return "custom"
	}
}
