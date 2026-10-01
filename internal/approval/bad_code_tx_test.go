package approval

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Review 55 R55-147 (C12-05): a wrong code bumps the approval's attempts and
// the daily wrong-code window in one transaction. When the daily count
// cannot be written, neither count is kept and Confirm fails; the per-
// approval counter never runs ahead of the daily cap.
func TestBadCodeCountsBothOrNeither(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, _, _ := newTestStore(t, clock(&now))
	view, err := s.Create(ctx, KindRelease, "s-1", "release quarantined result?", Action{})
	if err != nil {
		t.Fatal(err)
	}
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
	if shown.State != StatePending || shown.AttemptsLeft != MaxAttempts {
		t.Fatalf("after a failed wrong-code write: state %s, attempts_left %d; want pending, %d (nothing counted)",
			shown.State, shown.AttemptsLeft, MaxAttempts)
	}

	for _, ev := range []string{"INSERT", "UPDATE"} {
		if _, err := s.db.ExecContext(ctx, `DROP TRIGGER temp.fail_settings_`+ev); err != nil {
			t.Fatal(err)
		}
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
