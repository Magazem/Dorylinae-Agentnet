//go:build linux

package notify

import (
	"bufio"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
)

// ownedByRootNotWritable reports whether fi belongs to uid 0 and neither
// group nor others can write it (review 29 M2, "root-owned, not group- or
// world-writable check").
func ownedByRootNotWritable(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if st.Uid != 0 {
		return false
	}
	return fi.Mode().Perm()&0o022 == 0
}

// linuxDialogCandidates are the fixed absolute paths checked in order
// (Docs/protocol/approval.md §The approval window, Linux row: "never PATH
// ... a Flatpak-only install is not found"; review 29 M2).
var linuxDialogCandidates = []string{"/usr/bin", "/bin", "/run/current-system/sw/bin"}

// dialogEnvVars are read from the daemon's own environment, falling back to
// the systemd user manager's Environment property (Docs/protocol/approval.md
// §The approval window, Linux row; review 29 M1: a systemd user unit often
// starts before the desktop exports these).
var dialogEnvVars = []string{"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_RUNTIME_DIR"}

// findDialogProgram returns the absolute path of the first of name in
// linuxDialogCandidates that exists, is owned by root and is neither
// group- nor world-writable (review 29 M2).
func findDialogProgram(name string) (string, bool) {
	for _, dir := range linuxDialogCandidates {
		path := dir + "/" + name
		fi, err := os.Stat(path)
		if err != nil || fi.IsDir() {
			continue
		}
		if !ownedByRootNotWritable(fi) {
			continue
		}
		return path, true
	}
	return "", false
}

// systemdUserEnvironment reads the systemd user manager's Environment
// property in-process over godbus (review 29 M1). Best-effort: an error or
// timeout returns nil.
func systemdUserEnvironment(ctx context.Context) map[string]string {
	conn, err := connectSessionBus(ctx)
	if err != nil {
		return nil
	}
	defer func() { _ = conn.Close() }()
	obj := conn.Object("org.freedesktop.systemd1", dbus.ObjectPath("/org/freedesktop/systemd1"))
	variant, err := obj.GetProperty("org.freedesktop.systemd1.Manager.Environment")
	if err != nil {
		return nil
	}
	list, ok := variant.Value().([]string)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(list))
	for _, kv := range list {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}

// dialogEnv builds the child environment: the daemon's own DISPLAY etc. when
// set, else the systemd user manager's (review 29 M1).
func dialogEnv(ctx context.Context) []string {
	sysdEnv := map[string]string(nil)
	env := os.Environ()
	have := map[string]bool{}
	for _, kv := range env {
		for _, name := range dialogEnvVars {
			if strings.HasPrefix(kv, name+"=") {
				have[name] = true
			}
		}
	}
	missing := false
	for _, name := range dialogEnvVars {
		if !have[name] {
			missing = true
		}
	}
	if missing {
		sysdEnv = systemdUserEnvironment(ctx)
	}
	for _, name := range dialogEnvVars {
		if !have[name] && sysdEnv != nil {
			if v, ok := sysdEnv[name]; ok {
				env = append(env, name+"="+v)
			}
		}
	}
	return env
}

// escapeArg builds a single "--opt=value" argument, so peer-supplied text
// (a summary that starts with "-") is never a positional argument and never
// sits next to option parsing (Docs/protocol/approval.md §The approval
// window, Linux row; review 29 M3).
func escapeArg(opt, value string) string {
	return opt + "=" + escapeMarkup(value)
}

// mapZenityExit decodes zenity/kdialog's exit status into a dialog answer:
// zenity and kdialog cannot print "approve <digits>" themselves
// (Docs/protocol/approval.md §The approval window, "Answer format"): 0 with
// text -> approve, the extra button (zenity: stdout "Reject", exit 1) ->
// reject, any other exit including zenity's timeout (5) -> dismiss.
func mapZenityExit(exitCode int, stdout string) dialogAnswer {
	text := strings.TrimRight(stdout, "\n")
	switch exitCode {
	case 0:
		return dialogAnswer{kind: "approve", code: text}
	case 1:
		if text == "Reject" {
			return dialogAnswer{kind: "reject"}
		}
		return dialogAnswer{kind: "dismiss"}
	default:
		return dialogAnswer{kind: "dismiss"}
	}
}

// decodeExit turns a finished dialog's exit status and first stdout line
// into its answer, and reports whether that answer is a valid one: a human
// pressed OK (exit 0) or zenity's Reject button (exit 1 with stdout
// "Reject"). Any other exit, including zenity's timeout (5), a Cancel or a
// failure to open the display (GTK exits 1 with no output), is dismiss and
// not valid, so before the ready grace it counts as not ready
// (Docs/protocol/approval.md §The approval window, "Ready when": "An early
// non-zero exit is failure"; review 30, M2).
func decodeExit(zenity bool, exitCode int, line string) (dialogAnswer, bool) {
	if len(line) > maxAnswerLine {
		return dialogAnswer{kind: "dismiss"}, false
	}
	if !zenity {
		// kdialog has no Reject button: reject via agentnet approve --reject.
		if exitCode == 0 {
			return dialogAnswer{kind: "approve", code: strings.TrimRight(line, "\n")}, true
		}
		return dialogAnswer{kind: "dismiss"}, false
	}
	ans := mapZenityExit(exitCode, line)
	return ans, ans.kind != "dismiss"
}

func startDialog(ctx context.Context, _, tag, kind, summary string, expires time.Time) (approval.WindowHandle, error) {
	timeoutSecs := int(time.Until(expires).Seconds())
	if timeoutSecs < 1 {
		timeoutSecs = 1
	}
	text := kind + ": " + summary
	var cmd *exec.Cmd
	var useZenity bool
	if path, ok := findDialogProgram("zenity"); ok {
		useZenity = true
		cmd = exec.CommandContext(ctx, path, //nolint:gosec // resolved from a fixed, ownership-checked candidate list, never PATH
			escapeArg("--title", "AgentNet approval "+tag),
			"--entry",
			escapeArg("--text", text),
			"--extra-button=Reject",
			escapeArg("--timeout", itoa(timeoutSecs)))
	} else if path, ok := findDialogProgram("kdialog"); ok {
		cmd = exec.CommandContext(ctx, path, //nolint:gosec // resolved from a fixed, ownership-checked candidate list, never PATH
			escapeArg("--title", "AgentNet approval "+tag),
			escapeArg("--inputbox", text))
	} else {
		h := newDialogHandle(func() {})
		h.markNotReady()
		return h, nil
	}
	cmd.Env = dialogEnv(ctx)
	// SIGKILL the dialog when the thread that started it exits, so it dies
	// with the daemon (Docs/protocol/approval.md §The approval window,
	// "Lifetime"; review 30, M5). The daemon never calls
	// runtime.LockOSThread, so Go never retires this thread early.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		h := newDialogHandle(func() {})
		h.markNotReady()
		return h, nil
	}
	if err := cmd.Start(); err != nil {
		h := newDialogHandle(func() {})
		h.markNotReady()
		return h, nil
	}
	handle := newDialogHandle(func() { _ = cmd.Process.Kill() })

	go func() {
		t := time.NewTimer(readyGraceLinux)
		defer t.Stop()
		<-t.C
		handle.markReady()
	}()

	go func() {
		sc := bufio.NewScanner(stdout)
		var line string
		if sc.Scan() {
			line = sc.Text()
		}
		_, _ = io.Copy(io.Discard, stdout) // read to EOF before Wait
		err := cmd.Wait()
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			code = -1
		}
		ans, valid := decodeExit(useZenity, code, line)
		if valid {
			// "already exited with a valid answer" counts as ready
			// (Docs/protocol/approval.md §The approval window, "Ready when").
			handle.markReady()
		}
		// Otherwise an exit before the 1.5 s grace is a failure (not ready);
		// after it, the window was shown and closed: dismiss. Either way an
		// answer is always delivered, so the Store's watcher never waits on
		// an exited dialog and approval_open can reopen it (review 30, M1).
		handle.markNotReady() // no-op once ready
		handle.deliver(ans)
	}()

	return handle, nil
}

const readyGraceLinux = 1500 * time.Millisecond

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
