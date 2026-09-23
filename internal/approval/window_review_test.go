package approval

// Review 30 (Docs/review/30-2.2d-review.md) regressions: a Create whose
// window is still in its ready wait must not break other calls, must not
// survive a lockout or Close, and approval_open must never start a second
// window for the same approval. Never a real dialog process.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// gatedRunner is a WindowRunner whose handles block in Ready until release
// is closed, so a test can act while a Create or reopen is mid-wait.
type gatedRunner struct {
	release chan struct{}
	started chan string // id of each Start, as it happens

	mu      sync.Mutex
	handles []*fakeWinHandle
}

func newGatedRunner() *gatedRunner {
	return &gatedRunner{release: make(chan struct{}), started: make(chan string, 16)}
}

type gatedHandle struct {
	*fakeWinHandle
	release chan struct{}
}

func (h gatedHandle) Ready(ctx context.Context) bool {
	select {
	case <-h.release:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *gatedRunner) Start(_ context.Context, id, _, _, _ string, _ time.Time) (WindowHandle, error) {
	h := newFakeWinHandle(true)
	r.mu.Lock()
	r.handles = append(r.handles, h)
	r.mu.Unlock()
	r.started <- id
	return gatedHandle{fakeWinHandle: h, release: r.release}, nil
}

func (r *gatedRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.handles)
}

func (r *gatedRunner) handle(i int) *fakeWinHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handles[i]
}

func newGatedStore(t *testing.T) (*Store, *fakeNotifier, *gatedRunner) {
	t.Helper()
	n := &fakeNotifier{}
	r := newGatedRunner()
	s, err := NewStore(openTestDB(t), &fakeAudit{}, n, r, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s, n, r
}

type createResult struct {
	view View
	err  error
}

func createAsync(s *Store) <-chan createResult {
	out := make(chan createResult, 1)
	go func() {
		v, err := s.Create(context.Background(), KindGrant, "g-1", "s", Action{})
		out <- createResult{v, err}
	}()
	return out
}

// H1: while a Create waits for its window, List (and the sweep every
// Create runs) must not fail on the reserved slot, which has no row yet.
func TestReservedSlotDoesNotBreakListOrSweep(t *testing.T) {
	s, _, r := newGatedStore(t)
	res := createAsync(s)
	<-r.started
	if _, err := s.List(context.Background()); err != nil {
		t.Fatalf("List during a pending ready wait: %v", err)
	}
	if _, err := s.ResolveTag("a-"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("ResolveTag short = %v", err)
	}
	close(r.release)
	if got := <-res; got.err != nil {
		t.Fatalf("Create: %v", got.err)
	}
}

// H1: a lockout during the ready wait drops the reserved slot; Create then
// kills its window, withdraws nothing it showed, and stores no row.
func TestLockoutDuringReadyWaitStoresNothing(t *testing.T) {
	ctx := context.Background()
	s, _, r := newGatedStore(t)
	res := createAsync(s)
	id := <-r.started
	for i := 0; i < MaxWrongPerDay; i++ {
		if _, err := s.settings.recordWrongCode(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	entries := s.dropAllLocked()
	s.mu.Unlock()
	if len(entries) != 0 {
		t.Fatalf("a reserved slot must not be handed to lockAll: %v", entries)
	}
	close(r.release)
	got := <-res
	if !errors.Is(got.err, ErrLocked) {
		t.Fatalf("Create after lockout = %v, want ErrLocked", got.err)
	}
	if r.handle(0).killCount() == 0 {
		t.Fatal("the window of an aborted Create must be killed")
	}
	if _, err := s.Show(ctx, id); !errors.Is(err, ErrUnknown) {
		t.Fatalf("row stored for an aborted Create: %v", err)
	}
}

// H1: Close during the ready wait: nothing is stored, the window dies.
func TestCloseDuringReadyWaitStoresNothing(t *testing.T) {
	s, _, r := newGatedStore(t)
	res := createAsync(s)
	<-r.started
	s.Close()
	close(r.release)
	if got := <-res; got.err == nil {
		t.Fatal("Create finished after Close")
	}
	if r.handle(0).killCount() == 0 {
		t.Fatal("window not killed")
	}
}

// M3: two approval_open calls racing on a closed window start one window.
func TestConcurrentOpenStartsOneWindow(t *testing.T) {
	ctx := context.Background()
	s, _, r := newGatedStore(t)
	res := createAsync(s)
	<-r.started
	close(r.release) // every later Ready returns at once too
	got := <-res
	if got.err != nil {
		t.Fatal(got.err)
	}
	id := got.view.ID
	// Dismiss the first window.
	r.handle(0).answerCh <- fakeWinAnswer{kind: "dismiss"}
	pollUntilStore(t, func() bool { return s.windowState(id) == "closed" })

	// Hold the second Start inside reopen by making its Ready block.
	r.release = make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.OpenWindow(ctx, id)
		}()
	}
	<-r.started
	time.Sleep(50 * time.Millisecond) // give the second OpenWindow its chance
	close(r.release)
	wg.Wait()
	if n := r.count(); n != 2 {
		t.Fatalf("windows started = %d, want 2 (the first plus one reopen)", n)
	}
}
