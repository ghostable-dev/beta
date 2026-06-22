package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/prompt"
	"github.com/ghostable-dev/beta/internal/store"
)

var version = "dev"

type Runner struct {
	args        []string
	in          io.Reader
	out         io.Writer
	errOut      io.Writer
	interactive bool
	prompts     prompt.Session
}

func Run(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	runner := NewRunner(args, in, out, errOut)
	if err := runner.Run(); err != nil {
		return printRunError(err, errOut)
	}
	return 0
}

func printRunError(err error, errOut io.Writer) int {
	if prompt.IsCanceled(err) {
		fmt.Fprintln(errOut, warn("Canceled."))
		return 130
	}
	fmt.Fprintln(errOut, danger("Error:"), err)
	return 1
}

func NewRunner(args []string, in io.Reader, out io.Writer, errOut io.Writer) *Runner {
	inFile, inOK := in.(*os.File)
	outFile, outOK := out.(*os.File)
	interactive := inOK && outOK && prompt.IsTerminal(inFile) && prompt.IsTerminal(outFile)
	return &Runner{
		args:        args,
		in:          in,
		out:         out,
		errOut:      errOut,
		interactive: interactive,
		prompts:     prompt.New(in, out),
	}
}

func (r *Runner) Run() error {
	args := r.args
	if len(args) == 0 {
		args = []string{"ghostable"}
	}
	if len(args) == 1 {
		if r.interactive {
			selected, err := r.prompts.SelectOptionsWithIntro([]string{
				rootPromptHeading(),
				"Use arrow keys to move, Enter to select",
				"",
			}, warn("Available commands:"), promptOptions(rootCommandOptions), 0)
			if err != nil {
				return err
			}
			args = append(args, selected)
		} else {
			r.printRootHelp()
			return nil
		}
	}

	switch args[1] {
	case "-h", "--help", "help":
		r.printRootHelp()
	case "-v", "--version", "version":
		fmt.Fprintln(r.out, version)
	case "setup":
		return r.runSetup(args[2:])
	case "status":
		return r.runStatus(args[2:])
	case "env", "environment":
		return r.runEnv(args[2:])
	case "var", "variable":
		return r.runVar(args[2:])
	case "validate":
		return r.runValidate(args[2:])
	case "review":
		return r.runReview(args[2:])
	case "scan":
		return r.runScan(args[2:])
	case "schema":
		return r.runSchema(args[2:])
	case "agent", "agents":
		return r.runAgent(args[2:])
	case "access":
		return r.runDevice(args[2:])
	case "device":
		return r.runDevice(args[2:])
	case "deploy":
		return r.runDeploy(args[2:])
	default:
		return fmt.Errorf("unknown command %q", args[1])
	}

	return nil
}

func rootPromptHeading() string {
	return fmt.Sprintf("Ghostable %s", accent(version))
}

func accent(value string) string {
	return success(value)
}

func success(value string) string {
	return "\033[32m" + value + "\033[0m"
}

func warn(value string) string {
	return "\033[33m" + value + "\033[0m"
}

func muted(value string) string {
	return "\033[2m" + value + "\033[0m"
}

func danger(value string) string {
	return "\033[31m" + value + "\033[0m"
}

type commandOption struct {
	Label       string
	Value       string
	Description string
}

var rootCommandOptions = []commandOption{
	{Label: "setup", Description: "Initialize .ghostable for this project"},
	{Label: "status", Description: "Show local project status"},
	{Label: "env", Description: "Manage environments and encrypted values"},
	{Label: "var", Description: "Manage individual variables"},
	{Label: "validate", Description: "Check values against schema rules"},
	{Label: "schema", Description: "Manage validation schema files and rules"},
	{Label: "review", Description: "Review code changes against encrypted ENV state"},
	{Label: "deploy", Description: "Write decrypted values for deploy scripts"},
	{Label: "scan", Description: "Find hard-coded secrets"},
	{Label: "agents", Value: "agent", Description: "Print agent guidance"},
	{Label: "access", Description: "Manage devices and access grants"},
}

func (r *Runner) selectCommand(label string, options []commandOption) (string, error) {
	return r.prompts.SelectOptions(label, promptOptions(options), 0)
}

func promptOptions(options []commandOption) []prompt.SelectOption {
	result := make([]prompt.SelectOption, 0, len(options))
	for _, option := range options {
		result = append(result, prompt.SelectOption{
			Label:       option.Label,
			Value:       option.Value,
			Description: option.Description,
		})
	}
	return result
}

func printCommandDescriptions(out io.Writer, options []commandOption) {
	width := 0
	for _, option := range options {
		if len(option.Label) > width {
			width = len(option.Label)
		}
	}
	for _, option := range options {
		fmt.Fprintf(out, "  %s%s  %s\n", success(option.Label), strings.Repeat(" ", width-len(option.Label)), option.Description)
	}
}

func (r *Runner) progressReporter(enabled bool) func(message string) {
	if !enabled || !r.interactive {
		return nil
	}
	return func(message string) {
		fmt.Fprintln(r.errOut, warn(message+"..."))
	}
}

func (r *Runner) printProgress(enabled bool, message string) {
	report := r.progressReporter(enabled)
	if report != nil {
		report(message)
	}
}

func isHelpArg(value string) bool {
	switch value {
	case "-h", "--help", "help":
		return true
	default:
		return false
	}
}

func hasFlag(args []string, name string) bool {
	long := "--" + name
	short := "-" + name
	for _, arg := range args {
		if arg == long || arg == short || strings.HasPrefix(arg, long+"=") || strings.HasPrefix(arg, short+"=") {
			return true
		}
	}
	return false
}

func (r *Runner) printRootHelp() {
	fmt.Fprintf(r.out, "Ghostable %s\n", version)
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Manage server-less encrypted environment secrets with local storage and git.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Usage:"))
	fmt.Fprintln(r.out, "  ghostable setup [options]")
	fmt.Fprintln(r.out, "  ghostable status [--json]")
	fmt.Fprintln(r.out, "  ghostable env <command> [options]")
	fmt.Fprintln(r.out, "  ghostable var <command> [options]")
	fmt.Fprintln(r.out, "  ghostable validate [options]")
	fmt.Fprintln(r.out, "  ghostable schema <command> [options]")
	fmt.Fprintln(r.out, "  ghostable review --base <ref> [options]")
	fmt.Fprintln(r.out, "  ghostable deploy [environment] [options]")
	fmt.Fprintln(r.out, "  ghostable scan [paths...] [options]")
	fmt.Fprintln(r.out, "  ghostable agents <command> [options]")
	fmt.Fprintln(r.out, "  ghostable access <command> [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Core commands:"))
	printCommandDescriptions(r.out, rootCommandOptions)
}

func newFlagSet(name string, errOut io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	return fs
}

func printJSON(out io.Writer, value interface{}) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func (r *Runner) openRepo() (store.Repository, error) {
	return store.Open(".")
}

func (r *Runner) selectEnvironment(repo store.Repository, provided string) (string, error) {
	return r.selectEnvironmentWithLabel(repo, provided, "Select an environment", "env")
}

func (r *Runner) selectEnvironmentWithLabel(repo store.Repository, provided string, label string, missingFlag string) (string, error) {
	if provided != "" {
		return provided, nil
	}

	envs := repo.Environments()
	if len(envs) == 0 {
		return "", errors.New("no environments are configured")
	}
	if len(envs) == 1 {
		return envs[0].Name, nil
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --%s to select an environment", missingFlag)
	}

	choices := make([]string, 0, len(envs))
	for _, env := range envs {
		choices = append(choices, env.Name)
	}
	return r.prompts.Select(label, choices, 0)
}

func (r *Runner) selectEnvironmentExcept(repo store.Repository, provided string, excluded string, label string, missingFlag string) (string, error) {
	if provided != "" {
		return provided, nil
	}

	envs := repo.Environments()
	choices := make([]string, 0, len(envs))
	for _, env := range envs {
		if env.Name != excluded {
			choices = append(choices, env.Name)
		}
	}
	if len(choices) == 0 {
		return "", errors.New("no other environments are configured")
	}
	if len(choices) == 1 {
		return choices[0], nil
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --%s", missingFlag)
	}
	return r.prompts.Select(label, choices, 0)
}

func (r *Runner) selectDevice(repo store.Repository, provided string) (string, error) {
	if provided != "" {
		return provided, nil
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --device-id")
	}

	devices, err := repo.Devices()
	if err != nil {
		return "", err
	}
	choices := make([]string, 0, len(devices))
	idsByLabel := map[string]string{}
	for _, device := range devices {
		if device.ID == repo.DeviceID() && len(devices) > 1 {
			continue
		}
		label := deviceLabel(device)
		choices = append(choices, label)
		idsByLabel[label] = device.ID
	}
	if len(choices) == 0 {
		return "", errors.New("no devices are available")
	}
	selected, err := r.prompts.Select("Select a device", choices, 0)
	if err != nil {
		return "", err
	}
	return idsByLabel[selected], nil
}

func (r *Runner) selectChoice(label string, choices []string, provided string, fallback string, missingFlag string) (string, error) {
	if strings.TrimSpace(provided) != "" {
		return strings.TrimSpace(provided), nil
	}
	if !r.interactive {
		if fallback != "" {
			return fallback, nil
		}
		return "", fmt.Errorf("pass --%s", missingFlag)
	}
	defaultIndex := 0
	for index, choice := range choices {
		if choice == fallback {
			defaultIndex = index
			break
		}
	}
	return r.prompts.Select(label, choices, defaultIndex)
}

func deviceLabel(device domain.DeviceRecord) string {
	name := strings.TrimSpace(terminalSafeText(device.Name))
	if name == "" {
		name = "Unnamed device"
	}
	platform := strings.TrimSpace(terminalSafeText(device.Platform))
	id := device.ID
	if len(id) > 18 {
		id = id[:18] + "..."
	}
	if platform != "" {
		return fmt.Sprintf("%s (%s, %s)", name, platform, id)
	}
	return fmt.Sprintf("%s (%s)", name, id)
}

func terminalSafeText(value string) string {
	var builder strings.Builder
	changed := false
	for _, r := range value {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			builder.WriteRune(' ')
			changed = true
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			changed = true
		default:
			builder.WriteRune(r)
		}
	}
	if !changed {
		return value
	}
	return builder.String()
}

func (r *Runner) ask(label string, value string, defaultValue string, missingFlag string) (string, error) {
	return r.askWithSpacing(label, value, defaultValue, missingFlag, true)
}

func (r *Runner) askOptional(label string, value string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}
	if !r.interactive {
		return "", nil
	}
	return r.prompts.AskHighlighted(label, "")
}

func (r *Runner) askTight(label string, value string, defaultValue string, missingFlag string) (string, error) {
	return r.askWithSpacing(label, value, defaultValue, missingFlag, false)
}

func (r *Runner) askWithSpacing(label string, value string, defaultValue string, missingFlag string, leadingBlank bool) (string, error) {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}
	if !r.interactive {
		if defaultValue != "" {
			return defaultValue, nil
		}
		return "", fmt.Errorf("pass --%s", missingFlag)
	}
	if !leadingBlank {
		return r.prompts.AskHighlightedTight(label, defaultValue)
	}
	return r.prompts.AskHighlighted(label, defaultValue)
}

func (r *Runner) confirm(label string, assumeYes bool) (bool, error) {
	if assumeYes {
		return true, nil
	}
	if !r.interactive {
		return false, fmt.Errorf("pass --assume-yes to confirm")
	}
	return r.prompts.Confirm(label, false)
}

func parsePositiveInt(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func projectPath(file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Clean(file)
}

func repoFilePath(root string, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(root, file)
}

func envFileDefault(env string) string {
	if env == "local" || env == "default" {
		return ".env"
	}
	return ".env." + env
}

func variableRows(values map[string]domain.Variable, showValues bool) []domain.Variable {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)

	rows := make([]domain.Variable, 0, len(keys))
	for _, key := range keys {
		value := values[key]
		if !showValues {
			value.Value = ""
			value.HasValue = false
		}
		rows = append(rows, value)
	}
	return rows
}

func sortStrings(values []string) {
	sort.Strings(values)
}
