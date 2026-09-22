package presence

import (
	"context"
	"testing"
	"time"
)

// fakeMailboxLookup reports every key in ok as having a mailbox key.
type fakeMailboxLookup map[string]bool

func (f fakeMailboxLookup) MailboxPub(peer string) ([]byte, bool) {
	if f[peer] {
		return []byte("pub"), true
	}
	return nil, false
}

// TestVisibleSetModes checks Docs/protocol/presence.md §Sending "Recipients"
// for all three modes, and the only_team auto-degrade to invisible when the
// target team is no longer active (ticket 1.3).
func TestVisibleSetModes(t *testing.T) {
	now := testNow
	clock := func() time.Time { return now }
	ts, _, tm := newResyncFixture(t, clock)
	ctx := context.Background()
	if _, err := ts.AddMember(ctx, tm.ID, "peer-1", now); err != nil {
		t.Fatal(err)
	}

	s := &Sender{Team: ts, Self: ts.Self, Now: clock, Peers: fakeMailboxLookup{"peer-1": true}}

	// Default (unset) mode behaves like visible.
	got, err := s.visibleSet(ctx)
	if err != nil || len(got) != 1 || got[0] != "peer-1" {
		t.Fatalf("default mode visibleSet = %v, err = %v", got, err)
	}

	if err := s.SetMode(ctx, VisibilityMode{Mode: ModeInvisible}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.visibleSet(ctx); err != nil || len(got) != 0 {
		t.Fatalf("invisible mode visibleSet = %v, err = %v", got, err)
	}

	if err := s.SetMode(ctx, VisibilityMode{Mode: ModeOnlyTeam, Team: tm.ID}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.visibleSet(ctx); err != nil || len(got) != 1 || got[0] != "peer-1" {
		t.Fatalf("only_team mode visibleSet = %v, err = %v", got, err)
	}
	if mode := s.Mode(); mode.Mode != ModeOnlyTeam || mode.Team != tm.ID {
		t.Fatalf("Mode() = %+v", mode)
	}

	// The team going inactive (dissolved, or left for a non-owner) degrades
	// only_team to invisible automatically.
	if _, err := ts.Delete(ctx, tm.ID, now); err != nil {
		t.Fatal(err)
	}
	if got, err := s.visibleSet(ctx); err != nil || len(got) != 0 {
		t.Fatalf("after team gone: visibleSet = %v, err = %v", got, err)
	}
	if mode := s.Mode(); mode.Mode != ModeInvisible {
		t.Fatalf("after team gone: Mode() = %+v, want invisible", mode)
	}
}
