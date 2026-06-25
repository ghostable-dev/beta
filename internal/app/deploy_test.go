package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/store"
)

func TestRunDeployWritesEnvironmentToDotenv(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "production"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "APP_NAME=Ghostable") {
		t.Fatalf("expected .env to contain deployed value, got:\n%s", string(content))
	}
	if strings.Contains(output.String(), "Ghostable\n") {
		t.Fatalf("did not expect deploy output to print plaintext value:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "👻 Ghostable deploy successful.") {
		t.Fatalf("expected deploy output to include success headline, got:\n%s", output.String())
	}
	if !strings.Contains(output.String(), warn("Environment:")+" production") ||
		!strings.Contains(output.String(), warn("File:")+" .env") ||
		!strings.Contains(output.String(), warn("Variables:")+" 1 variable") ||
		!strings.Contains(output.String(), warn("Device:")+" test-device (") {
		t.Fatalf("expected deploy output to include environment, file, count, and device, got:\n%s", output.String())
	}

	repo, err = store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	events, err := repo.History("production", "", "env.pulled", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("deploy should not record env.pulled events, got %#v", events)
	}
}

func TestRunDeployReplacesDotenvByDefault(t *testing.T) {
	root := setupDeployCommandTest(t)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("STALE=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "--env", "production"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Contains(text, "STALE=value") {
		t.Fatalf("expected deploy to replace stale .env values by default, got:\n%s", text)
	}
	if !strings.Contains(text, "APP_NAME=Ghostable") {
		t.Fatalf("expected deployed value, got:\n%s", text)
	}
}

func TestRunDeployCanMergeDotenv(t *testing.T) {
	root := setupDeployCommandTest(t)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("STALE=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "--env", "production", "--merge"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "STALE=value") || !strings.Contains(text, "APP_NAME=Ghostable") {
		t.Fatalf("expected deploy --merge to preserve and add values, got:\n%s", text)
	}
}

func TestRunDeployUsesAutomationCredentialToken(t *testing.T) {
	root := setupDeployCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	credential, err := repo.CreateAutomationCredential("deploy-bot", "deploy", []store.AutomationCredentialGrant{
		{EnvironmentName: "production", Role: "reader"},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("GHOSTABLE_CI_TOKEN", credential.Token)
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "empty-keys"))

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "production"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "APP_NAME=Ghostable") {
		t.Fatalf("expected token-backed deploy to write decrypted value, got:\n%s", string(content))
	}
	if !strings.Contains(output.String(), warn("Device:")+" deploy-bot (") ||
		!strings.Contains(output.String(), warn("Source:")+" GHOSTABLE_CI_TOKEN") {
		t.Fatalf("expected token-backed deploy output to include credential device and token source, got:\n%s", output.String())
	}
}

func setupDeployCommandTest(t *testing.T) string {
	t.Helper()
	allowProtectedEnvironmentAccessForTest(t)
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("production", "production"); err != nil {
		t.Fatal(err)
	}
	return root
}
