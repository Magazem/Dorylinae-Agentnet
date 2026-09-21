// Package service registers agentnetd as a per-user service that starts at
// login: a launchd agent on macOS, a systemd user unit on Linux and a Task
// Scheduler at-logon task on Windows.
//
// Every platform is a pure function from (Spec, Env) to a Plan: an ordered list
// of file writes, commands and removals. Nothing touches the machine until
// Plan.Apply, so the generated definitions are unit-testable on any OS and
// `--dry-run` can print exactly what would happen. Only the choice of
// platform (Current) is build-tagged.
package service

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Spec says what to run.
type Spec struct {
	// Executable is the absolute path of the agentnetd binary.
	Executable string
	// Home is the absolute config directory, passed to the daemon as --home so
	// the service does not depend on environment variables it will not inherit.
	Home string
	// Relay is the relay WebSocket URL passed to the daemon as --relay; empty
	// means no relay.
	Relay string
}

// Env carries the per-user facts a platform needs to place its files.
type Env struct {
	// HomeDir is the user's home directory.
	HomeDir string
	// ConfigHome is the XDG config directory (Linux); empty means HomeDir/.config.
	ConfigHome string
	// UID is the numeric user id (macOS launchd domain); -1 on Windows.
	UID int
	// User is the account name, DOMAIN\user on Windows.
	User string
}

// ErrUnsupported is returned by Current on operating systems without a backend.
var ErrUnsupported = errors.New("service install is not supported on this operating system")

// Platform builds install and uninstall plans for one operating system.
type Platform interface {
	// Name is a short identifier, e.g. "launchd", "systemd" or "schtasks".
	Name() string
	// Install returns the plan that registers and starts the service. It is
	// idempotent: applying it over an existing install replaces the definition.
	Install(Spec, Env) (Plan, error)
	// Uninstall returns the plan that stops and removes the service. It is
	// idempotent: applying it when nothing is installed succeeds.
	Uninstall(Spec, Env) (Plan, error)
}

// Op is the kind of a Step.
type Op int

// Step operations.
const (
	OpWrite  Op = iota // write Content to Path
	OpRun              // run Args
	OpRemove           // remove Path; a missing file is fine
)

// Step is one action of a Plan.
type Step struct {
	Op Op

	Path    string      // OpWrite, OpRemove
	Content string      // OpWrite
	Mode    os.FileMode // OpWrite
	UTF16   bool        // OpWrite: encode as UTF-16LE with a BOM

	Args   []string // OpRun: program and arguments
	Ignore bool     // OpRun: a failure is tolerated
	// Probe marks an OpRun whose failure means "nothing is installed": the
	// remaining OpRun steps are skipped. Its success counts as a change.
	Probe bool

	// Cleanup marks an OpRemove that also runs after an earlier step failed.
	Cleanup bool
}

// Plan is an ordered list of steps for one platform.
type Plan struct {
	Platform string
	Steps    []Step
}

// Result reports what Apply did.
type Result struct {
	// Changed is false when the plan found nothing to do (e.g. uninstalling
	// something that was not installed).
	Changed bool
}

// Runner executes external commands; tests substitute a fake.
type Runner interface {
	Run(ctx context.Context, args []string) ([]byte, error)
}

// ExecRunner runs commands with os/exec, returning combined output.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("empty command")
	}
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput() //nolint:gosec // args are built from fixed programs and validated paths
}

// Apply executes the plan. On the first failure it stops, still running
// cleanup removals, and returns that failure.
func (p Plan) Apply(ctx context.Context, r Runner) (Result, error) {
	var (
		res          Result
		firstErr     error
		notInstalled bool
	)
	for _, s := range p.Steps {
		if firstErr != nil && (s.Op != OpRemove || !s.Cleanup) {
			continue
		}
		switch s.Op {
		case OpWrite:
			if err := writeFile(s); err != nil {
				firstErr = err
				continue
			}
			res.Changed = true
		case OpRun:
			if notInstalled {
				continue
			}
			out, err := r.Run(ctx, s.Args)
			switch {
			case err == nil:
				if s.Probe {
					res.Changed = true
				}
			case s.Probe:
				notInstalled = true
			case s.Ignore:
			default:
				firstErr = fmt.Errorf("%s: %w: %s", strings.Join(s.Args, " "), err, strings.TrimSpace(string(out)))
			}
		case OpRemove:
			err := os.Remove(s.Path)
			switch {
			case err == nil:
				if !s.Cleanup {
					res.Changed = true
				}
			case errors.Is(err, os.ErrNotExist):
			default:
				if firstErr == nil {
					firstErr = fmt.Errorf("remove %s: %w", s.Path, err)
				}
			}
		}
	}
	return res, firstErr
}

func writeFile(s Step) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil { //nolint:gosec // LaunchAgents and systemd user dirs are conventionally 0755
		return fmt.Errorf("create %s: %w", filepath.Dir(s.Path), err)
	}
	data := []byte(s.Content)
	if s.UTF16 {
		data = encodeUTF16LE(s.Content)
	}
	if err := os.WriteFile(s.Path, data, s.Mode); err != nil {
		return fmt.Errorf("write %s: %w", s.Path, err)
	}
	return nil
}

func encodeUTF16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, 0, 2+2*len(u))
	out = append(out, 0xFF, 0xFE) // BOM
	for _, c := range u {
		out = binary.LittleEndian.AppendUint16(out, c)
	}
	return out
}

// Describe writes a human-readable rendering of the plan: the dry-run output.
func (p Plan) Describe(w io.Writer) {
	for _, s := range p.Steps {
		switch s.Op {
		case OpWrite:
			enc := ""
			if s.UTF16 {
				enc = ", UTF-16LE"
			}
			_, _ = fmt.Fprintf(w, "write %s (mode %04o%s):\n", s.Path, s.Mode.Perm(), enc)
			for _, line := range strings.Split(strings.TrimRight(s.Content, "\n"), "\n") {
				_, _ = fmt.Fprintf(w, "  | %s\n", line)
			}
		case OpRun:
			note := ""
			switch {
			case s.Probe:
				note = "  (failure = not installed)"
			case s.Ignore:
				note = "  (failure ignored)"
			}
			_, _ = fmt.Fprintf(w, "run: %s%s\n", strings.Join(s.Args, " "), note)
		case OpRemove:
			_, _ = fmt.Fprintf(w, "remove %s\n", s.Path)
		}
	}
}
