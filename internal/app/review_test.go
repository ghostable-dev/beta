package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/store"
)

func TestRunReviewHelp(t *testing.T) {
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "review", "--help"}, strings.NewReader(""), &output, &output)

	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, "Usage: ghostable review [paths...] [options]") ||
		!strings.Contains(text, "--env-only") ||
		!strings.Contains(text, "--secrets-only") ||
		!strings.Contains(text, "--format <FORMAT>") {
		t.Fatalf("expected review help output, got:\n%s", text)
	}
}

func TestRunReviewDefaultsToEnvAndSecretChecks(t *testing.T) {
	root := setupReviewCommandRepo(t)
	commitAllForReviewCommandTest(t, root, "baseline")

	writeReviewCommandFile(t, root, "app/client.php", "<?php\n$secret = env('OPENAI_API_KEY');\n")
	writeReviewCommandFile(t, root, "config.js", "console.log('sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789');\n")

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "review"}, strings.NewReader(""), &output, &output)
	err := runner.Run()
	if err == nil {
		t.Fatal("expected review to fail")
	}
	if !strings.Contains(err.Error(), "1 error") || !strings.Contains(err.Error(), "1 possible secret") {
		t.Fatalf("expected env and secret failure, got %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "missing from production") {
		t.Fatalf("expected ENV review output, got:\n%s", text)
	}
	if !strings.Contains(text, "Hard-coded secret scan") || !strings.Contains(text, "OpenAI API key") {
		t.Fatalf("expected secret scan output, got:\n%s", text)
	}
}

func TestRunReviewEnvOnlySkipsSecretScan(t *testing.T) {
	root := setupReviewCommandRepo(t)
	commitAllForReviewCommandTest(t, root, "baseline")
	writeReviewCommandFile(t, root, "config.js", "console.log('sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789');\n")

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "review", "--base", "HEAD", "--env-only"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(output.String(), "Hard-coded secret scan") {
		t.Fatalf("expected env-only review to skip secret scan, got:\n%s", output.String())
	}
}

func TestRunReviewSuppressCreatesSecretScanSuppression(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	writeReviewCommandFile(t, root, "config.js", "console.log('sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789');\n")

	var suppressOutput bytes.Buffer
	suppressRunner := NewRunner([]string{
		"ghostable", "review", "suppress",
		"--secrets-only",
		"--path", "config.js",
		"--line", "1",
		"--kind", "OpenAI API key",
		"--reason", "fixture secret is intentionally present",
	}, strings.NewReader(""), &suppressOutput, &suppressOutput)
	if err := suppressRunner.Run(); err != nil {
		t.Fatal(err)
	}
	writeReviewCommandFile(t, root, "config.js", "// moved down\nconsole.log('sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789');\n")

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "review", "--secrets-only", "config.js"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, "No hard-coded secrets found") {
		t.Fatalf("expected secret scan to pass after suppression, got:\n%s", text)
	}
	if !strings.Contains(text, "Suppressed:") {
		t.Fatalf("expected suppressed finding count, got:\n%s", text)
	}
}

func TestRunReviewSuppressesOnlySelectedDuplicateSecretFinding(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	writeReviewCommandFile(t, root, "config.js", strings.Join([]string{
		"console.log('sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789');",
		"console.log('sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789');",
		"",
	}, "\n"))

	var suppressOutput bytes.Buffer
	suppressRunner := NewRunner([]string{
		"ghostable", "review", "suppress",
		"--secrets-only",
		"--path", "config.js",
		"--line", "1",
		"--kind", "OpenAI API key",
	}, strings.NewReader(""), &suppressOutput, &suppressOutput)
	if err := suppressRunner.Run(); err != nil {
		t.Fatal(err)
	}

	writeReviewCommandFile(t, root, "config.js", strings.Join([]string{
		"// moved down",
		"console.log('sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789');",
		"console.log('sk-proj-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789');",
		"",
	}, "\n"))

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "review", "--secrets-only", "config.js"}, strings.NewReader(""), &output, &output)
	err := runner.Run()
	if err == nil {
		t.Fatalf("expected review to still report the unsuppressed duplicate, got nil error:\n%s", output.String())
	}

	text := output.String()
	if !strings.Contains(text, "config.js:3:") || !strings.Contains(text, "OpenAI API key") {
		t.Fatalf("expected second duplicate to remain unsuppressed, got:\n%s", text)
	}
	if !strings.Contains(text, "Suppressed:") || !strings.Contains(text, "1 secret finding suppressed.") {
		t.Fatalf("expected only one duplicate to be suppressed, got:\n%s", text)
	}
}

func TestReviewSuppressionPromptTextIsCompact(t *testing.T) {
	finding := reviewSuppressionFinding{
		Source:      suppressionSourceReview,
		Code:        "missing_encrypted_value",
		Environment: "default",
		Key:         "OPENAI_API_KEY",
		Path:        "internal/app/review_test.go",
		Line:        35,
		Message:     strings.Repeat("OPENAI_API_KEY is referenced in internal/app/review_test.go:35 but missing from default ", 3),
	}

	label := reviewFindingSuppressionLabel(finding)
	description := reviewFindingSuppressionDescription(finding)

	if len(label) > 30 {
		t.Fatalf("expected compact label, got %q", label)
	}
	if len(description) > 34 {
		t.Fatalf("expected compact description, got %q", description)
	}
	if strings.Contains(label, finding.Message) || strings.Contains(description, finding.Message) {
		t.Fatalf("expected prompt text to truncate long message, label=%q description=%q", label, description)
	}
}

func TestRunReviewAppliesSignedSuppression(t *testing.T) {
	root := setupReviewCommandRepo(t)
	commitAllForReviewCommandTest(t, root, "baseline")
	writeReviewCommandFile(t, root, "app/client.php", "<?php\n$secret = env('OPENAI_API_KEY');\n")

	var suppressOutput bytes.Buffer
	suppressRunner := NewRunner([]string{
		"ghostable", "review", "suppress",
		"--base", "HEAD",
		"--code", "missing_encrypted_value",
		"--env", "production",
		"--key", "OPENAI_API_KEY",
		"--path", "app/client.php",
		"--line", "2",
		"--reason", "tracked outside Ghostable for this fixture",
	}, strings.NewReader(""), &suppressOutput, &suppressOutput)
	if err := suppressRunner.Run(); err != nil {
		t.Fatal(err)
	}
	writeReviewCommandFile(t, root, "app/client.php", "<?php\n// moved down\n$secret = env('OPENAI_API_KEY');\n")

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "review", "--base", "HEAD", "--env-only"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if strings.Contains(text, "missing from production") {
		t.Fatalf("expected missing encrypted value to be suppressed, got:\n%s", text)
	}
	if !strings.Contains(text, "Suppressed:") {
		t.Fatalf("expected suppressed finding count, got:\n%s", text)
	}
}

func setupReviewCommandRepo(t *testing.T) string {
	t.Helper()
	root := setupTempWorkdir(t)
	runGitForReviewCommandTest(t, root, "init")
	runGitForReviewCommandTest(t, root, "config", "user.email", "test@example.com")
	runGitForReviewCommandTest(t, root, "config", "user.name", "Test User")
	_, _, err := store.Setup(".", store.SetupOptions{
		Name:         "Review Project",
		Environments: []domain.Environment{{Name: "production", Type: "production"}},
		DeviceName:   "test-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func writeReviewCommandFile(t *testing.T, root string, path string, content string) {
	t.Helper()
	absolutePath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commitAllForReviewCommandTest(t *testing.T, root string, message string) {
	t.Helper()
	runGitForReviewCommandTest(t, root, "add", ".")
	runGitForReviewCommandTest(t, root, "commit", "-m", message)
}

func runGitForReviewCommandTest(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
