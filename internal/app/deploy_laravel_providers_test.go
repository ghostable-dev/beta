package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/store"
)

func TestRunDeployCloudInvokesLaravelCloudCLI(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "API_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "cloud.log")
	cloudPath := filepath.Join(binDir, "cloud")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$CLOUD_LOG\"\n" +
		"exit 0\n"
	if err := os.WriteFile(cloudPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLOUD_LOG", logPath)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-cloud", "production", "--cloud-env", "cloud-prod", "--only", "APP_NAME"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	logContent, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logContent)
	for _, expected := range []string{
		"environment:variables cloud-prod --json --no-interaction --action=set --key=APP_NAME --value=Ghostable",
	} {
		if !strings.Contains(logText, expected) {
			t.Fatalf("expected Laravel Cloud CLI log to contain %q, got:\n%s", expected, logText)
		}
	}
	if strings.Contains(logText, "API_KEY") {
		t.Fatalf("did not expect --only filtered key to be synced, got:\n%s", logText)
	}
	if _, err := os.Stat(filepath.Join(root, ".env")); !os.IsNotExist(err) {
		t.Fatalf("Laravel Cloud deploy should not write .env, stat err: %v", err)
	}
	if !strings.Contains(output.String(), "👻 Ghostable Laravel Cloud deploy successful.") {
		t.Fatalf("expected Cloud deploy success output, got:\n%s", output.String())
	}
}

func TestRunDeployLaravelCloudUsesCloudTarget(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-cloud", "production", "--dry-run", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, `"target": "laravel-cloud"`) || !strings.Contains(text, `"provider": "Laravel Cloud"`) {
		t.Fatalf("expected laravel-cloud route to use Cloud target JSON, got:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(root, ".env")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write .env, stat err: %v", err)
	}
}

func TestRunDeployCloudRedactsValueFromCLIError(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	cloudPath := filepath.Join(binDir, "cloud")
	script := "#!/bin/sh\n" +
		"echo \"failed with $@\" >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(cloudPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-cloud", "production"}, strings.NewReader(""), &output, &output)
	err = runner.Run()
	if err == nil {
		t.Fatal("expected Laravel Cloud CLI failure")
	}
	if strings.Contains(err.Error(), "Ghostable") {
		t.Fatalf("expected CLI error to redact variable value, got %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("expected CLI error to include redaction marker, got %v", err)
	}
}

func TestRunDeployCloudRejectsProjectLocalCloudCLI(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cloudPath := filepath.Join(binDir, "cloud")
	if err := os.WriteFile(cloudPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-cloud", "production"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err == nil || !strings.Contains(err.Error(), "refusing to run Laravel Cloud CLI from project path") {
		t.Fatalf("expected project-local Laravel Cloud CLI to be rejected, got %v", err)
	}
}

func TestRunDeployLaravelVaporUsesVaporTarget(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-vapor", "production", "--dry-run"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(output.String(), "👻 Ghostable Vapor deploy plan.") {
		t.Fatalf("expected laravel-vapor route to use Vapor deploy path, got:\n%s", output.String())
	}
}

func TestRunDeployShortProviderTargetsRouteToProviders(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{name: "cloud", args: []string{"ghostable", "deploy", "cloud", "production", "--dry-run", "--json"}, expected: `"target": "laravel-cloud"`},
		{name: "vapor", args: []string{"ghostable", "deploy", "vapor", "production", "--dry-run"}, expected: "Ghostable Vapor deploy plan"},
		{name: "forge", args: []string{"ghostable", "deploy", "forge", "production", "--forge-site", "example.com", "--dry-run", "--json"}, expected: `"target": "laravel-forge"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			runner := NewRunner(test.args, strings.NewReader(""), &output, &output)
			if err := runner.Run(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), test.expected) {
				t.Fatalf("expected short provider target to route to %q, got:\n%s", test.expected, output.String())
			}
		})
	}
}

func TestRunDeployUsesManifestDeployTargetByDefault(t *testing.T) {
	root := setupDeployCommandTestWithTarget(t, "laravel-cloud")
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "production", "--dry-run", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, `"target": "laravel-cloud"`) || !strings.Contains(text, `"cloudEnvironment": "production"`) {
		t.Fatalf("expected manifest deploy target to route to Laravel Cloud, got:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(root, ".env")); !os.IsNotExist(err) {
		t.Fatalf("provider deploy dry-run should not write .env, stat err: %v", err)
	}
}

func TestRunDeployLocalFileOptionsBypassManifestDeployTarget(t *testing.T) {
	root := setupDeployCommandTestWithTarget(t, "laravel-cloud")
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "production", "--file", ".env.deploy", "--dry-run", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if strings.Contains(text, `"target": "laravel-cloud"`) {
		t.Fatalf("expected local file option to bypass provider target, got:\n%s", text)
	}
	if !strings.Contains(text, `"file": ".env.deploy"`) || !strings.Contains(text, `"valueOmitted": true`) {
		t.Fatalf("expected local deploy dry-run JSON, got:\n%s", text)
	}
}

func TestRunDeployForgeInvokesLaravelForgeCLI(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "API_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "forge.log")
	forgePath := filepath.Join(binDir, "forge")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"$FORGE_LOG\"\n" +
		"if [ \"$1\" = \"env:pull\" ]; then printf 'EXISTING=1\\n' > \"$3\"; exit 0; fi\n" +
		"if [ \"$1\" = \"env:push\" ]; then cat \"$3\" >> \"$FORGE_LOG\"; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(forgePath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FORGE_LOG", logPath)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-forge", "production", "--forge-site", "example.com", "--only", "APP_NAME"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	logContent, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logContent)
	for _, expected := range []string{
		"env:pull example.com ",
		"env:push example.com ",
		"EXISTING=1",
		"APP_NAME=Ghostable",
	} {
		if !strings.Contains(logText, expected) {
			t.Fatalf("expected Laravel Forge CLI log to contain %q, got:\n%s", expected, logText)
		}
	}
	if strings.Contains(logText, "API_KEY") {
		t.Fatalf("did not expect --only filtered key to be synced, got:\n%s", logText)
	}
	if _, err := os.Stat(filepath.Join(root, ".env")); !os.IsNotExist(err) {
		t.Fatalf("Laravel Forge deploy should not write .env, stat err: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "👻 Ghostable Laravel Forge deploy successful.") ||
		!strings.Contains(text, warn("Forge site:")+" example.com") {
		t.Fatalf("expected Forge deploy output to include provider success and site details, got:\n%s", text)
	}
}

func setupDeployCommandTestWithTarget(t *testing.T, deployTarget string) string {
	t.Helper()
	allowProtectedEnvironmentAccessForTest(t)
	root := setupTempWorkdir(t)
	_, _, err := store.Setup(".", store.SetupOptions{
		Name: "Test Project",
		Environments: []domain.Environment{
			{Name: "default", Type: "local"},
			{Name: "production", Type: "production"},
		},
		DeviceName:   "test-device",
		DeployTarget: deployTarget,
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRunDeployForgeRejectsProjectLocalForgeCLI(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	forgePath := filepath.Join(binDir, "forge")
	if err := os.WriteFile(forgePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-forge", "production", "--forge-site", "example.com"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err == nil || !strings.Contains(err.Error(), "refusing to run Laravel Forge CLI from project path") {
		t.Fatalf("expected project-local Laravel Forge CLI to be rejected, got %v", err)
	}
}

func TestRunDeployForgeRequiresSite(t *testing.T) {
	setupDeployCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-forge", "production", "--dry-run"}, strings.NewReader(""), &output, &output)
	err := runner.Run()
	if err == nil || !strings.Contains(err.Error(), "Laravel Forge site is required") {
		t.Fatalf("expected Forge deploy to require --forge-site, got %v", err)
	}
}

func TestRunDeployForgeRedactsValuesFromCLIError(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	forgePath := filepath.Join(binDir, "forge")
	script := "#!/bin/sh\n" +
		"echo \"failed with Ghostable\" >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(forgePath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-forge", "production", "--forge-site", "example.com"}, strings.NewReader(""), &output, &output)
	err = runner.Run()
	if err == nil {
		t.Fatal("expected Laravel Forge CLI failure")
	}
	if strings.Contains(err.Error(), "Ghostable") {
		t.Fatalf("expected CLI error to redact variable value, got %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("expected CLI error to include redaction marker, got %v", err)
	}
}
