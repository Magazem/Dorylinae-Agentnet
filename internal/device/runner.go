package device

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// MaxOutput is the most output a result carries: the last 32768 bytes
// (Docs/protocol/device.md §Running).
const MaxOutput = 32768

// rawTail is how much raw output the runner keeps before sanitising: more
// than MaxOutput, so a tail that shrinks (CRLF, ANSI sequences) still fills
// the result.
const rawTail = 4 * MaxOutput

// waitDelay bounds how long Wait waits for the output pipes after the process
// ended or was killed, in case something outside the process tree holds them.
const waitDelay = 5 * time.Second

// BaseEnv are the variables every command gets from the helper daemon's
// environment when set, besides the scope's own names
// (Docs/protocol/device.md §Running). Everything else, tokens and
// DORYLINAE_* included, is dropped.
var BaseEnv = []string{
	"PATH", "HOME", "USERPROFILE", "TMP", "TEMP", "TMPDIR", "LANG", "LC_ALL",
	"SystemRoot", "SystemDrive", "windir", "ComSpec", "PATHEXT", "LOCALAPPDATA", "APPDATA",
}

// MinimalEnv builds a command's environment: BaseEnv plus extra, each taken
// from lookup (the daemon's environment) when set there, never a DORYLINAE_*
// variable. A name is used once (compared as the OS compares names).
func MinimalEnv(extra []string, lookup func(string) (string, bool)) []string {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	seen := map[string]bool{}
	var env []string
	for _, name := range append(append([]string{}, BaseEnv...), extra...) {
		key := envKey(name)
		if seen[key] || strings.HasPrefix(strings.ToUpper(name), "DORYLINAE_") {
			continue
		}
		seen[key] = true
		if v, ok := lookup(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// RunSpec is one command to run. Path is absolute; Args[0] is Path.
type RunSpec struct {
	Path    string
	Args    []string
	Dir     string
	Env     []string
	Timeout time.Duration
}

// RunResult is the outcome of Run.
type RunResult struct {
	// Started is false when the program could not be started at all.
	Started  bool
	ExitCode int
	TimedOut bool
	// Output is the sanitised tail of combined stdout and stderr.
	Output   string
	Duration time.Duration
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return n, nil
	}
	if over := len(t.buf) + len(p) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	t.buf = append(t.buf, p...)
	return n, nil
}

func (t *tailBuffer) bytes() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.buf...)
}

// Run starts spec with no shell, stdin on the null device and only spec.Env,
// in spec.Dir, and waits for it. At spec.Timeout, or when ctx ends, it kills
// the whole process tree (Windows: a job object; Unix: the process group);
// when the program ends on its own, what it left behind in its tree is
// killed too. The output is the sanitised tail (SanitizeOutput).
func Run(ctx context.Context, spec RunSpec) RunResult {
	start := time.Now()
	tail := &tailBuffer{max: rawTail}
	cmd := &exec.Cmd{Path: spec.Path, Args: spec.Args, Dir: spec.Dir, Env: spec.Env, Stdout: tail, Stderr: tail}
	if cmd.Env == nil {
		cmd.Env = []string{} // never inherit the daemon's environment
	}
	cmd.WaitDelay = waitDelay
	tree, err := startTree(cmd)
	if err != nil {
		return RunResult{Duration: time.Since(start)}
	}
	var timedOut bool
	var mu sync.Mutex
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(spec.Timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			mu.Lock()
			timedOut = true
			mu.Unlock()
			tree.kill()
		case <-ctx.Done():
			tree.kill()
		case <-stop:
		}
	}()
	werr := cmd.Wait()
	close(stop)
	<-done
	tree.kill() // anything the program left running in its tree
	tree.close()
	mu.Lock()
	res := RunResult{Started: true, TimedOut: timedOut, Duration: time.Since(start)}
	mu.Unlock()
	var ee *exec.ExitError
	switch {
	case werr == nil:
		res.ExitCode = 0
	case errors.Is(werr, exec.ErrWaitDelay) && cmd.ProcessState != nil:
		// The program ended, but something it left behind held the output
		// open past waitDelay: its own exit code still counts (review 40 L4).
		res.ExitCode = cmd.ProcessState.ExitCode()
		if res.ExitCode < 0 {
			res.ExitCode = 1
		}
	case errors.As(werr, &ee):
		res.ExitCode = ee.ExitCode()
		if res.ExitCode < 0 {
			res.ExitCode = 1 // killed by a signal
		}
	default:
		res.ExitCode = 1
	}
	res.Output = SanitizeOutput(tail.bytes())
	return res
}

// CheckTarget re-checks, when a run starts, what the scope resolved at set
// time (review 40 L5): the working directory still resolves to itself, so a
// repo replaced since by a symlink or junction to somewhere else is refused,
// and the program is still a regular file that only this user or an
// administrator can change (CheckProgramOwner, review 40 L11).
func CheckTarget(path, dir string) error {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	same := resolved == dir
	if runtime.GOOS == "windows" {
		same = strings.EqualFold(resolved, dir)
	}
	if !same {
		return errors.New("device: the working directory no longer resolves to the path in the scope")
	}
	if fi, err := os.Stat(resolved); err != nil || !fi.IsDir() {
		return errors.New("device: the working directory is not a directory")
	}
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return errors.New("device: the program is not a regular file")
	}
	return CheckProgramOwner(path)
}

// replacement stands in for a control character or an invalid byte. It is
// "?", not U+FFFD: the D14 result rules refuse U+FFFD in every text field, as
// the mark of invalid UTF-8 (Docs/protocol/device.md §Running).
const replacement = '?'

// SanitizeOutput turns raw output into a result's output
// (Docs/protocol/device.md §Running): CRLF becomes LF, ANSI CSI sequences
// (ESC [ … and U+009B …) are removed, every other control character except
// \n and \t, invalid UTF-8 and U+FFFD itself become "?", and the last
// MaxOutput bytes are kept, cut at a UTF-8 boundary.
func SanitizeOutput(raw []byte) string {
	var b strings.Builder
	b.Grow(len(raw))
	s := string(raw)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError:
			b.WriteRune(replacement)
			i += size
			continue
		case r == '\r' && i+1 < len(s) && s[i+1] == '\n':
			i++ // the \n is written next
			continue
		case r == 0x1b && i+1 < len(s) && s[i+1] == '[':
			i = skipCSI(s, i+2)
			continue
		case r == 0x9b:
			i = skipCSI(s, i+size)
			continue
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			b.WriteRune(replacement)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	out := b.String()
	if len(out) > MaxOutput {
		cut := len(out) - MaxOutput
		for cut < len(out) && !utf8.RuneStart(out[cut]) {
			cut++
		}
		out = out[cut:]
	}
	return out
}

// skipCSI returns the index after the CSI sequence whose parameters start at
// i: parameter bytes 0x30-0x3F, intermediate bytes 0x20-0x2F, then one final
// byte 0x40-0x7E. An unterminated sequence is dropped to the end.
func skipCSI(s string, i int) int {
	for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
		i++
	}
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
		i++
	}
	return i
}
