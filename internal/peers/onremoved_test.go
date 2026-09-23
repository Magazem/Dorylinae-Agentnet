package peers_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestOnRemovedFiresAfterCommit is the 2.2c hook contract: peers.Store's
// OnRemoved runs once Remove's own transaction has committed, with the
// removed peer's key, so a caller (the daemon) can end everything that
// depended on that peer (Docs/protocol/grant.md, "peers remove of the
// holder revokes all its grants").
func TestOnRemovedFiresAfterCommit(t *testing.T) {
	e := newEnv(t, time.Millisecond)
	a, keyA := memberOf(t, "a")
	introduce(t, e, a, "owner-key")

	var got []string
	e.store.OnRemoved = func(_ context.Context, key string) error {
		got = append(got, key)
		return nil
	}
	if err := e.store.Remove(context.Background(), keyA); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != keyA {
		t.Fatalf("OnRemoved called with %v, want [%s]", got, keyA)
	}

	// A failing OnRemoved is surfaced to the caller, but the removal itself
	// already committed: a retry of Remove sees ErrNoPeer, not a second
	// removal attempt.
	b, keyB := memberOf(t, "b")
	introduce(t, e, b, "owner-key")
	e.store.OnRemoved = func(context.Context, string) error { return errTestHook }
	if err := e.store.Remove(context.Background(), keyB); !errors.Is(err, errTestHook) {
		t.Fatalf("err = %v, want errTestHook", err)
	}
}

var errTestHook = errTest("onremoved hook failure")

type errTest string

func (e errTest) Error() string { return string(e) }
