package request

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// Request ids are unique only per sender (R55-F20, Docs/review/83-r55-f20-spec.md).

// fakeRow stores a row (direction, peer, id) directly, as a database from
// before R55-F20 (or another code path) could hold it: a request from a
// throwaway sender, re-keyed.
func fakeRow(t *testing.T, s *Store, direction, peer, id, title string) {
	t.Helper()
	req := receivedRequest()
	req.ID = NewID()
	req.From = testKey(9)
	req.To = s.Self
	req.Title = title
	if err := deliverRequest(t, s, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE requests SET direction = ?, peer = ?, id = ? WHERE peer = ? AND id = ?`,
		direction, peer, id, req.From, req.ID); err != nil {
		t.Fatal(err)
	}
}

func rowHash(t *testing.T, s *Store, direction, peer, id string) string {
	t.Helper()
	var h string
	if err := s.DB.QueryRow(`SELECT body_hash || state FROM requests WHERE direction = ? AND peer = ? AND id = ?`,
		direction, peer, id).Scan(&h); err != nil {
		t.Fatal(err)
	}
	return h
}

// Acceptance tests 1 and 2: B's new request with an id we sent, to B or to C,
// is bad_body and stores nothing; our out row is unchanged.
func TestReceiveRefusesOurOutID(t *testing.T) {
	for _, to := range []struct{ name, peer string }{{"same peer", testFrom}, {"other peer", testKey(3)}} {
		t.Run(to.name, func(t *testing.T) {
			s, _, _ := newTestStore(t, testTo, &policy{})
			fakeRow(t, s, "out", to.peer, testID, "mine")
			before := rowHash(t, s, "out", to.peer, testID)
			if err := deliverRequest(t, s, receivedRequest(), time.Now()); !errors.Is(err, mail.ErrBadBody) {
				t.Fatalf("err = %v, want bad_body", err)
			}
			if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = ?`, testID); n != 0 {
				t.Errorf("in rows = %d, want 0", n)
			}
			if after := rowHash(t, s, "out", to.peer, testID); after != before {
				t.Errorf("out row changed: %s -> %s", before, after)
			}
		})
	}
}

// Acceptance test 3: C's in row holds the id. B's request is refused; C's
// resend is still a duplicate whose last reply is echoed.
func TestReceiveRefusesOtherPeersInID(t *testing.T) {
	s, ob, al := newTestStore(t, testTo, &policy{})
	ctx := context.Background()
	peerC := testKey(3)
	fromC := receivedRequest()
	fromC.From = peerC
	if err := deliverRequest(t, s, fromC, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decline(ctx, testID, peerC, "no"); err != nil {
		t.Fatal(err)
	}
	if err := deliverRequest(t, s, receivedRequest(), time.Now()); !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("B's request: err = %v, want bad_body", err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE id = ?`, testID); n != 1 {
		t.Fatalf("rows with the id = %d, want 1", n)
	}

	later := time.Now().Add(resendThrottle + time.Minute)
	s.Now = func() time.Time { return later }
	al.entries = nil
	if err := deliverRequest(t, s, fromC, later); err != nil {
		t.Fatalf("C's resend: %v", err)
	}
	if got := al.actions(); len(got) != 1 || got[0] != "request.duplicate" {
		t.Errorf("audit = %v, want request.duplicate", got)
	}
	if n := countRows(t, ob.DB, `SELECT COUNT(*) FROM outbox WHERE kind = 'request.decline'`); n != 2 {
		t.Errorf("decline mails = %d, want 2 (the reply and its echo)", n)
	}
}

// Acceptance test 4: a request refused for its id counts towards no daily cap
// and leaves B's tombstone in place.
func TestReceiveRefusedIDKeepsTombstone(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	fakeRow(t, s, "out", testFrom, testID, "mine")
	now := time.Now()
	if err := deliverMirror(t, s, KindCancel, testFrom, now, map[string]any{"at": wireTime(now), "request": testID}); err != nil {
		t.Fatal(err)
	}
	daily := `SELECT COUNT(*) FROM requests WHERE direction = 'in' AND peer = ?`
	before := countRows(t, s.DB, daily, testFrom)
	if err := deliverRequest(t, s, receivedRequest(), now.Add(time.Second)); !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("err = %v, want bad_body", err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM request_cancels WHERE peer = ? AND id = ?`, testFrom, testID); n != 1 {
		t.Errorf("tombstones = %d, want 1", n)
	}
	if after := countRows(t, s.DB, daily, testFrom); after != before {
		t.Errorf("B's daily rows %d -> %d, want unchanged", before, after)
	}
}

// Acceptance test 5: Submit draws the id again while a row has it, and gives
// up after maxIDDraws.
func TestSubmitRedrawsTakenID(t *testing.T) {
	s, ob, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	fakeRow(t, s, "in", testKey(3), testID, "theirs")
	fresh := NewID()
	ids := []string{testID, fresh}
	s.NewIDFunc = func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	}
	prepared := 0
	p := submitParams("", "")
	p.Prepare = func(*Request) error {
		prepared++
		return nil
	}
	out, err := s.Submit(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if out.Request.ID != fresh || prepared != 2 {
		t.Fatalf("submitted %s after %d prepares, want %s after 2", out.Request.ID, prepared, fresh)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = ?`, fresh); n != 1 {
		t.Errorf("out rows = %d, want 1", n)
	}
	if n := countRows(t, ob.DB, `SELECT COUNT(*) FROM outbox WHERE kind = 'request'`); n != 1 {
		t.Fatalf("outbox rows = %d, want 1", n)
	}
	var mailID string
	if err := s.DB.QueryRow(`SELECT mail_id FROM requests WHERE direction = 'out' AND id = ?`, fresh).Scan(&mailID); err != nil {
		t.Fatal(err)
	}
	if out.MailID != mailID {
		t.Errorf("mail id = %s, want the out row's %s", out.MailID, mailID)
	}

	s.NewIDFunc = func() string { return testID }
	prepared = 0
	if _, err := s.Submit(ctx, p); err == nil || !strings.Contains(err.Error(), "no unused request id") {
		t.Fatalf("eight taken ids: err = %v", err)
	}
	if prepared != maxIDDraws {
		t.Errorf("prepares = %d, want %d", prepared, maxIDDraws)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE direction = 'out'`); n != 1 {
		t.Errorf("out rows = %d, want 1 (nothing stored)", n)
	}
	if n := countRows(t, ob.DB, `SELECT COUNT(*) FROM outbox WHERE kind = 'request'`); n != 1 {
		t.Errorf("outbox rows = %d, want 1 (nothing queued)", n)
	}
}

// Acceptance test 6: request_show on old colliding rows.
func TestShowOldCollision(t *testing.T) {
	ctx := context.Background()
	peerB := testKey(3)

	s, _, _ := newTestStore(t, testTo, &policy{})
	fakeRow(t, s, "out", testKey(4), testID, "mine")
	if v, err := s.Show(ctx, testID, ""); err != nil || v.Direction != "out" {
		t.Fatalf("out only: %s, %v", v.Direction, err)
	}
	if _, err := s.Show(ctx, testID, testKey(4)); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("out only, from: %v, want ErrUnknownRequest (from selects in rows)", err)
	}
	fakeRow(t, s, "in", peerB, testID, "theirs")
	if _, err := s.Show(ctx, testID, ""); !errors.Is(err, ErrAmbiguousRequest) {
		t.Fatalf("out and in: %v, want ErrAmbiguousRequest", err)
	}
	if v, err := s.Show(ctx, testID, peerB); err != nil || v.Direction != "in" || v.Title != "theirs" {
		t.Fatalf("from B: %s %q, %v; want the in row", v.Direction, v.Title, err)
	}

	s2, _, _ := newTestStore(t, testTo, &policy{})
	fakeRow(t, s2, "in", peerB, testID, "theirs")
	if v, err := s2.Show(ctx, testID, ""); err != nil || v.Direction != "in" {
		t.Fatalf("in only: %s, %v", v.Direction, err)
	}
	if k, err := s2.FindIn(ctx, testID, ""); err != nil || k.Peer != peerB {
		t.Fatalf("FindIn = %+v, %v", k, err)
	}
}

// Acceptance test 13 (R55-163): a request that consumes a tombstone is stored
// cancelled, but keeps no content when D5 or the team checks would have
// declined it.
func TestTombstonedRowKeepsNoContentWhenRefused(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pol      policy
		wantBody bool
	}{
		{"unverified peer", policy{unverified: true}, false},
		{"not a team member", policy{notMember: true}, false},
		{"unknown team", policy{inactive: true}, false},
		{"verified member", policy{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pol := tc.pol
			s, ob, al := newTestStore(t, testTo, &pol)
			ctx := context.Background()
			now := time.Now()
			req := receivedRequest()
			if err := deliverMirror(t, s, KindCancel, testFrom, now, map[string]any{"at": wireTime(now), "request": req.ID}); err != nil {
				t.Fatal(err)
			}
			if err := deliverRequest(t, s, req, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			var body, state, lastReply string
			var seq int
			if err := s.DB.QueryRow(`SELECT body, state, state_seq, last_reply FROM requests WHERE direction = 'in' AND peer = ? AND id = ?`,
				testFrom, req.ID).Scan(&body, &state, &seq, &lastReply); err != nil {
				t.Fatal(err)
			}
			if state != StateCancelled || seq != 1 || !strings.Contains(lastReply, `"kind":"request.cancelled"`) || !strings.Contains(lastReply, `"seq":1`) {
				t.Fatalf("row = %s seq %d last_reply %s", state, seq, lastReply)
			}
			if gotBody := body != emptyBody; gotBody != tc.wantBody {
				t.Fatalf("body = %s, want content %v", body, tc.wantBody)
			}
			v, err := s.Show(ctx, req.ID, testFrom)
			if err != nil {
				t.Fatalf("show: %v", err)
			}
			if wantTitle := map[bool]string{true: req.Title, false: ""}[tc.wantBody]; v.Title != wantTitle {
				t.Errorf("title = %q, want %q", v.Title, wantTitle)
			}
			if tc.wantBody {
				return
			}
			if _, err := s.Show(ctx, req.ID, ""); err != nil {
				t.Errorf("show without from: %v", err)
			}
			// A resend is a duplicate (the body hash is kept) that echoes the
			// tombstone's seq = 1; a second cancel is a duplicate cancel.
			later := now.Add(resendThrottle + time.Minute)
			s.Now = func() time.Time { return later }
			al.entries = nil
			if err := deliverRequest(t, s, req, later); err != nil {
				t.Fatal(err)
			}
			if got := al.actions(); len(got) != 1 || got[0] != "request.duplicate" {
				t.Errorf("resend audit = %v, want request.duplicate", got)
			}
			if n := countRows(t, ob.DB, `SELECT COUNT(*) FROM outbox WHERE kind = 'request.cancelled'`); n != 2 {
				t.Errorf("cancelled mails = %d, want 2 (the tombstone's and its echo)", n)
			}
			al.entries = nil
			if err := deliverMirror(t, s, KindCancel, testFrom, later, map[string]any{"at": wireTime(later), "request": req.ID}); err != nil {
				t.Fatal(err)
			}
			if len(al.entries) != 1 || !strings.Contains(al.entries[0].detail, `"result":"duplicate"`) {
				t.Errorf("second cancel audit = %+v, want a duplicate request.cancel_in", al.entries)
			}
		})
	}
}
