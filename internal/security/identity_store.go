package security

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ghostable-dev/beta/internal/domain"
)

type IdentityStore struct {
	fileRoot string
}

func NewIdentityStore() (IdentityStore, error) {
	if override := strings.TrimSpace(os.Getenv("GHOSTABLE_KEYSTORE")); override != "" {
		return IdentityStore{fileRoot: override}, nil
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return IdentityStore{}, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return IdentityStore{}, err
	}
	return IdentityStore{fileRoot: filepath.Join(configDir, "ghostable", "identities")}, nil
}

func (s IdentityStore) Load(projectID string) (domain.LocalIdentityRecord, error) {
	if s.usesKeychain() {
		return s.loadKeychain(projectID)
	}
	if s.usesWindowsCredentialManager() {
		return s.loadWindowsCredential(projectID)
	}
	return s.loadFile(projectID)
}

func (s IdentityStore) Save(identity domain.LocalIdentityRecord) error {
	if s.usesKeychain() {
		return s.saveKeychain(identity)
	}
	if s.usesWindowsCredentialManager() {
		return s.saveWindowsCredential(identity)
	}
	return s.saveFile(identity)
}

func (s IdentityStore) Delete(projectID string) error {
	if s.usesKeychain() {
		if err := s.deleteKeychain(projectID); err != nil {
			return err
		}
		return s.UnregisterProjectIdentity(projectID)
	}
	if s.usesWindowsCredentialManager() {
		if err := s.deleteWindowsCredential(projectID); err != nil {
			return err
		}
		return s.UnregisterProjectIdentity(projectID)
	}
	if err := os.Remove(s.filePath(projectID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return s.UnregisterProjectIdentity(projectID)
}

func (s IdentityStore) Path(projectID string) string {
	if s.usesKeychain() {
		return "macOS Keychain: " + keychainService(projectID)
	}
	if s.usesWindowsCredentialManager() {
		return "Windows Credential Manager: " + windowsCredentialTarget(projectID)
	}
	return s.filePath(projectID)
}

func (s IdentityStore) usesKeychain() bool {
	return s.fileRoot == "" && runtime.GOOS == "darwin"
}

func (s IdentityStore) usesWindowsCredentialManager() bool {
	return s.fileRoot == "" && runtime.GOOS == "windows"
}

func (s IdentityStore) loadFile(projectID string) (domain.LocalIdentityRecord, error) {
	content, err := os.ReadFile(s.filePath(projectID))
	if err != nil {
		return domain.LocalIdentityRecord{}, err
	}
	var identity domain.LocalIdentityRecord
	if err := json.Unmarshal(content, &identity); err != nil {
		return domain.LocalIdentityRecord{}, err
	}
	if identity.Schema != domain.LocalIdentitySchema {
		return domain.LocalIdentityRecord{}, fmt.Errorf("unsupported identity schema %q", identity.Schema)
	}
	if identity.ProjectID != projectID {
		return domain.LocalIdentityRecord{}, fmt.Errorf("identity project id mismatch")
	}
	return identity, nil
}

func (s IdentityStore) saveFile(identity domain.LocalIdentityRecord) error {
	if err := os.MkdirAll(s.fileRoot, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.fileRoot, 0o700); err != nil {
		return err
	}
	content, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	path := s.filePath(identity.ProjectID)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func (s IdentityStore) filePath(projectID string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(projectID)
	return filepath.Join(s.fileRoot, safe+".json")
}

const macOSSecurityExecutable = "/usr/bin/security"

func (s IdentityStore) loadKeychain(projectID string) (domain.LocalIdentityRecord, error) {
	output, err := exec.Command(macOSSecurityPath(), "find-generic-password", "-w", "-s", keychainService(projectID), "-a", keychainAccount(projectID)).Output()
	if err != nil {
		return domain.LocalIdentityRecord{}, os.ErrNotExist
	}
	content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(output)))
	if err != nil {
		return domain.LocalIdentityRecord{}, err
	}
	var identity domain.LocalIdentityRecord
	if err := json.Unmarshal(content, &identity); err != nil {
		return domain.LocalIdentityRecord{}, err
	}
	if identity.Schema != domain.LocalIdentitySchema || identity.ProjectID != projectID {
		return domain.LocalIdentityRecord{}, fmt.Errorf("invalid keychain identity payload")
	}
	return identity, nil
}

func (s IdentityStore) saveKeychain(identity domain.LocalIdentityRecord) error {
	content, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(content)
	service := keychainService(identity.ProjectID)
	account := keychainAccount(identity.ProjectID)
	if err := s.deleteKeychain(identity.ProjectID); err != nil {
		return err
	}
	cmd := exec.Command(macOSSecurityPath(), "add-generic-password", "-U", "-s", service, "-a", account, "-w", encoded)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("unable to save Ghostable identity in macOS Keychain: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func (s IdentityStore) deleteKeychain(projectID string) error {
	output, err := exec.Command(macOSSecurityPath(), "delete-generic-password", "-s", keychainService(projectID), "-a", keychainAccount(projectID)).CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if isKeychainItemNotFound(text) {
			return nil
		}
		if text == "" {
			return fmt.Errorf("unable to delete Ghostable identity from macOS Keychain: %w", err)
		}
		return fmt.Errorf("unable to delete Ghostable identity from macOS Keychain: %s", text)
	}
	return nil
}

func isKeychainItemNotFound(output string) bool {
	normalized := strings.ToLower(output)
	return strings.Contains(normalized, "could not be found") ||
		strings.Contains(normalized, "not found") ||
		strings.Contains(normalized, "-25300")
}

func macOSSecurityPath() string {
	return macOSSecurityExecutable
}

func keychainService(projectID string) string {
	return "dev.ghostable.identity." + projectID
}

func keychainAccount(projectID string) string {
	return "device"
}
