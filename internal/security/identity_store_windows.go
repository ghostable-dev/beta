//go:build windows

package security

import (
	"encoding/json"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/ghostable-dev/beta/internal/domain"
	"golang.org/x/sys/windows"
)

const (
	windowsCredentialTypeGeneric         = 1
	windowsCredentialPersistLocalMachine = 2
	windowsCredentialUserName            = "device"
)

var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

type windowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func (s IdentityStore) loadWindowsCredential(projectID string) (domain.LocalIdentityRecord, error) {
	target, err := windows.UTF16PtrFromString(windowsCredentialTarget(projectID))
	if err != nil {
		return domain.LocalIdentityRecord{}, err
	}

	var credential *windowsCredential
	result, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(windowsCredentialTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&credential)),
	)
	if result == 0 {
		if windowsCredentialNotFound(callErr) {
			return domain.LocalIdentityRecord{}, os.ErrNotExist
		}
		return domain.LocalIdentityRecord{}, windowsCredentialCallError("read Windows credential", callErr)
	}
	if credential == nil {
		return domain.LocalIdentityRecord{}, fmt.Errorf("read Windows credential: empty response")
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credential)))

	content := make([]byte, int(credential.CredentialBlobSize))
	if len(content) > 0 {
		copy(content, unsafe.Slice(credential.CredentialBlob, len(content)))
	}

	var identity domain.LocalIdentityRecord
	if err := json.Unmarshal(content, &identity); err != nil {
		return domain.LocalIdentityRecord{}, err
	}
	if identity.Schema != domain.LocalIdentitySchema || identity.ProjectID != projectID {
		return domain.LocalIdentityRecord{}, fmt.Errorf("invalid Windows Credential Manager identity payload")
	}
	return identity, nil
}

func (s IdentityStore) saveWindowsCredential(identity domain.LocalIdentityRecord) error {
	content, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	if len(content) == 0 {
		return fmt.Errorf("unable to save empty Ghostable identity")
	}

	target, err := windows.UTF16PtrFromString(windowsCredentialTarget(identity.ProjectID))
	if err != nil {
		return err
	}
	comment, err := windows.UTF16PtrFromString("Ghostable local device identity")
	if err != nil {
		return err
	}
	userName, err := windows.UTF16PtrFromString(windowsCredentialUserName)
	if err != nil {
		return err
	}

	credential := windowsCredential{
		Type:               windowsCredentialTypeGeneric,
		TargetName:         target,
		Comment:            comment,
		CredentialBlobSize: uint32(len(content)),
		CredentialBlob:     &content[0],
		Persist:            windowsCredentialPersistLocalMachine,
		UserName:           userName,
	}
	result, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&credential)), 0)
	if result == 0 {
		return windowsCredentialCallError("save Ghostable identity in Windows Credential Manager", callErr)
	}
	return nil
}

func (s IdentityStore) deleteWindowsCredential(projectID string) error {
	target, err := windows.UTF16PtrFromString(windowsCredentialTarget(projectID))
	if err != nil {
		return err
	}
	result, _, callErr := procCredDeleteW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(windowsCredentialTypeGeneric),
		0,
	)
	if result == 0 && !windowsCredentialNotFound(callErr) {
		return windowsCredentialCallError("delete Windows credential", callErr)
	}
	return nil
}

func windowsCredentialTarget(projectID string) string {
	return "dev.ghostable.identity." + projectID
}

func windowsCredentialNotFound(err error) bool {
	errno, ok := err.(syscall.Errno)
	return ok && errno == syscall.Errno(windows.ERROR_NOT_FOUND)
}

func windowsCredentialCallError(action string, err error) error {
	errno, ok := err.(syscall.Errno)
	if ok && errno == 0 {
		return fmt.Errorf("%s failed", action)
	}
	return fmt.Errorf("%s: %w", action, err)
}
