package main

// Ticket 2.1b: CLI tests for `agentnet wait` (Docs/protocol/work-session.md
// §CLI, "wait"): the timeout exit code, using a directly-seeded session row
// so the test needs no peer or team setup.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// seedOpenSession inserts a work_sessions row directly (role requester, state
// open, never changing), so `agentnet wait` has something to poll without a
// full request/accept/result round trip.
func seedOpenSession(t *testing.T, dbPath string) (sid, reqID string) {
	t.Helper()
	sid = "s-00000000000000000000000000000001"
	reqID = "r-0000000000000000000000000000ab01"
	ctx := context.Background()
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000")
	wire := time.Now().UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
	if _, err := st.DB().ExecContext(ctx, `
INSERT INTO work_sessions (id, role, peer, request_id, team_id, state, seq, round, opened, state_at, updated)
VALUES (?, 'requester', 'bob-key-placeholder-0000000000000000000000000', ?, 't-0000000000000000000000000000ab01', 'open', 0, 1, ?, ?, ?)`,
		sid, reqID, wire, wire, now); err != nil {
		t.Fatal(err)
	}
	return sid, reqID
}

func TestWaitTimeoutExitCode(t *testing.T) {
	p := shortHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.RunWithOptions(ctx, p, ready, daemon.Options{}) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}

	sid, _ := seedOpenSession(t, p.DB)
	oldInterval := waitPollInterval
	waitPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = oldInterval })

	var out, errb bytes.Buffer
	start := time.Now()
	code := run([]string{"wait", sid, "--timeout", "1", "--json"}, &out, &errb)
	took := time.Since(start)
	if code != exitWaitTimeout {
		t.Fatalf("code = %d, stderr = %q, stdout = %q, want %d", code, errb.String(), out.String(), exitWaitTimeout)
	}
	if took > 5*time.Second {
		t.Fatalf("wait took %v, want close to the 1s timeout", took)
	}
	var body struct {
		OK   bool   `json:"ok"`
		Wait string `json:"wait"`
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON %q: %v", out.String(), err)
	}
	if !body.OK || body.Wait != "timeout" {
		t.Fatalf("body = %+v", body)
	}
}

func TestSessionsHelpAndUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"sessions", "--help"}, &out, &errb); code != exitOK {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), "agentnet sessions") {
		t.Fatalf("help output = %q", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"session"}, &out, &errb); code != exitUsage {
		t.Fatalf("code = %d, want usage", code)
	}
}

func TestResultRequiresStatus(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"result", "s-00000000000000000000000000000001"}, &out, &errb); code != exitUsage {
		t.Fatalf("code = %d, stderr = %q, want usage", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--status") {
		t.Fatalf("stderr = %q, want a mention of --status", errb.String())
	}
}

func TestSessionMutuallyExclusiveActions(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"session", "s-00000000000000000000000000000001", "--discard", "--cancel"}, &out, &errb)
	if code != exitUsage {
		t.Fatalf("code = %d, stderr = %q, want usage", code, errb.String())
	}
}
