package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghostable-dev/beta/internal/manifest"
	"github.com/ghostable-dev/beta/internal/security"
)

type LocalIdentityCleanupEntry struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	Root        string `json:"root"`
	DeviceID    string `json:"deviceId"`
	Identity    string `json:"identity"`
	Reason      string `json:"reason"`
}

type LocalIdentityCleanupResult struct {
	Registry string                      `json:"registry"`
	DryRun   bool                        `json:"dryRun"`
	Orphaned []LocalIdentityCleanupEntry `json:"orphaned"`
	Removed  []LocalIdentityCleanupEntry `json:"removed"`
}

func CleanupOrphanedLocalIdentities(dryRun bool) (LocalIdentityCleanupResult, error) {
	identityStore, err := security.NewIdentityStore()
	if err != nil {
		return LocalIdentityCleanupResult{}, err
	}
	registryPath, err := identityStore.RegistryPath()
	if err != nil {
		return LocalIdentityCleanupResult{}, err
	}
	entries, err := identityStore.ListProjectIdentities()
	if err != nil {
		return LocalIdentityCleanupResult{}, err
	}

	result := LocalIdentityCleanupResult{
		Registry: registryPath,
		DryRun:   dryRun,
		Orphaned: []LocalIdentityCleanupEntry{},
		Removed:  []LocalIdentityCleanupEntry{},
	}
	for _, entry := range entries {
		reason, orphaned := localIdentityOrphanReason(entry)
		if !orphaned {
			continue
		}
		cleanupEntry := localIdentityCleanupEntry(entry, reason)
		result.Orphaned = append(result.Orphaned, cleanupEntry)
		if dryRun {
			continue
		}
		if err := identityStore.Delete(entry.ProjectID); err != nil {
			return result, fmt.Errorf("delete local identity for %s: %w", localIdentityProjectLabel(entry), err)
		}
		result.Removed = append(result.Removed, cleanupEntry)
	}
	return result, nil
}

func localIdentityOrphanReason(entry security.IdentityRegistryEntry) (string, bool) {
	root := strings.TrimSpace(entry.Root)
	if root == "" {
		return "repo path was not recorded", true
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "repo path does not exist", true
		}
		return "", false
	}
	if !info.IsDir() {
		return "repo path is not a directory", true
	}

	manifestPath := filepath.Join(root, ".ghostable", "ghostable.yaml")
	file, err := os.Open(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "Ghostable manifest is missing", true
		}
		return "", false
	}
	defer file.Close()

	project, err := manifest.Read(file)
	if err != nil {
		return "", false
	}
	if project.ID != entry.ProjectID {
		return "repo path belongs to a different Ghostable project", true
	}
	return "", false
}

func localIdentityCleanupEntry(entry security.IdentityRegistryEntry, reason string) LocalIdentityCleanupEntry {
	return LocalIdentityCleanupEntry{
		ProjectID:   entry.ProjectID,
		ProjectName: entry.ProjectName,
		Root:        entry.Root,
		DeviceID:    entry.DeviceID,
		Identity:    entry.Identity,
		Reason:      reason,
	}
}

func localIdentityProjectLabel(entry security.IdentityRegistryEntry) string {
	if strings.TrimSpace(entry.ProjectName) != "" {
		return entry.ProjectName
	}
	return entry.ProjectID
}
