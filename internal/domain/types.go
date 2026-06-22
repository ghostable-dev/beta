package domain

import "time"

const (
	ProjectSchema        = "ghostable.project.v1"
	DeviceSchema         = "ghostable.device.v1"
	AccessRequestSchema  = "ghostable.access-request.v1"
	EnvironmentKeySchema = "ghostable.environment-key.v1"
	AccessGrantSchema    = "ghostable.access-grant.v1"
	PolicySchema         = "ghostable.policy.v1"
	LayoutSchema         = "ghostable.layout.v1"
	ValueSchema          = "ghostable.value.v1"
	LegacyValueSchema    = "ghostable.value.go.v1"
	EventSchema          = "ghostable.event.v1"
	LocalKeySchema       = "ghostable.local-key.go.v1"
	LocalIdentitySchema  = "ghostable.local-identity.go.v1"
	DefaultEnvName       = "default"
	DefaultEnvType       = "local"
	DefaultActivity      = "minimal"
	DefaultDeviceName    = "Local device"
	GhostableOrgScope    = "ghostable"
)

type ProjectManifest struct {
	Schema         string
	ID             string
	Name           string
	Language       string
	Framework      string
	PackageManager string
	DeployTarget   string
	ActivityMode   string
	AuditEnvs      []string
	Environments   map[string]Environment
	ScanLevel      string
	ScanIgnores    []string
}

type Environment struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type PublicKeyRecord struct {
	Alg         string `json:"alg"`
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
}

type DeviceRecord struct {
	Schema        string          `json:"schema"`
	ID            string          `json:"id"`
	Name          string          `json:"name,omitempty"`
	Platform      string          `json:"platform,omitempty"`
	Status        string          `json:"status"`
	CreatedAt     string          `json:"createdAt"`
	SigningKey    PublicKeyRecord `json:"signingKey"`
	EncryptionKey PublicKeyRecord `json:"encryptionKey"`
	DeviceID      string          `json:"device_id,omitempty"`
	ClientSig     string          `json:"client_sig,omitempty"`
}

type AccessRequestFile struct {
	Schema    string               `json:"schema"`
	ProjectID string               `json:"projectId"`
	Request   AccessRequest        `json:"request"`
	Review    *AccessRequestReview `json:"review,omitempty"`
}

type AccessRequest struct {
	Schema         string `json:"schema"`
	ProjectID      string `json:"projectId"`
	ID             string `json:"id"`
	DeviceID       string `json:"deviceId"`
	Environment    string `json:"environment"`
	Role           string `json:"role"`
	Reason         string `json:"reason,omitempty"`
	CreatedAt      string `json:"createdAt"`
	SignerDeviceID string `json:"device_id,omitempty"`
	ClientSig      string `json:"client_sig,omitempty"`
}

type AccessRequestReview struct {
	Schema             string `json:"schema"`
	ProjectID          string `json:"projectId"`
	RequestID          string `json:"requestId"`
	Status             string `json:"status"`
	ReviewedByDeviceID string `json:"reviewedByDeviceId"`
	ReviewedAt         string `json:"reviewedAt"`
	Reason             string `json:"reason,omitempty"`
	SignerDeviceID     string `json:"device_id,omitempty"`
	ClientSig          string `json:"client_sig,omitempty"`
}

type LocalKeyRecord struct {
	Schema    string    `json:"schema"`
	ProjectID string    `json:"projectId"`
	DeviceID  string    `json:"deviceId"`
	KeyB64    string    `json:"keyB64"`
	CreatedAt time.Time `json:"createdAt"`
}

type LocalIdentityRecord struct {
	Schema                  string   `json:"schema"`
	ProjectID               string   `json:"projectId"`
	DeviceID                string   `json:"deviceId"`
	Name                    string   `json:"name,omitempty"`
	Platform                string   `json:"platform,omitempty"`
	CreatedAt               string   `json:"createdAt"`
	SigningPublicKeyB64     string   `json:"signingPublicKeyB64"`
	SigningPrivateKeyB64    string   `json:"signingPrivateKeyB64"`
	EncryptionPublicKeyB64  string   `json:"encryptionPublicKeyB64"`
	EncryptionPrivateKeyB64 string   `json:"encryptionPrivateKeyB64"`
	TrustedPolicySigners    []string `json:"trustedPolicySigners,omitempty"`
	TrustedPolicyVersion    int      `json:"trustedPolicyVersion,omitempty"`
}

type Policy struct {
	Schema       string                       `json:"schema"`
	ProjectID    string                       `json:"projectId"`
	Version      int                          `json:"version"`
	UpdatedAt    string                       `json:"updatedAt"`
	Owners       []string                     `json:"owners"`
	Environments map[string]EnvironmentPolicy `json:"environments"`
	Revoked      map[string]DeviceRevocation  `json:"revoked,omitempty"`
	DeviceID     string                       `json:"device_id,omitempty"`
	ClientSig    string                       `json:"client_sig,omitempty"`
}

type DeviceRevocation struct {
	DeviceID          string `json:"deviceId"`
	RevokedAt         string `json:"revokedAt"`
	RevokedByDeviceID string `json:"revokedByDeviceId"`
}

type EnvironmentPolicy struct {
	Readers  []string `json:"readers"`
	Writers  []string `json:"writers"`
	Grantors []string `json:"grantors"`
}

type Layout struct {
	Schema      string         `json:"schema"`
	ProjectID   string         `json:"projectId"`
	Environment string         `json:"environment"`
	UpdatedAt   string         `json:"updatedAt"`
	Keys        map[string]int `json:"keys"`
}

type EncryptedPayload struct {
	Alg           string `json:"alg"`
	NonceB64      string `json:"nonceB64"`
	CiphertextB64 string `json:"ciphertextB64"`
}

type EnvironmentKeyRecord struct {
	Schema            string           `json:"schema"`
	ProjectID         string           `json:"projectId"`
	Environment       string           `json:"environment"`
	Version           int              `json:"version"`
	Fingerprint       string           `json:"fingerprint"`
	EncryptedKey      EncryptedPayload `json:"encryptedKey"`
	CreatedByDeviceID string           `json:"createdByDeviceId"`
	CreatedAt         string           `json:"createdAt"`
	UpdatedAt         string           `json:"updatedAt"`
}

type AccessGrantRecord struct {
	Schema            string       `json:"schema"`
	ProjectID         string       `json:"projectId"`
	Environment       string       `json:"environment"`
	DeviceID          string       `json:"deviceId"`
	GrantedByDeviceID string       `json:"grantedByDeviceId"`
	GrantedAt         string       `json:"grantedAt"`
	EnvKeyVersion     int          `json:"envKeyVersion"`
	EnvKeyFingerprint string       `json:"envKeyFingerprint"`
	Envelope          EnvelopeJSON `json:"envelope"`
	SignerDeviceID    string       `json:"device_id,omitempty"`
	ClientSig         string       `json:"client_sig,omitempty"`
}

type EnvelopeJSON struct {
	ID                     string            `json:"id"`
	Version                string            `json:"version"`
	Alg                    string            `json:"alg,omitempty"`
	ToDevicePublicKey      string            `json:"to_device_public_key"`
	FromEphemeralPublicKey string            `json:"from_ephemeral_public_key"`
	NonceB64               string            `json:"nonce_b64"`
	CiphertextB64          string            `json:"ciphertext_b64"`
	CreatedAt              string            `json:"created_at"`
	Meta                   map[string]string `json:"meta,omitempty"`
	AADB64                 string            `json:"aad_b64,omitempty"`
	SenderKID              string            `json:"sender_kid,omitempty"`
	SignatureB64           string            `json:"signature_b64,omitempty"`
}

type ValueRecord struct {
	Schema            string            `json:"schema"`
	ProjectID         string            `json:"projectId"`
	Environment       string            `json:"environment"`
	Key               string            `json:"key"`
	Version           int               `json:"version"`
	UpdatedAt         string            `json:"updatedAt"`
	UpdatedByDeviceID string            `json:"updatedByDeviceId"`
	Secret            SecretBody        `json:"secret"`
	KeyHash           string            `json:"keyHash,omitempty"`
	EncryptedValue    EncryptedPayload  `json:"encryptedValue,omitempty"`
	EncryptedNote     *EncryptedPayload `json:"encryptedNote,omitempty"`
	Sensitive         bool              `json:"sensitive,omitempty"`
	CreatedByDeviceID string            `json:"createdByDeviceId,omitempty"`
	CreatedAtLegacy   time.Time         `json:"createdAt,omitempty"`
	UpdatedAtLegacy   time.Time         `json:"-"`
}

type SecretBody struct {
	Name              string                 `json:"name"`
	Env               string                 `json:"env"`
	Ciphertext        string                 `json:"ciphertext"`
	Nonce             string                 `json:"nonce"`
	Alg               string                 `json:"alg"`
	AAD               SecretAAD              `json:"aad"`
	Claims            map[string]string      `json:"claims"`
	IfVersion         *int                   `json:"if_version,omitempty"`
	EnvKekVersion     int                    `json:"env_kek_version,omitempty"`
	EnvKekFingerprint string                 `json:"env_kek_fingerprint,omitempty"`
	LineBytes         int                    `json:"line_bytes,omitempty"`
	IsVaporSecret     *bool                  `json:"is_vapor_secret,omitempty"`
	IsCommented       bool                   `json:"is_commented,omitempty"`
	ChangeNote        map[string]interface{} `json:"change_note,omitempty"`
	ClientSig         string                 `json:"client_sig,omitempty"`
}

type SecretAAD struct {
	Org     string `json:"org"`
	Project string `json:"project"`
	Env     string `json:"env"`
	Name    string `json:"name"`
}

type LegacyValueRecord struct {
	Schema            string            `json:"schema"`
	ProjectID         string            `json:"projectId"`
	Environment       string            `json:"environment"`
	Key               string            `json:"key"`
	KeyHash           string            `json:"keyHash"`
	EncryptedValue    EncryptedPayload  `json:"encryptedValue"`
	EncryptedNote     *EncryptedPayload `json:"encryptedNote,omitempty"`
	Sensitive         bool              `json:"sensitive"`
	Version           int               `json:"version"`
	CreatedByDeviceID string            `json:"createdByDeviceId"`
	UpdatedByDeviceID string            `json:"updatedByDeviceId"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
}

type Variable struct {
	Key         string `json:"key"`
	Value       string `json:"value,omitempty"`
	HasValue    bool   `json:"hasValue"`
	Sensitive   bool   `json:"sensitive"`
	Commented   bool   `json:"commented,omitempty"`
	VaporSecret bool   `json:"vaporSecret,omitempty"`
	Note        string `json:"note,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

type Event struct {
	Schema         string                 `json:"schema"`
	Action         string                 `json:"action"`
	ProjectID      string                 `json:"projectId"`
	Environment    string                 `json:"environment,omitempty"`
	Key            string                 `json:"key,omitempty"`
	DeviceID       string                 `json:"deviceId,omitempty"`
	GitHead        string                 `json:"gitHead,omitempty"`
	OccurredAt     string                 `json:"occurredAt"`
	Details        map[string]interface{} `json:"details,omitempty"`
	SignerDeviceID string                 `json:"device_id,omitempty"`
	ClientSig      string                 `json:"client_sig,omitempty"`
}

type EnvDiff struct {
	Environment       string      `json:"environment"`
	SourceEnvironment string      `json:"sourceEnvironment,omitempty"`
	TargetEnvironment string      `json:"targetEnvironment,omitempty"`
	File              string      `json:"file"`
	Added             []DiffEntry `json:"added"`
	Changed           []DiffEntry `json:"changed"`
	Removed           []DiffEntry `json:"removed"`
	Unchanged         []string    `json:"unchanged"`
	Summary           DiffSummary `json:"summary"`
}

type DiffEntry struct {
	Key         string `json:"key"`
	LocalValue  string `json:"localValue,omitempty"`
	StoredValue string `json:"storedValue,omitempty"`
	SourceValue string `json:"sourceValue,omitempty"`
	TargetValue string `json:"targetValue,omitempty"`
}

type DiffSummary struct {
	Added     int `json:"added"`
	Changed   int `json:"changed"`
	Removed   int `json:"removed"`
	Unchanged int `json:"unchanged"`
}
