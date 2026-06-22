package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/manifest"
	"github.com/ghostable-dev/beta/internal/prompt"
	"github.com/ghostable-dev/beta/internal/store"
)

func TestRunSetupDefaultsEnvironmentWithoutPrompt(t *testing.T) {
	root := setupTempWorkdir(t)

	input := strings.NewReader("")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "setup", "--name", "Test Project", "--device-name", "test-device"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runSetup(runner.args[2:]); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Initial environments") {
		t.Fatal("setup should not prompt for initial environments")
	}

	file, err := os.Open(filepath.Join(root, ".ghostable", "ghostable.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	project, err := manifest.Read(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := project.Environments["default"]; !ok {
		t.Fatalf("expected default environment, got %#v", project.Environments)
	}
	if _, ok := project.Environments["local"]; ok {
		t.Fatalf("did not expect local environment, got %#v", project.Environments)
	}
}

func TestRunSetupPrintsBannerAfterPrompts(t *testing.T) {
	setupTempWorkdir(t)

	input := strings.NewReader("Prompted Project\nPrompted Device\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "setup"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runSetup(runner.args[2:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	projectPrompt := strings.Index(text, "Project name")
	devicePrompt := strings.Index(text, "Device label")
	banner := strings.Index(text, "▗▄▄▖")
	initialized := strings.Index(text, "Initialized Ghostable")
	if projectPrompt < 0 || devicePrompt < 0 || banner < 0 || initialized < 0 {
		t.Fatalf("expected prompts, banner, and setup notes in output:\n%s", text)
	}
	if !(projectPrompt < banner && devicePrompt < banner && banner < initialized) {
		t.Fatalf("expected prompts before banner and notes after banner:\n%s", text)
	}
}

func TestNormalizeDefaultDeviceName(t *testing.T) {
	tests := map[string]string{
		" Joe's Mac Studio.local ": "Joe's Mac Studio",
		"build-runner.LOCAL":       "build-runner",
		"workstation":              "workstation",
		"\x00":                     "",
	}

	for input, expected := range tests {
		if actual := normalizeDefaultDeviceName(input); actual != expected {
			t.Fatalf("normalizeDefaultDeviceName(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestRunSetupSeedsDefaultEnvironmentFromDotenvWhenConfirmed(t *testing.T) {
	root := setupTempWorkdir(t)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("APP_KEY=super-secret\nAPP_NAME=Ghostable\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("y\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "setup", "--name", "Test Project", "--device-name", "test-device"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runSetup(runner.args[2:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	seedPrompt := strings.Index(text, "A .env file was detected")
	banner := strings.Index(text, "▗▄▄▖")
	if seedPrompt < 0 || banner < 0 || seedPrompt > banner {
		t.Fatalf("expected .env prompt before banner:\n%s", text)
	}
	if !strings.Contains(text, "Imported 2 variables from .env into default.") {
		t.Fatalf("expected import summary in output:\n%s", text)
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	values, err := repo.ReadVariables("default")
	if err != nil {
		t.Fatal(err)
	}
	if values["APP_KEY"].Value != "super-secret" {
		t.Fatalf("unexpected APP_KEY value: %#v", values["APP_KEY"])
	}
	if values["APP_NAME"].Value != "Ghostable" {
		t.Fatalf("unexpected APP_NAME value: %#v", values["APP_NAME"])
	}
}

func TestRunStatusPrintsBannerAndInventory(t *testing.T) {
	setupTempWorkdir(t)
	repo, _, err := store.Setup(".", store.SetupOptions{
		Name:       "Status Project",
		DeviceName: "test-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "status"}, strings.NewReader(""), &output, &output)
	if err := runner.runStatus(runner.args[2:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		"▗▄▄▖",
		"Ghostable status",
		warn("Project      "),
		success("Status Project"),
		warn("Inventory"),
		success("1"),
		"Inventory",
		"Environments",
		"default",
		"Devices",
		"test-device",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected status output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunSetupAcceptsNoMetadataCompatibilityFlag(t *testing.T) {
	setupTempWorkdir(t)

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "setup",
		"--name", "Compat Project",
		"--env", "development",
		"--device-name", "test-device",
		"--no-metadata",
		"--json",
	}, strings.NewReader(""), &output, &output)
	if err := runner.runSetup(runner.args[2:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, `"created": true`) {
		t.Fatalf("expected setup JSON payload:\n%s", text)
	}
}

func TestRunStatusJSONDoesNotPrintBanner(t *testing.T) {
	setupTempWorkdir(t)
	if _, _, err := store.Setup(".", store.SetupOptions{
		Name:       "Status Project",
		DeviceName: "test-device",
	}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "status", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.runStatus(runner.args[2:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if strings.Contains(text, "▗▄▄▖") {
		t.Fatalf("did not expect banner in JSON output:\n%s", text)
	}
	if !strings.Contains(text, `"project"`) {
		t.Fatalf("expected status JSON payload:\n%s", text)
	}
}

func setupTempWorkdir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousWD); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))
	return root
}
