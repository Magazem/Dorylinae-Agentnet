package daemon_test

// Ticket 1.4c acceptance (Docs/review/11-phase1-tickets.md, Docs/protocol/request.md).

import (
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// harnessSharedTeam creates a team owned by a, invites b, and waits until both
// sides see the two-member roster; returns the team id.
func harnessSharedTeam(t *testing.T, a, b *harnessNode, name string) string {
	t.Helper()
	var teamRes daemon.TeamResult
	a.call("team_create", daemon.TeamCreateParams{Name: name}, &teamRes)
	inv := harnessTeamInvite(t, a, teamRes.Team.ID)
	if inv.Code == "" {
		t.Fatalf("team invite = %+v, want a code", inv)
	}
	join := harnessTeamJoin(t, b, inv.Code)
	if join.State != "complete" {
		t.Fatalf("team join = %+v, want complete", join)
	}
	harnessWait(t, "both sides to see the roster", func() bool {
		return len(teamMemberKeys(t, a, teamRes.Team.ID)) == 2 && len(teamMemberKeys(t, b, teamRes.Team.ID)) == 2
	})
	return teamRes.Team.ID
}

// TestRequestFieldsIntactCiphertext is the 1.4 acceptance test: A requests B
// with every field set; B's `in` row has every field byte-identical after
// canonicalisation, and a relay-side tap sees only type:mail and no title or
// brief substring in any frame.
func TestRequestFieldsIntactCiphertext(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	const title = "Review the retry change"
	const brief = "What: review PR 12\nWhy: ship today\nDone when: approved"
	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "review", Team: teamID, Title: title, Brief: brief,
		Urgency: "high", UrgencyReason: "release today",
		Artifacts:      []daemon.ArtifactParam{{URL: "https://github.com/o/r/pull/12", Branch: "feat/x", Commit: "1a2b3c4"}},
		RequestedGrant: &daemon.GrantParam{Action: "repo.read", Resource: "github.com/o/r#feat/x"},
	}, &res)
	if res.ID == "" || res.Status != "queued" || res.MailID == "" {
		t.Fatalf("request_submit = %+v", res)
	}

	harnessWait(t, "B to store the in row", func() bool { return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in'`) == 1 })
	var gotTeam, gotType, gotUrgency, gotState, body string
	if err := b.query(`SELECT team_id, type, urgency, state, body FROM requests WHERE direction = 'in' LIMIT 1`,
		&gotTeam, &gotType, &gotUrgency, &gotState, &body); err != nil {
		t.Fatal(err)
	}
	if gotTeam != teamID || gotType != "review" || gotUrgency != "high" || gotState != "pending" {
		t.Errorf("B's in row = team %q type %q urgency %q state %q, want %q review high pending", gotTeam, gotType, gotUrgency, gotState, teamID)
	}
	if got := extractJSONString(body, "title"); got != title {
		t.Errorf("B's stored title = %q, want %q", got, title)
	}
	if got := extractJSONString(body, "brief"); got != brief {
		t.Errorf("B's stored brief = %q, want %q", got, brief)
	}
	if !strings.Contains(body, "1a2b3c4") || !strings.Contains(body, "repo.read") {
		t.Errorf("B's stored body missing artifact/grant fields: %s", body)
	}

	// The relay only ever saw a mail envelope: never the title or brief text.
	if strings.Contains(r.logs.String(), title) || strings.Contains(r.logs.String(), brief) {
		t.Error("relay log contains the request title or brief")
	}
}

// extractJSONString is a tiny helper: the exact value of a top-level string
// member in a canonical JSON object, without pulling in a decoder.
func extractJSONString(obj, key string) string {
	needle := `"` + key + `":"`
	i := strings.Index(obj, needle)
	if i < 0 {
		return ""
	}
	rest := obj[i+len(needle):]
	var b strings.Builder
	for j := 0; j < len(rest); j++ {
		if rest[j] == '"' && (j == 0 || rest[j-1] != '\\') {
			break
		}
		b.WriteByte(rest[j])
	}
	return strings.ReplaceAll(b.String(), `\n`, "\n")
}

// TestRequestOfflineQueued is the 1.9 acceptance test: with B stopped,
// agentnet request returns in under 2s with status: queued, daemon_online:
// false and a last_seen. B stops gracefully, so it sends a goodbye and A
// shows it offline at once (Docs/protocol/presence.md §Levels); without the
// goodbye A would show it online for up to 2.5 × interval.
func TestRequestOfflineQueued(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")
	harnessWait(t, "A to hear B online", func() bool {
		return a.count(`SELECT COUNT(*) FROM presence_peers WHERE key = '`+b.key+`' AND state = 'online'`) == 1
	})

	b.stop()
	harnessWait(t, "relay to see B leave", func() bool { return !r.rs.Connected(b.key) })
	// B's goodbye was forwarded before its connection closed; A stores it asynchronously.
	harnessWait(t, "A to store B's goodbye", func() bool {
		return a.count(`SELECT COUNT(*) FROM presence_peers WHERE key = '`+b.key+`' AND state = 'offline'`) == 1
	})

	begin := time.Now()
	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: "offline test", Brief: "What: nothing\n",
	}, &res)
	if d := time.Since(begin); d >= 2*time.Second {
		t.Errorf("request_submit took %v, want < 2s", d)
	}
	if res.Status != "queued" {
		t.Errorf("status = %q, want queued", res.Status)
	}
	if res.Peer.DaemonOnline {
		t.Error("peer.daemon_online = true, want false (B is stopped)")
	}
	if res.Peer.LastSeen == nil {
		t.Error("peer.last_seen = null, want B's goodbye time")
	}
	if n := a.count(`SELECT COUNT(*) FROM outbox WHERE kind = 'request' AND state IN ('queued', 'relayed')`); n != 1 {
		t.Errorf("undelivered request mails = %d, want 1", n)
	}
}

// TestRequestIdempotencyKey covers Docs/protocol/request.md §Idempotency: the
// same key and params returns the first request as a duplicate; different
// params with the same key is idempotency_conflict.
func TestRequestIdempotencyKey(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	params := daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: "idem test", Brief: "What: nothing\n",
		IdempotencyKey: "retry-1",
	}
	var first, second daemon.RequestSubmitResult
	a.call("request_submit", params, &first)
	if first.Duplicate {
		t.Fatalf("first submit reported duplicate: %+v", first)
	}
	a.call("request_submit", params, &second)
	if !second.Duplicate || second.ID != first.ID {
		t.Fatalf("second submit = %+v, want duplicate of %s", second, first.ID)
	}
	if n := a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out'`); n != 1 {
		t.Fatalf("out rows = %d, want 1", n)
	}

	params2 := params
	params2.Title = "a different title"
	err := ipcCallErr(a, "request_submit", params2, &daemon.RequestSubmitResult{})
	if errCode(err) != "idempotency_conflict" {
		t.Fatalf("different params with the same key: err = %v, want idempotency_conflict", err)
	}
}

// TestRequestSubmitNoSharedTeam: the sender-side half of the team policy rule
// (Docs/protocol/request.md §Submitting step 3): a request to a peer with no
// shared active team is refused locally with no_shared_team, and to a peer
// with several shared teams needs --team (ambiguous_team). The matching
// receiver-side auto-decline codes (unknown_team, not_team_member) are
// unreachable through a compliant sender and are covered at the unit level in
// internal/request (TestApplyAutoDecline*); the sender-mirror assertion for
// them is ticket 1.6a.
func TestRequestSubmitNoSharedTeam(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)

	err := ipcCallErr(a, "request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Title: "no shared team", Brief: "What: x\n",
	}, &daemon.RequestSubmitResult{})
	if errCode(err) != "no_shared_team" {
		t.Fatalf("submit with no shared team: err = %v, want no_shared_team", err)
	}

	harnessSharedTeam(t, a, b, "x")
	harnessSharedTeam(t, a, b, "y")
	err = ipcCallErr(a, "request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Title: "ambiguous team", Brief: "What: x\n",
	}, &daemon.RequestSubmitResult{})
	if errCode(err) != "ambiguous_team" {
		t.Fatalf("submit sharing two teams with no --team: err = %v, want ambiguous_team", err)
	}
}
