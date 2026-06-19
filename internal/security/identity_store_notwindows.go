//go:build !windows

package security

import (
	"fmt"

	"github.com/ghostable-dev/beta/internal/domain"
)

func (s IdentityStore) loadWindowsCredential(projectID string) (domain.LocalIdentityRecord, error) {
	return domain.LocalIdentityRecord{}, fmt.Errorf("Windows Credential Manager is not available on this platform")
}

func (s IdentityStore) saveWindowsCredential(identity domain.LocalIdentityRecord) error {
	return fmt.Errorf("Windows Credential Manager is not available on this platform")
}

func (s IdentityStore) deleteWindowsCredential(projectID string) error {
	return fmt.Errorf("Windows Credential Manager is not available on this platform")
}

func windowsCredentialTarget(projectID string) string {
	return "dev.ghostable.identity." + projectID
}
