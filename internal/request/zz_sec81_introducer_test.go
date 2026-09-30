package request

// Security review 81 (R55-F13), finding M1: the introducer caps are shared by
// every key one owner introduced, so two hostile introduced members starve an
// honest member of the same owner who has sent nothing.

import (
	"errors"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

func TestSec81IntroducerCapStarvesHonestMember(t *testing.T) {
	now := time.Now()
	owner := testKey(30)
	h1, h2, honest := testKey(31), testKey(32), testKey(33)
	addIntroduced := func(s *Store) {
		for _, k := range []string{h1, h2, honest} {
			if _, err := s.DB.Exec(`INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust, introduced_by)
				VALUES (?, 'n', 'h', '[]', '{}', '2026-01-01T00:00:00Z', 'team', ?)`, k, owner); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Open cap: each hostile key stays within its own 100.
	s, _, _ := newTestStore(t, testTo, &policy{})
	addIntroduced(s)
	seedIn(t, s.DB, h1, owner, StatePending, now.Add(-48*time.Hour), 100)
	seedIn(t, s.DB, h2, owner, StatePending, now.Add(-48*time.Hour), 100)
	r := freshRequest(honest)
	if err := deliverRequest(t, s, r, now); err != nil {
		t.Fatal(err)
	}
	if _, code, _ := inRow(t, s.DB, honest, r.ID); code != "inbox_full" {
		t.Fatalf("honest member's first request = %q; the finding expects inbox_full", code)
	}
	t.Logf("open: honest member with 0 open requests is auto-declined inbox_full")

	// Daily cap: each hostile key stays within its own 200 (all auto-declined,
	// no human action can clear them).
	s2, _, _ := newTestStore(t, testTo, &policy{})
	addIntroduced(s2)
	seedIn(t, s2.DB, h1, owner, StateDeclined, now.Add(-time.Hour), 200)
	seedIn(t, s2.DB, h2, owner, StateDeclined, now.Add(-time.Hour), 200)
	if err := deliverRequest(t, s2, freshRequest(honest), now); !errors.Is(err, mail.ErrLimit) {
		t.Fatalf("honest member's first request: %v; the finding expects mail.ErrLimit", err)
	}
	t.Logf("daily: honest member's first request in 24 h is refused for a limit")
}
