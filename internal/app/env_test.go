package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/prompt"
	"github.com/ghostable-dev/beta/internal/store"
)

func TestRunEnvCreatePromptsForEnvironmentType(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)

	input := &oneByteReader{reader: strings.NewReader("n\n4\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "create", "staging"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runEnvCreate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	intro := strings.Index(text, "Create Environment")
	basePrompt := strings.Index(text, "Base this environment")
	typePrompt := strings.Index(text, "Environment type")
	if intro < 0 || basePrompt < 0 || typePrompt < 0 {
		t.Fatalf("expected intro, base, and environment type prompts, got:\n%s", text)
	}
	if !(intro < basePrompt && basePrompt < typePrompt) {
		t.Fatalf("expected intro, then base prompt, then environment type prompt, got:\n%s", text)
	}
	if !strings.Contains(text, success("Create Environment")) {
		t.Fatalf("expected green create environment intro, got:\n%s", text)
	}
	if !strings.Contains(text, promptAnswerText("Base this environment on an existing environment?", "No")) {
		t.Fatalf("expected surfaced base choice, got:\n%s", text)
	}
	if !strings.Contains(text, success("No")+"\n\nUse arrow keys to move, Enter to select") {
		t.Fatalf("expected blank line after final base answer before next menu, got:\n%s", text)
	}
	if !strings.Contains(text, "Environment type") {
		t.Fatalf("expected environment type prompt, got:\n%s", output.String())
	}
	if !strings.Contains(text, success(`Created "staging" environment.`)) {
		t.Fatalf("expected quoted created environment summary, got:\n%s", text)
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	env := repo.Manifest.Environments["staging"]
	if env.Type != "staging" {
		t.Fatalf("expected staging type, got %q", env.Type)
	}
}

func TestRunEnvCreatePromptsForEnvironmentNameWithLeadingSpace(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)

	input := &oneByteReader{reader: strings.NewReader("n\nstaging\n4\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "create"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runEnvCreate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, success("No")+"\n\nEnvironment name: ") {
		t.Fatalf("expected blank line before environment name prompt, got:\n%s", text)
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.Manifest.Environments["staging"]; !ok {
		t.Fatalf("expected staging environment to be created")
	}
}

func TestRunEnvDuplicatePromptsForEnvironmentType(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)

	input := strings.NewReader("5\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "duplicate", "default", "production", "--seed", "none"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runEnvDuplicate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "New environment type") {
		t.Fatalf("expected new environment type prompt, got:\n%s", output.String())
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	env := repo.Manifest.Environments["production"]
	if env.Type != "production" {
		t.Fatalf("expected production type, got %q", env.Type)
	}
}

func TestRunEnvCopyIsUnknown(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "copy"}, strings.NewReader(""), &output, &output)

	err := runner.Run()
	if err == nil {
		t.Fatal("expected env copy to be unknown")
	}
	if !strings.Contains(err.Error(), `unknown env command "copy"`) {
		t.Fatalf("expected env copy to be unknown, got %v", err)
	}
}

func TestRunEnvValidateIsUnknown(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "validate"}, strings.NewReader(""), &output, &output)

	err := runner.Run()
	if err == nil {
		t.Fatal("expected env validate to be unknown")
	}
	if !strings.Contains(err.Error(), `unknown env command "validate"`) {
		t.Fatalf("expected env validate to be unknown, got %v", err)
	}
}

func TestRunEnvRunInjectsValuesAndInheritsByDefault(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "GHOSTABLE_ENV_RUN_HELPER", "1", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL_ONLY", "from-shell")

	var output bytes.Buffer
	runner := NewRunner(append([]string{"ghostable", "env", "run", "--env", "default", "--"}, envRunHelperCommand()...), strings.NewReader(""), &output, &output)
	if err := runner.runEnvRun(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"APP_NAME=Ghostable", "SHELL_ONLY=from-shell"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected env run output to contain %q:\n%s", expected, text)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".env")); !os.IsNotExist(err) {
		t.Fatalf("env run should not write .env, stat err: %v", err)
	}
}

func TestRunEnvRunInteractivePromptsForCommandAndOptions(t *testing.T) {
	setupRepoForEnvRunTest(t, map[string]string{
		"GHOSTABLE_ENV_RUN_HELPER": "1",
		"APP_NAME":                 "Ghostable",
	})
	t.Setenv("SHELL_ONLY", "from-shell")

	input := &oneByteReader{reader: strings.NewReader(envRunHelperCommandLine() + "\n1\nn\nn\ny\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "run"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	if err := runner.runEnvRun(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		"Command to run",
		"Inject variables",
		"Validate before running?",
		"Mask command output?",
		"Inherit current shell environment?",
		"APP_NAME=Ghostable",
		"SHELL_ONLY=from-shell",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected interactive env run output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunEnvRunInteractiveCanSelectKeys(t *testing.T) {
	setupRepoForEnvRunTest(t, map[string]string{
		"GHOSTABLE_ENV_RUN_HELPER": "1",
		"APP_NAME":                 "Ghostable",
		"SECRET_TOKEN":             "super-secret-value",
	})

	input := &oneByteReader{reader: strings.NewReader(envRunHelperCommandLine() + "\n2\nAPP_NAME,GHOSTABLE_ENV_RUN_HELPER\nn\nn\ny\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "run"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	if err := runner.runEnvRun(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, "Available keys:") || !strings.Contains(text, "APP_NAME=Ghostable") {
		t.Fatalf("expected selected keys prompt and selected value:\n%s", text)
	}
	if !strings.Contains(text, "SECRET_TOKEN=") || strings.Contains(text, "super-secret-value") {
		t.Fatalf("expected unselected secret value to be omitted:\n%s", text)
	}
}

func TestRunEnvRunInteractiveSuggestsProjectCommands(t *testing.T) {
	root := setupRepoForEnvRunTest(t, map[string]string{
		"GHOSTABLE_ENV_RUN_HELPER": "1",
		"APP_NAME":                 "Ghostable",
	})
	if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{"scripts":{"test":"phpunit","post-install-cmd":"ignored"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"dev":"vite","build":"vite build"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("Custom command\n" + envRunHelperCommandLine() + "\n1\nn\nn\ny\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "run"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	if err := runner.runEnvRun(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"composer test", "npm run dev", "npm run build", "Custom command", "APP_NAME=Ghostable"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected suggested command output to contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "post-install-cmd") {
		t.Fatalf("did not expect composer event scripts to be suggested:\n%s", text)
	}
}

func TestRunEnvRunInteractiveHidesRiskySuggestionsForProduction(t *testing.T) {
	root := setupRepoForEnvRunTest(t, map[string]string{})
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("production", "production"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "GHOSTABLE_ENV_RUN_HELPER", "1", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "artisan"), []byte("#!/usr/bin/env php\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{"scripts":{"test":"phpunit","deploy":"ship"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"build":"vite build","migrate":"node migrate.js"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("Custom command\n" + envRunHelperCommandLine() + "\n1\nn\nn\ny\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "run", "--env", "production"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	if err := runner.runEnvRun(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"php artisan about", "composer test", "npm run build", "Custom command"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected production suggestions to contain %q:\n%s", expected, text)
		}
	}
	for _, hidden := range []string{"php artisan migrate", "composer deploy", "npm run migrate", "php artisan queue:work"} {
		if strings.Contains(text, hidden) {
			t.Fatalf("did not expect risky production suggestion %q:\n%s", hidden, text)
		}
	}
}

func TestRunEnvRunInteractiveRiskyCustomCommandRequiresEnvironmentName(t *testing.T) {
	root := setupRepoForEnvRunTest(t, map[string]string{})
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("production", "production"); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("echo migrate\nwrong\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "run", "--env", "production"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	err = runner.runEnvRun(runner.args[3:])
	if err == nil {
		t.Fatal("expected risky custom command to require production confirmation")
	}
	if !strings.Contains(err.Error(), "risky command canceled") {
		t.Fatalf("expected risky command cancellation, got %v", err)
	}
	if !strings.Contains(output.String(), "Type production to continue") {
		t.Fatalf("expected production confirmation prompt, got:\n%s", output.String())
	}
}

func TestRunEnvRunNoInheritOmitsShellValues(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "GHOSTABLE_ENV_RUN_HELPER", "1", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL_ONLY", "from-shell")

	var output bytes.Buffer
	args := append([]string{"ghostable", "env", "run", "--env", "default", "--no-inherit", "--"}, envRunHelperCommand()...)
	runner := NewRunner(args, strings.NewReader(""), &output, &output)
	if err := runner.runEnvRun(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, "APP_NAME=Ghostable") {
		t.Fatalf("expected Ghostable value to be injected:\n%s", text)
	}
	if !strings.Contains(text, "SHELL_ONLY=") || strings.Contains(text, "SHELL_ONLY=from-shell") {
		t.Fatalf("expected shell-only value to be omitted with --no-inherit:\n%s", text)
	}
}

func TestRunEnvRunOnlyFiltersInjectedValues(t *testing.T) {
	setupRepoForEnvRunTest(t, map[string]string{
		"GHOSTABLE_ENV_RUN_HELPER": "1",
		"APP_NAME":                 "Ghostable",
		"SECRET_TOKEN":             "super-secret-value",
	})

	var output bytes.Buffer
	args := append([]string{"ghostable", "env", "run", "--env", "default", "--only", "GHOSTABLE_ENV_RUN_HELPER,APP_NAME", "--"}, envRunHelperCommand()...)
	runner := NewRunner(args, strings.NewReader(""), &output, &output)
	if err := runner.runEnvRun(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, "APP_NAME=Ghostable") {
		t.Fatalf("expected selected value to be injected:\n%s", text)
	}
	if !strings.Contains(text, "SECRET_TOKEN=") || strings.Contains(text, "super-secret-value") {
		t.Fatalf("expected unselected value to be omitted:\n%s", text)
	}
}

func TestRunEnvRunMaskOutputRedactsInjectedValues(t *testing.T) {
	setupRepoForEnvRunTest(t, map[string]string{
		"GHOSTABLE_ENV_RUN_HELPER": "1",
		"SECRET_TOKEN":             "super-secret-value",
	})

	var output bytes.Buffer
	args := append([]string{"ghostable", "env", "run", "--env", "default", "--mask-output", "--"}, envRunHelperCommand()...)
	runner := NewRunner(args, strings.NewReader(""), &output, &output)
	if err := runner.runEnvRun(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if strings.Contains(text, "super-secret-value") {
		t.Fatalf("expected masked output to hide secret value:\n%s", text)
	}
	if !strings.Contains(text, "SECRET_TOKEN=[secret]") {
		t.Fatalf("expected masked output placeholder, got:\n%s", text)
	}
}

func TestRunEnvRunStrictRejectsMissingRequestedKeys(t *testing.T) {
	setupRepoForEnvRunTest(t, map[string]string{
		"GHOSTABLE_ENV_RUN_HELPER": "1",
	})

	var output bytes.Buffer
	args := append([]string{"ghostable", "env", "run", "--env", "default", "--only", "MISSING_KEY", "--strict", "--"}, envRunHelperCommand()...)
	runner := NewRunner(args, strings.NewReader(""), &output, &output)
	err := runner.runEnvRun(runner.args[3:])
	if err == nil {
		t.Fatal("expected missing key to fail in strict mode")
	}
	if !strings.Contains(err.Error(), "missing requested 1 key: MISSING_KEY") {
		t.Fatalf("expected missing key error, got %v", err)
	}
	if output.String() != "" {
		t.Fatalf("expected child command not to run, got output:\n%s", output.String())
	}
}

func TestRunEnvRunStrictValidatesSchemaBeforeRunning(t *testing.T) {
	root := setupRepoForEnvRunTest(t, map[string]string{
		"GHOSTABLE_ENV_RUN_HELPER": "1",
	})
	if err := os.WriteFile(filepath.Join(root, ".ghostable", "schema.yaml"), []byte("REQUIRED_KEY:\n  - required\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	args := append([]string{"ghostable", "env", "run", "--env", "default", "--strict", "--"}, envRunHelperCommand()...)
	runner := NewRunner(args, strings.NewReader(""), &output, &output)
	err := runner.runEnvRun(runner.args[3:])
	if err == nil {
		t.Fatal("expected strict validation to fail")
	}
	if !strings.Contains(err.Error(), "strict validation failed") {
		t.Fatalf("expected strict validation error, got %v", err)
	}
	text := output.String()
	if !strings.Contains(text, "REQUIRED_KEY") {
		t.Fatalf("expected validation output to mention REQUIRED_KEY, got:\n%s", text)
	}
	if strings.Contains(text, "APP_NAME=") || strings.Contains(text, "SHELL_ONLY=") {
		t.Fatalf("expected child command not to run, got output:\n%s", text)
	}
}

func TestRunEnvRunPropagatesChildExitCode(t *testing.T) {
	setupRepoForEnvRunTest(t, map[string]string{
		"GHOSTABLE_ENV_RUN_HELPER": "1",
		"EXIT_CODE":                "7",
	})

	var output bytes.Buffer
	args := append([]string{"ghostable", "env", "run", "--env", "default", "--"}, envRunHelperCommand()...)
	code := Run(args, strings.NewReader(""), &output, &output)
	if code != 7 {
		t.Fatalf("expected child exit code 7, got %d with output:\n%s", code, output.String())
	}
}

func TestEnvHelpHidesInternalCommands(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "--help"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := stripAppColorCodes(output.String())
	if strings.Contains(text, "\n  file") || strings.Contains(text, "Save env file content") {
		t.Fatalf("did not expect hidden env file command in help:\n%s", text)
	}
	if strings.Contains(text, "\n  duplicate") || strings.Contains(text, "Create an environment from another") {
		t.Fatalf("did not expect hidden env duplicate command in help:\n%s", text)
	}
	if strings.Contains(text, "\n  layout") || strings.Contains(text, "Manage environment key order") {
		t.Fatalf("did not expect hidden env layout command in help:\n%s", text)
	}
}

func setupRepoForEnvRunTest(t *testing.T, values map[string]string) string {
	t.Helper()
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range values {
		if err := repo.SetVariable("default", key, value, "test"); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func envRunHelperCommand() []string {
	return []string{os.Args[0], "-test.run=TestEnvRunHelperProcess"}
}

func envRunHelperCommandLine() string {
	return strconv.Quote(os.Args[0]) + " -test.run=TestEnvRunHelperProcess"
}

func TestEnvRunHelperProcess(t *testing.T) {
	if os.Getenv("GHOSTABLE_ENV_RUN_HELPER") != "1" {
		return
	}
	fmt.Fprintf(os.Stdout, "APP_NAME=%s\n", os.Getenv("APP_NAME"))
	fmt.Fprintf(os.Stdout, "SECRET_TOKEN=%s\n", os.Getenv("SECRET_TOKEN"))
	fmt.Fprintf(os.Stdout, "SHELL_ONLY=%s\n", os.Getenv("SHELL_ONLY"))
	if os.Getenv("EXIT_CODE") == "7" {
		os.Exit(7)
	}
	os.Exit(0)
}

func TestRunEnvDiffWithFromAndToComparesEnvironments(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("staging", "staging"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("staging", "ALPHA", "two", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("staging", "BETA", "two", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "diff", "--from", "default", "--to", "staging", "--show-values"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvDiff(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		"Diff:",
		success("default"),
		success("staging"),
		"~ ALPHA",
		"=one -> two",
		"- BETA",
		"=two",
		"0 added, 1 changed, 1 removed, 0 unchanged",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected env diff output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunEnvDiffRequiresDifferentSourceAndTarget(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "diff", "--from", "default", "--to", "default"}, strings.NewReader(""), &output, &output)

	err := runner.runEnvDiff(runner.args[3:])
	if err == nil {
		t.Fatal("expected same source and target to fail")
	}
	if !strings.Contains(err.Error(), "target environment must be different from source environment") {
		t.Fatalf("expected same-environment error, got %v", err)
	}
}

func TestRunEnvDiffPromptsForSourceAndTargetEnvironments(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("production", "production"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("staging", "staging"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("staging", "ALPHA", "two", "test"); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("1\n1\n2\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "diff"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runEnvDiff(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		"Compare",
		"Select source environment",
		"Select target environment",
		"Diff:",
		success("default"),
		success("staging"),
		"1 changed",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected interactive env diff output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunEnvDiffCanPromptForFileComparison(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("staging", "staging"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("ALPHA=two\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("2\n1\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "diff", "--show-values"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runEnvDiff(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		"Compare",
		"env file",
		"Select an environment",
		"Diff:",
		success(".env"),
		success("default"),
		"~ ALPHA",
		"=two -> one",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected interactive file diff output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunEnvDiffWithEnvAndFileKeepsFileComparison(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("ALPHA=two\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "diff", "--env", "default", "--file", ".env", "--show-values"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvDiff(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		"Diff:",
		success(".env"),
		success("default"),
		"~ ALPHA",
		"=two -> one",
		"0 added, 1 changed, 0 removed, 0 unchanged",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected file diff output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunEnvCreateFromEnvDefaultsToNonSensitiveValues(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "create", "staging", "--from-env", "default"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvCreate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	repo, err = store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	values, err := repo.ReadVariables("staging")
	if err != nil {
		t.Fatal(err)
	}
	if values["APP_NAME"].Value != "Ghostable" {
		t.Fatalf("expected APP_NAME to be copied, got %#v", values)
	}
	if _, ok := values["APP_KEY"]; ok {
		t.Fatalf("did not expect sensitive-looking APP_KEY to be copied: %#v", values)
	}
	if !strings.Contains(output.String(), `Created "staging" environment and seeded 1 variable from default.`) {
		t.Fatalf("expected seed summary, got:\n%s", output.String())
	}
	if strings.Contains(output.String(), "created,") || strings.Contains(output.String(), "updated,") || strings.Contains(output.String(), "unchanged.") {
		t.Fatalf("did not expect push-style counts in env create seed summary:\n%s", output.String())
	}
}

func TestRunEnvCreateFromEnvCanCopyKeysOnly(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "create", "staging", "--from-env", "default", "--seed", "keys-only"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvCreate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	repo, err = store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	values, err := repo.ReadVariables("staging")
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatalf("did not expect values to be copied: %#v", values)
	}
	var layout domain.Layout
	content, err := os.ReadFile(filepath.Join(root, ".ghostable", "environments", "staging", "layout.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &layout); err != nil {
		t.Fatal(err)
	}
	if len(layout.Keys) != 2 || layout.Keys["APP_NAME"] == 0 || layout.Keys["APP_KEY"] == 0 {
		t.Fatalf("expected key layout to be copied, got %#v", layout.Keys)
	}
	if !strings.Contains(output.String(), `Created "staging" environment and seeded key layout from default with 2 keys.`) {
		t.Fatalf("expected key layout seed summary, got:\n%s", output.String())
	}
}

func TestRunEnvCreatePromptsForSourceEnvironmentAndCopyMode(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("y\n1\n2\n4\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "create", "staging"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runEnvCreate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		success("Create Environment"),
		promptAnswerText("Base this environment on an existing environment?", "Yes"),
		promptAnswerText("Select source environment", "default"),
		promptAnswerText("Copy values", "all"),
		"Environment type",
		"keys only",
		"No values",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected prompt %q in output:\n%s", expected, text)
		}
	}
	for _, expected := range []string{
		success("Yes") + "\n\nUse arrow keys to move, Enter to select",
		success("default") + "\n\nUse arrow keys to move, Enter to select",
		success("all") + "\n\nUse arrow keys to move, Enter to select",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected blank line before next menu after answer %q in output:\n%s", expected, text)
		}
	}
	intro := strings.Index(text, "Create Environment")
	basePrompt := strings.Index(text, "Base this environment")
	typePrompt := strings.Index(text, "Environment type")
	if intro < 0 || basePrompt < 0 || typePrompt < 0 || !(intro < basePrompt && basePrompt < typePrompt) {
		t.Fatalf("expected intro and base/copy prompts before environment type prompt:\n%s", text)
	}
	repo, err = store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	values, err := repo.ReadVariables("staging")
	if err != nil {
		t.Fatal(err)
	}
	if values["APP_KEY"].Value != "secret" {
		t.Fatalf("expected all mode to copy APP_KEY, got %#v", values)
	}
}

func TestRunEnvListPrintsSummaryTable(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "list"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvList(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"Name", "Type", "Variables", "Last updated", "default", "local", "1"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected env list output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunEnvListJSONIncludesSummaryFields(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "list", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvList(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	var rows []envListRow
	if err := json.Unmarshal(output.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one env row, got %#v", rows)
	}
	row := rows[0]
	if row.Name != "default" || row.Type != "local" || row.Variables != 1 || row.LastUpdated == "" {
		t.Fatalf("unexpected env row: %#v", row)
	}
}

func TestRunEnvPushAcceptsAbsoluteFilePath(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	envFile := filepath.Join(t.TempDir(), ".env.seed")
	if err := os.WriteFile(envFile, []byte("APP_NAME=Ghostable\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "push", "--env", "default", "--file", envFile, "--assume-yes", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvPush(runner.args[3:], false); err != nil {
		t.Fatal(err)
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	variable, exists, err := repo.GetVariable("default", "APP_NAME")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Value != "Ghostable" {
		t.Fatalf("expected absolute file env push to store APP_NAME, got exists=%v variable=%#v", exists, variable)
	}
}

func TestRunEnvFileSaveRejectsOutsideProjectPath(t *testing.T) {
	setupRepoForEnvCommandTest(t)
	outside := filepath.Join(t.TempDir(), ".env.outside")
	content := base64.StdEncoding.EncodeToString([]byte("APP_NAME=Ghostable\n"))

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "file", "save", "--file", outside, "--content-base64", content}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err == nil || !strings.Contains(err.Error(), "must stay inside the project") {
		t.Fatalf("expected outside env file save path to be rejected, got %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("env file save should not write outside project, stat err: %v", err)
	}
}

func TestRunEnvFileSaveRequiresExplicitInputs(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "file", "save", "--file", ".env.generated"}, strings.NewReader(""), &output, &output)

	err := runner.Run()
	if err == nil {
		t.Fatal("expected missing content-base64 to fail")
	}
	if !strings.Contains(err.Error(), "--file and --content-base64 are required") {
		t.Fatalf("expected missing content-base64 error, got %v", err)
	}
}

func TestRunEnvHistoryPrintsSummaryTable(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_KEY", "secret", "added key"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "history", "--env", "default", "--key", "APP_KEY"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvHistory(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"When", "Action", "Environment", "Key", "Device", "APP_KEY", "test-device"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected history output to contain %q:\n%s", expected, text)
		}
	}
	for _, expected := range []string{success("variable.created"), success("default")} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected colorized history output to contain %q:\n%s", expected, text)
		}
	}
	if !strings.Contains(text, "AM ") && !strings.Contains(text, "PM ") {
		t.Fatalf("expected history timestamp to include AM/PM local time:\n%s", text)
	}
}

func TestRunEnvHistoryPrintsEmptyMessage(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "history", "--key", "MISSING_KEY"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvHistory(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, warn("No history events found.")) {
		t.Fatalf("expected empty history message, got:\n%s", text)
	}
}

func setupRepoForEnvCommandTest(t *testing.T) string {
	t.Helper()
	root := setupTempWorkdir(t)
	_, _, err := store.Setup(".", store.SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "default", Type: "local"}},
		DeviceName:   "test-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

type oneByteReader struct {
	reader *strings.Reader
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.reader.Read(p)
}

func promptAnswerText(label string, answer string) string {
	separator := ": "
	if strings.HasSuffix(strings.TrimSpace(label), "?") {
		separator = " "
	}
	return warn(label) + separator + success(answer)
}
