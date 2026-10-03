package approval

// R55-F31: the approval store's audit rows against a real audit log, with a
// TEMP trigger making one action's insert fail (the store has one connection,
// so the trigger sees every insert). T4 (approval.create is an S row), T5.1 and
// T5.3 (reject and bad_code are S- rows) and T7 (who rejected, and why).

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
)

type realAuditEnv struct {
	s   *Store
	n   *fakeNotifier
	db  *sql.DB
	log *bytes.Buffer
	now time.Time
}

func newRealAuditEnv(t *testing.T) *realAuditEnv {
	t.Helper()
	db := openTestDB(t)
	e := &realAuditEnv{db: db, n: &fakeNotifier{}, log: &bytes.Buffer{}, now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	audit.SetErrorLog(slog.New(slog.NewTextHandler(e.log, nil)))
	t.Cleanup(func() { audit.SetErrorLog(nil) })
	s, err := NewStore(db, audit.New(db), e.n, nil, clock(&e.now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	e.s = s
	return e
}

func (e *realAuditEnv) inject(t *testing.T, action string) {
	t.Helper()
	if _, err := e.db.Exec(fmt.Sprintf(`CREATE TEMP TRIGGER audit_fail BEFORE INSERT ON main.audit_events
WHEN NEW.action = '%s' BEGIN SELECT RAISE(ABORT, 'injected'); END`, action)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM sqlite_temp_master WHERE name = 'audit_fail'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the audit_fail trigger is gone (%d, %v)", n, err)
	}
}

func (e *realAuditEnv) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// T4: approval.create is an S row. If it fails, no approval exists: the row, the
// slot and the notification are cleaned up as after a failed insert.
func TestCreateAuditFailureStoresNothing(t *testing.T) {
	e := newRealAuditEnv(t)
	e.inject(t, "approval.create")
	_, err := e.s.Create(context.Background(), KindGrant, "g-1", "s", Action{})
	var we *audit.WriteError
	if !errors.As(err, &we) || we.Action != "approval.create" {
		t.Fatalf("Create = %v, want an *audit.WriteError for approval.create", err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM approvals`); n != 0 {
		t.Fatalf("%d approvals rows after a failed row", n)
	}
	e.n.mu.Lock()
	removed := len(e.n.remove)
	e.n.mu.Unlock()
	if removed != 1 {
		t.Fatalf("notifications withdrawn = %d, want 1", removed)
	}
	if got, _ := e.s.List(context.Background()); len(got) != 0 {
		t.Fatalf("List = %+v, want none", got)
	}
}

// T5.1: an expiry still marks the row expired and runs OnReject when its
// approval.reject row fails; the failure is logged once.
func TestExpiryCommitsWhenItsRowFails(t *testing.T) {
	ctx := context.Background()
	e := newRealAuditEnv(t)
	var rejected int32
	v, err := e.s.Create(ctx, KindGrant, "g-1", "s", Action{OnReject: func(context.Context) { atomic.AddInt32(&rejected, 1) }})
	if err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(TTL + time.Minute)
	e.inject(t, "approval.reject")
	if _, err := e.s.Confirm(ctx, v.ID, "000000"); !errors.Is(err, ErrExpired) {
		t.Fatalf("Confirm = %v, want ErrExpired", err)
	}
	var state string
	if err := e.db.QueryRow(`SELECT state FROM approvals WHERE id = ?`, v.ID).Scan(&state); err != nil || state != StateExpired {
		t.Fatalf("state %q (%v), want expired", state, err)
	}
	if atomic.LoadInt32(&rejected) != 1 {
		t.Fatalf("OnReject ran %d times", rejected)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM audit_events WHERE action = 'approval.reject'`); n != 0 {
		t.Fatalf("%d approval.reject rows, want none", n)
	}
	if out := e.log.String(); strings.Count(out, "event=audit_error") != 1 || !strings.Contains(out, "action=approval.reject") || strings.Contains(out, v.ID) {
		t.Fatalf("central log: %q", out)
	}
}

// T5.3: a wrong code is still counted when its approval.bad_code row fails.
func TestWrongCodeCountedWhenItsRowFails(t *testing.T) {
	ctx := context.Background()
	e := newRealAuditEnv(t)
	v, err := e.s.Create(ctx, KindGrant, "g-1", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	e.inject(t, "approval.bad_code")
	_, err = e.s.Confirm(ctx, v.ID, "000000")
	var bce *BadCodeError
	if !errors.As(err, &bce) || bce.AttemptsLeft != MaxAttempts-1 {
		t.Fatalf("Confirm = %v, want a BadCodeError with %d attempts left", err, MaxAttempts-1)
	}
	var attempts int
	if err := e.db.QueryRow(`SELECT attempts FROM approvals WHERE id = ?`, v.ID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("attempts %d (%v), want 1", attempts, err)
	}
	if !strings.Contains(e.log.String(), "action=approval.bad_code") {
		t.Fatalf("no audit_error line: %q", e.log.String())
	}
}

// T7 (R55-123): a daemon-caused reject names the reason, actor daemon, no via;
// a human reject through IPC says reason user, via ipc.
func TestRejectForNamesTheReason(t *testing.T) {
	ctx := context.Background()
	e := newRealAuditEnv(t)
	v1, err := e.s.Create(ctx, KindDeviceLink, "p-1", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.RejectFor(ctx, v1.ID, "superseded"); err != nil {
		t.Fatal(err)
	}
	var actor, detail string
	if err := e.db.QueryRow(`SELECT actor, detail FROM audit_events WHERE action = 'approval.reject' AND instr(detail, ?) > 0`, v1.ID).Scan(&actor, &detail); err != nil {
		t.Fatal(err)
	}
	if actor != "daemon" || !strings.Contains(detail, `"reason":"superseded"`) || strings.Contains(detail, `"via"`) {
		t.Fatalf("daemon reject row: actor %s detail %s", actor, detail)
	}
	v2, err := e.s.Create(ctx, KindGrant, "g-2", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Reject(ctx, v2.ID, "ipc"); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(`SELECT detail FROM audit_events WHERE action = 'approval.reject' AND instr(detail, ?) > 0`, v2.ID).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, `"reason":"user"`) || !strings.Contains(detail, `"via":"ipc"`) {
		t.Fatalf("human reject row: %s", detail)
	}
	if _, err := e.s.RejectFor(ctx, v2.ID, "free text"); err == nil {
		t.Fatal("RejectFor accepted a reason outside the fixed set")
	}
}

// R55-125: Check results are cached, status never waits for one, and a missing
// window names its fix in approval_unavailable (UnavailableError).
func TestWindowCheckCacheAndUnavailableFix(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := newFakeWinRunner()
	s, err := NewStore(db, &fakeAudit{}, &fakeNotifier{}, r, clock(&now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.CheckWindow(context.Background())
	if ok, fix, has := s.WindowStatus(); !has || !ok || fix != "" {
		t.Fatalf("WindowStatus = %v %q %v, want ok", ok, fix, has)
	}
	r.mu.Lock()
	r.missing, r.fix, r.notReady = true, "install zenity (or kdialog)", true
	r.mu.Unlock()
	// Within the TTL the old value stands and nothing is re-checked.
	if ok, _, _ := s.WindowStatus(); !ok {
		t.Fatal("the cached value changed inside its TTL")
	}
	now = now.Add(WindowCheckTTL + time.Second)
	// Stale: the call returns the cached value at once and refreshes in the background.
	if ok, _, _ := s.WindowStatus(); !ok {
		t.Fatal("WindowStatus waited for a fresh check")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ok, fix, _ := s.WindowStatus(); !ok {
			if fix != "install zenity (or kdialog)" {
				t.Fatalf("fix %q", fix)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background refresh never landed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// An opening that fails names the fix (and is still ErrUnavailable).
	_, err = s.Create(context.Background(), KindGrant, "g-1", "s", Action{})
	var ue *UnavailableError
	if !errors.Is(err, ErrUnavailable) || !errors.As(err, &ue) || !ue.Window || ue.Fix != "install zenity (or kdialog)" {
		t.Fatalf("Create = %v, want an UnavailableError naming the fix", err)
	}
	// Terminal mode has no window to check.
	ts, err := NewStore(db, &fakeAudit{}, &fakeNotifier{}, nil, clock(&now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ts.Close)
	if _, _, has := ts.WindowStatus(); has {
		t.Fatal("terminal mode reports a window")
	}
}
