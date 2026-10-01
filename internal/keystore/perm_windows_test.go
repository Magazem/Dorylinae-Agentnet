package keystore

import (
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

// Review 55 R55-091: the temporary key file is owner-only from its creation,
// before any byte is written, and no other handle can be opened on it.
func TestCreatePrivateTempIsPrivateFromCreation(t *testing.T) {
	f, err := createPrivateTemp(testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := OwnerOnly(f.Name()); err != nil {
		t.Fatalf("not owner-only at creation: %v", err)
	}
	if g, err := os.Open(f.Name()); err == nil {
		_ = g.Close()
		t.Fatal("a second handle could be opened while the file is written")
	}
}

// Review 55 R55-089: a key file others can read is refused on load, as on
// Unix, not used silently.
func TestFileGetRefusesSharedKeyFile(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "identity.key")
	f := NewFile(path)
	if err := f.Set([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(); err != nil {
		t.Fatal(err)
	}
	grantUsers(t, path, windows.GENERIC_READ)
	if _, err := f.Get(); err == nil {
		t.Fatal("a key file Users can read was loaded")
	}
}
