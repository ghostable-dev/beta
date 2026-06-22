package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/store"
)

func TestRunValidateChecksStoredEnvironmentValues(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "validate", "--env", "default"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), success("Validation passed.")) {
		t.Fatalf("expected validation success output, got:\n%s", output.String())
	}
}

func TestRunValidateHelp(t *testing.T) {
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "validate", "--help"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		"Usage: ghostable validate [options]",
		"--env <ENV>",
		"--file <PATH>",
		"--json",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected validate help to contain %q:\n%s", expected, text)
		}
	}
}
