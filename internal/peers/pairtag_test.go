package peers_test

// Ticket 1.1d: a pairing tag and a completion hook carrying the lookup
// (Docs/review/11-phase1-tickets.md, 1.1d), used by team_invite/team_join.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

// tagRecorder is a peers.Completer that records every call.
type tagRecorder struct {
	mu   sync.Mutex
	info []peers.CompletionInfo
}

func (r *tagRecorder) Completed(info peers.CompletionInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.info = append(r.info, info)
}

func (r *tagRecorder) last() (peers.CompletionInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.info) == 0 {
		return peers.CompletionInfo{}, false
	}
	return r.info[len(r.info)-1], true
}

func (r *tagRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.info)
}

func TestPairingTagCompletedOnSuccess(t *testing.T) {
	issuer, redeemer := newNode(t, "issuer", nil), newNode(t, "redeemer", nil)
	newBus(t, nil, issuer, redeemer)

	issuerTag, redeemerTag := &tagRecorder{}, &tagRecorder{}
	ist, err := issuer.m.StartTagged(context.Background(), issuerTag)
	if err != nil || ist.Code == "" {
		t.Fatalf("StartTagged = %+v, %v", ist, err)
	}
	code := bareCode(ist.Code)

	rst, err := redeemer.m.RedeemTagged(context.Background(), code, false, redeemerTag)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, redeemer, rst.ID, peers.StateComplete)
	waitState(t, issuer, ist.ID, peers.StateComplete)
	waitFor(t, "issuer tag Completed", func() bool { return issuerTag.count() == 1 })
	waitFor(t, "redeemer tag Completed", func() bool { return redeemerTag.count() == 1 })

	ii, _ := issuerTag.last()
	if ii.State != peers.StateComplete || ii.Role != peers.RoleIssuer || ii.PairingID != ist.ID ||
		ii.Lookup != code[:5] || ii.Peer == nil || ii.Peer.PublicKey != redeemer.id.key {
		t.Fatalf("issuer completion info = %+v, want complete/issuer/%s/%s/%s", ii, ist.ID, code[:5], redeemer.id.key)
	}
	ri, _ := redeemerTag.last()
	if ri.State != peers.StateComplete || ri.Role != peers.RoleRedeemer || ri.PairingID != rst.ID ||
		ri.Lookup != code[:5] || ri.Peer == nil || ri.Peer.PublicKey != issuer.id.key {
		t.Fatalf("redeemer completion info = %+v, want complete/redeemer/%s/%s/%s", ri, rst.ID, code[:5], issuer.id.key)
	}

	// A Completer's Completed must run exactly once per session.
	time.Sleep(50 * time.Millisecond)
	if n := issuerTag.count(); n != 1 {
		t.Errorf("issuer tag Completed called %d times, want 1", n)
	}
	if n := redeemerTag.count(); n != 1 {
		t.Errorf("redeemer tag Completed called %d times, want 1", n)
	}
}

func TestPairingTagCompletedOnFailureCarriesNoPeer(t *testing.T) {
	issuer := newNode(t, "issuer", nil)
	redeemer := newNode(t, "redeemer", func(c *peers.Config) { c.ConfirmWait = 300 * time.Millisecond })
	newBus(t, nil, issuer, redeemer)

	issuerTag := &tagRecorder{}
	ist, err := issuer.m.StartTagged(context.Background(), issuerTag)
	if err != nil || ist.Code == "" {
		t.Fatalf("StartTagged = %+v, %v", ist, err)
	}
	code := bareCode(ist.Code)

	redeemerTag := &tagRecorder{}
	rst, err := redeemer.m.RedeemTagged(context.Background(), wrongSecret(code), false, redeemerTag)
	if err != nil {
		t.Fatal(err)
	}
	final := waitState(t, redeemer, rst.ID, peers.StateFailed)
	waitFor(t, "redeemer tag Completed", func() bool { return redeemerTag.count() == 1 })

	ri, _ := redeemerTag.last()
	if ri.State != peers.StateFailed || ri.Peer != nil || ri.PairingID != rst.ID {
		t.Fatalf("redeemer completion info = %+v, want failed with no peer (status error: %+v)", ri, final.Error)
	}
	// The issuer's lone attempt failed too, but its own session stays pending
	// (it allows up to 3 attempts): its tag must not have completed yet.
	if n := issuerTag.count(); n != 0 {
		t.Errorf("issuer tag Completed called %d times while still pending, want 0", n)
	}
}

// hookTag is a peers.Completer that runs fn when it completes.
type hookTag func(peers.CompletionInfo)

func (h hookTag) Completed(info peers.CompletionInfo) { h(info) }

// TestPairingTagIssuerCompletedBeforeTagI: the issuer's Completed runs before
// it sends pair.confirm{tag_I}, so the redeemer cannot complete (and a joiner
// cannot submit team.join) before the owner's team_invites row exists.
func TestPairingTagIssuerCompletedBeforeTagI(t *testing.T) {
	issuer, redeemer := newNode(t, "issuer", nil), newNode(t, "redeemer", nil)
	b := newBus(t, nil, issuer, redeemer)

	var mu sync.Mutex
	sentAtCompletion, calls := -1, 0
	redeemerDone := false
	redeemerTag := &tagRecorder{}
	ist, err := issuer.m.StartTagged(context.Background(), hookTag(func(peers.CompletionInfo) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		sentAtCompletion = b.envsFrom(issuer.id.key)
		redeemerDone = redeemerTag.count() > 0
	}))
	if err != nil || ist.Code == "" {
		t.Fatalf("StartTagged = %+v, %v", ist, err)
	}
	rst, err := redeemer.m.RedeemTagged(context.Background(), bareCode(ist.Code), false, redeemerTag)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, redeemer, rst.ID, peers.StateComplete)
	waitState(t, issuer, ist.ID, peers.StateComplete)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("issuer Completed called %d times, want 1", calls)
	}
	if sentAtCompletion != 0 || redeemerDone {
		t.Fatalf("at issuer Completed: issuer had sent %d envelopes (want 0: tag_I not yet sent), redeemer done = %v",
			sentAtCompletion, redeemerDone)
	}
}
