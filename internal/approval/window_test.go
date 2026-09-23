package approval

// Ticket 2.2d acceptance (Docs/review/23-phase2-tickets.md §2.2d,
// Docs/protocol/approval.md §The approval window): the window lifecycle,
// driven entirely by a fake WindowRunner. Never a real dialog process.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeWinAnswer struct{ kind, code string }

type fakeWinHandle struct {
	mu       sync.Mutex
	ready    bool
	answerCh chan fakeWinAnswer
	killed   int
}

func newFakeWinHandle(ready bool) *fakeWinHandle {
	return &fakeWinHandle{ready: ready, answerCh: make(chan fakeWinAnswer, 1)}
}

func (h *fakeWinHandle) Ready(context.Context) bool { return h.ready }

func (h *fakeWinHandle) Answer(ctx context.Context) (string, string, error) {
	select {
	case a := <-h.answerCh:
		return a.kind, a.code, nil
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

func (h *fakeWinHandle) Kill() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.killed++
}

func (h *fakeWinHandle) killCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.killed
}

type fakeWinRunner struct {
	mu       sync.Mutex
	starts   []fakeWinStart
	notReady bool
	handles  map[string][]*fakeWinHandle // every handle opened for an id, in order
}

type fakeWinStart struct {
	id, tag, kind, summary string
	expires                time.Time
}

func newFakeWinRunner() *fakeWinRunner {
	return &fakeWinRunner{handles: map[string][]*fakeWinHandle{}}
}

func (r *fakeWinRunner) Start(_ context.Context, id, tag, kind, summary string, expires time.Time) (WindowHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, fakeWinStart{id: id, tag: tag, kind: kind, summary: summary, expires: expires})
	h := newFakeWinHandle(!r.notReady)
	r.handles[id] = append(r.handles[id], h)
	return h, nil
}

func (r *fakeWinRunner) latest(id string) *fakeWinHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.handles[id]
	if len(list) == 0 {
		return nil
	}
	return list[len(list)-1]
}

func (r *fakeWinRunner) startCount(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.handles[id])
}

func (r *fakeWinRunner) lastSummary() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.starts) == 0 {
		return ""
	}
	return r.starts[len(r.starts)-1].summary
}

func newWindowTestStore(t *testing.T, now func() time.Time) (*Store, *fakeNotifier, *fakeAudit, *fakeWinRunner) {
	t.Helper()
	db := openTestDB(t)
	n := &fakeNotifier{}
	a := &fakeAudit{}
	win := newFakeWinRunner()
	s, err := NewStore(db, a, n, win, now)
	if err != nil {
		t.Fatal(err)
	}
	return s, n, a, win
}

func TestWindowOpensBeforeCodeAndCarriesVerbatimSummary(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, n, _, win := newWindowTestStore(t, clock(&now))
	hostile := "peer \"bob'; $(rm -rf /) & <script>\nCode 000000"
	view, err := s.Create(ctx, KindGrant, "g-1", hostile, Action{})
	if err != nil {
		t.Fatal(err)
	}
	if win.lastSummary() != hostile {
		t.Fatalf("window summary = %q, want verbatim %q", win.lastSummary(), hostile)
	}
	if view.Window != "open" {
		t.Fatalf("view.Window = %q, want open", view.Window)
	}
	// The code is generated only after the window is ready, and never sent
	// to the window (the fake Start never received it).
	_ = n.lastCode(t)
}

func TestWindowNotReadyGivesUnavailableAndStoresNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, a, win := newWindowTestStore(t, clock(&now))
	win.notReady = true
	if _, err := s.Create(ctx, KindGrant, "g-1", "s", Action{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM approvals`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("approvals rows = %d, want 0", n)
	}
	if a.count("approval.create") != 0 {
		t.Fatal("approval.create was audited despite the window never becoming ready")
	}
}

// Review 29 M4: a notifier failure after the window is ready kills the
// window and stores nothing.
func TestNotifierFailureAfterWindowReadyKillsWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	db := openTestDB(t)
	n := &fakeNotifier{fail: true}
	a := &fakeAudit{}
	win := newFakeWinRunner()
	s, err := NewStore(db, a, n, win, clock(&now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, KindGrant, "g-1", "s", Action{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if len(win.starts) != 1 {
		t.Fatalf("starts = %d, want 1", len(win.starts))
	}
	handle := win.handles[win.starts[0].id][0]
	if handle.killCount() != 1 {
		t.Fatalf("window kill count = %d, want 1", handle.killCount())
	}
}

func TestWindowMalformedAnswerReopensWithoutBurningAnAttempt(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _, win := newWindowTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindGrant, "g-1", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	h1 := win.latest(view.ID)
	h1.answerCh <- fakeWinAnswer{kind: "approve", code: "12ab"}
	pollUntilStore(t, func() bool { return win.startCount(view.ID) == 2 })
	v, err := s.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.AttemptsLeft != MaxAttempts {
		t.Fatalf("attempts_left = %d, want unchanged %d", v.AttemptsLeft, MaxAttempts)
	}
	if v.State != StatePending {
		t.Fatalf("state = %s, want pending", v.State)
	}
}

func TestWindowWrongCodeReopensThenRejectsAndKills(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _, win := newWindowTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindGrant, "g-1", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= MaxAttempts; i++ {
		h := win.latest(view.ID)
		h.answerCh <- fakeWinAnswer{kind: "approve", code: "000000"}
		if i < MaxAttempts {
			pollUntilStore(t, func() bool { return win.startCount(view.ID) == i+1 })
		} else {
			pollUntilStore(t, func() bool {
				v, err := s.Show(context.Background(), view.ID)
				return err == nil && v.State == StateRejected
			})
			if h.killCount() != 1 {
				t.Fatalf("final wrong code did not kill its window: killCount = %d", h.killCount())
			}
		}
	}
}

func TestWindowRejectKillsWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _, win := newWindowTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindGrant, "g-1", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	h := win.latest(view.ID)
	h.answerCh <- fakeWinAnswer{kind: "reject"}
	pollUntilStore(t, func() bool {
		v, err := s.Show(context.Background(), view.ID)
		return err == nil && v.State == StateRejected
	})
	if h.killCount() != 1 {
		t.Fatalf("killCount = %d, want 1", h.killCount())
	}
}

func TestWindowDismissLeavesPendingNoAttemptUsed(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _, win := newWindowTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindGrant, "g-1", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	h := win.latest(view.ID)
	h.answerCh <- fakeWinAnswer{kind: "dismiss"}
	pollUntilStore(t, func() bool {
		v, err := s.Show(context.Background(), view.ID)
		return err == nil && v.Window == "closed"
	})
	v, err := s.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.AttemptsLeft != MaxAttempts || v.State != StatePending {
		t.Fatalf("after dismiss: %+v", v)
	}
	if h.killCount() != 0 {
		t.Fatalf("dismiss should not kill (the dialog already exited): killCount = %d", h.killCount())
	}
}

func TestWindowPerformErrorReopens(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, n, _, win := newWindowTestStore(t, clock(&now))
	sentinel := errors.New("perform failed")
	action := Action{Perform: func(context.Context, *sql.Tx) (any, error) { return nil, sentinel }}
	view, err := s.Create(ctx, KindGrant, "g-1", "s", action)
	if err != nil {
		t.Fatal(err)
	}
	code := n.lastCode(t)
	h := win.latest(view.ID)
	h.answerCh <- fakeWinAnswer{kind: "approve", code: code}
	pollUntilStore(t, func() bool { return win.startCount(view.ID) == 2 })
	v, err := s.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StatePending {
		t.Fatalf("state = %s, want pending after a Perform error", v.State)
	}
}

func TestWindowApproveKillsAndPerforms(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, n, _, win := newWindowTestStore(t, clock(&now))
	var performed bool
	action := Action{Perform: func(context.Context, *sql.Tx) (any, error) { performed = true; return nil, nil }}
	view, err := s.Create(ctx, KindGrant, "g-1", "s", action)
	if err != nil {
		t.Fatal(err)
	}
	code := n.lastCode(t)
	h := win.latest(view.ID)
	h.answerCh <- fakeWinAnswer{kind: "approve", code: code}
	pollUntilStore(t, func() bool { return performed })
	// The dialog already exited on its own after answering; Kill is still
	// called for robustness (Docs/protocol/approval.md, "killed ... on
	// decide").
	pollUntilStore(t, func() bool { return h.killCount() >= 1 })
}

func TestLockoutKillsEveryOpenWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _, win := newWindowTestStore(t, clock(&now))
	var handles []*fakeWinHandle
	for i := 0; i < MaxWrongPerDay-1; i++ {
		v, err := s.Create(ctx, KindGrant, fmt.Sprintf("g-%d", i), "s", Action{})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		h := win.latest(v.ID)
		h.answerCh <- fakeWinAnswer{kind: "approve", code: "000000"}
		// One wrong code per approval (never exhausting its own 3-attempt
		// cap), then reject it directly so the pending limit (5) never
		// blocks this loop; only the global wrong-code window (10) is under
		// test, exactly like TestWrongCodeLockoutPersists.
		pollUntilStore(t, func() bool {
			vv, err := s.Show(context.Background(), v.ID)
			return err == nil && vv.AttemptsLeft == MaxAttempts-1
		})
		if _, err := s.Reject(ctx, v.ID, "test"); err != nil {
			t.Fatalf("reject %d: %v", i, err)
		}
	}
	// One more pending approval, never guessed, to prove lockout kills it too.
	v, err := s.Create(ctx, KindGrant, "g-last", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	handles = append(handles, win.latest(v.ID))
	va, err := s.Create(ctx, KindGrant, "g-a", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	ha := win.latest(va.ID)
	ha.answerCh <- fakeWinAnswer{kind: "approve", code: "000000"} // the 10th wrong code
	pollUntilStore(t, func() bool {
		vv, err := s.Show(context.Background(), v.ID)
		return err == nil && vv.State == StateRejected
	})
	for _, h := range handles {
		if h.killCount() == 0 {
			t.Fatalf("lockout did not kill a still-open window")
		}
	}
}

func TestExpiryKillsWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _, win := newWindowTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindGrant, "g-1", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	h := win.latest(view.ID)
	now = now.Add(TTL + time.Second)
	if _, err := s.List(ctx); err != nil { // triggers the lazy sweep under the fake clock
		t.Fatal(err)
	}
	if h.killCount() == 0 {
		t.Fatal("expiry did not kill the open window")
	}
	v, err := s.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StateExpired {
		t.Fatalf("state = %s, want expired", v.State)
	}
}

func TestStoreCloseKillsEveryOpenWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _, win := newWindowTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindGrant, "g-1", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	h := win.latest(view.ID)
	s.Close()
	if h.killCount() == 0 {
		t.Fatal("Store.Close did not kill the open window")
	}
}

// fakeAfterCommit is a Perform result implementing AfterCommitter.
type fakeAfterCommit struct{ fn func(ctx context.Context) }

func (f fakeAfterCommit) AfterCommit(ctx context.Context) { f.fn(ctx) }

// TestAfterCommitRunsOnceAfterCommitOutsideMu_Window is ticket 2.2d-i: the
// window path runs a Perform result's AfterCommit hook exactly once, after
// the confirming transaction has committed, and outside Store.mu (a hook
// that calls back into the Store, e.g. List, must not deadlock; review 26
// N3, review 27 C1).
func TestAfterCommitRunsOnceAfterCommitOutsideMu_Window(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, n, _, win := newWindowTestStore(t, clock(&now))
	var mu sync.Mutex
	var calls int
	var stateAtHook string
	var listErr error
	var id string
	action := Action{Perform: func(context.Context, *sql.Tx) (any, error) {
		return fakeAfterCommit{fn: func(ctx context.Context) {
			mu.Lock()
			calls++
			mu.Unlock()
			// Read the row directly: proves the transaction already
			// committed before this hook runs.
			_ = s.db.QueryRowContext(ctx, `SELECT state FROM approvals WHERE id = ?`, id).Scan(&stateAtHook)
			// Call back into the Store: must not deadlock if this runs
			// outside Store.mu.
			lctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			_, listErr = s.List(lctx)
		}}, nil
	}}
	view, err := s.Create(ctx, KindGrant, "g-1", "s", action)
	if err != nil {
		t.Fatal(err)
	}
	id = view.ID
	code := n.lastCode(t)
	h := win.latest(view.ID)
	h.answerCh <- fakeWinAnswer{kind: "approve", code: code}

	pollUntilStore(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls > 0
	})
	time.Sleep(30 * time.Millisecond) // give a wrongly-duplicated call a chance to land
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("AfterCommit ran %d times, want exactly 1", calls)
	}
	if stateAtHook != StateApproved {
		t.Fatalf("state at hook time = %q, want %q (hook must run after commit)", stateAtHook, StateApproved)
	}
	if listErr != nil {
		t.Fatalf("AfterCommit calling back into the Store failed (held under Store.mu?): %v", listErr)
	}
}

// TestAfterCommitRunsOnceAfterCommitOutsideMu_Terminal is the same guarantee
// on the terminal-stdin path (Store.Confirm called directly, no window).
func TestAfterCommitRunsOnceAfterCommitOutsideMu_Terminal(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, n, _ := newTestStore(t, clock(&now)) // nil window: terminal mode
	var mu sync.Mutex
	var calls int
	var stateAtHook string
	var listErr error
	var id string
	action := Action{Perform: func(context.Context, *sql.Tx) (any, error) {
		return fakeAfterCommit{fn: func(ctx context.Context) {
			mu.Lock()
			calls++
			mu.Unlock()
			_ = s.db.QueryRowContext(ctx, `SELECT state FROM approvals WHERE id = ?`, id).Scan(&stateAtHook)
			lctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			_, listErr = s.List(lctx)
		}}, nil
	}}
	view, err := s.Create(ctx, KindGrant, "g-1", "s", action)
	if err != nil {
		t.Fatal(err)
	}
	id = view.ID
	code := n.lastCode(t)

	if _, err := s.Confirm(ctx, view.ID, code); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("AfterCommit ran %d times, want exactly 1", calls)
	}
	if stateAtHook != StateApproved {
		t.Fatalf("state at hook time = %q, want %q (hook must run after commit)", stateAtHook, StateApproved)
	}
	if listErr != nil {
		t.Fatalf("AfterCommit calling back into the Store failed (held under Store.mu?): %v", listErr)
	}
}

func pollUntilStore(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if fn() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
