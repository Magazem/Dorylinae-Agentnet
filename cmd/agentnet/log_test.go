package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// startLogDaemon runs a daemon on p until the returned stop is called.
func startLogDaemon(t *testing.T, p paths.Paths) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, p, ready) }()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("daemon exited: %v", err)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("daemon not ready")
	}
	var once bool
	stop = func() {
		if !once {
			once = true
			cancel()
			<-done
		}
	}
	t.Cleanup(stop)
	return stop
}

// runLogCmd runs `agentnet log args...` and returns exit code, stdout, stderr.
func runLogCmd(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(append([]string{"log"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

// sideStore opens a second handle on the daemon's database (a second process
// appends the same way, audit.md §Appending).
func sideStore(t *testing.T, p paths.Paths) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), p.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func decodeEvents(t *testing.T, out string) []audit.Entry {
	t.Helper()
	var body struct {
		OK     bool          `json:"ok"`
		Events []audit.Entry `json:"events"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil || !body.OK {
		t.Fatalf("not an events body: %q: %v", out, err)
	}
	return body.Events
}

func TestLogListVerifyHeadAnchor(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	p := shortHome(t)
	startLogDaemon(t, p)
	side := sideStore(t, p)
	al := audit.New(side.DB())
	ctx := context.Background()

	// since 24h --json: the daemon's own start rows are there, chain_start included.
	code, out, errs := runLogCmd("--since", "24h", "--json")
	if code != exitOK {
		t.Fatalf("log --since 24h: %d %s", code, errs)
	}
	evs := decodeEvents(t, out)
	if len(evs) < 2 || evs[0].Action != audit.ActionChainStart || evs[1].Action != audit.ActionDaemonStart {
		t.Fatalf("events = %+v", evs)
	}
	if code, out, _ := runLogCmd("--since", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "--json"); code != exitOK || len(decodeEvents(t, out)) != 0 {
		t.Fatalf("a future --since must be empty: %d %s", code, out)
	}
	if code, out, _ := runLogCmd("--action", "daemon.", "--json"); code != exitOK || len(decodeEvents(t, out)) != 1 {
		t.Fatalf("--action daemon.: %d %s", code, out)
	}

	// Human output: one line per row, an ESC in a detail value never reaches the terminal.
	if err := al.Append(ctx, audit.ActorCLI, "x.esc", map[string]string{"name": "a\x1b[31mred\x1b]0;title\x07", "n": "1"}); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runLogCmd("--action", "x.")
	if code != exitOK || !strings.Contains(out, " cli x.esc n=") || strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("human output: %d %q", code, out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("want one line, got %q", out)
	}

	// --head, --verify, --anchor.
	code, out, _ = runLogCmd("--head", "--json")
	var hb struct {
		OK   bool        `json:"ok"`
		Head *audit.Head `json:"head"`
	}
	if err := json.Unmarshal([]byte(out), &hb); code != exitOK || err != nil || !hb.OK || hb.Head == nil || hb.Head.Hash == "" {
		t.Fatalf("--head: %d %q %v", code, out, err)
	}
	anchor := fmt.Sprintf("%d:%s", hb.Head.ID, hb.Head.Hash)
	if code, out, _ := runLogCmd("--head"); code != exitOK || !strings.HasPrefix(out, anchor[:strings.IndexByte(anchor, ':')]+" "+hb.Head.Hash) {
		t.Fatalf("human --head: %d %q", code, out)
	}
	if code, out, errs := runLogCmd("--verify"); code != exitOK || !strings.Contains(out, "intact") {
		t.Fatalf("--verify clean: %d %q %q", code, out, errs)
	}
	if code, out, errs := runLogCmd("--verify", "--anchor", anchor, "--json"); code != exitOK {
		t.Fatalf("--verify --anchor: %d %q %q", code, out, errs)
	}
	// An anchor on a row that is not what was recorded is tampering: exit 5.
	bad := fmt.Sprintf("%d:%s", hb.Head.ID, strings.Repeat("0", 64))
	if code, _, errs := runLogCmd("--verify", "--anchor", bad); code != exitAuditBroken || !strings.Contains(errs, "row "+fmt.Sprint(hb.Head.ID)) || !strings.Contains(errs, "anchor_mismatch") {
		t.Fatalf("bad anchor: %d %q", code, errs)
	}
	// A malformed anchor is a usage error, not tampering.
	if code, _, _ := runLogCmd("--verify", "--anchor", "nonsense"); code != exitUsage {
		t.Fatalf("malformed anchor: %d", code)
	}
	// Flag misuse.
	for _, args := range [][]string{{"--verify", "--head"}, {"--verify", "--session", "s-1"}, {"--anchor", "1:" + strings.Repeat("a", 64)}, {"--since", "soon"}, {"extra"}} {
		if code, _, _ := runLogCmd(args...); code != exitUsage {
			t.Errorf("log %v = %d, want usage", args, code)
		}
	}

	// Tamper with one row (a second handle with the triggers dropped): exit 5 naming it.
	for _, q := range []string{`DROP TRIGGER audit_events_no_update`, `UPDATE audit_events SET detail = '{"pid":1}' WHERE id = 2`} {
		if _, err := side.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errs = runLogCmd("--verify", "--json")
	var vb struct {
		OK     bool                `json:"ok"`
		Verify *audit.VerifyResult `json:"verify"`
	}
	if err := json.Unmarshal([]byte(out), &vb); code != exitAuditBroken || err != nil || vb.Verify == nil ||
		vb.Verify.Status != audit.StatusBroken || vb.Verify.FirstBad != 2 || vb.Verify.Reason != audit.ReasonHashMismatch {
		t.Fatalf("tampered --verify: %d %q %q %v", code, out, errs, err)
	}
	if code, _, errs := runLogCmd("--verify"); code != exitAuditBroken || !strings.Contains(errs, "BROKEN at row 2: hash_mismatch") {
		t.Fatalf("tampered --verify human: %d %q", code, errs)
	}
}

// Paging with after_id over 2500 rows: the CLI returns every row once.
func TestLogPagesOver2500Rows(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	p := shortHome(t)
	startLogDaemon(t, p)
	al := audit.New(sideStore(t, p).DB())
	for i := 0; i < 2500; i++ {
		if err := al.Append(context.Background(), audit.ActorCLI, "bulk.row", map[string]int{"n": i}); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errs := runLogCmd("--action", "bulk.", "--json")
	if code != exitOK {
		t.Fatalf("%d %s", code, errs)
	}
	evs := decodeEvents(t, out)
	if len(evs) != 2500 {
		t.Fatalf("got %d rows, want 2500", len(evs))
	}
	for i := 1; i < len(evs); i++ {
		// The daemon appends its own rows in between, so ids only increase.
		if evs[i].ID <= evs[i-1].ID {
			t.Fatalf("rows %d and %d are out of order or repeated: ids %d, %d", i-1, i, evs[i-1].ID, evs[i].ID)
		}
	}
	if code, out, _ := runLogCmd("--action", "bulk.", "--limit", "1200", "--json"); code != exitOK || len(decodeEvents(t, out)) != 1200 {
		t.Fatalf("--limit 1200: %d, %d rows", code, len(decodeEvents(t, out)))
	}
}

// Review 44 L2: with an unchained head the daemon cannot start, and `log`
// still verifies, lists and prints the head by reading the database directly.
func TestLogReadsDirectlyWhenTheDaemonWillNotStart(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	p := shortHome(t)

	// No daemon and no database: exit 3.
	if code, _, errs := runLogCmd("--verify"); code != exitDaemonNotFound || !strings.Contains(errs, "not running") {
		t.Fatalf("nothing there: %d %q", code, errs)
	}

	stop := startLogDaemon(t, p)
	stop()
	side := sideStore(t, p)
	// A clean log, daemon down: verified directly, read-only.
	code, out, errs := runLogCmd("--verify")
	if code != exitOK || !strings.Contains(out, "intact") || !strings.Contains(errs, "reading the database directly") {
		t.Fatalf("direct verify: %d %q %q", code, out, errs)
	}
	// An unchained row appended by code that bypassed internal/audit.
	for _, q := range []string{
		`DROP TRIGGER audit_events_chained`,
		`INSERT INTO audit_events (id, ts, actor, action, detail) VALUES ((SELECT MAX(id) + 1 FROM audit_events), '2026-10-01T09:00:00Z', 'x', 'bypass', '{}')`,
	} {
		if _, err := side.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var bad int64
	if err := side.DB().QueryRow(`SELECT MAX(id) FROM audit_events`).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	_ = side.Close()

	// The daemon no longer starts.
	dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dcancel()
	if err := daemon.Run(dctx, p, nil); err == nil || !strings.Contains(err.Error(), "agentnet log --verify") {
		t.Fatalf("daemon.Run over an unchained head: %v", err)
	}

	code, _, errs = runLogCmd("--verify")
	if code != exitAuditBroken || !strings.Contains(errs, fmt.Sprintf("BROKEN at row %d: unchained", bad)) {
		t.Fatalf("direct verify of a broken log: %d %q", code, errs)
	}
	code, out, _ = runLogCmd("--json")
	if evs := decodeEvents(t, out); code != exitOK || evs[len(evs)-1].Action != "bypass" {
		t.Fatalf("direct list: %d %q", code, out)
	}
	if code, out, _ := runLogCmd("--head", "--json"); code != exitOK || !strings.Contains(out, fmt.Sprintf(`"id":%d`, bad)) {
		t.Fatalf("direct head: %d %q", code, out)
	}
	// The direct handle is read-only: it wrote nothing.
	if code, out, _ := runLogCmd("--action", "daemon.", "--json"); code != exitOK || len(decodeEvents(t, out)) != 2 {
		t.Fatalf("daemon rows after the failed start: %d %q", code, out)
	}
}

func TestLogParseSince(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"24h":                  now.Add(-24 * time.Hour),
		"90m":                  now.Add(-90 * time.Minute),
		"7d":                   now.Add(-7 * 24 * time.Hour),
		"2026-09-30T00:00:00Z": time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
	} {
		got, err := parseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "soon", "-1h", "xd"} {
		if _, err := parseSince(in, now); err == nil {
			t.Errorf("parseSince(%q) succeeded", in)
		}
	}
}

func TestLogFormatEventStripsEscapes(t *testing.T) {
	e := audit.Entry{ID: 1, TS: "2026-10-01T09:00:00Z", Actor: "da\x1bemon", Action: "x.y",
		Detail: json.RawMessage(`{"b":"\u001b[2Jhi","a":3,"c":{"k":"\u009b"}}`)}
	line := formatEvent(e)
	if strings.ContainsAny(line, "\x1b\u009b") || !strings.Contains(line, "a=3 b=") || !strings.Contains(line, "x.y") {
		t.Fatalf("line = %q", line)
	}
}
