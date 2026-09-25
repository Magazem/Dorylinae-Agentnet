package device

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Scope limits (Docs/protocol/device.md §Scope).
const (
	MaxScopeTypes    = 3
	MaxScopeRepos    = 16
	MaxScopeCommands = 32
	MaxArgv          = 64
	MaxArgBytes      = 4096
	MaxTimeoutS      = 3600
	MaxEnvNames      = 32
	MaxNameLen       = 64
	MaxEnvNameLen    = 128
	MaxScopeTTL      = 30 * 24 * time.Hour
)

// Out-of-scope checks, the "check" of device.out_of_scope
// (Docs/protocol/device.md §Audit), in the order they are applied.
const (
	CheckLink    = "link"
	CheckScope   = "scope"
	CheckExpired = "expired"
	CheckType    = "type"
	CheckCommand = "command"
	CheckCreated = "created"
	CheckQueue   = "queue"
)

// ErrNoScope is returned when a link has no scope.
var ErrNoScope = errors.New("device: no scope")

// Scope is what the helper's human allows its controller to run
// (Docs/protocol/device.md §Scope). Once validated, every repo path is the
// EvalSymlinks-resolved directory and every argv[0] the absolute path found
// by exec.LookPath at set time.
type Scope struct {
	Types    []string  `json:"types"`
	Repos    []Repo    `json:"repos"`
	Commands []Command `json:"commands"`
	Expires  string    `json:"expires"`
}

// Repo is a working directory a command may run in.
type Repo struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// Command is one allowlisted argv.
type Command struct {
	Name     string   `json:"name"`
	Repo     string   `json:"repo"`
	Argv     []string `json:"argv"`
	TimeoutS int      `json:"timeout_s"`
	Env      []string `json:"env,omitempty"`
}

// ExpiresAt parses Expires.
func (sc Scope) ExpiresAt() (time.Time, error) {
	return time.Parse(time.RFC3339, sc.Expires)
}

// Command returns the command called name, if any.
func (sc Scope) Command(name string) (Command, bool) {
	for _, c := range sc.Commands {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// RepoPath returns the path of the repo labelled label.
func (sc Scope) RepoPath(label string) (string, bool) {
	for _, r := range sc.Repos {
		if r.Label == label {
			return r.Path, true
		}
	}
	return "", false
}

// HasType reports whether typ is one of the scope's request types.
func (sc Scope) HasType(typ string) bool {
	for _, t := range sc.Types {
		if t == typ {
			return true
		}
	}
	return false
}

// CommandNames returns the command names, in order.
func (sc Scope) CommandNames() []string {
	out := make([]string, len(sc.Commands))
	for i, c := range sc.Commands {
		out[i] = c.Name
	}
	return out
}

// ScopeError is a scope that breaks a rule of Docs/protocol/device.md §Scope.
// Field names the member (for example "commands[2].argv[0]"). Forbidden is
// set for a repo path that may not be used at all (forbidden_resource);
// otherwise the error is bad_scope.
type ScopeError struct {
	Field     string
	Reason    string
	Forbidden bool
}

func (e *ScopeError) Error() string { return e.Field + ": " + e.Reason }

func scopeErr(field, format string, a ...any) *ScopeError {
	return &ScopeError{Field: field, Reason: fmt.Sprintf(format, a...)}
}

// Resolver resolves the parts of a scope that depend on the machine.
type Resolver struct {
	// RepoPath resolves an absolute directory once (EvalSymlinks) and refuses
	// the places no scope may name. A *ScopeError it returns keeps its
	// Forbidden flag; the field is filled in by ValidateScope.
	RepoPath func(raw string) (string, error)
	// LookPath finds a program; nil uses exec.LookPath.
	LookPath func(file string) (string, error)
	// CheckProgram refuses a resolved argv[0] that others can change; nil
	// uses CheckProgramOwner (review 40 L11).
	CheckProgram func(path string) error
}

// ValidName reports whether s is 1-64 characters from [a-z0-9._-], the rule
// for repo labels and command names.
func ValidName(s string) bool {
	if len(s) < 1 || len(s) > MaxNameLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// validEnvName reports whether s is a portable environment variable name.
func validEnvName(s string) bool {
	if len(s) < 1 || len(s) > MaxEnvNameLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

var validTypes = map[string]bool{"review": true, "task": true, "question": true}

// ValidateScope checks in against Docs/protocol/device.md §Scope and returns
// it resolved: repo paths through r.RepoPath and argv[0] through LookPath,
// stored as an absolute path so a later PATH change cannot swap the program.
// On Windows, a batch file is refused: CreateProcess would run it through
// cmd.exe, and the runner never uses a shell. A program that users other than
// this one (or an administrator) can change is refused too (CheckProgram).
func ValidateScope(in Scope, now time.Time, r Resolver) (Scope, error) {
	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	checkProgram := r.CheckProgram
	if checkProgram == nil {
		checkProgram = CheckProgramOwner
	}
	out := Scope{}
	if len(in.Types) < 1 || len(in.Types) > MaxScopeTypes {
		return Scope{}, scopeErr("types", "must hold 1-%d of review, task, question", MaxScopeTypes)
	}
	seen := map[string]bool{}
	for i, t := range in.Types {
		if !validTypes[t] {
			return Scope{}, scopeErr(fmt.Sprintf("types[%d]", i), "must be review, task or question")
		}
		if seen[t] {
			return Scope{}, scopeErr(fmt.Sprintf("types[%d]", i), "is listed twice")
		}
		seen[t] = true
		out.Types = append(out.Types, t)
	}

	if len(in.Repos) < 1 || len(in.Repos) > MaxScopeRepos {
		return Scope{}, scopeErr("repos", "must hold 1-%d repos", MaxScopeRepos)
	}
	labels := map[string]bool{}
	for i, rp := range in.Repos {
		field := fmt.Sprintf("repos[%d]", i)
		if !ValidName(rp.Label) {
			return Scope{}, scopeErr(field+".label", "must be 1-%d characters from [a-z0-9._-]", MaxNameLen)
		}
		if labels[rp.Label] {
			return Scope{}, scopeErr(field+".label", "is used twice")
		}
		labels[rp.Label] = true
		if !filepath.IsAbs(rp.Path) {
			return Scope{}, &ScopeError{Field: field + ".path", Reason: "must be an absolute path", Forbidden: true}
		}
		if r.RepoPath == nil {
			return Scope{}, scopeErr(field+".path", "cannot be resolved")
		}
		resolved, err := r.RepoPath(rp.Path)
		if err != nil {
			var se *ScopeError
			if errors.As(err, &se) {
				return Scope{}, &ScopeError{Field: field + ".path", Reason: se.Reason, Forbidden: se.Forbidden}
			}
			return Scope{}, &ScopeError{Field: field + ".path", Reason: err.Error(), Forbidden: true}
		}
		out.Repos = append(out.Repos, Repo{Label: rp.Label, Path: resolved})
	}

	if len(in.Commands) < 1 || len(in.Commands) > MaxScopeCommands {
		return Scope{}, scopeErr("commands", "must hold 1-%d commands", MaxScopeCommands)
	}
	names := map[string]bool{}
	for i, c := range in.Commands {
		field := fmt.Sprintf("commands[%d]", i)
		if !ValidName(c.Name) {
			return Scope{}, scopeErr(field+".name", "must be 1-%d characters from [a-z0-9._-]", MaxNameLen)
		}
		if names[c.Name] {
			return Scope{}, scopeErr(field+".name", "is used twice")
		}
		names[c.Name] = true
		if !labels[c.Repo] {
			return Scope{}, scopeErr(field+".repo", "must be one of the repo labels")
		}
		if len(c.Argv) < 1 || len(c.Argv) > MaxArgv {
			return Scope{}, scopeErr(field+".argv", "must hold 1-%d strings", MaxArgv)
		}
		for j, a := range c.Argv {
			if len(a) < 1 || len(a) > MaxArgBytes {
				return Scope{}, scopeErr(fmt.Sprintf("%s.argv[%d]", field, j), "must be 1-%d bytes", MaxArgBytes)
			}
			if strings.IndexByte(a, 0) >= 0 {
				return Scope{}, scopeErr(fmt.Sprintf("%s.argv[%d]", field, j), "must not contain NUL")
			}
		}
		prog, err := resolveProgram(c.Argv[0], lookPath)
		if err != nil {
			return Scope{}, scopeErr(field+".argv[0]", "%s", err.Error())
		}
		if err := checkProgram(prog); err != nil {
			return Scope{}, scopeErr(field+".argv[0]", "%s", err.Error())
		}
		if c.TimeoutS < 1 || c.TimeoutS > MaxTimeoutS {
			return Scope{}, scopeErr(field+".timeout_s", "must be 1-%d", MaxTimeoutS)
		}
		if len(c.Env) > MaxEnvNames {
			return Scope{}, scopeErr(field+".env", "must hold 0-%d names", MaxEnvNames)
		}
		envSeen := map[string]bool{}
		var env []string
		for j, e := range c.Env {
			ef := fmt.Sprintf("%s.env[%d]", field, j)
			if !validEnvName(e) {
				return Scope{}, scopeErr(ef, "must be an environment variable name ([A-Za-z_][A-Za-z0-9_]*, at most %d)", MaxEnvNameLen)
			}
			if strings.HasPrefix(strings.ToUpper(e), "DORYLINAE_") {
				return Scope{}, scopeErr(ef, "DORYLINAE_* variables are never passed to a command")
			}
			key := envKey(e)
			if envSeen[key] {
				return Scope{}, scopeErr(ef, "is listed twice")
			}
			envSeen[key] = true
			env = append(env, e)
		}
		argv := append([]string{prog}, c.Argv[1:]...)
		out.Commands = append(out.Commands, Command{Name: c.Name, Repo: c.Repo, Argv: argv, TimeoutS: c.TimeoutS, Env: env})
	}

	exp, err := time.Parse(time.RFC3339, in.Expires)
	if err != nil {
		return Scope{}, scopeErr("expires", "must be an RFC 3339 time")
	}
	if !exp.After(now) {
		return Scope{}, scopeErr("expires", "must be in the future")
	}
	if exp.After(now.Add(MaxScopeTTL)) {
		return Scope{}, scopeErr("expires", "must be at most 30 days from now")
	}
	out.Expires = exp.UTC().Format(time.RFC3339)
	return out, nil
}

// resolveProgram finds argv[0] once and returns its absolute path. A name
// with a directory part must already be absolute: a relative path would
// depend on the daemon's working directory.
func resolveProgram(name string, lookPath func(string) (string, error)) (string, error) {
	if strings.ContainsAny(name, `/\`) && !filepath.IsAbs(name) {
		return "", errors.New("must be a program name or an absolute path")
	}
	p, err := lookPath(name)
	if err != nil {
		return "", fmt.Errorf("program not found: %w", err)
	}
	if !filepath.IsAbs(p) {
		// exec.LookPath returns a relative path only for a PATH entry that is
		// itself relative: that would depend on the daemon's working directory.
		return "", errors.New("program does not resolve to an absolute path; give an absolute path")
	}
	if runtime.GOOS == "windows" {
		// Windows drops trailing dots and spaces from a file name, so
		// "x.bat." opens x.bat: judge the name as Windows will open it, and
		// allow only real executables (review 40 L3).
		switch strings.ToLower(filepath.Ext(strings.TrimRight(p, ". "))) {
		case ".bat", ".cmd":
			return "", errors.New("a batch file runs through cmd.exe; name the program itself")
		case ".exe", ".com":
		default:
			return "", errors.New("on Windows the program must be an .exe or .com file")
		}
	}
	return filepath.Clean(p), nil
}

// envKey folds an environment variable name the way the OS compares it.
func envKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

// DisplayQuote quotes s as a JSON string for a human to read (the approval
// summary, the CLI), and also escapes, as \uXXXX, every rune that is not
// graphic or is a format character: bidi controls (U+202E …), zero-width
// characters and the like, which json.Marshal leaves as they are and which
// could make a path or argv read differently from what runs (review 40 M1).
func DisplayQuote(s string) string {
	raw, _ := json.Marshal(s)
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range string(raw) {
		switch {
		case r == utf8.RuneError || (r > 0x7e && (!unicode.IsGraphic(r) || unicode.Is(unicode.Cf, r))):
			if r > 0xffff {
				hi, lo := utf16.EncodeRune(r)
				fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
			} else {
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DisplayArgv is DisplayQuote for an argv: a JSON array of quoted strings.
func DisplayArgv(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = DisplayQuote(a)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// CanonicalScope is the stored form of a validated scope.
func CanonicalScope(sc Scope) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(sc); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// ActiveHelperLinkTx returns the active link in which peer is this device's
// controller, if any. Everything about a helper's authority is keyed on the
// peer's key, never on the link id: the two sides can hold different ids
// after an asymmetric retry (review 36 L6).
func (s *Store) ActiveHelperLinkTx(ctx context.Context, q querier, peer string) (Link, bool, error) {
	l, err := scanLink(q.QueryRowContext(ctx, `SELECT `+linkCols+` FROM device_links WHERE peer = ? AND state = 'active' AND role = 'helper'`, peer))
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, false, nil
	}
	if err != nil {
		return Link{}, false, fmt.Errorf("device: read link: %w", err)
	}
	return l, true, nil
}

// ActiveHelperLink is ActiveHelperLinkTx on the store's connection.
func (s *Store) ActiveHelperLink(ctx context.Context, peer string) (Link, bool, error) {
	return s.ActiveHelperLinkTx(ctx, s.DB, peer)
}

// ScopeTx returns the scope of link id.
func (s *Store) ScopeTx(ctx context.Context, q querier, linkID string) (Scope, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT scope FROM device_scopes WHERE link = ?`, linkID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Scope{}, ErrNoScope
	}
	if err != nil {
		return Scope{}, fmt.Errorf("device: read scope: %w", err)
	}
	var sc Scope
	if err := json.Unmarshal([]byte(raw), &sc); err != nil {
		return Scope{}, fmt.Errorf("device: decode scope: %w", err)
	}
	return sc, nil
}

// Scope is ScopeTx on the store's connection.
func (s *Store) Scope(ctx context.Context, linkID string) (Scope, error) {
	return s.ScopeTx(ctx, s.DB, linkID)
}

// SetScopeTx stores sc (already validated) as the scope of link id, replacing
// any previous one atomically.
func (s *Store) SetScopeTx(ctx context.Context, tx *sql.Tx, linkID string, sc Scope, approvalID string, now time.Time) error {
	raw, err := CanonicalScope(sc)
	if err != nil {
		return err
	}
	exp, err := sc.ExpiresAt()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO device_scopes (link, scope, expires, approval, created) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (link) DO UPDATE SET scope = excluded.scope, expires = excluded.expires, approval = excluded.approval, created = excluded.created`,
		linkID, raw, fmtTime(exp), approvalID, fmtTime(now)); err != nil {
		return fmt.Errorf("device: set scope: %w", err)
	}
	return nil
}

// ClearScopeTx deletes the scope of link id and reports whether one existed.
func (s *Store) ClearScopeTx(ctx context.Context, tx *sql.Tx, linkID string) (bool, error) {
	res, err := tx.ExecContext(ctx, `DELETE FROM device_scopes WHERE link = ?`, linkID)
	if err != nil {
		return false, fmt.Errorf("device: clear scope: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RunPlan is what an in-scope request runs: the command resolved from the
// current scope.
type RunPlan struct {
	Link    Link
	Command Command
	Dir     string
	// Expires is when the scope ends: a run still going then is stopped.
	Expires time.Time
}

// CheckRunTx applies the in-scope checks 1-5 of Docs/protocol/device.md
// §Running to a request from peer, in order, through q; the limits (check 6)
// are the caller's. It returns the plan, or the name of the first check that
// failed. run is "" when the request has no run member. created is compared
// with the link's activation at whole seconds, because a request's created
// time is truncated to the second (Docs/protocol/request.md §Submitting).
func (s *Store) CheckRunTx(ctx context.Context, q querier, peer, typ, run string, created, now time.Time) (RunPlan, string, error) {
	link, ok, err := s.ActiveHelperLinkTx(ctx, q, peer)
	if err != nil {
		return RunPlan{}, "", err
	}
	if !ok {
		return RunPlan{}, CheckLink, nil
	}
	sc, err := s.ScopeTx(ctx, q, link.ID)
	if errors.Is(err, ErrNoScope) {
		return RunPlan{}, CheckScope, nil
	}
	if err != nil {
		return RunPlan{}, "", err
	}
	exp, err := sc.ExpiresAt()
	if err != nil || !now.Before(exp) {
		return RunPlan{}, CheckExpired, nil
	}
	if !sc.HasType(typ) {
		return RunPlan{}, CheckType, nil
	}
	cmd, ok := sc.Command(run)
	if run == "" || !ok {
		return RunPlan{}, CheckCommand, nil
	}
	dir, ok := sc.RepoPath(cmd.Repo)
	if !ok {
		return RunPlan{}, CheckCommand, nil
	}
	if created.Before(link.ActivatedAt.Truncate(time.Second)) {
		return RunPlan{}, CheckCreated, nil
	}
	return RunPlan{Link: link, Command: cmd, Dir: dir, Expires: exp}, "", nil
}
