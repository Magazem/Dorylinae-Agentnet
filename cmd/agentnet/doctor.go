package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/service"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

// Doctor check states (Docs/review/49-phase4-tickets.md §CLI contracts #doctor).
const (
	doctorOK   = "ok"
	doctorWarn = "warn"
	doctorFail = "fail"
	doctorSkip = "skip"
)

// doctorSocketTimeout bounds the "socket reachable within 1s" check.
const doctorSocketTimeout = 1 * time.Second

// doctorRelayTimeout bounds a doctor probe dial to the relay (never
// authenticated; see relayclient.Probe).
const doctorRelayTimeout = 3 * time.Second

// relayURLEnv and insecureRelayEnv mirror cmd/agentnetd's RelayEnv and
// InsecureRelayEnv: the only place a relay URL is discoverable without the
// daemon running (there is no other persisted, unauthenticated config
// doctor may read).
const (
	relayURLEnv      = "DORYLINAE_RELAY_URL"
	insecureRelayEnv = "DORYLINAE_ALLOW_INSECURE_RELAY"
	// relayCAFileName mirrors cmd/agentnetd's relayCAFile: the private CA
	// `agentnetd install --relay-ca` stores in the config directory.
	relayCAFileName = "relay_ca.pem"
)

// assumedChallengeTTL mirrors internal/relay's defaultChallengeTTL. It is not
// on the wire, so this is doctor's only way to recover the relay's clock from
// challenge.expires; the 2-minute budget is far larger than any plausible
// server-side TTL, so an inexact assumption here does not matter.
const assumedChallengeTTL = 10 * time.Second

// clockSkewBudget is the "within 2 minutes" of the clock check.
const clockSkewBudget = 2 * time.Minute

// doctorCheck is one row of `agentnet doctor`'s report.
type doctorCheck struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// doctorResult is the --json body of `agentnet doctor`.
type doctorResult struct {
	OK     bool          `json:"ok"`
	Checks []doctorCheck `json:"checks"`
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Check this machine's AgentNet setup: identity, config, service and relay.

Usage:
  agentnet doctor [--json]

Flags:
  --json   print machine-readable JSON on stdout

Never needs agentnetd to be running: with it stopped, the checks that need it
(service, socket) fail cleanly and the rest still run. Never signs in to the
relay with the identity key.

Exit codes: 0 no check failed (warn/skip still exit 0), 1 a check failed, 2 usage.
`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if fs.NArg() > 0 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}

	p, err := paths.Default()
	if err != nil {
		return failJSON(*asJSON, stdout, stderr, exitError, "doctor_error", err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), doctorRelayTimeout+doctorSocketTimeout+5*time.Second)
	defer cancel()

	checks := runDoctorChecks(ctx, p, service.ExecRunner{}, time.Now())
	result := doctorResult{OK: true, Checks: checks}
	for _, c := range checks {
		if c.State == doctorFail {
			result.OK = false
		}
	}
	exit := exitOK
	if !result.OK {
		exit = exitError
	}

	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(result)
		return exit
	}
	for _, c := range checks {
		line := fmt.Sprintf("%-9s %-4s %s", c.ID, c.State, c.Detail)
		if c.Fix != "" {
			line += " (fix: " + c.Fix + ")"
		}
		_, _ = fmt.Fprintln(stdout, line)
	}
	return exit
}

// runDoctorChecks runs every check in Docs/review/49-phase4-tickets.md
// §CLI contracts #doctor except "account" (added by ticket 4.2c; checkAccount
// is its extension point below).
func runDoctorChecks(ctx context.Context, p paths.Paths, run service.Runner, now time.Time) []doctorCheck {
	res, up := queryStatus(ctx, p)

	relayURL := ""
	if up && res.Relay != nil {
		relayURL = res.Relay.URL
	} else if !up {
		relayURL = os.Getenv(relayURLEnv)
	}

	// A single unauthenticated probe (never signs in, review 50 M4) serves
	// both "relay" (only when the daemon is down: when it is up, "relay"
	// trusts the daemon's own reported state instead) and "clock" (always:
	// the daemon does not keep the challenge it saw at its last handshake).
	var probed *probedRelay
	if relayURL != "" {
		roots, _ := loadRelayRoots(p.Dir)
		pctx, pcancel := context.WithTimeout(ctx, doctorRelayTimeout)
		ch, perr := relayclient.Probe(pctx, relayURL, roots)
		pcancel()
		probed = &probedRelay{challenge: ch, err: perr}
	}

	exe, _ := siblingDaemonPath()

	return []doctorCheck{
		checkBinary(up, res.Version, relayMinClient(res)),
		checkConfig(p),
		checkKeychain(p),
		checkService(ctx, run, exe, p.Dir, up),
		checkSocket(p, up),
		checkRelay(relayURL, up, res, probed),
		checkAccount(),
		checkGit(),
		checkClock(relayURL, probed, now),
	}
}

// queryStatus calls the daemon's "status" IPC method with the socket check's
// 1-second budget. Its success also answers the "socket" check, so doctor
// never dials twice.
func queryStatus(ctx context.Context, p paths.Paths) (res daemon.StatusResult, up bool) {
	cctx, cancel := context.WithTimeout(ctx, doctorSocketTimeout)
	defer cancel()
	return res, ipc.Call(cctx, p.Endpoint, "status", nil, &res) == nil
}

// checkBinary compares agentnet's and agentnetd's versions, then checks
// them against min_client, the oldest release the relay's `ready` frame
// says it supports (4.4a; empty: none, or the daemon is not connected yet).
func checkBinary(daemonUp bool, daemonVersion, minClient string) doctorCheck {
	return checkBinaryFor(version.Version, daemonUp, daemonVersion, minClient)
}

// relayMinClient is the relay's ready.min_client as the daemon reported it
// in status, or "" when there is none.
func relayMinClient(res daemon.StatusResult) string {
	if res.Relay == nil {
		return ""
	}
	return res.Relay.MinClient
}

// checkBinaryFor is checkBinary with this CLI's version injected, for tests.
func checkBinaryFor(cliVersion string, daemonUp bool, daemonVersion, minClient string) doctorCheck {
	if !daemonUp {
		return doctorCheck{ID: "binary", State: doctorSkip, Detail: "agentnetd is not running: cannot compare versions"}
	}
	if daemonVersion != cliVersion {
		return doctorCheck{ID: "binary", State: doctorFail,
			Detail: "agentnet and agentnetd report different versions",
			Fix:    "reinstall so the CLI and the daemon come from the same release"}
	}
	// min_client is relay text relayed by the daemon, which drops anything
	// that is not a release version; check again rather than print it
	// (review 53 L4).
	if _, ok := version.ParseRelease(minClient); !ok {
		return doctorCheck{ID: "binary", State: doctorOK, Detail: "agentnet and agentnetd versions match"}
	}
	meets, ok := version.MeetsMinimum(daemonVersion, minClient)
	switch {
	case !ok:
		return doctorCheck{ID: "binary", State: doctorWarn,
			Detail: "versions match; this is not a release build, so the relay's minimum " + minClient + " cannot be compared",
			Fix:    "install a release build (see Docs/cli/install.md)"}
	case !meets:
		return doctorCheck{ID: "binary", State: doctorFail,
			Detail: "version " + daemonVersion + " is older than the relay's minimum supported version " + minClient,
			Fix:    "upgrade agentnet and agentnetd to the latest release (see Docs/cli/install.md)"}
	}
	return doctorCheck{ID: "binary", State: doctorOK, Detail: "agentnet and agentnetd versions match and meet the relay's minimum " + minClient}
}

// checkConfig is the existing D24/L11 owner-only check (internal/device),
// reused here; a drive-root ACL like "Authenticated Users:(M)" is a warn with
// a fix, not a hard failure. Paths are printed relative to ~ (never a bare
// absolute path outside it).
func checkConfig(p paths.Paths) doctorCheck {
	return checkConfigWith(p, device.CheckProgramOwner)
}

// checkConfigWith is checkConfig with the ownership check injected, so a test
// can exercise the warn branch without building a real writable-by-others
// directory (OS-specific ACLs).
func checkConfigWith(p paths.Paths, checkOwner func(string) error) doctorCheck {
	if fi, err := os.Stat(p.Dir); err != nil || !fi.IsDir() {
		return doctorCheck{ID: "config", State: doctorFail,
			Detail: "the config directory does not exist",
			Fix:    "run `agentnetd install` (or `agentnet setup`)"}
	}
	if err := checkOwner(p.Dir); err != nil {
		var we *device.WritableError
		if errors.As(err, &we) {
			return doctorCheck{ID: "config", State: doctorWarn,
				Detail: fmt.Sprintf("%s can be changed by %s", displayRelHome(we.Path), we.Who),
				Fix:    "restrict the directory to your own user (and Administrators/root)"}
		}
		return doctorCheck{ID: "config", State: doctorFail, Detail: "could not check the config directory's owner"}
	}
	return doctorCheck{ID: "config", State: doctorOK, Detail: displayRelHome(p.Dir) + " exists and is owner-only"}
}

// displayRelHome renders path relative to the user's home (as "~/...") so
// doctor's output never carries an absolute path outside the config
// directory's own display. Paths outside the home directory, or when the
// home directory is unknown, are never printed at all.
func displayRelHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "the config directory"
	}
	return displayRelTo(home, path)
}

// displayRelTo is displayRelHome with home injected, for deterministic tests.
func displayRelTo(home, path string) string {
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "the config directory"
	}
	if rel == "." {
		return "~"
	}
	return "~" + string(filepath.Separator) + rel
}

// checkKeychain reports which backend holds the identity key, without ever
// creating one (doctor only reads).
func checkKeychain(p paths.Paths) doctorCheck {
	ks, err := identity.NewKeystoreFromEnv(p.Dir)
	if err != nil {
		return doctorCheck{ID: "keychain", State: doctorFail, Detail: "could not open the key storage"}
	}
	_, backend, err := ks.Load()
	switch {
	case err == nil:
		return doctorCheck{ID: "keychain", State: doctorOK, Detail: "identity key readable from " + backend}
	case errors.Is(err, keystore.ErrNotFound):
		return doctorCheck{ID: "keychain", State: doctorWarn,
			Detail: "no identity key yet",
			Fix:    "run `agentnet setup` (or `agentnetd`) to create one"}
	default:
		return doctorCheck{ID: "keychain", State: doctorFail,
			Detail: "the identity key is not readable",
			Fix:    "check keychain access or the key file's permissions"}
	}
}

// checkSocket reuses queryStatus's own 1-second, already-attempted dial: a
// second dial would not learn anything queryStatus did not already find out.
func checkSocket(p paths.Paths, up bool) doctorCheck {
	if runtime.GOOS == "darwin" && len(p.Endpoint) > 104 {
		return doctorCheck{ID: "socket", State: doctorFail,
			Detail: "the IPC socket path is longer than macOS's 104-byte limit",
			Fix:    "use a shorter $DORYLINAE_HOME"}
	}
	if !up {
		return doctorCheck{ID: "socket", State: doctorFail,
			Detail: "not reachable within 1s",
			Fix:    "start agentnetd (or your service manager)"}
	}
	return doctorCheck{ID: "socket", State: doctorOK, Detail: "reachable"}
}

// probedRelay is the result of one relayclient.Probe call, shared by the
// "relay" and "clock" checks.
type probedRelay struct {
	challenge relayclient.Challenge
	err       error
}

// checkRelay applies the URL scheme rule, then, with the daemon running,
// trusts its own reported connection state (never dialling itself); with it
// stopped, uses the shared unauthenticated probe.
func checkRelay(url string, daemonUp bool, res daemon.StatusResult, probed *probedRelay) doctorCheck {
	if url == "" {
		return doctorCheck{ID: "relay", State: doctorSkip, Detail: "no relay configured"}
	}
	if _, err := relayclient.CheckURL(url, allowInsecureRelay()); err != nil {
		return doctorCheck{ID: "relay", State: doctorFail, Detail: "relay URL: " + err.Error(),
			Fix: "a non-loopback relay must use wss://"}
	}
	if daemonUp {
		if res.Relay == nil {
			return doctorCheck{ID: "relay", State: doctorSkip, Detail: "no relay configured"}
		}
		if !res.Relay.Connected {
			detail := "not connected"
			if res.Relay.LastError != "" {
				detail += ": " + res.Relay.LastError
			}
			return doctorCheck{ID: "relay", State: doctorWarn, Detail: detail,
				Fix: "check network access to the relay; agentnetd retries automatically"}
		}
		return doctorCheck{ID: "relay", State: doctorOK, Detail: "connected, auth " + res.Relay.Auth}
	}
	if probed == nil || probed.err != nil {
		return doctorCheck{ID: "relay", State: doctorFail, Detail: "cannot reach the relay",
			Fix: "check the relay URL and network"}
	}
	if !slices.Contains(probed.challenge.Auth, envelope.AuthV2) {
		return doctorCheck{ID: "relay", State: doctorWarn, Detail: "the relay does not offer auth v2",
			Fix: "upgrade the relay"}
	}
	return doctorCheck{ID: "relay", State: doctorOK, Detail: "reachable, offers auth v2"}
}

// checkAccount is the extension point for ticket 4.2c (bound/unbound/
// suspended, quota group, quota state, from the daemon). It always skips: a
// half-implemented account check would be worse than an honest gap.
func checkAccount() doctorCheck {
	return doctorCheck{ID: "account", State: doctorSkip, Detail: "account support is added by a later ticket (4.2c)"}
}

// checkGit is D23: below Git 2.32, git.read grants are refused, but that does
// not stop the daemon, so it is a warn.
func checkGit() doctorCheck {
	if _, reason := capability.NewGitBackend(); reason != "" {
		return doctorCheck{ID: "git", State: doctorWarn, Detail: reason, Fix: "install Git 2.32 or newer"}
	}
	return doctorCheck{ID: "git", State: doctorOK, Detail: "git 2.32 or newer"}
}

// checkClock estimates the relay's clock from the shared probe's
// challenge.expires (the nonce window end, relay-now plus its undisclosed
// challenge TTL; see assumedChallengeTTL) and compares it with now.
func checkClock(url string, probed *probedRelay, now time.Time) doctorCheck {
	if url == "" {
		return doctorCheck{ID: "clock", State: doctorSkip, Detail: "no relay configured"}
	}
	if probed == nil || probed.err != nil {
		return doctorCheck{ID: "clock", State: doctorSkip, Detail: "could not reach the relay to check its clock"}
	}
	if probed.challenge.Expires.IsZero() {
		return doctorCheck{ID: "clock", State: doctorSkip, Detail: "the relay did not report an expiry"}
	}
	relayNow := probed.challenge.Expires.Add(-assumedChallengeTTL)
	skew := now.Sub(relayNow)
	if skew < 0 {
		skew = -skew
	}
	if skew > clockSkewBudget {
		return doctorCheck{ID: "clock", State: doctorFail,
			Detail: "the local clock differs from the relay's by more than 2 minutes",
			Fix:    "sync the system clock (NTP)"}
	}
	return doctorCheck{ID: "clock", State: doctorOK, Detail: "within 2 minutes of the relay"}
}

func allowInsecureRelay() bool { return os.Getenv(insecureRelayEnv) == "1" }

// loadRelayRoots reads the private CA `agentnetd install --relay-ca` stored
// in the config directory, if any; nil (system roots) when there is none.
func loadRelayRoots(dir string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(filepath.Join(dir, relayCAFileName)) //nolint:gosec // fixed name inside the config dir
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return relayclient.LoadRoots(pem)
}

// siblingDaemonPath guesses agentnetd's path as the file next to this
// binary: how install.sh, the Homebrew formula and the Windows MSI lay
// binaries out. It is a best-effort input to the "service" check's "points
// at this binary" test, not a hard requirement (doctor.md documents the
// limitation).
func siblingDaemonPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	name := "agentnetd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(self), name), nil
}

// serviceStatus is what the OS-level per-user service definition says.
type serviceStatus struct {
	installed bool
	running   bool
	// matches is whether the definition names exe and home; false when it
	// could not be determined (never treated as authoritative on its own).
	matches bool
}

// checkService trusts a running daemon it can already reach over IPC (up):
// that is a stronger signal than parsing the installed definition. With the
// daemon down it probes the OS-level definition directly, the same
// command-line tools service.Uninstall's existence probe uses internally,
// without depending on internal/service beyond its exported names.
func checkService(ctx context.Context, run service.Runner, exe, home string, up bool) doctorCheck {
	if up {
		return doctorCheck{ID: "service", State: doctorOK, Detail: "agentnetd is reachable over IPC"}
	}
	st, err := probeService(ctx, run, exe, home)
	if errors.Is(err, service.ErrUnsupported) {
		return doctorCheck{ID: "service", State: doctorWarn,
			Detail: "per-user service install is not supported on this OS",
			Fix:    "start agentnetd yourself"}
	}
	if !st.installed {
		return doctorCheck{ID: "service", State: doctorFail,
			Detail: "no per-user service is installed",
			Fix:    "run `agentnetd install`"}
	}
	if !st.running {
		return doctorCheck{ID: "service", State: doctorFail,
			Detail: "the service is installed but not running",
			Fix:    "log out and back in, or run `agentnetd install` again"}
	}
	if !st.matches {
		return doctorCheck{ID: "service", State: doctorWarn,
			Detail: "the installed service does not clearly point at this binary and home",
			Fix:    "run `agentnetd install` again"}
	}
	return doctorCheck{ID: "service", State: doctorOK, Detail: "installed and running"}
}

// probeService dispatches to the platform-specific probe. Each one is a pure
// function of (Runner, exe, home) so it is testable on any host OS with a
// fake Runner, independent of runtime.GOOS.
func probeService(ctx context.Context, run service.Runner, exe, home string) (serviceStatus, error) {
	switch runtime.GOOS {
	case "windows":
		return probeServiceWindows(ctx, run, exe, home), nil
	case "darwin":
		return probeServiceDarwin(ctx, run, exe, home), nil
	case "linux":
		return probeServiceLinux(ctx, run, exe, home), nil
	default:
		return serviceStatus{}, service.ErrUnsupported
	}
}

func probeServiceWindows(ctx context.Context, run service.Runner, exe, home string) serviceStatus {
	out, err := run.Run(ctx, []string{"schtasks.exe", "/Query", "/TN", service.TaskName, "/XML"})
	if err != nil {
		return serviceStatus{}
	}
	s := string(out)
	running := false
	if ro, rerr := run.Run(ctx, []string{"schtasks.exe", "/Query", "/TN", service.TaskName, "/FO", "LIST", "/V"}); rerr == nil {
		running = strings.Contains(string(ro), "Running")
	}
	return serviceStatus{installed: true, running: running, matches: strings.Contains(s, exe) && strings.Contains(s, home)}
}

func probeServiceDarwin(ctx context.Context, run service.Runner, exe, home string) serviceStatus {
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + service.LaunchdLabel
	out, err := run.Run(ctx, []string{"launchctl", "print", target})
	if err != nil {
		return serviceStatus{}
	}
	s := string(out)
	running := strings.Contains(s, "state = running")
	return serviceStatus{installed: true, running: running, matches: strings.Contains(s, exe) && strings.Contains(s, home)}
}

func probeServiceLinux(ctx context.Context, run service.Runner, exe, home string) serviceStatus {
	out, err := run.Run(ctx, []string{"systemctl", "--user", "cat", service.SystemdUnit})
	if err != nil {
		return serviceStatus{}
	}
	s := string(out)
	running := false
	if ro, rerr := run.Run(ctx, []string{"systemctl", "--user", "is-active", service.SystemdUnit}); rerr == nil {
		running = strings.TrimSpace(string(ro)) == "active"
	}
	return serviceStatus{installed: true, running: running, matches: strings.Contains(s, exe) && strings.Contains(s, home)}
}
