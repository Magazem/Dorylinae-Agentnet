package approval

import (
	"context"
	"database/sql"
	"sync"
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

// panicNotifier panics in Show while panics is set, as a buggy notifier
// would, after the window is ready and the slot reserved.
type panicNotifier struct {
	*fakeNotifier
	mu     sync.Mutex
	panics bool
}

func (p *panicNotifier) Show(ctx context.Context, id string, expires time.Time, title, body string) error {
	p.mu.Lock()
	panics := p.panics
	p.mu.Unlock()
	if panics {
		panic("notifier bug")
	}
	return p.fakeNotifier.Show(ctx, id, expires, title, body)
}

// Review 77b, R2: a panic between Create's reservation and its
// finalisation, recovered by the IPC server, releases the reserved slot and
// closes the window. MaxPending of them used to refuse every Create with
// ErrLimit until a restart.
func TestPanicInCreateReleasesReservation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	n := &panicNotifier{fakeNotifier: &fakeNotifier{}, panics: true}
	win := newFakeWinRunner()
	s, err := NewStore(openTestDB(t), &fakeAudit{}, n, win, clock(&now))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxPending; i++ {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("Create %d did not panic", i)
				}
			}()
			_, _ = s.Create(ctx, KindGrant, "g", "s", Action{})
		}()
	}
	win.mu.Lock()
	for id, hs := range win.handles {
		for _, h := range hs {
			if h.killCount() == 0 {
				t.Errorf("the window of %s was left open after the panic", id)
			}
		}
	}
	opened := len(win.handles)
	win.mu.Unlock()
	if opened != MaxPending {
		t.Fatalf("%d windows opened, want %d", opened, MaxPending)
	}
	n.fakeNotifier.mu.Lock()
	removed := len(n.remove)
	n.fakeNotifier.mu.Unlock()
	if removed != MaxPending {
		t.Errorf("%d notifications withdrawn, want %d", removed, MaxPending)
	}
	n.mu.Lock()
	n.panics = false
	n.mu.Unlock()
	v, err := s.Create(ctx, KindGrant, "g-ok", "s", Action{})
	if err != nil {
		t.Fatalf("Create after %d recovered panics: %v", MaxPending, err)
	}
	// A finalised approval is unaffected by the cleanup path.
	views, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].ID != v.ID {
		t.Fatalf("List = %+v, want only %s", views, v.ID)
	}
}
