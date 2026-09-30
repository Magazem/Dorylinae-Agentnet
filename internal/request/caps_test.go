package request

// R55-F13 acceptance for the per-peer caps of Docs/protocol/request.md
// §Per-peer caps and §Receiving (tests A5-A8, A13, A21, A22 of
// Docs/review/71-r55-f13-spec.md). Uses the helpers of store_test.go and
// lifecycle_test.go.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// seedIn inserts n `in` rows from peer directly (the caps count stored rows,
// so how they got there does not matter), received at receivedAt.
func seedIn(t *testing.T, db *sql.DB, peer string, introducer any, state string, receivedAt time.Time, n int) {
	t.Helper()
	seedIntro(t, db, peer, introducer, nil, state, receivedAt, n)
}

// seedIntro is seedIn with the sender's introduction time recorded on the
// rows (requests.introduced_at, owner decision D62).
func seedIntro(t *testing.T, db *sql.DB, peer string, introducer, introducedAt any, state string, receivedAt time.Time, n int) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := tx.Exec(`INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared,
			body, body_hash, state, created, received_at, mail_id, updated, introducer, introduced_at)
			VALUES ('in', ?, ?, ?, 'task', 'normal', 'normal', '{}', 'h', ?, ?, ?, 'm', ?, ?, ?)`,
			peer, NewID(), testTeam, state, wireTime(receivedAt), storeTime(receivedAt), storeTime(receivedAt), introducer, introducedAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func freshRequest(from string) *Request {
	r := receivedRequest()
	r.ID = NewID()
	r.From = from
	return r
}

func inRow(t *testing.T, db *sql.DB, peer, id string) (state, code, body string) {
	t.Helper()
	var c sql.NullString
	if err := db.QueryRow(`SELECT state, decline_code, body FROM requests WHERE direction = 'in' AND peer = ? AND id = ?`, peer, id).
		Scan(&state, &c, &body); err != nil {
		t.Fatalf("row %s: %v", id, err)
	}
	return state, c.String, body
}

// A5: the 101st open request from one peer is auto-declined inbox_full with
// no content, and the decline goes out in the same transaction. Other peers
// are unaffected, deferred and accepted count, final states do not, and a
// freed slot takes the next request.
func TestOpenCapInboxFull(t *testing.T) {
	s, _, al := newTestStore(t, testTo, &policy{})
	now := time.Now()
	seedIn(t, s.DB, testFrom, nil, StatePending, now.Add(-48*time.Hour), 98)
	seedIn(t, s.DB, testFrom, nil, StateDeferred, now.Add(-48*time.Hour), 1)
	seedIn(t, s.DB, testFrom, nil, StateDeclined, now.Add(-48*time.Hour), 40) // final: not counted
	last := freshRequest(testFrom)
	if err := deliverRequest(t, s, last, now); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := inRow(t, s.DB, testFrom, last.ID); st != StatePending {
		t.Fatalf("100th open request = %s, want pending", st)
	}

	full := freshRequest(testFrom)
	full.Title = "secret-title-marker"
	if err := deliverRequest(t, s, full, now); err != nil {
		t.Fatal(err)
	}
	st, code, body := inRow(t, s.DB, testFrom, full.ID)
	if st != StateDeclined || code != "inbox_full" || body != "{}" {
		t.Fatalf("101st = %s %s body %s, want declined inbox_full {}", st, code, body)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM outbox WHERE kind = 'request.decline' AND to_key = ?`, testFrom); n != 1 {
		t.Fatalf("decline mails = %d, want 1", n)
	}
	var lastReply string
	if err := s.DB.QueryRow(`SELECT last_reply FROM requests WHERE id = ?`, full.ID).Scan(&lastReply); err != nil || !strings.Contains(lastReply, `"code":"inbox_full"`) {
		t.Fatalf("last_reply = %s, %v", lastReply, err)
	}
	if acts := al.actions(); acts[len(acts)-1] != "request.auto_decline" {
		t.Fatalf("audit = %v", acts)
	}

	// Another peer is still pending.
	other := freshRequest(testKey(9))
	if err := deliverRequest(t, s, other, now); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := inRow(t, s.DB, other.From, other.ID); st != StatePending {
		t.Fatalf("other peer = %s, want pending", st)
	}

	// Accepted counts too: accepting one keeps the cap full.
	if _, err := s.Accept(context.Background(), last.ID, testFrom); err != nil {
		t.Fatal(err)
	}
	still := freshRequest(testFrom)
	if err := deliverRequest(t, s, still, now); err != nil {
		t.Fatal(err)
	}
	if _, code, _ := inRow(t, s.DB, testFrom, still.ID); code != "inbox_full" {
		t.Fatalf("with an accepted row counted: code %q, want inbox_full", code)
	}
	// After the user declines one, the next request is pending.
	if _, err := s.Decline(context.Background(), last.ID, testFrom, "no"); err == nil {
		t.Fatal("declining an accepted request should be refused")
	}
	var pend string
	if err := s.DB.QueryRow(`SELECT id FROM requests WHERE direction = 'in' AND peer = ? AND state = 'pending' LIMIT 1`, testFrom).Scan(&pend); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decline(context.Background(), pend, testFrom, "no"); err != nil {
		t.Fatal(err)
	}
	next := freshRequest(testFrom)
	if err := deliverRequest(t, s, next, now); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := inRow(t, s.DB, testFrom, next.ID); st != StatePending {
		t.Fatalf("after a decline freed a slot: %s, want pending", st)
	}
}

// A5 (sender side): the sender's mirror accepts the inbox_full code and ends
// declined (inbox_full).
func TestMirrorAcceptsInboxFull(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	out, err := s.Submit(context.Background(), submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	body := map[string]any{"at": wireTime(now), "code": "inbox_full", "request": out.Request.ID, "seq": 1}
	if err := deliverMirror(t, s, KindDecline, testTo, now, body); err != nil {
		t.Fatal(err)
	}
	var state, code string
	if err := s.DB.QueryRow(`SELECT state, decline_code FROM requests WHERE direction = 'out' AND id = ?`, out.Request.ID).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != StateDeclined || code != "inbox_full" {
		t.Fatalf("mirror = %s %s, want declined inbox_full", state, code)
	}
}

// A6: the 201st new request in 24 h is refused for a limit: no row, the
// tombstone kept. After 24 h the same request id is stored.
func TestDailyCapRefusesForLimit(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	now := time.Now().UTC()
	s.Now = func() time.Time { return now }
	// 200 rows in the last 24 h, mixed states (auto-declined and cancelled
	// rows count too).
	seedIn(t, s.DB, testFrom, nil, StateDeclined, now.Add(-23*time.Hour), 120)
	seedIn(t, s.DB, testFrom, nil, StateCancelled, now.Add(-2*time.Hour), 60)
	seedIn(t, s.DB, testFrom, nil, StatePending, now.Add(-time.Hour), 20)
	req := freshRequest(testFrom)
	if _, err := s.DB.Exec(`INSERT INTO request_cancels (peer, id, reason, received_at) VALUES (?, ?, 'x', ?)`,
		testFrom, req.ID, storeTime(now)); err != nil {
		t.Fatal(err)
	}
	err := deliverRequest(t, s, req, now)
	if !errors.Is(err, mail.ErrLimit) || errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("201st: err = %v, want mail.ErrLimit", err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE id = ?`, req.ID); n != 0 {
		t.Fatalf("rows for the refused request = %d, want 0", n)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM request_cancels WHERE id = ?`, req.ID); n != 1 {
		t.Fatalf("tombstone rows = %d, want 1 (kept)", n)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM outbox`); n != 0 {
		t.Fatalf("outbox rows = %d, want 0 (no decline mail)", n)
	}
	// Another peer is not limited.
	other := freshRequest(testKey(9))
	if err := deliverRequest(t, s, other, now); err != nil {
		t.Fatal(err)
	}
	// 24 h later the rows have left the window: a new mail with the same
	// request is stored (as cancelled, through its tombstone).
	later := now.Add(24*time.Hour + time.Minute)
	s.Now = func() time.Time { return later }
	if err := deliverRequest(t, s, req, later); err != nil {
		t.Fatalf("after the window: %v", err)
	}
	if st, _, _ := inRow(t, s.DB, testFrom, req.ID); st != StateCancelled {
		t.Fatalf("after the window = %s, want cancelled (tombstone)", st)
	}
}

// A7: duplicates and conflicts of known ids are never capped.
func TestCapsSkipKnownIDs(t *testing.T) {
	s, _, al := newTestStore(t, testTo, &policy{})
	now := time.Now()
	known := freshRequest(testFrom)
	if err := deliverRequest(t, s, known, now); err != nil {
		t.Fatal(err)
	}
	seedIn(t, s.DB, testFrom, nil, StatePending, now, 250) // over both caps
	if err := deliverRequest(t, s, known, now); err != nil {
		t.Fatalf("duplicate over the caps: %v", err)
	}
	changed := *known
	changed.Title = "conflicting title"
	if err := deliverRequest(t, s, &changed, now); err != nil {
		t.Fatalf("conflict over the caps: %v", err)
	}
	acts := al.actions()
	if fmt.Sprint(acts[len(acts)-2:]) != "[request.duplicate request.conflict]" {
		t.Fatalf("audit = %v", acts)
	}
}

// A8: every auto-decline code stores {} and the views still work.
func TestAutoDeclinedRowsKeepNoContent(t *testing.T) {
	cases := []struct {
		code string
		pol  policy
		fill bool
	}{
		{"unverified_peer", policy{unverified: true}, false},
		{"unknown_team", policy{inactive: true}, false},
		{"not_team_member", policy{notMember: true}, false},
		{"inbox_full", policy{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			pol := tc.pol
			s, _, _ := newTestStore(t, testTo, &pol)
			if tc.fill {
				seedIn(t, s.DB, testFrom, nil, StatePending, time.Now().Add(-48*time.Hour), MaxOpenPerPeer)
			}
			req := freshRequest(testFrom)
			req.Title = "content-marker-title"
			if err := deliverRequest(t, s, req, time.Now()); err != nil {
				t.Fatal(err)
			}
			_, code, body := inRow(t, s.DB, testFrom, req.ID)
			if code != tc.code || body != "{}" {
				t.Fatalf("row code %s body %s, want %s {}", code, body, tc.code)
			}
			canon, _ := Canonical(req)
			if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE id = ? AND body_hash = ?`, req.ID, BodyHash(canon)); n != 1 {
				t.Fatal("body_hash of the received request not kept")
			}
			v, err := s.Show(context.Background(), req.ID, testFrom)
			if err != nil {
				t.Fatalf("request show: %v", err)
			}
			if v.Title != "" {
				t.Fatalf("view title = %q, want none", v.Title)
			}
			views, err := s.InboxList(context.Background(), InboxFilter{All: true})
			if err != nil {
				t.Fatalf("inbox list: %v", err)
			}
			found := false
			for _, v := range views {
				found = found || v.ID == req.ID
			}
			if !found {
				t.Fatal("declined row missing from inbox --all")
			}
			// A resend is still recognised as a duplicate.
			if err := deliverRequest(t, s, req, time.Now()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// introducePeer stores key as a member introduced by owner at introducedAt,
// as peers.Store.Introduce would.
func introducePeer(t *testing.T, db *sql.DB, key, owner string, introducedAt time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust, introduced_by)
		VALUES (?, 'n', 'h', '[]', '{}', ?, 'team', ?)`, key, introducedAt.UTC().Format(time.RFC3339), owner); err != nil {
		t.Fatal(err)
	}
}

// A21: keys introduced by one owner in the last 7 days share the introducer
// caps, and a churned key (its peers row gone) still counts while its
// introduction is fresh (review 71b F2, owner decision D62).
func TestIntroducerCapsFreshKeyChurn(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	now := time.Now()
	s.Now = func() time.Time { return now }
	introduced := now.Add(-24 * time.Hour).Truncate(time.Second)
	at := storeTime(introduced)
	owner := testKey(20)
	keys := []string{testKey(21), testKey(22), testKey(23)}
	for _, k := range keys {
		introducePeer(t, s.DB, k, owner, introduced)
	}
	seedIntro(t, s.DB, keys[0], owner, at, StatePending, now.Add(-2*time.Hour), 90)
	seedIntro(t, s.DB, keys[1], owner, at, StatePending, now.Add(-2*time.Hour), 90)
	seedIntro(t, s.DB, keys[2], owner, at, StatePending, now.Add(-2*time.Hour), 19)
	r := freshRequest(keys[2])
	if err := deliverRequest(t, s, r, now); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := inRow(t, s.DB, keys[2], r.ID); st != StatePending {
		t.Fatalf("200th for the owner = %s, want pending", st)
	}
	var intro, introAt sql.NullString
	if err := s.DB.QueryRow(`SELECT introducer, introduced_at FROM requests WHERE id = ?`, r.ID).Scan(&intro, &introAt); err != nil ||
		intro.String != owner || introAt.String != at {
		t.Fatalf("introducer = %v, introduced_at = %v, %v; want the owner at %s", intro, introAt, err, at)
	}
	r = freshRequest(keys[2])
	if err := deliverRequest(t, s, r, now); err != nil {
		t.Fatal(err)
	}
	if _, code, _ := inRow(t, s.DB, keys[2], r.ID); code != "inbox_full" {
		t.Fatalf("201st for the owner (key under 100) = %q, want inbox_full", code)
	}
	// The keys are garbage-collected; a fresh key from the same owner still
	// counts against the 200.
	if _, err := s.DB.Exec(`DELETE FROM peers WHERE introduced_by = ?`, owner); err != nil {
		t.Fatal(err)
	}
	fresh := testKey(24)
	introducePeer(t, s.DB, fresh, owner, now.Add(-time.Minute))
	r = freshRequest(fresh)
	if err := deliverRequest(t, s, r, now); err != nil {
		t.Fatal(err)
	}
	if _, code, _ := inRow(t, s.DB, fresh, r.ID); code != "inbox_full" {
		t.Fatalf("fresh key of the same owner = %q, want inbox_full", code)
	}
	// Once the churned keys' introductions are older than 7 days their open
	// rows stop counting; the fresh key is bounded by its own caps and the
	// fresh keys' rows only.
	later := introduced.Add(IntroducerFreshFor + time.Hour)
	s.Now = func() time.Time { return later }
	r = freshRequest(fresh)
	if err := deliverRequest(t, s, r, later); err != nil {
		t.Fatal(err)
	}
	if st, code, _ := inRow(t, s.DB, fresh, r.ID); st != StatePending {
		t.Fatalf("fresh key after the churned keys aged out = %s/%q, want pending", st, code)
	}

	// The introducer daily cap: 400 rows in 24 h across the owner's fresh keys.
	s2, _, _ := newTestStore(t, testTo, &policy{})
	for _, k := range keys {
		introducePeer(t, s2.DB, k, owner, introduced)
	}
	seedIntro(t, s2.DB, keys[0], owner, at, StateDeclined, now.Add(-time.Hour), 199)
	seedIntro(t, s2.DB, keys[1], owner, at, StateDeclined, now.Add(-time.Hour), 199)
	seedIntro(t, s2.DB, keys[2], owner, at, StateDeclined, now.Add(-time.Hour), 2)
	if err := deliverRequest(t, s2, freshRequest(keys[2]), now); !errors.Is(err, mail.ErrLimit) {
		t.Fatalf("401st for the owner in 24 h: %v, want mail.ErrLimit", err)
	}
}

// Security review 81 M1, owner decision D62: members introduced more than 7
// days ago do not share the introducer caps, so two hostile established
// members of one owner, each within its own caps, cannot lock out an honest
// member of the same owner, established or newly introduced.
func TestIntroducerCapsSpareEstablishedMembers(t *testing.T) {
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour).Truncate(time.Second)
	oldAt := storeTime(old)
	owner := testKey(30)
	h1, h2, honest, newcomer := testKey(31), testKey(32), testKey(33), testKey(34)
	setup := func(s *Store) {
		for _, k := range []string{h1, h2, honest} {
			introducePeer(t, s.DB, k, owner, old)
		}
		introducePeer(t, s.DB, newcomer, owner, now.Add(-time.Hour))
	}

	// Open cap: each hostile key holds 100 open requests, its own cap.
	s, _, _ := newTestStore(t, testTo, &policy{})
	setup(s)
	seedIntro(t, s.DB, h1, owner, oldAt, StatePending, now.Add(-48*time.Hour), 100)
	seedIntro(t, s.DB, h2, owner, oldAt, StatePending, now.Add(-48*time.Hour), 100)
	for _, k := range []string{honest, newcomer} {
		r := freshRequest(k)
		if err := deliverRequest(t, s, r, now); err != nil {
			t.Fatal(err)
		}
		if st, code, _ := inRow(t, s.DB, k, r.ID); st != StatePending {
			t.Fatalf("member %s's first request = %s/%q, want pending", k[:8], st, code)
		}
	}
	// The hostile keys are still bounded by their own open cap.
	r := freshRequest(h1)
	if err := deliverRequest(t, s, r, now); err != nil {
		t.Fatal(err)
	}
	if _, code, _ := inRow(t, s.DB, h1, r.ID); code != "inbox_full" {
		t.Fatalf("hostile key's 101st open = %q, want inbox_full", code)
	}

	// Daily cap: each hostile key sent 200 today, its own cap.
	s2, _, _ := newTestStore(t, testTo, &policy{})
	setup(s2)
	seedIntro(t, s2.DB, h1, owner, oldAt, StateDeclined, now.Add(-time.Hour), 200)
	seedIntro(t, s2.DB, h2, owner, oldAt, StateDeclined, now.Add(-time.Hour), 200)
	for _, k := range []string{honest, newcomer} {
		if err := deliverRequest(t, s2, freshRequest(k), now); err != nil {
			t.Fatalf("member %s's first request today: %v, want accepted", k[:8], err)
		}
	}
	if err := deliverRequest(t, s2, freshRequest(h2), now); !errors.Is(err, mail.ErrLimit) {
		t.Fatalf("hostile key's 201st today: %v, want mail.ErrLimit", err)
	}
}

// fakeDecided answers HasDecisionTx from a fixed set of request ids.
type fakeDecided struct {
	nopDebates
	decided map[string]bool
}

func (f *fakeDecided) HasDecisionTx(_ context.Context, _ *sql.Tx, _, _, id string) (bool, error) {
	return f.decided[id], nil
}

// A22: a debate request re-using a pruned id whose Decision was kept is
// bad_body, and nothing is stored.
func TestDebateIDWithDecisionRefused(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	req := receivedRequest()
	req.Type = TypeDebate
	req.Debate = &DebateMember{Rounds: 2, TurnTimeoutS: 3600, Commitment: strings.Repeat("a", 64)}
	hooks := &fakeDecided{decided: map[string]bool{req.ID: true}}
	s.Debates = hooks
	if err := deliverRequest(t, s, req, time.Now()); !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("err = %v, want bad_body", err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests`); n != 0 || hooks.received != 0 {
		t.Fatalf("rows %d, debate rows %d; want nothing stored", n, hooks.received)
	}
	hooks.decided = nil
	if err := deliverRequest(t, s, req, time.Now()); err != nil {
		t.Fatalf("without a Decision: %v", err)
	}
}

// A13: after prune, a resend of a pruned `in` request is bad_body (older
// than 30 days and unknown), and a late lifecycle mail for a pruned `out`
// row is an orphan.
func TestAfterPruneNothingReadmitted(t *testing.T) {
	s, _, al := newTestStore(t, testTo, &policy{})
	now := time.Now().UTC()
	old := freshRequest(testFrom)
	old.Created = now.Add(-40 * 24 * time.Hour).Truncate(time.Second)
	s.Now = func() time.Time { return old.Created.Add(time.Minute) }
	if err := deliverRequest(t, s, old, old.Created.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DELETE FROM requests WHERE id = ?`, old.ID); err != nil { // what prune does
		t.Fatal(err)
	}
	s.Now = func() time.Time { return now }
	if err := deliverRequest(t, s, old, now); !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("resend of a pruned request: %v, want bad_body", err)
	}

	sender, _, sal := newTestStore(t, testFrom, &policy{})
	out, err := sender.Submit(context.Background(), submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sender.DB.Exec(`DELETE FROM requests WHERE id = ?`, out.Request.ID); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"at": wireTime(now), "request": out.Request.ID, "seq": 3}
	if err := deliverMirror(t, sender, KindComplete, testTo, now, body); err != nil {
		t.Fatalf("late complete: %v", err)
	}
	if acts := sal.actions(); acts[len(acts)-1] != "request.orphan" {
		t.Fatalf("audit = %v, want request.orphan", acts)
	}
	_ = al
}
