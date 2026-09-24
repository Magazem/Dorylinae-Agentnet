package daemon

// Regression tests for Docs/review/28-2.2c-review.md.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func (h *grantHarness) insertPolicy(pol capability.Policy) {
	h.t.Helper()
	ctx := context.Background()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.caps.PolicyInsertTx(ctx, tx, pol); err != nil {
		_ = tx.Rollback()
		h.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		h.t.Fatal(err)
	}
}

func (h *grantHarness) policyCount() int {
	h.t.Helper()
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM grant_policies`).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

type grantCreateOut struct {
	Grant    GrantView     `json:"grant"`
	Approval approval.View `json:"approval"`
}

// H1: a policy approved without --public (sensitive) must not auto-issue a
// --public (non-sensitive) git.read grant: that grant would escape the
// result quarantine without any human approval.
func TestReview28PublicGrantNotCoveredBySensitivePolicy(t *testing.T) {
	repo := testGitRepo(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	now := time.Now()
	h.insertPolicy(capability.Policy{
		ID: capability.NewPolicyID(), Peer: peer, Action: capability.ActionGitRead, Path: repo, Branch: "main",
		MaxExpiresS: int((7 * 24 * time.Hour).Seconds()), Until: now.Add(24 * time.Hour), Created: now,
	})
	sid := h.openSession(peer, "r-28282828282828282828282828282801")

	var out grantCreateOut
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "git.read", Resource: repo + "#main", Public: true}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Grant.State != capability.StatePendingApproval || out.Approval.ID == "" || out.Grant.Sensitive {
		t.Fatalf("public grant under a sensitive policy = %+v, want pending_approval with an approval", out)
	}
	if n := outboxRows(t, h.db, "grant"); n != 0 {
		t.Fatalf("outbox has %d grant rows, want 0", n)
	}

	// The same grant without --public (sensitive) is covered.
	var out2 grantCreateOut
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "git.read", Resource: repo + "#main"}, &out2); err != nil {
		t.Fatal(err)
	}
	if out2.Grant.State != capability.StateActive || !out2.Grant.Sensitive {
		t.Fatalf("sensitive grant under a sensitive policy = %+v, want active", out2.Grant)
	}
}

// M1: the auto-issue transaction re-checks the session and the peer.
func TestReview28RecheckIssuanceTx(t *testing.T) {
	h, peer := newGrantHarness(t, peers.TrustRelay, true)
	sid := h.openSession(peer, "r-28282828282828282828282828282802")
	ctx := context.Background()
	check := func() string {
		tx, err := h.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		return ipcCode(recheckIssuanceTx(ctx, tx, h.ws, sid, peer, true))
	}
	if got := check(); got != CodeUnverifiedPeer {
		t.Fatalf("relay-trust peer on a non-loopback relay: code %q, want unverified_peer", got)
	}
	if _, err := h.db.Exec(`UPDATE peers SET trust = 'code' WHERE public_key = ?`, peer); err != nil {
		t.Fatal(err)
	}
	if got := check(); got != "" {
		t.Fatalf("open session, code-trust peer: code %q, want ok", got)
	}
	if _, err := h.db.Exec(`UPDATE work_sessions SET state = 'awaiting_result' WHERE id = ?`, sid); err != nil {
		t.Fatal(err)
	}
	if got := check(); got != CodeBadState {
		t.Fatalf("session left open: code %q, want bad_state", got)
	}
	if _, err := h.db.Exec(`UPDATE work_sessions SET state = 'open' WHERE id = ?`, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`DELETE FROM peers WHERE public_key = ?`, peer); err != nil {
		t.Fatal(err)
	}
	if got := check(); got != CodeBadState {
		t.Fatalf("peer removed: code %q, want bad_state", got)
	}
}

// M2: approval re-checks step 3: a resource directory replaced (here,
// deleted) while the approval waited drops the grant and sends no mail.
func TestReview28ApprovalRechecksResource(t *testing.T) {
	base := testutil.TempDir(t)
	dir := filepath.Join(base, "res")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	sid := h.openSession(peer, "r-28282828282828282828282828282803")
	var out grantCreateOut
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	err := h.confirm(out.Approval.ID)
	if got := ipcCode(err); got != CodeForbiddenResource {
		t.Fatalf("confirm after the resource vanished: err = %v, want forbidden_resource", err)
	}
	if n := outboxRows(t, h.db, "grant"); n != 0 {
		t.Fatalf("outbox has %d grant rows, want 0", n)
	}
}

// M3: a policy approval confirmed after its peer was removed inserts nothing.
func TestReview28PolicyApprovalAfterPeerRemoved(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	var out struct {
		Approval approval.View `json:"approval"`
	}
	if err := h.call("grant_policy_add", GrantPolicyAddParams{Peer: peer, Action: "fs.read", Resource: dir, MaxExpires: "2h"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := h.ps.Remove(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	err := h.confirm(out.Approval.ID)
	if got := ipcCode(err); got != CodeBadState {
		t.Fatalf("confirm after peers remove: err = %v, want bad_state", err)
	}
	if n := h.policyCount(); n != 0 {
		t.Fatalf("%d policies after a dropped approval, want 0", n)
	}
}

// M4: the removal hook revokes grants and deletes policies in the removing
// transaction.
func TestReview28PeerRemovalEndsPolicies(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	now := time.Now()
	h.insertPolicy(capability.Policy{
		ID: capability.NewPolicyID(), Peer: peer, Action: capability.ActionFSRead, Path: dir,
		MaxExpiresS: 3600, Until: now.Add(time.Hour), Created: now,
	})
	if err := h.ps.Remove(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	if n := h.policyCount(); n != 0 {
		t.Fatalf("%d policies after peers remove, want 0", n)
	}
}

// M5, L1: forbidden-path comparisons fold case on case-insensitive systems
// and compare whole components.
func TestReview28PathContainment(t *testing.T) {
	root := string(filepath.Separator) + "x"
	cfg := filepath.Join(root, "home", ".dorylinae")
	if !pathEqualOrContains(cfg, filepath.Join(cfg, "..secrets")) {
		t.Error(`a child named "..secrets" must count as inside the config dir`)
	}
	if pathEqualOrContains(cfg, filepath.Join(root, "home", ".dorylinae-other")) {
		t.Error("a sibling sharing a string prefix must not count as inside")
	}
	saved := caseInsensitiveFS
	t.Cleanup(func() { caseInsensitiveFS = saved })
	caseInsensitiveFS = true
	if !pathEqualOrContains(cfg, filepath.Join(root, "home", ".DORYLINAE")) {
		t.Error("on a case-insensitive filesystem the config dir in another case must be refused")
	}
	if !pathsEqual(filepath.Join(root, "Home"), filepath.Join(root, "home")) {
		t.Error("on a case-insensitive filesystem the home dir in another case must be refused")
	}
}
