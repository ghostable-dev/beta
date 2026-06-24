package security

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ghostable-dev/beta/internal/domain"
)

const identityRegistrySchema = "ghostable.identity-registry.v1"

type IdentityRegistryEntry struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	Root        string `json:"root"`
	DeviceID    string `json:"deviceId"`
	Identity    string `json:"identity"`
	UpdatedAt   string `json:"updatedAt"`
}

type identityRegistryFile struct {
	Schema  string                  `json:"schema"`
	Version int                     `json:"version"`
	Entries []IdentityRegistryEntry `json:"entries"`
}

func (s IdentityStore) RegisterProjectIdentity(identity domain.LocalIdentityRecord, projectName string, root string) error {
	if strings.TrimSpace(identity.ProjectID) == "" {
		return fmt.Errorf("identity project id is required")
	}
	if strings.TrimSpace(identity.DeviceID) == "" {
		return fmt.Errorf("identity device id is required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	registry, err := s.loadIdentityRegistry()
	if err != nil {
		return err
	}

	entry := IdentityRegistryEntry{
		ProjectID:   identity.ProjectID,
		ProjectName: strings.TrimSpace(projectName),
		Root:        absoluteRoot,
		DeviceID:    identity.DeviceID,
		Identity:    s.Path(identity.ProjectID),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	replaced := false
	for i, existing := range registry.Entries {
		if existing.ProjectID == identity.ProjectID {
			registry.Entries[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		registry.Entries = append(registry.Entries, entry)
	}
	sortIdentityRegistryEntries(registry.Entries)
	return s.writeIdentityRegistry(registry)
}

func (s IdentityStore) UnregisterProjectIdentity(projectID string) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil
	}
	registry, err := s.loadIdentityRegistry()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	next := registry.Entries[:0]
	for _, entry := range registry.Entries {
		if entry.ProjectID != projectID {
			next = append(next, entry)
		}
	}
	if len(next) == len(registry.Entries) {
		return nil
	}
	registry.Entries = next
	return s.writeIdentityRegistry(registry)
}

func (s IdentityStore) ListProjectIdentities() ([]IdentityRegistryEntry, error) {
	registry, err := s.loadIdentityRegistry()
	if err != nil {
		if os.IsNotExist(err) {
			return []IdentityRegistryEntry{}, nil
		}
		return nil, err
	}
	entries := append([]IdentityRegistryEntry(nil), registry.Entries...)
	sortIdentityRegistryEntries(entries)
	return entries, nil
}

func (s IdentityStore) RegistryPath() (string, error) {
	if strings.TrimSpace(s.fileRoot) != "" {
		return filepath.Join(s.fileRoot, "identity-registry.json"), nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "ghostable", "identity-registry.json"), nil
}

func (s IdentityStore) loadIdentityRegistry() (identityRegistryFile, error) {
	path, err := s.RegistryPath()
	if err != nil {
		return identityRegistryFile{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newIdentityRegistryFile(), nil
		}
		return identityRegistryFile{}, err
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return newIdentityRegistryFile(), nil
	}

	var registry identityRegistryFile
	if err := json.Unmarshal(content, &registry); err != nil {
		return identityRegistryFile{}, err
	}
	if registry.Schema != identityRegistrySchema {
		return identityRegistryFile{}, fmt.Errorf("unsupported identity registry schema %q", registry.Schema)
	}
	if registry.Version != 1 {
		return identityRegistryFile{}, fmt.Errorf("unsupported identity registry version %d", registry.Version)
	}
	if registry.Entries == nil {
		registry.Entries = []IdentityRegistryEntry{}
	}
	return registry, nil
}

func (s IdentityStore) writeIdentityRegistry(registry identityRegistryFile) error {
	registry.Schema = identityRegistrySchema
	registry.Version = 1
	if registry.Entries == nil {
		registry.Entries = []IdentityRegistryEntry{}
	}
	sortIdentityRegistryEntries(registry.Entries)

	path, err := s.RegistryPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	content, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return writeLocalIdentityFileAtomic(path, content, 0o600)
}

func newIdentityRegistryFile() identityRegistryFile {
	return identityRegistryFile{
		Schema:  identityRegistrySchema,
		Version: 1,
		Entries: []IdentityRegistryEntry{},
	}
}

func sortIdentityRegistryEntries(entries []IdentityRegistryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ProjectName != entries[j].ProjectName {
			return entries[i].ProjectName < entries[j].ProjectName
		}
		if entries[i].Root != entries[j].Root {
			return entries[i].Root < entries[j].Root
		}
		return entries[i].ProjectID < entries[j].ProjectID
	})
}

func writeLocalIdentityFileAtomic(path string, content []byte, mode os.FileMode) error {
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
