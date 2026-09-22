package peers_test

import (
	"context"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

func memberOf(t *testing.T, name string) (peers.Member, string) {
	t.Helper()
	raw, key := signedCard(t, name)
	sc, err := agentcard.Verify(raw)
	if err != nil {
		t.Fatal(err)
	}
	return peers.Member{Card: sc, Raw: raw}, key
}

func introduce(t *testing.T, e *env, m peers.Member, owner string) {
	t.Helper()
	ctx := context.Background()
	tx, err := e.db.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Introduce(ctx, tx, m, owner, time.Now()); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func gc(t *testing.T, e *env) []peers.Removed {
	t.Helper()
	ctx := context.Background()
	tx, err := e.db.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.store.GCIntroduced(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return out
}

func peerByKey(t *testing.T, e *env, key string) *peers.Peer {
	t.Helper()
	for _, p := range e.peerList(t) {
		if p.PublicKey == key {
			return &p
		}
	}
	return nil
}

func TestIntroduceInsertsTeamPeer(t *testing.T) {
	e := newEnv(t, time.Millisecond)
	m, key := memberOf(t, "m")
	introduce(t, e, m, "owner-key")
	p := peerByKey(t, e, key)
	if p == nil || p.Trust != peers.TrustTeam || p.IntroducedBy == nil || *p.IntroducedBy != "owner-key" {
		t.Fatalf("peer = %+v", p)
	}
}

func TestIntroduceKeepsDirectlyPairedPeer(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, time.Millisecond)
	m, key := memberOf(t, "direct")
	if err := e.store.AddTrusted(ctx, m.Card, m.Raw, time.Now(), peers.TrustCode, nil); err != nil {
		t.Fatal(err)
	}
	m.Card.Card.Name = "renamed by roster"
	introduce(t, e, m, "owner-key")
	p := peerByKey(t, e, key)
	if p.Name != "direct" || p.Trust != peers.TrustCode || p.IntroducedBy != nil {
		t.Fatalf("directly paired peer changed: %+v", p)
	}
}

func TestIntroduceRefreshesIntroducedPeer(t *testing.T) {
	e := newEnv(t, time.Millisecond)
	m, key := memberOf(t, "old")
	introduce(t, e, m, "owner-key")
	m.Card.Card.Name = "new"
	introduce(t, e, m, "other-owner")
	p := peerByKey(t, e, key)
	if p.Name != "new" || p.Trust != peers.TrustTeam || *p.IntroducedBy != "owner-key" {
		t.Fatalf("peer = %+v", p)
	}
}

func TestTeamTrustRank(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, time.Millisecond)
	m, key := memberOf(t, "m")
	introduce(t, e, m, "owner-key")
	// A v1 (relay) re-pair never lowers team.
	if err := e.store.Add(ctx, m.Card, m.Raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	if p := peerByKey(t, e, key); p.Trust != peers.TrustTeam {
		t.Fatalf("trust after relay re-add = %q, want team", p.Trust)
	}
	// SetTrust(relay) is a no-op and keeps introduced_by.
	m2, key2 := memberOf(t, "m2")
	introduce(t, e, m2, "owner-key")
	if err := e.store.SetTrust(ctx, key2, peers.TrustRelay); err != nil {
		t.Fatal(err)
	}
	if p := peerByKey(t, e, key2); p.Trust != peers.TrustTeam || p.IntroducedBy == nil {
		t.Fatalf("after SetTrust(relay): %+v", p)
	}
}

func TestV2RepairRaisesTeamToCodeAndClearsIntroducedBy(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, time.Millisecond)
	m, key := memberOf(t, "m")
	introduce(t, e, m, "owner-key")
	if err := e.store.AddTrusted(ctx, m.Card, m.Raw, time.Now(), peers.TrustCode, nil); err != nil {
		t.Fatal(err)
	}
	if p := peerByKey(t, e, key); p.Trust != peers.TrustCode || p.IntroducedBy != nil {
		t.Fatalf("after v2 re-pair: %+v", p)
	}
	if n := len(gc(t, e)); n != 0 {
		t.Fatalf("GC removed %d directly paired peers", n)
	}
}

func TestVerifyClearsIntroducedBy(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, time.Millisecond)
	m, key := memberOf(t, "m")
	introduce(t, e, m, "owner-key")
	if err := e.store.SetTrust(ctx, key, peers.TrustFingerprint); err != nil {
		t.Fatal(err)
	}
	if p := peerByKey(t, e, key); p.Trust != peers.TrustFingerprint || p.IntroducedBy != nil {
		t.Fatalf("after verify: %+v", p)
	}
}

func TestGCIntroducedSemantics(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, time.Millisecond)
	db := e.db.DB()
	// The teams tables arrive with migration 9 (1.1b); create them here.
	for _, q := range []string{
		`CREATE TABLE teams (id TEXT PRIMARY KEY, name TEXT NOT NULL, owner TEXT NOT NULL, epoch INTEGER NOT NULL,
			state TEXT NOT NULL, created TEXT NOT NULL, updated TEXT NOT NULL)`,
		`CREATE TABLE team_members (team_id TEXT NOT NULL, key TEXT NOT NULL, added TEXT NOT NULL, PRIMARY KEY (team_id, key))`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	mActive, kActive := memberOf(t, "active")
	mLeft, kLeft := memberOf(t, "left")
	mNone, kNone := memberOf(t, "none")
	mDirect, kDirect := memberOf(t, "direct")
	for _, m := range []peers.Member{mActive, mLeft, mNone} {
		introduce(t, e, m, "owner-key")
	}
	if err := e.store.AddTrusted(ctx, mDirect.Card, mDirect.Raw, time.Now(), peers.TrustCode, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO teams VALUES ('t1', 'a', 'o', 1, 'active', 'x', 'x')`,
		`INSERT INTO teams VALUES ('t2', 'b', 'o', 1, 'left', 'x', 'x')`,
		`INSERT INTO team_members VALUES ('t1', '` + kActive + `', 'x')`,
		`INSERT INTO team_members VALUES ('t2', '` + kLeft + `', 'x')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// Waiting outbox rows to the peers about to go, and one to a peer that stays.
	for _, r := range []struct{ id, to, state string }{
		{"o1", kNone, "queued"}, {"o2", kLeft, "relayed"}, {"o3", kNone, "delivered"}, {"o4", kActive, "queued"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO outbox (id, to_key, kind, created, state, updated) VALUES (?, ?, 'k', 'x', ?, 'x')`,
			r.id, r.to, r.state); err != nil {
			t.Fatal(err)
		}
	}
	removed := gc(t, e)
	got := map[string]bool{}
	for _, r := range removed {
		got[r.PublicKey] = true
		if r.Name == "" || r.Fingerprint == "" {
			t.Errorf("removed entry incomplete: %+v", r)
		}
	}
	if len(removed) != 2 || !got[kLeft] || !got[kNone] {
		t.Fatalf("removed = %+v, want exactly the left-team and teamless peers", removed)
	}
	if peerByKey(t, e, kActive) == nil || peerByKey(t, e, kDirect) == nil {
		t.Fatal("GC removed an active-team member or a directly paired peer")
	}
	states := map[string]string{}
	rows, err := db.QueryContext(ctx, `SELECT id, state || ':' || COALESCE(error, '') FROM outbox`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, s string
		if err := rows.Scan(&id, &s); err != nil {
			t.Fatal(err)
		}
		states[id] = s
	}
	want := map[string]string{"o1": "failed:unpaired", "o2": "failed:unpaired", "o3": "delivered:", "o4": "queued:"}
	for id, w := range want {
		if states[id] != w {
			t.Errorf("outbox %s = %q, want %q", id, states[id], w)
		}
	}
}

func TestGCIntroducedWithoutTeamTables(t *testing.T) {
	e := newEnv(t, time.Millisecond)
	m, key := memberOf(t, "m")
	introduce(t, e, m, "owner-key")
	if n := len(gc(t, e)); n != 1 || peerByKey(t, e, key) != nil {
		t.Fatalf("with no team tables an introduced peer must be collected (removed %d)", n)
	}
}
