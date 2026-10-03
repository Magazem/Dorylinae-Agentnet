package main

// R55-F20 acceptance tests 7 (wait), 11 and 14 through two real daemons
// (Docs/review/83-r55-f20-spec.md §5). Old colliding rows cannot be produced
// any more, so they are made by copying a row in the database.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

const collidingPeer = "PEER-C"

// copyRow copies the rows of table matching where, applies set to the copy
// and stores it, on n's database while the daemon runs.
func copyRow(t *testing.T, n *testNode, table, where, set string, args ...any) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	conn, err := st.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	for _, q := range []string{
		`CREATE TEMP TABLE dup AS SELECT * FROM ` + table + ` WHERE ` + where,
		`UPDATE dup SET ` + set,
		`INSERT INTO ` + table + ` SELECT * FROM dup`,
		`DROP TABLE dup`,
	} {
		if _, err := conn.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		args = nil
	}
}

func nodeCount(t *testing.T, n *testNode, q string, args ...any) int {
	t.Helper()
	st, err := store.Open(context.Background(), n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var c int
	if err := st.DB().QueryRow(q, args...).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

// waitRun runs `agentnet wait <id> --json` against n and reports how long it
// took.
func waitRun(t *testing.T, n *testNode, id, timeout string) (code int, out string, took time.Duration) {
	t.Helper()
	t.Setenv(paths.HomeEnv, n.p.Dir)
	var wout, werr bytes.Buffer
	start := time.Now()
	code = run([]string{"wait", id, "--timeout", timeout, "--json"}, &wout, &werr)
	return code, wout.String(), time.Since(start)
}

// Acceptance test 14, and the wait half of test 7: an id that an old out and
// in row share, or that two sessions share, ends `wait` at once with
// ambiguous_request; the s- id still waits for the outcome.
func TestWaitAmbiguousRequestID(t *testing.T) {
	oldInterval := waitPollInterval
	waitPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = oldInterval })
	a, b := consultTeam(t)

	_, out, _ := cli(t, a, "consult", "@bob", "--question", "Will you answer?", "--json")
	var sub requestBody
	if err := json.Unmarshal([]byte(out), &sub); err != nil || sub.Session == "" {
		t.Fatalf("consult --json = %q: %v", out, err)
	}
	copyRow(t, a, "requests", `direction = 'out' AND id = ?`, `direction = 'in', peer = '`+collidingPeer+`'`, sub.ID)
	code, wout, took := waitRun(t, a, sub.ID, "30")
	if code != exitError || !strings.Contains(wout, `"ambiguous_request"`) {
		t.Fatalf("wait r- on an out/in collision = %d %q, want exit 1 ambiguous_request", code, wout)
	}
	if took > 5*time.Second {
		t.Errorf("wait took %v, want an immediate failure", took)
	}

	pollCLI(t, b, "the question in B's inbox", func(o string) bool { return strings.Contains(o, sub.ID) }, "inbox", "--json")
	if code, out, errs := cli(t, b, "decline", sub.ID, "--reason", "not now", "--json"); code != exitOK {
		t.Fatalf("decline: %d %s %s", code, out, errs)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, wout, _ := waitRun(t, a, sub.Session, "2")
		var w waitBody
		_ = json.Unmarshal([]byte(wout), &w)
		if code == exitOK && w.Wait == "declined" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("wait <s-id> never reported declined; last %d %q", code, wout)
		}
	}

	// Two sessions with the same request id.
	_, out, _ = cli(t, a, "consult", "@bob", "--question", "And this one?", "--json")
	var sub2 requestBody
	if err := json.Unmarshal([]byte(out), &sub2); err != nil || sub2.Session == "" {
		t.Fatalf("consult --json = %q: %v", out, err)
	}
	pollCLI(t, b, "the second question", func(o string) bool { return strings.Contains(o, sub2.ID) }, "inbox", "--json")
	if code, out, errs := cli(t, b, "accept", sub2.ID, "--json"); code != exitOK {
		t.Fatalf("accept: %d %s %s", code, out, errs)
	}
	deadline = time.Now().Add(20 * time.Second)
	for nodeCount(t, a, `SELECT COUNT(*) FROM work_sessions WHERE id = ?`, sub2.Session) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("A never opened the session")
		}
		time.Sleep(50 * time.Millisecond)
	}
	other := worksession.DeriveID(collidingPeer, a.key, sub2.ID)
	copyRow(t, a, "work_sessions", `id = ?`, `id = '`+other+`', role = 'worker', peer = '`+collidingPeer+`'`, sub2.Session)
	code, wout, took = waitRun(t, a, sub2.ID, "30")
	if code != exitError || !strings.Contains(wout, `"ambiguous_request"`) || took > 5*time.Second {
		t.Fatalf("wait r- of two sessions = %d %q after %v, want exit 1 ambiguous_request at once", code, wout, took)
	}
}

// Acceptance test 11: the respondent's --cancel in invited is bad_state and
// never cancels the user's own outgoing request with the same id.
func TestDebateCancelRespondentInvited(t *testing.T) {
	a, b := consultTeam(t)
	posA := writeJSONFile(t, t.TempDir(), "pos.json", `{"claim":"x","argument":"y"}`)
	_, out, _ := cli(t, a, "debate", "@bob", "--topic", "Do not cancel mine", "--position-file", posA, "--json")
	var sub requestBody
	if err := json.Unmarshal([]byte(out), &sub); err != nil || sub.ID == "" {
		t.Fatalf("debate start --json = %q: %v", out, err)
	}
	pollCLI(t, b, "the invitation", func(o string) bool { return strings.Contains(o, sub.ID) }, "inbox", "--json")
	// B's own outgoing request with the same id (an old collision).
	copyRow(t, b, "requests", `direction = 'in' AND id = ?`, `direction = 'out', peer = '`+collidingPeer+`', type = 'task'`, sub.ID)

	code, out, errs := cli(t, b, "debate", sub.Session, "--cancel", "--json")
	if code != exitError || !strings.Contains(out, `"bad_state"`) || !strings.Contains(out, "agentnet decline "+sub.ID) {
		t.Fatalf("B --cancel in invited = %d %q %q, want exit 1 bad_state pointing to decline", code, out, errs)
	}
	if n := nodeCount(t, b, `SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = ? AND state = 'pending' AND cancel IS NULL`, sub.ID); n != 1 {
		t.Errorf("B's own out row: %d pending uncancelled rows, want 1", n)
	}
	if n := nodeCount(t, b, `SELECT COUNT(*) FROM outbox WHERE kind = 'request.cancel'`); n != 0 {
		t.Errorf("B queued %d request.cancel mails, want 0", n)
	}
}
