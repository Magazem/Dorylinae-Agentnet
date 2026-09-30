package team_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// deliverAt is deliver with a chosen msg.created, to model a delayed mail.
func deliverAt(t *testing.T, from, to *testNode, kind string, body any, created time.Time) {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := agentcard.ParseStrict(raw)
	if err != nil {
		t.Fatal(err)
	}
	genBody, _ := gen.(map[string]any)
	op := &mail.Opened{Msg: mail.Msg{V: 1, ID: mail.NewID(), From: from.key, To: to.key, Created: created, Kind: kind, Body: genBody}}
	k := to.kinds[kind]
	tx, err := to.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Apply(ctx, tx, op); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if k.After != nil {
		k.After(ctx, op)
	}
}

// R55-068: a team.leave made before the member's current membership began (a
// delayed leave arriving after a rejoin) must not remove the rejoined member.
func TestStaleLeaveDoesNotRemoveRejoinedMember(t *testing.T) {
	owner := newTestNode(t, "owner")
	member := newTestNode(t, "member")
	pairWith(t, owner, member, peers.TrustCode)
	newNetwork(t, owner, member)
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	addPendingJoin(t, member, owner.key, "AAAA1", member.now())
	if _, err := owner.ts.AddMember(ctx, tm.ID, member.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	epoch := func() int64 {
		got, err := owner.ts.Get(ctx, tm.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Epoch
	}
	before := epoch()

	deliverAt(t, member, owner, "team.leave", map[string]any{"team": tm.ID}, owner.now().Add(-time.Hour))
	members, err := owner.ts.Members(ctx, tm.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("a leave older than the membership removed the member: %+v, %v", members, err)
	}
	if epoch() != before {
		t.Fatal("a stale leave changed the epoch")
	}

	// A leaver whose clock runs a few minutes behind is still honoured (skew).
	deliverAt(t, member, owner, "team.leave", map[string]any{"team": tm.ID}, owner.now().Add(-mail.MaxSkew/2))
	members, err = owner.ts.Members(ctx, tm.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("a leave after the membership began did not remove the member: %+v, %v", members, err)
	}
}

// txOutbox is a team.TxOutbox that can fail its transactional submit.
type txOutbox struct {
	fail    bool
	failErr error // returned instead of the generic failure when set
	queued  []string
	woken   int
	plainOK int
}

func (o *txOutbox) Submit(_ context.Context, _, _ string, _ any) (mail.Submitted, error) {
	o.plainOK++
	return mail.Submitted{ID: mail.NewID(), State: mail.StateQueued}, nil
}

func (o *txOutbox) SubmitTx(_ context.Context, _ *sql.Tx, to, _ string, _ any) (mail.Submitted, error) {
	if o.fail {
		if o.failErr != nil {
			return mail.Submitted{}, o.failErr
		}
		return mail.Submitted{}, errors.New("outbox unavailable")
	}
	o.queued = append(o.queued, to)
	return mail.Submitted{ID: mail.NewID(), State: mail.StateQueued}, nil
}

func (o *txOutbox) Wake() { o.woken++ }

// R55-113: team.leave is queued in the same transaction as the leave, so a
// failed submit leaves the team active and the leave can be retried.
func TestLeaveNotifyIsAtomicWithTheMail(t *testing.T) {
	owner, member, body := pairedTeam(t)
	ctx := context.Background()
	if err := deliver(t, owner, member, "team.roster", cloneBody(t, body)); err != nil {
		t.Fatal(err)
	}
	teamID := teamField(t, body)["id"].(string)
	ob := &txOutbox{fail: true}
	member.ts.Outbox = ob

	if _, _, err := member.ts.LeaveNotify(ctx, teamID, member.now()); err == nil {
		t.Fatal("LeaveNotify succeeded although the mail could not be queued")
	}
	got, err := member.ts.Get(ctx, teamID)
	if err != nil || got.State != team.StateActive {
		t.Fatalf("after a failed submit the team is %+v, %v; want active so the leave can be retried", got, err)
	}

	ob.fail = false
	if _, _, err := member.ts.LeaveNotify(ctx, teamID, member.now()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, err = member.ts.Get(ctx, teamID)
	if err != nil || got.State != team.StateLeft {
		t.Fatalf("after the retry the team is %+v, %v; want left", got, err)
	}
	if len(ob.queued) != 1 || ob.queued[0] != owner.key || ob.woken != 1 {
		t.Fatalf("queued = %v, woken = %d; want one team.leave to the owner and one wake", ob.queued, ob.woken)
	}
}

// Review 79 L3: a permanent "cannot send" (owner unpaired, no mailbox key)
// must not make leaving impossible: the team is left locally.
func TestLeaveNotifyLeavesWhenTheMailCanNeverBeSent(t *testing.T) {
	for _, sentinel := range []error{mail.ErrUnpaired, mail.ErrNoMailboxKey} {
		owner, member, body := pairedTeam(t)
		ctx := context.Background()
		if err := deliver(t, owner, member, "team.roster", cloneBody(t, body)); err != nil {
			t.Fatal(err)
		}
		teamID := teamField(t, body)["id"].(string)
		ob := &txOutbox{fail: true, failErr: sentinel}
		member.ts.Outbox = ob
		if _, _, err := member.ts.LeaveNotify(ctx, teamID, member.now()); err != nil {
			t.Fatalf("%v: LeaveNotify = %v, want the leave to commit", sentinel, err)
		}
		got, err := member.ts.Get(ctx, teamID)
		if err != nil || got.State != team.StateLeft {
			t.Fatalf("%v: team = %+v, %v; want left", sentinel, got, err)
		}
		if len(ob.queued) != 0 || ob.woken != 0 {
			t.Fatalf("%v: queued %v, woken %d; want nothing", sentinel, ob.queued, ob.woken)
		}
	}
}
