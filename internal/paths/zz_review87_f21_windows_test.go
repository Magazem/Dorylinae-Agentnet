package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 87 (R55-F21 security review) evidence tests. 87-03 fails while the
// finding stands; the junction probe passes (negative result: no finding).

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

// Finding 87-03: an inherit-only ACE on the config dir is skipped by
// CheckPrivate, so Ensure leaves it in place, yet every file later created
// in the dir with an inherited DACL (the database, logs) is readable by Users.
func TestReview87InheritOnlyAceOnConfigDir(t *testing.T) {
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
	if CheckPrivate(dir) != nil {
		return // fixed: the inherit-only entry is flagged (and Ensure rewrites it)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "dorylinae.db")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivate(f); err != nil {
		t.Fatalf("the dir passes CheckPrivate and Ensure, but a file created in it is not private: %v", err)
	}
}

// Finding 87-04 probe: does Ensure's DACL rewrite propagate through a
// junction inside the config dir to the junction's target? The junction and
// its target both live in this test's own temp dir; the junction is removed
// (non-recursively) before the temp dir is cleaned up.
func TestReview87EnsurePropagationThroughJunction(t *testing.T) {
	root := testutil.TempDir(t)
	home := filepath.Join(root, "home")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{home, outside} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	grantUsersInherit(t, home, windows.GENERIC_READ, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
	link := filepath.Join(home, "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v %s", err, out)
	}
	// Registered after TempDir, so it runs first: the junction goes before
	// the recursive cleanup of root.
	t.Cleanup(func() { _ = os.Remove(link) })
	sddl := func(p string) string {
		sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		return sd.String()
	}
	beforeDir, beforeFile := sddl(outside), sddl(filepath.Join(outside, "f.txt"))
	p, err := In(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	afterDir, afterFile := sddl(outside), sddl(filepath.Join(outside, "f.txt"))
	t.Logf("target dir:  %s -> %s", beforeDir, afterDir)
	t.Logf("target file: %s -> %s", beforeFile, afterFile)
	t.Logf("junction:    %s", sddl(link))
	if beforeDir != afterDir || beforeFile != afterFile {
		t.Fatal("Ensure's DACL rewrite propagated through the junction to its target")
	}
}
