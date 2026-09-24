package main

// Ticket 2.5 acceptance, CLI side (Docs/protocol/consult.md, Docs/cli/consult.md):
// the consult round trip through two real daemons and a relay, driven only
// through the CLI, plus the CLI's own file rules.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// consultTeam pairs two new nodes through a team invite and waits until both
// see the two-member roster.
func consultTeam(t *testing.T) (a, b *testNode) {
	t.Helper()
	srv := relay.New(relay.Options{})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http")
	a = startNode(t, "alice", url)
	b = startNode(t, "bob", url)
	a.waitRelay(t)
	b.waitRelay(t)

	code, out, errs := cli(t, a, "team", "create", "backend", "--json")
	if code != exitOK {
		t.Fatalf("team create: %d %s %s", code, out, errs)
	}
	var created teamOut
	if err := json.Unmarshal([]byte(out), &created); err != nil || !created.OK {
		t.Fatalf("team create --json: %q: %v", out, err)
	}
	code, out, errs = cli(t, a, "team", "invite", created.Team.ID, "--json")
	if code != exitOK {
		t.Fatalf("team invite: %d %s %s", code, out, errs)
	}
	norm, ok := envelope.NormalizePairCodeV2(decodeTeamInvite(t, out).Code)
	if !ok {
		t.Fatalf("no v2 invite code in %q", out)
	}
	code, out, errs = cli(t, b, "team", "join", norm, "--json")
	if code != exitOK {
		t.Fatalf("team join: %d %s %s", code, out, errs)
	}
	var joined pairOut
	_ = json.Unmarshal([]byte(out), &joined)
	deadline := time.Now().Add(10 * time.Second)
	for joined.State == "pending" {
		if time.Now().After(deadline) {
			t.Fatalf("join never finished: %+v", joined)
		}
		time.Sleep(20 * time.Millisecond)
		_, out, _ = cli(t, b, "pair", "--status", joined.PairingID, "--json")
		_ = json.Unmarshal([]byte(out), &joined)
	}
	for _, n := range []*testNode{a, b} {
		for {
			_, out, _ = cli(t, n, "team", "show", created.Team.ID, "--json")
			var show teamShowOut
			if err := json.Unmarshal([]byte(out), &show); err == nil && show.OK && len(show.Team.Members) == 2 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("a node never saw the roster: %q", out)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	return a, b
}

// pollCLI runs the CLI against n until ok accepts the JSON output.
func pollCLI(t *testing.T, n *testNode, what string, ok func(out string) bool, args ...string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, out, _ := cli(t, n, args...)
		if ok(out) {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; last output %q", what, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func auditText(t *testing.T, n *testNode) string {
	t.Helper()
	st, err := store.Open(context.Background(), n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var all string
	if err := st.DB().QueryRow(`SELECT COALESCE(group_concat(action || ' ' || detail, char(10)), '') FROM audit_events`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	return all
}

// TestConsultRoundTrip is the plan's 2.5 acceptance (in-process part): agent A
// consults B with one context file, B answers with `result --file`, and
// `wait <session> --timeout 300` on A returns the answer; A accepts it and both
// sides show the session closed and the request completed. No audit row holds
// context or answer text.
func TestConsultRoundTrip(t *testing.T) {
	oldInterval := waitPollInterval
	waitPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = oldInterval })
	a, b := consultTeam(t)

	const contextMarker = "CONTEXT-MARKER-7f3a"
	const answerMarker = "ANSWER-MARKER-91bc"
	dir := t.TempDir()
	ctxPath := filepath.Join(dir, "outbox.go")
	if err := os.WriteFile(ctxPath, []byte("package mail\r\n// "+contextMarker+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	answerPath := filepath.Join(dir, "answer.md")
	if err := os.WriteFile(answerPath, []byte("Yes: "+answerMarker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errs := cli(t, a, "consult", "@bob", "--question", "Is the retry backoff safe under clock skew?\nMore text.",
		"--context-file", ctxPath, "--json")
	if code != exitOK {
		t.Fatalf("consult: %d %s %s", code, out, errs)
	}
	var sub requestBody
	if err := json.Unmarshal([]byte(out), &sub); err != nil || !sub.OK || sub.Status != "queued" || sub.ID == "" {
		t.Fatalf("consult --json = %q: %v", out, err)
	}
	if !strings.HasPrefix(sub.Session, "s-") || len(sub.Session) != 34 {
		t.Fatalf("consult --json session = %q, want a derived s- id", sub.Session)
	}

	// B's agent sees the question in the inbox (sizes only) and its context in show.
	pollCLI(t, b, "the question in B's inbox", func(o string) bool { return strings.Contains(o, sub.ID) }, "inbox", "--json")
	_, out, _ = cli(t, b, "inbox", "--json")
	var inbox inboxBody
	if err := json.Unmarshal([]byte(out), &inbox); err != nil || len(inbox.Requests) != 1 {
		t.Fatalf("inbox --json = %q: %v", out, err)
	}
	q := inbox.Requests[0]
	if q.Type != "question" || q.Title != "Is the retry backoff safe under clock skew?" || q.ContextFiles == nil || *q.ContextFiles != 1 || len(q.Context) != 0 {
		t.Fatalf("inbox view = %+v, want a question with context_files 1 and no context text", q)
	}
	_, out, _ = cli(t, b, "request", "show", sub.ID, "--json")
	var shown requestShowBody
	if err := json.Unmarshal([]byte(out), &shown); err != nil || len(shown.Request.Context) != 1 ||
		shown.Request.Context[0].Name != "outbox.go" || shown.Request.Context[0].Text != "package mail\n// "+contextMarker+"\n" {
		t.Fatalf("request show --json = %q: %v", out, err)
	}

	// B answers in one step.
	code, out, errs = cli(t, b, "result", sub.ID, "--file", answerPath, "--json")
	if code != exitOK {
		t.Fatalf("result: %d %s %s", code, out, errs)
	}
	var ans sessionActionBody
	if err := json.Unmarshal([]byte(out), &ans); err != nil || ans.Session.ID != sub.Session || ans.Session.State != "open" ||
		ans.Session.Result == nil || ans.Session.Result.Status != "n/a" {
		t.Fatalf("result --json = %q: %v (want session %s)", out, err, sub.Session)
	}

	// A waits on the derived session id and gets the answer.
	t.Setenv(paths.HomeEnv, a.p.Dir)
	var wout, werr bytes.Buffer
	if code := run([]string{"wait", sub.Session, "--timeout", "300", "--json"}, &wout, &werr); code != exitOK {
		t.Fatalf("wait: %d %s %s", code, wout.String(), werr.String())
	}
	var w waitBody
	if err := json.Unmarshal(wout.Bytes(), &w); err != nil || w.Wait != "result" || w.Session == nil ||
		w.Session.Result == nil || w.Session.Result.Output != "Yes: "+answerMarker+"\n" || w.Session.Result.Status != "n/a" {
		t.Fatalf("wait --json = %q: %v", wout.String(), err)
	}

	code, out, errs = cli(t, a, "accept-result", sub.Session, "--json")
	if code != exitOK {
		t.Fatalf("accept-result: %d %s %s", code, out, errs)
	}
	pollCLI(t, b, "B's session to close", func(o string) bool { return strings.Contains(o, `"state":"closed"`) }, "session", sub.Session, "--json")
	pollCLI(t, b, "B's request to complete", func(o string) bool { return strings.Contains(o, `"state":"completed"`) }, "request", "show", sub.ID, "--json")
	pollCLI(t, a, "A's request to complete", func(o string) bool { return strings.Contains(o, `"state":"completed"`) }, "request", "show", sub.ID, "--json")
	_, out, _ = cli(t, a, "session", sub.Session, "--json")
	if !strings.Contains(out, `"state":"closed"`) || !strings.Contains(out, `"outcome":"accepted"`) {
		t.Fatalf("A session after accept-result = %q", out)
	}

	// Context and answer text are never audited (sizes only).
	for name, n := range map[string]*testNode{"alice": a, "bob": b} {
		text := auditText(t, n)
		for _, secret := range []string{contextMarker, answerMarker, "Is the retry backoff"} {
			if strings.Contains(text, secret) {
				t.Errorf("%s audit holds %q", name, secret)
			}
		}
		if !strings.Contains(text, "context_files") {
			t.Errorf("%s audit has no context_files size", name)
		}
	}
}

func TestReadContextFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	c, err := readContextFile(write("a.go", []byte("line1\r\nline2\ttab\n")))
	if err != nil || c.Name != "a.go" || c.Text != "line1\nline2\ttab\n" {
		t.Fatalf("readContextFile = %+v, %v", c, err)
	}
	for name, data := range map[string][]byte{
		"binary.bin": {0x00, 0x01, 0x02, 'a'},
		"esc.txt":    []byte("a\x1b[31mred"),
		"latin1.txt": {'c', 'a', 'f', 0xe9},
	} {
		if _, err := readContextFile(write(name, data)); err == nil || !strings.Contains(err.Error(), "is not text") {
			t.Errorf("%s: err = %v, want \"is not text\"", name, err)
		}
	}
	if _, err := readContextFile(write("huge.txt", bytes.Repeat([]byte("a"), maxContextFileRead+1))); err == nil {
		t.Error("a file over the read bound was accepted")
	}
	if _, err := readContextFile(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file was accepted")
	}
	if _, err := readContextFile("-"); err == nil {
		t.Error("stdin was accepted as a context file")
	}
}

func TestConsultTitle(t *testing.T) {
	if got := consultTitle("Short question?\nBody"); got != "Short question?" {
		t.Fatalf("title = %q", got)
	}
	if got := consultTitle("\n\n  Indented first line  \nx"); got != "Indented first line" {
		t.Fatalf("title = %q", got)
	}
	long := strings.Repeat("é", 200)
	got := consultTitle(long)
	if n := len([]rune(got)); n != 120 || !strings.HasSuffix(got, "…") {
		t.Fatalf("cut title = %d code points %q, want 120 ending in an ellipsis", n, got)
	}
	exact := strings.Repeat("a", 120)
	if got := consultTitle(exact); got != exact {
		t.Fatalf("120-code-point title was changed: %q", got)
	}
}

func TestConsultUsageErrors(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "b.bin")
	if err := os.WriteFile(binary, []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"no peer":          {"consult", "--question", "q"},
		"no question":      {"consult", "@bob"},
		"both questions":   {"consult", "@bob", "--question", "q", "--question-from-file", "x"},
		"extra argument":   {"consult", "@bob", "@carol", "--question", "q"},
		"blank question":   {"consult", "@bob", "--question", "  \n "},
		"unknown flag":     {"consult", "@bob", "--question", "q", "--bogus"},
		"question file":    {"consult", "@bob", "--question-from-file", filepath.Join(t.TempDir(), "missing")},
		"context is a dir": {"consult", "@bob", "--question", "q", "--context-file", t.TempDir()},
	} {
		var out, errb bytes.Buffer
		code := run(args, &out, &errb)
		if code != exitUsage && name != "context is a dir" {
			t.Errorf("%s: code = %d, want usage (stderr %q)", name, code, errb.String())
		}
		if name == "context is a dir" && code != exitError {
			t.Errorf("%s: code = %d, want error", name, code)
		}
	}

	// A binary context file is refused by the CLI before any daemon call, with
	// the bad_request code, under --json too.
	var out, errb bytes.Buffer
	code := run([]string{"consult", "@bob", "--question", "q", "--context-file", binary, "--json"}, &out, &errb)
	if code != exitError || !strings.Contains(out.String(), `"bad_request"`) || !strings.Contains(out.String(), "context file") ||
		!strings.Contains(out.String(), "is not text") {
		t.Fatalf("binary context: code %d, stdout %q", code, out.String())
	}
	var help bytes.Buffer
	if code := run([]string{"consult", "--help"}, &help, &errb); code != exitOK || !strings.Contains(help.String(), "--context-file") {
		t.Fatalf("consult --help: %d %q", code, help.String())
	}
}

var _ = daemon.RequestSubmitResult{}

// wait on the derived session id, before the session exists: timeout (exit 4)
// while the peer has not answered, "declined" once it declines.
func TestConsultWaitTimeoutAndDeclined(t *testing.T) {
	oldInterval := waitPollInterval
	waitPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = oldInterval })
	a, b := consultTeam(t)

	_, out, _ := cli(t, a, "consult", "@bob", "--question", "Will you answer?", "--json")
	var sub requestBody
	if err := json.Unmarshal([]byte(out), &sub); err != nil || sub.Session == "" {
		t.Fatalf("consult --json = %q: %v", out, err)
	}
	waitOn := func(n *testNode, timeout string) (int, waitBody) {
		t.Helper()
		t.Setenv(paths.HomeEnv, n.p.Dir)
		var wout, werr bytes.Buffer
		code := run([]string{"wait", sub.Session, "--timeout", timeout, "--json"}, &wout, &werr)
		var w waitBody
		if err := json.Unmarshal(wout.Bytes(), &w); err != nil {
			t.Fatalf("wait --json = %q (%s): %v", wout.String(), werr.String(), err)
		}
		return code, w
	}
	if code, w := waitOn(a, "1"); code != exitWaitTimeout || w.Wait != "timeout" {
		t.Fatalf("wait before any answer = %d %+v, want exit 4 timeout", code, w)
	}

	pollCLI(t, b, "the question in B's inbox", func(o string) bool { return strings.Contains(o, sub.ID) }, "inbox", "--json")
	if code, out, errs := cli(t, b, "decline", sub.ID, "--reason", "not now", "--json"); code != exitOK {
		t.Fatalf("decline: %d %s %s", code, out, errs)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, w := waitOn(a, "2")
		if code == exitOK && w.Wait == "declined" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("wait never reported declined; last %d %+v", code, w)
		}
	}
}
