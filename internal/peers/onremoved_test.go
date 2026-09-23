package peers_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestOnRemovedTxRunsInsideRemoval is the 2.2c hook contract: peers.Store's
// OnRemovedTx runs inside the transaction that deletes the peer row, with the
// removed peer's key, so a caller (the daemon) can end everything that
// depended on that peer atomically (Docs/protocol/grant.md, "peers remove of
// the holder revokes all its grants"; Docs/review/28-2.2c-review.md M4).
func TestOnRemovedTxRunsInsideRemoval(t *testing.T) {
	e := newEnv(t, time.Millisecond)
	a, keyA := memberOf(t, "a")
	introduce(t, e, a, "owner-key")

	var got []string
	e.store.OnRemovedTx = func(_ context.Context, tx *sql.Tx, key string) error {
		if tx == nil {
			t.Error("OnRemovedTx called without a transaction")
		}
		got = append(got, key)
		return nil
	}
	if err := e.store.Remove(context.Background(), keyA); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != keyA {
		t.Fatalf("OnRemovedTx called with %v, want [%s]", got, keyA)
	}

	// A failing hook rolls the removal back: the peer is still there.
	b, keyB := memberOf(t, "b")
	introduce(t, e, b, "owner-key")
	e.store.OnRemovedTx = func(context.Context, *sql.Tx, string) error { return errTestHook }
	if err := e.store.Remove(context.Background(), keyB); !errors.Is(err, errTestHook) {
		t.Fatalf("err = %v, want errTestHook", err)
	}
	e.store.OnRemovedTx = nil
	if err := e.store.Remove(context.Background(), keyB); err != nil {
		t.Fatalf("peer was removed despite the failing hook: %v", err)
	}
}

// TestOnRemovedTxRunsOnGC: a team-introduced peer removed by GCIntroduced
// (team left or dissolved) also runs the hook, so its grants and policies end
// too (Docs/review/28-2.2c-review.md M4).
func TestOnRemovedTxRunsOnGC(t *testing.T) {
	e := newEnv(t, time.Millisecond)
	a, keyA := memberOf(t, "a")
	introduce(t, e, a, "owner-key")

	var got []string
	e.store.OnRemovedTx = func(_ context.Context, _ *sql.Tx, key string) error {
		got = append(got, key)
		return nil
	}
	ctx := context.Background()
	tx, err := e.db.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := e.store.GCIntroduced(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || len(got) != 1 || got[0] != keyA {
		t.Fatalf("GC removed %v, hook saw %v, want [%s]", removed, got, keyA)
	}
}

var errTestHook = errTest("onremoved hook failure")

type errTest string

func (e errTest) Error() string { return string(e) }
