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
	ar, err := s.ActivateTx(ctx, tx, rec.ID, now)
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
	if _, err := s.ActivateTx(ctx, tx2, rec.ID, now); err == nil {
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
	if _, err := s.ActivateTx(ctx, tx0, active.ID, now); err != nil {
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
	ids, err := s.RevokeForPeer(ctx, "peer-x", ReasonPeerRemoved, now)
	if err != nil || len(ids) != 1 || ids[0] != a.ID {
		t.Fatalf("RevokeForPeer = %v, %v", ids, err)
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
	baseParams := MatchParams{Peer: "peer-a", Action: ActionFSRead, Path: "/srv/repo", Scope: "internal/mail/inbox", ExpiresS: 3600, Now: now}
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
	otherBranch := MatchParams{Peer: "peer-a", Action: ActionGitRead, Path: "/srv/repo", Branch: "feature", ExpiresS: 3600, Now: now}
	if match(otherBranch) {
		t.Error("a different branch should not match")
	}
	sameBranch := otherBranch
	sameBranch.Branch = "main"
	if !match(sameBranch) {
		t.Error("the same branch should match")
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
	pastUntil := MatchParams{Peer: "peer-a", Action: ActionFSRead, Path: "/srv/old", ExpiresS: 3600, Now: now}
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
