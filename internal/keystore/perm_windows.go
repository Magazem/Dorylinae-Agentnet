//go:build windows

package keystore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"golang.org/x/sys/windows"
)

// ownerOnlySD is a protected security descriptor (no inherited entries)
// holding a single allow entry for the current user.
func ownerOnlySD() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("lookup current user: %w", err)
	}
	return windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
}

// createPrivateTemp creates a new file in dir that carries the owner-only
// DACL from its creation, opened with share mode 0 so no other handle can be
// opened on it while it is being written (review 55 R55-091: setting the
// DACL after creation left a window in which a handle opened under the
// inherited DACL kept its access).
func createPrivateTemp(dir string) (*os.File, error) {
	sd, err := ownerOnlySD()
	if err != nil {
		return nil, err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	for range 100 {
		var rnd [8]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			return nil, err
		}
		name := filepath.Join(dir, ".secret-"+hex.EncodeToString(rnd[:])+".tmp")
		p, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
		h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, sa,
			windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) {
			continue
		}
		if err != nil {
			return nil, &fs.PathError{Op: "create", Path: name, Err: err}
		}
		return os.NewFile(uintptr(h), name), nil
	}
	return nil, fmt.Errorf("create a temporary file in %s: too many collisions", dir)
}

// syncDir does nothing on Windows: NTFS journals the rename, and a directory
// cannot be opened for FlushFileBuffers without backup semantics.
func syncDir(string) error { return nil }

// checkOwnerOnly refuses a key file that is not private to the current user:
// owned by someone else, or readable by anyone else (review 55 R55-089).
func checkOwnerOnly(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	if err := paths.CheckPrivate(path); err != nil {
		return fmt.Errorf("key file %s: %w; it must be accessible by the current user only", path, err)
	}
	return nil
}

// OwnerOnly reports whether path has a protected DACL whose only entry is an
// allow entry for the current user.
func OwnerOnly(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		return err
	}
	if ctrl&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s: DACL inherits permissions from its parent", path)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil || dacl.AceCount != 1 {
		return fmt.Errorf("%s: expected exactly one ACL entry", path)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		return err
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) //nolint:gosec // the SID follows the ACE header
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !windows.EqualSid(sid, user.User.Sid) {
		return fmt.Errorf("%s: ACL entry is not an allow entry for the current user", path)
	}
	return nil
}
