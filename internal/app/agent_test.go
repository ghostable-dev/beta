package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/prompt"
	"github.com/ghostable-dev/beta/internal/store"
)

func TestRunAccessCreateCreatesToken(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "access", "create",
		"--name", "build",
		"--kind", "access",
		"--grant", "default:reader",
		"--json",
	}, strings.NewReader(""), &output, &output)

	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	var result store.AutomationCredentialResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("parse output JSON: %v\n%s", err, output.String())
	}
	if result.Credential.Name != "build" || result.Credential.Kind != "access" {
		t.Fatalf("unexpected credential summary: %#v", result.Credential)
	}
	if !strings.HasPrefix(result.Token, "ghostable_credential_") {
		t.Fatalf("expected credential token prefix, got %q", result.Token)
	}
	if result.EnvironmentVariable != "GHOSTABLE_CI_TOKEN" {
		t.Fatalf("unexpected env var: %s", result.EnvironmentVariable)
	}

	encoded := strings.TrimPrefix(result.Token, "ghostable_credential_")
	tokenBytes, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var token map[string]interface{}
	if err := json.Unmarshal(tokenBytes, &token); err != nil {
		t.Fatal(err)
	}
	if token["kind"] != "access" || token["name"] != "build" {
		t.Fatalf("unexpected token payload: %#v", token)
	}

	repo, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	devices, err := repo.Devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("expected local device plus credential device, got %d", len(devices))
	}
}

func TestRunAgentCredentialCommandIsUnknown(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "agent", "credential", "create"}, strings.NewReader(""), &output, &output)

	err := runner.Run()
	if err == nil {
		t.Fatal("expected agent credential command to be unknown")
	}
	if !strings.Contains(err.Error(), `unknown agent command "credential"`) {
		t.Fatalf("expected agent credential command to be unknown, got %v", err)
	}
}

func TestRunAgentsAlias(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "agent", "capabilities", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "ghostable.agent-capabilities.v1") {
		t.Fatalf("expected agent capabilities output, got:\n%s", output.String())
	}
}

func TestRunAgentCapabilitiesListsSafeCommands(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "agents", "capabilities", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	var capabilities agentCapabilitiesPayload
	if err := json.Unmarshal(output.Bytes(), &capabilities); err != nil {
		t.Fatalf("parse capabilities JSON: %v\n%s", err, output.String())
	}

	for _, expected := range []string{
		"status --json",
		"env list --json",
		"env diff --from <source-env> --to <target-env> --json",
		"env pull --env <env> --file <path> --dry-run --json",
		"validate --env <env> --json",
		"review --base <ref> --env <env> --json",
		"review --secrets-only --json",
		"deploy <env> --dry-run --json",
		"var pull --env <env> --key <key> --json",
		"access status --json",
		"access approvers --env <env> --json",
		"access requests list --json",
		"access matrix --json",
	} {
		if !containsString(capabilities.Commands, expected) {
			t.Fatalf("expected safe agent command %q in capabilities: %#v", expected, capabilities.Commands)
		}
	}

	unsafeFragments := []string{
		"--show-values",
		"env push",
		"env sync",
		"env delete",
		"env rename",
		"var push",
		"var promote",
		"var delete",
		"schema rule",
		"schema file",
		"schema key",
		"access create",
		"access share",
		"access revoke",
		"access delete",
		"access requests approve",
		"access requests deny",
	}
	for _, command := range capabilities.Commands {
		for _, unsafeFragment := range unsafeFragments {
			if strings.Contains(command, unsafeFragment) {
				t.Fatalf("did not expect unsafe agent command %q in capabilities: %#v", command, capabilities.Commands)
			}
		}
	}
}

func TestRunAccessCreateUsesSelectors(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	input := strings.NewReader("build\n3\n1\n1\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "create"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)

	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, "Select credential kind") {
		t.Fatalf("expected credential kind selector, got:\n%s", text)
	}
	if !strings.Contains(text, "Select an environment to grant") {
		t.Fatalf("expected credential grant selector, got:\n%s", text)
	}
	if !strings.Contains(text, "ghostable_credential_") {
		t.Fatalf("expected credential token output, got:\n%s", text)
	}
}

func TestRunDeviceCreateDefaultsToAccessKind(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "device", "create",
		"--name", "access-bot",
		"--grant", "default:reader",
		"--json",
	}, strings.NewReader(""), &output, &output)

	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	var result store.AutomationCredentialResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("parse output JSON: %v\n%s", err, output.String())
	}
	if result.Credential.Kind != "access" {
		t.Fatalf("expected access credential kind, got %q", result.Credential.Kind)
	}
}

func TestRunAccessCreateNormalizesDeployKindToAccess(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "access", "create",
		"--name", "old-deploy-name",
		"--kind", "deploy",
		"--grant", "default:reader",
		"--json",
	}, strings.NewReader(""), &output, &output)

	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	var result store.AutomationCredentialResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("parse output JSON: %v\n%s", err, output.String())
	}
	if result.Credential.Kind != "access" {
		t.Fatalf("expected deploy alias to normalize to access, got %q", result.Credential.Kind)
	}
	if result.Credential.Usage != "deploy" {
		t.Fatalf("expected deploy usage to be preserved, got %q", result.Credential.Usage)
	}
	if len(result.NextSteps) == 0 || !strings.Contains(result.NextSteps[0], "GHOSTABLE_CI_TOKEN") {
		t.Fatalf("expected credential JSON next steps, got %#v", result.NextSteps)
	}
}
