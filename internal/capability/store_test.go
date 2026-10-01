package capability

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(testutil.TempDir(t), "t.db")
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB()
}

//nolint:gosec // test fixture: a placeholder canonical-token string, not a credential
func testRecord(id string, now time.Time) Record {
	return Record{
		ID: id, Direction: DirectionIssued, Peer: "peer-key", Session: "s-11111111111111111111111111111111",
		Action: ActionFSRead, Label: "repo-ab12", Path: "/tmp/repo", Scope: "",
		Sensitive: true, Nbf: now, Exp: now.Add(2 * time.Hour), Token: `{"grant":{},"sig":"x"}`,
	}
}

// TestGrantInsertGetList covers basic CRUD: InsertPending, Get, List filters.
func TestGrantInsertGetList(t *testing.T) {
	db := openTestDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }

	rec := testRecord(NewGrantID(), now)
	if err := s.InsertPending(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StatePendingApproval || got.Path != rec.Path || !got.Sensitive {
		t.Fatalf("got = %+v", got)
	}
	list, err := s.List(ctx, ListFilter{Session: rec.Session})
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if _, err := s.Get(ctx, "g-missing"); !errors.Is(err, ErrUnknownGrant) {
		t.Fatalf("err = %v, want ErrUnknownGrant", err)
	}
}

// TestActivateAndRevoke covers the pending_approval -> active -> revoked
// path, and idempotent revoke.
func TestActivateAndRevoke(t *testing.T) {
	db := openTestDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	rec := testRecord(NewGrantID(), now)
	if err := s.InsertPending(ctx, rec); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ar, err := s.ActivateTx(ctx, tx, rec.ID, "a-test", now)
	if err != nil {
		t.Fatal(err)
	}
	if ar.State != StateActive {
		t.Fatalf("state = %s, want active", ar.State)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// A second Activate (already active) is refused.
	tx2, _ := db.BeginTx(ctx, nil)
	if _, err := s.ActivateTx(ctx, tx2, rec.ID, "a-test", now); err == nil {
		t.Error("re-activate of an active grant should fail")
	}
	_ = tx2.Rollback()

	tx3, _ := db.BeginTx(ctx, nil)
	changed, err := s.RevokeTx(ctx, tx3, rec.ID, ReasonUser, now)
	if err != nil || !changed {
		t.Fatalf("revoke: %v changed=%v", err, changed)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, rec.ID)
	if got.State != StateRevoked || got.Reason != ReasonUser {
		t.Fatalf("got = %+v", got)
	}

	// Idempotent: revoking again changes nothing.
	tx4, _ := db.BeginTx(ctx, nil)
	changed, err = s.RevokeTx(ctx, tx4, rec.ID, ReasonUser, now)
	if err != nil || changed {
		t.Fatalf("second revoke: %v changed=%v, want changed=false", err, changed)
	}
	_ = tx4.Commit()
}

// TestRevokeForSessionIncludesPending is the 2.2c acceptance: closing a
// session revokes every grant of it, including pending_approval ones.
func TestRevokeForSessionIncludesPending(t *testing.T) {
	db := openTestDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sid := "s-22222222222222222222222222222222"

	active := testRecord(NewGrantID(), now)
	active.Session = sid
	pending := testRecord(NewGrantID(), now)
	pending.Session = sid
	other := testRecord(NewGrantID(), now)
	other.Session = "s-33333333333333333333333333333333"

	if err := s.InsertPending(ctx, active); err != nil {
		t.Fatal(err)
	}
	tx0, _ := db.BeginTx(ctx, nil)
	if _, err := s.ActivateTx(ctx, tx0, active.ID, "a-test", now); err != nil {
		t.Fatal(err)
	}
	_ = tx0.Commit()
	if err := s.InsertPending(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertPending(ctx, other); err != nil {
		t.Fatal(err)
	}

	tx, _ := db.BeginTx(ctx, nil)
	ids, err := s.RevokeForSessionTx(ctx, tx, sid, ReasonSessionClosed, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("revoked %d grants, want 2", len(ids))
	}
	for _, id := range []string{active.ID, pending.ID} {
		got, _ := s.Get(ctx, id)
		if got.State != StateRevoked || got.Reason != ReasonSessionClosed {
			t.Errorf("%s: got %+v, want revoked/session_closed", id, got)
		}
	}
	got, _ := s.Get(ctx, other.ID)
	if got.State != StatePendingApproval {
		t.Errorf("other session's grant = %+v, want untouched", got)
	}
}

// TestRevokeForPeer is the "peers remove revokes all that peer's grants" acceptance.
func TestRevokeForPeer(t *testing.T) {
	db := openTestDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	a := testRecord(NewGrantID(), now)
	a.Peer = "peer-x"
	b := testRecord(NewGrantID(), now)
	b.Peer = "peer-y"
	if err := s.InsertPending(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertPending(ctx, b); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := s.RevokeForPeerTx(ctx, tx, "peer-x", ReasonPeerRemoved, now)
	if err != nil || len(ids) != 1 || ids[0] != a.ID {
		_ = tx.Rollback()
		t.Fatalf("RevokeForPeerTx = %v, %v", ids, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, a.ID)
	if got.State != StateRevoked || got.Reason != ReasonPeerRemoved {
		t.Fatalf("a = %+v", got)
	}
	got, _ = s.Get(ctx, b.ID)
	if got.State != StatePendingApproval {
		t.Fatalf("b should be untouched, got %+v", got)
	}
}

// TestFindHeldTxRejectsOtherGrantor is review 24 M8: a grant.revoke (or any
// held lookup) must only match the peer that actually granted it.
func TestFindHeldTxRejectsOtherGrantor(t *testing.T) {
	db := openTestDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tx, _ := db.BeginTx(ctx, nil)
	rec := testRecord(NewGrantID(), now)
	rec.Peer = "grantor-a"
	if err := s.InsertHeldTx(ctx, tx, rec); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()

	tx2, _ := db.BeginTx(ctx, nil)
	_, ok, err := s.FindHeldTx(ctx, tx2, rec.ID, "grantor-b")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("FindHeldTx matched the wrong grantor")
	}
	_ = tx2.Rollback()

	tx3, _ := db.BeginTx(ctx, nil)
	got, ok, err := s.FindHeldTx(ctx, tx3, rec.ID, "grantor-a")
	if err != nil || !ok || got.ID != rec.ID {
		t.Fatalf("FindHeldTx(correct grantor) = %+v, %v, %v", got, ok, err)
	}
	_ = tx3.Rollback()

	// Unaffected: still active.
	final, _ := s.Get(ctx, rec.ID)
	if final.State != StateActive {
		t.Fatalf("state changed unexpectedly: %+v", final)
	}
}

func testPolicy(id, peer, action, path string, now time.Time) Policy {
	return Policy{
		ID: id, Peer: peer, Action: action, Path: path, Scope: "", Public: false,
		MaxExpiresS: int((7 * 24 * time.Hour).Seconds()), Until: now.Add(30 * 24 * time.Hour), Created: now,
	}
}

// TestPolicyMatchRules is the 2.2c acceptance for policy matching: a
// matching policy is found, and each of the documented mismatches (scope
// outside, longer expiry, other peer, other branch, sensitive under a
// --public-only policy, a policy past its until) is refused.
func TestPolicyMatchRules(t *testing.T) {
	db := openTestDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }

	base := testPolicy(NewPolicyID(), "peer-a", ActionFSRead, "/srv/repo", now)
	base.Scope = "internal/mail"
	insertPolicy(t, s, base)

	match := func(mp MatchParams) bool {
		p, err := s.Match(ctx, mp)
		if err != nil {
			t.Fatal(err)
		}
		return p != nil
	}
	baseParams := MatchParams{Peer: "peer-a", Action: ActionFSRead, Path: "/srv/repo", Scope: "internal/mail/inbox", Sensitive: true, ExpiresS: 3600, Now: now}
	if !match(baseParams) {
		t.Error("expected the base policy to match a scope inside its own")
	}

	scopeOutside := baseParams
	scopeOutside.Scope = "internal/mailbox" // not a segment-prefix of internal/mail
	if match(scopeOutside) {
		t.Error("scope outside the policy's should not match")
	}

	longerExpiry := baseParams
	longerExpiry.ExpiresS = int((8 * 24 * time.Hour).Seconds())
	if match(longerExpiry) {
		t.Error("an expiry over the policy's cap should not match")
	}

	otherPeer := baseParams
	otherPeer.Peer = "peer-z"
	if match(otherPeer) {
		t.Error("a different peer should not match")
	}

	gitPolicy := testPolicy(NewPolicyID(), "peer-a", ActionGitRead, "/srv/repo", now)
	gitPolicy.Branch = "main"
	insertPolicy(t, s, gitPolicy)
	otherBranch := MatchParams{Peer: "peer-a", Action: ActionGitRead, Path: "/srv/repo", Branch: "feature", Sensitive: true, ExpiresS: 3600, Now: now}
	if match(otherBranch) {
		t.Error("a different branch should not match")
	}
	sameBranch := otherBranch
	sameBranch.Branch = "main"
	if !match(sameBranch) {
		t.Error("the same branch should match")
	}
	// Review 28 H1: a policy without --public (sensitive) must not
	// auto-issue a --public grant, which would escape the result quarantine.
	publicUnderSensitive := sameBranch
	publicUnderSensitive.Sensitive = false
	if match(publicUnderSensitive) {
		t.Error("a --public grant should not match a policy without --public")
	}

	publicOnly := testPolicy(NewPolicyID(), "peer-a", ActionGitRead, "/srv/pub", now)
	publicOnly.Public = true
	insertPolicy(t, s, publicOnly)
	sensitiveUnderPublic := MatchParams{Peer: "peer-a", Action: ActionGitRead, Path: "/srv/pub", Sensitive: true, ExpiresS: 3600, Now: now}
	if match(sensitiveUnderPublic) {
		t.Error("a sensitive grant should not match a --public-only policy")
	}
	nonSensitiveUnderPublic := sensitiveUnderPublic
	nonSensitiveUnderPublic.Sensitive = false
	if !match(nonSensitiveUnderPublic) {
		t.Error("a non-sensitive grant should match a --public policy")
	}

	expired := testPolicy(NewPolicyID(), "peer-a", ActionFSRead, "/srv/old", now)
	expired.Until = now.Add(-time.Minute)
	insertPolicy(t, s, expired)
	pastUntil := MatchParams{Peer: "peer-a", Action: ActionFSRead, Path: "/srv/old", Sensitive: true, ExpiresS: 3600, Now: now}
	if match(pastUntil) {
		t.Error("a policy past its until should not match, and should be pruned")
	}
	pols, err := s.PolicyList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pols {
		if p.ID == expired.ID {
			t.Error("expired policy was not pruned")
		}
	}
}

// insertPolicy commits pol in its own transaction: the store's DB has a
// single connection (internal/store.Open), so a caller must never leave a
// transaction open across other calls that also need it.
func insertPolicy(t *testing.T, s *Store, pol Policy) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PolicyInsertTx(ctx, tx, pol); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestScopeWithin exercises the segment-prefix rule directly (never a
// string prefix, Docs/protocol/grant.md §Paths).
func TestScopeWithin(t *testing.T) {
	cases := []struct {
		child, parent string
		want          bool
	}{
		{"", "", true},
		{"a", "", true},
		{"", "a", false},
		{"internal/mail", "internal/mail", true},
		{"internal/mail/inbox", "internal/mail", true},
		{"internal/mailbox", "internal/mail", false}, // string prefix, not a segment
		{"internal", "internal/mail", false},
	}
	for _, c := range cases {
		if got := scopeWithin(c.child, c.parent); got != c.want {
			t.Errorf("scopeWithin(%q,%q) = %v, want %v", c.child, c.parent, got, c.want)
		}
	}
}

// A16 (R55-F13, review 55 R55-064): List pages by (created, id), newest
// first. 450 rows, three to each created value, read 200 at a time: 3 pages,
// every id once, in order, including the rows that share created.
func TestGrantListPages(t *testing.T) {
	db := openTestDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	want := map[string]bool{}
	for i := 0; i < 450; i++ {
		rec := testRecord(NewGrantID(), base)
		rec.Created = base.Add(time.Duration(i/3) * time.Second)
		if err := s.InsertPending(ctx, rec); err != nil {
			t.Fatal(err)
		}
		want[rec.ID] = true
	}
	var all []Record
	var sizes []int
	cursor := ""
	for len(sizes) < 10 {
		page, err := s.List(ctx, ListFilter{Session: "s-11111111111111111111111111111111", Limit: 200, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(page))
		all = append(all, page...)
		if len(page) < 200 {
			break
		}
		cursor = CursorOf(page[len(page)-1])
	}
	if len(sizes) != 3 || sizes[0] != 200 || sizes[1] != 200 || sizes[2] != 50 {
		t.Fatalf("page sizes = %v, want [200 200 50]", sizes)
	}
	seen := map[string]bool{}
	for i, r := range all {
		if seen[r.ID] || !want[r.ID] {
			t.Fatalf("row %d: %s duplicate or unknown", i, r.ID)
		}
		seen[r.ID] = true
		if i > 0 {
			p := all[i-1]
			if r.Created.After(p.Created) || (r.Created.Equal(p.Created) && r.ID >= p.ID) {
				t.Fatalf("row %d (%s %s) not after row %d (%s %s)", i, r.Created, r.ID, i-1, p.Created, p.ID)
			}
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("%d of %d ids listed", len(seen), len(want))
	}

	// A cursor List did not make is refused, not read as a position.
	for _, c := range []string{"!!", "bm90LWEtY3Vyc29y", CursorOf(Record{ID: "", Created: base}),
		"MjAyNi0wMS0wMVQwMDowMDowMFp8Zy0x" /* "2026-01-01T00:00:00Z|g-1": not the store format */} {
		if _, err := s.List(ctx, ListFilter{Cursor: c}); !errors.Is(err, ErrBadCursor) {
			t.Errorf("cursor %q: err = %v, want ErrBadCursor", c, err)
		}
	}
}

// R55-F13 (review 71b F6): the held caps count live (exp > now) and total
// held rows of one session; issued rows and other sessions do not count.
func TestCountHeldInSession(t *testing.T) {
	db := openTestDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	const sid = "s-22222222222222222222222222222222"
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	add := func(session string, exp time.Time, held bool) {
		rec := testRecord(NewGrantID(), now.Add(-3*time.Hour))
		rec.Session, rec.Exp = session, exp
		if held {
			err = s.InsertHeldTx(ctx, tx, rec)
		} else {
			err = s.InsertActiveTx(ctx, tx, rec)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	add(sid, now.Add(time.Hour), true)
	add(sid, now.Add(time.Hour), true)
	add(sid, now, true) // exp == now: no longer live
	add(sid, now.Add(-time.Hour), true)
	add(sid, now.Add(time.Hour), false)
	add("s-33333333333333333333333333333333", now.Add(time.Hour), true)
	live, err := s.CountHeldLiveInSessionTx(ctx, tx, sid, now)
	if err != nil || live != 2 {
		t.Fatalf("live = %d, %v; want 2", live, err)
	}
	total, err := s.CountHeldInSessionTx(ctx, tx, sid)
	if err != nil || total != 4 {
		t.Fatalf("total = %d, %v; want 4", total, err)
	}
}
