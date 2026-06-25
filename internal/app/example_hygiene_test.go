package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/prompt"
	"github.com/ghostable-dev/beta/internal/store"
)

func TestRunExampleGenerateDiscoversStoredSchemaAndCodeKeys(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "STORED_SECRET", "secret", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_ENV", "local", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ghostable", "schema.yaml"), []byte("SCHEMA_ONLY:\n  - required\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeAppTestFile(t, root, "config/services.php", "<?php\nreturn ['token' => env('CODE_SECRET')];\n")
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("EXISTING=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "example", "generate"}, strings.NewReader(""), &output, &output)
	if err := runner.runExampleGenerate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{
		"EXISTING=value",
		"APP_ENV=local",
		"APP_NAME=Ghostable",
		"CODE_SECRET=",
		"SCHEMA_ONLY=",
		"STORED_SECRET=",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected generated .env.example to contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "STORED_SECRET=secret") {
		t.Fatalf("did not expect sensitive value in generated .env.example:\n%s", text)
	}
	if !strings.Contains(output.String(), "Generated .env.example with 5 keys.") {
		t.Fatalf("expected generation summary, got:\n%s", output.String())
	}
}

func TestRunExampleGenerateCanForceBlankValues(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "example", "generate", "--values", "blank", "--replace"}, strings.NewReader(""), &output, &output)
	if err := runner.runExampleGenerate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "Ghostable") || !strings.Contains(string(content), "APP_NAME=") {
		t.Fatalf("expected blank example value, got:\n%s", string(content))
	}
}

func TestRunExampleGeneratePromptsToPruneStaleKeys(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("OLD_SECRET=\nAPP_NAME=\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("y\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "example", "generate"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	if err := runner.runExampleGenerate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Contains(text, "OLD_SECRET") {
		t.Fatalf("expected stale example key to be pruned, got:\n%s", text)
	}
	if !strings.Contains(text, "APP_NAME=Ghostable") {
		t.Fatalf("expected discovered key to remain with safe value, got:\n%s", text)
	}
	outputText := output.String()
	for _, expected := range []string{"Stale example keys", "Prune 1 key from .env.example?", "Removed:", "OLD_SECRET"} {
		if !strings.Contains(outputText, expected) {
			t.Fatalf("expected output to contain %q, got:\n%s", expected, outputText)
		}
	}
}

func TestRunExampleGenerateKeepsStaleKeysWhenPruneDeclined(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("OLD_SECRET=\nAPP_NAME=\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("n\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "example", "generate"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	if err := runner.runExampleGenerate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "OLD_SECRET=") {
		t.Fatalf("expected stale example key to be preserved, got:\n%s", text)
	}
	if strings.Contains(output.String(), "Removed: OLD_SECRET") {
		t.Fatalf("did not expect removal summary when pruning was declined, got:\n%s", output.String())
	}
}

func TestRunExampleGeneratePruneFlagRemovesStaleKeys(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_NAME", "Ghostable", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("# Existing comment\nOLD_SECRET=\nAPP_NAME=\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "example", "generate", "--prune"}, strings.NewReader(""), &output, &output)
	if err := runner.runExampleGenerate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Contains(text, "OLD_SECRET") {
		t.Fatalf("expected --prune to remove stale key, got:\n%s", text)
	}
	if !strings.Contains(text, "# Existing comment") || !strings.Contains(text, "APP_NAME=Ghostable") {
		t.Fatalf("expected --prune to preserve comments and discovered keys, got:\n%s", text)
	}
	if strings.Contains(output.String(), "Prune 1 key") {
		t.Fatalf("did not expect prompt when --prune is provided, got:\n%s", output.String())
	}
}

func TestRunEnvPushPromptsToGenerateExampleForNewKeys(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("NEW_SECRET=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("y\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "push", "--env", "default", "--file", ".env"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runEnvPush(runner.args[3:], false); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "NEW_SECRET=") {
		t.Fatalf("expected .env.example to include NEW_SECRET, got:\n%s", string(content))
	}
	if !strings.Contains(output.String(), "Update .env.example now?") {
		t.Fatalf("expected update prompt, got:\n%s", output.String())
	}
}

func TestRunEnvSyncPromptRemovesDeletedExampleKeys(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "OLD_SECRET", "secret", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.example"), []byte("OLD_SECRET=\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("y\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "env", "sync", "--env", "default", "--file", ".env"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.runEnvPush(runner.args[3:], true); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "OLD_SECRET") {
		t.Fatalf("expected .env.example to remove OLD_SECRET, got:\n%s", string(content))
	}
}

func TestRunHygieneReportFindsUnusedAndStaleVariables(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "USED_SECRET", "secret", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "UNUSED_SECRET", "secret", "test"); err != nil {
		t.Fatal(err)
	}
	markVariableUpdatedAt(t, root, "default", "UNUSED_SECRET", "2000-01-01T00:00:00Z")
	writeAppTestFile(t, root, "app/config.php", "<?php\n$value = env('USED_SECRET');\n")

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "hygiene", "report",
		"--env", "default",
		"--stale-after", "1d",
		"--rotation-after", "999999d",
		"--unused",
		"--json",
	}, strings.NewReader(""), &output, &output)
	if err := runner.runHygieneReport(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	var report hygieneReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("parse hygiene JSON: %v\n%s", err, output.String())
	}
	if !hasHygieneFinding(report.Findings, "unused_variable", "UNUSED_SECRET") {
		t.Fatalf("expected unused variable finding, got %#v", report.Findings)
	}
	if !hasHygieneFinding(report.Findings, "stale_variable", "UNUSED_SECRET") {
		t.Fatalf("expected stale variable finding, got %#v", report.Findings)
	}
	if hasHygieneFinding(report.Findings, "unused_variable", "USED_SECRET") {
		t.Fatalf("did not expect USED_SECRET to be unused, got %#v", report.Findings)
	}
}

func TestRunHygieneReportDoesNotMarkOldVariablesStaleByDefault(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_DEBUG", "false", "test"); err != nil {
		t.Fatal(err)
	}
	markVariableUpdatedAt(t, root, "default", "APP_DEBUG", "2000-01-01T00:00:00Z")

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "hygiene", "report",
		"--env", "default",
		"--rotation-after", "999999d",
		"--json",
	}, strings.NewReader(""), &output, &output)
	if err := runner.runHygieneReport(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	var report hygieneReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("parse hygiene JSON: %v\n%s", err, output.String())
	}
	if hasHygieneFinding(report.Findings, "stale_variable", "APP_DEBUG") {
		t.Fatalf("did not expect APP_DEBUG to be stale by default, got %#v", report.Findings)
	}
	if hasHygieneFinding(report.Findings, "rotation_due", "APP_DEBUG") {
		t.Fatalf("did not expect APP_DEBUG to be rotation-due without policy, got %#v", report.Findings)
	}
	if hasHygieneFinding(report.Findings, "unused_variable", "APP_DEBUG") {
		t.Fatalf("did not expect APP_DEBUG to be unused by default, got %#v", report.Findings)
	}
}

func TestRunHygienePromptsForCommandInInteractiveMode(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	input := strings.NewReader("1\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "hygiene"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"Select a hygiene command", "report", "rotation"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected interactive hygiene menu to contain %q, got:\n%s", expected, text)
		}
	}
}

func TestRunHygieneReportFindsRotationDueFromPolicy(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "STRIPE_SECRET_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}
	markVariableUpdatedAt(t, root, "default", "STRIPE_SECRET_KEY", "2000-01-01T00:00:00Z")

	var setOutput bytes.Buffer
	setRunner := NewRunner([]string{
		"ghostable", "hygiene", "rotation", "set",
		"--key", "STRIPE_SECRET_KEY",
		"--days", "90",
		"--json",
	}, strings.NewReader(""), &setOutput, &setOutput)
	if err := setRunner.Run(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "hygiene", "report",
		"--env", "default",
		"--rotation-after", "999999d",
		"--json",
	}, strings.NewReader(""), &output, &output)
	if err := runner.runHygieneReport(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	var report hygieneReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("parse hygiene JSON: %v\n%s", err, output.String())
	}
	if !hasHygieneFinding(report.Findings, "rotation_due", "STRIPE_SECRET_KEY") {
		t.Fatalf("expected rotation_due finding, got %#v", report.Findings)
	}
}

func TestRunHygieneRotationSetWritesEnvironmentOverride(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("production", "production"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "hygiene", "rotation", "set",
		"--env", "production",
		"--key", "STRIPE_SECRET_KEY",
		"--days", "60",
		"--json",
	}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".ghostable", "hygiene.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{
		"rotation:",
		"environments:",
		"production:",
		"STRIPE_SECRET_KEY:",
		"rotationAfterDays: 60",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected hygiene policy to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunHygieneRotationSetRejectsDurationDays(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "hygiene", "rotation", "set",
		"--key", "STRIPE_SECRET_KEY",
		"--days", "90d",
	}, strings.NewReader(""), &output, &output)

	err := runner.Run()
	if err == nil || !strings.Contains(err.Error(), "whole number of days") {
		t.Fatalf("expected whole-number days error, got %v", err)
	}
}

func TestRunHygieneSuppressCreatesSignedSuppression(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "UNUSED_SECRET", "secret", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "hygiene", "suppress",
		"--code", "unused_variable",
		"--env", "default",
		"--key", "UNUSED_SECRET",
		"--reason", "kept for manual operations",
	}, strings.NewReader(""), &output, &output)
	if err := runner.runHygieneSuppress(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	repo, err = store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	report, err := buildHygieneReport(repo, []string{"default"}, 3650*24*time.Hour, 3650*24*time.Hour, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("expected finding to be suppressed, got %#v", report.Findings)
	}
	if !hasHygieneFinding(report.SuppressedFindings, "unused_variable", "UNUSED_SECRET") {
		t.Fatalf("expected suppressed unused variable finding, got %#v", report.SuppressedFindings)
	}
}

func TestRunHygieneSuppressPromptsForCurrentFinding(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "STRIPE_SECRET_KEY", "secret", "test"); err != nil {
		t.Fatal(err)
	}
	markVariableUpdatedAt(t, root, "default", "STRIPE_SECRET_KEY", "2000-01-01T00:00:00Z")
	var setOutput bytes.Buffer
	setRunner := NewRunner([]string{
		"ghostable", "hygiene", "rotation", "set",
		"--key", "STRIPE_SECRET_KEY",
		"--days", "90",
		"--json",
	}, strings.NewReader(""), &setOutput, &setOutput)
	if err := setRunner.Run(); err != nil {
		t.Fatal(err)
	}

	input := &oneByteReader{reader: strings.NewReader("1\nkept for manual operations\nn\n")}
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "hygiene", "suppress"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	repo, err = store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	report, err := buildHygieneReport(repo, []string{"default"}, 0, 3650*24*time.Hour, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("expected finding to be suppressed, got %#v", report.Findings)
	}
	if !strings.Contains(output.String(), "Select finding to suppress") {
		t.Fatalf("expected interactive finding picker, got:\n%s", output.String())
	}
}

func TestRunHygieneReportPrintsSARIF(t *testing.T) {
	setupRepoForEnvCommandTest(t)
	repo, err := store.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "UNUSED_SECRET", "secret", "test"); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "hygiene", "report",
		"--env", "default",
		"--stale-after", "999999d",
		"--rotation-after", "999999d",
		"--unused",
		"--sarif",
	}, strings.NewReader(""), &output, &output)
	if err := runner.runHygieneReport(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{`"version": "2.1.0"`, `"ruleId": "unused_variable"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected SARIF output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunHygieneRotateChangesEnvironmentKeyAndKeepsValuesReadable(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariable("default", "APP_KEY", "base64:secret", "test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetVariableNote("default", "APP_KEY", "rotate after launch"); err != nil {
		t.Fatal(err)
	}
	before, err := repo.ReadEnvironmentKeyMetadata("default")
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "hygiene", "rotate", "--env", "default", "--reason", "test", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.runHygieneRotate(runner.args[3:]); err != nil {
		t.Fatal(err)
	}

	repo, err = store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	after, err := repo.ReadEnvironmentKeyMetadata("default")
	if err != nil {
		t.Fatal(err)
	}
	if before.Fingerprint == after.Fingerprint {
		t.Fatalf("expected key fingerprint to change")
	}
	variable, exists, err := repo.GetVariable("default", "APP_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || variable.Value != "base64:secret" || variable.Note != "rotate after launch" {
		t.Fatalf("expected value to remain readable, got exists=%v variable=%#v", exists, variable)
	}
}

func writeAppTestFile(t *testing.T, root string, path string, content string) {
	t.Helper()
	absolutePath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func markVariableUpdatedAt(t *testing.T, root string, env string, key string, updatedAt string) {
	t.Helper()
	valueFiles, err := filepath.Glob(filepath.Join(root, ".ghostable", "environments", env, "values", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range valueFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var record domain.ValueRecord
		if err := json.Unmarshal(content, &record); err != nil {
			t.Fatal(err)
		}
		if record.Key != key {
			continue
		}
		record.UpdatedAt = updatedAt
		next, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		next = append(next, '\n')
		if err := os.WriteFile(file, next, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("variable %s was not found in %s", key, env)
}

func hasHygieneFinding(findings []hygieneFinding, code string, key string) bool {
	for _, finding := range findings {
		if finding.Code == code && finding.Key == key {
			return true
		}
	}
	return false
}
