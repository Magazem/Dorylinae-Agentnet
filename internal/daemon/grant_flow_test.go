package daemon

// Ticket 2.2c acceptance (Docs/protocol/grant.md, Docs/review/23-phase2-tickets.md
// "2.2c Grant issuance"). These tests drive registerGrant directly over a real
// in-process IPC listener (no relay needed: the outbox's peer lookup is
// satisfied by a peer row whose mailbox_keys announces a throwaway X25519 key,
// so SubmitTx can seal mail without a live counterparty), following the same
// lightweight pattern as TestRequestSubmitRefusesRelayTrust (request_d5_test.go).
// A live work session is seeded directly (reqStore.Sessions is not wired into
// the daemon binary yet, review 27 design choice (1); 2.1b turns on the real
// accept path later).

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// fakeApprovalNotifier captures the 6-digit code shown for each approval id
// (mirroring internal/approval's own test notifier).
type fakeApprovalNotifier struct {
	mu    sync.Mutex
	codes map[string]string
}

func (f *fakeApprovalNotifier) Show(_ context.Context, id string, _ time.Time, _, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.codes == nil {
		f.codes = map[string]string{}
	}
	f.codes[id] = body[len(body)-6:]
	return nil
}

func (f *fakeApprovalNotifier) Remove(context.Context, string) {}

func (f *fakeApprovalNotifier) code(t *testing.T, id string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.codes[id]
	if !ok {
		t.Fatalf("no code was shown for approval %s", id)
	}
	return c
}

// grantHarness wires one daemon's worth of grant machinery over a real IPC
// listener, without a relay: newHarnessRelay-based tests live in
// outbox_harness_test.go's package (daemon_test); this file is package
// daemon so it can reach registerGrant, CodeNotGrantor and friends directly.
type grantHarness struct {
	t        *testing.T
	db       *sql.DB
	ps       *peers.Store
	ws       *worksession.Store
	caps     *capability.Store
	appr     *approval.Store
	notifier *fakeApprovalNotifier
	ob       *mail.Outbox
	log      *audit.Log
	self     string
	priv     ed25519.PrivateKey
	peer     string
	endpoint string
	cancel   context.CancelFunc
	done     chan error
}

// fakeMailPeers satisfies mail.OutboxPeers/TxOutboxPeers with one always-paired
// peer whose mailbox key is pub, so SubmitTx can seal mail with no live
// counterparty (same idea as internal/worksession/helpers_test.go's fakePeers).
type fakeMailPeers struct{ pub []byte }

func (p fakeMailPeers) IsPaired(string) bool                        { return true }
func (p fakeMailPeers) MailboxPub(string) ([]byte, bool)            { return p.pub, true }
func (p fakeMailPeers) IsPairedTx(*sql.Tx, string) bool             { return true }
func (p fakeMailPeers) MailboxPubTx(*sql.Tx, string) ([]byte, bool) { return p.pub, true }

// newGrantHarness builds a running daemon endpoint with grant_* and
// grant_policy_* registered, one paired peer ("the peer", trust as given),
// and returns the harness plus the peer's own key (needed to build tokens
// for the offline-verify tests).
func newGrantHarness(t *testing.T, peerTrust string, nonLoopbackRelay bool) (*grantHarness, string) {
	t.Helper()
	ctx := context.Background()
	dir := testutil.TempDir(t)
	st, err := store.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	self := envelope.KeyString(pub)

	peerPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	peerKey := envelope.KeyString(peerPub)
	xk, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mboxJSON := fmt.Sprintf(`[{"announcement":{"pub":%q}}]`, base64.RawURLEncoding.EncodeToString(xk.PublicKey().Bytes()))
	if _, err := db.Exec(`INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys)
VALUES (?, 'peerb', 'h', '[]', '{}', ?, ?, ?)`, peerKey, time.Now().UTC().Format(time.RFC3339), peerTrust, mboxJSON); err != nil {
		t.Fatal(err)
	}

	ps := peers.NewStore(db)
	log := audit.New(db)
	ws := &worksession.Store{DB: db, Self: self, Audit: log}
	caps := &capability.Store{DB: db}
	ws.RevokeGrants = func(ctx context.Context, tx *sql.Tx, sid string, now time.Time) error {
		_, err := caps.RevokeForSessionTx(ctx, tx, sid, capability.ReasonSessionClosed, now)
		return err
	}
	ps.OnRemovedTx = revokeForRemovedPeer(caps)
	ob := &mail.Outbox{
		DB:    db,
		Priv:  func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), priv...), nil },
		Peers: fakeMailPeers{pub: xk.PublicKey().Bytes()},
	}
	ws.Outbox = ob
	notifier := &fakeApprovalNotifier{}
	fixedNow := time.Now()
	apprStore, err := approval.NewStore(db, log, notifier, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}

	p, err := paths.In(filepath.Join(dir, "ep"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	ln, err := ipc.Listen(p.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	srv := ipc.NewServer()
	registerGrant(srv, caps, ws, apprStore, ps, ob, log, grantIdentity{Self: self, Priv: func() (ed25519.PrivateKey, error) {
		return append(ed25519.PrivateKey(nil), priv...), nil
	}}, p.Dir, nonLoopbackRelay)
	registerApproval(srv, apprStore)
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(sctx, ln) }()
	t.Cleanup(func() { cancel(); <-done })

	h := &grantHarness{t: t, db: db, ps: ps, ws: ws, caps: caps, appr: apprStore, notifier: notifier, ob: ob, log: log,
		self: self, priv: priv, peer: peerKey, endpoint: p.Endpoint, cancel: cancel, done: done}
	return h, peerKey
}

func (h *grantHarness) call(method string, params, out any) error {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ipc.Call(ctx, h.endpoint, method, params, out)
}

// openSession seeds a work_sessions row directly (role requester, on h,
// with peer, state open) and returns the derived session id.
func (h *grantHarness) openSession(peer, reqID string) string {
	h.t.Helper()
	ctx := context.Background()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.ws.OpenSession(ctx, tx, worksession.RoleRequester, peer, reqID, "", time.Now()); err != nil {
		h.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		h.t.Fatal(err)
	}
	return worksession.DeriveID(h.self, peer, reqID)
}

func ipcCode(err error) string {
	var ie *ipc.Error
	if errors.As(err, &ie) {
		return ie.Code
	}
	return ""
}

// testGitRepo creates a temporary git repository with one commit on branch
// "main", skipping the test if git is not on PATH.
func testGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := lookupGit(); err != nil {
		t.Skip("git not available")
	}
	dir := testutil.TempDir(t)
	gitPath, err := lookupGit()
	if err != nil {
		t.Skip("git not available")
	}
	run := func(args ...string) {
		t.Helper()
		//nolint:gosec // test helper; gitPath from exec.LookPath, args are fixed literals
		cmd := exec.Command(gitPath, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "a@example.com")
	run("config", "user.name", "a")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// ---- Tests ----

// TestGrantIssuanceRefusals is the 2.2c acceptance list of issuance
// refusals: not_requester, a closed session (bad_state), a trust=relay peer
// on a non-loopback relay, a relative path, the config dir, the home dir, a
// filesystem root, a missing branch and an over-long expiry.
func TestGrantIssuanceRefusals(t *testing.T) {
	dir := testutil.TempDir(t)

	t.Run("not_requester", func(t *testing.T) {
		h, peer := newGrantHarness(t, peers.TrustCode, false)
		reqID := "r-11111111111111111111111111111111"
		ctx := context.Background()
		tx, _ := h.db.BeginTx(ctx, nil)
		if err := h.ws.OpenSession(ctx, tx, worksession.RoleWorker, peer, reqID, "", time.Now()); err != nil {
			t.Fatal(err)
		}
		_ = tx.Commit()
		sid := worksession.DeriveID(peer, h.self, reqID)
		var out map[string]any
		err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out)
		if got := ipcCode(err); got != CodeNotRequester {
			t.Fatalf("err = %v, want not_requester", err)
		}
	})

	t.Run("closed session", func(t *testing.T) {
		h, peer := newGrantHarness(t, peers.TrustCode, false)
		sid := h.openSession(peer, "r-22222222222222222222222222222222")
		if _, err := h.db.Exec(`UPDATE work_sessions SET state = 'closed', outcome = 'cancelled' WHERE id = ?`, sid); err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out)
		if got := ipcCode(err); got != CodeBadState {
			t.Fatalf("err = %v, want bad_state", err)
		}
	})

	t.Run("unverified_peer", func(t *testing.T) {
		h, peer := newGrantHarness(t, peers.TrustRelay, true)
		sid := h.openSession(peer, "r-33333333333333333333333333333333")
		var out map[string]any
		err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out)
		if got := ipcCode(err); got != CodeUnverifiedPeer {
			t.Fatalf("err = %v, want unverified_peer", err)
		}
	})

	forbidden := func(t *testing.T, resource string) {
		t.Helper()
		h, peer := newGrantHarness(t, peers.TrustCode, false)
		sid := h.openSession(peer, "r-44444444444444444444444444444444")
		var out map[string]any
		err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: resource}, &out)
		if got := ipcCode(err); got != CodeForbiddenResource {
			t.Fatalf("resource %q: err = %v, want forbidden_resource", resource, err)
		}
	}
	t.Run("relative path", func(t *testing.T) { forbidden(t, "relative/path") })
	t.Run("config dir", func(t *testing.T) {
		h, peer := newGrantHarness(t, peers.TrustCode, false)
		sid := h.openSession(peer, "r-55555555555555555555555555555555")
		var out map[string]any
		// h's own config dir (p.Dir from newGrantHarness) is inside dir/ep:
		// use it directly as the resource.
		cfgDir := filepath.Dir(h.endpoint) // endpoint lives directly in the config dir on Unix
		if _, statErr := os.Stat(cfgDir); statErr != nil {
			t.Skip("cannot resolve config dir on this platform for this check")
		}
		err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: cfgDir}, &out)
		if got := ipcCode(err); got != CodeForbiddenResource {
			t.Fatalf("err = %v, want forbidden_resource", err)
		}
	})
	t.Run("home dir", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home dir")
		}
		forbidden(t, home)
	})
	t.Run("filesystem root", func(t *testing.T) {
		root := string(filepath.Separator)
		if os.PathSeparator == '\\' {
			if wd, err := os.Getwd(); err == nil {
				root = filepath.VolumeName(wd) + `\`
			}
		}
		forbidden(t, root)
	})

	t.Run("missing branch", func(t *testing.T) {
		repo := testGitRepo(t)
		h, peer := newGrantHarness(t, peers.TrustCode, false)
		sid := h.openSession(peer, "r-66666666666666666666666666666666")
		var out map[string]any
		err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "git.read", Resource: repo + "#does-not-exist"}, &out)
		if got := ipcCode(err); got != CodeForbiddenResource {
			t.Fatalf("err = %v, want forbidden_resource", err)
		}
	})

	t.Run("expires over 7d", func(t *testing.T) {
		h, peer := newGrantHarness(t, peers.TrustCode, false)
		sid := h.openSession(peer, "r-77777777777777777777777777777777")
		var out map[string]any
		err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir, Expires: "192h"}, &out) // 8d
		if got := ipcCode(err); got != ipc.CodeBadRequest {
			t.Fatalf("err = %v, want bad_request", err)
		}
	})
}

// TestGrantPublicFSStillSensitive is OD-P2-13: --public on fs.read still
// ends up sensitive=true.
func TestGrantPublicFSStillSensitive(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	sid := h.openSession(peer, "r-88888888888888888888888888888888")
	var out struct {
		Grant GrantView `json:"grant"`
	}
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir, Public: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Grant.Sensitive {
		t.Error("fs.read with --public should still be sensitive")
	}
}

// TestGrantApprovalFlowPendingThenConfirm is the 2.2c approval-flow
// acceptance: pending until confirmed; a wrong/rejected code sends no mail;
// the right code activates the grant and sends the grant mail.
func TestGrantApprovalFlowPendingThenConfirm(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	sid := h.openSession(peer, "r-99999999999999999999999999999999")

	var out struct {
		Grant    GrantView     `json:"grant"`
		Approval approval.View `json:"approval"`
	}
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Grant.State != capability.StatePendingApproval || out.Approval.ID == "" {
		t.Fatalf("out = %+v, want pending_approval with an approval", out)
	}
	rec, err := h.caps.Get(context.Background(), out.Grant.ID)
	if err != nil || rec.State != capability.StatePendingApproval {
		t.Fatalf("stored grant = %+v, %v", rec, err)
	}

	// Reject: no mail, grant stays pending_approval (approval.Store never
	// calls Perform on Reject).
	var rejOut approval.View
	if err := h.call("approval_reject", struct{ ID string }{out.Approval.ID}, &rejOut); err != nil {
		t.Fatal(err)
	}
	if n := outboxRows(t, h.db, "grant"); n != 0 {
		t.Fatalf("outbox has %d grant rows after reject, want 0", n)
	}

	// A second grant_create, confirmed with the right code: mail is sent.
	var out2 struct {
		Grant    GrantView     `json:"grant"`
		Approval approval.View `json:"approval"`
	}
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out2); err != nil {
		t.Fatal(err)
	}
	code := h.notifier.code(t, out2.Approval.ID)
	var confirmOut map[string]json.RawMessage
	if err := h.call("approval_confirm", struct{ ID, Code string }{out2.Approval.ID, code}, &confirmOut); err != nil {
		t.Fatal(err)
	}
	rec2, err := h.caps.Get(context.Background(), out2.Grant.ID)
	if err != nil || rec2.State != capability.StateActive {
		t.Fatalf("stored grant after confirm = %+v, %v", rec2, err)
	}
	if n := outboxRows(t, h.db, "grant"); n != 1 {
		t.Fatalf("outbox has %d grant rows after confirm, want 1", n)
	}

	// grant.issue is audited once, through the approval's own transaction,
	// with only ids/enums (grant, peer, mail), per Docs/protocol/grant.md
	// §Audit.
	detail := lastAuditDetail(t, h.db, "grant.issue")
	if detail["grant"] != out2.Grant.ID || detail["peer"] != peer || detail["mail"] == "" {
		t.Fatalf("grant.issue detail = %+v, want grant=%s peer=%s mail=<id>", detail, out2.Grant.ID, peer)
	}
}

// lastAuditDetail returns the JSON detail of the most recent audit_events
// row with this action, decoded as a map of strings (every 2.2c grant.*
// detail member is an id or enum).
func lastAuditDetail(t *testing.T, db *sql.DB, action string) map[string]string {
	t.Helper()
	var raw string
	if err := db.QueryRow(`SELECT detail FROM audit_events WHERE action = ? ORDER BY id DESC LIMIT 1`, action).Scan(&raw); err != nil {
		t.Fatalf("no %s audit row: %v", action, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range m {
		switch x := v.(type) {
		case string:
			out[k] = x
		default:
			b, _ := json.Marshal(x)
			out[k] = string(b)
		}
	}
	return out
}

// TestGrantSessionCloseRevokesPendingApproval is the 2.2c acceptance:
// closing the session revokes every grant in the closing transaction,
// including pending_approval ones, and a later confirm attempt is rejected
// with reason precondition and sends no mail.
func TestGrantSessionCloseRevokesPendingApproval(t *testing.T) {
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

	if _, err := h.ws.Cancel(context.Background(), sid, "done"); err != nil {
		t.Fatal(err)
	}
	rec, err := h.caps.Get(context.Background(), out.Grant.ID)
	if err != nil || rec.State != capability.StateRevoked || rec.Reason != capability.ReasonSessionClosed {
		t.Fatalf("grant after session close = %+v, %v", rec, err)
	}

	code := h.notifier.code(t, out.Approval.ID)
	var confirmOut map[string]json.RawMessage
	err = h.call("approval_confirm", struct{ ID, Code string }{out.Approval.ID, code}, &confirmOut)
	if got := ipcCode(err); got != CodeBadState {
		t.Fatalf("confirm after close: err = %v, want bad_state (precondition)", err)
	}
	var state string
	if err := h.db.QueryRow(`SELECT state FROM approvals WHERE id = ?`, out.Approval.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "rejected" {
		t.Fatalf("approval state = %s, want rejected", state)
	}
	var lastRejectReason string
	if err := h.db.QueryRow(`SELECT detail FROM audit_events WHERE action = 'approval.reject' AND actor='daemon' ORDER BY id DESC LIMIT 1`).Scan(&lastRejectReason); err == nil {
		if !containsSubstr(lastRejectReason, `"reason":"precondition"`) {
			t.Errorf("approval.reject detail = %s, want reason precondition", lastRejectReason)
		}
	}
	if n := outboxRows(t, h.db, "grant"); n != 0 {
		t.Fatalf("outbox has %d grant rows, want 0 (no mail after a dropped approval)", n)
	}
}

// TestGrantApprovedAfterSessionLeftOpenIsDropped simulates the session
// leaving "open" (without closing: for example awaiting_result) while a
// grant approval is still pending. The precondition re-check must drop it
// and send no mail.
func TestGrantApprovedAfterSessionLeftOpenIsDropped(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	sid := h.openSession(peer, "r-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	var out struct {
		Grant    GrantView     `json:"grant"`
		Approval approval.View `json:"approval"`
	}
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`UPDATE work_sessions SET state = 'awaiting_result' WHERE id = ?`, sid); err != nil {
		t.Fatal(err)
	}
	code := h.notifier.code(t, out.Approval.ID)
	var confirmOut map[string]json.RawMessage
	err := h.call("approval_confirm", struct{ ID, Code string }{out.Approval.ID, code}, &confirmOut)
	if got := ipcCode(err); got != CodeBadState {
		t.Fatalf("err = %v, want bad_state", err)
	}
	rec, err := h.caps.Get(context.Background(), out.Grant.ID)
	if err != nil || rec.State != capability.StatePendingApproval {
		t.Fatalf("grant should be untouched (still pending_approval): %+v, %v", rec, err)
	}
	if n := outboxRows(t, h.db, "grant"); n != 0 {
		t.Fatalf("outbox has %d grant rows, want 0", n)
	}
}

// TestGrantPolicyAddNeedsApproval is the 2.2c acceptance: adding a policy
// needs a human approval.
func TestGrantPolicyAddNeedsApproval(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)

	var out struct {
		Policy   GrantPolicyView `json:"policy"`
		Approval approval.View   `json:"approval"`
	}
	if err := h.call("grant_policy_add", GrantPolicyAddParams{Peer: peer, Action: "fs.read", Resource: dir, MaxExpires: "2h"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Approval.ID == "" {
		t.Fatal("grant_policy_add did not create an approval")
	}
	pols, err := h.caps.PolicyList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pols) != 0 {
		t.Fatal("policy exists before approval")
	}
	code := h.notifier.code(t, out.Approval.ID)
	var confirmOut map[string]json.RawMessage
	if err := h.call("approval_confirm", struct{ ID, Code string }{out.Approval.ID, code}, &confirmOut); err != nil {
		t.Fatal(err)
	}
	pols, err = h.caps.PolicyList(context.Background())
	if err != nil || len(pols) != 1 {
		t.Fatalf("policies after confirm = %v, %v", pols, err)
	}

	// grant.policy_add is audited once, through the approval's own
	// transaction, with only ids/enums (policy, peer, action).
	detail := lastAuditDetail(t, h.db, "grant.policy_add")
	if detail["policy"] != pols[0].ID || detail["peer"] != peer || detail["action"] != "fs.read" {
		t.Fatalf("grant.policy_add detail = %+v, want policy=%s peer=%s action=fs.read", detail, pols[0].ID, peer)
	}
}

// TestGrantMatchingPolicyIssuesAtOnce is the 2.2c acceptance: a matching
// policy issues at once, no approval prompt, and the grant is active with
// mail sent.
func TestGrantMatchingPolicyIssuesAtOnce(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pol := capability.Policy{
		ID: capability.NewPolicyID(), Peer: peer, Action: capability.ActionFSRead, Path: resolved,
		MaxExpiresS: int((7 * 24 * time.Hour).Seconds()), Until: now.Add(30 * 24 * time.Hour), Created: now,
	}
	tx, _ := h.db.BeginTx(context.Background(), nil)
	if err := h.caps.PolicyInsertTx(context.Background(), tx, pol); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	sid := h.openSession(peer, "r-cccccccccccccccccccccccccccccccc")
	var out struct {
		Grant GrantView `json:"grant"`
	}
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Grant.State != capability.StateActive {
		t.Fatalf("grant state = %s, want active (policy match, no approval)", out.Grant.State)
	}
	if n := outboxRows(t, h.db, "grant"); n != 1 {
		t.Fatalf("outbox has %d grant rows, want 1", n)
	}
}

// TestGrantOfflineVerifyAndWidenedCaveatRejected is the 2.2 acceptance: a
// token issued by A verifies offline (holder steps 1-8, no network), and a
// widened-caveat token is rejected at step 4, both as holder and as grantor.
func TestGrantOfflineVerifyAndWidenedCaveatRejected(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pol := capability.Policy{
		ID: capability.NewPolicyID(), Peer: peer, Action: capability.ActionFSRead, Path: resolved,
		MaxExpiresS: int((7 * 24 * time.Hour).Seconds()), Until: now.Add(30 * 24 * time.Hour), Created: now,
	}
	tx, _ := h.db.BeginTx(context.Background(), nil)
	if err := h.caps.PolicyInsertTx(context.Background(), tx, pol); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit()

	sid := h.openSession(peer, "r-dddddddddddddddddddddddddddddddd")
	var out struct {
		Grant GrantView `json:"grant"`
	}
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out); err != nil {
		t.Fatal(err)
	}
	rec, err := h.caps.Get(context.Background(), out.Grant.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Offline verification, both roles, "relay stopped" simulated by not
	// touching any network at all: the holder verifies from the stored
	// canonical token, and the grantor recomputes from the same bytes.
	sessionOpen := func(id, requester, worker string) (bool, bool) {
		if id != sid || requester != h.self || worker != peer {
			return false, false
		}
		return true, true
	}
	g, verr := capability.Verify([]byte(rec.Token), capability.VerifyParams{
		Role: capability.RoleHolder, Self: peer, Counterparty: h.self, Now: now, SessionOpen: sessionOpen,
	})
	if verr != nil || g.ID != rec.ID {
		t.Fatalf("holder offline verify: %v", verr)
	}
	g2, verr := capability.Verify([]byte(rec.Token), capability.VerifyParams{
		Role: capability.RoleGrantor, Self: h.self, Counterparty: peer, Now: now, SessionOpen: sessionOpen,
	})
	if verr != nil || g2.ID != rec.ID {
		t.Fatalf("grantor offline verify: %v", verr)
	}

	// Widened caveat: change exp, keep the same sig.
	var top map[string]any
	if err := json.Unmarshal([]byte(rec.Token), &top); err != nil {
		t.Fatal(err)
	}
	grantObj := top["grant"].(map[string]any)
	// Still inside the nbf+7d format bound, so only step 4 (the signature)
	// should reject this, not step 2 (malformed).
	grantObj["exp"] = rec.Exp.Add(time.Hour).UTC().Format("2006-01-02T15:04:05Z")
	widened, err := json.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	if _, verr := capability.Verify(widened, capability.VerifyParams{
		Role: capability.RoleHolder, Self: peer, Counterparty: h.self, Now: now, SessionOpen: sessionOpen,
	}); capability.ReasonOf(verr) != capability.ReasonBadSignature {
		t.Fatalf("holder widened-caveat verify: reason = %q, want bad_signature", capability.ReasonOf(verr))
	}
	if _, verr := capability.Verify(widened, capability.VerifyParams{
		Role: capability.RoleGrantor, Self: h.self, Counterparty: peer, Now: now, SessionOpen: sessionOpen,
	}); capability.ReasonOf(verr) != capability.ReasonBadSignature {
		t.Fatalf("grantor widened-caveat verify: reason = %q, want bad_signature", capability.ReasonOf(verr))
	}
}

// TestPeersRemoveRevokesGrants is the 2.2c acceptance: peers remove revokes
// all of that peer's grants.
func TestPeersRemoveRevokesGrants(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	sid := h.openSession(peer, "r-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	var out struct {
		Grant GrantView `json:"grant"`
	}
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out); err != nil {
		t.Fatal(err)
	}
	if err := h.ps.Remove(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	rec, err := h.caps.Get(context.Background(), out.Grant.ID)
	if err != nil || rec.State != capability.StateRevoked || rec.Reason != capability.ReasonPeerRemoved {
		t.Fatalf("grant after peers remove = %+v, %v", rec, err)
	}
}

func outboxRows(t *testing.T, db *sql.DB, kind string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE kind = ?`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
