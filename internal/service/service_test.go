package service

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

var (
	unixSpec = Spec{Executable: "/opt/dorylinae/agentnetd", Home: "/home/ann/.config/dorylinae"}
	unixEnv  = Env{HomeDir: "/home/ann", UID: 501, User: "ann"}
	winSpec  = Spec{Executable: `C:\Program Files\Dorylinae\agentnetd.exe`, Home: `C:\Users\ann\AppData\Roaming\dorylinae`}
	winEnv   = Env{HomeDir: `C:\Users\ann`, UID: -1, User: `DESKTOP\ann`}
)

func TestLaunchdPlist(t *testing.T) {
	plan, err := Launchd{}.Install(unixSpec, unixEnv)
	if err != nil {
		t.Fatal(err)
	}
	w := plan.Steps[0]
	if w.Path != "/home/ann/Library/LaunchAgents/dev.dorylinae.agentnetd.plist" {
		t.Errorf("plist path = %q", w.Path)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.dorylinae.agentnetd</string>
	<key>ProgramArguments</key>
	<array>
		<string>/opt/dorylinae/agentnetd</string>
		<string>run</string>
		<string>--home</string>
		<string>/home/ann/.config/dorylinae</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>/home/ann/.config/dorylinae/agentnetd.log</string>
	<key>StandardErrorPath</key>
	<string>/home/ann/.config/dorylinae/agentnetd.log</string>
</dict>
</plist>
`
	if w.Content != want {
		t.Errorf("plist mismatch:\n%s", w.Content)
	}
	cmds := runArgs(plan)
	wantCmds := []string{
		"launchctl bootout gui/501/dev.dorylinae.agentnetd",
		"launchctl bootstrap gui/501 /home/ann/Library/LaunchAgents/dev.dorylinae.agentnetd.plist",
	}
	assertEqual(t, cmds, wantCmds)
}

func TestLaunchdPlistEscapesXML(t *testing.T) {
	got := LaunchdPlist(Spec{Executable: "/opt/a&b/<agentnetd>", Home: "/h"})
	if !strings.Contains(got, "<string>/opt/a&amp;b/&lt;agentnetd&gt;</string>") {
		t.Errorf("executable not escaped:\n%s", got)
	}
}

func TestSystemdUnit(t *testing.T) {
	plan, err := Systemd{}.Install(unixSpec, unixEnv)
	if err != nil {
		t.Fatal(err)
	}
	w := plan.Steps[0]
	if w.Path != "/home/ann/.config/systemd/user/agentnetd.service" {
		t.Errorf("unit path = %q", w.Path)
	}
	want := `[Unit]
Description=AgentNet daemon (agentnetd)

[Service]
Type=simple
ExecStart="/opt/dorylinae/agentnetd" run --home "/home/ann/.config/dorylinae"
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
	if w.Content != want {
		t.Errorf("unit mismatch:\n%s", w.Content)
	}
	assertEqual(t, runArgs(plan), []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable --now agentnetd.service",
	})

	env := unixEnv
	env.ConfigHome = "/xdg"
	p2, _ := Systemd{}.Install(unixSpec, env)
	if p2.Steps[0].Path != "/xdg/systemd/user/agentnetd.service" {
		t.Errorf("XDG_CONFIG_HOME ignored: %q", p2.Steps[0].Path)
	}
}

func TestSystemdQuote(t *testing.T) {
	got := systemdQuote(`/a b/"c"/100%/$x\y`)
	want := `"/a b/\"c\"/100%%/$$x\\y"`
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestTaskXML(t *testing.T) {
	plan, err := Schtasks{}.Install(winSpec, winEnv)
	if err != nil {
		t.Fatal(err)
	}
	w := plan.Steps[0]
	if w.Path != `C:\Users\ann\AppData\Roaming\dorylinae\agentnetd-task.xml` || !w.UTF16 || w.Mode != 0o600 {
		t.Errorf("unexpected write step: %+v", w)
	}

	var task struct {
		Triggers struct {
			Logon struct {
				UserID  string `xml:"UserId"`
				Enabled bool   `xml:"Enabled"`
			} `xml:"LogonTrigger"`
		} `xml:"Triggers"`
		Principal struct {
			UserID    string `xml:"UserId"`
			LogonType string `xml:"LogonType"`
			RunLevel  string `xml:"RunLevel"`
		} `xml:"Principals>Principal"`
		Settings struct {
			Policy string `xml:"MultipleInstancesPolicy"`
			Limit  string `xml:"ExecutionTimeLimit"`
		} `xml:"Settings"`
		Exec struct {
			Command string `xml:"Command"`
			Args    string `xml:"Arguments"`
			WorkDir string `xml:"WorkingDirectory"`
		} `xml:"Actions>Exec"`
	}
	// The declared UTF-16 encoding applies to the on-disk form; parse the text.
	dec := xml.NewDecoder(strings.NewReader(w.Content))
	dec.CharsetReader = func(string, io.Reader) (io.Reader, error) { return strings.NewReader(w.Content), nil }
	if err := dec.Decode(&task); err != nil {
		t.Fatalf("task XML does not parse: %v\n%s", err, w.Content)
	}
	if !task.Triggers.Logon.Enabled || task.Triggers.Logon.UserID != `DESKTOP\ann` {
		t.Errorf("logon trigger = %+v", task.Triggers.Logon)
	}
	if task.Principal.UserID != `DESKTOP\ann` || task.Principal.LogonType != "InteractiveToken" || task.Principal.RunLevel != "LeastPrivilege" {
		t.Errorf("principal = %+v (must not require elevation)", task.Principal)
	}
	if task.Settings.Policy != "IgnoreNew" || task.Settings.Limit != "PT0S" {
		t.Errorf("settings = %+v", task.Settings)
	}
	if task.Exec.Command != winSpec.Executable {
		t.Errorf("command = %q", task.Exec.Command)
	}
	if want := `run --home C:\Users\ann\AppData\Roaming\dorylinae`; task.Exec.Args != want {
		t.Errorf("arguments = %q, want %q", task.Exec.Args, want)
	}

	assertEqual(t, runArgs(plan), []string{
		`schtasks.exe /Create /TN Dorylinae agentnetd /XML C:\Users\ann\AppData\Roaming\dorylinae\agentnetd-task.xml /F`,
		`schtasks.exe /Run /TN Dorylinae agentnetd`,
	})
	if last := plan.Steps[2]; last.Op != OpRemove || !last.Cleanup {
		t.Errorf("temp XML must be removed even when /Create fails: %+v", last)
	}
}

func TestWindowsQuote(t *testing.T) {
	for in, want := range map[string]string{
		`C:\plain\dir`:      `C:\plain\dir`,
		`C:\Users\a b\dir`:  `"C:\Users\a b\dir"`,
		`C:\Users\a b\dir\`: `"C:\Users\a b\dir\\"`,
		`a"b`:               `"a\"b"`,
		``:                  `""`,
	} {
		if got := windowsQuote(in); got != want {
			t.Errorf("windowsQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestPlansRejectRelativePaths(t *testing.T) {
	bad := Spec{Executable: "agentnetd", Home: "dorylinae"}
	for _, p := range []Platform{Launchd{}, Systemd{}, Schtasks{}} {
		if _, err := p.Install(bad, unixEnv); err == nil {
			t.Errorf("%s accepted relative paths", p.Name())
		}
	}
}

func TestUninstallPlans(t *testing.T) {
	l, _ := Launchd{}.Uninstall(unixSpec, unixEnv)
	assertEqual(t, runArgs(l), []string{"launchctl bootout gui/501/dev.dorylinae.agentnetd"})
	if s := l.Steps[1]; s.Op != OpRemove || s.Path != "/home/ann/Library/LaunchAgents/dev.dorylinae.agentnetd.plist" {
		t.Errorf("launchd remove step: %+v", s)
	}

	s, _ := Systemd{}.Uninstall(unixSpec, unixEnv)
	assertEqual(t, runArgs(s), []string{
		"systemctl --user disable --now agentnetd.service",
		"systemctl --user daemon-reload",
	})

	w, _ := Schtasks{}.Uninstall(winSpec, winEnv)
	assertEqual(t, runArgs(w), []string{
		"schtasks.exe /Query /TN Dorylinae agentnetd",
		"schtasks.exe /End /TN Dorylinae agentnetd",
		"schtasks.exe /Delete /TN Dorylinae agentnetd /F",
	})
	if !w.Steps[0].Probe {
		t.Error("windows uninstall must probe for the task first")
	}
}

func TestDescribe(t *testing.T) {
	plan, _ := Systemd{}.Install(unixSpec, unixEnv)
	var b bytes.Buffer
	plan.Describe(&b)
	out := b.String()
	for _, want := range []string{
		"write /home/ann/.config/systemd/user/agentnetd.service (mode 0644):",
		`  | ExecStart="/opt/dorylinae/agentnetd" run --home "/home/ann/.config/dorylinae"`,
		"run: systemctl --user enable --now agentnetd.service",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("describe output missing %q:\n%s", want, out)
		}
	}
}

// fakeRunner records commands and fails the ones the test names.
type fakeRunner struct {
	ran  []string
	fail map[string]bool
}

func (f *fakeRunner) Run(_ context.Context, args []string) ([]byte, error) {
	cmd := strings.Join(args, " ")
	f.ran = append(f.ran, cmd)
	if f.fail[cmd] {
		return []byte("boom"), errors.New("exit status 1")
	}
	return nil, nil
}

func TestApplyInstallAndUninstall(t *testing.T) {
	dir := t.TempDir()
	plan := Plan{Platform: "test", Steps: []Step{
		{Op: OpWrite, Path: filepath.Join(dir, "sub", "x.conf"), Content: "hello", Mode: 0o644},
		{Op: OpRun, Args: []string{"do", "it"}},
	}}
	r := &fakeRunner{}
	res, err := plan.Apply(context.Background(), r)
	if err != nil || !res.Changed {
		t.Fatalf("install apply: %+v, %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "sub", "x.conf")); string(b) != "hello" {
		t.Errorf("file content = %q", b)
	}

	un := Plan{Platform: "test", Steps: []Step{
		{Op: OpRun, Args: []string{"stop"}, Ignore: true},
		{Op: OpRemove, Path: filepath.Join(dir, "sub", "x.conf")},
	}}
	r = &fakeRunner{fail: map[string]bool{"stop": true}}
	if res, err = un.Apply(context.Background(), r); err != nil || !res.Changed {
		t.Fatalf("first uninstall: %+v, %v", res, err)
	}
	// Second time: file gone, stop still fails -> success with no change.
	if res, err = un.Apply(context.Background(), r); err != nil || res.Changed {
		t.Fatalf("second uninstall must be a clean no-op: %+v, %v", res, err)
	}
}

func TestApplyProbeSkipsWhenNotInstalled(t *testing.T) {
	plan, _ := Schtasks{}.Uninstall(winSpec, winEnv)

	absent := &fakeRunner{fail: map[string]bool{"schtasks.exe /Query /TN Dorylinae agentnetd": true}}
	res, err := plan.Apply(context.Background(), absent)
	if err != nil || res.Changed {
		t.Fatalf("absent task: %+v, %v", res, err)
	}
	if len(absent.ran) != 1 {
		t.Errorf("only the probe should run, got %v", absent.ran)
	}

	present := &fakeRunner{}
	res, err = plan.Apply(context.Background(), present)
	if err != nil || !res.Changed || len(present.ran) != 3 {
		t.Fatalf("present task: %+v, %v, %v", res, err, present.ran)
	}

	failing := &fakeRunner{fail: map[string]bool{"schtasks.exe /Delete /TN Dorylinae agentnetd /F": true}}
	if _, err = plan.Apply(context.Background(), failing); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("a failed delete must be reported with its output, got %v", err)
	}
}

func TestApplyFailureStopsButCleansUp(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "task.xml")
	plan := Plan{Platform: "test", Steps: []Step{
		{Op: OpWrite, Path: xmlPath, Content: "<x/>", Mode: 0o600, UTF16: true},
		{Op: OpRun, Args: []string{"create"}},
		{Op: OpRemove, Path: xmlPath, Cleanup: true},
		{Op: OpRun, Args: []string{"run"}},
	}}
	r := &fakeRunner{fail: map[string]bool{"create": true}}
	if _, err := plan.Apply(context.Background(), r); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(xmlPath); !os.IsNotExist(err) {
		t.Error("temp file left behind after failure")
	}
	assertEqual(t, r.ran, []string{"create"})
}

func TestEncodeUTF16LE(t *testing.T) {
	got := encodeUTF16LE("aé😀")
	if got[0] != 0xFF || got[1] != 0xFE {
		t.Fatalf("missing BOM: % x", got[:2])
	}
	var u []uint16
	for i := 2; i < len(got); i += 2 {
		u = append(u, uint16(got[i])|uint16(got[i+1])<<8)
	}
	if s := string(utf16.Decode(u)); s != "aé😀" {
		t.Errorf("round trip = %q", s)
	}
}

func runArgs(p Plan) []string {
	var out []string
	for _, s := range p.Steps {
		if s.Op == OpRun {
			out = append(out, strings.Join(s.Args, " "))
		}
	}
	return out
}

func assertEqual(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}
