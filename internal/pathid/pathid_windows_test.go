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
