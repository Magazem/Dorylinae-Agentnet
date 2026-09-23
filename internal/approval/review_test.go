package approval

// Regression tests for the findings of Docs/review/26-2.2a-review.md.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// M-1: an approval that expires without anyone calling Confirm must stop
// counting toward MaxPending and must leave approval_list.
func TestExpiredPendingFreesSlotAndLeavesList(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, n, a := newTestStore(t, clock(&now))
	var old []string
	for i := 0; i < MaxPending; i++ {
		v, err := s.Create(ctx, KindGrant, fmt.Sprintf("g-%d", i), "s", Action{})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		old = append(old, v.ID)
	}
	now = now.Add(TTL + time.Second)

	views, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 0 {
		t.Fatalf("list after expiry = %+v, want none", views)
	}
	if _, err := s.Create(ctx, KindGrant, "g-new", "s", Action{}); err != nil {
		t.Fatalf("create after all pending expired: %v", err)
	}
	for _, id := range old {
		v, err := s.Show(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != StateExpired {
			t.Fatalf("%s state = %s, want expired", id, v.State)
		}
	}
	if got := a.count("approval.reject"); got != MaxPending {
		t.Fatalf("approval.reject audits = %d, want %d", got, MaxPending)
	}
	n.mu.Lock()
	removed := len(n.remove)
	n.mu.Unlock()
	if removed != MaxPending {
		t.Fatalf("notifications withdrawn = %d, want %d", removed, MaxPending)
	}
}

// hookAudit calls hook (outside its own lock) for every event.
type hookAudit struct {
	fakeAudit
	hook func(action string)
}

func (h *hookAudit) Append(ctx context.Context, actor, action string, detail any) error {
	_ = h.fakeAudit.Append(ctx, actor, action, detail)
	if h.hook != nil {
		h.hook(action)
	}
	return nil
}

// M-2: once the 10th wrong code is recorded, no other pending approval may
// be tried, not even by a Confirm that was already waiting for the lock.
func TestLockoutLeavesNoWindowForAnotherGuess(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	db := openTestDB(t)
	n := &fakeNotifier{}
	a := &hookAudit{}
	s, err := NewStore(db, a, n, clock(&now))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxWrongPerDay-1; i++ {
		v, err := s.Create(ctx, KindGrant, fmt.Sprintf("g-%d", i), "s", Action{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Confirm(ctx, v.ID, "wrong"); err == nil {
			t.Fatal("wrong code accepted")
		}
		if _, err := s.Reject(ctx, v.ID); err != nil {
			t.Fatal(err)
		}
	}
	va, err := s.Create(ctx, KindGrant, "g-a", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	vb, err := s.Create(ctx, KindGrant, "g-b", "s", Action{})
	if err != nil {
		t.Fatal(err)
	}
	codeB := n.lastCode(t)
	var performed bool
	s.mu.Lock()
	s.pending[vb.ID].action = Action{Perform: func(context.Context, *sql.Tx) (any, error) {
		performed = true
		return nil, nil
	}}
	s.mu.Unlock()

	var wg sync.WaitGroup
	var raceErr error
	var once sync.Once
	a.hook = func(action string) {
		if action != "approval.bad_code" {
			return
		}
		// Called while the 10th wrong code holds s.mu: this Confirm queues
		// on the mutex and runs as soon as it is released.
		once.Do(func() {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, raceErr = s.Confirm(ctx, vb.ID, codeB)
			}()
		})
	}
	if _, err := s.Confirm(ctx, va.ID, "wrong"); err == nil {
		t.Fatal("wrong code accepted")
	}
	wg.Wait()
	if !errors.Is(raceErr, ErrUnknown) {
		t.Fatalf("confirm racing the lock: err = %v, want ErrUnknown", raceErr)
	}
	if performed {
		t.Fatal("an approval was performed after the 10th wrong code")
	}
	v, err := s.Show(ctx, vb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StateRejected {
		t.Fatalf("state = %s, want rejected", v.State)
	}
}

// The lock lasts only while the rolling 24 h window is full.
func TestLockoutEndsWhenWindowHasRoom(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _ := newTestStore(t, clock(&now))
	for i := 0; i < MaxWrongPerDay; i++ {
		v, err := s.Create(ctx, KindGrant, fmt.Sprintf("g-%d", i), "s", Action{})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		_, _ = s.Confirm(ctx, v.ID, "wrong")
		_, _ = s.Reject(ctx, v.ID)
	}
	if _, err := s.Create(ctx, KindGrant, "g-x", "s", Action{}); !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	now = now.Add(WrongCodeWindow - time.Minute)
	if _, err := s.Create(ctx, KindGrant, "g-y", "s", Action{}); !errors.Is(err, ErrLocked) {
		t.Fatalf("just inside the window: err = %v, want ErrLocked", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := s.Create(ctx, KindGrant, "g-z", "s", Action{}); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

// L-4: decided rows older than 30 days are pruned; pending and recent rows stay.
func TestPruneDecidedRows(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _ := newTestStore(t, clock(&now))
	old := now.Add(-31 * 24 * time.Hour).UTC().Format(storeTimeFmt)
	recent := now.Add(-time.Hour).UTC().Format(storeTimeFmt)
	for _, r := range []struct{ id, state, decided string }{
		{"a-old", StateRejected, old},
		{"a-recent", StateApproved, recent},
	} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO approvals (id, kind, subject, summary, created, expires, attempts, state, decided)
VALUES (?, 'grant', 'g', 's', ?, ?, 0, ?, ?)`, r.id, r.decided, r.decided, r.state, r.decided); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ExpireStale(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Show(ctx, "a-old"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("old decided row: err = %v, want pruned", err)
	}
	if _, err := s.Show(ctx, "a-recent"); err != nil {
		t.Fatalf("recent decided row pruned: %v", err)
	}
}

// Ticket 2.2a acceptance: no code and no code material in any SQLite table
// or audit detail, across create, a wrong code, confirm and reject.
func TestNoCodeMaterialAnywhereInDatabase(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, n, a := newTestStore(t, clock(&now))

	v1, err := s.Create(ctx, KindGrant, "g-1", "approve grant to bob?", Action{})
	if err != nil {
		t.Fatal(err)
	}
	code1 := n.lastCode(t)
	if _, err := s.Confirm(ctx, v1.ID, "wrong"); err == nil {
		t.Fatal("wrong code accepted")
	}
	if _, err := s.Confirm(ctx, v1.ID, code1); err != nil {
		t.Fatal(err)
	}
	v2, err := s.Create(ctx, KindRelease, "s-1", "release result?", Action{})
	if err != nil {
		t.Fatal(err)
	}
	code2 := n.lastCode(t)
	if _, err := s.Reject(ctx, v2.ID); err != nil {
		t.Fatal(err)
	}

	var dump strings.Builder
	tables, err := s.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	_ = tables.Close()
	for _, name := range names {
		rows, err := s.db.QueryContext(ctx, `SELECT * FROM "`+name+`"`) //nolint:gosec // name comes from sqlite_master, test only
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					v = string(b)
				}
				fmt.Fprintf(&dump, "%s.%s=%v\n", name, cols[i], v)
			}
		}
		_ = rows.Close()
	}
	a.mu.Lock()
	for _, e := range a.events {
		fmt.Fprintf(&dump, "audit %s %s %v\n", e.actor, e.action, e.detail)
	}
	a.mu.Unlock()

	haystack := strings.ToLower(dump.String())
	for _, c := range []struct{ id, code string }{{v1.ID, code1}, {v2.ID, code2}} {
		if regexp.MustCompile(`(^|[^0-9a-z])` + c.code + `([^0-9a-z]|$)`).MatchString(haystack) {
			t.Fatalf("code %s found in the database or audit:\n%s", c.code, haystack)
		}
		for _, pre := range []string{
			c.code, c.id + c.code, c.id + "\n" + c.code,
			"dorylinae-approval-v2\n" + c.id + "\n" + c.code,
			"dorylinae-approval-v1\n" + c.id + "\n" + c.code,
		} {
			sum := sha256.Sum256([]byte(pre))
			if strings.Contains(haystack, hex.EncodeToString(sum[:])) {
				t.Fatalf("a hash of the code (%q) is stored", pre)
			}
		}
		mac := CodeMACHex(s.key, c.id, c.code)
		if strings.Contains(haystack, mac) {
			t.Fatal("code_mac is stored")
		}
	}
}
