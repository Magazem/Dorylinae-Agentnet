package daemon

// Windows spellings of forbidden resources (review 55 C11-04, T4-01, C11-02;
// R55-006 in Docs/review/55-code-review/99-report.md). Each must be refused
// at issuance, so nothing is served.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
	"golang.org/x/sys/windows"
)

func readViaFS(rec capability.Record, rel string) (string, error) {
	var out []byte
	err := capability.FSBackend{}.Read(context.Background(), rec, rel, 0, 64, func(f capability.Fragment) error {
		out = append(out, f.Data...)
		return nil
	})
	return string(out), err
}

// mustRefuse fails the test if raw is accepted, and reports whether the
// config dir's database is then served.
func mustRefuse(t *testing.T, cfg, name, raw string) {
	t.Helper()
	resolved, ierr := validateResource(cfg, raw)
	if ierr == nil {
		data, err := readViaFS(capability.Record{Path: resolved, Action: capability.ActionFSRead}, "dorylinae.db")
		t.Errorf("%s: %q accepted as %q; served read err=%v data=%q", name, raw, resolved, err, data)
		return
	}
	t.Logf("%s: %q refused: %s", name, raw, ierr.Message)
}

// C11-04: the config dir and home spelled with \\?\, \\.\, UNC and
// GLOBALROOT prefixes.
func TestValidateResourceRefusesPrefixedSpellings(t *testing.T) {
	cfg, _ := identityDirs(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		`\\?\` + cfg, `\\?\` + home, `\\?\C:\`, `\\.\C:\`, `\\localhost\C$`, `\\localhost\C$\Users`,
		`\\127.0.0.1\C$\` + home[3:], `\\?\GLOBALROOT\Device\HarddiskVolume3\`,
		`\\?\UNC\localhost\C$\` + home[3:], `\\localhost\C$\` + cfg[3:],
	} {
		mustRefuse(t, cfg, "prefixed", p)
	}
}

// T4-01: the NTFS directory streams, an 8.3 short name, a trailing dot or
// space, and the volume-GUID form of the config dir.
func TestValidateResourceRefusesNTFSSpellings(t *testing.T) {
	cfg, _ := identityDirs(t)
	spellings := map[string]string{
		"index_allocation": cfg + `::$INDEX_ALLOCATION`,
		"index_alloc_i30":  cfg + `:$I30:$INDEX_ALLOCATION`,
		"trailing_dot":     cfg + `.`,
		"trailing_space":   cfg + ` `,
	}
	if p, err := windows.UTF16PtrFromString(cfg); err == nil {
		buf := make([]uint16, 1024)
		if n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf))); err == nil && n > 0 { //nolint:gosec // fixed 1024
			spellings["short_8dot3"] = windows.UTF16ToString(buf[:n])
		}
	}
	vol := make([]uint16, 64)
	root, _ := windows.UTF16PtrFromString(filepath.VolumeName(cfg) + `\`)
	if err := windows.GetVolumeNameForVolumeMountPoint(root, &vol[0], uint32(len(vol))); err == nil { //nolint:gosec // fixed 64
		spellings["volume_guid"] = windows.UTF16ToString(vol) + cfg[len(filepath.VolumeName(cfg))+1:]
	}
	for name, raw := range spellings {
		mustRefuse(t, cfg, name, raw)
	}
}

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

// T4-01: a subst drive letter onto the config dir, onto its parent (which
// contains it), and onto the home dir; a project dir on a subst drive stays
// allowed, stored as the directory the drive stands for.
func TestValidateResourceSubst(t *testing.T) {
	cfg, proj := identityDirs(t)
	subst := func(target string) string {
		t.Helper()
		drive := freeDrive()
		if drive == "" {
			t.Skip("no free drive letter")
		}
		if out, err := exec.Command("subst", drive, target).CombinedOutput(); err != nil { //nolint:gosec // test: fixed command, temp dir
			t.Skipf("subst: %v %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("subst", drive, "/D").Run() }) //nolint:gosec // test: fixed command
		return drive
	}
	d := subst(filepath.Dir(cfg))
	mustRefuse(t, cfg, "subst_parent", d+`\`)
	mustRefuse(t, cfg, "subst_cfg", d+`\dorylinae-config`)
	resolved, ierr := validateResource(cfg, d+`\proj`)
	if ierr != nil {
		t.Fatalf("project dir on a subst drive refused: %s", ierr.Message)
	}
	want, _ := validateResource(cfg, proj)
	if resolved != want {
		t.Fatalf("subst project resolved to %q, want %q", resolved, want)
	}
	// A home outside the config dir's tree, so that only the home rule applies.
	home := filepath.Join(testutil.TempDir(t), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", home)
	_, ierr = validateResource(cfg, subst(home)+`\`)
	if ierr == nil || !strings.Contains(ierr.Message, "home directory") {
		t.Fatalf("subst of the home dir: %v, want the home-directory refusal", ierr)
	}
}

func mklinkJ(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil { //nolint:gosec // test: fixed command, temp dirs
		t.Fatalf("mklink /J: %v %s", err, out)
	}
}

// C11-02: a junction to the config dir is refused, and so is a resource
// replaced by one after issuance (the approval recheck).
func TestValidateResourceRefusesJunctionToConfigDir(t *testing.T) {
	cfg, proj := identityDirs(t)
	j := filepath.Join(proj, "innocent")
	mklinkJ(t, j, cfg)
	mustRefuse(t, cfg, "junction", j)

	other := filepath.Join(proj, "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, ierr := validateResource(cfg, other)
	if ierr != nil {
		t.Fatalf("allowed dir refused: %s", ierr.Message)
	}
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	mklinkJ(t, other, cfg)
	if err := recheckResource(cfg, capability.Record{Path: resolved, Action: capability.ActionFSRead}); err == nil {
		t.Fatal("recheck accepted a resource swapped for a junction to the config dir")
	}
	if _, err := readViaFS(capability.Record{Path: resolved, Action: capability.ActionFSRead}, "dorylinae.db"); err == nil {
		t.Log("note: serving follows a root swapped by a local writer (out of scope, review 55 C09)")
	}
}

// C11-02: the stock "%USERPROFILE%\Application Data" junction points at
// %APPDATA% (Roaming), which holds the default config dir.
func TestValidateResourceRefusesStockApplicationDataJunction(t *testing.T) {
	home, _ := os.UserHomeDir()
	j := filepath.Join(home, "Application Data")
	if _, err := os.Lstat(j); err != nil {
		t.Skipf("no stock junction: %v", err)
	}
	cfgBase, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := os.MkdirTemp(cfgBase, "dn-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cfg) })
	if resolved, ierr := validateResource(cfg, j); ierr == nil {
		t.Fatalf("the Application Data junction (containing the config dir) accepted as %q", resolved)
	}
}
