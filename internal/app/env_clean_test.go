package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunEnvCleanDryRunListsLocalEnvFiles(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	writeEnvCleanTestFile(t, root, ".env")
	writeEnvCleanTestFile(t, root, ".env.production")
	writeEnvCleanTestFile(t, root, ".env.production.ghostable-backup-20260625T120000Z")
	writeEnvCleanTestFile(t, root, ".env.example")
	writeEnvCleanTestFile(t, root, ".env.example.local")
	writeEnvCleanTestFile(t, root, ".envrc")
	if err := os.Mkdir(filepath.Join(root, ".env.directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeEnvCleanTestFile(t, filepath.Join(root, "nested"), ".env")

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "clean", "--dry-run"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvClean(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{".env", ".env.production", ".env.production.ghostable-backup-20260625T120000Z"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected dry run output to contain %q:\n%s", expected, text)
		}
		assertEnvCleanTestFileExists(t, root, expected)
	}
	for _, unexpected := range []string{".env.example", ".env.example.local", ".envrc", ".env.directory", "nested/.env"} {
		if strings.Contains(text, unexpected) {
			t.Fatalf("did not expect dry run output to contain %q:\n%s", unexpected, text)
		}
	}
}

func TestRunEnvCleanRemovesLocalEnvFilesWithAssumeYes(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	writeEnvCleanTestFile(t, root, ".env")
	writeEnvCleanTestFile(t, root, ".env.local")
	writeEnvCleanTestFile(t, root, ".env.example")
	writeEnvCleanTestFile(t, root, ".envrc")

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "clean", "--assume-yes", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvClean(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	var result envCleanResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	expected := []string{".env", ".env.local"}
	if !reflect.DeepEqual(result.Files, expected) || !reflect.DeepEqual(result.Removed, expected) {
		t.Fatalf("expected clean result for env files only, got %#v", result)
	}
	assertEnvCleanTestFileMissing(t, root, ".env")
	assertEnvCleanTestFileMissing(t, root, ".env.local")
	assertEnvCleanTestFileExists(t, root, ".env.example")
	assertEnvCleanTestFileExists(t, root, ".envrc")
}

func TestRunEnvCleanCanIncludeExampleFiles(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	writeEnvCleanTestFile(t, root, ".env.example")
	writeEnvCleanTestFile(t, root, ".env.example.local")

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "clean", "--include-example", "--assume-yes", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.runEnvClean(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	var result envCleanResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	expected := []string{".env.example", ".env.example.local"}
	if !reflect.DeepEqual(result.Removed, expected) {
		t.Fatalf("expected example files to be removed, got %#v", result)
	}
	assertEnvCleanTestFileMissing(t, root, ".env.example")
	assertEnvCleanTestFileMissing(t, root, ".env.example.local")
}

func TestRunEnvCleanRequiresConfirmation(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	writeEnvCleanTestFile(t, root, ".env")

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "clean"}, strings.NewReader(""), &output, &output)
	err := runner.runEnvClean(runner.args[3:])
	if err == nil {
		t.Fatal("expected env clean to require confirmation")
	}
	if !strings.Contains(err.Error(), "pass --assume-yes to confirm") {
		t.Fatalf("expected confirmation error, got %v", err)
	}
	assertEnvCleanTestFileExists(t, root, ".env")
}

func writeEnvCleanTestFile(t *testing.T, root string, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte("APP_NAME=Ghostable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertEnvCleanTestFileExists(t *testing.T, root string, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, name)); err != nil {
		t.Fatalf("expected %s to exist: %v", name, err)
	}
}

func assertEnvCleanTestFileMissing(t *testing.T, root string, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat err: %v", name, err)
	}
}
