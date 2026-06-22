package app

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/prompt"
	"github.com/ghostable-dev/beta/internal/store"
)

func TestPrintAccessDeviceRowsStripsTerminalControlSequences(t *testing.T) {
	var output bytes.Buffer
	printAccessDeviceRows(&output, []domain.DeviceRecord{{
		ID:        "device-1234567890",
		Name:      "prod\x1b[31m\nspoof",
		Platform:  "darwin\x1b]0;owned\a",
		Status:    "active\x1b[2J",
		CreatedAt: "2026-01-01T00:00:00Z",
	}}, "device-1234567890", false)

	text := stripAppColorCodes(output.String())
	assertNoTerminalControls(t, text)
	if !strings.Contains(text, "prod[31m spoof") {
		t.Fatalf("expected sanitized device name in output, got:\n%s", text)
	}
	if !strings.Contains(text, "darwin]0;owned") {
		t.Fatalf("expected sanitized platform in output, got:\n%s", text)
	}
}

func TestPrintStatusDeviceRowsStripsTerminalControlSequences(t *testing.T) {
	var output bytes.Buffer
	runner := &Runner{out: &output}
	repo := store.Repository{Identity: domain.LocalIdentityRecord{DeviceID: "device-1"}}
	printStatusDeviceRows(runner, repo, []domain.DeviceRecord{{
		ID:       "device-1",
		Name:     "owner\x1b[2J\nspoof",
		Platform: "linux\x1b]2;owned\a",
		Status:   "active\x1b[31m",
	}})

	text := stripAppColorCodes(output.String())
	assertNoTerminalControls(t, text)
	if !strings.Contains(text, "owner[2J spoof") {
		t.Fatalf("expected sanitized device name in output, got:\n%s", text)
	}
	if !strings.Contains(text, "linux]2;owned") {
		t.Fatalf("expected sanitized platform in output, got:\n%s", text)
	}
}

func stripAppColorCodes(value string) string {
	replacer := strings.NewReplacer(
		"\x1b[31m", "",
		"\x1b[32m", "",
		"\x1b[33m", "",
		"\x1b[2m", "",
		"\x1b[0m", "",
	)
	return replacer.Replace(value)
}

func assertNoTerminalControls(t *testing.T, value string) {
	t.Helper()
	for _, r := range value {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			t.Fatalf("expected no terminal control characters in %q", value)
		}
	}
}

func TestRunDeviceGrantsAndRevoke(t *testing.T) {
	setupRepoForEnvCommandTest(t)
	repo, err := store.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateAutomationCredential("ci-device", "ci", []store.AutomationCredentialGrant{{EnvironmentName: "default", Role: "reader"}})
	if err != nil {
		t.Fatal(err)
	}

	var grantsOutput bytes.Buffer
	grantsRunner := NewRunner([]string{"ghostable", "device", "grants", "--env", "default", "--json"}, strings.NewReader(""), &grantsOutput, &grantsOutput)
	if err := grantsRunner.Run(); err != nil {
		t.Fatal(err)
	}
	var grantsPayload struct {
		Grants []store.DeviceGrant `json:"grants"`
	}
	if err := json.Unmarshal(grantsOutput.Bytes(), &grantsPayload); err != nil {
		t.Fatalf("parse grants JSON: %v\n%s", err, grantsOutput.String())
	}
	if !hasGrant(grantsPayload.Grants, created.Credential.DeviceID, "default", "reader") {
		t.Fatalf("expected reader grant for created device, got %#v", grantsPayload.Grants)
	}

	var revokeOutput bytes.Buffer
	revokeRunner := NewRunner([]string{
		"ghostable", "device", "revoke",
		"--device-id", created.Credential.DeviceID,
		"--env", "default",
		"--assume-yes",
		"--json",
	}, strings.NewReader(""), &revokeOutput, &revokeOutput)
	if err := revokeRunner.Run(); err != nil {
		t.Fatal(err)
	}
	var revokeResult store.DeviceRevokeResult
	if err := json.Unmarshal(revokeOutput.Bytes(), &revokeResult); err != nil {
		t.Fatalf("parse revoke JSON: %v\n%s", err, revokeOutput.String())
	}
	if !revokeResult.Revoked || len(revokeResult.Removed) != 1 {
		t.Fatalf("expected one revoked grant, got %#v", revokeResult)
	}

	grants, err := repo.DeviceGrants("default")
	if err != nil {
		t.Fatal(err)
	}
	if hasGrant(grants, created.Credential.DeviceID, "default", "reader") {
		t.Fatalf("expected created device grant to be removed, got %#v", grants)
	}

	var deleteOutput bytes.Buffer
	deleteRunner := NewRunner([]string{
		"ghostable", "device", "delete",
		"--device-id", created.Credential.DeviceID,
		"--assume-yes",
		"--json",
	}, strings.NewReader(""), &deleteOutput, &deleteOutput)
	if err := deleteRunner.Run(); err != nil {
		t.Fatal(err)
	}
	var deleteResult store.DeviceDeleteResult
	if err := json.Unmarshal(deleteOutput.Bytes(), &deleteResult); err != nil {
		t.Fatalf("parse delete JSON: %v\n%s", err, deleteOutput.String())
	}
	if !deleteResult.Deleted || deleteResult.DeviceID != created.Credential.DeviceID {
		t.Fatalf("expected deleted device record, got %#v", deleteResult)
	}
	devices, err := repo.Devices()
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range devices {
		if device.ID == created.Credential.DeviceID {
			t.Fatalf("expected created device record to be deleted, got %#v", devices)
		}
	}
}

func TestRunAccessRemoveIsUnknown(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "remove"}, strings.NewReader(""), &output, &output)

	err := runner.Run()
	if err == nil {
		t.Fatal("expected access remove to be unknown")
	}
	if !strings.Contains(err.Error(), `unknown access command "remove"`) {
		t.Fatalf("expected access remove to be unknown, got %v", err)
	}
}

func TestRunAccessAlias(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "grants", "--env", "default", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\"grants\"") {
		t.Fatalf("expected access grants output, got:\n%s", output.String())
	}
}

func TestRunAccessListPrintsDeviceTable(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "list"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"Device", "Platform", "Status", "Current", "Created", "ID", "test-device"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected access list output to contain %q:\n%s", expected, text)
		}
	}
	for _, expected := range []string{success("test-device"), success("active"), success("yes")} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected colorized access list output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunAccessStatusPrintsDetailTable(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "status"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"Field", "Value", "Device", "Device ID", "Platform", "Status", "Project", "Local key", "test-device", "Environment", "Role", "Permissions", "default", "owner", "read, write, grant, own"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected access status output to contain %q:\n%s", expected, text)
		}
	}
	if !strings.Contains(text, success("test-device")) {
		t.Fatalf("expected colorized device name in access status output:\n%s", text)
	}
}

func TestRunAccessMyStatusIsUnknown(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "my", "status"}, strings.NewReader(""), &output, &output)
	err := runner.Run()
	if err == nil {
		t.Fatal("expected access my status to be unknown")
	}
	if !strings.Contains(err.Error(), `unknown access command "my"`) {
		t.Fatalf("expected access my status to be unknown, got %v", err)
	}
}

func TestAccessHelpShowsStatusCommand(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "--help"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	if !strings.Contains(text, "status") || !strings.Contains(text, "Show this device's access status") {
		t.Fatalf("expected access help to describe status command:\n%s", text)
	}
	if !strings.Contains(text, "approvers") || !strings.Contains(text, "Show devices that can grant access") {
		t.Fatalf("expected access help to describe approvers command:\n%s", text)
	}
	if strings.Contains(text, "my status") {
		t.Fatalf("did not expect access help to describe my status command:\n%s", text)
	}
}

func TestRunAccessStatusJSONIncludesDeviceAndRoles(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "status", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	var payload struct {
		DeviceID string `json:"deviceId"`
		Device   struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"device"`
		Roles            map[string][]string `json:"roles"`
		EnvironmentRoles []struct {
			Environment string   `json:"environment"`
			Role        string   `json:"role"`
			Permissions []string `json:"permissions"`
		} `json:"environmentRoles"`
	}
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("parse access status JSON: %v\n%s", err, output.String())
	}
	if payload.DeviceID == "" || payload.Device.ID != payload.DeviceID || payload.Device.Name != "test-device" {
		t.Fatalf("unexpected status payload device: %#v", payload)
	}
	if roles := payload.Roles["default"]; !containsString(roles, "owner") || !containsString(roles, "writer") {
		t.Fatalf("expected owner roles for default env, got %#v", roles)
	}
	if len(payload.EnvironmentRoles) != 1 || payload.EnvironmentRoles[0].Environment != "default" || payload.EnvironmentRoles[0].Role != "owner" {
		t.Fatalf("expected default owner environment role, got %#v", payload.EnvironmentRoles)
	}
}

func TestRunAccessGrantsPrintsGrantTable(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "grants", "--env", "default"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"Environment", "Role", "Device", "Platform", "Status", "Current", "ID", "default", "owner", "test-device"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected access grants output to contain %q:\n%s", expected, text)
		}
	}
	for _, expected := range []string{success("default"), success("owner"), success("active"), success("yes")} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected colorized access grants output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunAccessApproversPrintsGrantCapableDevices(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "approvers"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"Environment", "Role", "Device", "default", "owner", "test-device"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected access approvers output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunAccessApproversWorksWithoutLocalIdentity(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "missing-keys"))

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "approvers", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	var payload struct {
		Approvers []store.DeviceGrant `json:"approvers"`
	}
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("parse approvers JSON: %v\n%s", err, output.String())
	}
	if !hasGrant(payload.Approvers, "", "default", "owner") {
		t.Fatalf("expected default owner approver, got %#v", payload.Approvers)
	}
}

func TestRunAccessCreatePrintsCredentialTables(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{
		"ghostable", "access", "create",
		"--name", "access-bot",
		"--grant", "default:reader",
	}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"Field", "Value", "Credential", "Kind", "Device ID", "Environment", "Role", "ghostable_credential_"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected access create output to contain %q:\n%s", expected, text)
		}
	}
	for _, expected := range []string{success("access-bot"), success("access"), success("default"), success("reader")} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected colorized access create output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunAccessMatrixPrintsRolesByEnvironment(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "matrix"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"Device", "Platform", "Status", "Current", "ID", "default", "test-device", "owner"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected access matrix output to contain %q:\n%s", expected, text)
		}
	}
	for _, expected := range []string{success("test-device"), success("active"), success("yes"), success("owner")} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected colorized access matrix output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunAccessMatrixJSONIncludesRoles(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "matrix", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	var payload accessMatrixPayload
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("parse matrix JSON: %v\n%s", err, output.String())
	}
	if len(payload.Environments) != 1 || payload.Environments[0] != "default" {
		t.Fatalf("unexpected matrix environments: %#v", payload.Environments)
	}
	if len(payload.Devices) != 1 || payload.Devices[0].Roles["default"] != "owner" {
		t.Fatalf("unexpected matrix devices: %#v", payload.Devices)
	}
}

func TestRunAccessRequestsCreateListAndApprove(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	ownerKeyStore := filepath.Join(root, "keys")
	secondKeyStore := filepath.Join(root, "second-keys")
	t.Setenv("GHOSTABLE_KEYSTORE", secondKeyStore)

	var joinOutput bytes.Buffer
	joinRunner := NewRunner([]string{"ghostable", "access", "join", "--name", "second-device", "--json"}, strings.NewReader(""), &joinOutput, &joinOutput)
	if err := joinRunner.Run(); err != nil {
		t.Fatal(err)
	}
	var joinPayload struct {
		Device struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	if err := json.Unmarshal(joinOutput.Bytes(), &joinPayload); err != nil {
		t.Fatalf("parse join JSON: %v\n%s", err, joinOutput.String())
	}

	var createOutput bytes.Buffer
	createRunner := NewRunner([]string{
		"ghostable", "access", "requests", "create",
		"--env", "default",
		"--role", "reader",
		"--reason", "desktop request",
		"--json",
	}, strings.NewReader(""), &createOutput, &createOutput)
	if err := createRunner.Run(); err != nil {
		t.Fatal(err)
	}
	var createPayload store.AccessRequestResult
	if err := json.Unmarshal(createOutput.Bytes(), &createPayload); err != nil {
		t.Fatalf("parse request create JSON: %v\n%s", err, createOutput.String())
	}
	if createPayload.State != "created" || createPayload.RequestID == "" || createPayload.DeviceID != joinPayload.Device.ID {
		t.Fatalf("unexpected create payload: %#v", createPayload)
	}

	var listOutput bytes.Buffer
	listRunner := NewRunner([]string{"ghostable", "access", "requests", "list", "--json"}, strings.NewReader(""), &listOutput, &listOutput)
	if err := listRunner.Run(); err != nil {
		t.Fatal(err)
	}
	var listPayload store.AccessRequestList
	if err := json.Unmarshal(listOutput.Bytes(), &listPayload); err != nil {
		t.Fatalf("parse request list JSON: %v\n%s", err, listOutput.String())
	}
	if len(listPayload.Valid) != 1 || listPayload.Valid[0].Request.ID != createPayload.RequestID || listPayload.Valid[0].AccessState != "pending" {
		t.Fatalf("expected one pending request, got %#v", listPayload)
	}

	t.Setenv("GHOSTABLE_KEYSTORE", ownerKeyStore)
	var approveOutput bytes.Buffer
	approveRunner := NewRunner([]string{"ghostable", "access", "requests", "approve", createPayload.RequestID, "--json"}, strings.NewReader(""), &approveOutput, &approveOutput)
	if err := approveRunner.Run(); err != nil {
		t.Fatal(err)
	}
	var approvePayload store.AccessRequestReviewResult
	if err := json.Unmarshal(approveOutput.Bytes(), &approvePayload); err != nil {
		t.Fatalf("parse request approve JSON: %v\n%s", err, approveOutput.String())
	}
	if approvePayload.State != "approved" || approvePayload.DeviceID != joinPayload.Device.ID {
		t.Fatalf("unexpected approve payload: %#v", approvePayload)
	}

	repo, err := store.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	grants, err := repo.DeviceGrants("default")
	if err != nil {
		t.Fatal(err)
	}
	if !hasGrant(grants, joinPayload.Device.ID, "default", "reader") {
		t.Fatalf("expected approved reader grant for second device, got %#v", grants)
	}

	var pendingOutput bytes.Buffer
	pendingRunner := NewRunner([]string{"ghostable", "access", "requests", "list", "--json"}, strings.NewReader(""), &pendingOutput, &pendingOutput)
	if err := pendingRunner.Run(); err != nil {
		t.Fatal(err)
	}
	var pendingPayload store.AccessRequestList
	if err := json.Unmarshal(pendingOutput.Bytes(), &pendingPayload); err != nil {
		t.Fatalf("parse pending requests JSON: %v\n%s", err, pendingOutput.String())
	}
	if len(pendingPayload.Valid) != 0 {
		t.Fatalf("expected approved request to be hidden from pending list, got %#v", pendingPayload)
	}
}

func TestRunAccessRequestsListCanSelectAndApprove(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	ownerKeyStore := filepath.Join(root, "keys")
	secondKeyStore := filepath.Join(root, "second-keys")
	t.Setenv("GHOSTABLE_KEYSTORE", secondKeyStore)

	var joinOutput bytes.Buffer
	joinRunner := NewRunner([]string{"ghostable", "access", "join", "--name", "second-device", "--json"}, strings.NewReader(""), &joinOutput, &joinOutput)
	if err := joinRunner.Run(); err != nil {
		t.Fatal(err)
	}
	var joinPayload struct {
		Device struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	if err := json.Unmarshal(joinOutput.Bytes(), &joinPayload); err != nil {
		t.Fatalf("parse join JSON: %v\n%s", err, joinOutput.String())
	}

	var createOutput bytes.Buffer
	createRunner := NewRunner([]string{"ghostable", "access", "requests", "create", "--env", "default", "--role", "reader", "--json"}, strings.NewReader(""), &createOutput, &createOutput)
	if err := createRunner.Run(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GHOSTABLE_KEYSTORE", ownerKeyStore)
	input := strings.NewReader("1\n1\n")
	var output bytes.Buffer
	listRunner := NewRunner([]string{"ghostable", "access", "requests", "list"}, input, &output, &output)
	listRunner.interactive = true
	listRunner.prompts = prompt.New(input, &output)
	if err := listRunner.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Approved access request") {
		t.Fatalf("expected selected request to be approved, got:\n%s", output.String())
	}

	repo, err := store.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	grants, err := repo.DeviceGrants("default")
	if err != nil {
		t.Fatal(err)
	}
	if !hasGrant(grants, joinPayload.Device.ID, "default", "reader") {
		t.Fatalf("expected selected request to grant reader access, got %#v", grants)
	}
}

func TestRunAccessRequestsPromptsForSubcommand(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	input := strings.NewReader("1\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "requests"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "No pending access requests") {
		t.Fatalf("expected interactive requests list output, got:\n%s", output.String())
	}
}

func TestRunAccessRequestsCreatePromptsForValues(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "second-keys"))

	var joinOutput bytes.Buffer
	joinRunner := NewRunner([]string{"ghostable", "access", "join", "--name", "second-device", "--json"}, strings.NewReader(""), &joinOutput, &joinOutput)
	if err := joinRunner.Run(); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("2\n1\nbecause\n")
	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "requests", "create"}, input, &output, &output)
	runner.interactive = true
	runner.prompts = prompt.New(input, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{"Select an environment", "Select requested access", "Reason", "Created access request"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected interactive create output to contain %q:\n%s", expected, text)
		}
	}
}

func TestRunAccessJoinDoesNotReplaceExistingLocalDevice(t *testing.T) {
	setupRepoForEnvCommandTest(t)

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "join", "--name", "replacement"}, strings.NewReader(""), &output, &output)
	err := runner.Run()
	if err == nil {
		t.Fatal("expected join to reject replacing the active local device")
	}
	if !strings.Contains(err.Error(), "already joined") {
		t.Fatalf("expected already joined error, got %v", err)
	}

	repo, openErr := store.Open(".")
	if openErr != nil {
		t.Fatal(openErr)
	}
	devices, devicesErr := repo.Devices()
	if devicesErr != nil {
		t.Fatal(devicesErr)
	}
	if len(devices) != 1 || devices[0].Name != "test-device" {
		t.Fatalf("expected original device to remain active, got %#v", devices)
	}
}

func TestRunAccessJoinCreatesDeviceWhenLocalIdentityIsMissing(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "second-keys"))

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "join", "--name", "second-device", "--json"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	var payload struct {
		Device struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"device"`
		CreatedLocalKey bool `json:"createdLocalKey"`
	}
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("parse join JSON: %v\n%s", err, output.String())
	}
	if payload.Device.ID == "" || payload.Device.Name != "second-device" || !payload.CreatedLocalKey {
		t.Fatalf("unexpected join payload: %#v", payload)
	}

	repo, err := store.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	if repo.DeviceID() != payload.Device.ID {
		t.Fatalf("expected new local identity to be active, got %s want %s", repo.DeviceID(), payload.Device.ID)
	}
}

func TestRunAccessJoinPrintsNextSteps(t *testing.T) {
	root := setupRepoForEnvCommandTest(t)
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "second-keys"))

	var output bytes.Buffer
	runner := NewRunner([]string{"ghostable", "access", "join", "--name", "second-device"}, strings.NewReader(""), &output, &output)
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	text := output.String()
	for _, expected := range []string{
		"Next steps:",
		"Commit the public device record:",
		".ghostable/devices/",
		"Commit the new device.joined event under .ghostable/events/.",
		"ghostable access requests create --env <env> --role reader",
		"ghostable access share --device-id",
		"Devices that can grant access:",
		"test-device",
		"owner",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected access join output to contain %q:\n%s", expected, text)
		}
	}
}

func hasGrant(grants []store.DeviceGrant, deviceID string, env string, role string) bool {
	for _, grant := range grants {
		if (deviceID == "" || grant.DeviceID == deviceID) && grant.Environment == env && grant.Role == role {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
