package approval

import (
	"context"
	"testing"
	"time"
)

// Review 55, R55-030 (C12-01): List kept a rows cursor open on the single
// pooled SQLite connection (store.Open: SetMaxOpenConns(1)) and took s.mu
// for each row through windowState, while Create (and confirm, sweep, ...)
// take s.mu first and then query the DB. In desktop mode the two orders
// deadlocked the whole daemon.
func TestListDoesNotDeadlockWithCreate(t *testing.T) {
	s, _, _, _ := newWindowTestStore(t, nil)
	ctx := context.Background()
	for i := 0; i < MaxPending; i++ {
		if _, err := s.Create(ctx, KindGrant, "g-x", "summary", Action{}); err != nil {
			t.Fatal(err)
		}
	}
	stop := make(chan struct{})
	listed := make(chan struct{})
	created := make(chan struct{})
	go func() {
		defer close(listed)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = s.List(ctx)
		}
	}()
	go func() {
		defer close(created)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = s.Create(ctx, KindGrant, "g-y", "summary", Action{}) // ErrLimit after a DB read under s.mu
		}
	}()
	time.Sleep(2 * time.Second)
	close(stop)
	deadline := time.After(5 * time.Second)
	for _, ch := range []chan struct{}{listed, created} {
		select {
		case <-ch:
		case <-deadline:
			t.Fatal("deadlock: List held the only DB connection while waiting for s.mu, and Create held s.mu while waiting for the connection")
		}
	}
}
