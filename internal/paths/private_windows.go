//go:build windows

package paths

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Well-known SIDs whose access to the config dir is not someone else's.
const (
	sidSystem         = "S-1-5-18"
	sidAdministrators = "S-1-5-32-544"
	sidCreatorOwner   = "S-1-3-0"
	sidOwnerRights    = "S-1-3-4"
)

// Allow ACE types (winnt.h); the first two are a header, a mask and the SID.
const (
	aceAllowed         = 0x0
	aceAllowedCompound = 0x4
	aceAllowedObject   = 0x5
	aceAllowedCallback = 0x9
	aceAllowedCbObject = 0xB
)

// harmlessAccess are the rights that reveal and change nothing of the
// contents: synchronize, reading the security descriptor, the attributes
// and the extended attributes, and traversing a directory.
const harmlessAccess = windows.SYNCHRONIZE | windows.READ_CONTROL | 0x80 | 0x8 | 0x20

var (
	selfOnce sync.Once
	selfSID  string
	errSelf  error
)

// currentUserSID is the SID of the user this process runs as.
func currentUserSID() (string, error) {
	selfOnce.Do(func() {
		tu, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			errSelf = err
			return
		}
		selfSID = tu.User.Sid.String()
	})
	return selfSID, errSelf
}

func trusted(sid, self string) bool {
	return sid == self || sid == sidSystem || sid == sidAdministrators
}

// CheckPrivate reports whether p (the config dir, or a key file in it) is
// private to the current user: owned by this user (or SYSTEM or
// Administrators, the owner of what an elevated process creates), and no
// allow entry of its DACL grants anyone else more than harmlessAccess
// (review 55 R55-089). It returns a *NotPrivateError when it is not.
func CheckPrivate(p string) error {
	self, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("read the current user: %w", err)
	}
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read the access list of %s: %w", p, err)
	}
	ownerSID, _, err := sd.Owner()
	if err != nil || ownerSID == nil {
		return &NotPrivateError{Path: p, Who: "an unknown owner", Owner: true}
	}
	owner := ownerSID.String()
	if !trusted(owner, self) {
		return &NotPrivateError{Path: p, Who: "its owner, " + owner, Owner: true}
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return &NotPrivateError{Path: p, Who: "everyone (it has no access list)"}
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("read the access list of %s: %w", p, err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || uint32(ace.Mask)&^harmlessAccess == 0 {
			continue
		}
		switch ace.Header.AceType {
		case aceAllowed, aceAllowedCallback:
		case aceAllowedCompound, aceAllowedObject, aceAllowedCbObject:
			return &NotPrivateError{Path: p, Who: "an object access entry"}
		default:
			continue // deny entries
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String() //nolint:gosec // the SID follows the mask in an ACCESS_ALLOWED_ACE (winnt.h)
		switch {
		case sid == sidCreatorOwner || sid == sidOwnerRights:
			sid = owner
		case strings.HasPrefix(sid, "S-1-15-"):
			continue // an AppContainer SID never lets another user in
		}
		if !trusted(sid, self) {
			return &NotPrivateError{Path: p, Who: sid}
		}
	}
	return nil
}

// secureDir makes the config dir private (R55-089): a dir owned by another
// user is refused, and a DACL that lets someone else in is replaced by a
// protected one with a single entry, inherited by everything inside, for the
// current user. A private dir is left as it is.
func secureDir(dir string) error {
	err := CheckPrivate(dir)
	var np *NotPrivateError
	if err == nil || !errors.As(err, &np) || np.Owner {
		return err
	}
	self, err := currentUserSID()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + self + ")")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}
