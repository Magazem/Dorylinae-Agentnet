package device

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// grantUsers adds an ACE to p's DACL that gives BUILTIN\Users mask. The
// owner of p may change its DACL without being an administrator.
func grantUsers(t *testing.T, p string, mask windows.ACCESS_MASK) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	users, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: mask,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(users),
		},
	}}, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Skipf("cannot change the access list here: %v", err)
	}
}

// Review 40 L11: a program whose directory, or the file itself, BUILTIN\Users
// can write is refused, at scope-set time and by CheckTarget at each run.
// A program in a private directory is accepted, and so is a
// system program (owned by TrustedInstaller, readable by Users).
func TestCheckProgramOwnerWindows(t *testing.T) {
	dir := testutil.PrivateDir(t)
	prog := filepath.Join(dir, "tool.exe")
	if err := os.WriteFile(prog, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckProgramOwner(prog); err != nil {
		t.Fatalf("a program in a private directory: %v", err)
	}
	if sys, err := windows.GetSystemDirectory(); err == nil {
		if err := CheckProgramOwner(filepath.Join(sys, "whoami.exe")); err != nil {
			t.Fatalf("a system program: %v", err)
		}
	}

	shared := filepath.Join(dir, "shared")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	inShared := filepath.Join(shared, "tool.exe")
	if err := os.WriteFile(inShared, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Adding files to the program's directory (a DLL beside it) is enough.
	grantUsers(t, shared, fileWriteData)
	var we *WritableError
	if err := CheckProgramOwner(inShared); !errors.As(err, &we) || we.Path != shared || we.Who != "Users" {
		t.Fatalf("a program in a directory Users can add files to: %v", err)
	}
	if err := CheckTarget(inShared, dir); !errors.As(err, &we) {
		t.Fatalf("CheckTarget: %v", err)
	}
	sc := baseScope()
	sc.Commands[0].Argv = []string{inShared}
	var se *ScopeError
	if _, err := ValidateScope(sc, scopeNow, Resolver{RepoPath: func(raw string) (string, error) { return raw, nil }}); !errors.As(err, &se) ||
		se.Field != "commands[0].argv[0]" || !strings.HasPrefix(se.Reason, ReasonWritableByOthers+":") {
		t.Fatalf("scope with a program Users can swap: %v", err)
	}

	// The file itself writable by Users.
	grantUsers(t, prog, windows.GENERIC_WRITE)
	if err := CheckProgramOwner(prog); !errors.As(err, &we) || we.Path != prog {
		t.Fatalf("a program Users can write: %v", err)
	}
}

// The ACE rule on its own: which SIDs, rights, flags and ACE types count.
func TestClassifyACE(t *testing.T) {
	const self = "S-1-5-21-1-2-3-1001"
	const other = "S-1-5-21-1-2-3-1002"
	for _, tc := range []struct {
		name    string
		typ, fl uint8
		mask    uint32
		sid     string
		owner   string
		role    pathRole
		wantWho string
	}{
		{"self full", aceAllowed, 0, genericAll, self, self, roleProgram, ""},
		{"system full", aceAllowed, 0, 0x1f01ff, sidSystem, self, roleProgram, ""},
		{"admins full", aceAllowed, 0, 0x1f01ff, sidAdministrators, self, roleParent, ""},
		{"trustedinstaller", aceAllowed, 0, 0x1f01ff, sidTrustedInstaller, self, roleAncestor, ""},
		{"users read", aceAllowed, 0, 0x1200a9, "S-1-5-32-545", self, roleProgram, ""},
		{"users modify", aceAllowed, 0, 0x1301bf, "S-1-5-32-545", self, roleProgram, "Users"},
		{"everyone write", aceAllowed, 0, fileWriteData, "S-1-1-0", self, roleProgram, "Everyone"},
		{"auth users modify", aceAllowed, 0, 0x1301bf, "S-1-5-11", self, roleAncestor, "Authenticated Users"},
		{"other user delete", aceAllowed, 0, accessDelete, other, self, roleProgram, other},
		{"users write dac", aceAllowed, 0, accessWriteDAC, "S-1-5-32-545", self, roleAncestor, "Users"},
		// Creating entries above the program's directory cannot replace it
		// (C:\ grants Authenticated Users "append data").
		{"auth users add subdir on an ancestor", aceAllowed, 0, fileAppendData, "S-1-5-11", self, roleAncestor, ""},
		{"users add file beside the program", aceAllowed, 0, fileWriteData, "S-1-5-32-545", self, roleParent, "Users"},
		{"users delete child of an ancestor", aceAllowed, 0, fileDeleteChild, "S-1-5-32-545", self, roleAncestor, "Users"},
		{"inherit-only", aceAllowed, windows.INHERIT_ONLY_ACE, genericAll, "S-1-5-32-545", self, roleParent, ""},
		{"deny ignored", aceDenied, 0, genericAll, "S-1-1-0", self, roleProgram, ""},
		{"creator owner = self", aceAllowed, 0, genericAll, sidCreatorOwner, self, roleProgram, ""},
		{"owner rights = admins", aceAllowed, 0, genericAll, sidOwnerRights, sidAdministrators, roleProgram, ""},
		{"capability sid", aceAllowed, 0, genericAll, "S-1-15-3-1-2-3", self, roleParent, ""},
		{"callback ace", aceAllowedCallback, 0, genericWrite, "S-1-5-32-545", self, roleProgram, "Users"},
		{"object ace", aceAllowedObject, 0, genericWrite, "", self, roleProgram, "an object access entry"},
	} {
		if got := classifyACE(tc.typ, tc.fl, tc.mask, tc.sid, tc.owner, self, tc.role); got != tc.wantWho {
			t.Errorf("%s: who = %q, want %q", tc.name, got, tc.wantWho)
		}
	}
}

// junction makes link a junction to target, or skips the test.
func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil { //nolint:gosec // fixed test command
		t.Skipf("mklink /J: %v %s", err, out)
	}
}

// Review 41 M1: a junction on the program's path is checked as a link (its
// own access list, which can rewrite where it points) and followed: the
// directories above its target count too. CheckProgramOwner itself refuses
// any path through a junction today (EvalSymlinks does not follow mount
// points), so the walk is exercised directly.
func TestOwnerWalkJunction(t *testing.T) {
	dir := testutil.PrivateDir(t)
	open := filepath.Join(dir, "open")
	inner := filepath.Join(open, "real")
	if err := os.MkdirAll(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "tool.exe"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := filepath.Join(dir, "j")
	junction(t, j, inner)
	walk := func() error {
		w := ownerWalk{seen: map[string]bool{}}
		return w.check(filepath.Join(j, "tool.exe"), 0)
	}
	if err := walk(); err != nil {
		t.Fatalf("a junction to a private directory: %v", err)
	}
	var we *WritableError
	grantUsers(t, open, fileDeleteChild)
	if err := walk(); !errors.As(err, &we) || we.Path != open {
		t.Fatalf("a junction whose target's directory Users can delete from: %v", err)
	}

	j2 := filepath.Join(dir, "j2")
	junction(t, j2, filepath.Join(dir, "j2-target"))
	if err := os.Mkdir(filepath.Join(dir, "j2-target"), 0o700); err != nil {
		t.Fatal(err)
	}
	grantUsers(t, j2, fileWriteAttrs)
	w := ownerWalk{seen: map[string]bool{}}
	if err := w.check(j2, 1); !errors.As(err, &we) || we.Path != j2 {
		t.Fatalf("a junction Users can rewrite: %v", err)
	}
}
