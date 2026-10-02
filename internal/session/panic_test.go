package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Review 77b (follow-up to review 77, M2): the IPC server recovers a
// handler's panic, so a panic on a send path must not leave m.mu held.
// Before, sendApp and SendData sealed under a hand-unlocked m.mu, and a
// panic there left the worker blocked and the daemon deaf until restart.
func TestPanicOnSendPathLeavesManagerUsable(t *testing.T) {
	r := &fakeRelay{nodes: map[string]*Manager{}}
	n := newNode(t, r, map[string]bool{})
	m := n.m
	// wedged reports whether mu stays held for a second. It never blocks,
	// and it unlocks a wedged mu so the test's cleanup (Close) still ends.
	wedged := func(mu *sync.Mutex) bool {
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if mu.TryLock() {
				mu.Unlock()
				return false
			}
		}
		mu.Unlock()
		return true
	}
	// A current session with no session behind it makes sealLocked
	// dereference nil under m.mu.
	m.withLock(func() { m.current["ghost"] = "missing" })
	for i, send := range []func() error{
		func() error { return m.SendData(context.Background(), "ghost", []byte(`{"type":"x"}`)) },
		func() error { return m.Send(context.Background(), "ghost", []byte(`{"type":"x"}`)) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("send %d did not panic", i)
				}
			}()
			_ = send()
		}()
		if wedged(&m.mu) || wedged(&m.sendMu) {
			t.Fatalf("send %d: a lock is still held after the recovered panic", i)
		}
	}
	m.withLock(func() { delete(m.current, "ghost") })
	if err := m.SendData(context.Background(), "other", []byte(`{"type":"x"}`)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("after the panics: %v, want ErrNoSession", err)
	}
}
