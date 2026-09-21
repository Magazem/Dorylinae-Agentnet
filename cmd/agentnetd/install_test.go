package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/service"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// stubPlatform drops one file under a temp dir so the CLI wiring can be
// exercised on any OS without touching launchd, systemd or Task Scheduler.
type stubPlatform struct{ file string }

func (stubPlatform) Name() string { return "stub" }

func (s stubPlatform) Install(service.Spec, service.Env) (service.Plan, error) {
	return service.Plan{Platform: "stub", Steps: []service.Step{
		{Op: service.OpWrite, Path: s.file, Content: "def", Mode: 0o600},
		{Op: service.OpRun, Args: []string{"stub", "start"}},
	}}, nil
}

func (s stubPlatform) Uninstall(service.Spec, service.Env) (service.Plan, error) {
	return service.Plan{Platform: "stub", Steps: []service.Step{
		{Op: service.OpRun, Args: []string{"stub", "stop"}, Ignore: true},
		{Op: service.OpRemove, Path: s.file},
	}}, nil
}

type recRunner struct {
	ran  []string
	fail bool
}

func (r *recRunner) Run(_ context.Context, args []string) ([]byte, error) {
	r.ran = append(r.ran, strings.Join(args, " "))
	if r.fail {
		return []byte("nope"), errors.New("exit status 1")
	}
	return nil, nil
}

func setup(t *testing.T) (home string, r *recRunner, def string) {
	t.Helper()
	home = filepath.Join(t.TempDir(), "home")
	def = filepath.Join(t.TempDir(), "service.def")
	r = &recRunner{}
	old := newServiceDeps
	newServiceDeps = func() (serviceDeps, error) {
		return serviceDeps{platform: stubPlatform{file: def}, runner: r, exe: "/bin/agentnetd"}, nil
	}
	t.Cleanup(func() { newServiceDeps = old })
	return home, r, def
}

func invoke(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func auditEvents(t *testing.T, home string) []audit.Event {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(home, "dorylinae.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	evs, err := audit.New(st.DB()).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestInstallDryRunChangesNothing(t *testing.T) {
	home, r, def := setup(t)
	code, out, errs := invoke(t, "install", "--dry-run", "--home", home)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"dry run (stub)", "write " + def, "run: stub start"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if len(r.ran) != 0 {
		t.Errorf("dry run executed commands: %v", r.ran)
	}
	if _, err := os.Stat(def); !os.IsNotExist(err) {
		t.Error("dry run wrote the service definition")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Error("dry run created the config directory")
	}
}

func TestInstallUninstallLifecycleAndAudit(t *testing.T) {
	home, r, def := setup(t)

	if code, out, errs := invoke(t, "install", "--home", home); code != 0 {
		t.Fatalf("install exit %d: %s %s", code, out, errs)
	}
	b, err := os.ReadFile(def) //nolint:gosec // path under t.TempDir()
	if err != nil || string(b) != "def" {
		t.Fatalf("definition not written: %q %v", b, err)
	}
	if strings.Join(r.ran, ";") != "stub start" {
		t.Errorf("commands = %v", r.ran)
	}

	if code, out, errs := invoke(t, "uninstall", "--home", home); code != 0 || !strings.Contains(out, "removed") {
		t.Fatalf("uninstall exit %d: %s %s", code, out, errs)
	}
	if _, err := os.Stat(def); !os.IsNotExist(err) {
		t.Error("uninstall left the definition behind")
	}

	// Idempotent: a second uninstall succeeds and says there was nothing to do.
	if code, out, errs := invoke(t, "uninstall", "--home", home); code != 0 || !strings.Contains(out, "nothing to do") {
		t.Fatalf("second uninstall exit %d: %s %s", code, out, errs)
	}

	evs := auditEvents(t, home)
	if len(evs) != 3 {
		t.Fatalf("want 3 audit events, got %d: %+v", len(evs), evs)
	}
	wantActions := []string{audit.ActionServiceInstall, audit.ActionServiceUninstall, audit.ActionServiceUninstall}
	wantChanged := []bool{true, true, false}
	for i, e := range evs {
		var d serviceAuditDetail
		if err := json.Unmarshal(e.Detail, &d); err != nil {
			t.Fatal(err)
		}
		if e.Action != wantActions[i] || e.Actor != audit.ActorCLI || d.Changed != wantChanged[i] || d.Platform != "stub" {
			t.Errorf("event %d = %s/%s %+v", i, e.Actor, e.Action, d)
		}
	}
}

func TestInstallFailureWritesNoAudit(t *testing.T) {
	home, r, _ := setup(t)
	r.fail = true
	code, _, errs := invoke(t, "install", "--home", home)
	if code != 1 || !strings.Contains(errs, "nope") {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	if evs := auditEvents(t, home); len(evs) != 0 {
		t.Errorf("failed install was audited: %+v", evs)
	}
}

func TestInstallHelpAndUsage(t *testing.T) {
	setup(t)
	code, out, _ := invoke(t, "install", "--help")
	if code != 0 {
		t.Fatalf("--help exit %d", code)
	}
	for _, want := range []string{"Usage:\n  agentnetd install [--home DIR] [--dry-run]", "--dry-run", "--home"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q:\n%s", want, out)
		}
	}
	if code, _, _ := invoke(t, "install", "extra"); code != 2 {
		t.Errorf("stray argument: exit %d, want 2", code)
	}
	if code, _, _ := invoke(t, "uninstall", "--bogus"); code != 2 {
		t.Errorf("bad flag: exit %d, want 2", code)
	}
}

func TestRunSubcommandStillParsesFlags(t *testing.T) {
	if code, out, _ := invoke(t, "run", "--version"); code != 0 || !strings.HasPrefix(out, "agentnetd") {
		t.Errorf("run --version: exit %d, %q", code, out)
	}
}
