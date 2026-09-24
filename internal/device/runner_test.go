package device

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

var (
	helperOnce sync.Once
	helperDir  string
	helperPath string
	helperErr  error
)

// runnerHelper builds testdata/runnerhelper once per test binary with go
// build (no shell scripts) and returns its absolute path.
func runnerHelper(t *testing.T) string {
	t.Helper()
	helperOnce.Do(func() {
		helperDir, helperErr = os.MkdirTemp("", "dn-runner-")
		if helperErr != nil {
			return
		}
		helperPath = filepath.Join(helperDir, "runnerhelper")
		if runtime.GOOS == "windows" {
			helperPath += ".exe"
		}
		out, err := exec.Command("go", "build", "-o", helperPath, "./testdata/runnerhelper").CombinedOutput() //nolint:gosec // fixed test package
		if err != nil {
			helperErr = &buildError{out: string(out), err: err}
		}
	})
	if helperErr != nil {
		t.Fatalf("build runnerhelper: %v", helperErr)
	}
	return helperPath
}

type buildError struct {
	out string
	err error
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.out }

func TestMain(m *testing.M) {
	code := m.Run()
	if helperDir != "" {
		_ = os.RemoveAll(helperDir)
	}
	os.Exit(code)
}

func runHelper(t *testing.T, dir string, env []string, timeout time.Duration, args ...string) RunResult {
	t.Helper()
	prog := runnerHelper(t)
	return Run(context.Background(), RunSpec{Path: prog, Args: append([]string{prog}, args...), Dir: dir, Env: env, Timeout: timeout})
}

// The command sees only the minimal environment: a secret in the daemon's
// environment and DORYLINAE_* never reach it, a listed extra name does
// (ticket 2.D2 acceptance).
func TestRunMinimalEnvironment(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "s3cr3t-marker")
	t.Setenv("DORYLINAE_HOME", "/nope")
	t.Setenv("GOFLAGS", "-count=1")
	env := MinimalEnv([]string{"GOFLAGS", "DORYLINAE_HOME"}, nil)
	res := runHelper(t, testutil.TempDir(t), env, 30*time.Second, "env")
	if !res.Started || res.ExitCode != 0 {
		t.Fatalf("env: %+v", res)
	}
	if strings.Contains(res.Output, "SECRET_TOKEN") || strings.Contains(res.Output, "s3cr3t-marker") {
		t.Fatalf("the secret reached the command:\n%s", res.Output)
	}
	if strings.Contains(strings.ToUpper(res.Output), "DORYLINAE") {
		t.Fatalf("a DORYLINAE_* variable reached the command:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "GOFLAGS=-count=1") {
		t.Fatalf("the listed extra variable is missing:\n%s", res.Output)
	}
	allowed := map[string]bool{"GOFLAGS": true}
	for _, n := range BaseEnv {
		allowed[envKey(n)] = true
	}
	for _, line := range strings.Split(strings.TrimSpace(res.Output), "\n") {
		name, _, _ := strings.Cut(line, "=")
		if name == "" { // Windows keeps per-drive "=C:" entries out of Environ
			continue
		}
		if !allowed[envKey(name)] {
			t.Errorf("unexpected variable %q in the command's environment", name)
		}
	}
}

// Exit codes are reported as they are; the working directory is the repo.
func TestRunExitCodeAndDirectory(t *testing.T) {
	dir := testutil.TempDir(t)
	for _, code := range []int{0, 3} {
		res := runHelper(t, dir, nil, 30*time.Second, "exit", strconv.Itoa(code))
		if !res.Started || res.ExitCode != code || res.TimedOut {
			t.Fatalf("exit %d: %+v", code, res)
		}
	}
	res := runHelper(t, dir, nil, 30*time.Second, "pwd")
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(res.Output))
	if !strings.EqualFold(got, want) {
		t.Fatalf("working directory = %q, want %q", got, want)
	}
}

// The output is the tail, at most 32768 bytes, with ANSI sequences and CRs
// removed and other control characters replaced (ticket 2.D2 acceptance).
func TestRunOutputTail(t *testing.T) {
	res := runHelper(t, testutil.TempDir(t), nil, 60*time.Second, "spam", "5000")
	if res.ExitCode != 0 {
		t.Fatalf("spam: %+v", res)
	}
	if len(res.Output) > MaxOutput || len(res.Output) < MaxOutput-64 {
		t.Fatalf("output is %d bytes, want the last %d", len(res.Output), MaxOutput)
	}
	if strings.ContainsAny(res.Output, "\x1b\r\a") || !utf8.ValidString(res.Output) {
		t.Fatalf("output still holds escape, CR or BEL bytes, or bad UTF-8")
	}
	if !strings.Contains(res.Output, "line 004999 green bell?\n") || !strings.HasSuffix(res.Output, "last line on stderr\n") {
		t.Fatalf("the tail is not the end of the output: %q", res.Output[len(res.Output)-120:])
	}
	if strings.Contains(res.Output, "line 000000") {
		t.Fatal("the head of the output was kept instead of the tail")
	}
	// It is a valid D14 output as it is.
	if err := request.ValidateComplete("", &request.Result{Status: "pass", Output: res.Output}); err != nil {
		t.Fatalf("the sanitised output is not a valid result output: %v", err)
	}
}

// The timeout kills a child of the child: the process tree ends (ticket 2.D2
// acceptance).
func TestRunTimeoutKillsProcessTree(t *testing.T) {
	dir := testutil.TempDir(t)
	beat := filepath.Join(dir, "beat.txt")
	res := runHelper(t, dir, nil, 2*time.Second, "tree", beat)
	if !res.TimedOut || res.ExitCode == 0 {
		t.Fatalf("tree: %+v", res)
	}
	size := func() int64 {
		fi, err := os.Stat(beat)
		if err != nil {
			return 0
		}
		return fi.Size()
	}
	if size() == 0 {
		t.Fatal("the grandchild never ran")
	}
	time.Sleep(300 * time.Millisecond)
	before := size()
	time.Sleep(time.Second)
	if after := size(); after != before {
		t.Fatalf("the grandchild still runs after the timeout (%d -> %d bytes)", before, after)
	}
}

// Review 40 L4: a program that exits 0 but leaves a child holding its output
// open is still exit 0 (not a WaitDelay error reported as exit 1), and the
// child is killed with the tree.
func TestRunOrphanHoldingOutput(t *testing.T) {
	dir := testutil.TempDir(t)
	beat := filepath.Join(dir, "beat.txt")
	res := runHelper(t, dir, nil, 60*time.Second, "orphan", beat)
	if !res.Started || res.TimedOut || res.ExitCode != 0 {
		t.Fatalf("orphan: %+v", res)
	}
	size := func() int64 {
		fi, err := os.Stat(beat)
		if err != nil {
			return 0
		}
		return fi.Size()
	}
	time.Sleep(300 * time.Millisecond)
	before := size()
	time.Sleep(time.Second)
	if after := size(); after != before {
		t.Fatalf("the orphan still runs after the program ended (%d -> %d bytes)", before, after)
	}
}

// Review 40 L5: at start, the repo must still resolve to the path the human
// approved, and the program must still be a regular file.
func TestCheckTarget(t *testing.T) {
	prog := runnerHelper(t)
	dir, err := filepath.EvalSymlinks(testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckTarget(prog, dir); err != nil {
		t.Fatalf("unchanged target: %v", err)
	}
	if CheckTarget(filepath.Join(dir, "missing"), dir) == nil || CheckTarget(dir, dir) == nil {
		t.Fatal("a missing program or a directory as the program passed")
	}
	if CheckTarget(prog, filepath.Join(dir, "gone")) == nil {
		t.Fatal("a missing directory passed")
	}
	// The repo replaced by a link to another directory.
	other := testutil.TempDir(t)
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CheckTarget(prog, repo); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(repo); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, repo); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	if CheckTarget(prog, repo) == nil {
		t.Fatal("a repo swapped for a symlink passed")
	}
}

// On Windows `go env GOCACHE` needs LOCALAPPDATA: it must succeed with the
// minimal environment (ticket 2.D2 acceptance).
func TestRunGoEnvWithMinimalEnvironment(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	res := Run(context.Background(), RunSpec{Path: goBin, Args: []string{goBin, "env", "GOCACHE"}, Dir: testutil.TempDir(t),
		Env: MinimalEnv(nil, nil), Timeout: 60 * time.Second})
	if res.ExitCode != 0 || strings.TrimSpace(res.Output) == "" {
		t.Fatalf("go env GOCACHE with the minimal environment: %+v", res)
	}
}

func TestRunProgramMissing(t *testing.T) {
	missing := filepath.Join(testutil.TempDir(t), "no-such-program")
	res := Run(context.Background(), RunSpec{Path: missing, Args: []string{missing}, Timeout: time.Second})
	if res.Started {
		t.Fatalf("a missing program started: %+v", res)
	}
}

func TestSanitizeOutput(t *testing.T) {
	for in, want := range map[string]string{
		"plain\n":              "plain\n",
		"crlf\r\nline\r\n":     "crlf\nline\n",
		"\x1b[31mred\x1b[0m":   "red",
		"\x1b[1;32;40mx\x1b[m": "x",
		"\x1b[?25lhidden":      "hidden",
		"c1 \u009b31mcsi":      "c1 csi",
		"tab\tok":              "tab\tok",
		"bell\a":               "bell?",
		"lone cr\r":            "lone cr?",
		"osc \x1b]0;title\a":   "osc ?]0;title?",
		"del\x7f":              "del?",
		"bad \xff utf8":        "bad ? utf8",
		"c1 \u0085 nel":        "c1 ? nel",
		"unterminated \x1b[12": "unterminated ",
		"literal � mark":       "literal ? mark",
		"unicode é ✓":          "unicode é ✓",
	} {
		if got := SanitizeOutput([]byte(in)); got != want {
			t.Errorf("SanitizeOutput(%q) = %q, want %q", in, got, want)
		}
	}
	// The cut keeps the last MaxOutput bytes at a UTF-8 boundary.
	long := strings.Repeat("é", MaxOutput) // 2 bytes each
	for _, in := range []string{"bell\a", "bad \xff", "\x1b]8;;x\a", "lit \uFFFD"} {
		if err := request.ValidateComplete("", &request.Result{Status: "pass", Output: SanitizeOutput([]byte(in))}); err != nil {
			t.Errorf("SanitizeOutput(%q) is not a valid result output: %v", in, err)
		}
	}
	got := SanitizeOutput([]byte("x" + long))
	if len(got) > MaxOutput || !utf8.ValidString(got) || !strings.HasSuffix(got, "éé") {
		t.Fatalf("cut: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}

func TestMinimalEnvLookup(t *testing.T) {
	vals := map[string]string{"PATH": "/bin", "SECRET": "x", "EXTRA": "y", "DORYLINAE_KEY": "z"}
	env := MinimalEnv([]string{"EXTRA", "DORYLINAE_KEY", "UNSET"}, func(k string) (string, bool) { v, ok := vals[k]; return v, ok })
	got := strings.Join(env, " ")
	if got != "PATH=/bin EXTRA=y" {
		t.Fatalf("MinimalEnv = %q", got)
	}
}
