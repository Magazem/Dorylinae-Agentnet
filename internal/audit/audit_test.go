package audit_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

func TestAppendListAndAppendOnly(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
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
