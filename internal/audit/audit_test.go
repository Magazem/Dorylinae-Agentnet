package audit_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestAppendListAndAppendOnly(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	l := audit.New(s.DB())

	if err := l.Append(ctx, "tester", "x.one", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ctx, "tester", "x.two", nil); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ctx, "", "x", nil); err == nil {
		t.Fatal("expected error for empty actor")
	}
	evs, err := l.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Action != "x.one" || string(evs[0].Detail) != `{"n":1}` || string(evs[1].Detail) != `{}` {
		t.Fatalf("unexpected events: %+v", evs)
	}

	if _, err := s.DB().ExecContext(ctx, `UPDATE audit_events SET actor='evil'`); err == nil {
		t.Fatal("UPDATE should be rejected")
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM audit_events`); err == nil {
		t.Fatal("DELETE should be rejected")
	}
}

// An Append whose context is cancelled while it runs must never leave the
// pooled connection inside its BEGIN IMMEDIATE: the driver can report the
// cancellation after BEGIN already took effect, and every later Append on
// that connection then failed with "cannot start a transaction within a
// transaction" (INV-5: CI TestLifecycle, daemon.stop after a cancelled
// mailbox.rotate). The store has one connection, so the next Append reuses it.
func TestCancelledAppendLeavesNoOpenTransaction(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(testutil.TempDir(t), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	l := audit.New(s.DB())

	for i := range 3000 {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			for range i % 300 {
				runtime.Gosched()
			}
			cancel()
		}()
		_ = l.Append(ctx, "tester", "x.cancelled", nil) // may or may not land
		cancel()
		if err := l.Append(context.Background(), "tester", "x.after", nil); err != nil {
			t.Fatalf("iteration %d: append after a cancelled append: %v", i, err)
		}
	}
	if _, err := audit.New(s.DB()).List(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func openFaulty(t *testing.T) (*store.Store, *audit.Log, *bytes.Buffer) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "f.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var buf bytes.Buffer
	audit.SetErrorLog(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { audit.SetErrorLog(nil) })
	return s, audit.New(s.DB()), &buf
}

func TestAppendTxSoftKeepsTxUsable(t *testing.T) {
	ctx := context.Background()
	s, _, buf := openFaulty(t)
	if _, err := s.DB().ExecContext(ctx, `CREATE TEMP TRIGGER audit_fail BEFORE INSERT ON main.audit_events
		WHEN NEW.action = 'x.soft' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tt (n INTEGER)`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tt VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if err := audit.AppendTxSoft(ctx, tx, "tester", "x.soft", map[string]string{"secret": "MARKER"}); err != nil {
		t.Fatalf("soft append returned %v", err)
	}
	if err := audit.AppendTxSoft(ctx, tx, "tester", "x.ok", nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM tt`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("change not committed: %d %v", n, err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='x.soft'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed row present: %d %v", n, err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='x.ok'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("later row missing: %d %v", n, err)
	}
	out := buf.String()
	if strings.Count(out, "event=audit_error") != 1 || !strings.Contains(out, "action=x.soft") || strings.Contains(out, "MARKER") {
		t.Fatalf("log: %s", out)
	}
}

func TestAppendTxFailureIsWriteErrorAndLoggedOnce(t *testing.T) {
	ctx := context.Background()
	s, l, buf := openFaulty(t)
	if _, err := s.DB().ExecContext(ctx, `CREATE TEMP TRIGGER audit_fail BEFORE INSERT ON main.audit_events
		WHEN NEW.action = 'x.hard' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = audit.AppendTx(ctx, tx, "tester", "x.hard", nil)
	_ = tx.Rollback()
	var we *audit.WriteError
	if !errors.As(err, &we) || we.Action != "x.hard" {
		t.Fatalf("err = %v", err)
	}
	if err := l.Append(ctx, "tester", "x.hard", nil); !errors.As(err, &we) {
		t.Fatalf("Append err = %v", err)
	}
	if n := strings.Count(buf.String(), "event=audit_error"); n != 2 {
		t.Fatalf("want 2 log lines, got %d: %s", n, buf.String())
	}
}

func TestAppendTxSoftTxLost(t *testing.T) {
	ctx := context.Background()
	s, _, _ := openFaulty(t)
	if _, err := s.DB().ExecContext(ctx, `CREATE TEMP TRIGGER audit_fail BEFORE INSERT ON main.audit_events
		WHEN NEW.action = 'x.lost' BEGIN SELECT RAISE(ROLLBACK, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = audit.AppendTxSoft(ctx, tx, "tester", "x.lost", nil)
	_ = tx.Rollback()
	var tl *audit.TxLostError
	if !errors.As(err, &tl) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunSoftRetriesWithoutRowsAfterATxLoss(t *testing.T) {
	ctx := context.Background()
	s, _, buf := openFaulty(t)
	if _, err := s.DB().ExecContext(ctx, `CREATE TEMP TRIGGER audit_fail BEFORE INSERT ON main.audit_events
		WHEN NEW.action = 'x.lost' BEGIN SELECT RAISE(ROLLBACK, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tt (n INTEGER)`); err != nil {
		t.Fatal(err)
	}
	runs := 0
	err := audit.RunSoft(ctx, s.DB(), func(tx *sql.Tx, withRows bool) error {
		runs++
		if want := runs == 1; withRows != want {
			t.Errorf("run %d: withRows = %v, want %v", runs, withRows, want)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tt VALUES (7)`); err != nil {
			return err
		}
		// A nested hook writes through AppendTxSoft too; on the retry it is a no-op.
		if err := audit.AppendTxSoft(ctx, tx, "tester", "x.lost", nil); err != nil {
			return err
		}
		return audit.AppendTxSoft(ctx, tx, "tester", "x.after", nil)
	})
	if err != nil {
		t.Fatalf("RunSoft = %v", err)
	}
	if runs != 2 {
		t.Fatalf("fn ran %d times, want 2 (the retry)", runs)
	}
	var n int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM tt WHERE n = 7`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the change committed %d times (%v), want once", n, err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action LIKE 'x.%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d rows written on the retry, want none", n)
	}
	if got := strings.Count(buf.String(), "event=audit_error"); got != 1 {
		t.Fatalf("%d audit_error lines, want 1: %s", got, buf.String())
	}
}

func TestRunSoftOtherErrorsRollBack(t *testing.T) {
	ctx := context.Background()
	s, _, _ := openFaulty(t)
	if _, err := s.DB().ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tt (n INTEGER)`); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := audit.RunSoft(ctx, s.DB(), func(tx *sql.Tx, _ bool) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tt VALUES (9)`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	var n int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM tt WHERE n = 9`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d rows survived a failed change (%v)", n, err)
	}
}
