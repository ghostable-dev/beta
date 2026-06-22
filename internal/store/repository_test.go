package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/dotenv"
	"github.com/ghostable-dev/beta/internal/security"
)

func TestRepositoryEncryptsAndPullsVariables(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "test-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.PutVariables("local", map[string]string{
		"APP_KEY": "super-secret-value",
	}, PutOptions{Reason: "test"})
	if err != nil {
		t.Fatal(err)
	}

	valueFiles, err := filepath.Glob(filepath.Join(root, ".ghostable", "environments", "local", "values", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(valueFiles) != 1 {
		t.Fatalf("expected one encrypted value file, got %d", len(valueFiles))
	}
	content, err := os.ReadFile(valueFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "super-secret-value") {
		t.Fatal("plaintext secret leaked into encrypted value file")
	}
	var record domain.ValueRecord
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatal(err)
	}
	if record.Schema != domain.ValueSchema {
		t.Fatalf("expected TS-compatible value schema, got %s", record.Schema)
	}
	if record.Secret.ClientSig == "" {
		t.Fatal("expected signed value payload")
	}
	if record.Secret.Alg != "xchacha20-poly1305" {
		t.Fatalf("expected xchacha20-poly1305, got %s", record.Secret.Alg)
	}

	grantFiles, err := filepath.Glob(filepath.Join(root, ".ghostable", "environments", "local", "access", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(grantFiles) != 1 {
		t.Fatalf("expected one access grant, got %d", len(grantFiles))
	}
	var grant domain.AccessGrantRecord
	grantContent, err := os.ReadFile(grantFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(grantContent, &grant); err != nil {
		t.Fatal(err)
	}
	if grant.ClientSig == "" || grant.Envelope.SignatureB64 == "" {
		t.Fatal("expected signed grant and signed envelope")
	}

	variables, err := repo.ReadVariables("local")
	if err != nil {
		t.Fatal(err)
	}
	if variables["APP_KEY"].Value != "super-secret-value" {
		t.Fatalf("unexpected decrypted value: %q", variables["APP_KEY"].Value)
	}

	result, _, err := repo.Pull("local", PullOptions{File: ".env", Backup: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Written != 1 {
		t.Fatalf("expected one written variable, got %d", result.Written)
	}

	envFile, err := os.Open(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	defer envFile.Close()

	parsed, err := dotenv.Parse(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Entries["APP_KEY"].Value != "super-secret-value" {
		t.Fatalf("unexpected pulled value: %q", parsed.Entries["APP_KEY"].Value)
	}
}

func TestRepositoryRejectsTamperedValueSignature(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "test-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutVariables("local", map[string]string{"APP_KEY": "super-secret-value"}, PutOptions{Reason: "test"}); err != nil {
		t.Fatal(err)
	}

	valueFiles, err := filepath.Glob(filepath.Join(root, ".ghostable", "environments", "local", "values", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(valueFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	var record domain.ValueRecord
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatal(err)
	}
	record.Secret.Name = "OTHER_KEY"
	tampered, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(valueFiles[0], append(tampered, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.ReadVariables("local"); err == nil {
		t.Fatal("expected tampered value signature to be rejected")
	}
}

func TestRepositoryReadsVariableMetadataWithoutDecrypting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "test-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutVariables("local", map[string]string{"APP_KEY": "super-secret-value"}, PutOptions{Reason: "test"}); err != nil {
		t.Fatal(err)
	}

	projectRepo, err := OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := projectRepo.ReadVariableMetadata("local")
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 1 {
		t.Fatalf("expected one metadata entry, got %#v", metadata)
	}
	if metadata[0].Key != "APP_KEY" || metadata[0].Environment != "local" {
		t.Fatalf("unexpected metadata entry: %#v", metadata[0])
	}
	if !metadata[0].ValidSignature {
		t.Fatalf("expected valid metadata signature: %#v", metadata[0])
	}
	if metadata[0].Version != 1 || metadata[0].UpdatedByDeviceID == "" {
		t.Fatalf("expected version and updater metadata: %#v", metadata[0])
	}
}

func TestRepositoryReportsTamperedVariableMetadataSignature(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "test-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutVariables("local", map[string]string{"APP_KEY": "super-secret-value"}, PutOptions{Reason: "test"}); err != nil {
		t.Fatal(err)
	}

	valueFiles, err := filepath.Glob(filepath.Join(root, ".ghostable", "environments", "local", "values", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(valueFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	var record domain.ValueRecord
	if err := json.Unmarshal(content, &record); err != nil {
		t.Fatal(err)
	}
	record.Secret.Name = "OTHER_KEY"
	tampered, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(valueFiles[0], append(tampered, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	projectRepo, err := OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := projectRepo.ReadVariableMetadata("local")
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 1 {
		t.Fatalf("expected one metadata entry, got %#v", metadata)
	}
	if metadata[0].ValidSignature {
		t.Fatalf("expected metadata signature to be invalid: %#v", metadata[0])
	}
	if !strings.Contains(metadata[0].SignatureError, "not bound") {
		t.Fatalf("expected binding error, got %#v", metadata[0])
	}
}

func TestRepositoryRejectsUnsignedPolicy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	policy := readPolicyForTest(t, root)
	policy.DeviceID = ""
	policy.ClientSig = ""
	writePolicyForTest(t, root, policy)

	if _, err := repo.readPolicy(); err == nil || !strings.Contains(err.Error(), "policy is not signed") {
		t.Fatalf("expected unsigned policy to be rejected, got %v", err)
	}
}

func TestRepositoryRejectsPolicySignedByUntrustedOwner(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	attackerIdentity, attackerDevice, err := security.NewDeviceIdentity(repo.Manifest.ID, "attacker-device", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.writeDevice(attackerDevice); err != nil {
		t.Fatal(err)
	}

	policy := readPolicyForTest(t, root)
	policy.Owners = appendUnique(policy.Owners, attackerDevice.ID)
	policy.DeviceID = attackerDevice.ID
	policy.ClientSig = ""
	signature, err := security.SignCanonical(policy, attackerIdentity)
	if err != nil {
		t.Fatal(err)
	}
	policy.ClientSig = signature
	writePolicyForTest(t, root, policy)

	if _, err := repo.readPolicy(); err == nil || !strings.Contains(err.Error(), "is not trusted") {
		t.Fatalf("expected untrusted policy signer to be rejected, got %v", err)
	}
}

func TestRepositoryRejectsEnvironmentStoragePathCollision(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("Production", "production"); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.CreateEnvironment("production", "production"); err == nil || !strings.Contains(err.Error(), "conflicts with existing environment") {
		t.Fatalf("expected storage path collision to be rejected, got %v", err)
	}
}

func TestPullRejectsSymlinkedDotenvOutput(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutVariables("local", map[string]string{"APP_KEY": "super-secret-value"}, PutOptions{Reason: "test"}); err != nil {
		t.Fatal(err)
	}

	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.env")
	if err := os.WriteFile(outsideFile, []byte("EXISTING=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, ".env")); err != nil {
		t.Skipf("symlink creation is not available: %v", err)
	}

	if _, _, err := repo.Pull("local", PullOptions{File: ".env"}); err == nil || !strings.Contains(err.Error(), "refusing to write through symlink") {
		t.Fatalf("expected symlinked dotenv output to be rejected, got %v", err)
	}

	content, err := os.ReadFile(outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "super-secret-value") {
		t.Fatal("secret was written through symlinked dotenv output")
	}
}

func TestPullRejectsDotenvOutputOutsideProject(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutVariables("local", map[string]string{"APP_KEY": "super-secret-value"}, PutOptions{Reason: "test"}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := repo.Pull("local", PullOptions{File: "../outside.env"}); err == nil || !strings.Contains(err.Error(), "must stay inside the project") {
		t.Fatalf("expected outside dotenv output path to be rejected, got %v", err)
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "outside.env")); !os.IsNotExist(err) {
		t.Fatalf("outside dotenv output should not be written, stat err: %v", err)
	}
}

func TestSetupRejectsSymlinkedGhostableStateRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".ghostable")); err != nil {
		t.Skipf("symlink creation is not available: %v", err)
	}

	if _, _, err := Setup(root, SetupOptions{Name: "Test Project"}); err == nil || !strings.Contains(err.Error(), "symlinked Ghostable state path") {
		t.Fatalf("expected symlinked state root to be rejected, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "ghostable.yaml")); !os.IsNotExist(err) {
		t.Fatalf("setup should not write manifest through symlinked state root, stat err: %v", err)
	}
}

func TestRepositoryRejectsSymlinkedValuesStateDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	valuesDir := repo.valuesDir("local")
	outside := t.TempDir()
	if err := os.RemoveAll(valuesDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, valuesDir); err != nil {
		t.Skipf("symlink creation is not available: %v", err)
	}

	if _, err := repo.PutVariables("local", map[string]string{"APP_KEY": "super-secret-value"}, PutOptions{Reason: "test"}); err == nil || !strings.Contains(err.Error(), "symlinked Ghostable state path") {
		t.Fatalf("expected symlinked values state directory to be rejected, got %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("state write escaped into symlink target: %#v", entries)
	}
}

func TestRepositoryRejectsDeviceRecordFromMismatchedFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, attackerDevice, err := security.NewDeviceIdentity(repo.Manifest.ID, "attacker-device", "test")
	if err != nil {
		t.Fatal(err)
	}
	ownerDevicePath := filepath.Join(root, ".ghostable", "devices", idFileName(repo.DeviceID()))
	if err := writeJSONAtomic(ownerDevicePath, attackerDevice, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.readDevice(repo.DeviceID()); err == nil || !strings.Contains(err.Error(), "does not match requested device") {
		t.Fatalf("expected mismatched device file to be rejected, got %v", err)
	}
}

func TestRepositoryRejectsInactiveDeviceRecord(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	device, err := repo.readDevice(repo.DeviceID())
	if err != nil {
		t.Fatal(err)
	}
	device.Status = "revoked"
	if err := security.SignDeviceRecord(&device, repo.Identity); err != nil {
		t.Fatal(err)
	}
	if err := repo.writeDevice(device); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.readDevice(repo.DeviceID()); err == nil || !strings.Contains(err.Error(), "is not active") {
		t.Fatalf("expected inactive device record to be rejected, got %v", err)
	}
}

func TestOpenRejectsManifestEnvironmentStoragePathCollision(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	repo.Manifest.Environments["LOCAL"] = domain.Environment{Name: "LOCAL", Type: "local"}
	if err := writeManifest(repo.ManifestPath, repo.Manifest); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(root); err == nil || !strings.Contains(err.Error(), "conflicts with existing environment") {
		t.Fatalf("expected colliding manifest environments to be rejected, got %v", err)
	}
}

func TestRepositoryRejectsDotSegmentEnvironmentName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := repo.CreateEnvironment(".", "local"); err == nil || !strings.Contains(err.Error(), "unsafe path segment") {
		t.Fatalf("expected dot-segment environment name to be rejected, got %v", err)
	}
}

func TestOpenRejectsDotSegmentEnvironmentName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	repo.Manifest.Environments[".."] = domain.Environment{Name: "..", Type: "local"}
	if err := writeManifest(repo.ManifestPath, repo.Manifest); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(root); err == nil || !strings.Contains(err.Error(), "unsafe path segment") {
		t.Fatalf("expected manifest dot-segment environment name to be rejected, got %v", err)
	}
}

func TestRepositoryRejectsStaleAccessGrantAfterEnvironmentKeyRotation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateAutomationCredential("ci-device", "ci", []AutomationCredentialGrant{{EnvironmentName: "local", Role: "writer"}})
	if err != nil {
		t.Fatal(err)
	}
	ownerGrantPath := filepath.Join(root, ".ghostable", "environments", "local", "access", idFileName(repo.DeviceID()))
	oldOwnerGrant, err := os.ReadFile(ownerGrantPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := repo.RevokeDevice(created.Credential.DeviceID, "local"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownerGrantPath, oldOwnerGrant, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.readAccessGrant("local", repo.DeviceID()); err == nil || !strings.Contains(err.Error(), "does not match current environment key") {
		t.Fatalf("expected stale access grant to be rejected, got %v", err)
	}
}

func TestRepositoryRejectsRolledBackPolicyVersion(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	oldPolicy := readPolicyForTest(t, root)
	newerPolicy := oldPolicy
	newerPolicy.Version = oldPolicy.Version + 1
	newerPolicy.UpdatedAt = security.Now()
	if err := repo.signAndWritePolicy(newerPolicy); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.readPolicy(); err != nil {
		t.Fatal(err)
	}
	writePolicyForTest(t, root, oldPolicy)

	if _, err := repo.readPolicy(); err == nil || !strings.Contains(err.Error(), "older than trusted local policy version") {
		t.Fatalf("expected rolled back policy version to be rejected, got %v", err)
	}
}

func TestReaderDeviceCannotWriteVariables(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	ownerRepo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ownerRepo.PutVariables("local", map[string]string{"APP_KEY": "owner-value"}, PutOptions{Reason: "owner"}); err != nil {
		t.Fatal(err)
	}

	readerDevice, _, err := ownerRepo.JoinDevice("reader-device", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := ownerRepo.ShareDevice(readerDevice.ID, "local", "reader"); err != nil {
		t.Fatal(err)
	}

	readerRepo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	values, err := readerRepo.ReadVariables("local")
	if err != nil {
		t.Fatal(err)
	}
	if values["APP_KEY"].Value != "owner-value" {
		t.Fatalf("reader should decrypt shared value, got %q", values["APP_KEY"].Value)
	}

	if _, err := readerRepo.PutVariables("local", map[string]string{"APP_KEY": "reader-value"}, PutOptions{Reason: "reader"}); err == nil {
		t.Fatal("expected reader device write to be rejected")
	}
}

func TestRepositoryRejectsAccessRequestReviewByUnauthorizedDevice(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerIdentity := repo.Identity

	if _, _, err := repo.JoinDevice("requesting-device", "test"); err != nil {
		t.Fatal(err)
	}
	requesterRepo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	requestResult, err := requesterRepo.CreateAccessRequest("local", "reader", "needs access")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := repo.JoinDevice("unauthorized-reviewer", "test"); err != nil {
		t.Fatal(err)
	}
	reviewerRepo, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	reviewerIdentity := reviewerRepo.Identity

	record, err := repo.readAccessRequestFile(requestResult.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	review := domain.AccessRequestReview{
		Schema:             domain.AccessRequestSchema,
		ProjectID:          repo.Manifest.ID,
		RequestID:          record.Request.ID,
		Status:             "approved",
		ReviewedByDeviceID: reviewerIdentity.DeviceID,
		ReviewedAt:         security.Now(),
		SignerDeviceID:     reviewerIdentity.DeviceID,
	}
	signature, err := security.SignCanonical(review, reviewerIdentity)
	if err != nil {
		t.Fatal(err)
	}
	review.ClientSig = signature
	record.Review = &review
	if err := repo.writeAccessRequestFile(record); err != nil {
		t.Fatal(err)
	}

	repo.Identity = ownerIdentity
	requests, err := repo.ListAccessRequests(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests.Valid) != 0 {
		t.Fatalf("unauthorized review should not be accepted as valid, got %#v", requests.Valid)
	}
	if len(requests.Invalid) != 1 || !strings.Contains(requests.Invalid[0].Error, "not authorized to review access requests") {
		t.Fatalf("expected unauthorized review to be invalid, got %#v", requests.Invalid)
	}
}

func TestRepositoryRejectsUnsignedHistoryEvent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	event := domain.Event{
		Schema:      domain.EventSchema,
		Action:      "variable.deleted",
		ProjectID:   repo.Manifest.ID,
		Environment: "local",
		Key:         "APP_KEY",
		DeviceID:    repo.DeviceID(),
		OccurredAt:  security.Now(),
	}
	path := filepath.Join(root, ".ghostable", "events", "unsigned.json")
	if err := writeJSONAtomic(path, event, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.History("local", "", "", 0); err == nil || !strings.Contains(err.Error(), "missing a signature") {
		t.Fatalf("expected unsigned history event to be rejected, got %v", err)
	}
}

func TestRepositoryDeviceGrantsAndRevoke(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := repo.CreateAutomationCredential("ci-device", "ci", []AutomationCredentialGrant{{EnvironmentName: "local", Role: "writer"}})
	if err != nil {
		t.Fatal(err)
	}

	grants, err := repo.DeviceGrants("local")
	if err != nil {
		t.Fatal(err)
	}
	if roleForGrant(grants, created.Credential.DeviceID, "local") != "writer" {
		t.Fatalf("expected writer grant for credential, got %#v", grants)
	}

	grantFiles, err := filepath.Glob(filepath.Join(root, ".ghostable", "environments", "local", "access", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(grantFiles) != 2 {
		t.Fatalf("expected owner and credential access grants, got %d", len(grantFiles))
	}
	if _, err := repo.DeleteDevice(created.Credential.DeviceID); err == nil {
		t.Fatal("expected active credential device delete to be rejected")
	}

	result, err := repo.RevokeDevice(created.Credential.DeviceID, "local")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Revoked || len(result.Removed) != 1 {
		t.Fatalf("expected one revoked grant, got %#v", result)
	}

	grants, err = repo.DeviceGrants("local")
	if err != nil {
		t.Fatal(err)
	}
	if roleForGrant(grants, created.Credential.DeviceID, "local") != "" {
		t.Fatalf("expected credential grant to be removed, got %#v", grants)
	}

	grantFiles, err = filepath.Glob(filepath.Join(root, ".ghostable", "environments", "local", "access", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(grantFiles) != 1 {
		t.Fatalf("expected only owner access grant after revoke, got %d", len(grantFiles))
	}

	deleteResult, err := repo.DeleteDevice(created.Credential.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if !deleteResult.Deleted || deleteResult.DeviceID != created.Credential.DeviceID {
		t.Fatalf("unexpected delete result: %#v", deleteResult)
	}
	devices, err := repo.Devices()
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range devices {
		if device.ID == created.Credential.DeviceID {
			t.Fatalf("expected credential device record to be deleted, got %#v", devices)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".ghostable", "devices", idFileName(created.Credential.DeviceID))); !os.IsNotExist(err) {
		t.Fatalf("expected credential device file to be deleted, stat err: %v", err)
	}
}

func TestRepositoryRotatesEnvironmentKeyWhenDeviceRevoked(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutVariables("local", map[string]string{"APP_KEY": "owner-value"}, PutOptions{Reason: "owner"}); err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateAutomationCredential("ci-device", "ci", []AutomationCredentialGrant{{EnvironmentName: "local", Role: "writer"}})
	if err != nil {
		t.Fatal(err)
	}

	beforeKey, err := repo.readEnvironmentKey("local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RevokeDevice(created.Credential.DeviceID, "local"); err != nil {
		t.Fatal(err)
	}
	afterKey, err := repo.readEnvironmentKey("local")
	if err != nil {
		t.Fatal(err)
	}

	if afterKey.Fingerprint == beforeKey.Fingerprint {
		t.Fatal("expected revocation to rotate the environment key fingerprint")
	}
	if afterKey.Version <= beforeKey.Version {
		t.Fatalf("expected key version to increase from %d, got %d", beforeKey.Version, afterKey.Version)
	}

	values, err := repo.ReadVariables("local")
	if err != nil {
		t.Fatal(err)
	}
	if values["APP_KEY"].Value != "owner-value" {
		t.Fatalf("expected owner to decrypt value after rotation, got %q", values["APP_KEY"].Value)
	}
}

func TestRepositoryRejectsDuplicateValueRecords(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutVariables("local", map[string]string{"APP_KEY": "old-value"}, PutOptions{Reason: "old"}); err != nil {
		t.Fatal(err)
	}
	oldRecord, err := os.ReadFile(repo.valuePath("local", "APP_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutVariables("local", map[string]string{"APP_KEY": "current-value"}, PutOptions{Reason: "current"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.valuesDir("local"), "zzzz-replayed-app-key.json"), oldRecord, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.ReadVariables("local"); err == nil || !strings.Contains(err.Error(), "duplicate value record") {
		t.Fatalf("expected duplicate value record to be rejected, got %v", err)
	}
}

func TestRepositoryRejectsValueRecordFromDifferentEnvironmentDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GHOSTABLE_KEYSTORE", filepath.Join(root, "keys"))

	repo, _, err := Setup(root, SetupOptions{
		Name:         "Test Project",
		Environments: []domain.Environment{{Name: "local", Type: "local"}},
		DeviceName:   "owner-device",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEnvironment("production", "production"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PutVariables("production", map[string]string{"API_TOKEN": "production-secret"}, PutOptions{Reason: "prod"}); err != nil {
		t.Fatal(err)
	}
	productionRecord, err := os.ReadFile(repo.valuePath("production", "API_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repo.valuePath("local", "API_TOKEN"), productionRecord, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.ReadVariables("local"); err == nil || !strings.Contains(err.Error(), "belongs to environment production, not local") {
		t.Fatalf("expected cross-environment value record to be rejected, got %v", err)
	}
}

func roleForGrant(grants []DeviceGrant, deviceID string, env string) string {
	for _, grant := range grants {
		if grant.DeviceID == deviceID && grant.Environment == env {
			return grant.Role
		}
	}
	return ""
}

func readPolicyForTest(t *testing.T, root string) domain.Policy {
	t.Helper()

	var policy domain.Policy
	if err := readJSON(filepath.Join(root, ".ghostable", "policy.json"), &policy); err != nil {
		t.Fatal(err)
	}
	return policy
}

func writePolicyForTest(t *testing.T, root string, policy domain.Policy) {
	t.Helper()

	if err := writeJSONAtomic(filepath.Join(root, ".ghostable", "policy.json"), policy, 0o644); err != nil {
		t.Fatal(err)
	}
}
