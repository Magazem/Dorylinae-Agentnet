package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// grantUsers adds an ACE to p's DACL that gives BUILTIN\Users mask (inherited
// by children). The owner of p may change its DACL without being an
// administrator.
func grantUsers(t *testing.T, p string, mask windows.ACCESS_MASK) {
	t.Helper()
	grantUsersInherit(t, p, mask, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
}

// grantUsersInherit is grantUsers with the inheritance flags inh.
func grantUsersInherit(t *testing.T, p string, mask windows.ACCESS_MASK, inh uint32) {
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
		Inheritance:       inh,
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

// Review 55 R55-089: a config dir others can read is made private by
// Ensure, and what is created in it afterwards inherits that.
func TestEnsureMakesSharedDirPrivate(t *testing.T) {
	dir := filepath.Join(testutil.TempDir(t), "home")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	grantUsers(t, dir, windows.GENERIC_READ)
	var np *NotPrivateError
	if err := CheckPrivate(dir); !errors.As(err, &np) || np.Owner {
		t.Fatalf("CheckPrivate of a dir Users can read = %v, want a NotPrivateError", err)
	}
	p, err := In(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivate(dir); err != nil {
		t.Fatalf("after Ensure: %v", err)
	}
	f := filepath.Join(dir, "dorylinae.db")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivate(f); err != nil {
		t.Fatalf("a file created after Ensure: %v", err)
	}
}

// Rights that reveal nothing of the contents (reading the attributes and the
// access list, synchronize) do not make a path shared.
func TestCheckPrivateIgnoresHarmlessAccess(t *testing.T) {
	dir := filepath.Join(testutil.TempDir(t), "home")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := In(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	grantUsers(t, dir, windows.SYNCHRONIZE|windows.READ_CONTROL|0x80)
	if err := CheckPrivate(dir); err != nil {
		t.Fatalf("harmless access reported: %v", err)
	}
}

// Review 87 L1: an inherit-only entry on the config dir grants nothing on the
// dir itself, but every file created in it later (the database, logs) gets
// it, so the dir is not private.
func TestInheritOnlyAceMakesDirNotPrivate(t *testing.T) {
	dir := filepath.Join(testutil.TempDir(t), "home")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := In(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	grantUsersInherit(t, dir, windows.GENERIC_READ, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT|windows.INHERIT_ONLY)
	var np *NotPrivateError
	if err := CheckPrivate(dir); !errors.As(err, &np) {
		t.Fatalf("CheckPrivate with an inherit-only Users entry = %v, want a NotPrivateError", err)
	}
	// The dir is still empty, so Ensure makes it private again.
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "dorylinae.db")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivate(f); err != nil {
		t.Fatalf("a file created after Ensure: %v", err)
	}
}

// Review 87 L4: Ensure does not replace the access list of an existing dir
// that holds files (a shared folder $DORYLINAE_HOME points at); it refuses
// with a *SharedDirError naming the fix, and the dir is left as it was.
func TestEnsureRefusesSharedDirWithContents(t *testing.T) {
	dir := filepath.Join(testutil.TempDir(t), "shared")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "team-notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	grantUsers(t, dir, windows.GENERIC_READ)
	before := sddl(t, dir)
	p, err := In(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = p.Ensure()
	var se *SharedDirError
	var np *NotPrivateError
	if !errors.As(err, &se) || !errors.As(err, &np) || !strings.Contains(err.Error(), HomeEnv) {
		t.Fatalf("Ensure of a shared dir with contents = %v, want a SharedDirError naming %s", err, HomeEnv)
	}
	if after := sddl(t, dir); after != before {
		t.Fatalf("the access list changed: %s -> %s", before, after)
	}
}

func sddl(t *testing.T, p string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}
