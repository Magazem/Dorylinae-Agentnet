package paths

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// grantUsers adds an ACE to p's DACL that gives BUILTIN\Users mask (inherited
// by children). The owner of p may change its DACL without being an
// administrator.
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
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
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
