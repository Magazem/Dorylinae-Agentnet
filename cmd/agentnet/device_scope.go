package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
)

// defaultTimeoutS is a command's timeout when --timeout does not name it.
const defaultTimeoutS = 900

const deviceScopeHelp = `Set, clear or show what your controller device may run on this helper.

Usage:
  agentnet device scope <controller> --types T[,T] --repo LABEL=PATH...
                        --command 'NAME=REPO:ARGV-JSON'... --expires D
                        [--timeout NAME=SECONDS]... [--env NAME=VAR]... [--json]
  agentnet device scope <controller> --from-file scope.json [--json]
  agentnet device scope <controller> --clear [--json]
  agentnet device scope <controller> --show [--json]

Run it on the HELPER. The scope stays on this device and is never sent to the
controller. Setting a scope needs your approval in the AgentNet approval
window, which shows every command's repo path and its full argv with the
program resolved to an absolute path: exactly what will run. A new scope
replaces the old one. Clearing needs no approval.

A request from the controller runs only if its type is in --types, it names
one of the commands (agentnet request <helper> task --run NAME ...), it was
sent after the link became active, and the scope has not expired. Anything
else lands in the normal inbox. The command runs with no shell, in its repo,
with a minimal environment (PATH, HOME or USERPROFILE, TMP/TEMP/TMPDIR, LANG,
LC_ALL and the Windows system variables, plus the --env names), and is killed
with its whole process tree at its timeout. The controller gets the exit code
and the last 32 KiB of output. One run at a time, at most 8 queued and 60 per
day.

Only name repositories you trust: running a repository's tests runs its code,
just as if you ran them by hand.

Flags:
  --types T[,T]          request types that may run: review, task, question
  --repo LABEL=PATH      repeatable, 1-16. LABEL is 1-64 of [a-z0-9._-]; PATH an
                         existing absolute directory (not your home directory,
                         the AgentNet config dir or a filesystem root)
  --command 'NAME=REPO:ARGV-JSON'
                         repeatable, 1-32. NAME 1-64 of [a-z0-9._-], REPO one of
                         the labels, ARGV-JSON a JSON array of 1-64 strings,
                         e.g. 'test=agentnet:["go","test","./..."]'. The program
                         (argv[0]) is looked up now, on this device's PATH, and
                         stored as an absolute path; a .bat or .cmd file is
                         refused (it would run through cmd.exe), and so is a
                         program that other users can change, it or a folder
                         above it (writable_by_others; checked again at every
                         run)
  --timeout NAME=SECONDS repeatable: the command's timeout, 1-3600 (default 900)
  --env NAME=VAR         repeatable: pass the daemon's VAR to command NAME too
                         (up to 32 per command; never DORYLINAE_*)
  --expires D            required: an RFC 3339 time or a duration from now
                         (90m, 12h, 7d); at most 30 days
  --from-file F          read the whole scope as JSON: {"types", "repos":
                         [{"label","path"}], "commands": [{"name","repo","argv",
                         "timeout_s","env"}], "expires"}
  --clear                remove the scope (queued runs are dropped, a running
                         one is killed and reported as cancelled)
  --show                 print the stored scope
  --json                 print {"ok":true,...} (or {"ok":false,"error":{...}})

Errors: unknown_link (no active link with that device), not_helper (this device
is the controller), bad_scope (the message names the field), forbidden_resource
(a repo path that may not be used), approval_limit, approval_locked,
approval_unavailable.

Exit codes: 0 approval created (or cleared, shown), 1 error, 2 usage,
3 daemon not running.
`

type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

// parseExpires accepts an RFC 3339 time or a duration from now with an
// optional "d" (24 h) suffix.
func parseExpires(v string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if strings.HasSuffix(v, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil || n <= 0 {
			return time.Time{}, fmt.Errorf("--expires: %q is not a time or a duration", v)
		}
		return now.Add(time.Duration(n) * 24 * time.Hour), nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("--expires: %q is not a time or a duration", v)
	}
	return now.Add(d), nil
}

// buildScope turns the flags into a scope; the daemon validates it.
func buildScope(types string, repos, commands, timeouts, envs []string, expires string, now time.Time) (device.Scope, error) {
	var sc device.Scope
	for _, t := range strings.Split(types, ",") {
		if t = strings.TrimSpace(t); t != "" {
			sc.Types = append(sc.Types, t)
		}
	}
	for _, r := range repos {
		label, path, ok := strings.Cut(r, "=")
		if !ok || label == "" || path == "" {
			return device.Scope{}, fmt.Errorf("--repo must be LABEL=PATH, got %q", r)
		}
		sc.Repos = append(sc.Repos, device.Repo{Label: label, Path: path})
	}
	timeoutOf := map[string]int{}
	for _, t := range timeouts {
		name, secs, ok := strings.Cut(t, "=")
		n, err := strconv.Atoi(secs)
		if !ok || name == "" || err != nil {
			return device.Scope{}, fmt.Errorf("--timeout must be NAME=SECONDS, got %q", t)
		}
		timeoutOf[name] = n
	}
	envOf := map[string][]string{}
	for _, e := range envs {
		name, v, ok := strings.Cut(e, "=")
		if !ok || name == "" || v == "" {
			return device.Scope{}, fmt.Errorf("--env must be NAME=VAR, got %q", e)
		}
		envOf[name] = append(envOf[name], v)
	}
	known := map[string]bool{}
	for _, c := range commands {
		name, rest, ok := strings.Cut(c, "=")
		repo, argvJSON, ok2 := strings.Cut(rest, ":")
		if !ok || !ok2 || name == "" || repo == "" {
			return device.Scope{}, fmt.Errorf("--command must be NAME=REPO:ARGV-JSON, got %q", c)
		}
		var argv []string
		if err := json.Unmarshal([]byte(argvJSON), &argv); err != nil {
			return device.Scope{}, fmt.Errorf("--command %s: ARGV-JSON must be a JSON array of strings: %w", name, err)
		}
		timeout := defaultTimeoutS
		if t, ok := timeoutOf[name]; ok {
			timeout = t
		}
		known[name] = true
		sc.Commands = append(sc.Commands, device.Command{Name: name, Repo: repo, Argv: argv, TimeoutS: timeout, Env: envOf[name]})
	}
	for name := range timeoutOf {
		if !known[name] {
			return device.Scope{}, fmt.Errorf("--timeout names %q, which no --command defines", name)
		}
	}
	for name := range envOf {
		if !known[name] {
			return device.Scope{}, fmt.Errorf("--env names %q, which no --command defines", name)
		}
	}
	exp, err := parseExpires(expires, now)
	if err != nil {
		return device.Scope{}, err
	}
	sc.Expires = exp.UTC().Format(time.RFC3339)
	return sc, nil
}

func printScope(w io.Writer, sc device.Scope) {
	_, _ = fmt.Fprintf(w, "Types: %s\nExpires: %s\n", strings.Join(sc.Types, ", "), sc.Expires)
	for _, c := range sc.Commands {
		dir, _ := sc.RepoPath(c.Repo)
		// Quoted and escaped: a path or argv holding a control or bidi
		// character cannot drive the terminal or change how the line reads
		// (review 40 L6).
		_, _ = fmt.Fprintf(w, "  %s  in %s (%s)\n      runs %s, timeout %d s", c.Name, c.Repo, device.DisplayQuote(dir), device.DisplayArgv(c.Argv), c.TimeoutS)
		if len(c.Env) > 0 {
			_, _ = fmt.Fprintf(w, ", env %s", strings.Join(c.Env, " "))
		}
		_, _ = fmt.Fprintln(w)
	}
}

func runDeviceScope(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet device scope", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	types := fs.String("types", "", "request types that may run")
	expires := fs.String("expires", "", "RFC 3339 time or a duration from now")
	fromFile := fs.String("from-file", "", "read the scope as JSON")
	clearIt := fs.Bool("clear", false, "remove the scope")
	show := fs.Bool("show", false, "print the stored scope")
	var repos, commands, timeouts, envs repeated
	fs.Var(&repos, "repo", "LABEL=PATH (repeatable)")
	fs.Var(&commands, "command", "NAME=REPO:ARGV-JSON (repeatable)")
	fs.Var(&timeouts, "timeout", "NAME=SECONDS (repeatable)")
	fs.Var(&envs, "env", "NAME=VAR (repeatable)")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, deviceScopeHelp) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <controller> (see 'agentnet device scope --help')")
	}
	building := *types != "" || len(repos) > 0 || len(commands) > 0 || len(timeouts) > 0 || len(envs) > 0 || *expires != ""
	modes := 0
	for _, on := range []bool{building, *fromFile != "", *clearIt, *show} {
		if on {
			modes++
		}
	}
	if modes != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give either the scope flags, --from-file, --clear or --show (see 'agentnet device scope --help')")
	}
	peer := pos[0]
	switch {
	case *clearIt:
		var res daemon.DeviceScopeClearResult
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "device_scope_clear", daemon.DevicePeerParams{Peer: peer}, &res); code != exitOK {
			return code
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(struct {
				OK bool `json:"ok"`
				daemon.DeviceScopeClearResult
			}{OK: true, DeviceScopeClearResult: res})
			return exitOK
		}
		_, _ = fmt.Fprintf(stdout, "Scope for %s cleared; nothing from it runs here any more.\n", devicePeerLabel(res.Link.Peer))
		return exitOK
	case *show:
		var res daemon.DeviceScopeShowResult
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "device_scope_show", daemon.DevicePeerParams{Peer: peer}, &res); code != exitOK {
			return code
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(struct {
				OK bool `json:"ok"`
				daemon.DeviceScopeShowResult
			}{OK: true, DeviceScopeShowResult: res})
			return exitOK
		}
		printScope(stdout, res.Scope)
		return exitOK
	}
	var raw []byte
	if *fromFile != "" {
		raw, err = os.ReadFile(*fromFile)
		if err != nil {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("--from-file: %v", err))
		}
	} else {
		if *types == "" || len(repos) == 0 || len(commands) == 0 || *expires == "" {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--types, --repo, --command and --expires are required (see 'agentnet device scope --help')")
		}
		sc, err := buildScope(*types, repos, commands, timeouts, envs, *expires, time.Now())
		if err != nil {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
		}
		raw, _ = json.Marshal(sc) // a struct of strings, ints and slices: cannot fail
	}
	var res daemon.DeviceScopeSetResult
	if code := callDaemon(*asJSON, stdout, stderr, approveTimeout, "device_scope_set", daemon.DeviceScopeSetParams{Peer: peer, Scope: raw}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK bool `json:"ok"`
			daemon.DeviceScopeSetResult
		}{OK: true, DeviceScopeSetResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintln(stdout, "This scope will be stored once you approve it:")
	printScope(stdout, res.Scope)
	_, _ = fmt.Fprintf(stdout, "Approval %s pending. Type the code in the AgentNet approval window (reopen it with 'agentnet approve --open %s').\n", res.Approval.ID, res.Approval.ID)
	return exitOK
}
