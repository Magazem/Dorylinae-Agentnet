package audit_test

import (
	"context"
	"path/filepath"
	"runtime"
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
