package device

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

var scopeNow = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// fakeProgram is the absolute path the fake LookPath resolves every name to.
func fakeProgram() string {
	if runtime.GOOS == "windows" {
		return `C:\tools\prog.exe`
	}
	return "/usr/bin/prog"
}

func fakeResolver() Resolver {
	return Resolver{
		RepoPath: func(raw string) (string, error) { return raw, nil },
		LookPath: func(string) (string, error) { return fakeProgram(), nil },
		// The fake program does not exist; the ownership rule has its own
		// tests (perm_test.go).
		CheckProgram: func(string) error { return nil },
	}
}

func repoPath(i int) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`C:\src\r%d`, i)
	}
	return fmt.Sprintf("/src/r%d", i)
}

func baseScope() Scope {
	return Scope{
		Types:    []string{"task"},
		Repos:    []Repo{{Label: "agentnet", Path: repoPath(0)}},
		Commands: []Command{{Name: "test", Repo: "agentnet", Argv: []string{"go", "test", "./..."}, TimeoutS: 900, Env: []string{"GOFLAGS"}}},
		Expires:  scopeNow.Add(7 * 24 * time.Hour).Format(time.RFC3339),
	}
}

func names(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

// Every scope member at its limit is accepted and one over is refused, with
// the field named (ticket 2.D2 acceptance).
func TestValidateScopeLimits(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*Scope)
		field string // "" = valid
	}{
		{"base", func(*Scope) {}, ""},
		{"3 types", func(s *Scope) { s.Types = []string{"review", "task", "question"} }, ""},
		{"no types", func(s *Scope) { s.Types = nil }, "types"},
		{"4 types", func(s *Scope) { s.Types = []string{"review", "task", "question", "task"} }, "types"},
		{"duplicate type", func(s *Scope) { s.Types = []string{"task", "task"} }, "types[1]"},
		{"unknown type", func(s *Scope) { s.Types = []string{"deploy"} }, "types[0]"},
		{"16 repos", func(s *Scope) {
			s.Repos = nil
			for i := 0; i < 16; i++ {
				s.Repos = append(s.Repos, Repo{Label: fmt.Sprintf("r%d", i), Path: repoPath(i)})
			}
			s.Commands[0].Repo = "r0"
		}, ""},
		{"17 repos", func(s *Scope) {
			s.Repos = nil
			for i := 0; i < 17; i++ {
				s.Repos = append(s.Repos, Repo{Label: fmt.Sprintf("r%d", i), Path: repoPath(i)})
			}
			s.Commands[0].Repo = "r0"
		}, "repos"},
		{"no repos", func(s *Scope) { s.Repos = nil }, "repos"},
		{"label 64", func(s *Scope) { l := strings.Repeat("a", 64); s.Repos[0].Label = l; s.Commands[0].Repo = l }, ""},
		{"label 65", func(s *Scope) { l := strings.Repeat("a", 65); s.Repos[0].Label = l; s.Commands[0].Repo = l }, "repos[0].label"},
		{"label upper", func(s *Scope) { s.Repos[0].Label = "Agent"; s.Commands[0].Repo = "Agent" }, "repos[0].label"},
		{"duplicate label", func(s *Scope) { s.Repos = append(s.Repos, Repo{Label: "agentnet", Path: repoPath(1)}) }, "repos[1].label"},
		{"relative path", func(s *Scope) { s.Repos[0].Path = "src/x" }, "repos[0].path"},
		{"32 commands", func(s *Scope) {
			s.Commands = nil
			for _, n := range names("c", 32) {
				s.Commands = append(s.Commands, Command{Name: n, Repo: "agentnet", Argv: []string{"go"}, TimeoutS: 1})
			}
		}, ""},
		{"33 commands", func(s *Scope) {
			s.Commands = nil
			for _, n := range names("c", 33) {
				s.Commands = append(s.Commands, Command{Name: n, Repo: "agentnet", Argv: []string{"go"}, TimeoutS: 1})
			}
		}, "commands"},
		{"no commands", func(s *Scope) { s.Commands = nil }, "commands"},
		{"name 64", func(s *Scope) { s.Commands[0].Name = strings.Repeat("n", 64) }, ""},
		{"name 65", func(s *Scope) { s.Commands[0].Name = strings.Repeat("n", 65) }, "commands[0].name"},
		{"name with space", func(s *Scope) { s.Commands[0].Name = "go test" }, "commands[0].name"},
		{"duplicate name", func(s *Scope) { s.Commands = append(s.Commands, s.Commands[0]) }, "commands[1].name"},
		{"unknown repo", func(s *Scope) { s.Commands[0].Repo = "other" }, "commands[0].repo"},
		{"argv 64", func(s *Scope) { s.Commands[0].Argv = append([]string{"go"}, names("a", 63)...) }, ""},
		{"argv 65", func(s *Scope) { s.Commands[0].Argv = append([]string{"go"}, names("a", 64)...) }, "commands[0].argv"},
		{"no argv", func(s *Scope) { s.Commands[0].Argv = nil }, "commands[0].argv"},
		{"arg 4096", func(s *Scope) { s.Commands[0].Argv[1] = strings.Repeat("x", 4096) }, ""},
		{"arg 4097", func(s *Scope) { s.Commands[0].Argv[1] = strings.Repeat("x", 4097) }, "commands[0].argv[1]"},
		{"empty arg", func(s *Scope) { s.Commands[0].Argv[1] = "" }, "commands[0].argv[1]"},
		{"NUL in arg", func(s *Scope) { s.Commands[0].Argv[1] = "a\x00b" }, "commands[0].argv[1]"},
		{"relative argv0 path", func(s *Scope) { s.Commands[0].Argv[0] = "bin/go" }, "commands[0].argv[0]"},
		{"timeout 1", func(s *Scope) { s.Commands[0].TimeoutS = 1 }, ""},
		{"timeout 3600", func(s *Scope) { s.Commands[0].TimeoutS = 3600 }, ""},
		{"timeout 0", func(s *Scope) { s.Commands[0].TimeoutS = 0 }, "commands[0].timeout_s"},
		{"timeout 3601", func(s *Scope) { s.Commands[0].TimeoutS = 3601 }, "commands[0].timeout_s"},
		{"env 32", func(s *Scope) { s.Commands[0].Env = names("V", 32) }, ""},
		{"env 33", func(s *Scope) { s.Commands[0].Env = names("V", 33) }, "commands[0].env"},
		{"env name 128", func(s *Scope) { s.Commands[0].Env = []string{strings.Repeat("V", 128)} }, ""},
		{"env name 129", func(s *Scope) { s.Commands[0].Env = []string{strings.Repeat("V", 129)} }, "commands[0].env[0]"},
		{"env bad name", func(s *Scope) { s.Commands[0].Env = []string{"A=B"} }, "commands[0].env[0]"},
		{"env DORYLINAE", func(s *Scope) { s.Commands[0].Env = []string{"DORYLINAE_HOME"} }, "commands[0].env[0]"},
		{"env duplicate", func(s *Scope) { s.Commands[0].Env = []string{"X", "X"} }, "commands[0].env[1]"},
		{"expires 30 d", func(s *Scope) { s.Expires = scopeNow.Add(MaxScopeTTL).Format(time.RFC3339) }, ""},
		{"expires 30 d + 1 s", func(s *Scope) { s.Expires = scopeNow.Add(MaxScopeTTL + time.Second).Format(time.RFC3339) }, "expires"},
		{"expires now", func(s *Scope) { s.Expires = scopeNow.Format(time.RFC3339) }, "expires"},
		{"expires missing", func(s *Scope) { s.Expires = "" }, "expires"},
	}
	for _, tc := range cases {
		sc := baseScope()
		sc.Commands[0].Argv = append([]string(nil), sc.Commands[0].Argv...)
		tc.mut(&sc)
		_, err := ValidateScope(sc, scopeNow, fakeResolver())
		var se *ScopeError
		switch {
		case tc.field == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.field != "" && (!errors.As(err, &se) || se.Field != tc.field):
			t.Errorf("%s: err = %v, want a bad_scope on %s", tc.name, err, tc.field)
		}
	}
}

// argv[0] is resolved once, at set time, to an absolute path: a later PATH
// change cannot swap the program (ticket 2.D2 acceptance).
func TestValidateScopeResolvesArgv0(t *testing.T) {
	dir := testutil.PrivateDir(t)
	prog := filepath.Join(dir, "tool")
	if runtime.GOOS == "windows" {
		prog += ".exe"
	}
	if err := os.WriteFile(prog, []byte("x"), 0o700); err != nil { //nolint:gosec // the test program must be executable for LookPath
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	sc := baseScope()
	sc.Commands[0].Argv = []string{"tool", "--flag"}
	got, err := ValidateScope(sc, scopeNow, Resolver{RepoPath: func(raw string) (string, error) { return raw, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if a0 := got.Commands[0].Argv[0]; !filepath.IsAbs(a0) || !strings.EqualFold(a0, prog) {
		t.Fatalf("argv[0] = %q, want %q", a0, prog)
	}
	if got.Commands[0].Argv[1] != "--flag" {
		t.Fatalf("argv changed: %q", got.Commands[0].Argv)
	}
	// Not found on PATH.
	sc.Commands[0].Argv = []string{"no-such-tool-xyz"}
	var se *ScopeError
	if _, err := ValidateScope(sc, scopeNow, Resolver{RepoPath: func(raw string) (string, error) { return raw, nil }}); !errors.As(err, &se) || se.Field != "commands[0].argv[0]" {
		t.Fatalf("missing program: %v", err)
	}
}

// A batch file would run through cmd.exe: refused on Windows.
func TestValidateScopeRefusesBatchFiles(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch files are a Windows rule")
	}
	dir := testutil.TempDir(t)
	for _, name := range []string{"run.bat", "run.CMD"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("@echo off"), 0o600); err != nil {
			t.Fatal(err)
		}
		sc := baseScope()
		sc.Commands[0].Argv = []string{p}
		var se *ScopeError
		if _, err := ValidateScope(sc, scopeNow, Resolver{RepoPath: func(raw string) (string, error) { return raw, nil }, LookPath: exec.LookPath}); !errors.As(err, &se) || se.Field != "commands[0].argv[0]" {
			t.Fatalf("%s: err = %v, want refused", name, err)
		}
	}
	// Review 40 L3: Windows opens "run.bat." and "run.bat " as run.bat, and
	// anything that is not an .exe or .com is refused.
	for _, p := range []string{`C:\x\run.bat.`, `C:\x\run.cmd .`, `C:\x\script.ps1`, `C:\x\noext`} {
		sc := baseScope()
		sc.Commands[0].Argv = []string{p}
		var se *ScopeError
		lp := func(string) (string, error) { return p, nil }
		if _, err := ValidateScope(sc, scopeNow, Resolver{RepoPath: func(raw string) (string, error) { return raw, nil }, LookPath: lp}); !errors.As(err, &se) || se.Field != "commands[0].argv[0]" {
			t.Fatalf("%s: err = %v, want refused", p, err)
		}
	}
}

// Review 40 M1: the display form escapes bidi controls, zero-width and other
// invisible characters, so a path or argv reads as what runs.
func TestDisplayQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                `"plain"`,
		"caf\u00e9":            `"café"`,
		"a\u202eb":             `"a\u202eb"`,
		"x\u200by":             `"x\u200by"`,
		"q\"\\\n\x1b":          `"q\"\\\n\u001b"`,
		"\U000E0041tag":        `"\udb40\udc41tag"`,
		"bad\xffbyte\ufffd":    `"bad\ufffdbyte\ufffd"`,
		"line\u2028sep\u00a0x": `"line\u2028sep` + "\u00a0" + `x"`,
	} {
		if got := DisplayQuote(in); got != want {
			t.Errorf("DisplayQuote(%q) = %s, want %s", in, got, want)
		}
	}
	if got := DisplayArgv([]string{"/bin/rm", "-f\u202e"}); got != `["/bin/rm","-f\u202e"]` {
		t.Errorf("DisplayArgv = %s", got)
	}
}

// A repo path refused by the resolver keeps its forbidden flag and gets the
// field name.
func TestValidateScopeForbiddenRepo(t *testing.T) {
	r := Resolver{
		RepoPath: func(string) (string, error) {
			return "", &ScopeError{Reason: "may not be the home directory", Forbidden: true}
		},
		LookPath: func(string) (string, error) { return fakeProgram(), nil },
	}
	var se *ScopeError
	if _, err := ValidateScope(baseScope(), scopeNow, r); !errors.As(err, &se) || !se.Forbidden || se.Field != "repos[0].path" {
		t.Fatalf("err = %v", err)
	}
}

// insertLink stores a link row directly.
func insertLink(t *testing.T, s *Store, id, peer, role, state string, activated time.Time) {
	t.Helper()
	if _, err := s.DB.Exec(`INSERT INTO device_links (id, peer, role, state, nonce, created, activated_at, updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, peer, role, state, "00112233445566778899aabbccddeeff", fmtTime(activated), fmtTime(activated), fmtTime(activated)); err != nil {
		t.Fatal(err)
	}
}

// CheckRunTx applies the checks in the spec's order and names the first that
// fails (Docs/protocol/device.md §Running, §Audit).
func TestCheckRunOrder(t *testing.T) {
	s, clk := newTestStore(t)
	ctx := context.Background()
	activated := clk.Now()
	created := activated.Add(time.Minute)
	check := func(peer, typ, run string, created time.Time) string {
		t.Helper()
		_, c, err := s.CheckRunTx(ctx, s.DB, peer, typ, run, created, clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := check("C", "task", "test", created); c != CheckLink {
		t.Fatalf("no link: %q", c)
	}
	// This device is the controller of C: not a helper link.
	insertLink(t, s, "l-00000000000000000000000000000001", "C", RoleController, StateActive, activated)
	if c := check("C", "task", "test", created); c != CheckLink {
		t.Fatalf("controller link: %q", c)
	}
	const id = "l-00000000000000000000000000000002"
	insertLink(t, s, id, "H", RoleHelper, StateActive, activated)
	if c := check("H", "task", "test", created); c != CheckScope {
		t.Fatalf("no scope: %q", c)
	}
	sc, err := ValidateScope(baseScope(), clk.Now(), fakeResolver())
	if err != nil {
		t.Fatal(err)
	}
	inTx(t, s, func(tx *sql.Tx) {
		if err := s.SetScopeTx(ctx, tx, id, sc, "a-1", clk.Now()); err != nil {
			t.Fatal(err)
		}
	})
	if c := check("H", "review", "test", created); c != CheckType {
		t.Fatalf("wrong type: %q", c)
	}
	if c := check("H", "task", "", created); c != CheckCommand {
		t.Fatalf("no run: %q", c)
	}
	if c := check("H", "task", "deploy", created); c != CheckCommand {
		t.Fatalf("unknown command: %q", c)
	}
	if c := check("H", "task", "test", activated.Add(-2*time.Second)); c != CheckCreated {
		t.Fatalf("created before activation: %q", c)
	}
	// Same second as the activation (created is truncated to seconds): in scope.
	if c := check("H", "task", "test", activated.Truncate(time.Second)); c != "" {
		t.Fatalf("created in the activation second: %q", c)
	}
	plan, c, err := s.CheckRunTx(ctx, s.DB, "H", "task", "test", created, clk.Now())
	if err != nil || c != "" || plan.Command.Name != "test" || plan.Dir != repoPath(0) || plan.Link.ID != id {
		t.Fatalf("in scope: plan %+v, check %q, err %v", plan, c, err)
	}
	clk.Advance(8 * 24 * time.Hour)
	if c := check("H", "task", "test", created); c != CheckExpired {
		t.Fatalf("expired: %q", c)
	}
	// A revoked link is no link, and its scope went with it.
	inTx(t, s, func(tx *sql.Tx) {
		if _, err := s.RevokeForPeerTx(ctx, tx, "H", clk.Now()); err != nil {
			t.Fatal(err)
		}
	})
	if c := check("H", "task", "test", created); c != CheckLink {
		t.Fatalf("revoked: %q", c)
	}
	if _, err := s.Scope(ctx, id); !errors.Is(err, ErrNoScope) {
		t.Fatalf("scope after unlink: %v", err)
	}
}

// A new scope replaces the old one; clearing removes it.
func TestScopeReplaceAndClear(t *testing.T) {
	s, clk := newTestStore(t)
	ctx := context.Background()
	const id = "l-00000000000000000000000000000003"
	insertLink(t, s, id, "H", RoleHelper, StateActive, clk.Now())
	first, _ := ValidateScope(baseScope(), clk.Now(), fakeResolver())
	second := baseScope()
	second.Commands[0].Name = "lint"
	second2, _ := ValidateScope(second, clk.Now(), fakeResolver())
	for _, sc := range []Scope{first, second2} {
		inTx(t, s, func(tx *sql.Tx) {
			if err := s.SetScopeTx(ctx, tx, id, sc, "a-x", clk.Now()); err != nil {
				t.Fatal(err)
			}
		})
	}
	got, err := s.Scope(ctx, id)
	if err != nil || len(got.Commands) != 1 || got.Commands[0].Name != "lint" {
		t.Fatalf("scope = %+v, %v", got, err)
	}
	inTx(t, s, func(tx *sql.Tx) {
		if ok, err := s.ClearScopeTx(ctx, tx, id); err != nil || !ok {
			t.Fatalf("clear: %v %v", ok, err)
		}
	})
	if _, err := s.Scope(ctx, id); !errors.Is(err, ErrNoScope) {
		t.Fatalf("after clear: %v", err)
	}
}

// RevokePending touches only a row still pending approval (review 36 L8).
func TestRevokePendingOnlyPending(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	l, err := s.CreateIntent(ctx, "P", RoleHelper)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokePending(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	var st string
	if err := s.DB.QueryRow(`SELECT state FROM device_links WHERE id = ?`, l.ID).Scan(&st); err != nil || st != StateRevoked {
		t.Fatalf("state = %q, %v", st, err)
	}
	// The slot is free: a helper link with another peer is allowed at once.
	if _, err := s.CreateIntent(ctx, "Q", RoleHelper); err != nil {
		t.Fatalf("after RevokePending: %v", err)
	}
	insertLink(t, s, "l-00000000000000000000000000000004", "R", RoleController, StateActive, s.Time())
	if err := s.RevokePending(ctx, "l-00000000000000000000000000000004"); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT state FROM device_links WHERE id = 'l-00000000000000000000000000000004'`).Scan(&st); err != nil || st != StateActive {
		t.Fatalf("an active link was touched: %q", st)
	}
}
