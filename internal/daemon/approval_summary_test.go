package daemon

// R55-F5 acceptance on the grant harness (Docs/review/58-r55-f5-spec.md A6,
// A7, A10, A12, A13, A19).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/pathid"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func (h *grantHarness) peerFP(key string) string {
	h.t.Helper()
	fp, err := envelope.KeyFingerprint(key)
	if err != nil {
		h.t.Fatal(err)
	}
	return envelope.FormatFingerprint(fp)
}

func (h *grantHarness) rename(key, name string) {
	h.t.Helper()
	if _, err := h.db.Exec(`UPDATE peers SET name = ? WHERE public_key = ?`, name, key); err != nil {
		h.t.Fatal(err)
	}
}

func (h *grantHarness) body(id string) string {
	h.notifier.mu.Lock()
	defer h.notifier.mu.Unlock()
	return h.notifier.bodies[id]
}

type f5GrantOut struct {
	Grant    GrantView     `json:"grant"`
	Approval approval.View `json:"approval"`
}

// A6 (review 55 C11-01): the policy summary states the branch, the scope,
// PUBLIC, the maximum expiry, until (UTC and duration) and the fingerprint.
func TestPolicyApprovalSummary(t *testing.T) {
	repo := testGitRepo(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	var out struct {
		Approval approval.View `json:"approval"`
	}
	if err := h.call("grant_policy_add", GrantPolicyAddParams{Peer: peer, Action: "git.read", Resource: repo + "#main", Scope: "docs",
		Public: true, MaxExpires: "90m", Until: "2160h"}, &out); err != nil {
		t.Fatal(err)
	}
	resolved, err := pathid.Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	s := out.Approval.Summary
	for _, want := range []string{
		"Allow grants with no further question until ",
		" UTC (90 d): git.read to peer " + h.peerFP(peer) + ` named "peerb" on ` + displayQuoteForTest(resolved) + `, branch "main", only paths inside "docs", each grant for at most 1 h 30 min, PUBLIC grants only: results are NOT quarantined. Confirm only if you asked for exactly this.`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("policy summary lacks %q:\n%s", want, s)
		}
	}
}

// A7 (review 55 C11-06, C12-02): a raw card name reaches no summary and no
// notification: no bidi control, no decoy code.
func TestRawPeerNameReachesNoSummary(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	h.rename(peer, "Bob\u202e code 482913")
	sid := h.openSession(peer, "r-a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7")
	var g f5GrantOut
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &g); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Approval approval.View `json:"approval"`
	}
	if err := h.call("grant_policy_add", GrantPolicyAddParams{Peer: peer, Action: "fs.read", Resource: dir, MaxExpires: "2h"}, &p); err != nil {
		t.Fatal(err)
	}
	for _, v := range []approval.View{g.Approval, p.Approval} {
		body := h.body(v.ID)
		if !strings.HasPrefix(body, v.Summary+" Type this code only") {
			t.Fatalf("notification body is not the summary: %q", body)
		}
		for _, s := range []string{v.Summary, body} {
			if strings.ContainsRune(s, 0x202e) || strings.Contains(s, "482913") {
				t.Fatalf("%s: the raw name reached %q", v.Kind, s)
			}
		}
		if !strings.Contains(v.Summary, `named "Bob code …"`) {
			t.Fatalf("%s summary: %s", v.Kind, v.Summary)
		}
	}
}

// A10 (grant, grant_policy): a rename while the approval waits, or an
// UPDATE of approvals.summary, rejects the approval (precondition) and the
// action is not performed.
func TestGrantApprovalRejectedWhenTextChanges(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	sid := h.openSession(peer, "r-a10a10a10a10a10a10a10a10a10a10a1")
	grant := func() f5GrantOut {
		var out f5GrantOut
		if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	check := func(what string, out f5GrantOut) {
		t.Helper()
		if err := h.confirm(out.Approval.ID); !errors.Is(err, approval.ErrChanged) {
			t.Fatalf("%s: confirm err = %v, want ErrChanged", what, err)
		}
		rec, err := h.caps.Get(context.Background(), out.Grant.ID)
		if err != nil || rec.State == capability.StateActive {
			t.Fatalf("%s: grant %+v, %v", what, rec, err)
		}
		if n := outboxRows(t, h.db, "grant"); n != 0 {
			t.Fatalf("%s: %d grant mails", what, n)
		}
		var state string
		_ = h.db.QueryRow(`SELECT state FROM approvals WHERE id = ?`, out.Approval.ID).Scan(&state)
		if state != approval.StateRejected {
			t.Fatalf("%s: approval %s", what, state)
		}
	}

	out := grant()
	h.rename(peer, "peerb2")
	check("rename", out)

	out = grant()
	if _, err := h.db.Exec(`UPDATE approvals SET summary = 'Grant fs.read to a friend.' WHERE id = ?`, out.Approval.ID); err != nil {
		t.Fatal(err)
	}
	check("summary updated", out)

	// A19 (review 58a M3): the row no longer agrees with the signed token.
	for col, update := range map[string]string{
		"scope":     `UPDATE grants SET scope = 'src' WHERE id = ?`,
		"sensitive": `UPDATE grants SET sensitive = 0 WHERE id = ?`,
	} {
		out = grant()
		if _, err := h.db.Exec(update, out.Grant.ID); err != nil {
			t.Fatal(err)
		}
		check("row "+col, out)
	}

	var pol struct {
		Approval approval.View `json:"approval"`
	}
	if err := h.call("grant_policy_add", GrantPolicyAddParams{Peer: peer, Action: "fs.read", Resource: dir, MaxExpires: "2h"}, &pol); err != nil {
		t.Fatal(err)
	}
	h.rename(peer, "peerb3")
	if err := h.confirm(pol.Approval.ID); !errors.Is(err, approval.ErrChanged) {
		t.Fatalf("policy: confirm err = %v, want ErrChanged", err)
	}
	if pols, err := h.caps.PolicyList(context.Background()); err != nil || len(pols) != 0 {
		t.Fatalf("policy stored after a rejected approval: %v, %v", pols, err)
	}
}

// A13 (review 55 R55-148): on the daemon's terminal, a value that is not 6
// digits is not checked, uses no attempt and writes no approval.bad_code.
func TestTerminalTypoUsesNoAttempt(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	sid := h.openSession(peer, "r-a13a13a13a13a13a13a13a13a13a13a1")
	var out f5GrantOut
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out); err != nil {
		t.Fatal(err)
	}
	tag := out.Approval.ID[:len("a-")+6]
	for _, line := range []string{tag + " 48291", tag + " abcdef"} {
		var buf bytes.Buffer
		handleTerminalLine(context.Background(), &buf, h.appr, line)
		if !strings.Contains(buf.String(), "enter the 6-digit code") {
			t.Fatalf("%q: reply %q", line, buf.String())
		}
	}
	v, err := h.appr.Show(context.Background(), out.Approval.ID)
	if err != nil || v.AttemptsLeft != approval.MaxAttempts {
		t.Fatalf("attempts_left = %d, %v", v.AttemptsLeft, err)
	}
	var n int
	_ = h.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'approval.bad_code'`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d approval.bad_code rows", n)
	}
}

// A12 (review 55 R55-122): the session view lists the session's grants,
// with the label only in ws_show, and never the local path.
func TestSessionViewListsGrants(t *testing.T) {
	dir := testutil.TempDir(t)
	h, peer := newGrantHarness(t, peers.TrustCode, false)
	sid := h.openSession(peer, "r-a12a12a12a12a12a12a12a12a12a12a1")
	var out f5GrantOut
	if err := h.call("grant_create", GrantCreateParams{Peer: peer, Session: sid, Action: "fs.read", Resource: dir}, &out); err != nil {
		t.Fatal(err)
	}
	if err := h.confirm(out.Approval.ID); err != nil {
		t.Fatal(err)
	}
	rec, err := h.caps.Get(context.Background(), out.Grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	show := sessionGrants(context.Background(), h.db, sid, false)
	list := sessionGrants(context.Background(), h.db, sid, true)
	if len(show) != 1 || len(list) != 1 {
		t.Fatalf("grants: show %v, list %v", show, list)
	}
	g := show[0].(SessionGrantView)
	if g.ID != out.Grant.ID || g.Action != "fs.read" || !g.Sensitive || g.State != capability.StateActive || g.Exp == "" || g.Label != rec.Label {
		t.Fatalf("ws_show grant = %+v", g)
	}
	if l := list[0].(SessionGrantView); l.Label != "" {
		t.Fatalf("ws_list grant has a label: %+v", l)
	}
	for _, v := range [][]any{show, list} {
		raw, _ := json.Marshal(v)
		pathJSON, _ := json.Marshal(rec.Path)
		if strings.Contains(string(raw), strings.Trim(string(pathJSON), `"`)) || strings.Contains(string(raw), "token") {
			t.Fatalf("the view holds the path or the token: %s", raw)
		}
	}
}
