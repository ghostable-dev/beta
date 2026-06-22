package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/prompt"
	"github.com/ghostable-dev/beta/internal/store"
)

func TestRunVarPullSelectsExistingVariable(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "BETA", "two", "test"); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("2\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "pull", "--env", "default", "--show-values"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runVarPull(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "Select a variable") {
		t.Fatalf("expected variable selector, got:\n%s", text)
	}
	if !strings.Contains(text, "BETA=two") {
		t.Fatalf("expected selected variable output, got:\n%s", text)
	}
}

func TestVarHelpShowsPromoteAndHidesCopy(t *testing.T) {
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "--help"}, strings.NewReader(""), &output, &output)

	if err := runner.runVar(runner.args[2:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, "promote") {
		t.Fatalf("expected var help to show promote:\n%s", text)
	}
	if strings.Contains(text, "copy") {
		t.Fatalf("did not expect var help to show copy:\n%s", text)
	}
}

func TestRunVarPushSelectsVariableFromFile(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	envFile := filepath.Join(root, ".env.seed")
	if err := os.WriteFile(envFile, []byte("ALPHA=one\nBETA=two\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("2\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push", "--env", "default", "--file", ".env.seed"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runVarPush(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "Select a variable") {
		t.Fatalf("expected variable selector, got:\n%s", text)
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	variable, exists, err := repo.GetVariable("default", "BETA")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Value != "two" {
		t.Fatalf("expected selected file variable to be stored, got exists=%v variable=%#v", exists, variable)
	}
	if _, exists, err := repo.GetVariable("default", "ALPHA"); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Fatal("did not expect unselected file variable to be stored")
	}
}

func TestRunVarPushAcceptsAbsoluteFilePath(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	envFile := filepath.Join(t.TempDir(), ".env.seed")
	if err := os.WriteFile(envFile, []byte("ALPHA=one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push", "--env", "default", "--key", "ALPHA", "--file", envFile, "--json"}, strings.NewReader(""), &output, &output)

	if err := runner.runVarPush(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	variable, exists, err := repo.GetVariable("default", "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Value != "one" {
		t.Fatalf("expected absolute file variable to be stored, got exists=%v variable=%#v", exists, variable)
	}
}

func TestRunVarPushStoresCommentedFileVariable(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	envFile := filepath.Join(t.TempDir(), ".env.seed")
	if err := os.WriteFile(envFile, []byte("# ALPHA=one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push", "--env", "default", "--key", "ALPHA", "--file", envFile, "--json"}, strings.NewReader(""), &output, &output)

	if err := runner.runVarPush(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	variable, exists, err := repo.GetVariable("default", "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Value != "one" || !variable.Commented {
		t.Fatalf("expected commented variable to be stored disabled, got exists=%v variable=%#v", exists, variable)
	}
	if !strings.Contains(output.String(), `"commented": true`) {
		t.Fatalf("expected JSON payload to include commented state, got:\n%s", output.String())
	}
}

func TestRunVarPushStoresVaporSecretMetadata(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	envFile := filepath.Join(t.TempDir(), ".env.seed")
	if err := os.WriteFile(envFile, []byte("ALPHA=one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push", "--env", "default", "--key", "ALPHA", "--file", envFile, "--vapor-secret", "--json"}, strings.NewReader(""), &output, &output)

	if err := runner.runVarPush(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	variable, exists, err := repo.GetVariable("default", "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || !variable.VaporSecret {
		t.Fatalf("expected variable to be marked as a Vapor Secret, got exists=%v variable=%#v", exists, variable)
	}
	if !strings.Contains(output.String(), `"vaporSecret": true`) {
		t.Fatalf("expected JSON payload to include Vapor Secret state, got:\n%s", output.String())
	}
}

func TestRunVarVaporSecretTogglesMetadata(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariableVaporSecret("default", "ALPHA", true, "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "vapor-secret", "--env", "default", "--key", "ALPHA", "--enabled=false", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.runVarVaporSecret(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	variable, exists, err := repo.GetVariable("default", "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.VaporSecret {
		t.Fatalf("expected variable to be unmarked as a Vapor Secret, got exists=%v variable=%#v", exists, variable)
	}
	if !strings.Contains(output.String(), `"vaporSecret": false`) {
		t.Fatalf("expected JSON payload to include disabled Vapor Secret state, got:\n%s", output.String())
	}
}

func TestRunVarPromoteCopiesValueAndMetadata(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("staging", "staging"); err != nil {
		t.Fatal(err)
	}
	commented := true
	vaporSecret := true
	if err := repo.SetVariableWithOptions("default", "APP_KEY", "secret", store.VariableWriteOptions{
		Reason:      "test",
		Commented:   &commented,
		VaporSecret: &vaporSecret,
	}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "promote", "--from", "default", "--to", "staging", "--key", "APP_KEY", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.runVarPromote(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	variable, exists, err := repo.GetVariable("staging", "APP_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Value != "secret" || !variable.Commented || !variable.VaporSecret {
		t.Fatalf("expected copied value and metadata, got exists=%v variable=%#v", exists, variable)
	}
	for _, expected := range []string{
		`"key": "APP_KEY"`,
		`"mode": "value"`,
		`"promoted": true`,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("expected JSON output to contain %q, got:\n%s", expected, output.String())
		}
	}
}

func TestRunVarPromoteKeepsCopyAlias(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("staging", "staging"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "copy", "--from", "default", "--to", "staging", "--key", "APP_KEY"}, strings.NewReader(""), &output, &output)
	if err := runner.runVar(runner.args[2:]); err != nil {
		t.Fatal(err)
	}

	variable, exists, err := repo.GetVariable("staging", "APP_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Value != "secret" {
		t.Fatalf("expected hidden copy alias to promote value, got exists=%v variable=%#v", exists, variable)
	}
	if !strings.Contains(output.String(), success("Promoted APP_KEY from default to staging.")) {
		t.Fatalf("expected promotion output, got:\n%s", output.String())
	}
}

func TestRunVarPromotePromptsForKeyOnlyPromotion(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("staging", "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("production", "production"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("1\n2\n1\n2\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "promote"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runVarPromote(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		"Select source environment",
		"Select target environment",
		"Select a variable",
		"Promotion mode",
		promptAnswerText("Promotion mode", "key only"),
		success("Added ALPHA to staging layout from default."),
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected var promote output to contain %q:\n%s", expected, text)
		}
	}
	values, err := repo.ReadVariables("staging")
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatalf("did not expect key-only promotion to write values, got %#v", values)
	}
	var layout domain.Layout
	content, err := os.ReadFile(filepath.Join(root, ".ghostable", "environments", "staging", "layout.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &layout); err != nil {
		t.Fatal(err)
	}
	if layout.Keys["ALPHA"] == 0 {
		t.Fatalf("expected ALPHA in staging layout, got %#v", layout.Keys)
	}
}

func TestRunVarPullAcceptsAbsoluteFilePath(t *testing.T) {
	setupRepoForVarCommandTest(t)
	repo, err := store.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}

	envFile := filepath.Join(t.TempDir(), ".env.out")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "pull", "--env", "default", "--key", "ALPHA", "--file", envFile, "--json"}, strings.NewReader(""), &output, &output)

	if err := runner.runVarPull(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "ALPHA=one") {
		t.Fatalf("expected absolute output file to contain ALPHA, got:\n%s", string(content))
	}
}

func setupRepoForVarCommandTest(t *testing.T) string {
	t.Helper()
	return setupRepoForEnvCommandTest(t)
}
