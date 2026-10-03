package audit

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// TestQuerySessionRequestIDCollision: R55-F20 acceptance test 9. An r- id is
// resolved through its sessions and its requests rows together, as
// (direction, peer) pairs; more than one pair is ErrAmbiguous.
func TestQuerySessionRequestIDCollision(t *testing.T) {
	const req = "r-cccc"
	now := time.Now().UTC().Format(time.RFC3339)
	session := func(id, role, peer string) string {
		return fmt.Sprintf(`INSERT INTO work_sessions (id, role, peer, request_id, team_id, state, opened, state_at, updated)
			VALUES ('%s', '%s', '%s', '%s', 't-1', 'open', '%s', '%s', '%s')`, id, role, peer, req, now, now, now)
	}
	request := func(direction, peer string) string {
		return fmt.Sprintf(`INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared, body, body_hash, state, state_seq, created, mail_id, updated)
			VALUES ('%s', '%s', '%s', 't-1', 'task', 'normal', 'normal', '{}', 'h', 'pending', 0, '%s', 'm-1', '%s')`, direction, peer, req, now, now)
	}
	for _, c := range []struct {
		name string
		rows []string
	}{
		{"two sessions, different peers", []string{session("s-1", "requester", "PEER-B"), session("s-2", "worker", "PEER-C")}},
		{"no session, in rows from B and C", []string{request("in", "PEER-B"), request("in", "PEER-C")}},
		{"a session for B, a session-less request from C", []string{session("s-1", "worker", "PEER-B"), request("in", "PEER-B"), request("in", "PEER-C")}},
		{"an out and an in row for the same peer", []string{request("out", "PEER-B"), request("in", "PEER-B")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := openStore(t, filepath.Join(testutil.TempDir(t), "q.db"))
			exec(t, s.DB(), c.rows...)
			if _, err := New(s.DB()).Query(context.Background(), ListParams{Session: req}); !errors.Is(err, ErrAmbiguous) {
				t.Fatalf("err = %v, want ErrAmbiguous", err)
			}
		})
	}

	// One request row from B: C's rows naming the id are not shown. With no
	// row at all, every row naming the id is.
	s := openStore(t, filepath.Join(testutil.TempDir(t), "q.db"))
	l := New(s.DB())
	for _, peer := range []string{"PEER-B", "PEER-C"} {
		if err := l.Append(context.Background(), ActorDaemon, "request.in", map[string]string{"request": req, "peer": peer}); err != nil {
			t.Fatal(err)
		}
	}
	if res := query(t, l, ListParams{Session: req}); len(res.Events) != 2 {
		t.Fatalf("no row: %d events, want both", len(res.Events))
	}
	exec(t, s.DB(), request("in", "PEER-B"))
	res := query(t, l, ListParams{Session: req})
	if len(res.Events) != 1 || string(res.Events[0].Detail) != `{"peer":"PEER-B","request":"r-cccc"}` {
		t.Fatalf("B's request row: events = %+v, want only B's", res.Events)
	}
	// A session for the same (direction, peer) as the request is one pair.
	exec(t, s.DB(), session("s-1", "worker", "PEER-B"))
	if res := query(t, l, ListParams{Session: req}); len(res.Events) != 1 {
		t.Fatalf("session and request of B: %d events, want 1", len(res.Events))
	}
}
