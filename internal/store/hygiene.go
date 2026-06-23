package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/security"
)

type CreateSuppressionInput struct {
	Code        string
	Environment string
	Key         string
	Reason      string
	ExpiresAt   string
}

type SuppressionEntry struct {
	Suppression    domain.SuppressionRecord `json:"suppression"`
	File           string                   `json:"file"`
	Expired        bool                     `json:"expired"`
	ValidSignature bool                     `json:"validSignature"`
	SignatureError string                   `json:"signatureError,omitempty"`
}

type EnvironmentKeyMetadata struct {
	Environment string `json:"environment"`
	Version     int    `json:"version"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type EnvironmentKeyRotationResult struct {
	Environment         string   `json:"environment"`
	PreviousVersion     int      `json:"previousVersion"`
	NextVersion         int      `json:"nextVersion"`
	PreviousFingerprint string   `json:"previousFingerprint"`
	NextFingerprint     string   `json:"nextFingerprint"`
	Files               []string `json:"files"`
	Rotated             bool     `json:"rotated"`
}

func (r Repository) CreateSuppression(input CreateSuppressionInput) (SuppressionEntry, error) {
	code := strings.TrimSpace(input.Code)
	if code == "" {
		return SuppressionEntry{}, fmt.Errorf("suppression code is required")
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return SuppressionEntry{}, fmt.Errorf("suppression reason is required")
	}
	env := strings.TrimSpace(input.Environment)
	if env != "" {
		if err := r.requireEnvironment(env); err != nil {
			return SuppressionEntry{}, err
		}
	}
	key := strings.TrimSpace(input.Key)
	if key != "" {
		if err := validateKey(key); err != nil {
			return SuppressionEntry{}, err
		}
	}
	expiresAt := strings.TrimSpace(input.ExpiresAt)
	if expiresAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, expiresAt); err != nil {
			return SuppressionEntry{}, fmt.Errorf("expires-at must be RFC3339: %w", err)
		}
	}

	id, err := randomID()
	if err != nil {
		return SuppressionEntry{}, err
	}
	record := domain.SuppressionRecord{
		Schema:            domain.SuppressionSchema,
		ProjectID:         r.Manifest.ID,
		ID:                id,
		Code:              code,
		Environment:       env,
		Key:               key,
		Reason:            reason,
		CreatedByDeviceID: r.DeviceID(),
		CreatedAt:         security.Now(),
		ExpiresAt:         expiresAt,
		SignerDeviceID:    r.DeviceID(),
	}
	signature, err := security.SignCanonical(record, r.Identity)
	if err != nil {
		return SuppressionEntry{}, err
	}
	record.ClientSig = signature

	file := filepath.Join(".ghostable", "suppressions", idFileName(record.ID))
	if err := writeJSONAtomic(filepath.Join(r.Root, file), record, 0o644); err != nil {
		return SuppressionEntry{}, err
	}
	if err := r.recordEvent("hygiene.suppression.created", env, key, map[string]interface{}{
		"code":   code,
		"reason": reason,
	}); err != nil {
		return SuppressionEntry{}, err
	}
	return SuppressionEntry{
		Suppression:    record,
		File:           filepath.ToSlash(file),
		ValidSignature: true,
	}, nil
}

func (r Repository) Suppressions(now time.Time) ([]SuppressionEntry, error) {
	dir := filepath.Join(r.Root, ".ghostable", "suppressions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SuppressionEntry{}, nil
		}
		return nil, err
	}

	result := []SuppressionEntry{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var record domain.SuppressionRecord
		item := SuppressionEntry{
			File: filepath.ToSlash(filepath.Join(".ghostable", "suppressions", entry.Name())),
		}
		if err := readJSON(path, &record); err != nil {
			item.SignatureError = err.Error()
			result = append(result, item)
			continue
		}
		item.Suppression = record
		if err := r.verifySuppression(record); err != nil {
			item.SignatureError = err.Error()
		} else {
			item.ValidSignature = true
		}
		item.Expired = suppressionExpired(record, now)
		result = append(result, item)
	}
	return result, nil
}

func (r Repository) verifySuppression(record domain.SuppressionRecord) error {
	if record.Schema != domain.SuppressionSchema {
		return fmt.Errorf("suppression has invalid schema")
	}
	if record.ProjectID != r.Manifest.ID {
		return fmt.Errorf("suppression project id does not match this project")
	}
	if record.ID == "" || record.Code == "" || record.Reason == "" {
		return fmt.Errorf("suppression is missing required fields")
	}
	if record.ClientSig == "" {
		return fmt.Errorf("suppression is missing a signature")
	}
	signerID := record.SignerDeviceID
	if signerID == "" {
		signerID = record.CreatedByDeviceID
	}
	if signerID == "" {
		return fmt.Errorf("suppression is missing a signer device")
	}
	device, err := r.readDevice(signerID)
	if err != nil {
		return err
	}
	if !security.VerifyCanonical(record, device.SigningKey.PublicKey, record.ClientSig) {
		return fmt.Errorf("suppression signature could not be verified")
	}
	return nil
}

func suppressionExpired(record domain.SuppressionRecord, now time.Time) bool {
	if strings.TrimSpace(record.ExpiresAt) == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	if err != nil {
		return true
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !expiresAt.After(now)
}

func (r Repository) ReadEnvironmentKeyMetadata(env string) (EnvironmentKeyMetadata, error) {
	if err := r.requireEnvironment(env); err != nil {
		return EnvironmentKeyMetadata{}, err
	}
	record, err := r.readEnvironmentKey(env)
	if err != nil {
		return EnvironmentKeyMetadata{}, err
	}
	return EnvironmentKeyMetadata{
		Environment: record.Environment,
		Version:     record.Version,
		Fingerprint: record.Fingerprint,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
	}, nil
}

func (r Repository) RotateEnvironmentKey(env string, reason string) (EnvironmentKeyRotationResult, error) {
	if err := r.requireEnvironment(env); err != nil {
		return EnvironmentKeyRotationResult{}, err
	}
	if err := r.requireWrite(env); err != nil {
		return EnvironmentKeyRotationResult{}, err
	}
	if err := r.requireGrant(env); err != nil {
		return EnvironmentKeyRotationResult{}, err
	}

	before, err := r.readEnvironmentKey(env)
	if err != nil {
		return EnvironmentKeyRotationResult{}, err
	}
	policy, err := r.readPolicy()
	if err != nil {
		return EnvironmentKeyRotationResult{}, err
	}
	files, err := r.rotateEnvironmentKey(env, policy)
	if err != nil {
		return EnvironmentKeyRotationResult{}, err
	}
	after, err := r.readEnvironmentKey(env)
	if err != nil {
		return EnvironmentKeyRotationResult{}, err
	}
	if err := r.recordEvent("environment.key_rotated", env, "", map[string]interface{}{
		"reason":              reason,
		"previousVersion":     before.Version,
		"nextVersion":         after.Version,
		"previousFingerprint": before.Fingerprint,
		"nextFingerprint":     after.Fingerprint,
	}); err != nil {
		return EnvironmentKeyRotationResult{}, err
	}
	return EnvironmentKeyRotationResult{
		Environment:         env,
		PreviousVersion:     before.Version,
		NextVersion:         after.Version,
		PreviousFingerprint: before.Fingerprint,
		NextFingerprint:     after.Fingerprint,
		Files:               files,
		Rotated:             true,
	}, nil
}
