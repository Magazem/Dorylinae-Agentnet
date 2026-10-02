package approval

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Review 55 R55-147 (C12-05): a wrong code bumps the approval's attempts and
// the daily wrong-code window in one transaction. When the daily count
// cannot be written, neither count is kept, Confirm fails and the approval is
// dropped (fail closed, review 89 F1); the per-approval counter never runs
// ahead of the daily cap.
func TestBadCodeCountsBothOrNeither(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, n, _ := newTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindRelease, "s-1", "release quarantined result?", Action{})
	if err != nil {
		t.Fatal(err)
	}
	goodCode := n.lastCode(t)
	// The pool has one connection, so these TEMP triggers see every write.
	for _, ev := range []string{"INSERT", "UPDATE"} {
		if _, err := s.db.ExecContext(ctx, `CREATE TEMP TRIGGER fail_settings_`+ev+` BEFORE `+ev+` ON settings
			BEGIN SELECT RAISE(ABORT, 'settings write refused'); END`); err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.Confirm(ctx, view.ID, "000000")
	var bce *BadCodeError
	if err == nil || errors.As(err, &bce) {
		t.Fatalf("Confirm with the daily count unwritable = %v, want a write error", err)
	}
	shown, err := s.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shown.State != StateRejected {
		t.Fatalf("after a failed wrong-code write: state %s, want rejected (fail closed, review 89 F1)", shown.State)
	}

	for _, ev := range []string{"INSERT", "UPDATE"} {
		if _, err := s.db.ExecContext(ctx, `DROP TRIGGER temp.fail_settings_`+ev); err != nil {
			t.Fatal(err)
		}
	}
	// The approval is gone from memory: even the right code is refused.
	_, err = s.Confirm(ctx, view.ID, goodCode)
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("Confirm after the fail-closed drop = %v, want ErrUnknown", err)
	}
	// A fresh approval counts normally again.
	view, err = s.Create(ctx, KindRelease, "s-2", "release quarantined result?", Action{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Confirm(ctx, view.ID, "000000")
	if !errors.As(err, &bce) || bce.AttemptsLeft != MaxAttempts-1 {
		t.Fatalf("Confirm = %v, want *BadCodeError with %d attempts left", err, MaxAttempts-1)
	}
	times, err := s.settings.wrongCodes(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(times) != 1 {
		t.Fatalf("daily window holds %d wrong codes, want 1", len(times))
	}
}

// Review 89 F1: an undecodable approval.wrong_codes value must not make every
// wrong code fail (which lifted the 3-attempt cap). It reads as a full window:
// the wrong code is counted and approvals are locked.
func TestUndecodableWrongCodesLocks(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, n, _ := newTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindRelease, "s-1", "release quarantined result?", Action{})
	if err != nil {
		t.Fatal(err)
	}
	goodCode := n.lastCode(t)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value, updated) VALUES (?, '{"not":"a list"}', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, keyWrongCodes, now.UTC().Format(storeTimeFmt)); err != nil {
		t.Fatal(err)
	}
	_, err = s.Confirm(ctx, view.ID, "000000")
	var bce *BadCodeError
	if !errors.As(err, &bce) {
		t.Fatalf("Confirm with an undecodable window = %v, want *BadCodeError (counted, not an error)", err)
	}
	_, err = s.Confirm(ctx, view.ID, goodCode)
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("Confirm after the lock = %v, want ErrUnknown", err)
	}
	if _, err := s.Create(ctx, KindRelease, "s-2", "release quarantined result?", Action{}); !errors.Is(err, ErrLocked) {
		t.Fatalf("Create after the lock = %v, want ErrLocked", err)
	}
}
