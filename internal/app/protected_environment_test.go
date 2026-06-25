package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/store"
	"github.com/ghostable-dev/beta/internal/userpresence"
)

func TestProtectedProductionEnvPullRequiresLocalUserPresence(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo := createProtectedProductionEnvironmentForTest(t, root)
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "pull", "--env", "production", "--file", ".env.production"}, strings.NewReader(""), &output, &output)

	err := runner.runEnvPull(runner.args[3:])
	if err == nil {
		t.Fatal("expected production env pull to require local user presence")
	}
	if !strings.Contains(err.Error(), "requires local user confirmation") {
		t.Fatalf("expected local confirmation error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".env.production")); !os.IsNotExist(statErr) {
		t.Fatalf("expected protected pull to leave .env.production unwritten, stat err: %v", statErr)
	}
}

func TestProtectedProductionEnvPullAllowsAutomationCredential(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo := createProtectedProductionEnvironmentForTest(t, root)
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
	runner := NewRunner([]string{"ghostable", "env", "pull", "--env", "production", "--file", ".env.production"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvPull(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env.production"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "APP_NAME=Ghostable") {
		t.Fatalf("expected automation credential pull to write production value, got:\n%s", string(content))
	}
}

func TestProtectedProductionEnvPullUsesLocalVerifier(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo := createProtectedProductionEnvironmentForTest(t, root)
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	called := false
	withProtectedEnvironmentVerifierForTest(t, func(request userpresence.Request) error {
		called = true
		if request.Environment != "production" || request.Operation != protectedOperationEnvPull {
			return fmt.Errorf("unexpected user-presence request: %#v", request)
		}
		return nil
	})

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "pull", "--env", "production", "--file", ".env.production"}, strings.NewReader(""), &output, &output)
	runner.interactive = true
	if err := runner.runEnvPull(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected protected production env pull to call the user-presence verifier")
	}
}

func TestDefaultEnvPullDoesNotRequireLocalVerifier(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	withProtectedEnvironmentVerifierForTest(t, func(request userpresence.Request) error {
		return fmt.Errorf("unexpected user-presence request: %#v", request)
	})

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "pull", "--env", "default", "--file", ".env"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvPull(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
}

func TestProductionDeployDryRunDoesNotRequireLocalVerifier(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo := createProtectedProductionEnvironmentForTest(t, root)
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	withProtectedEnvironmentVerifierForTest(t, func(request userpresence.Request) error {
		return fmt.Errorf("unexpected user-presence request: %#v", request)
	})

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "deploy", "production", "--dry-run"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Dry run: 1 variables would be written") {
		t.Fatalf("expected dry-run output, got:\n%s", output.String())
	}
}

func TestProductionEnvPullDryRunWithoutValuesDoesNotRequireLocalVerifier(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo := createProtectedProductionEnvironmentForTest(t, root)
	if err := repo.SetVariable("production", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	withProtectedEnvironmentVerifierForTest(t, func(request userpresence.Request) error {
		return fmt.Errorf("unexpected user-presence request: %#v", request)
	})

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "pull", "--env", "production", "--dry-run"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvPull(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Dry run: 1 variables would be written") {
		t.Fatalf("expected dry-run output, got:\n%s", output.String())
	}
}

func createProtectedProductionEnvironmentForTest(t *testing.T, root string) store.Repository {
	t.Helper()
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("production", "production"); err != nil {
		t.Fatal(err)
	}
	return repo
}

func allowProtectedEnvironmentAccessForTest(t *testing.T) {
	t.Helper()
	withProtectedEnvironmentVerifierForTest(t, func(request userpresence.Request) error {
		return nil
	})
}

func withProtectedEnvironmentVerifierForTest(t *testing.T, verifier func(userpresence.Request) error) {
	t.Helper()
	previous := verifyProtectedEnvironmentUserPresence
	verifyProtectedEnvironmentUserPresence = verifier
	t.Cleanup(func() {
		verifyProtectedEnvironmentUserPresence = previous
	})
}
