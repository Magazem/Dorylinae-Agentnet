package approval

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// R55-F5 A3: Store.Create refuses a summary that is longer than 4096 code
// points or not display-safe. Nothing is stored or audited, and no window or
// notification is started (the backstop of Docs/protocol/approval.md
// §Length).
func TestCreateRefusesLongOrUnsafeSummary(t *testing.T) {
	ctx := context.Background()
	for name, sum := range map[string]string{
		"4097 code points": strings.Repeat("é", 4097),
		"zero-width space": "Grant fs.read to Code 4\u200b8\u200b2",
		"newline":          "Grant fs.read\nApproved by IT",
	} {
		now := time.Now()
		s, n, a, win := newWindowTestStore(t, clock(&now))
		if _, err := s.Create(ctx, KindGrant, "g-1", sum, Action{}); !errors.Is(err, ErrNotDisplaySafe) {
			t.Errorf("%s: err = %v, want ErrNotDisplaySafe", name, err)
		}
		var rows int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM approvals`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		n.mu.Lock()
		shows := len(n.shows)
		n.mu.Unlock()
		a.mu.Lock()
		events := len(a.events)
		a.mu.Unlock()
		win.mu.Lock()
		starts := len(win.starts)
		win.mu.Unlock()
		if rows != 0 || shows != 0 || events != 0 || starts != 0 {
			t.Errorf("%s: rows %d, notifications %d, audit %d, windows %d; want all 0", name, rows, shows, events, starts)
		}
	}
	// Exactly 4096 code points is accepted.
	now := time.Now()
	s, _, _, _ := newWindowTestStore(t, clock(&now))
	if _, err := s.Create(ctx, KindGrant, "g-1", strings.Repeat("é", 4096), Action{}); err != nil {
		t.Fatalf("4096 code points: %v", err)
	}
}

// R55-F5 A10 (store half): at confirm, the summary rebuilt from the object of
// record must equal approvals.summary read in the same transaction; an
// UPDATE of the stored summary, or a rebuilt text that differs, rejects the
// approval with reason "precondition" and the action is not performed.
func TestConfirmComparesRebuiltSummary(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		rebuilt string
		tamper  bool
	}{
		"same":         {rebuilt: "Grant x."},
		"text changed": {rebuilt: "Grant y."},
		"row tampered": {rebuilt: "Grant x.", tamper: true},
	} {
		now := time.Now()
		s, n, a, _ := newWindowTestStore(t, clock(&now))
		performed := false
		view, err := s.Create(ctx, KindGrant, "g-1", "Grant x.", Action{
			Rebuild: func(context.Context, *sql.Tx) (string, error) { return tc.rebuilt, nil },
			Perform: func(context.Context, *sql.Tx) (any, error) { performed = true; return nil, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		if tc.tamper {
			if _, err := s.db.ExecContext(ctx, `UPDATE approvals SET summary = 'Grant z.' WHERE id = ?`, view.ID); err != nil {
				t.Fatal(err)
			}
		}
		_, err = s.Confirm(ctx, view.ID, n.lastCode(t))
		ok := name == "same"
		if ok != (err == nil) || ok != performed {
			t.Errorf("%s: err %v, performed %v", name, err, performed)
		}
		if ok {
			continue
		}
		if !errors.Is(err, ErrChanged) {
			t.Errorf("%s: err = %v, want ErrChanged", name, err)
		}
		var state string
		if err := s.db.QueryRowContext(ctx, `SELECT state FROM approvals WHERE id = ?`, view.ID).Scan(&state); err != nil || state != StateRejected {
			t.Errorf("%s: state %q, %v", name, state, err)
		}
		found := false
		a.mu.Lock()
		for _, e := range a.events {
			if d, ok := e.detail.(map[string]string); ok && e.action == "approval.reject" && d["reason"] == "precondition" {
				found = true
			}
		}
		a.mu.Unlock()
		if !found {
			t.Errorf("%s: no approval.reject precondition audit", name)
		}
	}
}
