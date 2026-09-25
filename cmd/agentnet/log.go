package main

// Ticket 3.6b: `agentnet log` (Docs/cli/log.md, Docs/protocol/audit.md §agentnet log).

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// exitAuditBroken is `log --verify` finding a broken chain (Docs/cli/log.md).
const exitAuditBroken = 5

const (
	logListTimeout   = 15 * time.Second
	logVerifyTimeout = 120 * time.Second
	logPage          = 1000
)

// anchorFlags collects the repeatable --anchor ID:HASH.
type anchorFlags []string

func (a *anchorFlags) String() string { return strings.Join(*a, ",") }
func (a *anchorFlags) Set(v string) error {
	*a = append(*a, v)
	return nil
}

type logEventsBody struct {
	OK     bool          `json:"ok"`
	Events []audit.Entry `json:"events"`
}

type logVerifyBody struct {
	OK     bool                `json:"ok"`
	Verify *audit.VerifyResult `json:"verify"`
}

type logHeadBody struct {
	OK   bool        `json:"ok"`
	Head *audit.Head `json:"head"`
}

const logUsage = `Shows the audit log and checks its hash chain.

Usage:
  agentnet log [--since DURATION|TIME] [--until TIME] [--session ID] [--action PREFIX]
               [--limit N] [--json]
  agentnet log --verify [--anchor ID:HASH]... [--timeout SECONDS] [--json]
  agentnet log --head [--json]

Flags:
  --since D|T     only rows at or after this time: a duration back from now
                  (24h, 90m, 7d) or an RFC 3339 time
  --until T       only rows at or before this RFC 3339 time
  --session ID    only the rows of one work session (s-...) or request (r-...):
                  its request, grants, approvals, decision and ws.* rows
  --action PFX    only actions starting with PFX (for example "grant.")
  --limit N       print at most N rows (default: all; the CLI pages through the daemon)
  --verify        check the whole hash chain; exit 5 if it is broken
  --anchor I:H    with --verify: the row with id I must still have hash H (repeatable)
  --head          print the newest row's id, hash and time, to keep as an anchor
  --timeout S     with --verify: seconds to wait (default 120)
  --json          print machine-readable JSON on stdout:
                  {"ok":true,"events":[{"id","ts","actor","action","detail","hash"}]},
                  {"ok":true,"verify":{"status","rows","legacy_rows","chained_from",
                  "head","first_bad","reason"}} or {"ok":true,"head":{"id","hash","ts"}}

The filters are views; --verify always checks the whole chain. When agentnetd is
not running the command reads the database directly, read-only, so a broken
log can still be diagnosed.

Exit codes: 0 ok, 1 error, 2 usage, 3 database not found and daemon not running,
5 the chain is broken (--verify).
`

func runLog(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet log", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	since := fs.String("since", "", "only rows at or after this duration back or time")
	until := fs.String("until", "", "only rows at or before this time")
	session := fs.String("session", "", "only the rows of this session or request")
	action := fs.String("action", "", "only actions with this prefix")
	limit := fs.Int("limit", 0, "print at most N rows")
	verify := fs.Bool("verify", false, "check the hash chain")
	head := fs.Bool("head", false, "print the newest row")
	timeout := fs.Int("timeout", int(logVerifyTimeout/time.Second), "with --verify: seconds to wait")
	var anchors anchorFlags
	fs.Var(&anchors, "anchor", "with --verify: ID:HASH that must still be in the chain")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, logUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	usage := func(msg string) int { return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", msg) }
	filtered := *since != "" || *until != "" || *session != "" || *action != "" || *limit != 0
	switch {
	case len(pos) > 0:
		return usage(fmt.Sprintf("unexpected argument %q", pos[0]))
	case *verify && *head:
		return usage("--verify and --head cannot be combined")
	case (*verify || *head) && filtered:
		return usage("filters apply to the list only; --verify and --head always look at the whole chain")
	case len(anchors) > 0 && !*verify:
		return usage("--anchor needs --verify")
	case *limit < 0:
		return usage("--limit must not be negative")
	case *timeout <= 0:
		return usage("--timeout must be positive")
	}

	params := audit.ListParams{Session: *session, Action: *action}
	if *since != "" {
		t, err := parseSince(*since, time.Now())
		if err != nil {
			return usage(err.Error())
		}
		params.Since = t.UTC().Format(time.RFC3339Nano)
	}
	if *until != "" {
		t, err := time.Parse(time.RFC3339Nano, *until)
		if err != nil {
			return usage(fmt.Sprintf("--until %q is not an RFC 3339 time", *until))
		}
		params.Until = t.UTC().Format(time.RFC3339Nano)
	}

	p, err := paths.Default()
	if err != nil {
		return failJSON(*asJSON, stdout, stderr, exitError, "daemon_error", err.Error())
	}
	wait := logListTimeout
	if *verify {
		// audit_verify is exempt from the 2 s rule (audit.md §Verification).
		wait = time.Duration(*timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	src, err := openLogSource(ctx, p, stderr)
	if err != nil {
		return logFail(*asJSON, stdout, stderr, p, err)
	}
	defer src.close()

	switch {
	case *head:
		h, err := src.head(ctx)
		if err != nil {
			return logFail(*asJSON, stdout, stderr, p, err)
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(logHeadBody{OK: true, Head: h})
		} else if h == nil {
			_, _ = fmt.Fprintln(stdout, "The audit log is empty.")
		} else {
			_, _ = fmt.Fprintf(stdout, "%d %s %s\n", h.ID, h.Hash, h.TS)
		}
		return exitOK
	case *verify:
		res, err := src.verify(ctx, anchors)
		if err != nil {
			return logFail(*asJSON, stdout, stderr, p, err)
		}
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(logVerifyBody{OK: true, Verify: res.Verify})
		}
		return printVerify(*asJSON, stdout, stderr, res.Verify)
	}

	var events []audit.Entry
	for {
		page := params
		page.Limit = logPage
		if *limit > 0 && *limit-len(events) < logPage {
			page.Limit = *limit - len(events)
		}
		res, err := src.list(ctx, page)
		if err != nil {
			return logFail(*asJSON, stdout, stderr, p, err)
		}
		events = append(events, res.Events...)
		if res.NextAfterID == 0 || (*limit > 0 && len(events) >= *limit) {
			break
		}
		params.AfterID = res.NextAfterID
	}
	if *asJSON {
		if events == nil {
			events = []audit.Entry{}
		}
		_ = json.NewEncoder(stdout).Encode(logEventsBody{OK: true, Events: events})
		return exitOK
	}
	for _, e := range events {
		_, _ = fmt.Fprintln(stdout, formatEvent(e))
	}
	return exitOK
}

// parseSince accepts a Go duration (or Nd days) back from now, or an RFC 3339 time.
func parseSince(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil && strings.HasSuffix(s, "d") {
		var days float64
		if days, err = strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64); err == nil {
			d = time.Duration(days * float64(24*time.Hour))
		}
	}
	if err != nil || d < 0 {
		return time.Time{}, fmt.Errorf("--since %q is not a duration (24h, 90m, 7d) or an RFC 3339 time", s)
	}
	return now.Add(-d), nil
}

func printVerify(asJSON bool, stdout, stderr io.Writer, v *audit.VerifyResult) int {
	broken := v.Status == audit.StatusBroken
	if !asJSON {
		if broken {
			_, _ = fmt.Fprintf(stderr, "agentnet: the audit log is BROKEN at row %d: %s\n", v.FirstBad, v.Reason)
		} else {
			_, _ = fmt.Fprintf(stdout, "The audit log is intact: %d rows checked", v.Rows)
			if v.LegacyRows > 0 {
				_, _ = fmt.Fprintf(stdout, ", %d of them written before the chain existed (their history before then cannot be checked)", v.LegacyRows)
			}
			_, _ = fmt.Fprintln(stdout, ".")
		}
		if v.Head != nil {
			_, _ = fmt.Fprintf(stdout, "head %d %s (keep it as an anchor: --anchor %d:%s)\n", v.Head.ID, v.Head.Hash, v.Head.ID, v.Head.Hash)
		}
	}
	if broken {
		return exitAuditBroken
	}
	return exitOK
}

// formatEvent is one line: "ts actor action key=value ...", every part through
// the control-character cleaner (detail is content-free, but a key or name from
// a peer must not inject terminal escapes).
func formatEvent(e audit.Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s", notify.Clean(e.TS, 0), notify.Clean(e.Actor, 0), notify.Clean(e.Action, 0))
	var d map[string]json.RawMessage
	if err := json.Unmarshal(e.Detail, &d); err != nil {
		if s := notify.Clean(string(e.Detail), 0); s != "" {
			b.WriteString(" detail=" + s)
		}
		return b.String()
	}
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := d[k]
		if compact, err := compactJSON(v); err == nil {
			v = compact
		}
		b.WriteString(" " + notify.Clean(k, 0) + "=" + notify.Clean(string(v), 0))
	}
	return b.String()
}

func compactJSON(v json.RawMessage) (json.RawMessage, error) {
	var x any
	if err := json.Unmarshal(v, &x); err != nil {
		return nil, err
	}
	return json.Marshal(x)
}

// logSource reads the audit log through the daemon, or directly when it is not running.
type logSource interface {
	list(ctx context.Context, p audit.ListParams) (*audit.ListResult, error)
	verify(ctx context.Context, anchors []string) (*daemon.AuditVerifyResult, error)
	head(ctx context.Context) (*audit.Head, error)
	close()
}

// errNoLog means neither a daemon nor a database file was found.
var errNoLog = errors.New("no daemon and no database")

func openLogSource(ctx context.Context, p paths.Paths, stderr io.Writer) (logSource, error) {
	conn, err := ipc.Dial(ctx, p.Endpoint)
	if err == nil {
		_ = conn.Close()
		return ipcSource{endpoint: p.Endpoint}, nil
	}
	if !errors.Is(err, ipc.ErrNotRunning) {
		return nil, err
	}
	// The daemon is down (or would not start, for example after an unchained
	// row): read the file directly, read-only.
	db, derr := store.OpenReadOnly(p.DB)
	if derr != nil {
		if errors.Is(derr, os.ErrNotExist) {
			return nil, errNoLog
		}
		return nil, derr
	}
	_, _ = fmt.Fprintln(stderr, "agentnet: agentnetd is not running; reading the database directly (read-only)")
	return dbSource{db: db, log: audit.New(db)}, nil
}

type ipcSource struct{ endpoint string }

func (s ipcSource) list(ctx context.Context, p audit.ListParams) (*audit.ListResult, error) {
	var res audit.ListResult
	if err := ipc.Call(ctx, s.endpoint, "audit_list", p, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (s ipcSource) verify(ctx context.Context, anchors []string) (*daemon.AuditVerifyResult, error) {
	var res daemon.AuditVerifyResult
	if err := ipc.Call(ctx, s.endpoint, "audit_verify", daemon.AuditVerifyParams{Anchors: anchors}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (s ipcSource) head(ctx context.Context) (*audit.Head, error) {
	var res daemon.AuditHeadResult
	if err := ipc.Call(ctx, s.endpoint, "audit_head", nil, &res); err != nil {
		return nil, err
	}
	return res.Head, nil
}

func (ipcSource) close() {}

type dbSource struct {
	db  interface{ Close() error }
	log *audit.Log
}

func (s dbSource) list(ctx context.Context, p audit.ListParams) (*audit.ListResult, error) {
	return s.log.Query(ctx, p)
}

func (s dbSource) verify(ctx context.Context, anchors []string) (*daemon.AuditVerifyResult, error) {
	return daemon.AuditVerify(ctx, s.log, anchors)
}

func (s dbSource) head(ctx context.Context) (*audit.Head, error) { return s.log.Head(ctx) }

func (s dbSource) close() { _ = s.db.Close() }

// logFail maps an error of either source to the CLI's error output and exit code.
func logFail(asJSON bool, stdout, stderr io.Writer, p paths.Paths, err error) int {
	var ie *ipc.Error
	switch {
	case errors.Is(err, errNoLog), errors.Is(err, ipc.ErrNotRunning):
		return failJSON(asJSON, stdout, stderr, exitDaemonNotFound, "daemon_not_running",
			fmt.Sprintf("agentnetd is not running and there is no database at %s", p.DB))
	case errors.As(err, &ie):
		code := exitError
		if ie.Code == ipc.CodeBadRequest {
			code = exitUsage
		}
		return failJSON(asJSON, stdout, stderr, code, ie.Code, ie.Message)
	case errors.Is(err, audit.ErrBadParams), errors.Is(err, audit.ErrBadAnchor):
		return failJSON(asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return failJSON(asJSON, stdout, stderr, exitError, "timeout", "the log did not answer in time (raise --timeout for --verify)")
	default:
		return failJSON(asJSON, stdout, stderr, exitError, "daemon_error", err.Error())
	}
}
