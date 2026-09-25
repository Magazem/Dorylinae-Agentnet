package main

// Ticket 2.1b: `agentnet sessions`, `session`, `result`, `wait`,
// `accept-result` (Docs/protocol/work-session.md §CLI).

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

// sessionListBody is the machine-readable output of `sessions --json`.
type sessionListBody struct {
	OK bool `json:"ok"`
	daemon.SessionListResult
}

// sessionShowBody is the machine-readable output of `session <id> --json`.
type sessionShowBody struct {
	OK bool `json:"ok"`
	daemon.SessionShowResult
}

// sessionActionBody is the machine-readable output of the session-mutating
// commands (`session <id> --discard`, etc.) under --json.
type sessionActionBody struct {
	OK bool `json:"ok"`
	daemon.SessionResult
}

const sessionsUsage = `Lists work sessions (Docs/protocol/work-session.md).

Usage:
  agentnet sessions [--state S] [--role requester|worker] [--json]

Flags:
  --state S   open, awaiting_result, quarantined or closed
  --role R    requester or worker
  --json      print machine-readable JSON on stdout

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
`

func runSessions(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet sessions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	state := fs.String("state", "", "open, awaiting_result, quarantined or closed")
	role := fs.String("role", "", "requester or worker")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, sessionsUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) > 0 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", pos[0]))
	}
	params := map[string]any{}
	if *state != "" {
		params["state"] = *state
	}
	if *role != "" {
		params["role"] = *role
	}
	var res daemon.SessionListResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "ws_list", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		if res.Sessions == nil {
			res.Sessions = []daemon.SessionView{}
		}
		_ = json.NewEncoder(stdout).Encode(sessionListBody{OK: true, SessionListResult: res})
		return exitOK
	}
	if len(res.Sessions) == 0 {
		_, _ = fmt.Fprintln(stdout, "No sessions.")
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tROLE\tSTATE\tPEER\tREQUEST\tROUND")
	for _, s := range res.Sessions {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n", s.ID, s.Role, s.State, s.Peer.Name, s.Request.Title, s.Round)
	}
	_ = tw.Flush()
	return exitOK
}

const sessionUsage = `Shows or changes one work session (Docs/protocol/work-session.md).

Usage:
  agentnet session <id> [--json]
  agentnet session <id> --request-changes TEXT | --changes-from-file F [--json]
  agentnet session <id> --discard [--json]
  agentnet session <id> --cancel [--reason R] [--json]
  agentnet session <id> --release [--json]

<id> is a session id (s-...) or the request id it belongs to (r-...).

Flags:
  --request-changes TEXT     ask the worker to submit again (1-4000 characters).
                             Also allowed straight from a quarantined result,
                             without releasing it first
  --changes-from-file F      read the changes text from file F (- = stdin)
  --discard                  quarantined only: close the session, cancelled,
                             without ever seeing the result
  --cancel                   close an open session
  --reason R                 optional, with --cancel, 1-500 characters
  --release                  release a quarantined result (needs a human
                             approval; prints the approval id)
  --json                     print machine-readable JSON on stdout

Exit codes: 0 done, 1 error (bad_state, unknown_session, not_requester, ...),
2 usage, 3 daemon not running.
`

func runSession(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet session", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	requestChanges := fs.String("request-changes", "", "ask the worker to submit again")
	changesFile := fs.String("changes-from-file", "", "read the changes text from a file (- = stdin)")
	discard := fs.Bool("discard", false, "quarantined only: close without ever seeing the result")
	cancel := fs.Bool("cancel", false, "close an open session")
	reason := fs.String("reason", "", "optional, with --cancel")
	release := fs.Bool("release", false, "release a quarantined result")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, sessionUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <id> (see 'agentnet session --help')")
	}
	id := pos[0]

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	actions := 0
	for _, a := range []bool{set["request-changes"] || set["changes-from-file"], *discard, *cancel, *release} {
		if a {
			actions++
		}
	}
	if actions > 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give at most one of --request-changes/--changes-from-file, --discard, --cancel or --release")
	}
	if set["request-changes"] && set["changes-from-file"] {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one of --request-changes or --changes-from-file")
	}
	if *reason != "" && !*cancel {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--reason is only valid with --cancel")
	}

	switch {
	case set["request-changes"] || set["changes-from-file"]:
		text := *requestChanges
		if set["changes-from-file"] {
			t, err := readOutputFile(*changesFile)
			if err != nil {
				return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
			}
			text = t
		}
		var res daemon.SessionResult
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "ws_request_changes", map[string]any{"id": id, "changes": text}, &res); code != exitOK {
			return code
		}
		return printSessionAction(*asJSON, stdout, "Requested changes on", res)

	case *discard:
		var res daemon.SessionResult
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "ws_discard", map[string]any{"id": id}, &res); code != exitOK {
			return code
		}
		return printSessionAction(*asJSON, stdout, "Discarded", res)

	case *cancel:
		var res daemon.SessionCancelResult
		params := map[string]any{"id": id}
		if *reason != "" {
			params["reason"] = *reason
		}
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "ws_cancel", params, &res); code != exitOK {
			return code
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(struct {
				OK bool `json:"ok"`
				daemon.SessionCancelResult
			}{OK: true, SessionCancelResult: res})
			return exitOK
		}
		verb := "Cancelled"
		if res.Duplicate {
			verb = "Already cancelling (duplicate)"
		}
		_, _ = fmt.Fprintf(stdout, "%s %s\n", verb, res.Session.ID)
		return exitOK

	case *release:
		var res map[string]json.RawMessage
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "ws_release", map[string]any{"id": id}, &res); code != exitOK {
			return code
		}
		var view approval.View
		if raw, ok := res["approval"]; ok {
			_ = json.Unmarshal(raw, &view)
		}
		if *asJSON {
			out := map[string]any{"ok": true, "approval": view}
			_ = json.NewEncoder(stdout).Encode(out)
			return exitOK
		}
		_, _ = fmt.Fprintf(stdout, "Approval %s pending; answer it in the approval window ('agentnet approve --open %s' shows it again).\n", view.ID, view.ID)
		return exitOK

	default:
		var res daemon.SessionShowResult
		if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "ws_show", map[string]any{"id": id}, &res); code != exitOK {
			return code
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(sessionShowBody{OK: true, SessionShowResult: res})
			return exitOK
		}
		printSessionHuman(stdout, res.Session)
		return exitOK
	}
}

func printSessionAction(asJSON bool, stdout io.Writer, verb string, res daemon.SessionResult) int {
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(sessionActionBody{OK: true, SessionResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "%s %s (now %s)\n", verb, res.Session.ID, res.Session.State)
	return exitOK
}

func printSessionHuman(w io.Writer, s daemon.SessionView) {
	_, _ = fmt.Fprintf(w, "%s  role %s  state %s  round %d\n", s.ID, s.Role, s.State, s.Round)
	_, _ = fmt.Fprintf(w, "  peer:    %s\n", s.Peer.Name)
	_, _ = fmt.Fprintf(w, "  request: %s (%s)\n", s.Request.Title, s.Request.ID)
	if s.Outcome != "" {
		_, _ = fmt.Fprintf(w, "  outcome: %s\n", s.Outcome)
	}
	if s.Quarantine != nil {
		_, _ = fmt.Fprintf(w, "  quarantined: status %s, %d result bytes, %d output bytes, %d artifacts (run 'agentnet session %s --release')\n",
			s.Quarantine.Status, s.Quarantine.ResultBytes, s.Quarantine.OutputBytes, s.Quarantine.Artifacts, s.ID)
	}
	if s.Result != nil {
		_, _ = fmt.Fprintf(w, "  result: status %s\n", s.Result.Status)
		if s.Result.Summary != "" {
			_, _ = fmt.Fprintf(w, "    summary: %s\n", s.Result.Summary)
		}
	}
	if s.Cancel != "" {
		_, _ = fmt.Fprintf(w, "  cancel: %s\n", s.Cancel)
	}
}

// resultParams is ws_result's params (Docs/protocol/work-session.md §IPC).
type resultParams struct {
	ID     string         `json:"id"`
	Result map[string]any `json:"result"`
	Notes  string         `json:"notes,omitempty"`
}

const resultUsage = `Submits a work session's result (Docs/protocol/work-session.md), the
worker side of a request. Worker only.

Usage:
  agentnet result <id> [--status pass|fail|partial|n/a]
                  [--summary T] [--file F | --output-from-file F]
                  [--exit-code N] [--artifact SPEC]...
                  [--verification none|tests_passed] [--notes T] [--json]

<id> is a session id (s-...) or the request id it belongs to (r-...). On a
question that is still pending or deferred, it accepts the request, opens the
session and submits the answer in one step (Docs/protocol/consult.md).

Flags:
  --status S              pass, fail, partial or n/a. Required, except on a
                          pending question, where it defaults to n/a
  --summary T              one line, up to 280 characters
  --file F                 result output from file F (- = stdin), up to
                           32768 bytes. CRLF becomes LF, ANSI colour and
                           cursor sequences are removed. Same as
                           --output-from-file
  --output-from-file F     alias for --file
  --exit-code N            an integer from -2147483648 to 4294967295
  --artifact SPEC          repeatable, up to 20. Same SPEC as 'agentnet request'
  --verification V         none (default) or tests_passed: your own claim,
                           not proof
  --notes T                up to 2000 characters, sent to the requester
  --json                   print machine-readable JSON on stdout

Exit codes: 0 done, 1 error (bad_state, not_worker, result_too_large, ...),
2 usage, 3 daemon not running.
`

func runResult(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet result", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	status := fs.String("status", "", "pass, fail, partial or n/a")
	summary := fs.String("summary", "", "one line, up to 280 characters")
	file := fs.String("file", "", "result output from file F (- = stdin)")
	outputFromFile := fs.String("output-from-file", "", "alias for --file")
	exitCode := fs.Int64("exit-code", 0, "an integer from -2147483648 to 4294967295")
	verification := fs.String("verification", "", "none (default) or tests_passed")
	notes := fs.String("notes", "", "up to 2000 characters, sent to the requester")
	var artifacts []string
	fs.Func("artifact", "an artifact pointer (repeatable, up to 20)", func(v string) error {
		artifacts = append(artifacts, v)
		return nil
	})
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, resultUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <id> (see 'agentnet result --help')")
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["file"] && set["output-from-file"] {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give at most one of --file or --output-from-file")
	}

	// status is left out when not given: the daemon defaults it to n/a when
	// the id is a pending question (a consult answer) and refuses a result
	// without status otherwise.
	result := map[string]any{}
	if *status != "" {
		result["status"] = *status
	}
	if set["summary"] {
		result["summary"] = *summary
	}
	if set["exit-code"] {
		result["exit_code"] = *exitCode
	}
	outFile := *file
	if set["output-from-file"] {
		outFile = *outputFromFile
	}
	if outFile != "" {
		out, err := readOutputFile(outFile)
		if err != nil {
			return failJSON(*asJSON, stdout, stderr, exitError, "bad_request", err.Error())
		}
		result["output"] = out
	}
	for _, spec := range artifacts {
		a, err := parseArtifactFlag(spec)
		if err != nil {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
		}
		result["artifacts"] = append(asArtifactSlice(result["artifacts"]), a)
	}
	verif := *verification
	if verif == "" {
		verif = "none"
	}
	result["verification"] = verif

	params := resultParams{ID: pos[0], Result: result}
	if *notes != "" {
		params.Notes = *notes
	}
	var res daemon.SessionResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "ws_result", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(sessionActionBody{OK: true, SessionResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Submitted result for %s (%s)\n", res.Session.ID, res.Session.State)
	return exitOK
}

func asArtifactSlice(v any) []any {
	if v == nil {
		return nil
	}
	s, _ := v.([]any)
	return s
}

const acceptResultUsage = `Accepts a work session's result, closing the session on both sides
(Docs/protocol/work-session.md §Accept-result). Requester only.

Usage:
  agentnet accept-result <id> [--human] [--json]

Flags:
  --human   require a human approval first; the stored verification becomes
            human_accepted. Prints the approval id; answer it in the approval window
            ('agentnet approve --open <id>' shows it again)
  --json    print machine-readable JSON on stdout

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
`

func runAcceptResult(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet accept-result", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	human := fs.Bool("human", false, "require a human approval first")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, acceptResultUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <id> (see 'agentnet accept-result --help')")
	}
	params := map[string]any{"id": pos[0]}
	if *human {
		params["human"] = true
	}
	var res map[string]json.RawMessage
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "ws_accept_result", params, &res); code != exitOK {
		return code
	}
	if raw, ok := res["approval"]; ok {
		var view approval.View
		_ = json.Unmarshal(raw, &view)
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(map[string]any{"ok": true, "approval": view})
			return exitOK
		}
		_, _ = fmt.Fprintf(stdout, "Approval %s pending; answer it in the approval window ('agentnet approve --open %s' shows it again).\n", view.ID, view.ID)
		return exitOK
	}
	var sessionRaw daemon.SessionResult
	if raw, ok := res["session"]; ok {
		_ = json.Unmarshal(raw, &sessionRaw.Session)
	}
	if raw, ok := res["mail_id"]; ok {
		_ = json.Unmarshal(raw, &sessionRaw.MailID)
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(sessionActionBody{OK: true, SessionResult: sessionRaw})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Accepted result for %s (now %s)\n", sessionRaw.Session.ID, sessionRaw.Session.State)
	return exitOK
}

// waitBody is the machine-readable output of `wait --json`.
type waitBody struct {
	OK      bool                `json:"ok"`
	Wait    string              `json:"wait"`
	Session *daemon.SessionView `json:"session,omitempty"`
}

const waitUsage = `Waits for a work session (or, before it exists, its request) to change,
polling once a second (Docs/protocol/work-session.md §CLI, "wait").

Usage:
  agentnet wait <id> [--timeout SECONDS] [--json]

<id> is a session id (s-...) or the request id it belongs to (r-...).

Flags:
  --timeout SECONDS   default 300, max 3600
  --json               print machine-readable JSON on stdout ({"ok", "wait",
                       "session"}); the result is included in full when
                       visible

Exit codes: 0 the wait resolved (result, closed, changed, declined or
cancelled), 1 error, 2 usage, 3 daemon not running, 4 timeout.
`

const exitWaitTimeout = 4

func runWait(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet wait", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	timeoutSec := fs.Int("timeout", 300, "seconds, default 300, max 3600")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, waitUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <id> (see 'agentnet wait --help')")
	}
	id := pos[0]
	timeout := time.Duration(*timeoutSec) * time.Second
	if *timeoutSec <= 0 {
		timeout = 300 * time.Second
	}
	if timeout > 3600*time.Second {
		timeout = 3600 * time.Second
	}
	deadline := time.Now().Add(timeout)

	p, err := paths.Default()
	if err != nil {
		return failJSON(*asJSON, stdout, stderr, exitError, "daemon_error", err.Error())
	}

	// A debate uses a different wait rule (Docs/protocol/debate.md §CLI,
	// "wait"): "turn" when it becomes the caller's turn, "closed" at the end.
	// debate_show on a non-debate id (or one this daemon has no debate row
	// for) answers unknown_session, and the loop below falls through to the
	// ordinary session/request wait.
	var firstShown daemon.DebateShowResult
	derr := waitCall(p, "debate_show", map[string]any{"id": id}, &firstShown)
	if derr == nil {
		return runDebateWait(*asJSON, stdout, stderr, p, id, deadline)
	}
	var ie *ipc.Error
	if errors.As(derr, &ie) && ie.Code != "unknown_session" {
		return failJSON(*asJSON, stdout, stderr, exitError, ie.Code, ie.Message)
	}

	var startState string
	haveStart := false
	for {
		var shown daemon.SessionShowResult
		serr := waitCall(p, "ws_show", map[string]any{"id": id}, &shown)
		if serr == nil {
			sv := shown.Session
			if !haveStart {
				startState = sv.State
				haveStart = true
			}
			var reason string
			switch sv.Role {
			case "requester":
				switch sv.State {
				case "awaiting_result":
					reason = "result"
				case "closed":
					reason = "closed"
				}
			case "worker":
				if sv.State != startState {
					if sv.State == "closed" {
						reason = "closed"
					} else {
						reason = "changed"
					}
				}
			}
			if reason != "" {
				return printWaitResult(*asJSON, stdout, reason, &sv)
			}
		} else {
			var ie *ipc.Error
			if errors.As(serr, &ie) && ie.Code == "unknown_session" {
				var rshown daemon.RequestShowResult
				if rerr := waitCall(p, "request_show", map[string]any{"id": id}, &rshown); rerr == nil {
					switch rshown.Request.State {
					case "declined":
						return printWaitResult(*asJSON, stdout, "declined", nil)
					case "cancelled":
						return printWaitResult(*asJSON, stdout, "cancelled", nil)
					}
				}
			} else if errors.As(serr, &ie) {
				return failJSON(*asJSON, stdout, stderr, exitError, ie.Code, ie.Message)
			} else if errors.Is(serr, ipc.ErrNotRunning) {
				return failJSON(*asJSON, stdout, stderr, exitDaemonNotFound, "daemon_not_running", "agentnetd is not running")
			}
		}
		if time.Now().After(deadline) {
			var sv *daemon.SessionView
			var last daemon.SessionShowResult
			if lerr := waitCall(p, "ws_show", map[string]any{"id": id}, &last); lerr == nil {
				sv = &last.Session
			}
			return printWaitResult(*asJSON, stdout, "timeout", sv)
		}
		time.Sleep(waitPollInterval)
	}
}

// waitPollInterval is overridden in tests to avoid a real 1 s sleep.
var waitPollInterval = time.Second

// waitCall is a low-level IPC call for the wait loop: it returns the raw
// error (an *ipc.Error, ipc.ErrNotRunning, or another error) instead of
// writing to stdout/stderr, so the loop can decide what to do next.
func waitCall(p paths.Paths, method string, params, res any) error {
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	return ipc.Call(ctx, p.Endpoint, method, params, res)
}

func printWaitResult(asJSON bool, stdout io.Writer, reason string, sv *daemon.SessionView) int {
	code := exitOK
	if reason == "timeout" {
		code = exitWaitTimeout
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(waitBody{OK: true, Wait: reason, Session: sv})
		return code
	}
	if sv != nil {
		_, _ = fmt.Fprintf(stdout, "%s: %s (%s)\n", reason, sv.ID, sv.State)
	} else {
		_, _ = fmt.Fprintf(stdout, "%s\n", reason)
	}
	return code
}

// debateWaitBody is the machine-readable output of `wait --json` on a debate.
type debateWaitBody struct {
	OK     bool               `json:"ok"`
	Wait   string             `json:"wait"`
	Debate *daemon.DebateView `json:"debate,omitempty"`
}

// runDebateWait polls debate_show (Docs/protocol/debate.md §CLI, "wait"):
// "turn" once it is the caller's turn, "closed" once the debate closed (or
// broke), "timeout" at deadline.
func runDebateWait(asJSON bool, stdout, stderr io.Writer, p paths.Paths, id string, deadline time.Time) int {
	for {
		var shown daemon.DebateShowResult
		derr := waitCall(p, "debate_show", map[string]any{"id": id}, &shown)
		if derr == nil {
			d := shown.Debate
			switch {
			case d.Phase == "closed" || d.Phase == "broken":
				return printDebateWaitResult(asJSON, stdout, "closed", &d)
			case d.Turn == "you":
				return printDebateWaitResult(asJSON, stdout, "turn", &d)
			}
		} else {
			var ie *ipc.Error
			if errors.As(derr, &ie) {
				return failJSON(asJSON, stdout, stderr, exitError, ie.Code, ie.Message)
			}
			if errors.Is(derr, ipc.ErrNotRunning) {
				return failJSON(asJSON, stdout, stderr, exitDaemonNotFound, "daemon_not_running", "agentnetd is not running")
			}
		}
		if time.Now().After(deadline) {
			var sv *daemon.DebateView
			var last daemon.DebateShowResult
			if lerr := waitCall(p, "debate_show", map[string]any{"id": id}, &last); lerr == nil {
				sv = &last.Debate
			}
			return printDebateWaitResult(asJSON, stdout, "timeout", sv)
		}
		time.Sleep(waitPollInterval)
	}
}

func printDebateWaitResult(asJSON bool, stdout io.Writer, reason string, dv *daemon.DebateView) int {
	code := exitOK
	if reason == "timeout" {
		code = exitWaitTimeout
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(debateWaitBody{OK: true, Wait: reason, Debate: dv})
		return code
	}
	if dv != nil {
		_, _ = fmt.Fprintf(stdout, "%s: %s (%s)\n", reason, dv.Session, dv.Phase)
	} else {
		_, _ = fmt.Fprintf(stdout, "%s\n", reason)
	}
	return code
}
