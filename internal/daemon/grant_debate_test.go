package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// seedDebate stores a debates row with peer in phase (Docs/protocol/debate.md
// §Persistence); the grant checks read only this table.
func (h *grantHarness) seedDebate(peer, sid, phase string) {
	h.t.Helper()
	var outcome any
	if phase == "closed" {
		outcome = "cancelled"
	}
	if _, err := h.db.Exec(`INSERT INTO debates (session, role, peer, request_id, rounds_max, turn_timeout_s, commitment, phase, outcome, created, updated)
		VALUES (?, 'initiator', ?, 'r-dddddddddddddddddddddddddddddddd', 2, 3600, 'c', ?, ?, 'x', 'x')`, sid, peer, phase, outcome); err != nil {
		h.t.Fatal(err)
	}
}

// Docs/protocol/debate.md §Quarantine interplay (review 43 M5): no sensitive
// grant to a peer with a debate in invited, positions, rounds or converge, at
// grant_create, on the policy path, and at the approval's confirm; and no
// grant at all on a debate's own session.
func TestGrantDebateOpen(t *testing.T) {
	ctx := context.Background()
	t.Run("grant_create while invited", func(t *testing.T) {
		dir := testutil.TempDir(t)
		h, peer := newGrantHarness(t, peers.TrustCode, false)
		sid := h.openSession(peer, "r-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.seedDebate(peer, "s-11111111111111111111111111111111", "invited")
		err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, nil)
		if ipcCode(err) != CodeDebateOpen {
			t.Fatalf("grant_create: %v, want debate_open", err)
		}
		if n := h.count(`SELECT COUNT(*) FROM grants`); n != 0 {
			t.Fatalf("%d grant rows", n)
		}
		if lastAuditDetail(t, h.db, "grant.refused")["reason"] != CodeDebateOpen {
			t.Error("refusal not audited")
		}
		// Once the debate closes, the grant goes to approval as usual.
		if _, err := h.db.Exec(`UPDATE debates SET phase = 'closed', outcome = 'cancelled'`); err != nil {
			t.Fatal(err)
		}
		var out struct {
			Approval approval.View `json:"approval"`
		}
		if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out); err != nil || out.Approval.ID == "" {
			t.Fatalf("after the close: %v %+v", err, out)
		}
	})
	t.Run("policy path", func(t *testing.T) {
		dir := testutil.TempDir(t)
		h, peer := newGrantHarness(t, peers.TrustCode, false)
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		pol := capability.Policy{ID: capability.NewPolicyID(), Peer: peer, Action: capability.ActionFSRead, Path: resolved,
			MaxExpiresS: int((7 * 24 * time.Hour).Seconds()), Until: now.Add(24 * time.Hour), Created: now}
		tx, _ := h.db.BeginTx(ctx, nil)
		if err := h.caps.PolicyInsertTx(ctx, tx, pol); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		sid := h.openSession(peer, "r-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		h.seedDebate(peer, "s-22222222222222222222222222222222", "rounds")
		err = h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, nil)
		if ipcCode(err) != CodeDebateOpen {
			t.Fatalf("grant_create: %v, want debate_open", err)
		}
		if n := h.count(`SELECT COUNT(*) FROM grants`) + outboxRows(t, h.db, "grant"); n != 0 {
			t.Fatalf("a policy issued a sensitive grant during a debate (%d rows)", n)
		}
	})
	t.Run("approval confirm", func(t *testing.T) {
		dir := testutil.TempDir(t)
		h, peer := newGrantHarness(t, peers.TrustCode, false)
		sid := h.openSession(peer, "r-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		var out struct {
			Grant    GrantView     `json:"grant"`
			Approval approval.View `json:"approval"`
		}
		if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out); err != nil {
			t.Fatal(err)
		}
		// The debate starts while the grant waits for its approval.
		h.seedDebate(peer, "s-33333333333333333333333333333333", "positions")
		if err := h.confirm(out.Approval.ID); err == nil {
			t.Fatal("confirm succeeded during an open debate")
		}
		rec, err := h.caps.Get(ctx, out.Grant.ID)
		if err != nil {
			t.Fatal(err)
		}
		if rec.State == capability.StateActive || outboxRows(t, h.db, "grant") != 0 {
			t.Fatalf("grant %s, %d grant mails", rec.State, outboxRows(t, h.db, "grant"))
		}
	})
	t.Run("debate session", func(t *testing.T) {
		dir := testutil.TempDir(t)
		h, peer := newGrantHarness(t, peers.TrustCode, false)
		sid := worksession.DeriveID(h.self, peer, "r-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
		if _, err := h.db.Exec(`INSERT INTO work_sessions (id, role, peer, request_id, team_id, state, opened, state_at, updated, kind)
			VALUES (?, 'requester', ?, 'r-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee', 't', 'open', 'o', 's', 'u', 'debate')`, sid, peer); err != nil {
			t.Fatal(err)
		}
		err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, nil)
		if ipcCode(err) != CodeBadState {
			t.Fatalf("grant_create on a debate session: %v, want bad_state", err)
		}
	})
}

func (h *grantHarness) count(q string) int {
	h.t.Helper()
	var n int
	if err := h.db.QueryRow(q).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}
