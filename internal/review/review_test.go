package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/store"
)

func TestReviewErrorsWhenReferencedKeyIsMissingFromEnvironment(t *testing.T) {
	root, _ := setupReviewGitRepo(t)
	commitAll(t, root, "baseline")

	writeReviewFile(t, root, "app/client.php", "<?php\n$secret = env('OPENAI_API_KEY');\n")

	report, err := Review(context.Background(), ReviewInput{
		Root:         root,
		BaseRef:      "HEAD",
		Environments: []string{"production"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !hasFinding(report.Errors, "missing_encrypted_value", "OPENAI_API_KEY") {
		t.Fatalf("expected missing encrypted value error, got %#v", report.Errors)
	}
}

func TestReviewDetectsCaseInsensitivePhpEnvAndLowercaseKey(t *testing.T) {
	root, _ := setupReviewGitRepo(t)
	commitAll(t, root, "baseline")

	writeReviewFile(t, root, "test.php", "<?php\n$test = ENV('hammer');\n")

	report, err := Review(context.Background(), ReviewInput{
		Root:         root,
		BaseRef:      "HEAD",
		Environments: []string{"production"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !hasFinding(report.Errors, "missing_encrypted_value", "hammer") {
		t.Fatalf("expected missing encrypted value error for lowercase PHP ENV reference, got %#v", report.Errors)
	}
}

func TestReviewDetectsAdditionalLanguageEnvReferences(t *testing.T) {
	root, _ := setupReviewGitRepo(t)
	commitAll(t, root, "baseline")

	files := map[string]string{
		"settings.py": strings.Join([]string{
			"import os",
			"from os import environ",
			`api_key = os.environ["PYTHON_ENV_SECRET"]`,
			`token = os.environ.get("PYTHON_ENV_TOKEN")`,
			`password = os.getenv("PYTHON_ENV_PASSWORD")`,
			`fallback = environ["PYTHON_IMPORTED_ENV"]`,
			`optional = environ.get("PYTHON_IMPORTED_OPTIONAL")`,
			"",
		}, "\n"),
		"config/initializers/env.rb": strings.Join([]string{
			`secret = ENV["RUBY_ENV_SECRET"]`,
			`token = ENV.fetch("RUBY_ENV_TOKEN")`,
			"",
		}, "\n"),
		"src/main/java/App.java": strings.Join([]string{
			"class App {",
			`  String secret = System.getenv("JAVA_ENV_SECRET");`,
			"}",
			"",
		}, "\n"),
		"Program.cs": strings.Join([]string{
			`var secret = Environment.GetEnvironmentVariable("CSHARP_ENV_SECRET");`,
			`var token = System.Environment.GetEnvironmentVariable("CSHARP_ENV_TOKEN");`,
			"",
		}, "\n"),
		"src/config.rs": strings.Join([]string{
			`let secret = std::env::var("RUST_ENV_SECRET").unwrap();`,
			`let token = env::var_os("RUST_ENV_TOKEN");`,
			"",
		}, "\n"),
		"Sources/App/Config.swift": strings.Join([]string{
			`let secret = ProcessInfo.processInfo.environment["SWIFT_ENV_SECRET"]`,
			"",
		}, "\n"),
	}
	for path, content := range files {
		writeReviewFile(t, root, path, content)
	}

	report, err := Review(context.Background(), ReviewInput{
		Root:         root,
		BaseRef:      "HEAD",
		Environments: []string{"production"},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		"PYTHON_ENV_SECRET",
		"PYTHON_ENV_TOKEN",
		"PYTHON_ENV_PASSWORD",
		"PYTHON_IMPORTED_ENV",
		"PYTHON_IMPORTED_OPTIONAL",
		"RUBY_ENV_SECRET",
		"RUBY_ENV_TOKEN",
		"JAVA_ENV_SECRET",
		"CSHARP_ENV_SECRET",
		"CSHARP_ENV_TOKEN",
		"RUST_ENV_SECRET",
		"RUST_ENV_TOKEN",
		"SWIFT_ENV_SECRET",
	} {
		if !hasFinding(report.Errors, "missing_encrypted_value", key) {
			t.Fatalf("expected missing encrypted value error for %s, got %#v", key, report.Errors)
		}
	}
}

func TestReviewPassesWhenReferenceHasInventorySchemaAndExample(t *testing.T) {
	root, repo := setupReviewGitRepo(t)
	if err := repo.SetVariable("production", "STRIPE_WEBHOOK_SECRET", "whsec_test_secret_value", "test"); err != nil {
		t.Fatal(err)
	}
	writeReviewFile(t, root, ".ghostable/schema.yaml", "STRIPE_WEBHOOK_SECRET:\n  - required\n")
	writeReviewFile(t, root, ".env.example", "STRIPE_WEBHOOK_SECRET=\n")
	commitAll(t, root, "baseline")

	writeReviewFile(t, root, "config/services.php", "<?php\nreturn ['stripe' => env('STRIPE_WEBHOOK_SECRET')];\n")

	report, err := Review(context.Background(), ReviewInput{
		Root:         root,
		BaseRef:      "HEAD",
		Environments: []string{"production"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !report.Passed {
		t.Fatalf("expected review to pass, errors=%#v warnings=%#v", report.Errors, report.Warnings)
	}
	if len(report.References) != 1 || report.References[0].Key != "STRIPE_WEBHOOK_SECRET" {
		t.Fatalf("expected STRIPE_WEBHOOK_SECRET reference, got %#v", report.References)
	}
}

func TestReviewWarnsWhenEncryptedValueChangesWithoutCodeReference(t *testing.T) {
	root, repo := setupReviewGitRepo(t)
	if err := repo.SetVariable("production", "MAIL_PASSWORD", "initial-secret-value", "test"); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "baseline")

	if err := repo.SetVariable("production", "MAIL_PASSWORD", "changed-secret-value", "test"); err != nil {
		t.Fatal(err)
	}

	report, err := Review(context.Background(), ReviewInput{
		Root:         root,
		BaseRef:      "HEAD",
		Environments: []string{"production"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !hasFinding(report.Warnings, "changed_value_without_reference", "MAIL_PASSWORD") {
		t.Fatalf("expected changed value warning, got %#v", report.Warnings)
	}
}

func TestReviewErrorsOnPlaintextEnvSecret(t *testing.T) {
	root, _ := setupReviewGitRepo(t)
	commitAll(t, root, "baseline")

	writeReviewFile(t, root, ".env", "OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz1234567890\n")

	report, err := Review(context.Background(), ReviewInput{
		Root:         root,
		BaseRef:      "HEAD",
		Environments: []string{"production"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !hasFinding(report.Errors, "plaintext_env_secret", "OPENAI_API_KEY") {
		t.Fatalf("expected plaintext env secret error, got %#v", report.Errors)
	}
}

func TestReviewIgnoresGithubWorkflowEnvironmentReferences(t *testing.T) {
	root, _ := setupReviewGitRepo(t)
	commitAll(t, root, "baseline")

	writeReviewFile(t, root, ".github/workflows/deploy.yml", strings.Join([]string{
		"name: deploy",
		"on: push",
		"jobs:",
		"  deploy:",
		"    runs-on: ubuntu-latest",
		"    steps:",
		"      - run: echo \"$HAMMER\"",
		"        env:",
		"          HAMMER: ${{ secrets.HAMMER }}",
		"",
	}, "\n"))

	report, err := Review(context.Background(), ReviewInput{
		Root:         root,
		BaseRef:      "HEAD",
		Environments: []string{"production"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(report.References) != 0 {
		t.Fatalf("expected GitHub workflow references to be ignored, got %#v", report.References)
	}
	if hasFinding(report.Errors, "missing_encrypted_value", "HAMMER") {
		t.Fatalf("did not expect missing Ghostable value error for GitHub secret reference, got %#v", report.Errors)
	}
}

func TestReviewReportsProgressPhases(t *testing.T) {
	root, _ := setupReviewGitRepo(t)
	commitAll(t, root, "baseline")

	var phases []string
	_, err := Review(context.Background(), ReviewInput{
		Root:         root,
		BaseRef:      "HEAD",
		Environments: []string{"production"},
		Status: func(message string) {
			phases = append(phases, message)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, expected := range []string{
		"Opening Ghostable project",
		"Reading git changes",
		"Scanning changed ENV references",
		"Reading encrypted ENV metadata",
		"Checking ENV review rules",
	} {
		if !containsString(phases, expected) {
			t.Fatalf("expected progress phase %q in %#v", expected, phases)
		}
	}
}

func setupReviewGitRepo(t *testing.T) (string, store.Repository) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))
	runGitForTest(t, root, "init")
	runGitForTest(t, root, "config", "user.email", "test@example.com")
	runGitForTest(t, root, "config", "user.name", "Test User")

	repo, _, err := store.Setup(root, store.SetupOptions{
		Name:         "Review Project",
		Environments: []domain.Environment{{Name: "production", Type: "production"}},
		DeviceName:   "test-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	return root, repo
}

func writeReviewFile(t *testing.T, root string, path string, content string) {
	t.Helper()
	absolutePath := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, root string, message string) {
	t.Helper()
	runGitForTest(t, root, "add", ".")
	runGitForTest(t, root, "commit", "-m", message)
}

func runGitForTest(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}

func hasFinding(findings []Finding, code string, key string) bool {
	for _, finding := range findings {
		if finding.Code == code && finding.Key == key {
			return true
		}
	}
	return false
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
