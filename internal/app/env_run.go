package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/store"
	"github.com/ghostable-dev/beta/internal/validation"
)

type envRunRequest struct {
	Environment string
	Only        []string
	Inherit     bool
	MaskOutput  bool
	Strict      bool
	Command     []string
}

func (r *Runner) runEnvRun(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		r.printEnvRunHelp()
		return nil
	}

	fs := newFlagSet("env run", r.errOut)
	env := fs.String("env", "", "Environment name")
	var only cli.Strings
	fs.Var(&only, "only", "Only inject these keys; may be repeated or comma-separated")
	inherit := fs.Bool("inherit", true, "Inherit the current process environment")
	noInherit := fs.Bool("no-inherit", false, "Run with only Ghostable values and minimal system env")
	maskOutput := fs.Bool("mask-output", false, "Redact injected values from child stdout and stderr")
	strict := fs.Bool("strict", false, "Validate injected values and fail when requested keys are missing")
	onlyProvided := hasFlag(args, "only")
	inheritProvided := hasFlag(args, "inherit")
	noInheritProvided := hasFlag(args, "no-inherit")
	strictProvided := hasFlag(args, "strict")
	maskOutputProvided := hasFlag(args, "mask-output")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("inherit", "no-inherit", "mask-output", "strict"))
	if err != nil {
		return err
	}
	if inheritProvided && noInheritProvided {
		return fmt.Errorf("pass either --inherit or --no-inherit, not both")
	}

	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	selected, err := r.selectEnvironment(repo, *env)
	if err != nil {
		return err
	}

	if len(positionals) == 0 {
		if !r.interactive {
			return fmt.Errorf("usage: ghostable env run --env <ENV> [options] -- <command> [args...]")
		}
		positionals, err = r.askEnvRunCommand(repo, selected)
		if err != nil {
			return err
		}
		if !onlyProvided {
			only, err = r.askEnvRunOnly(repo, selected)
			if err != nil {
				return err
			}
		}
		if !strictProvided {
			*strict, err = r.askEnvRunConfirm("Validate before running?", false)
			if err != nil {
				return err
			}
		}
		if !maskOutputProvided {
			*maskOutput, err = r.askEnvRunConfirm("Mask command output?", true)
			if err != nil {
				return err
			}
		}
		if !inheritProvided && !noInheritProvided {
			*inherit, err = r.askEnvRunConfirm("Inherit current shell environment?", true)
			if err != nil {
				return err
			}
		}
	}

	return r.runCommandWithEnvironment(repo, envRunRequest{
		Environment: selected,
		Only:        only,
		Inherit:     *inherit && !*noInherit,
		MaskOutput:  *maskOutput,
		Strict:      *strict,
		Command:     positionals,
	})
}

func (r *Runner) printEnvRunHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable env run --env <ENV> [options] -- <command> [args...]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Run a command with decrypted environment values without writing a .env file.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Options:"))
	fmt.Fprintln(r.out, "  --env <ENV>       Environment name")
	fmt.Fprintln(r.out, "  --only <KEYS>     Only inject these keys; may be repeated or comma-separated")
	fmt.Fprintln(r.out, "  --inherit         Inherit the current process environment (default)")
	fmt.Fprintln(r.out, "  --no-inherit      Run with only Ghostable values and minimal system env")
	fmt.Fprintln(r.out, "  --mask-output     Redact injected values from child stdout and stderr")
	fmt.Fprintln(r.out, "  --strict          Validate injected values and fail when requested keys are missing")
}

const envRunCustomCommandChoice = "Custom command"

type envRunCommandSuggestion struct {
	Command string
}

func (r *Runner) askEnvRunCommand(repo store.Repository, env string) ([]string, error) {
	productionLike := isProductionLikeEnvironment(repo, env)
	suggestions := envRunCommandSuggestions(repo, env)
	if len(suggestions) > 0 {
		choices := make([]string, 0, len(suggestions)+1)
		for _, suggestion := range suggestions {
			choices = append(choices, suggestion.Command)
		}
		choices = append(choices, envRunCustomCommandChoice)
		selected, err := r.prompts.Select("Command to run", choices, 0)
		if err != nil {
			return nil, err
		}
		r.printPromptAnswer("Command to run", selected)
		if selected != envRunCustomCommandChoice {
			return shellCommand(selected), nil
		}
	}

	return r.askEnvRunCustomCommand(env, productionLike)
}

func (r *Runner) askEnvRunCustomCommand(env string, productionLike bool) ([]string, error) {
	for {
		command, err := r.prompts.AskHighlighted("Command to run", "")
		if err != nil {
			return nil, err
		}
		command = strings.TrimSpace(command)
		if command != "" {
			if productionLike && isRiskyRunCommand(command) {
				if err := r.confirmRiskyEnvRunCommand(env); err != nil {
					return nil, err
				}
			}
			return shellCommand(command), nil
		}
		fmt.Fprintln(r.out, danger("Command is required."))
	}
}

func (r *Runner) confirmRiskyEnvRunCommand(env string) error {
	label := fmt.Sprintf("This command looks risky for %s. Type %s to continue", env, env)
	answer, err := r.prompts.AskHighlighted(label, "")
	if err != nil {
		return err
	}
	if answer != env {
		return fmt.Errorf("risky command canceled")
	}
	return nil
}

func envRunCommandSuggestions(repo store.Repository, env string) []envRunCommandSuggestion {
	productionLike := isProductionLikeEnvironment(repo, env)
	commands := []string{}
	commands = append(commands, laravelRunCommandSuggestions(repo.Root)...)
	commands = append(commands, symfonyRunCommandSuggestions(repo.Root)...)
	commands = append(commands, composerRunCommandSuggestions(repo.Root)...)
	commands = append(commands, packageJSONRunCommandSuggestions(repo.Root)...)
	commands = append(commands, goRunCommandSuggestions(repo.Root)...)
	commands = append(commands, makeRunCommandSuggestions(repo.Root)...)

	seen := map[string]bool{}
	suggestions := []envRunCommandSuggestion{}
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" || seen[command] {
			continue
		}
		if productionLike && isRiskyRunCommand(command) {
			continue
		}
		seen[command] = true
		suggestions = append(suggestions, envRunCommandSuggestion{Command: command})
		if len(suggestions) >= 12 {
			break
		}
	}
	return suggestions
}

func laravelRunCommandSuggestions(root string) []string {
	if !fileExists(filepath.Join(root, "artisan")) {
		return nil
	}
	return []string{
		"php artisan about",
		"php artisan route:list",
		"php artisan serve",
		"php artisan migrate",
		"php artisan queue:work",
	}
}

func symfonyRunCommandSuggestions(root string) []string {
	if !fileExists(filepath.Join(root, "bin", "console")) {
		return nil
	}
	return []string{
		"php bin/console about",
		"php bin/console debug:router",
		"php bin/console doctrine:migrations:migrate",
	}
}

func composerRunCommandSuggestions(root string) []string {
	scripts := readJSONScriptNames(filepath.Join(root, "composer.json"))
	if len(scripts) == 0 {
		return nil
	}
	scripts = orderScriptNames(scripts, []string{"test", "pest", "phpunit", "lint", "pint", "stan", "analyse", "analyze", "dev", "start", "build", "migrate", "deploy"})
	commands := []string{}
	for _, script := range scripts {
		if isComposerEventScript(script) {
			continue
		}
		commands = append(commands, "composer "+script)
	}
	return commands
}

func packageJSONRunCommandSuggestions(root string) []string {
	scripts := readJSONScriptNames(filepath.Join(root, "package.json"))
	if len(scripts) == 0 {
		return nil
	}
	scripts = orderScriptNames(scripts, []string{"dev", "start", "serve", "build", "test", "lint", "typecheck", "check", "preview", "migrate", "deploy"})
	commands := make([]string, 0, len(scripts))
	for _, script := range scripts {
		commands = append(commands, "npm run "+script)
	}
	return commands
}

func goRunCommandSuggestions(root string) []string {
	if !fileExists(filepath.Join(root, "go.mod")) {
		return nil
	}
	return []string{"go test ./..."}
}

func makeRunCommandSuggestions(root string) []string {
	targets := readMakeTargets(root)
	if len(targets) == 0 {
		return nil
	}
	targets = orderScriptNames(targets, []string{"test", "lint", "build", "dev", "serve", "start", "migrate", "deploy"})
	commands := make([]string, 0, len(targets))
	for _, target := range targets {
		commands = append(commands, "make "+target)
	}
	return commands
}

func readJSONScriptNames(path string) []string {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var manifest struct {
		Scripts map[string]json.RawMessage `json:"scripts"`
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil
	}
	names := make([]string, 0, len(manifest.Scripts))
	for name := range manifest.Scripts {
		if strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func readMakeTargets(root string) []string {
	path := filepath.Join(root, "Makefile")
	content, err := os.ReadFile(path)
	if err != nil {
		path = filepath.Join(root, "makefile")
		content, err = os.ReadFile(path)
		if err != nil {
			return nil
		}
	}
	targets := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, ".") {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, " =$") || seen[name] {
			continue
		}
		seen[name] = true
		targets = append(targets, name)
	}
	sort.Strings(targets)
	return targets
}

func orderScriptNames(names []string, preferred []string) []string {
	seen := map[string]bool{}
	ordered := []string{}
	nameSet := map[string]bool{}
	for _, name := range names {
		nameSet[name] = true
	}
	for _, name := range preferred {
		if nameSet[name] && !seen[name] {
			ordered = append(ordered, name)
			seen[name] = true
		}
	}
	for _, name := range names {
		if !seen[name] {
			ordered = append(ordered, name)
			seen[name] = true
		}
	}
	return ordered
}

func isComposerEventScript(name string) bool {
	switch name {
	case "pre-install-cmd", "post-install-cmd", "pre-update-cmd", "post-update-cmd",
		"pre-autoload-dump", "post-autoload-dump", "post-root-package-install",
		"post-create-project-cmd", "pre-archive-cmd", "post-archive-cmd":
		return true
	default:
		return false
	}
}

func isProductionLikeEnvironment(repo store.Repository, env string) bool {
	environment := repo.Manifest.Environments[env]
	return hasProductionToken(env) || hasProductionToken(environment.Type)
}

func hasProductionToken(value string) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		switch token {
		case "prod", "production", "live":
			return true
		}
	}
	return false
}

func isRiskyRunCommand(command string) bool {
	lower := strings.ToLower(command)
	for _, marker := range []string{
		"migrate",
		"migration",
		"rollback",
		"db:wipe",
		"db:seed",
		"seed",
		"cache:clear",
		"config:cache",
		"optimize",
		"queue:restart",
		"queue:work",
		"schedule:work",
		"deploy",
		"release",
		"publish",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func shellCommand(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", command}
	}
	return []string{"/bin/sh", "-c", command}
}

func (r *Runner) askEnvRunOnly(repo store.Repository, env string) ([]string, error) {
	variables, err := repo.ReadVariables(env)
	if err != nil {
		return nil, err
	}
	keys := variableKeys(variables)
	if len(keys) == 0 {
		return nil, nil
	}

	scopeLabel := "Inject variables"
	scope, err := r.prompts.Select(scopeLabel, []string{"All variables", "Selected keys"}, 0)
	if err != nil {
		return nil, err
	}
	r.printPromptAnswer(scopeLabel, scope)
	if scope == "All variables" {
		return nil, nil
	}

	fmt.Fprintf(r.out, "%s %s\n", warn("Available keys:"), strings.Join(keys, ", "))
	for {
		answer, err := r.prompts.AskHighlighted("Variable keys (comma-separated)", "")
		if err != nil {
			return nil, err
		}
		selected := uniqueAppStrings(strings.Split(answer, ","))
		if len(selected) > 0 {
			return selected, nil
		}
		fmt.Fprintln(r.out, danger("Select at least one key."))
	}
}

func (r *Runner) askEnvRunConfirm(label string, fallback bool) (bool, error) {
	value, err := r.prompts.Confirm(label, fallback)
	if err != nil {
		return false, err
	}
	r.printPromptAnswer(label, yesNo(value))
	return value, nil
}

func (r *Runner) runCommandWithEnvironment(repo store.Repository, request envRunRequest) error {
	variables, err := repo.ReadVariables(request.Environment)
	if err != nil {
		return err
	}
	values, missing := environmentRunValues(variables, request.Only)
	if request.Strict {
		if len(missing) > 0 {
			return fmt.Errorf("missing requested %s: %s", keyCountText(len(missing)), strings.Join(missing, ", "))
		}
		if err := r.ensureEnvironmentRunValuesAreValid(repo, request.Environment, values); err != nil {
			return err
		}
	}

	commandEnv := commandEnvironment(request.Inherit, values)
	stdout := r.out
	stderr := r.errOut
	var stdoutRedactor *redactingWriter
	var stderrRedactor *redactingWriter
	if request.MaskOutput {
		maskValues := redactionValues(values)
		stdoutRedactor = newRedactingWriter(r.out, maskValues)
		stderrRedactor = newRedactingWriter(r.errOut, maskValues)
		stdout = stdoutRedactor
		stderr = stderrRedactor
	}

	err = runChildCommand(runChildCommandInput{
		Command: request.Command,
		Env:     commandEnv,
		Stdin:   r.in,
		Stdout:  stdout,
		Stderr:  stderr,
	})
	if stdoutRedactor != nil {
		if flushErr := stdoutRedactor.Flush(); err == nil && flushErr != nil {
			err = flushErr
		}
	}
	if stderrRedactor != nil {
		if flushErr := stderrRedactor.Flush(); err == nil && flushErr != nil {
			err = flushErr
		}
	}
	return err
}

func (r *Runner) ensureEnvironmentRunValuesAreValid(repo store.Repository, env string, values map[string]string) error {
	result, err := validation.Validate(repo.Root, repo, env, values, "")
	if err != nil {
		return err
	}
	if result.Passed {
		return nil
	}
	for _, failure := range result.Errors {
		fmt.Fprintf(r.out, "%s: %s (%s)\n", danger(failure.Key), failure.Message, failure.Rule)
	}
	return fmt.Errorf("strict validation failed")
}

func environmentRunValues(variables map[string]domain.Variable, only []string) (map[string]string, []string) {
	values := map[string]string{}
	if len(only) == 0 {
		for key, variable := range variables {
			values[key] = variable.Value
		}
		return values, nil
	}

	keys := uniqueAppStrings(only)
	missing := []string{}
	for _, key := range keys {
		variable, ok := variables[key]
		if !ok {
			missing = append(missing, key)
			continue
		}
		values[key] = variable.Value
	}
	return values, missing
}

func commandEnvironment(inherit bool, values map[string]string) []string {
	env := map[string]string{}
	if inherit {
		for _, entry := range os.Environ() {
			key, value, ok := strings.Cut(entry, "=")
			if ok {
				env[key] = value
			}
		}
	} else {
		for _, key := range minimalEnvironmentKeys() {
			if value, ok := os.LookupEnv(key); ok {
				env[key] = value
			}
		}
	}
	for key, value := range values {
		env[key] = value
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}
	return result
}

func minimalEnvironmentKeys() []string {
	return []string{
		"PATH",
		"HOME",
		"USER",
		"LOGNAME",
		"SHELL",
		"TMPDIR",
		"TEMP",
		"TMP",
		"SystemRoot",
		"COMSPEC",
		"PATHEXT",
	}
}

type runChildCommandInput struct {
	Command []string
	Env     []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

func runChildCommand(input runChildCommandInput) error {
	if len(input.Command) == 0 {
		return fmt.Errorf("command is required")
	}
	cmd := exec.Command(input.Command[0], input.Command[1:]...)
	cmd.Env = input.Env
	cmd.Stdin = input.Stdin
	cmd.Stdout = input.Stdout
	cmd.Stderr = input.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			if code < 0 {
				code = 1
			}
			return commandExitError{Code: code}
		}
		return fmt.Errorf("run %s: %w", input.Command[0], err)
	}
	return nil
}

func redactionValues(values map[string]string) []string {
	seen := map[string]bool{}
	result := []string{}
	for key, value := range values {
		if value == "" || seen[value] {
			continue
		}
		if len(value) < 4 && !looksSensitiveSeedKey(key) {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Slice(result, func(i int, j int) bool {
		if len(result[i]) == len(result[j]) {
			return result[i] < result[j]
		}
		return len(result[i]) > len(result[j])
	})
	return result
}

type redactingWriter struct {
	out     io.Writer
	values  []string
	pending string
}

func newRedactingWriter(out io.Writer, values []string) *redactingWriter {
	return &redactingWriter{out: out, values: values}
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	if len(w.values) == 0 {
		if _, err := w.out.Write(p); err != nil {
			return 0, err
		}
		return len(p), nil
	}

	w.pending += string(p)
	if len(w.pending) <= redactionBufferLimit {
		return len(p), nil
	}

	flushLength := len(w.pending) - redactionBufferLimit
	flushText := w.pending[:flushLength]
	w.pending = w.pending[flushLength:]
	if _, err := io.WriteString(w.out, redactText(flushText, w.values)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *redactingWriter) Flush() error {
	if w.pending == "" {
		return nil
	}
	_, err := io.WriteString(w.out, redactText(w.pending, w.values))
	w.pending = ""
	return err
}

const redactionBufferLimit = 64 * 1024

func redactText(text string, values []string) string {
	for _, value := range values {
		text = strings.ReplaceAll(text, value, "[secret]")
	}
	return text
}
