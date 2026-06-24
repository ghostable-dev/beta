package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/store"
)

func TestRunDeployVaporDryRunUsesVaporSecretMetadata(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariableVaporSecret("production", "APP_KEY", true, "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-vapor", "production", "--dry-run"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		"👻 Ghostable Vapor deploy plan.",
		warn("Environment:") + " production",
		warn("Vapor environment:") + " production",
		warn("Env vars:") + " 1",
		warn("Vapor secrets:") + " 1",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected Vapor dry run output to contain %q, got:\n%s", expected, text)
		}
	}
}

func TestRunDeployVaporInvokesVaporCLI(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariableVaporSecret("production", "APP_KEY", true, "test"); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "vapor.log")
	vaporPath := filepath.Join(binDir, "vapor")
	script := "#!/bin/sh\n" +
		"file=\"\"\n" +
		"for arg in \"$@\"; do case \"$arg\" in --file=*) file=\"${arg#--file=}\" ;; esac; done\n" +
		"echo \"$@\" >> \"$VAPOR_LOG\"\n" +
		"if [ \"$1\" = \"env:pull\" ]; then : > \"$file\"; fi\n" +
		"if [ \"$1\" = \"env:push\" ]; then cat \"$file\" >> \"$VAPOR_LOG\"; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(vaporPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VAPOR_LOG", logPath)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-vapor", "production", "--vapor-env", "staging"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	logContent, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logContent)
	for _, expected := range []string{
		"env:pull staging",
		"env:push staging",
		"APP_NAME=Ghostable",
		"secret staging --name=APP_KEY --file=",
	} {
		if !strings.Contains(logText, expected) {
			t.Fatalf("expected Vapor CLI log to contain %q, got:\n%s", expected, logText)
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".env.staging")); !os.IsNotExist(err) {
		t.Fatalf("expected Vapor deploy to leave no repo-local env file, stat err: %v", err)
	}
	envFiles := vaporEnvironmentFileArgs(logText, "ghostable-vapor-staging-")
	if len(envFiles) == 0 {
		t.Fatalf("expected Vapor env commands to use a temporary environment file, got:\n%s", logText)
	}
	for _, envFile := range envFiles {
		if strings.HasPrefix(envFile, root) {
			t.Fatalf("Vapor environment temp file should not be inside repo: %s", envFile)
		}
		if _, err := os.Stat(envFile); !os.IsNotExist(err) {
			t.Fatalf("expected Vapor environment temp file to be removed, stat err: %v", err)
		}
	}
	if !strings.Contains(output.String(), "👻 Ghostable Vapor deploy successful.") {
		t.Fatalf("expected successful Vapor deploy output, got:\n%s", output.String())
	}
}

func TestRunDeployVaporRejectsProjectLocalVaporCLI(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariableVaporSecret("production", "APP_KEY", true, "test"); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	vaporPath := filepath.Join(binDir, "vapor")
	if err := os.WriteFile(vaporPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-vapor", "production"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err == nil || !strings.Contains(err.Error(), "refusing to run Vapor CLI from project path") {
		t.Fatalf("expected project-local Vapor CLI to be rejected, got %v", err)
	}
}

func TestRunDeployVaporDoesNotUseRepoLocalEnvironmentFile(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	vaporPath := filepath.Join(binDir, "vapor")
	script := "#!/bin/sh\n" +
		"file=\"\"\n" +
		"for arg in \"$@\"; do case \"$arg\" in --file=*) file=\"${arg#--file=}\" ;; esac; done\n" +
		"if [ \"$1\" = \"env:pull\" ]; then : > \"$file\"; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(vaporPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	outside := filepath.Join(t.TempDir(), "outside.env")
	if err := os.WriteFile(outside, []byte("EXISTING=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".env.staging")); err != nil {
		t.Skipf("symlink creation is not available: %v", err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "laravel-vapor", "production", "--vapor-env", "staging"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatalf("expected Vapor deploy to avoid repo-local symlinked environment file, got %v", err)
	}
	content, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "Ghostable") {
		t.Fatalf("Vapor environment write escaped through symlink: %s", string(content))
	}
}

func vaporEnvironmentFileArgs(logText string, prefix string) []string {
	files := []string{}
	for _, field := range strings.Fields(logText) {
		path, ok := strings.CutPrefix(field, "--file=")
		if ok && strings.Contains(filepath.Base(path), prefix) {
			files = append(files, path)
		}
	}
	return files
}
