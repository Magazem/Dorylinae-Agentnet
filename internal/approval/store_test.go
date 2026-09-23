package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// fakeNotifier records every Show/Remove call and can be made to fail.
type fakeNotifier struct {
	mu     sync.Mutex
	shows  []fakeShow
	remove []string
	fail   bool
}

type fakeShow struct {
	id, title, body string
	expires         time.Time
}

func (f *fakeNotifier) Show(_ context.Context, id string, expires time.Time, title, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("fake notifier failure")
	}
	f.shows = append(f.shows, fakeShow{id: id, title: title, body: body, expires: expires})
	return nil
}

func (f *fakeNotifier) Remove(_ context.Context, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.remove = append(f.remove, id)
}

func (f *fakeNotifier) lastCode(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.shows) == 0 {
		t.Fatal("no notification shown")
	}
	body := f.shows[len(f.shows)-1].body
	// body ends with " Code XXXXXX"
	return body[len(body)-6:]
}

type fakeAudit struct {
	mu     sync.Mutex
	events []fakeEvent
}

type fakeEvent struct {
	actor, action string
	detail        any
}

func (f *fakeAudit) Append(_ context.Context, actor, action string, detail any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeEvent{actor: actor, action: action, detail: detail})
	return nil
}

func (f *fakeAudit) count(action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.events {
		if e.action == action {
			n++
		}
	}
	return n
}

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

func newTestStore(t *testing.T, now func() time.Time) (*Store, *fakeNotifier, *fakeAudit) {
	t.Helper()
	db := openTestDB(t)
	n := &fakeNotifier{}
	a := &fakeAudit{}
	s, err := NewStore(db, a, n, now)
	if err != nil {
		t.Fatal(err)
	}
	return s, n, a
}

func clock(t *time.Time) func() time.Time {
	return func() time.Time { return *t }
}

func TestCreateAndConfirmRunsAction(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s, notifier, audit := newTestStore(t, clock(&now))

	var performed bool
	action := Action{
		Perform: func(_ context.Context, tx *sql.Tx) (any, error) {
			performed = true
			if tx == nil {
				t.Fatal("expected a non-nil tx")
			}
			return map[string]string{"status": "ok"}, nil
		},
	}
	view, err := s.Create(ctx, KindGrant, "g-1", "approve grant git.read to bob for 2h?", action)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != StatePending || view.AttemptsLeft != MaxAttempts {
		t.Fatalf("view = %+v", view)
	}
	code := notifier.lastCode(t)

	res, err := s.Confirm(ctx, view.ID, code)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !performed {
		t.Fatal("action.Perform was not called")
	}
	if m, ok := res.(map[string]string); !ok || m["status"] != "ok" {
		t.Fatalf("result = %+v", res)
	}
	shown, err := s.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shown.State != StateApproved {
		t.Fatalf("state = %s, want approved", shown.State)
	}
	if audit.count("approval.create") != 1 || audit.count("approval.approve") != 1 {
		t.Fatalf("audit events: %+v", audit.events)
	}
	if len(notifier.remove) != 1 || notifier.remove[0] != view.ID {
		t.Fatalf("notifier.remove = %v", notifier.remove)
	}
}

func TestConfirmWrongCodeThreeTimesRejects(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, audit := newTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindRelease, "s-1", "release quarantined result?", Action{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		_, err := s.Confirm(ctx, view.ID, "000000")
		var bce *BadCodeError
		if !errors.As(err, &bce) {
			t.Fatalf("attempt %d: err = %v, want *BadCodeError", i, err)
		}
		wantLeft := MaxAttempts - i
		if wantLeft < 0 {
			wantLeft = 0
		}
		if bce.AttemptsLeft != wantLeft {
			t.Fatalf("attempt %d: attempts_left = %d, want %d", i, bce.AttemptsLeft, wantLeft)
		}
	}
	shown, err := s.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shown.State != StateRejected {
		t.Fatalf("state = %s, want rejected", shown.State)
	}
	// A 4th attempt now finds nothing pending.
	if _, err := s.Confirm(ctx, view.ID, "000000"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("4th attempt err = %v, want ErrUnknown", err)
	}
	if audit.count("approval.reject") != 1 {
		t.Fatalf("reject audits = %d", audit.count("approval.reject"))
	}
}

func TestConfirmExpired(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, notifier, _ := newTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindGrant, "g-1", "summary", Action{})
	if err != nil {
		t.Fatal(err)
	}
	code := notifier.lastCode(t)
	now = now.Add(TTL + time.Second)
	if _, err := s.Confirm(ctx, view.ID, code); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
	shown, err := s.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shown.State != StateExpired {
		t.Fatalf("state = %s, want expired", shown.State)
	}
}

func TestRestartExpiresPending(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	db := openTestDB(t)
	n := &fakeNotifier{}
	a := &fakeAudit{}
	s1, err := NewStore(db, a, n, clock(&now))
	if err != nil {
		t.Fatal(err)
	}
	view, err := s1.Create(ctx, KindGrant, "g-1", "summary", Action{})
	if err != nil {
		t.Fatal(err)
	}
	code := n.lastCode(t)

	// Simulate a restart: a fresh Store over the same db has a new approval_key
	// and an empty in-memory map (Docs/protocol/approval.md §Object).
	s2, err := NewStore(db, a, n, clock(&now))
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.ExpireStale(ctx); err != nil {
		t.Fatal(err)
	}
	shown, err := s2.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shown.State != StateExpired {
		t.Fatalf("state = %s, want expired", shown.State)
	}
	if _, err := s2.Confirm(ctx, view.ID, code); !errors.Is(err, ErrExpired) {
		t.Fatalf("confirm after restart: err = %v, want ErrExpired", err)
	}
}

func TestPendingLimit(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _ := newTestStore(t, clock(&now))
	for i := 0; i < MaxPending; i++ {
		if _, err := s.Create(ctx, KindGrant, fmt.Sprintf("g-%d", i), "s", Action{}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := s.Create(ctx, KindGrant, "g-over", "s", Action{}); !errors.Is(err, ErrLimit) {
		t.Fatalf("6th create: err = %v, want ErrLimit", err)
	}
}

func TestHourlyLimit(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _ := newTestStore(t, clock(&now))
	for i := 0; i < MaxPerHour; i++ {
		v, err := s.Create(ctx, KindGrant, fmt.Sprintf("g-%d", i), "s", Action{})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		// Reject immediately so the pending limit (5) never blocks this loop;
		// only the hourly created-count limit (20) is under test.
		if _, err := s.Reject(ctx, v.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(ctx, KindGrant, "g-over", "s", Action{}); !errors.Is(err, ErrLimit) {
		t.Fatalf("21st create: err = %v, want ErrLimit", err)
	}
}

func TestNotifierFailureLeavesNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	db := openTestDB(t)
	n := &fakeNotifier{fail: true}
	a := &fakeAudit{}
	s, err := NewStore(db, a, n, clock(&now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, KindGrant, "g-1", "s", Action{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	var n2 int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM approvals`).Scan(&n2); err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("approvals rows = %d, want 0", n2)
	}
	if a.count("approval.create") != 0 {
		t.Fatalf("approval.create was audited despite the notifier failing")
	}
}

func TestWrongCodeLockoutPersists(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	db := openTestDB(t)
	n := &fakeNotifier{}
	a := &fakeAudit{}
	s, err := NewStore(db, a, n, clock(&now))
	if err != nil {
		t.Fatal(err)
	}
	// Create MaxWrongPerDay approvals (one per wrong code, so the per-approval
	// 3-attempt cap never interferes) and guess each wrong exactly once,
	// rejecting each right afterwards so the pending limit (5) never blocks
	// this loop; only the global wrong-code window (10) is under test. The
	// last one is left pending so the 10th wrong code's lockAll is the thing
	// that rejects it.
	var lastID string
	for i := 0; i < MaxWrongPerDay; i++ {
		v, err := s.Create(ctx, KindGrant, fmt.Sprintf("g-%d", i), "s", Action{})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		lastID = v.ID
		if _, err := s.Confirm(ctx, v.ID, "000000"); err == nil {
			t.Fatalf("guess %d unexpectedly succeeded", i)
		}
		if i < MaxWrongPerDay-1 {
			if _, err := s.Reject(ctx, v.ID); err != nil {
				t.Fatalf("reject %d: %v", i, err)
			}
		}
	}
	// Every still-pending approval (including one never guessed wrong) is now rejected.
	shown, err := s.Show(ctx, lastID)
	if err != nil {
		t.Fatal(err)
	}
	if shown.State != StateRejected {
		t.Fatalf("state = %s, want rejected (locked)", shown.State)
	}
	if _, err := s.Create(ctx, KindGrant, "g-locked", "s", Action{}); !errors.Is(err, ErrLocked) {
		t.Fatalf("create while locked: err = %v, want ErrLocked", err)
	}
	// A fresh Store over the same db (simulating a restart) is still locked:
	// the wrong-code window is persisted in settings, unlike the approval_key.
	s2, err := NewStore(db, a, n, clock(&now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Create(ctx, KindGrant, "g-locked-2", "s", Action{}); !errors.Is(err, ErrLocked) {
		t.Fatalf("create after restart while locked: err = %v, want ErrLocked", err)
	}
}

func TestPreconditionFailureDropsActionAndPassesErrorThrough(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, notifier, _ := newTestStore(t, clock(&now))
	sentinel := errors.New("session closed meanwhile")
	var performed bool
	action := Action{
		Precondition: func(context.Context, *sql.Tx) error { return sentinel },
		Perform: func(context.Context, *sql.Tx) (any, error) {
			performed = true
			return nil, nil
		},
	}
	view, err := s.Create(ctx, KindGrant, "g-1", "s", action)
	if err != nil {
		t.Fatal(err)
	}
	code := notifier.lastCode(t)
	_, err = s.Confirm(ctx, view.ID, code)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the precondition's own error unchanged", err)
	}
	if performed {
		t.Fatal("Perform ran despite a failed precondition")
	}
	shown, err := s.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shown.State != StateRejected {
		t.Fatalf("state = %s, want rejected", shown.State)
	}
}

func TestPerformFailureRollsBackAndLeavesApprovalPending(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, notifier, _ := newTestStore(t, clock(&now))
	sentinel := errors.New("perform failed")
	action := Action{
		Perform: func(context.Context, *sql.Tx) (any, error) { return nil, sentinel },
	}
	view, err := s.Create(ctx, KindGrant, "g-1", "s", action)
	if err != nil {
		t.Fatal(err)
	}
	code := notifier.lastCode(t)
	if _, err := s.Confirm(ctx, view.ID, code); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	shown, err := s.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shown.State != StatePending {
		t.Fatalf("state = %s, want pending (Perform's failure rolled back the approval too)", shown.State)
	}
}

func TestReject(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, audit := newTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindGrant, "g-1", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	rv, err := s.Reject(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rv.State != StateRejected {
		t.Fatalf("state = %s, want rejected", rv.State)
	}
	if audit.count("approval.reject") != 1 {
		t.Fatalf("reject audits = %d", audit.count("approval.reject"))
	}
}

func TestListNeverIncludesCode(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, notifier, _ := newTestStore(t, clock(&now))
	if _, err := s.Create(ctx, KindGrant, "g-1", "approve grant to bob", Action{}); err != nil {
		t.Fatal(err)
	}
	code := notifier.lastCode(t)
	views, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("views = %+v", views)
	}
	raw := fmt.Sprintf("%+v", views[0])
	if containsCode(raw, code) {
		t.Fatalf("view leaks the code: %s", raw)
	}
}

func containsCode(s, code string) bool {
	for i := 0; i+len(code) <= len(s); i++ {
		if s[i:i+len(code)] == code {
			return true
		}
	}
	return false
}
