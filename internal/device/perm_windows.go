package device

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Well-known SIDs whose write access is not "someone else's": SYSTEM,
// BUILTIN\Administrators and NT SERVICE\TrustedInstaller. The device's own
// user is added at run time (currentUserSID).
const (
	sidSystem           = "S-1-5-18"
	sidAdministrators   = "S-1-5-32-544"
	sidTrustedInstaller = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
	sidCreatorOwner     = "S-1-3-0"
	sidOwnerRights      = "S-1-3-4"
)

// Access rights that let a SID change or replace what it is granted on
// (winnt.h). The FILE_* values are the same bits as their directory
// meanings: 0x2 FILE_ADD_FILE, 0x4 FILE_ADD_SUBDIRECTORY.
const (
	fileWriteData   = 0x2
	fileAppendData  = 0x4
	fileDeleteChild = 0x40
	accessDelete    = 0x10000
	accessWriteDAC  = 0x40000
	accessWriteOwn  = 0x80000
	genericAll      = 0x10000000
	genericWrite    = 0x40000000
)

// writeMask is, for each role, the rights that let another SID swap the
// program. On the file: writing it, deleting it or changing its ACL or
// owner. On the directory holding it: also adding a file (a DLL next to the
// .exe is loaded first) or a subdirectory (a "<name>.exe.local" redirect),
// and deleting a child. Above that: deleting or renaming a child or the
// directory itself; merely creating new entries there (the Authenticated
// Users "append data" on C:\) cannot replace an existing one.
func writeMask(role pathRole) uint32 {
	common := uint32(accessDelete | accessWriteDAC | accessWriteOwn | genericAll)
	switch role {
	case roleProgram:
		return common | fileWriteData | fileAppendData | genericWrite
	case roleParent:
		return common | fileWriteData | fileAppendData | fileDeleteChild | genericWrite
	default:
		return common | fileDeleteChild
	}
}

// ACE types (winnt.h) whose layout is a header, a mask and then the SID.
const (
	aceAllowed         = 0x0
	aceDenied          = 0x1
	aceAllowedObject   = 0x5
	aceAllowedCallback = 0x9
	aceDeniedCallback  = 0xA
	aceAllowedCbObject = 0xB
)

var (
	userSIDOnce sync.Once
	userSID     string
	userSIDErr  error
)

// currentUserSID is the SID of the user the daemon runs as.
func currentUserSID() (string, error) {
	userSIDOnce.Do(func() {
		tu, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			userSIDErr = err
			return
		}
		userSID = tu.User.Sid.String()
	})
	return userSID, userSIDErr
}

// trustedSID reports whether sid's write access is allowed: this user, SYSTEM,
// Administrators or TrustedInstaller.
func trustedSID(sid, self string) bool {
	switch sid {
	case self, sidSystem, sidAdministrators, sidTrustedInstaller:
		return true
	}
	return false
}

// sidName names a SID for the error message.
func sidName(sid string) string {
	switch sid {
	case "S-1-1-0":
		return "Everyone"
	case "S-1-5-11":
		return "Authenticated Users"
	case "S-1-5-32-545":
		return "Users"
	case "S-1-5-4":
		return "Interactive users"
	}
	return sid
}

// classifyACE reports, for one ACE of p's DACL, who other than the trusted
// SIDs it lets change p, or "" if nobody. Deny ACEs are ignored (a stricter
// reading); inherit-only ACEs do not apply to p itself. An allow ACE of a
// kind whose SID is not read here (object and callback-object ACEs) counts
// as someone else when it grants a write right.
func classifyACE(aceType, aceFlags uint8, mask uint32, sid, owner, self string, role pathRole) string {
	if aceFlags&windows.INHERIT_ONLY_ACE != 0 {
		return ""
	}
	if mask&writeMask(role) == 0 {
		return ""
	}
	switch aceType {
	case aceAllowed, aceAllowedCallback:
	case aceAllowedObject, aceAllowedCbObject:
		return "an object access entry"
	default:
		return ""
	}
	switch {
	case sid == sidCreatorOwner || sid == sidOwnerRights:
		// Stands for the owner, which is checked on its own.
		sid = owner
	case strings.HasPrefix(sid, "S-1-15-"):
		// An app package or capability SID (AppContainer): it only counts
		// in the second access check of an AppContainer token, whose user
		// must be granted the access too, so it never lets anyone else in.
		return ""
	}
	if trustedSID(sid, self) {
		return ""
	}
	return sidName(sid)
}

// checkPathOwner reads p's owner and DACL and refuses p when the owner is not
// this user or an administrator SID, when it has no DACL (everyone has full
// access), or when an ACE lets another SID change it (classifyACE).
func checkPathOwner(p string, role pathRole) error {
	self, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("device: read the current user: %w", err)
	}
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("device: read the access list of %s: %w", DisplayQuote(p), err)
	}
	ownerSID, _, err := sd.Owner()
	if err != nil || ownerSID == nil {
		return &WritableError{Path: p, Who: "an unknown owner"}
	}
	owner := ownerSID.String()
	if !trustedSID(owner, self) {
		return &WritableError{Path: p, Who: "its owner, " + sidName(owner)}
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return &WritableError{Path: p, Who: "everyone (it has no access list)"}
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("device: read the access list of %s: %w", DisplayQuote(p), err)
		}
		sid := ""
		switch ace.Header.AceType {
		case aceAllowed, aceAllowedCallback, aceDenied, aceDeniedCallback:
			sid = (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String() //nolint:gosec // the SID follows the mask in an ACCESS_ALLOWED_ACE (winnt.h)
		}
		if who := classifyACE(ace.Header.AceType, ace.Header.AceFlags, uint32(ace.Mask), sid, owner, self, role); who != "" {
			return &WritableError{Path: p, Who: who}
		}
	}
	return nil
}
