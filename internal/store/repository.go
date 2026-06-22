package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	gcrypto "github.com/ghostable-dev/beta/internal/crypto"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/manifest"
	"github.com/ghostable-dev/beta/internal/security"
)

var (
	namePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	keyPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Repository struct {
	Root         string
	ManifestPath string
	Manifest     domain.ProjectManifest
	Identity     domain.LocalIdentityRecord

	identityStore security.IdentityStore
	identityPath  string
	legacyKey     []byte
}

type SetupOptions struct {
	Name           string
	Environments   []domain.Environment
	DeviceName     string
	Platform       string
	Language       string
	Framework      string
	PackageManager string
	DeployTarget   string
	ActivityMode   string
	Force          bool
}

type PutOptions struct {
	Reason string
	Sync   bool
}

type PullOptions struct {
	File      string
	Only      []string
	DryRun    bool
	Replace   bool
	Backup    bool
	Force     bool
	ShowValue bool
	SkipEvent bool
}

type PullResult struct {
	Environment string            `json:"environment"`
	File        string            `json:"file"`
	DryRun      bool              `json:"dryRun"`
	BackupFile  string            `json:"backupFile,omitempty"`
	Variables   []domain.Variable `json:"variables"`
	Written     int               `json:"written"`
}

type PushResult struct {
	Environment string   `json:"environment"`
	File        string   `json:"file"`
	Created     []string `json:"created"`
	Updated     []string `json:"updated"`
	Deleted     []string `json:"deleted"`
	Unchanged   []string `json:"unchanged"`
}

type EnvironmentResult struct {
	Environment domain.Environment `json:"environment"`
	Created     bool               `json:"created"`
}

type AutomationCredentialGrant struct {
	EnvironmentName string `json:"environmentName"`
	Role            string `json:"role"`
}

type AutomationCredentialSummary struct {
	ID          string                      `json:"id"`
	DeviceID    string                      `json:"deviceId"`
	Name        string                      `json:"name"`
	Kind        string                      `json:"kind"`
	Status      string                      `json:"status"`
	CreatedAt   string                      `json:"createdAt"`
	Permissions []AutomationCredentialGrant `json:"permissions"`
}

type AutomationCredentialResult struct {
	Action              string                      `json:"action"`
	State               string                      `json:"state"`
	Credential          AutomationCredentialSummary `json:"credential"`
	Token               string                      `json:"token"`
	EnvironmentVariable string                      `json:"environmentVariable"`
	Files               []string                    `json:"files"`
	Completed           bool                        `json:"completed"`
}

type automationCredentialToken struct {
	Schema      string                      `json:"schema"`
	Version     int                         `json:"version"`
	ProjectID   string                      `json:"projectId"`
	ProjectName string                      `json:"projectName"`
	Name        string                      `json:"name"`
	Kind        string                      `json:"kind"`
	DeviceID    string                      `json:"deviceId"`
	CreatedAt   string                      `json:"createdAt"`
	Permissions []AutomationCredentialGrant `json:"permissions"`
	Identity    domain.LocalIdentityRecord  `json:"identity"`
}

const (
	automationCredentialEnvironmentVariable = "GHOSTABLE_CI_TOKEN"
	automationCredentialSchema              = "ghostable.automation-credential.v1"
	automationCredentialTokenPrefix         = "ghostable_credential_"
)

type DeviceGrant struct {
	DeviceID    string `json:"deviceId"`
	DeviceName  string `json:"deviceName"`
	Platform    string `json:"platform,omitempty"`
	Status      string `json:"status,omitempty"`
	Environment string `json:"environment"`
	Role        string `json:"role"`
	Current     bool   `json:"current"`
}

type DeviceRevokeResult struct {
	DeviceID    string        `json:"deviceId"`
	Environment string        `json:"environment"`
	Removed     []DeviceGrant `json:"removed"`
	Files       []string      `json:"files"`
	Revoked     bool          `json:"revoked"`
}

type DeviceDeleteResult struct {
	DeviceID string `json:"deviceId"`
	Device   string `json:"device"`
	File     string `json:"file"`
	Deleted  bool   `json:"deleted"`
}

type AccessRequestEntry struct {
	Request     domain.AccessRequest `json:"request"`
	Device      domain.DeviceRecord  `json:"device"`
	AccessState string               `json:"accessState"`
}

type InvalidAccessRequestEntry struct {
	Request domain.AccessRequest `json:"request"`
	Error   string               `json:"error"`
}

type AccessRequestList struct {
	Valid   []AccessRequestEntry        `json:"valid"`
	Invalid []InvalidAccessRequestEntry `json:"invalid"`
}

type AccessRequestResult struct {
	Action      string               `json:"action"`
	State       string               `json:"state"`
	RequestID   string               `json:"requestId"`
	DeviceID    string               `json:"deviceId"`
	Environment string               `json:"environment"`
	Role        string               `json:"role"`
	Request     domain.AccessRequest `json:"request"`
	Device      domain.DeviceRecord  `json:"device"`
	RequestPath string               `json:"requestPath"`
	File        string               `json:"file"`
	Files       []string             `json:"files"`
	Completed   bool                 `json:"completed"`
}

type AccessRequestReviewResult struct {
	Action      string                     `json:"action"`
	State       string                     `json:"state"`
	RequestID   string                     `json:"requestId"`
	DeviceID    string                     `json:"deviceId"`
	Environment string                     `json:"environment"`
	Role        string                     `json:"role"`
	Request     domain.AccessRequest       `json:"request"`
	Review      domain.AccessRequestReview `json:"review"`
	Files       []string                   `json:"files"`
	Completed   bool                       `json:"completed"`
}

func FindRoot(start string) (string, string, error) {
	if start == "" {
		start = "."
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}

	info, err := os.Stat(current)
	if err == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}

	for {
		candidates := []string{
			filepath.Join(current, ".ghostable", "ghostable.yaml"),
			filepath.Join(current, ".ghostable", "ghostable.yml"),
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				return current, candidate, nil
			}
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return "", "", fmt.Errorf("no Ghostable manifest found; run `ghostable setup` first")
}

func (r Repository) History(env string, key string, action string, limit int) ([]domain.Event, error) {
	eventsDir := filepath.Join(r.Root, ".ghostable", "events")
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []domain.Event{}, nil
		}
		return nil, err
	}

	events := make([]domain.Event, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var event domain.Event
		if err := readJSON(filepath.Join(eventsDir, entry.Name()), &event); err != nil {
			continue
		}
		if err := r.verifyEvent(event); err != nil {
			return nil, err
		}
		if env != "" && event.Environment != env {
			continue
		}
		if key != "" && event.Key != key {
			continue
		}
		if action != "" && event.Action != action {
			continue
		}
		events = append(events, event)
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].OccurredAt > events[j].OccurredAt
	})
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

func (r Repository) verifyEvent(event domain.Event) error {
	if event.ClientSig == "" {
		return fmt.Errorf("event %s is missing a signature", event.Action)
	}
	signerID := event.SignerDeviceID
	if signerID == "" {
		signerID = event.DeviceID
	}
	if signerID == "" {
		return fmt.Errorf("event %s is missing a signer device", event.Action)
	}
	device, err := r.readDevice(signerID)
	if err != nil {
		return err
	}
	if !security.VerifyCanonical(event, device.SigningKey.PublicKey, event.ClientSig) {
		return fmt.Errorf("event %s signature could not be verified", event.Action)
	}
	return nil
}

func (r Repository) Devices() ([]domain.DeviceRecord, error) {
	devicesDir := filepath.Join(r.Root, ".ghostable", "devices")
	entries, err := os.ReadDir(devicesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []domain.DeviceRecord{}, nil
		}
		return nil, err
	}
	devices := make([]domain.DeviceRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var device domain.DeviceRecord
		if err := readJSON(filepath.Join(devicesDir, entry.Name()), &device); err == nil {
			devices = append(devices, device)
		}
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
	return devices, nil
}

func (r Repository) JoinDevice(name string, platform string) (domain.DeviceRecord, bool, error) {
	if name == "" {
		name = domain.DefaultDeviceName
	}
	if platform == "" {
		platform = platformLabel()
	}
	identity, device, err := security.NewDeviceIdentity(r.Manifest.ID, name, platform)
	if err != nil {
		return domain.DeviceRecord{}, false, err
	}
	policy, err := r.readPolicyFile()
	if err != nil {
		return domain.DeviceRecord{}, false, fmt.Errorf("verify existing policy before joining device: %w", err)
	}
	policySigner, err := r.verifyPolicySignature(policy)
	if err != nil {
		return domain.DeviceRecord{}, false, fmt.Errorf("verify existing policy before joining device: %w", err)
	}
	if policy.Version < 1 {
		return domain.DeviceRecord{}, false, fmt.Errorf("verify existing policy before joining device: policy version is required")
	}
	identity.TrustedPolicySigners = trustedPolicySigners(policySigner)
	identity.TrustedPolicyVersion = policy.Version
	if err := r.identityStore.Save(identity); err != nil {
		return domain.DeviceRecord{}, false, err
	}
	r.Identity = identity
	if err := r.writeDevice(device); err != nil {
		return domain.DeviceRecord{}, false, err
	}
	return device, true, r.recordEvent("device.joined", "", "", map[string]interface{}{"deviceId": device.ID})
}

func (r Repository) CreateAccessRequest(env string, role string, reason string) (AccessRequestResult, error) {
	env, role, err := r.normalizeAccessRequestTarget(env, role)
	if err != nil {
		return AccessRequestResult{}, err
	}
	if r.DeviceID() == "" {
		return AccessRequestResult{}, fmt.Errorf("this device has no local Ghostable identity")
	}
	device, err := r.localDeviceRecord()
	if err != nil {
		return AccessRequestResult{}, err
	}
	if granted, err := r.accessRequestAlreadyGranted(r.DeviceID(), env, role); err != nil {
		return AccessRequestResult{}, err
	} else if granted {
		return AccessRequestResult{
			Action:      "access.request.create",
			State:       "already_granted",
			DeviceID:    r.DeviceID(),
			Environment: env,
			Role:        role,
			Device:      device,
			Files:       []string{},
			Completed:   true,
		}, nil
	}
	if existing, ok, err := r.findPendingAccessRequest(r.DeviceID(), env, role); err != nil {
		return AccessRequestResult{}, err
	} else if ok {
		relativePath := accessRequestRelativePath(existing.ID)
		return AccessRequestResult{
			Action:      "access.request.create",
			State:       "existing",
			RequestID:   existing.ID,
			DeviceID:    existing.DeviceID,
			Environment: existing.Environment,
			Role:        existing.Role,
			Request:     existing,
			Device:      device,
			RequestPath: relativePath,
			File:        relativePath,
			Files:       []string{accessDeviceRelativePath(existing.DeviceID), relativePath},
			Completed:   true,
		}, nil
	}

	requestID, err := randomRequestID()
	if err != nil {
		return AccessRequestResult{}, err
	}
	request := domain.AccessRequest{
		Schema:         domain.AccessRequestSchema,
		ProjectID:      r.Manifest.ID,
		ID:             requestID,
		DeviceID:       r.DeviceID(),
		Environment:    env,
		Role:           role,
		Reason:         strings.TrimSpace(reason),
		CreatedAt:      security.Now(),
		SignerDeviceID: r.DeviceID(),
	}
	signature, err := security.SignCanonical(request, r.Identity)
	if err != nil {
		return AccessRequestResult{}, err
	}
	request.ClientSig = signature
	record := domain.AccessRequestFile{
		Schema:    domain.AccessRequestSchema,
		ProjectID: r.Manifest.ID,
		Request:   request,
	}
	if err := r.writeAccessRequestFile(record); err != nil {
		return AccessRequestResult{}, err
	}
	if err := r.recordEvent("access.request.created", accessRequestEventEnvironment(env), "", map[string]interface{}{"requestId": request.ID, "role": request.Role, "deviceId": request.DeviceID}); err != nil {
		return AccessRequestResult{}, err
	}

	relativePath := accessRequestRelativePath(request.ID)
	return AccessRequestResult{
		Action:      "access.request.create",
		State:       "created",
		RequestID:   request.ID,
		DeviceID:    request.DeviceID,
		Environment: request.Environment,
		Role:        request.Role,
		Request:     request,
		Device:      device,
		RequestPath: relativePath,
		File:        relativePath,
		Files:       []string{accessDeviceRelativePath(request.DeviceID), relativePath},
		Completed:   true,
	}, nil
}

func (r Repository) ListAccessRequests(includeReviewed bool) (AccessRequestList, error) {
	return r.readAccessRequests(includeReviewed)
}

func (r Repository) ApproveAccessRequest(requestID string) (AccessRequestReviewResult, error) {
	return r.reviewAccessRequest(requestID, "approved", "")
}

func (r Repository) DenyAccessRequest(requestID string, reason string) (AccessRequestReviewResult, error) {
	return r.reviewAccessRequest(requestID, "denied", reason)
}

func (r Repository) CreateAutomationCredential(name string, kind string, grants []AutomationCredentialGrant) (AutomationCredentialResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return AutomationCredentialResult{}, fmt.Errorf("credential name is required")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "ci"
	}
	if kind == "deploy" {
		kind = "access"
	}
	if !oneOf(kind, "agent", "ci", "access") {
		return AutomationCredentialResult{}, fmt.Errorf("kind must be agent, ci, or access")
	}

	permissions, err := r.normalizeAutomationCredentialGrants(grants)
	if err != nil {
		return AutomationCredentialResult{}, err
	}
	if len(permissions) == 0 {
		return AutomationCredentialResult{}, fmt.Errorf("at least one credential grant is required")
	}
	for _, permission := range permissions {
		if err := r.requireGrant(permission.EnvironmentName); err != nil {
			return AutomationCredentialResult{}, err
		}
	}

	identity, device, err := security.NewDeviceIdentity(r.Manifest.ID, name, "ghostable-automation:"+kind)
	if err != nil {
		return AutomationCredentialResult{}, err
	}
	identity.TrustedPolicySigners = trustedPolicySigners(r.DeviceID())
	if err := r.writeDevice(device); err != nil {
		return AutomationCredentialResult{}, err
	}

	for _, permission := range permissions {
		if err := r.ShareDevice(device.ID, permission.EnvironmentName, permission.Role); err != nil {
			return AutomationCredentialResult{}, err
		}
	}
	identity.TrustedPolicyVersion = r.trustedPolicyVersion()

	payload := automationCredentialToken{
		Schema:      automationCredentialSchema,
		Version:     1,
		ProjectID:   r.Manifest.ID,
		ProjectName: r.Manifest.Name,
		Name:        name,
		Kind:        kind,
		DeviceID:    device.ID,
		CreatedAt:   identity.CreatedAt,
		Permissions: permissions,
		Identity:    identity,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return AutomationCredentialResult{}, err
	}

	return AutomationCredentialResult{
		Action: "credential.create",
		State:  "created",
		Credential: AutomationCredentialSummary{
			ID:          device.ID,
			DeviceID:    device.ID,
			Name:        name,
			Kind:        kind,
			Status:      device.Status,
			CreatedAt:   device.CreatedAt,
			Permissions: permissions,
		},
		Token:               automationCredentialTokenPrefix + base64.RawURLEncoding.EncodeToString(encoded),
		EnvironmentVariable: automationCredentialEnvironmentVariable,
		Files:               automationCredentialFiles(device.ID, permissions),
		Completed:           true,
	}, nil
}

func loadAutomationCredentialIdentity(projectID string) (domain.LocalIdentityRecord, bool, error) {
	rawToken := strings.TrimSpace(os.Getenv(automationCredentialEnvironmentVariable))
	if rawToken == "" {
		return domain.LocalIdentityRecord{}, false, nil
	}

	identity, err := parseAutomationCredentialIdentity(rawToken, projectID)
	if err != nil {
		return domain.LocalIdentityRecord{}, true, err
	}
	return identity, true, nil
}

func parseAutomationCredentialIdentity(rawToken string, projectID string) (domain.LocalIdentityRecord, error) {
	if !strings.HasPrefix(rawToken, automationCredentialTokenPrefix) {
		return domain.LocalIdentityRecord{}, fmt.Errorf("%s has an invalid prefix", automationCredentialEnvironmentVariable)
	}

	encodedPayload := strings.TrimPrefix(rawToken, automationCredentialTokenPrefix)
	payloadBytes, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return domain.LocalIdentityRecord{}, fmt.Errorf("decode %s: %w", automationCredentialEnvironmentVariable, err)
	}

	var payload automationCredentialToken
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return domain.LocalIdentityRecord{}, fmt.Errorf("parse %s: %w", automationCredentialEnvironmentVariable, err)
	}
	if payload.Schema != automationCredentialSchema {
		return domain.LocalIdentityRecord{}, fmt.Errorf("%s has unsupported schema %q", automationCredentialEnvironmentVariable, payload.Schema)
	}
	if payload.Version != 1 {
		return domain.LocalIdentityRecord{}, fmt.Errorf("%s has unsupported version %d", automationCredentialEnvironmentVariable, payload.Version)
	}
	if payload.ProjectID != projectID {
		return domain.LocalIdentityRecord{}, fmt.Errorf("%s project id does not match this project", automationCredentialEnvironmentVariable)
	}

	identity := payload.Identity
	if identity.Schema != domain.LocalIdentitySchema {
		return domain.LocalIdentityRecord{}, fmt.Errorf("%s contains an unsupported identity schema", automationCredentialEnvironmentVariable)
	}
	if identity.ProjectID != projectID {
		return domain.LocalIdentityRecord{}, fmt.Errorf("%s identity project id does not match this project", automationCredentialEnvironmentVariable)
	}
	if identity.DeviceID == "" {
		return domain.LocalIdentityRecord{}, fmt.Errorf("%s identity device id is required", automationCredentialEnvironmentVariable)
	}
	if payload.DeviceID != "" && payload.DeviceID != identity.DeviceID {
		return domain.LocalIdentityRecord{}, fmt.Errorf("%s device id does not match its identity", automationCredentialEnvironmentVariable)
	}
	if identity.SigningPrivateKeyB64 == "" || identity.EncryptionPrivateKeyB64 == "" {
		return domain.LocalIdentityRecord{}, fmt.Errorf("%s is missing private key material", automationCredentialEnvironmentVariable)
	}

	return identity, nil
}

func (r Repository) DeviceGrants(env string) ([]DeviceGrant, error) {
	if env != "" && env != "all" {
		if err := r.requireEnvironment(env); err != nil {
			return nil, err
		}
	}

	policy, err := r.readPolicy()
	if err != nil {
		return nil, err
	}
	devices, err := r.Devices()
	if err != nil {
		return nil, err
	}
	devicesByID := map[string]domain.DeviceRecord{}
	for _, device := range devices {
		devicesByID[device.ID] = device
	}

	envs := r.grantEnvironmentNames(env)
	grants := []DeviceGrant{}
	for _, envName := range envs {
		for _, deviceID := range policyDeviceIDs(policy, envName) {
			role := policyRole(policy, envName, deviceID)
			if role == "" {
				continue
			}
			device := devicesByID[deviceID]
			grants = append(grants, DeviceGrant{
				DeviceID:    deviceID,
				DeviceName:  deviceGrantName(device, deviceID),
				Platform:    device.Platform,
				Status:      device.Status,
				Environment: envName,
				Role:        role,
				Current:     deviceID == r.DeviceID(),
			})
		}
	}

	sort.Slice(grants, func(i, j int) bool {
		if grants[i].Environment != grants[j].Environment {
			return grants[i].Environment < grants[j].Environment
		}
		if grants[i].Role != grants[j].Role {
			return roleRank(grants[i].Role) < roleRank(grants[j].Role)
		}
		if grants[i].DeviceName != grants[j].DeviceName {
			return grants[i].DeviceName < grants[j].DeviceName
		}
		return grants[i].DeviceID < grants[j].DeviceID
	})
	return grants, nil
}

func (r Repository) RevokeDevice(deviceID string, env string) (DeviceRevokeResult, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return DeviceRevokeResult{}, fmt.Errorf("device id is required")
	}
	if deviceID == r.DeviceID() {
		return DeviceRevokeResult{}, fmt.Errorf("cannot revoke the current device; use device leave when local key removal is supported")
	}
	if env == "" {
		env = "all"
	}
	if env != "all" {
		if err := r.requireEnvironment(env); err != nil {
			return DeviceRevokeResult{}, err
		}
	}
	if _, err := r.readDevice(deviceID); err != nil {
		return DeviceRevokeResult{}, err
	}

	policy, err := r.readPolicy()
	if err != nil {
		return DeviceRevokeResult{}, err
	}
	isOwner := contains(policy.Owners, deviceID)
	if isOwner && env != "all" {
		return DeviceRevokeResult{}, fmt.Errorf("owner access is project-wide; revoke this device with --env all")
	}
	if isOwner {
		if err := r.requireOwner(); err != nil {
			return DeviceRevokeResult{}, err
		}
		if len(policy.Owners) <= 1 {
			return DeviceRevokeResult{}, fmt.Errorf("cannot revoke the last owner device")
		}
	} else {
		for _, envName := range r.grantEnvironmentNames(env) {
			if err := r.requireGrant(envName); err != nil {
				return DeviceRevokeResult{}, err
			}
		}
	}

	before, err := r.DeviceGrants(env)
	if err != nil {
		return DeviceRevokeResult{}, err
	}
	removed := []DeviceGrant{}
	for _, grant := range before {
		if grant.DeviceID == deviceID {
			removed = append(removed, grant)
		}
	}
	if len(removed) == 0 && !isOwner {
		return DeviceRevokeResult{DeviceID: deviceID, Environment: env, Removed: []DeviceGrant{}, Files: []string{}, Revoked: false}, nil
	}

	files := []string{}
	if isOwner {
		policy.Owners = removeValue(policy.Owners, deviceID)
		files = append(files, filepath.Join(".ghostable", "policy.json"))
	}
	if policy.Revoked == nil {
		policy.Revoked = map[string]domain.DeviceRevocation{}
	}
	policy.Revoked[deviceID] = domain.DeviceRevocation{
		DeviceID:          deviceID,
		RevokedAt:         security.Now(),
		RevokedByDeviceID: r.DeviceID(),
	}

	for _, envName := range r.grantEnvironmentNames(env) {
		entry := policy.Environments[envName]
		entry.Readers = removeValue(entry.Readers, deviceID)
		entry.Writers = removeValue(entry.Writers, deviceID)
		entry.Grantors = removeValue(entry.Grantors, deviceID)
		policy.Environments[envName] = entry

		grantPath := filepath.Join(r.accessDir(envName), idFileName(deviceID))
		if err := os.Remove(grantPath); err != nil && !os.IsNotExist(err) {
			return DeviceRevokeResult{}, err
		}
		files = appendUnique(files, filepath.Join(".ghostable", "environments", environmentPathSegment(envName), "access", idFileName(deviceID)))
		if isOwner || grantRemovedFromEnvironment(removed, deviceID, envName) {
			rotatedFiles, err := r.rotateEnvironmentKey(envName, policy)
			if err != nil {
				return DeviceRevokeResult{}, err
			}
			for _, file := range rotatedFiles {
				files = appendUnique(files, file)
			}
		}
		if err := r.recordEvent("device.revoked", envName, "", map[string]interface{}{"deviceId": deviceID}); err != nil {
			return DeviceRevokeResult{}, err
		}
	}
	files = appendUnique(files, filepath.Join(".ghostable", "policy.json"))

	policy.UpdatedAt = security.Now()
	if err := r.signAndWritePolicy(policy); err != nil {
		return DeviceRevokeResult{}, err
	}

	return DeviceRevokeResult{
		DeviceID:    deviceID,
		Environment: env,
		Removed:     removed,
		Files:       files,
		Revoked:     true,
	}, nil
}

func grantRemovedFromEnvironment(removed []DeviceGrant, deviceID string, env string) bool {
	for _, grant := range removed {
		if grant.DeviceID == deviceID && grant.Environment == env {
			return true
		}
	}
	return false
}

type variableRotationRecord struct {
	record    domain.ValueRecord
	plaintext string
}

func (r Repository) rotateEnvironmentKey(env string, policy domain.Policy) ([]string, error) {
	values, err := r.readVariableRotationRecords(env)
	if err != nil {
		return nil, err
	}
	previousKey, err := r.readEnvironmentKey(env)
	if err != nil {
		return nil, err
	}
	nextKey, envKey, dek, err := security.RotateEnvironmentKey(r.Manifest.ID, env, r.Identity, previousKey)
	if err != nil {
		return nil, err
	}

	deviceIDs := policyDeviceIDs(policy, env)
	grants := make([]domain.AccessGrantRecord, 0, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		device, err := r.readDevice(deviceID)
		if err != nil {
			return nil, err
		}
		grant, err := security.NewAccessGrant(r.Manifest.ID, env, r.Identity, device, dek, nextKey)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}

	rotatedValues := make([]domain.ValueRecord, 0, len(values))
	for _, value := range values {
		record, err := r.buildRotatedValueRecord(value, nextKey, envKey)
		if err != nil {
			return nil, err
		}
		rotatedValues = append(rotatedValues, record)
	}

	files := []string{}
	if err := writeJSONAtomic(filepath.Join(r.environmentDir(env), "key.json"), nextKey, 0o644); err != nil {
		return nil, err
	}
	files = appendUnique(files, filepath.Join(".ghostable", "environments", environmentPathSegment(env), "key.json"))
	for _, grant := range grants {
		if err := r.writeAccessGrant(env, grant); err != nil {
			return nil, err
		}
		files = appendUnique(files, filepath.Join(".ghostable", "environments", environmentPathSegment(env), "access", idFileName(grant.DeviceID)))
	}
	for _, record := range rotatedValues {
		if err := writeJSONAtomic(r.valuePath(env, record.Key), record, 0o600); err != nil {
			return nil, err
		}
		files = appendUnique(files, filepath.Join(".ghostable", "environments", environmentPathSegment(env), "values", filepath.Base(r.valuePath(env, record.Key))))
	}
	return files, nil
}

func (r Repository) readVariableRotationRecords(env string) ([]variableRotationRecord, error) {
	valueRecords, err := r.readEnvironmentValueRecords(env)
	if err != nil {
		return nil, err
	}
	records := make([]variableRotationRecord, 0, len(valueRecords))
	for _, record := range valueRecords {
		plaintext, _, err := r.decryptRecord(record)
		if err != nil {
			return nil, err
		}
		records = append(records, variableRotationRecord{
			record:    record,
			plaintext: plaintext,
		})
	}
	return records, nil
}

func (r Repository) buildRotatedValueRecord(value variableRotationRecord, envKeyRecord domain.EnvironmentKeyRecord, envKey []byte) (domain.ValueRecord, error) {
	version := value.record.Version
	if version < 1 {
		version = 1
	}
	vaporSecret := value.record.Secret.IsVaporSecret != nil && *value.record.Secret.IsVaporSecret
	secret, err := security.BuildSecret(security.BuildSecretInput{
		ProjectID:            r.Manifest.ID,
		Environment:          value.record.Environment,
		Key:                  value.record.Key,
		Plaintext:            value.plaintext,
		EnvironmentKey:       envKey,
		EnvironmentKeyRecord: envKeyRecord,
		Identity:             r.Identity,
		Commented:            value.record.Secret.IsCommented,
		VaporSecret:          vaporSecret,
	})
	if err != nil {
		return domain.ValueRecord{}, err
	}
	return domain.ValueRecord{
		Schema:            domain.ValueSchema,
		ProjectID:         r.Manifest.ID,
		Environment:       value.record.Environment,
		Key:               value.record.Key,
		Version:           version,
		UpdatedAt:         security.Now(),
		UpdatedByDeviceID: r.DeviceID(),
		Secret:            secret,
	}, nil
}

func (r Repository) DeleteDevice(deviceID string) (DeviceDeleteResult, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return DeviceDeleteResult{}, fmt.Errorf("device id is required")
	}
	if deviceID == r.DeviceID() {
		return DeviceDeleteResult{}, fmt.Errorf("cannot delete the current device")
	}
	device, err := r.readDevice(deviceID)
	if err != nil {
		return DeviceDeleteResult{}, err
	}
	if err := r.requireOwner(); err != nil {
		return DeviceDeleteResult{}, err
	}
	grants, err := r.DeviceGrants("all")
	if err != nil {
		return DeviceDeleteResult{}, err
	}
	for _, grant := range grants {
		if grant.DeviceID == deviceID {
			return DeviceDeleteResult{}, fmt.Errorf("revoke %s access before deleting this device", deviceGrantName(device, deviceID))
		}
	}

	file := filepath.Join(".ghostable", "devices", idFileName(deviceID))
	if err := os.Remove(filepath.Join(r.Root, file)); err != nil {
		return DeviceDeleteResult{}, err
	}
	if err := r.recordEvent("device.deleted", "", "", map[string]interface{}{"deviceId": deviceID}); err != nil {
		return DeviceDeleteResult{}, err
	}

	return DeviceDeleteResult{
		DeviceID: deviceID,
		Device:   deviceGrantName(device, deviceID),
		File:     file,
		Deleted:  true,
	}, nil
}

func (r Repository) grantEnvironmentNames(env string) []string {
	if env != "" && env != "all" {
		return []string{env}
	}
	envs := make([]string, 0, len(r.Manifest.Environments))
	for name := range r.Manifest.Environments {
		envs = append(envs, name)
	}
	sort.Strings(envs)
	return envs
}

func policyDeviceIDs(policy domain.Policy, env string) []string {
	ids := []string{}
	for _, id := range policy.Owners {
		if !policyDeviceRevoked(policy, id) {
			ids = appendUnique(ids, id)
		}
	}
	entry := policy.Environments[env]
	for _, id := range entry.Grantors {
		if !policyDeviceRevoked(policy, id) {
			ids = appendUnique(ids, id)
		}
	}
	for _, id := range entry.Writers {
		if !policyDeviceRevoked(policy, id) {
			ids = appendUnique(ids, id)
		}
	}
	for _, id := range entry.Readers {
		if !policyDeviceRevoked(policy, id) {
			ids = appendUnique(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func policyRole(policy domain.Policy, env string, deviceID string) string {
	if contains(policy.Owners, deviceID) {
		return "owner"
	}
	entry := policy.Environments[env]
	switch {
	case contains(entry.Grantors, deviceID):
		return "grantor"
	case contains(entry.Writers, deviceID):
		return "writer"
	case contains(entry.Readers, deviceID):
		return "reader"
	default:
		return ""
	}
}

func deviceGrantName(device domain.DeviceRecord, deviceID string) string {
	name := strings.TrimSpace(device.Name)
	if name != "" {
		return name
	}
	if device.ID == "" {
		return "Unknown device"
	}
	return deviceID
}

func roleRank(role string) int {
	switch role {
	case "owner":
		return 0
	case "grantor":
		return 1
	case "writer":
		return 2
	case "reader":
		return 3
	default:
		return 4
	}
}

func (r Repository) ShareDevice(deviceID string, env string, role string) error {
	if deviceID == "" {
		return fmt.Errorf("device id is required")
	}
	if role == "" {
		role = "reader"
	}
	if !oneOf(role, "reader", "writer", "grantor", "owner") {
		return fmt.Errorf("role must be reader, writer, grantor, or owner")
	}

	policy, err := r.readPolicy()
	if err != nil {
		return err
	}
	if policyDeviceRevoked(policy, deviceID) {
		return fmt.Errorf("device %s has been revoked; join again with a new device identity", deviceID)
	}
	envs := []string{env}
	if env == "" || env == "all" {
		envs = nil
		for name := range r.Manifest.Environments {
			envs = append(envs, name)
		}
		sort.Strings(envs)
	}

	for _, name := range envs {
		if err := r.requireEnvironment(name); err != nil {
			return err
		}
		if role == "owner" {
			if err := r.requireOwner(); err != nil {
				return err
			}
		} else if err := r.requireGrant(name); err != nil {
			return err
		}
		entry := policy.Environments[name]
		switch role {
		case "reader":
			entry.Readers = appendUnique(entry.Readers, deviceID)
		case "writer":
			entry.Writers = appendUnique(entry.Writers, deviceID)
			entry.Readers = appendUnique(entry.Readers, deviceID)
		case "grantor":
			entry.Grantors = appendUnique(entry.Grantors, deviceID)
			entry.Readers = appendUnique(entry.Readers, deviceID)
		case "owner":
			policy.Owners = appendUnique(policy.Owners, deviceID)
		}
		policy.Environments[name] = entry
		if err := r.recordEvent("device.shared", name, "", map[string]interface{}{"deviceId": deviceID, "role": role}); err != nil {
			return err
		}
		envKey, err := r.readEnvironmentKey(name)
		if err != nil {
			return err
		}
		dek, err := r.loadEnvironmentDEK(name)
		if err != nil {
			return err
		}
		device, err := r.readDevice(deviceID)
		if err != nil {
			return err
		}
		grant, err := security.NewAccessGrant(r.Manifest.ID, name, r.Identity, device, dek, envKey)
		if err != nil {
			return err
		}
		if err := r.writeAccessGrant(name, grant); err != nil {
			return err
		}
	}
	policy.UpdatedAt = security.Now()
	return r.signAndWritePolicy(policy)
}

func (r Repository) DeviceID() string {
	return r.Identity.DeviceID
}

func (r Repository) normalizeAccessRequestTarget(env string, role string) (string, string, error) {
	env = strings.TrimSpace(env)
	if env == "" {
		env = "all"
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		role = "reader"
	}
	if !oneOf(role, "reader", "writer", "grantor", "owner") {
		return "", "", fmt.Errorf("role must be reader, writer, grantor, or owner")
	}
	if role == "owner" {
		env = "all"
	}
	if env != "all" {
		if err := r.requireEnvironment(env); err != nil {
			return "", "", err
		}
	}
	return env, role, nil
}

func (r Repository) findPendingAccessRequest(deviceID string, env string, role string) (domain.AccessRequest, bool, error) {
	requests, err := r.readAccessRequests(true)
	if err != nil {
		return domain.AccessRequest{}, false, err
	}
	for _, entry := range requests.Valid {
		request := entry.Request
		if entry.AccessState == "pending" && request.DeviceID == deviceID && request.Environment == env && request.Role == role {
			return request, true, nil
		}
	}
	return domain.AccessRequest{}, false, nil
}

func (r Repository) accessRequestAlreadyGranted(deviceID string, env string, role string) (bool, error) {
	policy, err := r.readPolicy()
	if err != nil {
		return false, err
	}
	for _, envName := range r.grantEnvironmentNames(env) {
		if !accessRoleSatisfies(policyRole(policy, envName, deviceID), role) {
			return false, nil
		}
	}
	return len(r.grantEnvironmentNames(env)) > 0, nil
}

func accessRoleSatisfies(existingRole string, requestedRole string) bool {
	if existingRole == "owner" || existingRole == requestedRole {
		return true
	}
	switch requestedRole {
	case "reader":
		return existingRole == "writer" || existingRole == "grantor"
	case "writer":
		return existingRole == "owner"
	case "grantor":
		return existingRole == "owner"
	case "owner":
		return false
	default:
		return false
	}
}

func (r Repository) reviewAccessRequest(requestID string, status string, reason string) (AccessRequestReviewResult, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return AccessRequestReviewResult{}, fmt.Errorf("request id is required")
	}
	if !oneOf(status, "approved", "denied") {
		return AccessRequestReviewResult{}, fmt.Errorf("request review status must be approved or denied")
	}
	record, err := r.readAccessRequestFile(requestID)
	if err != nil {
		return AccessRequestReviewResult{}, err
	}
	request := record.Request
	if request.DeviceID == r.DeviceID() {
		return AccessRequestReviewResult{}, fmt.Errorf("this device cannot review its own access request")
	}
	if record.Review != nil {
		return AccessRequestReviewResult{
			Action:      accessRequestReviewAction(status),
			State:       record.Review.Status,
			RequestID:   request.ID,
			DeviceID:    request.DeviceID,
			Environment: request.Environment,
			Role:        request.Role,
			Request:     request,
			Review:      *record.Review,
			Files:       []string{accessRequestRelativePath(request.ID)},
			Completed:   true,
		}, nil
	}
	if err := r.requireAccessRequestReviewer(request); err != nil {
		return AccessRequestReviewResult{}, err
	}

	files := []string{accessRequestRelativePath(request.ID)}
	if status == "approved" {
		granted, err := r.accessRequestAlreadyGranted(request.DeviceID, request.Environment, request.Role)
		if err != nil {
			return AccessRequestReviewResult{}, err
		}
		if !granted {
			if err := r.ShareDevice(request.DeviceID, request.Environment, request.Role); err != nil {
				return AccessRequestReviewResult{}, err
			}
		}
		files = appendUnique(files, accessRequestPolicyRelativePath())
		for _, envName := range r.grantEnvironmentNames(request.Environment) {
			files = appendUnique(files, accessGrantRelativePath(envName, request.DeviceID))
		}
	}

	review := domain.AccessRequestReview{
		Schema:             domain.AccessRequestSchema,
		ProjectID:          r.Manifest.ID,
		RequestID:          request.ID,
		Status:             status,
		ReviewedByDeviceID: r.DeviceID(),
		ReviewedAt:         security.Now(),
		Reason:             strings.TrimSpace(reason),
		SignerDeviceID:     r.DeviceID(),
	}
	signature, err := security.SignCanonical(review, r.Identity)
	if err != nil {
		return AccessRequestReviewResult{}, err
	}
	review.ClientSig = signature
	record.Review = &review
	if err := r.writeAccessRequestFile(record); err != nil {
		return AccessRequestReviewResult{}, err
	}
	if err := r.recordEvent("access.request."+status, accessRequestEventEnvironment(request.Environment), "", map[string]interface{}{"requestId": request.ID, "role": request.Role, "deviceId": request.DeviceID}); err != nil {
		return AccessRequestReviewResult{}, err
	}

	return AccessRequestReviewResult{
		Action:      accessRequestReviewAction(status),
		State:       status,
		RequestID:   request.ID,
		DeviceID:    request.DeviceID,
		Environment: request.Environment,
		Role:        request.Role,
		Request:     request,
		Review:      review,
		Files:       files,
		Completed:   true,
	}, nil
}

func accessRequestReviewAction(status string) string {
	if status == "approved" {
		return "access.request.approve"
	}
	return "access.request.deny"
}

func (r Repository) requireAccessRequestReviewer(request domain.AccessRequest) error {
	if request.Role == "owner" {
		return r.requireOwner()
	}
	for _, envName := range r.grantEnvironmentNames(request.Environment) {
		if err := r.requireGrant(envName); err != nil {
			return err
		}
	}
	return nil
}

func (r Repository) accessRequestState(request domain.AccessRequest, review *domain.AccessRequestReview) (string, error) {
	if review != nil {
		return review.Status, nil
	}
	granted, err := r.accessRequestAlreadyGranted(request.DeviceID, request.Environment, request.Role)
	if err != nil {
		if isPolicyTrustError(err) {
			return "pending", nil
		}
		return "", err
	}
	if granted {
		return "granted", nil
	}
	return "pending", nil
}

func isPolicyTrustError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "policy signer") && strings.Contains(err.Error(), "not trusted")
}

func (r Repository) readAccessRequests(includeReviewed bool) (AccessRequestList, error) {
	requestsDir := r.accessRequestsDir()
	entries, err := os.ReadDir(requestsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return AccessRequestList{Valid: []AccessRequestEntry{}, Invalid: []InvalidAccessRequestEntry{}}, nil
		}
		return AccessRequestList{}, err
	}
	result := AccessRequestList{Valid: []AccessRequestEntry{}, Invalid: []InvalidAccessRequestEntry{}}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(requestsDir, entry.Name())
		record, err := r.readAccessRequestFileAt(path)
		if err != nil {
			result.Invalid = append(result.Invalid, InvalidAccessRequestEntry{Request: bestEffortAccessRequest(path), Error: err.Error()})
			continue
		}
		state, err := r.accessRequestState(record.Request, record.Review)
		if err != nil {
			result.Invalid = append(result.Invalid, InvalidAccessRequestEntry{Request: record.Request, Error: err.Error()})
			continue
		}
		if !includeReviewed && state != "pending" {
			continue
		}
		device, err := r.readDevice(record.Request.DeviceID)
		if err != nil {
			result.Invalid = append(result.Invalid, InvalidAccessRequestEntry{Request: record.Request, Error: err.Error()})
			continue
		}
		result.Valid = append(result.Valid, AccessRequestEntry{Request: record.Request, Device: device, AccessState: state})
	}
	sort.Slice(result.Valid, func(i, j int) bool {
		if result.Valid[i].Request.CreatedAt != result.Valid[j].Request.CreatedAt {
			return result.Valid[i].Request.CreatedAt > result.Valid[j].Request.CreatedAt
		}
		return result.Valid[i].Request.ID < result.Valid[j].Request.ID
	})
	sort.Slice(result.Invalid, func(i, j int) bool {
		return result.Invalid[i].Request.ID < result.Invalid[j].Request.ID
	})
	return result, nil
}

func (r Repository) readAccessRequestFile(requestID string) (domain.AccessRequestFile, error) {
	return r.readAccessRequestFileAt(r.accessRequestPath(requestID))
}

func (r Repository) readAccessRequestFileAt(path string) (domain.AccessRequestFile, error) {
	var record domain.AccessRequestFile
	if err := readJSON(path, &record); err != nil {
		return record, err
	}
	if record.Schema != domain.AccessRequestSchema || record.Request.Schema != domain.AccessRequestSchema {
		return record, fmt.Errorf("access request has invalid schema")
	}
	if record.ProjectID != r.Manifest.ID || record.Request.ProjectID != r.Manifest.ID {
		return record, fmt.Errorf("access request belongs to a different Ghostable project")
	}
	if record.Request.ID == "" {
		return record, fmt.Errorf("access request is missing an id")
	}
	if record.Request.DeviceID == "" {
		return record, fmt.Errorf("access request is missing a device id")
	}
	if record.Request.SignerDeviceID != record.Request.DeviceID {
		return record, fmt.Errorf("access request signer does not match requesting device")
	}
	normalizedEnv, normalizedRole, err := r.normalizeAccessRequestTarget(record.Request.Environment, record.Request.Role)
	if err != nil {
		return record, err
	}
	if normalizedEnv != record.Request.Environment || normalizedRole != record.Request.Role {
		return record, fmt.Errorf("access request target is not normalized")
	}
	device, err := r.readDevice(record.Request.DeviceID)
	if err != nil {
		return record, err
	}
	if !security.VerifyCanonical(record.Request, device.SigningKey.PublicKey, record.Request.ClientSig) {
		return record, fmt.Errorf("access request signature could not be verified")
	}
	if record.Review != nil {
		if err := r.verifyAccessRequestReview(record.Request, *record.Review); err != nil {
			return record, err
		}
	}
	return record, nil
}

func (r Repository) verifyAccessRequestReview(request domain.AccessRequest, review domain.AccessRequestReview) error {
	if review.Schema != domain.AccessRequestSchema {
		return fmt.Errorf("access request review has invalid schema")
	}
	if review.ProjectID != r.Manifest.ID || review.RequestID != request.ID {
		return fmt.Errorf("access request review is not bound to this request")
	}
	if !oneOf(review.Status, "approved", "denied") {
		return fmt.Errorf("access request review has invalid status")
	}
	if review.ReviewedByDeviceID == "" || review.SignerDeviceID != review.ReviewedByDeviceID {
		return fmt.Errorf("access request review signer does not match reviewing device")
	}
	device, err := r.readDevice(review.ReviewedByDeviceID)
	if err != nil {
		return err
	}
	if !security.VerifyCanonical(review, device.SigningKey.PublicKey, review.ClientSig) {
		return fmt.Errorf("access request review signature could not be verified")
	}
	if err := r.requireAccessRequestReviewAuthority(request, review.ReviewedByDeviceID); err != nil {
		return err
	}
	return nil
}

func (r Repository) requireAccessRequestReviewAuthority(request domain.AccessRequest, reviewerDeviceID string) error {
	policy, err := r.readPolicy()
	if err != nil {
		return err
	}
	if request.Role == "owner" {
		if policyDeviceRevoked(policy, reviewerDeviceID) || !contains(policy.Owners, reviewerDeviceID) {
			return fmt.Errorf("device %s is not authorized to review owner access requests", reviewerDeviceID)
		}
		return nil
	}
	for _, envName := range r.grantEnvironmentNames(request.Environment) {
		if !canGrant(policy, envName, reviewerDeviceID) {
			return fmt.Errorf("device %s is not authorized to review access requests for %s", reviewerDeviceID, envName)
		}
	}
	return nil
}

func (r Repository) writeAccessRequestFile(record domain.AccessRequestFile) error {
	if record.Request.ID == "" {
		return fmt.Errorf("access request id is required")
	}
	return writeJSONAtomic(r.accessRequestPath(record.Request.ID), record, 0o644)
}

func bestEffortAccessRequest(path string) domain.AccessRequest {
	var record domain.AccessRequestFile
	if err := readJSON(path, &record); err == nil {
		return record.Request
	}
	return domain.AccessRequest{ID: strings.TrimSuffix(filepath.Base(path), ".json")}
}

func (r Repository) accessRequestsDir() string {
	return filepath.Join(r.Root, ".ghostable", "access-requests")
}

func (r Repository) accessRequestPath(requestID string) string {
	return filepath.Join(r.accessRequestsDir(), idFileName(requestID))
}

func accessRequestRelativePath(requestID string) string {
	return filepath.Join(".ghostable", "access-requests", idFileName(requestID))
}

func accessDeviceRelativePath(deviceID string) string {
	return filepath.Join(".ghostable", "devices", idFileName(deviceID))
}

func accessGrantRelativePath(env string, deviceID string) string {
	return filepath.Join(".ghostable", "environments", environmentPathSegment(env), "access", idFileName(deviceID))
}

func accessRequestPolicyRelativePath() string {
	return filepath.Join(".ghostable", "policy.json")
}

func accessRequestEventEnvironment(env string) string {
	if env == "all" {
		return ""
	}
	return env
}

func (r Repository) KeyPath() string {
	if r.identityPath != "" {
		return r.identityPath
	}
	return r.identityStore.Path(r.Manifest.ID)
}

func (r Repository) normalizeAutomationCredentialGrants(grants []AutomationCredentialGrant) ([]AutomationCredentialGrant, error) {
	byEnvironment := map[string]string{}
	for _, grant := range grants {
		env := strings.TrimSpace(grant.EnvironmentName)
		role := strings.ToLower(strings.TrimSpace(grant.Role))
		if role == "" {
			role = "reader"
		}
		if env == "" {
			return nil, fmt.Errorf("credential grant environment is required")
		}
		if err := r.requireEnvironment(env); err != nil {
			return nil, err
		}
		if !oneOf(role, "reader", "writer") {
			return nil, fmt.Errorf("credential grant role must be reader or writer")
		}
		if existing := byEnvironment[env]; existing == "" || (existing == "reader" && role == "writer") {
			byEnvironment[env] = role
		}
	}

	envs := make([]string, 0, len(byEnvironment))
	for env := range byEnvironment {
		envs = append(envs, env)
	}
	sort.Strings(envs)

	normalized := make([]AutomationCredentialGrant, 0, len(envs))
	for _, env := range envs {
		normalized = append(normalized, AutomationCredentialGrant{
			EnvironmentName: env,
			Role:            byEnvironment[env],
		})
	}
	return normalized, nil
}

func automationCredentialFiles(deviceID string, permissions []AutomationCredentialGrant) []string {
	paths := []string{
		filepath.Join(".ghostable", "devices", idFileName(deviceID)),
		filepath.Join(".ghostable", "policy.json"),
	}
	seen := map[string]bool{}
	for _, path := range paths {
		seen[path] = true
	}
	for _, permission := range permissions {
		path := filepath.Join(".ghostable", "environments", environmentPathSegment(permission.EnvironmentName), "access", idFileName(deviceID))
		if seen[path] {
			continue
		}
		paths = append(paths, path)
		seen[path] = true
	}
	return paths
}

type writeVariableInput struct {
	Environment string
	Key         string
	Value       string
	Note        string
	Existing    bool
	Commented   bool
	VaporSecret bool
}

func (r Repository) writeVariable(input writeVariableInput) error {
	if err := r.requireWrite(input.Environment); err != nil {
		return err
	}
	envKeyRecord, envKey, err := r.loadEnvironmentKey(input.Environment)
	if err != nil {
		return err
	}
	previousVersion := 0
	if input.Existing {
		if previous, err := r.readValueRecord(r.valuePath(input.Environment, input.Key)); err == nil {
			previousVersion = previous.Version
		}
	}
	secret, err := security.BuildSecret(security.BuildSecretInput{
		ProjectID:            r.Manifest.ID,
		Environment:          input.Environment,
		Key:                  input.Key,
		Plaintext:            input.Value,
		EnvironmentKey:       envKey,
		EnvironmentKeyRecord: envKeyRecord,
		Identity:             r.Identity,
		PreviousVersion:      previousVersion,
		Commented:            input.Commented,
		VaporSecret:          input.VaporSecret,
	})
	if err != nil {
		return err
	}
	record := domain.ValueRecord{
		Schema:            domain.ValueSchema,
		ProjectID:         r.Manifest.ID,
		Environment:       input.Environment,
		Key:               input.Key,
		Version:           previousVersion + 1,
		UpdatedAt:         security.Now(),
		UpdatedByDeviceID: r.DeviceID(),
		Secret:            secret,
	}

	if err := writeJSONAtomic(r.valuePath(input.Environment, input.Key), record, 0o600); err != nil {
		return err
	}
	return r.addLayoutKey(input.Environment, input.Key)
}

func (r Repository) readValueRecord(path string) (domain.ValueRecord, error) {
	var record domain.ValueRecord
	if err := readJSON(path, &record); err != nil {
		return record, err
	}
	if record.Schema != domain.ValueSchema && record.Schema != domain.LegacyValueSchema {
		return record, fmt.Errorf("%s uses unsupported value schema %q", path, record.Schema)
	}
	if record.ProjectID != r.Manifest.ID {
		return record, fmt.Errorf("%s belongs to a different Ghostable project", path)
	}
	return record, nil
}

func (r Repository) decryptRecord(record domain.ValueRecord) (string, string, error) {
	if record.Schema == domain.LegacyValueSchema {
		if r.legacyKey == nil {
			return "", "", fmt.Errorf("legacy Go value %s requires the old local key", record.Key)
		}
		var legacy domain.LegacyValueRecord
		encoded, _ := json.Marshal(record)
		if err := json.Unmarshal(encoded, &legacy); err != nil {
			return "", "", err
		}
		plaintext, err := gcrypto.Decrypt(r.legacyKey, legacy.EncryptedValue, []byte(strings.Join([]string{r.Manifest.ID, legacy.Environment, legacy.Key, "value"}, "\n")))
		if err != nil {
			return "", "", err
		}
		note := ""
		if legacy.EncryptedNote != nil {
			noteBytes, err := gcrypto.Decrypt(r.legacyKey, *legacy.EncryptedNote, []byte(strings.Join([]string{r.Manifest.ID, legacy.Environment, legacy.Key, "note"}, "\n")))
			if err != nil {
				return "", "", err
			}
			note = string(noteBytes)
		}
		return string(plaintext), note, nil
	}
	if err := r.verifyValueRecord(record); err != nil {
		return "", "", err
	}
	_, envKey, err := r.loadEnvironmentKey(record.Environment)
	if err != nil {
		return "", "", err
	}
	value, err := security.DecryptSecret(record.Secret, envKey)
	if err != nil {
		return "", "", err
	}
	return value, "", nil
}

func (r Repository) ensureEnvironmentDirs(env string) error {
	if err := validateEnvironmentName(env); err != nil {
		return err
	}
	dirs := []string{
		r.environmentDir(env),
		r.accessDir(env),
		r.valuesDir(env),
		filepath.Join(r.environmentDir(env), "deploy"),
		filepath.Join(r.Root, ".ghostable", "devices"),
		filepath.Join(r.Root, ".ghostable", "events"),
	}
	for _, dir := range dirs {
		if err := ensureGhostableStatePath(dir); err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (r Repository) requireEnvironment(env string) error {
	if err := validateEnvironmentName(env); err != nil {
		return err
	}
	if _, ok := r.Manifest.Environments[env]; !ok {
		return fmt.Errorf("environment %q was not found", env)
	}
	return nil
}

func (r Repository) requireOwner() error {
	policy, err := r.readPolicy()
	if err != nil {
		return err
	}
	if !contains(policy.Owners, r.DeviceID()) {
		return fmt.Errorf("this device does not have owner access")
	}
	return nil
}

func (r Repository) requireWrite(env string) error {
	policy, err := r.readPolicy()
	if err != nil {
		return err
	}
	if !canWrite(policy, env, r.DeviceID()) {
		return fmt.Errorf("this device does not have write access to %s", env)
	}
	return nil
}

func (r Repository) requireGrant(env string) error {
	policy, err := r.readPolicy()
	if err != nil {
		return err
	}
	if !canGrant(policy, env, r.DeviceID()) {
		return fmt.Errorf("this device does not have grant access to %s", env)
	}
	return nil
}

func (r Repository) environmentDir(env string) string {
	return filepath.Join(r.Root, ".ghostable", "environments", environmentPathSegment(env))
}

func (r Repository) accessDir(env string) string {
	return filepath.Join(r.environmentDir(env), "access")
}

func (r Repository) valuesDir(env string) string {
	return filepath.Join(r.environmentDir(env), "values")
}

func (r Repository) valuePath(env string, key string) string {
	return filepath.Join(r.valuesDir(env), keyPathSegment(key)+".json")
}

func (r Repository) resolveProjectPath(file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(r.Root, file)
}

func (r Repository) resolveProjectOutputPath(file string) (string, error) {
	path := r.resolveProjectPath(file)
	absoluteRoot, err := filepath.Abs(r.Root)
	if err != nil {
		return "", err
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !pathIsInsideDirectory(absoluteRoot, absolutePath) {
		return "", fmt.Errorf("dotenv output path %q must stay inside the project", file)
	}

	realRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", err
	}
	realParent, err := filepath.EvalSymlinks(filepath.Dir(absolutePath))
	if err != nil {
		return "", err
	}
	if !pathIsInsideDirectory(realRoot, realParent) {
		return "", fmt.Errorf("dotenv output path %q must stay inside the project", file)
	}

	info, err := os.Lstat(absolutePath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refusing to write through symlinked dotenv output %q", file)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("dotenv output path %q must be a regular file", file)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	return absolutePath, nil
}

func pathIsInsideDirectory(root string, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (!filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func (r Repository) writeDevice(device domain.DeviceRecord) error {
	if err := os.MkdirAll(filepath.Join(r.Root, ".ghostable", "devices"), 0o755); err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(r.Root, ".ghostable", "devices", idFileName(device.ID)), device, 0o644)
}

func (r Repository) readDevice(deviceID string) (domain.DeviceRecord, error) {
	var device domain.DeviceRecord
	if err := readJSON(filepath.Join(r.Root, ".ghostable", "devices", idFileName(deviceID)), &device); err != nil {
		return device, err
	}
	if err := security.VerifyDeviceRecord(device); err != nil {
		return device, err
	}
	if device.ID != deviceID {
		return device, fmt.Errorf("device file for %s contains %s and does not match requested device", deviceID, device.ID)
	}
	return device, nil
}

func (r Repository) localDeviceRecord() (domain.DeviceRecord, error) {
	return r.readDevice(r.DeviceID())
}

func (r Repository) readPolicy() (domain.Policy, error) {
	policy, err := r.readPolicyFile()
	if err != nil {
		return policy, err
	}
	if err := r.verifyTrustedPolicy(policy); err != nil {
		return policy, err
	}
	return policy, nil
}

func (r Repository) readPolicyFile() (domain.Policy, error) {
	var policy domain.Policy
	err := readJSON(filepath.Join(r.Root, ".ghostable", "policy.json"), &policy)
	if err != nil {
		return policy, err
	}
	if policy.ProjectID != r.Manifest.ID {
		return policy, fmt.Errorf("policy project id does not match this project")
	}
	return policy, nil
}

func (r Repository) verifyTrustedPolicy(policy domain.Policy) error {
	signerDeviceID, err := r.verifyPolicySignature(policy)
	if err != nil {
		return err
	}
	if !r.trustsPolicySigner(signerDeviceID) {
		if !r.bootstrapPolicySignerForTrustedVersion(policy, signerDeviceID) {
			return fmt.Errorf("policy signer %s is not trusted by this local identity", signerDeviceID)
		}
	}
	if err := r.verifyPolicyVersion(policy); err != nil {
		return err
	}
	return nil
}

func (r Repository) verifyPolicySignature(policy domain.Policy) (string, error) {
	if policy.DeviceID == "" || policy.ClientSig == "" {
		return "", fmt.Errorf("policy is not signed")
	}
	if !contains(policy.Owners, policy.DeviceID) {
		return "", fmt.Errorf("policy was signed by non-owner device %s", policy.DeviceID)
	}
	device, err := r.readDevice(policy.DeviceID)
	if err != nil {
		return "", err
	}
	if !security.VerifyCanonical(policy, device.SigningKey.PublicKey, policy.ClientSig) {
		return "", fmt.Errorf("policy signature could not be verified")
	}
	return policy.DeviceID, nil
}

func (r Repository) trustsPolicySigner(deviceID string) bool {
	if deviceID == "" {
		return false
	}
	if contains(r.Identity.TrustedPolicySigners, deviceID) {
		return true
	}
	if r.canPersistTrustedPolicyVersion() {
		identity, err := r.identityStore.Load(r.Manifest.ID)
		if err == nil && identity.DeviceID == r.DeviceID() && contains(identity.TrustedPolicySigners, deviceID) {
			return true
		}
	}
	return len(r.Identity.TrustedPolicySigners) == 0 && deviceID == r.DeviceID()
}

func (r Repository) bootstrapPolicySignerForTrustedVersion(policy domain.Policy, signerDeviceID string) bool {
	if signerDeviceID == "" || !contains(policy.Owners, signerDeviceID) {
		return false
	}
	identity := r.Identity
	if r.canPersistTrustedPolicyVersion() {
		if storedIdentity, err := r.identityStore.Load(r.Manifest.ID); err == nil && storedIdentity.DeviceID == r.DeviceID() {
			identity = storedIdentity
		}
	}
	if len(identity.TrustedPolicySigners) > 0 || identity.TrustedPolicyVersion == 0 || identity.TrustedPolicyVersion != policy.Version {
		return false
	}
	identity.TrustedPolicySigners = trustedPolicySigners(signerDeviceID)
	if !r.canPersistTrustedPolicyVersion() {
		return true
	}
	return r.identityStore.Save(identity) == nil
}

func (r Repository) verifyPolicyVersion(policy domain.Policy) error {
	if policy.Version < 1 {
		return fmt.Errorf("policy version is required")
	}
	trustedVersion := r.trustedPolicyVersion()
	if trustedVersion > 0 && policy.Version < trustedVersion {
		return fmt.Errorf("policy version %d is older than trusted local policy version %d", policy.Version, trustedVersion)
	}
	if policy.Version > trustedVersion {
		return r.rememberTrustedPolicyVersion(policy.Version)
	}
	return nil
}

func (r Repository) trustedPolicyVersion() int {
	trustedVersion := r.Identity.TrustedPolicyVersion
	if !r.canPersistTrustedPolicyVersion() {
		return trustedVersion
	}
	identity, err := r.identityStore.Load(r.Manifest.ID)
	if err != nil || identity.DeviceID != r.DeviceID() {
		return trustedVersion
	}
	if identity.TrustedPolicyVersion > trustedVersion {
		return identity.TrustedPolicyVersion
	}
	return trustedVersion
}

func (r Repository) rememberTrustedPolicyVersion(version int) error {
	if !r.canPersistTrustedPolicyVersion() {
		return nil
	}
	identity := r.Identity
	if storedIdentity, err := r.identityStore.Load(r.Manifest.ID); err == nil {
		if storedIdentity.DeviceID != r.DeviceID() {
			return nil
		}
		identity = storedIdentity
	} else if !os.IsNotExist(err) {
		return err
	}
	if version <= identity.TrustedPolicyVersion {
		return nil
	}
	identity.TrustedPolicyVersion = version
	return r.identityStore.Save(identity)
}

func (r Repository) canPersistTrustedPolicyVersion() bool {
	return r.DeviceID() != "" && r.identityPath != "" && r.identityPath != automationCredentialEnvironmentVariable
}

func (r Repository) signAndWritePolicy(policy domain.Policy) error {
	if policy.Version < 1 {
		policy.Version = 1
	} else if policy.ClientSig != "" {
		policy.Version++
	}
	policy.DeviceID = r.DeviceID()
	policy.ClientSig = ""
	signature, err := security.SignCanonical(policy, r.Identity)
	if err != nil {
		return err
	}
	policy.ClientSig = signature
	if err := writeJSONAtomic(filepath.Join(r.Root, ".ghostable", "policy.json"), policy, 0o644); err != nil {
		return err
	}
	return r.rememberTrustedPolicyVersion(policy.Version)
}

func (r Repository) createEnvironmentKey(env string, device domain.DeviceRecord) error {
	envKey, grant, _, err := security.NewEnvironmentKey(r.Manifest.ID, env, r.Identity, device)
	if err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(r.environmentDir(env), "key.json"), envKey, 0o644); err != nil {
		return err
	}
	return r.writeAccessGrant(env, grant)
}

func (r Repository) readEnvironmentKey(env string) (domain.EnvironmentKeyRecord, error) {
	var record domain.EnvironmentKeyRecord
	if err := readJSON(filepath.Join(r.environmentDir(env), "key.json"), &record); err != nil {
		return record, err
	}
	if record.Schema != domain.EnvironmentKeySchema {
		return record, fmt.Errorf("environment key for %s has invalid schema", env)
	}
	if record.ProjectID != r.Manifest.ID || record.Environment != env {
		return record, fmt.Errorf("environment key for %s is not bound to this project", env)
	}
	return record, nil
}

func (r Repository) writeAccessGrant(env string, grant domain.AccessGrantRecord) error {
	if err := os.MkdirAll(r.accessDir(env), 0o755); err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(r.accessDir(env), idFileName(grant.DeviceID)), grant, 0o644)
}

func (r Repository) readAccessGrant(env string, deviceID string) (domain.AccessGrantRecord, error) {
	var grant domain.AccessGrantRecord
	if err := readJSON(filepath.Join(r.accessDir(env), idFileName(deviceID)), &grant); err != nil {
		return grant, err
	}
	if grant.Schema != domain.AccessGrantSchema {
		return grant, fmt.Errorf("access grant for %s/%s has invalid schema", env, deviceID)
	}
	if grant.ProjectID != r.Manifest.ID || grant.Environment != env || grant.DeviceID != deviceID {
		return grant, fmt.Errorf("access grant for %s/%s is not bound to this project", env, deviceID)
	}
	envKey, err := r.readEnvironmentKey(env)
	if err != nil {
		return grant, err
	}
	if grant.EnvKeyVersion != envKey.Version || grant.EnvKeyFingerprint != envKey.Fingerprint {
		return grant, fmt.Errorf("access grant for %s/%s does not match current environment key", env, deviceID)
	}
	signer, err := r.readDevice(grant.GrantedByDeviceID)
	if err != nil {
		return grant, err
	}
	if !security.VerifyCanonical(grant, signer.SigningKey.PublicKey, grant.ClientSig) {
		return grant, fmt.Errorf("access grant signature could not be verified")
	}
	if !security.VerifyEnvelope(grant.Envelope, signer.SigningKey.PublicKey) {
		return grant, fmt.Errorf("access grant envelope signature could not be verified")
	}
	policy, err := r.readPolicy()
	if err != nil {
		return grant, err
	}
	if !canGrant(policy, env, grant.GrantedByDeviceID) {
		return grant, fmt.Errorf("access grant for %s was signed by unauthorized device %s", env, grant.GrantedByDeviceID)
	}
	if !canRead(policy, env, grant.DeviceID) {
		return grant, fmt.Errorf("access grant target %s does not have read access to %s", grant.DeviceID, env)
	}
	return grant, nil
}

func (r Repository) VerifyPolicyMetadata() error {
	policy, err := r.readPolicyFile()
	if err != nil {
		return err
	}
	if _, err := r.verifyPolicySignature(policy); err != nil {
		return err
	}
	if policy.Version < 1 {
		return fmt.Errorf("policy version is required")
	}
	return nil
}

func (r Repository) VerifyDeviceMetadataFile(path string) error {
	var device domain.DeviceRecord
	if err := readJSON(r.resolveProjectPath(path), &device); err != nil {
		return err
	}
	return security.VerifyDeviceRecord(device)
}

func (r Repository) VerifyAccessGrantMetadataFile(path string) error {
	var grant domain.AccessGrantRecord
	if err := readJSON(r.resolveProjectPath(path), &grant); err != nil {
		return err
	}
	if grant.Schema != domain.AccessGrantSchema {
		return fmt.Errorf("access grant has invalid schema")
	}
	if grant.ProjectID != r.Manifest.ID {
		return fmt.Errorf("access grant is not bound to this project")
	}
	return r.verifyAccessGrantMetadata(grant)
}

func (r Repository) loadEnvironmentDEK(env string) ([]byte, error) {
	grant, err := r.readAccessGrant(env, r.DeviceID())
	if err != nil {
		return nil, err
	}
	return security.DecryptEnvelope(r.Identity, grant.Envelope)
}

func (r Repository) loadEnvironmentKey(env string) (domain.EnvironmentKeyRecord, []byte, error) {
	record, err := r.readEnvironmentKey(env)
	if err != nil {
		return record, nil, err
	}
	grant, err := r.readAccessGrant(env, r.DeviceID())
	if err != nil {
		return record, nil, err
	}
	if grant.EnvKeyFingerprint != record.Fingerprint {
		return record, nil, fmt.Errorf("access grant for %s does not match environment key", env)
	}
	policy, err := r.readPolicy()
	if err != nil {
		return record, nil, err
	}
	if !canRead(policy, env, r.DeviceID()) {
		return record, nil, fmt.Errorf("this device does not have read access to %s", env)
	}
	dek, err := security.DecryptEnvelope(r.Identity, grant.Envelope)
	if err != nil {
		return record, nil, err
	}
	envKey, err := security.DecryptXChaCha(dek, record.EncryptedKey, nil)
	if err != nil {
		return record, nil, err
	}
	if security.Fingerprint(envKey) != record.Fingerprint {
		return record, nil, fmt.Errorf("environment key fingerprint mismatch for %s", env)
	}
	return record, envKey, nil
}

func (r Repository) verifyValueRecord(record domain.ValueRecord) error {
	if record.Schema != domain.ValueSchema {
		return nil
	}
	if record.ProjectID != r.Manifest.ID ||
		record.Environment != record.Secret.Env ||
		record.Key != record.Secret.Name ||
		record.Secret.AAD.Org != domain.GhostableOrgScope ||
		record.Secret.AAD.Project != record.ProjectID ||
		record.Secret.AAD.Env != record.Environment ||
		record.Secret.AAD.Name != record.Key {
		return fmt.Errorf("value %s is not bound to its Ghostable storage path", record.Key)
	}
	device, err := r.readDevice(record.UpdatedByDeviceID)
	if err != nil {
		return err
	}
	policy, err := r.readPolicy()
	if err != nil {
		return err
	}
	if !canWrite(policy, record.Environment, record.UpdatedByDeviceID) {
		return fmt.Errorf("value %s was signed by a device without write access", record.Key)
	}
	envKey, err := r.readEnvironmentKey(record.Environment)
	if err != nil {
		return err
	}
	if record.Secret.EnvKekVersion != envKey.Version || record.Secret.EnvKekFingerprint != envKey.Fingerprint {
		return fmt.Errorf("value %s does not match current environment key", record.Key)
	}
	if !security.VerifySecretBody(record.Secret, device.SigningKey.PublicKey, record.Secret.ClientSig) {
		return fmt.Errorf("value %s has an invalid device signature", record.Key)
	}
	return nil
}

func (r Repository) verifyValueRecordMetadata(record domain.ValueRecord) error {
	if record.Schema != domain.ValueSchema {
		return nil
	}
	if record.ProjectID != r.Manifest.ID ||
		record.Environment != record.Secret.Env ||
		record.Key != record.Secret.Name ||
		record.Secret.AAD.Org != domain.GhostableOrgScope ||
		record.Secret.AAD.Project != record.ProjectID ||
		record.Secret.AAD.Env != record.Environment ||
		record.Secret.AAD.Name != record.Key {
		return fmt.Errorf("value %s is not bound to its Ghostable storage path", record.Key)
	}
	device, err := r.readDevice(record.UpdatedByDeviceID)
	if err != nil {
		return err
	}
	policy, err := r.readVerifiedPolicyMetadata()
	if err != nil {
		return err
	}
	if !canWrite(policy, record.Environment, record.UpdatedByDeviceID) {
		return fmt.Errorf("value %s was signed by a device without write access", record.Key)
	}
	envKey, err := r.readEnvironmentKey(record.Environment)
	if err != nil {
		return err
	}
	if record.Secret.EnvKekVersion != envKey.Version || record.Secret.EnvKekFingerprint != envKey.Fingerprint {
		return fmt.Errorf("value %s does not match current environment key", record.Key)
	}
	if !security.VerifySecretBody(record.Secret, device.SigningKey.PublicKey, record.Secret.ClientSig) {
		return fmt.Errorf("value %s has an invalid device signature", record.Key)
	}
	return nil
}

func (r Repository) verifyAccessGrantMetadata(grant domain.AccessGrantRecord) error {
	if grant.Environment == "" || grant.DeviceID == "" {
		return fmt.Errorf("access grant is missing its environment or device")
	}
	envKey, err := r.readEnvironmentKey(grant.Environment)
	if err != nil {
		return err
	}
	if grant.EnvKeyVersion != envKey.Version || grant.EnvKeyFingerprint != envKey.Fingerprint {
		return fmt.Errorf("access grant for %s/%s does not match current environment key", grant.Environment, grant.DeviceID)
	}
	signer, err := r.readDevice(grant.GrantedByDeviceID)
	if err != nil {
		return err
	}
	if !security.VerifyCanonical(grant, signer.SigningKey.PublicKey, grant.ClientSig) {
		return fmt.Errorf("access grant signature could not be verified")
	}
	if !security.VerifyEnvelope(grant.Envelope, signer.SigningKey.PublicKey) {
		return fmt.Errorf("access grant envelope signature could not be verified")
	}
	policy, err := r.readVerifiedPolicyMetadata()
	if err != nil {
		return err
	}
	if !canGrant(policy, grant.Environment, grant.GrantedByDeviceID) {
		return fmt.Errorf("access grant for %s was signed by unauthorized device %s", grant.Environment, grant.GrantedByDeviceID)
	}
	if !canRead(policy, grant.Environment, grant.DeviceID) {
		return fmt.Errorf("access grant target %s does not have read access to %s", grant.DeviceID, grant.Environment)
	}
	return nil
}

func (r Repository) readVerifiedPolicyMetadata() (domain.Policy, error) {
	policy, err := r.readPolicyFile()
	if err != nil {
		return policy, err
	}
	if _, err := r.verifyPolicySignature(policy); err != nil {
		return policy, err
	}
	if policy.Version < 1 {
		return policy, fmt.Errorf("policy version is required")
	}
	return policy, nil
}

func (r Repository) writeLayout(env string, keys map[string]int) error {
	layout := domain.Layout{
		Schema:      domain.LayoutSchema,
		ProjectID:   r.Manifest.ID,
		Environment: env,
		UpdatedAt:   security.Now(),
		Keys:        keys,
	}
	return writeJSONAtomic(filepath.Join(r.environmentDir(env), "layout.json"), layout, 0o644)
}

func (r Repository) readLayout(env string) (domain.Layout, error) {
	var layout domain.Layout
	err := readJSON(filepath.Join(r.environmentDir(env), "layout.json"), &layout)
	if errors.Is(err, os.ErrNotExist) {
		layout = domain.Layout{
			Schema:      domain.LayoutSchema,
			ProjectID:   r.Manifest.ID,
			Environment: env,
			Keys:        map[string]int{},
		}
		return layout, nil
	}
	return layout, err
}

func (r Repository) addLayoutKey(env string, key string) error {
	layout, err := r.readLayout(env)
	if err != nil {
		return err
	}
	if layout.Keys == nil {
		layout.Keys = map[string]int{}
	}
	if _, ok := layout.Keys[key]; ok {
		return nil
	}
	layout.Keys[key] = len(layout.Keys) + 1
	return r.writeLayout(env, layout.Keys)
}

func (r Repository) removeLayoutKey(env string, key string) error {
	layout, err := r.readLayout(env)
	if err != nil {
		return err
	}
	delete(layout.Keys, key)
	return r.writeLayout(env, layout.Keys)
}

func (r Repository) layoutOrder(env string) []string {
	layout, err := r.readLayout(env)
	if err != nil {
		return nil
	}
	type item struct {
		key  string
		rank int
	}
	items := make([]item, 0, len(layout.Keys))
	for key, rank := range layout.Keys {
		items = append(items, item{key: key, rank: rank})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].rank == items[j].rank {
			return items[i].key < items[j].key
		}
		return items[i].rank < items[j].rank
	})
	order := make([]string, 0, len(items))
	for _, item := range items {
		order = append(order, item.key)
	}
	return order
}

func (r Repository) recordEvent(action string, env string, key string, details map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Join(r.Root, ".ghostable", "events"), 0o755); err != nil {
		return err
	}
	now := time.Now().UTC()
	event := domain.Event{
		Schema:         domain.EventSchema,
		Action:         action,
		ProjectID:      r.Manifest.ID,
		Environment:    env,
		Key:            key,
		DeviceID:       r.DeviceID(),
		GitHead:        gitHead(r.Root),
		OccurredAt:     now.Format(time.RFC3339Nano),
		Details:        details,
		SignerDeviceID: r.DeviceID(),
	}
	signature, err := security.SignCanonical(event, r.Identity)
	if err != nil {
		return err
	}
	event.ClientSig = signature

	nameParts := []string{now.Format("20060102T150405000Z"), action}
	if env != "" {
		nameParts = append(nameParts, env)
	}
	if key != "" {
		nameParts = append(nameParts, keyHash(key)[:12])
	}
	filename := safeFileName(strings.Join(nameParts, "-")) + ".json"
	return writeJSONAtomic(filepath.Join(r.Root, ".ghostable", "events", filename), event, 0o644)
}

func writeManifest(path string, project domain.ProjectManifest) error {
	if err := ensureGhostableStatePath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".ghostable.yaml.*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := manifest.Write(temp, project); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, 0o644); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func writeJSONAtomic(path string, value interface{}, mode os.FileMode) error {
	if err := ensureGhostableStatePath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func ensureGhostableStatePath(path string) error {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for _, statePath := range ghostableStateComponentPaths(absolutePath) {
		info, err := os.Lstat(statePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to use symlinked Ghostable state path %s", statePath)
		}
	}
	return nil
}

func ghostableStateComponentPaths(absolutePath string) []string {
	cleanPath := filepath.Clean(absolutePath)
	volume := filepath.VolumeName(cleanPath)
	pathWithoutVolume := strings.TrimPrefix(cleanPath, volume)
	isAbsolute := strings.HasPrefix(pathWithoutVolume, string(os.PathSeparator))
	parts := strings.Split(strings.Trim(pathWithoutVolume, string(os.PathSeparator)), string(os.PathSeparator))

	current := volume
	if isAbsolute {
		current += string(os.PathSeparator)
	}
	paths := []string{}
	inGhostableState := false
	for _, part := range parts {
		if part == "" {
			continue
		}
		if current == "" || strings.HasSuffix(current, string(os.PathSeparator)) {
			current += part
		} else {
			current = filepath.Join(current, part)
		}
		if part == ".ghostable" {
			inGhostableState = true
		}
		if inGhostableState {
			paths = append(paths, current)
		}
	}
	return paths
}

func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, mode); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func readJSON(path string, value interface{}) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, value)
}

func validateName(label string, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is required", label)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%s name %q may only contain letters, numbers, dots, dashes, and underscores", label, name)
	}
	return nil
}

func validateEnvironmentName(name string) error {
	if err := validateName("environment", name); err != nil {
		return err
	}
	return validateEnvironmentPathSegment(name)
}

func validateEnvironmentPathSegment(name string) error {
	segment := environmentPathSegment(name)
	if segment == "." || segment == ".." || strings.Trim(segment, ".") == "" {
		return fmt.Errorf("environment name %q resolves to unsafe path segment %q", name, segment)
	}
	return nil
}

func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("variable key is required")
	}
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("variable key %q cannot be written to a dotenv file", key)
	}
	return nil
}

func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func randomID() (string, error) {
	var bytes [16]byte
	if _, err := io.ReadFull(rand.Reader, bytes[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]), nil
}

func randomDeviceID() (string, error) {
	var bytes [16]byte
	if _, err := io.ReadFull(rand.Reader, bytes[:]); err != nil {
		return "", err
	}
	return "dev_" + hex.EncodeToString(bytes[:]), nil
}

func randomRequestID() (string, error) {
	var bytes [16]byte
	if _, err := io.ReadFull(rand.Reader, bytes[:]); err != nil {
		return "", err
	}
	return "req_" + hex.EncodeToString(bytes[:]), nil
}

func idFileName(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id)) + ".json"
}

func environmentPathSegment(envName string) string {
	normalized := strings.ToLower(envName)
	slug := regexp.MustCompile(`[^a-z0-9._-]+`).ReplaceAllString(normalized, "-")
	slug = strings.Trim(slug, "-")
	slug = regexp.MustCompile(`-{2,}`).ReplaceAllString(slug, "-")
	if slug != "" {
		return slug
	}
	return base64.RawURLEncoding.EncodeToString([]byte(envName))
}

func keyPathSegment(key string) string {
	trimmed := strings.TrimSpace(key)
	slug := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(trimmed, "-")
	slug = strings.Trim(slug, "-")
	slug = regexp.MustCompile(`-{2,}`).ReplaceAllString(slug, "-")
	if slug != "" && slug != "." && slug != ".." && slug == trimmed {
		return slug
	}
	if slug != "" && slug != "." && slug != ".." {
		return slug + "--" + base64.RawURLEncoding.EncodeToString([]byte(key))
	}
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func safeFileName(value string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	return replacer.Replace(value)
}

func platformLabel() string {
	return strings.Join([]string{runtimeGOOS(), runtimeGOARCH()}, "-")
}

func runtimeGOOS() string {
	if value := strings.TrimSpace(strings.ToLower(os.Getenv("GOOS_FOR_TEST"))); value != "" {
		return value
	}
	return runtime.GOOS
}

func runtimeGOARCH() string {
	if value := strings.TrimSpace(strings.ToLower(os.Getenv("GOARCH_FOR_TEST"))); value != "" {
		return value
	}
	return runtime.GOARCH
}

func environmentType(name string) string {
	switch strings.ToLower(name) {
	case "local", "default":
		return "local"
	case "dev", "development":
		return "development"
	case "preview":
		return "preview"
	case "stage", "staging":
		return "staging"
	case "prod", "production":
		return "production"
	default:
		return "custom"
	}
}

func gitHead(root string) string {
	command := exec.Command("git", "rev-parse", "--short", "HEAD")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func valueForOutput(value string, show bool) string {
	if show {
		return value
	}
	return ""
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result[value] = true
		}
	}
	return result
}

func sortedSet(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func trustedPolicySigners(deviceIDs ...string) []string {
	signers := make([]string, 0, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		if strings.TrimSpace(deviceID) == "" {
			continue
		}
		signers = appendUnique(signers, deviceID)
	}
	sort.Strings(signers)
	return signers
}

func removeValue(values []string, value string) []string {
	next := values[:0]
	for _, existing := range values {
		if existing != value {
			next = append(next, existing)
		}
	}
	return next
}

func contains(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func canRead(policy domain.Policy, env string, deviceID string) bool {
	if policyDeviceRevoked(policy, deviceID) {
		return false
	}
	if contains(policy.Owners, deviceID) {
		return true
	}
	envPolicy := policy.Environments[env]
	return contains(envPolicy.Readers, deviceID) ||
		contains(envPolicy.Writers, deviceID) ||
		contains(envPolicy.Grantors, deviceID)
}

func canWrite(policy domain.Policy, env string, deviceID string) bool {
	if policyDeviceRevoked(policy, deviceID) {
		return false
	}
	if contains(policy.Owners, deviceID) {
		return true
	}
	envPolicy := policy.Environments[env]
	return contains(envPolicy.Writers, deviceID)
}

func canGrant(policy domain.Policy, env string, deviceID string) bool {
	if policyDeviceRevoked(policy, deviceID) {
		return false
	}
	if contains(policy.Owners, deviceID) {
		return true
	}
	envPolicy := policy.Environments[env]
	return contains(envPolicy.Grantors, deviceID)
}

func policyDeviceRevoked(policy domain.Policy, deviceID string) bool {
	if policy.Revoked == nil {
		return false
	}
	revocation, ok := policy.Revoked[deviceID]
	return ok && revocation.DeviceID == deviceID
}
