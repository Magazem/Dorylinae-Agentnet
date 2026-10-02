package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/service"
)

// --- checkBinary ---

func TestCheckBinaryPass(t *testing.T) {
	got := checkBinary(true, versionForTest(), "")
	if got.State != doctorOK {
		t.Fatalf("state = %q, want ok: %+v", got.State, got)
	}
}

func TestCheckBinaryFailOnMismatch(t *testing.T) {
	got := checkBinary(true, versionForTest()+"-other", "")
	if got.State != doctorFail {
		t.Fatalf("state = %q, want fail: %+v", got.State, got)
	}
}

func TestCheckBinarySkipsWithoutDaemon(t *testing.T) {
	got := checkBinary(false, "", "")
	if got.State != doctorSkip {
		t.Fatalf("state = %q, want skip: %+v", got.State, got)
	}
}

func TestCheckBinaryMinClient(t *testing.T) {
	tests := []struct {
		name, v, min string
		want         string
	}{
		{"no minimum", "1.2.3", "", doctorOK},
		{"meets", "1.2.3", "1.2.3", doctorOK},
		{"newer", "1.10.0", "1.9.0", doctorOK},
		{"older", "1.2.2", "1.2.3", doctorFail},
		{"dev build", "0.0.0-dev+abc", "1.0.0", doctorWarn},
		{"junk minimum", "1.2.3", "9.9.9\x1b[2J", doctorOK},
	}
	for _, tc := range tests {
		got := checkBinaryFor(tc.v, true, tc.v, tc.min)
		if got.State != tc.want {
			t.Errorf("%s: state = %q, want %q: %+v", tc.name, got.State, tc.want, got)
		}
	}
	if got := relayMinClient(daemon.StatusResult{Relay: &daemon.RelayStatus{MinClient: "1.2.3"}}); got != "1.2.3" {
		t.Errorf("relayMinClient = %q", got)
	}
	if got := relayMinClient(daemon.StatusResult{}); got != "" {
		t.Errorf("relayMinClient without relay = %q", got)
	}
}

// --- checkConfigWith ---

func TestCheckConfigPass(t *testing.T) {
	dir := t.TempDir()
	got := checkConfigWith(paths.Paths{Dir: dir}, func(string) error { return nil }, func(string) error { return nil })
	if got.State != doctorOK {
		t.Fatalf("state = %q, want ok: %+v", got.State, got)
	}
}

func TestCheckConfigFailMissing(t *testing.T) {
	got := checkConfigWith(paths.Paths{Dir: filepath.Join(t.TempDir(), "missing")}, func(string) error { return nil }, func(string) error { return nil })
	if got.State != doctorFail {
		t.Fatalf("state = %q, want fail: %+v", got.State, got)
	}
}

func TestCheckConfigWarnOnWritableByOthers(t *testing.T) {
	dir := t.TempDir()
	we := &device.WritableError{Path: dir, Who: "Everyone"}
	got := checkConfigWith(paths.Paths{Dir: dir}, func(string) error { return we }, func(string) error { return nil })
	if got.State != doctorWarn || got.Fix == "" {
		t.Fatalf("got = %+v, want warn with a fix", got)
	}
}

// Review 55 R55-089: a directory others can read is not reported as
// owner-only.
func TestCheckConfigWarnOnReadableByOthers(t *testing.T) {
	dir := t.TempDir()
	np := &paths.NotPrivateError{Path: dir, Who: "Users"}
	got := checkConfigWith(paths.Paths{Dir: dir}, func(string) error { return nil }, func(string) error { return np })
	if got.State != doctorWarn || got.Fix == "" || strings.Contains(got.Detail, "owner-only") {
		t.Fatalf("got = %+v, want warn with a fix", got)
	}
}

// --- displayRelHome ---

func TestDisplayRelHomeNeverLeaksOutsidePaths(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if got := displayRelTo(home, home); got != "~" {
		t.Errorf("home dir = %q, want ~", got)
	}
	if got := displayRelTo(home, filepath.Join(home, "dorylinae")); got != "~"+string(filepath.Separator)+"dorylinae" {
		t.Errorf("under home = %q", got)
	}
	outside := filepath.Join(filepath.Dir(home), "elsewhere")
	if got := displayRelTo(home, outside); got != "the config directory" || strings.Contains(got, outside) {
		t.Errorf("outside home = %q, must not leak the path", got)
	}
}

// --- checkKeychain ---

func TestCheckKeychainPass(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	dir := t.TempDir()
	ks, err := identity.NewKeystore(dir, "file")
	if err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ks.Save(priv.Seed()); err != nil {
		t.Fatal(err)
	}
	got := checkKeychain(paths.Paths{Dir: dir})
	if got.State != doctorOK {
		t.Fatalf("state = %q, want ok: %+v", got.State, got)
	}
}

func TestCheckKeychainWarnWhenAbsent(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	got := checkKeychain(paths.Paths{Dir: t.TempDir()})
	if got.State != doctorWarn || got.Fix == "" {
		t.Fatalf("got = %+v, want warn with a fix", got)
	}
}

// Review 87 M1: a key that does not match the agent card (a stray or planted
// identity.key) is a failure, as it is at start-up; so is a card whose key is
// gone.
func TestCheckKeychainFailsWhenKeyDoesNotMatchCard(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	dir := t.TempDir()
	ks, err := identity.NewKeystore(dir, "file")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := checkKeychain(paths.Paths{Dir: dir}); got.State != doctorOK {
		t.Fatalf("matching key: %+v", got)
	}
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ks.Save(other.Seed()); err != nil {
		t.Fatal(err)
	}
	if got := checkKeychain(paths.Paths{Dir: dir}); got.State != doctorFail || got.Fix == "" {
		t.Fatalf("key not matching the card: %+v, want fail with a fix", got)
	}
	if err := os.Remove(filepath.Join(dir, identity.KeyFile)); err != nil {
		t.Fatal(err)
	}
	if got := checkKeychain(paths.Paths{Dir: dir}); got.State != doctorFail || got.Fix == "" {
		t.Fatalf("card without its key: %+v, want fail with a fix", got)
	}
}

// --- checkSocket ---

func TestCheckSocketPass(t *testing.T) {
	got := checkSocket(paths.Paths{Endpoint: "short"}, true)
	if got.State != doctorOK {
		t.Fatalf("state = %q, want ok: %+v", got.State, got)
	}
}

func TestCheckSocketFailWhenDaemonDown(t *testing.T) {
	got := checkSocket(paths.Paths{Endpoint: "short"}, false)
	if got.State != doctorFail || got.Fix == "" {
		t.Fatalf("got = %+v, want fail with a fix", got)
	}
}

// --- checkAccount ---

func TestCheckAccountAlwaysSkips(t *testing.T) {
	if got := checkAccount(); got.State != doctorSkip {
		t.Fatalf("state = %q, want skip: %+v", got.State, got)
	}
}

// --- checkGit ---

func TestCheckGitPass(t *testing.T) {
	got := checkGit()
	if got.State != doctorOK {
		t.Skipf("git on this runner: %+v (acceptable if genuinely absent/old)", got)
	}
}

func TestCheckGitFailWhenNotOnPath(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	got := checkGit()
	if got.State != doctorWarn || got.Fix == "" {
		t.Fatalf("got = %+v, want warn with a fix", got)
	}
}

// --- checkRelay / checkClock (pure, no network) ---

func TestCheckRelaySkipsWithNoURL(t *testing.T) {
	if got := checkRelay("", false, daemon.StatusResult{}, nil); got.State != doctorSkip {
		t.Fatalf("state = %q, want skip: %+v", got.State, got)
	}
}

func TestCheckRelayFailsOnBadScheme(t *testing.T) {
	got := checkRelay("http://example.com", false, daemon.StatusResult{}, nil)
	if got.State != doctorFail {
		t.Fatalf("state = %q, want fail: %+v", got.State, got)
	}
}

func TestCheckRelayFromDaemonConnected(t *testing.T) {
	res := daemon.StatusResult{Relay: &daemon.RelayStatus{URL: "wss://relay.example", Connected: true, Auth: "v2"}}
	got := checkRelay("wss://relay.example", true, res, nil)
	if got.State != doctorOK {
		t.Fatalf("state = %q, want ok: %+v", got.State, got)
	}
}

func TestCheckRelayFromDaemonDisconnected(t *testing.T) {
	res := daemon.StatusResult{Relay: &daemon.RelayStatus{URL: "wss://relay.example", Connected: false, LastError: "dial: timeout"}}
	got := checkRelay("wss://relay.example", true, res, nil)
	if got.State != doctorWarn || !strings.Contains(got.Detail, "timeout") {
		t.Fatalf("got = %+v, want warn mentioning the last error", got)
	}
}

func TestCheckRelayProbedOK(t *testing.T) {
	probed := &probedRelay{challenge: relayclient.Challenge{Auth: []string{"v1", "v2"}}}
	got := checkRelay("wss://relay.example", false, daemon.StatusResult{}, probed)
	if got.State != doctorOK {
		t.Fatalf("state = %q, want ok: %+v", got.State, got)
	}
}

func TestCheckRelayProbedUnreachable(t *testing.T) {
	probed := &probedRelay{err: errors.New("dial: connection refused")}
	got := checkRelay("wss://relay.example", false, daemon.StatusResult{}, probed)
	if got.State != doctorFail {
		t.Fatalf("state = %q, want fail: %+v", got.State, got)
	}
}

func TestCheckRelayProbedNoAuthV2(t *testing.T) {
	probed := &probedRelay{challenge: relayclient.Challenge{Auth: []string{"v1"}}}
	got := checkRelay("wss://relay.example", false, daemon.StatusResult{}, probed)
	if got.State != doctorWarn {
		t.Fatalf("state = %q, want warn: %+v", got.State, got)
	}
}

func TestCheckClockSkipsWithNoURL(t *testing.T) {
	if got := checkClock("", nil, time.Now()); got.State != doctorSkip {
		t.Fatalf("state = %q, want skip: %+v", got.State, got)
	}
}

func TestCheckClockPassWithinBudget(t *testing.T) {
	now := time.Now()
	probed := &probedRelay{challenge: relayclient.Challenge{Expires: now.Add(assumedChallengeTTL)}}
	got := checkClock("wss://relay.example", probed, now)
	if got.State != doctorOK {
		t.Fatalf("state = %q, want ok: %+v", got.State, got)
	}
}

func TestCheckClockFailsOutsideBudget(t *testing.T) {
	now := time.Now()
	skewed := now.Add(10 * time.Minute)
	probed := &probedRelay{challenge: relayclient.Challenge{Expires: skewed.Add(assumedChallengeTTL)}}
	got := checkClock("wss://relay.example", probed, now)
	if got.State != doctorFail || got.Fix == "" {
		t.Fatalf("got = %+v, want fail with a fix", got)
	}
}

// --- checkService with a fake Runner ---

// fakeServiceRunner answers only the commands explicitly marked ok; every
// other command (in particular, one probeService never calls) fails, the way
// a real OS command fails when nothing is installed.
type fakeServiceRunner struct {
	ok  map[string]bool
	out map[string][]byte
}

func (f fakeServiceRunner) Run(_ context.Context, args []string) ([]byte, error) {
	key := strings.Join(args, " ")
	if !f.ok[key] {
		return nil, fmt.Errorf("fake: %s: not found", key)
	}
	return f.out[key], nil
}

func TestCheckServiceOKWhenDaemonUp(t *testing.T) {
	got := checkService(context.Background(), fakeServiceRunner{}, "", "", true)
	if got.State != doctorOK {
		t.Fatalf("state = %q, want ok: %+v", got.State, got)
	}
}

func TestCheckServiceFailsWhenNothingInstalled(t *testing.T) {
	got := checkService(context.Background(), fakeServiceRunner{}, "/bin/agentnetd", "/home/x", false)
	if got.State != doctorFail || got.Fix == "" {
		t.Fatalf("got = %+v, want fail with a fix", got)
	}
}

// --- probeService{Windows,Darwin,Linux}, tested directly regardless of host OS ---

func TestProbeServiceWindows(t *testing.T) {
	exe, home := `C:\Program Files\AgentNet\agentnetd.exe`, `C:\Users\x\AppData\Roaming\dorylinae`
	xmlKey := "schtasks.exe /Query /TN " + service.TaskName + " /XML"
	verKey := "powershell.exe -NoProfile -NonInteractive -Command [int](Get-ScheduledTask -TaskName '" + service.TaskName + "').State"

	installedRunning := fakeServiceRunner{
		ok: map[string]bool{xmlKey: true, verKey: true},
		out: map[string][]byte{
			xmlKey: []byte("<Command>" + exe + "</Command><Arguments>run --home " + home + "</Arguments>"),
			verKey: []byte("4"), // a localised schtasks says anything but "Running" (R55-177)
		},
	}
	st := probeServiceWindows(context.Background(), installedRunning, exe, home)
	if !st.installed || !st.running || !st.matches {
		t.Fatalf("installed+running = %+v", st)
	}

	st = probeServiceWindows(context.Background(), fakeServiceRunner{}, exe, home)
	if st.installed {
		t.Fatalf("not installed reported installed: %+v", st)
	}
}

func TestProbeServiceDarwin(t *testing.T) {
	exe, home := "/usr/local/bin/agentnetd", "/Users/x/.config/dorylinae"
	key := "launchctl print gui/" + strconv.Itoa(os.Getuid()) + "/" + service.LaunchdLabel

	installedRunning := fakeServiceRunner{
		ok:  map[string]bool{key: true},
		out: map[string][]byte{key: []byte("state = running\n\t\"ProgramArguments\" => [" + exe + " " + home + "]")},
	}
	st := probeServiceDarwin(context.Background(), installedRunning, exe, home)
	if !st.installed || !st.running || !st.matches {
		t.Fatalf("installed+running = %+v", st)
	}

	st = probeServiceDarwin(context.Background(), fakeServiceRunner{}, exe, home)
	if st.installed {
		t.Fatalf("not installed reported installed: %+v", st)
	}
}

func TestProbeServiceLinux(t *testing.T) {
	exe, home := "/usr/local/bin/agentnetd", "/home/x/.config/dorylinae"
	catKey := "systemctl --user cat " + service.SystemdUnit
	activeKey := "systemctl --user is-active " + service.SystemdUnit
	unitContent := []byte("ExecStart=\"" + exe + "\" run --home \"" + home + "\"\n")

	installedRunning := fakeServiceRunner{
		ok:  map[string]bool{catKey: true, activeKey: true},
		out: map[string][]byte{catKey: unitContent, activeKey: []byte("active\n")},
	}
	st := probeServiceLinux(context.Background(), installedRunning, exe, home)
	if !st.installed || !st.running || !st.matches {
		t.Fatalf("installed+running = %+v", st)
	}

	installedStopped := fakeServiceRunner{
		ok:  map[string]bool{catKey: true, activeKey: true},
		out: map[string][]byte{catKey: unitContent, activeKey: []byte("inactive\n")},
	}
	st = probeServiceLinux(context.Background(), installedStopped, exe, home)
	if !st.installed || st.running {
		t.Fatalf("installed+stopped = %+v", st)
	}

	st = probeServiceLinux(context.Background(), fakeServiceRunner{}, exe, home)
	if st.installed {
		t.Fatalf("not installed reported installed: %+v", st)
	}
}

// --- end-to-end: runDoctorChecks against a real daemon ---

func TestDoctorRunsUsefulChecksWithDaemonStopped(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	p := shortHome(t)
	checks := runDoctorChecks(context.Background(), p, service.ExecRunner{}, time.Now())

	byID := map[string]doctorCheck{}
	for _, c := range checks {
		byID[c.ID] = c
	}
	for _, id := range []string{"socket", "service"} {
		if byID[id].State != doctorFail {
			t.Errorf("%s = %+v, want fail with the daemon stopped", id, byID[id])
		}
	}
	for _, id := range []string{"config", "keychain", "git", "account", "binary", "relay", "clock"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("%s did not run with the daemon stopped", id)
		}
	}
	if byID["binary"].State != doctorSkip {
		t.Errorf("binary = %+v, want skip without the daemon", byID["binary"])
	}
}

func TestDoctorReportsRelayWhenDaemonRunning(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	hs := httptest.NewUnstartedServer(nil)
	origin := "ws://" + hs.Listener.Addr().String()
	srv := relay.New(relay.Options{Public: true, Origins: []string{origin}})
	hs.Config.Handler = srv
	hs.Start()
	t.Cleanup(hs.Close)
	t.Cleanup(srv.Close)
	relayURL := "ws" + strings.TrimPrefix(hs.URL, "http")

	p := shortHome(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.RunWithOptions(ctx, p, ready, daemon.Options{RelayURL: relayURL}) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}

	deadline := time.Now().Add(10 * time.Second)
	var checks []doctorCheck
	for {
		checks = runDoctorChecks(ctx, p, service.ExecRunner{}, time.Now())
		byID := map[string]doctorCheck{}
		for _, c := range checks {
			byID[c.ID] = c
		}
		if byID["relay"].State == doctorOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay check never went ok: %+v", byID["relay"])
		}
		time.Sleep(20 * time.Millisecond)
	}

	byID := map[string]doctorCheck{}
	for _, c := range checks {
		byID[c.ID] = c
	}
	if byID["socket"].State != doctorOK {
		t.Errorf("socket = %+v, want ok with the daemon up", byID["socket"])
	}
	if byID["binary"].State != doctorOK {
		t.Errorf("binary = %+v, want ok (same build)", byID["binary"])
	}
	if byID["service"].State != doctorOK {
		t.Errorf("service = %+v, want ok (trusts a reachable daemon)", byID["service"])
	}
}

// TestDoctorOutputHasNoPathsOutsideConfigDir is the marker test (4.4c
// acceptance): doctor's output (JSON here) must carry no path outside the
// config directory's own "~"-relative display, and no peer names (doctor
// never touches peers at all, so this mostly guards the future).
func TestDoctorOutputHasNoPathsOutsideConfigDir(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	p := shortHome(t) // p.Dir is a temp dir outside the real home

	var out, errb bytes.Buffer
	code := run([]string{"doctor", "--json"}, &out, &errb)
	if code != exitError && code != exitOK {
		t.Fatalf("doctor --json: code=%d stderr=%q", code, errb.String())
	}
	var result doctorResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out.String(), err)
	}
	raw := out.String()
	if strings.Contains(raw, p.Dir) {
		t.Fatalf("doctor output leaks the config dir path: %q", raw)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.Contains(raw, home) {
		t.Fatalf("doctor output leaks the home directory: %q", raw)
	}
}

func versionForTest() string {
	var out, errb bytes.Buffer
	run([]string{"version", "--json"}, &out, &errb)
	var body struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(out.Bytes(), &body)
	return body.Version
}

func TestRelayFromDefinition(t *testing.T) {
	for name, tc := range map[string]struct{ def, want string }{
		"schtasks": {`<Arguments>run --home C:\h --relay wss://r.example/ws --log-file C:\h\l</Arguments>`, "wss://r.example/ws"},
		"quoted":   {`run --home "C:\a b" --relay "wss://r.example/a b"`, "wss://r.example/a b"},
		"systemd":  {"ExecStart=/x/agentnetd run --home /h --relay 'wss://r.example/ws'", "wss://r.example/ws"},
		"launchd":  {"<string>--relay</string>\n\t\t<string>wss://r.example/?a=1&amp;b=2</string>", "wss://r.example/?a=1&b=2"},
		"none":     {"run --home /h", ""},
	} {
		if got := relayFromDefinition(tc.def); got != tc.want {
			t.Errorf("%s: relayFromDefinition = %q, want %q", name, got, tc.want)
		}
	}
}

// R55-131: with the daemon down and no env var, doctor learns the relay from
// the installed service definition.
func TestProbeServiceReportsRelay(t *testing.T) {
	exe, home := "/x/agentnetd", "/h"
	r := fakeServiceRunner{
		ok:  map[string]bool{"systemctl --user cat " + service.SystemdUnit: true},
		out: map[string][]byte{"systemctl --user cat " + service.SystemdUnit: []byte("ExecStart=/x/agentnetd run --home /h --relay wss://r.example/ws")},
	}
	if st := probeServiceLinux(context.Background(), r, exe, home); st.relay != "wss://r.example/ws" {
		t.Fatalf("relay = %q", st.relay)
	}
}

// R55-134: with the daemon up, its own URL rule applies, not doctor's env.
func TestCheckRelayDaemonUpIgnoresDoctorInsecureEnv(t *testing.T) {
	t.Setenv(insecureRelayEnv, "")
	res := daemon.StatusResult{Relay: &daemon.RelayStatus{URL: "ws://relay.example/ws", Connected: true, Auth: "ok"}}
	if got := checkRelay("ws://relay.example/ws", true, res, nil); got.State != doctorOK {
		t.Fatalf("daemon up: %+v", got)
	}
	if got := checkRelay("ws://relay.example/ws", false, daemon.StatusResult{}, nil); got.State != doctorFail {
		t.Fatalf("daemon down, no insecure env: %+v", got)
	}
}
