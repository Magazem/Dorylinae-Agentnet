package audit

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func query(t *testing.T, l *Log, p ListParams) *ListResult {
	t.Helper()
	res, err := l.Query(context.Background(), p)
	if err != nil {
		t.Fatalf("Query(%+v): %v", p, err)
	}
	return res
}

func actions(res *ListResult) string {
	var out []string
	for _, e := range res.Events {
		out = append(out, e.Action)
	}
	return strings.Join(out, ",")
}

// audit_list shows every row, audit.chain_start included (List hides it).
func TestQueryShowsChainStart(t *testing.T) {
	s := openStore(t, filepath.Join(testutil.TempDir(t), "q.db"))
	l := New(s.DB())
	appendN(t, l, 2)
	res := query(t, l, ListParams{})
	if len(res.Events) != 3 || res.Events[0].Action != ActionChainStart || res.Events[0].Hash == "" || res.NextAfterID != 0 {
		t.Fatalf("Query = %+v", res)
	}
	if evs, _ := l.List(context.Background()); len(evs) != 2 {
		t.Fatalf("List still hides chain_start: %d", len(evs))
	}
	h, err := l.Head(context.Background())
	if err != nil || h == nil || h.ID != 3 || h.Hash != res.Events[2].Hash {
		t.Fatalf("Head = %+v, %v", h, err)
	}
}

func TestQueryPagesOver2500Rows(t *testing.T) {
	s := openStore(t, filepath.Join(testutil.TempDir(t), "q.db"))
	l := New(s.DB())
	appendN(t, l, 2499) // plus chain_start = 2500 rows
	var got []Entry
	var after int64
	calls := 0
	for {
		res := query(t, l, ListParams{Limit: 1000, AfterID: after})
		got = append(got, res.Events...)
		calls++
		if res.NextAfterID == 0 {
			break
		}
		after = res.NextAfterID
	}
	if len(got) != 2500 || calls != 3 {
		t.Fatalf("got %d rows in %d calls, want 2500 in 3", len(got), calls)
	}
	for i, e := range got {
		if e.ID != int64(i+1) {
			t.Fatalf("row %d has id %d", i, e.ID)
		}
	}
	// A limit above the maximum is clamped, not refused; a negative one is refused.
	if res := query(t, l, ListParams{Limit: 1 << 20}); len(res.Events) != 2500 || res.NextAfterID != 0 {
		t.Fatalf("clamped limit: %d rows, next %d", len(res.Events), res.NextAfterID)
	}
	if _, err := l.Query(context.Background(), ListParams{Limit: -1}); !errors.Is(err, ErrBadParams) {
		t.Fatalf("negative limit: %v", err)
	}
}

func TestQueryFilters(t *testing.T) {
	s := openStore(t, filepath.Join(testutil.TempDir(t), "q.db"))
	l := New(s.DB())
	ctx := context.Background()
	for _, a := range []string{"grant.create", "grant.revoke", "request.in", "granted.x"} {
		if err := l.Append(ctx, ActorCLI, a, map[string]string{"k": "v"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := actions(query(t, l, ListParams{Action: "grant."})); got != "grant.create,grant.revoke" {
		t.Fatalf("action prefix = %q", got)
	}
	// Time window: rewrite ts through the drop-triggers path so the rows are far apart.
	dropTriggers(t, s.DB())
	exec(t, s.DB(),
		`UPDATE audit_events SET ts = '2026-10-01T09:00:00.5Z' WHERE id = 2`,
		`UPDATE audit_events SET ts = '2026-10-01T09:00:00Z' WHERE id = 3`,
		`UPDATE audit_events SET ts = '2026-10-02T00:00:00Z' WHERE id = 4`,
		`UPDATE audit_events SET ts = '2026-10-03T00:00:00Z' WHERE id = 5`)
	// The half-second row sorts before its whole-second neighbour as text but is later in time.
	got := query(t, l, ListParams{Since: "2026-10-01T09:00:00.25Z", Until: "2026-10-02T00:00:00Z"})
	ids := func(res *ListResult) string {
		var out []string
		for _, e := range res.Events {
			out = append(out, fmt.Sprint(e.ID))
		}
		return strings.Join(out, ",")
	}
	if g := ids(got); g != "2,4" {
		t.Fatalf("since/until window ids = %s, want 2,4 (row 3 is earlier than .25, row 5 is later)", g)
	}
	if g := ids(query(t, l, ListParams{Since: "2026-10-02T00:00:00Z"})); g != "4,5" {
		t.Fatalf("since ids = %s", g)
	}
	if g := ids(query(t, l, ListParams{Until: "2026-10-01T09:00:00Z"})); g != "1,3" { // row 1 is the chain start, written now
		t.Fatalf("until ids = %s", g)
	}
	if _, err := l.Query(ctx, ListParams{Since: "yesterday"}); !errors.Is(err, ErrBadParams) {
		t.Fatalf("bad since: %v", err)
	}
	if _, err := l.Query(ctx, ListParams{Session: "x-1"}); !errors.Is(err, ErrBadParams) {
		t.Fatalf("bad session: %v", err)
	}
}

// A session view holds the session's request, grant, approval, decision and ws.*
// rows and nothing of another session (ticket 3.6b acceptance).
func TestQuerySessionView(t *testing.T) {
	s := openStore(t, filepath.Join(testutil.TempDir(t), "q.db"))
	l := New(s.DB())
	ctx := context.Background()
	const (
		sess, req, peer = "s-aaaa", "r-aaaa", "PEER-A"
		other, oreq     = "s-bbbb", "r-bbbb"
	)
	now := time.Now().UTC().Format(time.RFC3339)
	exec(t, s.DB(),
		fmt.Sprintf(`INSERT INTO work_sessions (id, role, peer, request_id, team_id, state, opened, state_at, updated)
			VALUES ('%s', 'requester', '%s', '%s', 't-1', 'open', '%s', '%s', '%s')`, sess, peer, req, now, now, now),
		fmt.Sprintf(`INSERT INTO grants (id, direction, peer, session, action, label, sensitive, nbf, exp, token, state, created, updated)
			VALUES ('g-1', 'issued', '%s', '%s', 'fs.read', 'l', 0, '%s', '%s', '{}', 'active', '%s', '%s')`, peer, sess, now, now, now, now),
		fmt.Sprintf(`INSERT INTO approvals (id, kind, subject, summary, created, expires, state)
			VALUES ('a-1', 'grant', 'g-1', 's', '%s', '%s', 'pending')`, now, now))
	rows := []struct {
		action string
		detail map[string]string
		in     bool
	}{
		{"request.in", map[string]string{"request": req, "peer": peer}, true},
		{"request.state", map[string]string{"request": req, "peer": "PEER-Q"}, false}, // same request id, other peer
		{"request.in", map[string]string{"request": oreq, "peer": peer}, false},
		{"ws.state", map[string]string{"session": sess, "peer": peer}, true},
		{"ws.state", map[string]string{"session": other, "peer": peer}, false},
		{"grant.create", map[string]string{"grant": "g-1", "session": sess}, true},
		{"grant.orphan", map[string]string{"grant": "g-1", "peer": peer}, true},
		{"grant.revoke", map[string]string{"grant": "g-9", "peer": peer}, false},
		{"approval.create", map[string]string{"id": "a-1", "kind": "grant", "subject": "g-1"}, true},
		{"approval.bad_code", map[string]string{"id": "a-1"}, true},
		{"approval.create", map[string]string{"id": "a-2", "kind": "grant", "subject": "g-9"}, false},
		{"approval.approve", map[string]string{"id": "a-3", "kind": "release", "subject": sess}, true},
		{"decision.close", map[string]string{"id": DecisionID(sess)}, true},
		{"decision.close", map[string]string{"id": DecisionID(other)}, false},
	}
	for _, r := range rows {
		if err := l.Append(ctx, ActorDaemon, r.action, r.detail); err != nil {
			t.Fatal(err)
		}
	}

	var want []string
	for i, r := range rows {
		if r.in {
			want = append(want, fmt.Sprintf("%d:%s", i+2, r.action)) // +1 chain_start, +1 one-based
		}
	}
	for _, id := range []string{sess, req} {
		res := query(t, l, ListParams{Session: id})
		var got []string
		for _, e := range res.Events {
			got = append(got, fmt.Sprintf("%d:%s", e.ID, e.Action))
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("session view for %s:\n got %v\nwant %v", id, got, want)
		}
	}
	// A request without a session shows its request rows, whatever the peer.
	res := query(t, l, ListParams{Session: oreq})
	if len(res.Events) != 1 || res.Events[0].Action != "request.in" {
		t.Errorf("request-only view = %+v", res.Events)
	}
	// The session filter combines with the action prefix.
	if got := actions(query(t, l, ListParams{Session: sess, Action: "approval."})); got != "approval.create,approval.bad_code,approval.approve" {
		t.Errorf("session+action = %q", got)
	}
	// An unknown session id matches its own ws rows only, never everything.
	if res := query(t, l, ListParams{Session: "s-zzzz"}); len(res.Events) != 0 {
		t.Errorf("unknown session = %+v", res.Events)
	}
}

func TestDecisionIDVector(t *testing.T) {
	// Docs/protocol/decision.md §Vector.
	if got, want := DecisionID("s-36375782ceb6baea9cee4d4273dfb035"), "d-7eaeb0b78e6bd96981357b500af94044"; got != want {
		t.Fatalf("DecisionID = %s, want %s", got, want)
	}
}

// Review 44 L2: the log can be read, and Verify names the broken row, through a
// read-only handle even when the head is unchained and the daemon cannot start.
func TestReadOnlyOpenVerifiesUnchainedHead(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "ro.db")
	s := openStore(t, path)
	l := New(s.DB())
	appendN(t, l, 3)
	dropTriggers(t, s.DB())
	exec(t, s.DB(), `INSERT INTO audit_events (id, ts, actor, action, detail) VALUES (5, '2026-10-01T09:00:00Z', 'x', 'bypass', '{}')`)
	// The daemon's own append now refuses, and says how to look.
	err := l.Append(context.Background(), ActorDaemon, ActionDaemonStart, nil)
	if err == nil || !strings.Contains(err.Error(), "agentnet log --verify") {
		t.Fatalf("append after unchained head: %v", err)
	}
	_ = s.Close()

	db, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ro := New(db)
	wantBroken(t, mustVerify(t, ro), 5, ReasonUnchained)
	if res := query(t, ro, ListParams{}); len(res.Events) != 5 {
		t.Fatalf("read-only list = %d rows", len(res.Events))
	}
	if _, err := db.Exec(`INSERT INTO audit_events (id, ts, actor, action, detail, hash) VALUES (99, 'x', 'x', 'x', '{}', NULL)`); err == nil {
		t.Fatal("the read-only handle accepted a write")
	}
	if _, err := store.OpenReadOnly(filepath.Join(testutil.TempDir(t), "missing.db")); err == nil {
		t.Fatal("OpenReadOnly of a missing file succeeded")
	}
}
