package approval

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// Review 77, M2: the IPC server recovers a handler's panic and keeps
// serving, so a panic must not leave s.mu held or the only DB connection in
// an open transaction. confirm runs Perform with both.
func TestPanicInPerformLeavesStoreUsable(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s, n, _ := newTestStore(t, clock(&now))
	v, err := s.Create(ctx, KindGrant, "g-1", "s", Action{Perform: func(context.Context, *sql.Tx) (any, error) {
		panic("perform bug") // under s.mu, inside confirm's transaction
	}})
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Perform did not panic")
			}
		}()
		_, _ = s.Confirm(ctx, v.ID, n.lastCode(t))
	}()
	done := make(chan error, 1)
	go func() {
		if _, err := s.List(ctx); err != nil {
			done <- err
			return
		}
		_, err := s.Create(ctx, KindGrant, "g-2", "s", Action{})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the store is wedged after a recovered panic: s.mu or the DB connection is still held")
	}
}
