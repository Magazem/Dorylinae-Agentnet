package pathid

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
	"golang.org/x/sys/windows"
)

// Every spelling that starts with two separators is refused by CheckLocal,
// without touching the filesystem (review 55 R55-027): the UNC host is
// TEST-NET-1, so a dial would take seconds.
func TestCheckLocalRefusesUNCAndDevicePaths(t *testing.T) {
	for _, p := range []string{
		`\\192.0.2.1\share`, `\\192.0.2.1\share\dir`, `//192.0.2.1/share/dir`,
		`\\?\UNC\192.0.2.1\share`, `\\?\C:\`, `\\.\C:\`, `\\?\GLOBALROOT\Device\HarddiskVolume1\`,
		`\\?\Volume{00000000-0000-0000-0000-000000000000}\`,
		// The NT object-manager prefix (review 61 F7-S2).
		`\??\UNC\192.0.2.1\share\x`, `\??\UNC\localhost\C$\Users`, `\??\C:\`, `\??\C:\Windows`,
		`\??\GLOBALROOT\Device\HarddiskVolume1\`, `/??/UNC/192.0.2.1/share`,
		`\??\Volume{00000000-0000-0000-0000-000000000000}\`,
	} {
		start := time.Now()
		if err := CheckLocal(p); !errors.Is(err, ErrRemote) {
			t.Errorf("CheckLocal(%q) = %v, want ErrRemote", p, err)
		}
		if _, err := Resolve(p); !errors.Is(err, ErrRemote) {
			t.Errorf("Resolve(%q) = %v, want ErrRemote", p, err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("%q took %v: the path was dialled", p, d)
		}
	}
	if err := CheckLocal(`C:\Windows`); err != nil {
		t.Errorf("CheckLocal(C:\\Windows) = %v", err)
	}
}

// A junction is resolved to its target (Go's EvalSymlinks leaves it).
func TestResolveJunction(t *testing.T) {
	base := testutil.TempDir(t)
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	j := filepath.Join(base, "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", j, target).CombinedOutput(); err != nil { //nolint:gosec // test: fixed command, temp dirs
		t.Skipf("mklink /J: %v %s", err, out)
	}
	a, err := Resolve(j)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Resolve(target)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("Resolve(junction) = %q, Resolve(target) = %q", a, b)
	}
}

// The 8.3 short name resolves to the long one, and the \\?\ spelling is
// refused.
func TestResolveShortName(t *testing.T) {
	dir := filepath.Join(testutil.TempDir(t), "a-long-directory-name")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	long, err := Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := windows.UTF16PtrFromString(dir)
	buf := make([]uint16, 1024)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf))) //nolint:gosec // fixed 1024
	if err != nil || n == 0 {
		t.Skipf("no short name: %v", err)
	}
	short := windows.UTF16ToString(buf[:n])
	got, err := Resolve(short)
	if err != nil || got != long {
		t.Fatalf("Resolve(%q) = %q, %v; want %q", short, got, err, long)
	}
	if _, err := Resolve(`\\?\` + dir); !errors.Is(err, ErrRemote) {
		t.Errorf(`Resolve(\\?\dir) = %v, want ErrRemote`, err)
	}
}

// freeDrive returns an unused drive letter such as "Q:", or "".
func freeDrive() string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return ""
	}
	for c := 'Z'; c >= 'G'; c-- {
		if mask&(1<<uint(c-'A')) == 0 {
			return string(c) + ":"
		}
	}
	return ""
}

// A subst drive is resolved to the directory it stands for.
func TestResolveSubst(t *testing.T) {
	base := testutil.TempDir(t)
	drive := freeDrive()
	if drive == "" {
		t.Skip("no free drive letter")
	}
	if out, err := exec.Command("subst", drive, base).CombinedOutput(); err != nil { //nolint:gosec // test: fixed command, temp dir
		t.Skipf("subst: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("subst", drive, "/D").Run() }) //nolint:gosec // test: fixed command
	want, err := Resolve(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(drive + `\`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("Resolve(%s\\) = %q, want %q", drive, got, want)
	}
	if ok, err := IsRoot(got); err != nil || ok {
		t.Fatalf("IsRoot(%q) = %v, %v; a subst target is not a root", got, ok, err)
	}
}

// A symlink to a UNC path is refused by the guard before anything follows it
// (review 55 R55-027): TEST-NET-1 is never reachable, so a dial would hang.
// The link is made with CreateSymbolicLink, which unlike os.Symlink does not
// stat the target.
func TestResolveRefusesLinkToUNC(t *testing.T) {
	link := filepath.Join(testutil.TempDir(t), "link")
	l, _ := windows.UTF16PtrFromString(link)
	target, _ := windows.UTF16PtrFromString(`\\192.0.2.1\share\dir`)
	const allowUnprivileged = 0x2 // SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE (developer mode)
	if err := windows.CreateSymbolicLink(l, target, windows.SYMBOLIC_LINK_FLAG_DIRECTORY|allowUnprivileged); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	start := time.Now()
	if _, err := Resolve(link); !errors.Is(err, ErrRemote) {
		t.Fatalf("Resolve(link to UNC) = %v, want ErrRemote", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v: the UNC target was dialled", d)
	}
}

// A refused spelling reaches no Windows call at all (review 61 F7-S2): the
// file and drive-table calls are replaced by ones that fail the test.
func TestRefusalMakesNoCall(t *testing.T) {
	oldLstat, oldDrive, oldCreate := lstat, getDriveType, createFile
	t.Cleanup(func() { lstat, getDriveType, createFile = oldLstat, oldDrive, oldCreate })
	var called []string
	lstat = func(p string) (os.FileInfo, error) {
		called = append(called, "Lstat "+p)
		return nil, os.ErrNotExist
	}
	getDriveType = func(root *uint16) uint32 {
		called = append(called, "GetDriveType "+windows.UTF16PtrToString(root))
		return windows.DRIVE_FIXED
	}
	createFile = func(name *uint16, _, _ uint32, _ *windows.SecurityAttributes, _, _ uint32, _ windows.Handle) (windows.Handle, error) {
		called = append(called, "CreateFile "+windows.UTF16PtrToString(name))
		return windows.InvalidHandle, windows.ERROR_FILE_NOT_FOUND
	}
	for _, p := range []string{
		`\??\UNC\192.0.2.1\share\x`, `\??\C:\Windows`, `\??\GLOBALROOT\Device\HarddiskVolume1\`,
		`\\192.0.2.1\share\dir`, `\\?\UNC\192.0.2.1\share`, `\\.\C:\`,
	} {
		called = nil
		if err := CheckLocal(p); !errors.Is(err, ErrRemote) {
			t.Errorf("CheckLocal(%q) = %v, want ErrRemote", p, err)
		}
		if _, err := Resolve(p); !errors.Is(err, ErrRemote) {
			t.Errorf("Resolve(%q) = %v, want ErrRemote", p, err)
		}
		if len(called) > 0 {
			t.Errorf("%q reached %q before being refused", p, called)
		}
	}
}

// A link whose target does not exist is refused before any open: the
// kernel, not the guard, would be the first to follow it (review 61b F7b-1).
func TestResolveRefusesDanglingLinkWithoutOpen(t *testing.T) {
	base := testutil.TempDir(t)
	j := filepath.Join(base, "junction")
	makeJunction(t, j, filepath.Join(base, "missing"))
	oldCreate := createFile
	t.Cleanup(func() { createFile = oldCreate })
	createFile = func(name *uint16, _, _ uint32, _ *windows.SecurityAttributes, _, _ uint32, _ windows.Handle) (windows.Handle, error) {
		t.Errorf("CreateFile %q reached", windows.UTF16PtrToString(name))
		return windows.InvalidHandle, windows.ERROR_FILE_NOT_FOUND
	}
	if _, err := Resolve(filepath.Join(j, "sub")); err == nil {
		t.Fatal("Resolve through a dangling junction succeeded")
	}
}

// makeJunction creates the junction j to target with mklink /J, or skips.
func makeJunction(t *testing.T, j, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", j, target).CombinedOutput(); err != nil { //nolint:gosec // test: fixed command, temp dirs
		t.Skipf("mklink /J: %v %s", err, out)
	}
	// Remove the link itself, never through it, before the temp dir goes.
	t.Cleanup(func() { _ = os.Remove(j) })
}

// A path through a junction resolves to the path through its target
// (review 61 F7-S4: go1.23+ EvalSymlinks fails on it).
func TestResolveThroughJunction(t *testing.T) {
	base := testutil.TempDir(t)
	target := filepath.Join(base, "target")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	j := filepath.Join(base, "junction")
	makeJunction(t, j, target)
	a, err := Resolve(filepath.Join(j, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Resolve(filepath.Join(target, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf(`Resolve(junction\sub) = %q, Resolve(target\sub) = %q`, a, b)
	}
}

// A junction whose target is spelled \\?\Volume{GUID}\..., as a folder
// mount point's is, is local and resolves (review 61 F7-S3). The junction
// points at a temp dir through the volume GUID of its own drive.
func TestResolveThroughVolumeGUIDLink(t *testing.T) {
	base, err := Resolve(testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "target")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	vol := filepath.VolumeName(base)
	root, _ := windows.UTF16PtrFromString(vol + `\`)
	buf := make([]uint16, 64)
	if err := windows.GetVolumeNameForVolumeMountPoint(root, &buf[0], uint32(len(buf))); err != nil { //nolint:gosec // fixed 64
		t.Skipf("no volume GUID for %s: %v", vol, err)
	}
	guid := windows.UTF16ToString(buf) // \\?\Volume{...}\
	if !isVolumeGUIDPath(guid) {
		t.Fatalf("isVolumeGUIDPath(%q) = false", guid)
	}
	j := filepath.Join(base, "mnt")
	makeJunction(t, j, guid+target[len(vol)+1:])
	if l, err := os.Readlink(j); err != nil || !isVolumeGUIDPath(l) {
		t.Skipf("the junction does not read back as a volume GUID path: %q, %v", l, err)
	}
	got, err := Resolve(filepath.Join(j, "sub"))
	if err != nil {
		t.Fatalf(`Resolve(mnt\sub) = %v`, err)
	}
	want, err := Resolve(filepath.Join(target, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf(`Resolve(mnt\sub) = %q, want %q`, got, want)
	}
}

func TestIsVolumeGUIDPath(t *testing.T) {
	for p, want := range map[string]bool{
		`\\?\Volume{0a1b2c3d-0000-0000-0000-00000000abcd}\`:        true,
		`\\?\volume{0A1B2C3D-0000-0000-0000-00000000ABCD}\dir\sub`: true,
		`\\?\Volume{0a1b2c3d-0000-0000-0000-00000000abcd}`:         true,
		`\\?\Volume{0a1b2c3d-0000-0000-0000-00000000abcd}x`:        false,
		`\\?\Volume{0a1b2c3d-0000-0000-0000-0000000Xabcd}\`:        false,
		`\\?\UNC\host\share`: false,
		`\??\Volume{0a1b2c3d-0000-0000-0000-00000000abcd}\`: false,
		`C:\dir`: false,
	} {
		if got := isVolumeGUIDPath(p); got != want {
			t.Errorf("isVolumeGUIDPath(%q) = %v, want %v", p, got, want)
		}
	}
}
