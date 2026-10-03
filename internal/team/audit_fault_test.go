package team_test

// R55-F31 T4.9: team.roster_apply is an S row (the roster introduces peers
// with team trust). With its insert failing, the roster is not applied: no team
// row and no introduced peer. Delivered again without the fault it applies.

import (
	"context"
	"errors"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
)

func TestRosterApplyAuditFailureAppliesNothing(t *testing.T) {
	ctx := context.Background()
	owner, member, other := newTestNode(t, "owner"), newTestNode(t, "member"), newTestNode(t, "other")
	tm := ownedTeam(t, owner, member, other)
	out := &multiOutbox{}
	owner.ts.Outbox = out
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	body := cloneBody(t, out.body(t, member.key))

	if _, err := member.db.Exec(`CREATE TEMP TRIGGER audit_fail BEFORE INSERT ON main.audit_events
WHEN NEW.action = 'team.roster_apply' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := member.db.QueryRow(`SELECT COUNT(*) FROM sqlite_temp_master WHERE name = 'audit_fail'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the trigger is gone (%d, %v)", n, err)
	}
	err := deliver(t, owner, member, "team.roster", body)
	var we *audit.WriteError
	if !errors.As(err, &we) || we.Action != "team.roster_apply" {
		t.Fatalf("deliver = %v, want an *audit.WriteError for team.roster_apply", err)
	}
	if err := member.db.QueryRow(`SELECT COUNT(*) FROM teams`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d teams rows after the failed roster (%v)", n, err)
	}
	if err := member.db.QueryRow(`SELECT COUNT(*) FROM peers WHERE public_key = ?`, other.key).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the introduced peer was stored (%d, %v)", n, err)
	}
	if _, err := member.db.Exec(`DROP TRIGGER audit_fail`); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, member, "team.roster", body); err != nil {
		t.Fatalf("the redelivery: %v", err)
	}
	if err := member.db.QueryRow(`SELECT COUNT(*) FROM peers WHERE public_key = ? AND trust = 'team'`, other.key).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the introduced peer after the redelivery: %d (%v)", n, err)
	}
}
