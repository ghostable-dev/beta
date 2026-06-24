package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if !strings.Contains(text, "annotation") {
		t.Fatalf("expected var help to show annotation:\n%s", text)
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

func TestRunVarPushFromFilePromptsForChangeReason(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	envFile := filepath.Join(root, ".env.seed")
	if err := os.WriteFile(envFile, []byte("ALPHA=one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("rotating beta credential\nn\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push", "--env", "default", "--file", ".env.seed", "--key", "ALPHA"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runVarPush(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Reason for this change") {
		t.Fatalf("expected change reason prompt, got:\n%s", output.String())
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	record := readValueRecordForAppTest(t, repo, "default", "ALPHA")
	if record.Secret.Change == nil || record.Secret.Change.Reason != "rotating beta credential" {
		t.Fatalf("expected prompted change reason in value record, got %#v", record.Secret.Change)
	}
}

func TestRunVarPushJSONDoesNotPromptForChangeReason(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	envFile := filepath.Join(root, ".env.seed")
	if err := os.WriteFile(envFile, []byte("ALPHA=one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push", "--env", "default", "--file", ".env.seed", "--key", "ALPHA", "--json"}, strings.NewReader(""), &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(strings.NewReader(""), &output)

	if err := runner.runVarPush(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Reason for this change") {
		t.Fatalf("did not expect change reason prompt in JSON mode, got:\n%s", output.String())
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	record := readValueRecordForAppTest(t, repo, "default", "ALPHA")
	if record.Secret.Change != nil {
		t.Fatalf("did not expect JSON mode to store a prompted change reason, got %#v", record.Secret.Change)
	}
}

func TestRunVarPushDefaultsToEnvironmentFileInInteractiveMode(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("ALPHA=one\nBETA=two\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("2\nn\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push", "--env", "default"}, input, &output, &output)
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
		t.Fatalf("expected selected default file variable to be stored, got exists=%v variable=%#v", exists, variable)
	}
}

func TestRunVarPushCanInferEnvironmentNameDotEnvFile(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("staging", "staging"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "staging.env"), []byte("STAGING_ONLY=yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("1\nn\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push", "--env", "staging"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runVarPush(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	repo, err = store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	variable, exists, err := repo.GetVariable("staging", "STAGING_ONLY")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Value != "yes" {
		t.Fatalf("expected staging.env variable to be stored, got exists=%v variable=%#v", exists, variable)
	}
}

func TestVarPushEmptyInferredFileOnlyAllowsNewVariable(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "OLD_KEY", "old", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push", "--env", "default"}, strings.NewReader(""), &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(strings.NewReader(""), &output)
	result, err := runner.tryVarPushFromFile(varPushFileInput{
		repo:                 repo,
		environment:          "default",
		file:                 ".env",
		fallbackOnMissingKey: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.handled || !result.newVariableOnly {
		t.Fatalf("expected empty inferred file to require a new variable, got %#v", result)
	}

	input := &oneByteReader{reader: strings.NewReader("new key\n")}
	output.Reset()
	runner = NewRunner([]string{"ghostable", "var", "push", "--env", "default"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	key, err := runner.selectVariableKeyForPush(repo, "default", "", result.newVariableOnly)
	if err != nil {
		t.Fatal(err)
	}
	if key != "NEW_KEY" {
		t.Fatalf("expected NEW_KEY, got %q", key)
	}
	if strings.Contains(output.String(), "OLD_KEY") || strings.Contains(output.String(), "Select a variable") {
		t.Fatalf("expected new variable prompt without stored key choices, got:\n%s", output.String())
	}
}

func TestAskManualVariableKeyFormatsTypedKey(t *testing.T) {
	input := &oneByteReader{reader: strings.NewReader("bad key\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	key, err := runner.askManualVariableKey()
	if err != nil {
		t.Fatal(err)
	}
	if key != "BAD_KEY" {
		t.Fatalf("expected BAD_KEY, got %q", key)
	}
	text := output.String()
	if !strings.Contains(text, "Formatted key:") || !strings.Contains(text, "BAD_KEY") {
		t.Fatalf("expected formatted key message, got:\n%s", text)
	}
}

func TestAskManualVariableKeyRejectsUnusableInput(t *testing.T) {
	input := &oneByteReader{reader: strings.NewReader("!!!\nGOOD_KEY\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	key, err := runner.askManualVariableKey()
	if err != nil {
		t.Fatal(err)
	}
	if key != "GOOD_KEY" {
		t.Fatalf("expected GOOD_KEY, got %q", key)
	}
	if !strings.Contains(output.String(), "Invalid key:") {
		t.Fatalf("expected invalid key message, got:\n%s", output.String())
	}
}

func TestSelectVariableKeyForPushFormatsProvidedManualKey(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push", "--env", "default", "--key", "bad key"}, strings.NewReader(""), &output, &output)

	key, err := runner.selectVariableKeyForPush(repo, "default", "bad key", false)
	if err != nil {
		t.Fatal(err)
	}
	if key != "BAD_KEY" {
		t.Fatalf("expected BAD_KEY, got %q", key)
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

func TestRunVarPushFormatsFileKey(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	envFile := filepath.Join(t.TempDir(), ".env.seed")
	if err := os.WriteFile(envFile, []byte("app-name=Ghostable\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "push", "--env", "default", "--key", "app-name", "--file", envFile, "--json"}, strings.NewReader(""), &output, &output)

	if err := runner.runVarPush(runner.args[3:]); err != nil {
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
		t.Fatalf("expected formatted file key to be stored, got exists=%v variable=%#v", exists, variable)
	}
	if _, exists, err := repo.GetVariable("default", "app-name"); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Fatal("did not expect raw app-name key to be stored")
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

func TestRunVarAnnotationSetListAndRemove(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "annotation", "set", "--env", "default", "--key", "ALPHA", "--name", "Owner", "--string", "platform"}, strings.NewReader(""), &output, &output)
	if err := runner.runVarAnnotation(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "owner=\"platform\"") {
		t.Fatalf("expected set output, got:\n%s", output.String())
	}

	result, err := repo.ReadKeyAnnotations("default", "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Annotations) != 1 || result.Annotations[0].Name != "owner" {
		t.Fatalf("expected owner annotation, got %#v", result.Annotations)
	}

	output.Reset()
	runner = NewRunner([]string{"ghostable", "var", "annotation", "list", "--env", "default", "--key", "ALPHA"}, strings.NewReader(""), &output, &output)
	if err := runner.runVarAnnotation(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "owner=\"platform\"") {
		t.Fatalf("expected list output, got:\n%s", output.String())
	}

	output.Reset()
	runner = NewRunner([]string{"ghostable", "var", "annotation", "remove", "--env", "default", "--key", "ALPHA", "--name", "owner"}, strings.NewReader(""), &output, &output)
	if err := runner.runVarAnnotation(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Removed") || !strings.Contains(output.String(), "owner") {
		t.Fatalf("expected remove output, got:\n%s", output.String())
	}

	result, err = repo.ReadKeyAnnotations("default", "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Annotations) != 0 {
		t.Fatalf("expected annotation to be removed, got %#v", result.Annotations)
	}
}

func TestRunVarAnnotationSetNumberJSON(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "annotation", "set", "--env", "default", "--key", "ALPHA", "--name", "rotation_days", "--number", "90", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.runVarAnnotation(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"rotation_days"`) || !strings.Contains(output.String(), `"number": 90`) {
		t.Fatalf("expected number annotation JSON, got:\n%s", output.String())
	}

	result, err := repo.ReadKeyAnnotations("default", "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Annotations) != 1 || result.Annotations[0].Value.Number == nil || *result.Annotations[0].Value.Number != 90 {
		t.Fatalf("expected stored number annotation, got %#v", result.Annotations)
	}
}

func TestRunVarAnnotationSetBoolJSON(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "annotation", "set", "--env", "default", "--key", "ALPHA", "--name", "deploy_managed", "--bool", "true", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.runVarAnnotation(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"deploy_managed"`) || !strings.Contains(output.String(), `"bool": true`) {
		t.Fatalf("expected bool annotation JSON, got:\n%s", output.String())
	}

	result, err := repo.ReadKeyAnnotations("default", "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Annotations) != 1 || result.Annotations[0].Value.Bool == nil || !*result.Annotations[0].Value.Bool {
		t.Fatalf("expected stored bool annotation, got %#v", result.Annotations)
	}
}

func TestRunVarAnnotationSetBoolFalse(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "annotation", "set", "--env", "default", "--key", "ALPHA", "--name", "deploy_managed", "--bool", "false"}, strings.NewReader(""), &output, &output)
	if err := runner.runVarAnnotation(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "deploy_managed=false") {
		t.Fatalf("expected false bool annotation output, got:\n%s", output.String())
	}

	result, err := repo.ReadKeyAnnotations("default", "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Annotations) != 1 || result.Annotations[0].Value.Bool == nil || *result.Annotations[0].Value.Bool {
		t.Fatalf("expected stored false bool annotation, got %#v", result.Annotations)
	}
}

func TestRunVarContextCanSkipMissingNote(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "GOONIES", "never-say-die", "test"); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("n\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "context", "--env", "default", "--key", "GOONIES"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runVarContext(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "GOONIES has no note. Add one now?") || !strings.Contains(output.String(), "GOONIES has no note.") {
		t.Fatalf("expected add-note prompt and no-note output, got:\n%s", output.String())
	}
	if strings.Contains(output.String(), "Variable note:") {
		t.Fatalf("did not expect note entry prompt when add-note was declined, got:\n%s", output.String())
	}

	variable, exists, err := repo.GetVariable("default", "GOONIES")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Note != "" {
		t.Fatalf("expected missing note to stay empty, got exists=%v variable=%#v", exists, variable)
	}
}

func TestRunVarContextCanAddMissingNote(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "GOONIES", "never-say-die", "test"); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("y\nowned by data team\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "context", "--env", "default", "--key", "GOONIES"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runVarContext(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "GOONIES has no note. Add one now?") ||
		!strings.Contains(output.String(), "Variable note:") ||
		!strings.Contains(output.String(), "GOONIES note: owned by data team") {
		t.Fatalf("expected add-note prompt and saved note output, got:\n%s", output.String())
	}

	variable, exists, err := repo.GetVariable("default", "GOONIES")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Note != "owned by data team" {
		t.Fatalf("expected prompted note to be stored, got exists=%v variable=%#v", exists, variable)
	}
}

func TestRunVarContextCanUpdateExistingNote(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariableNote("default", "ALPHA", "old note"); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("2\nupdated note\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "context", "--env", "default", "--key", "ALPHA"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runVarContext(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Variable note") ||
		!strings.Contains(output.String(), "Update note") ||
		!strings.Contains(output.String(), "ALPHA note: updated note") {
		t.Fatalf("expected update-note choice and output, got:\n%s", output.String())
	}

	variable, exists, err := repo.GetVariable("default", "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Note != "updated note" {
		t.Fatalf("expected note to be updated, got exists=%v variable=%#v", exists, variable)
	}
}

func TestRunVarContextNoteFlagCanClearNote(t *testing.T) {
	root := setupRepoForVarCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "ALPHA", "one", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariableNote("default", "ALPHA", "temporary note"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "var", "context", "--env", "default", "--key", "ALPHA", "--note="}, strings.NewReader(""), &output, &output)

	if err := runner.runVarContext(runner.args[3:]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "ALPHA has no note.") {
		t.Fatalf("expected cleared note output, got:\n%s", output.String())
	}

	variable, exists, err := repo.GetVariable("default", "ALPHA")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Note != "" {
		t.Fatalf("expected note to be cleared, got exists=%v variable=%#v", exists, variable)
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
	if err := repo.SetVariableWithOptions("default", "APP_KEY", "secret", store.VariableWriteOptions{
		Reason:    "test",
		Commented: &commented,
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
	if !exists || variable.Value != "secret" || !variable.Commented {
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
	keyMetadata := readKeyMetadataForTest(t, root, "staging")
	if keyMetadata["ALPHA"].Position == 0 {
		t.Fatalf("expected ALPHA in staging key metadata, got %#v", keyMetadata)
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
