//go:build windows

package keystore

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// restrictToOwner replaces the file's DACL with a protected one (no inherited
// entries) holding a single allow entry for the current user.
func restrictToOwner(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("lookup current user: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}

// checkOwnerOnly only checks existence on Windows: the DACL is set when the
// file is created, and OwnerOnly verifies it (used by tests and diagnostics).
func checkOwnerOnly(path string) error {
	_, err := os.Stat(path)
	return err
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
