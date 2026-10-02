package daemon_test

// R55-F31 T4, T5 and T6: fault injection. A TEMP trigger on the daemon's one
// SQLite connection makes the audit insert of one action fail. A TEMP trigger
// exists only on the connection that created it; the daemon's store has one
// connection (SetMaxOpenConns(1)) and every audit append shares it, so the
// trigger sees every production insert. Each case first asserts the trigger is
// still there, so a recycled connection cannot turn a case into a silent pass.

import (
	"bytes"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// faultEnv is qEnv with a handle on alice's database connection and a buffer
// for the central audit_error log.
type faultEnv struct {
	*qEnv
	relay *harnessRelay
	db    *sql.DB
	mu    sync.Mutex
	log   bytes.Buffer
	dir   string
}

func (f *faultEnv) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.log.Write(p)
}

func (f *faultEnv) logText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.log.String()
}

func newFaultEnv(t *testing.T) *faultEnv {
	t.Helper()
	r := newHarnessRelay(t)
	f := &faultEnv{relay: r}
	e := &qEnv{appr: &qNotifier{}, win: newFakeWindowRunner(), shown: &qShown{}}
	e.a, e.b = newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	e.a.ApprovalNotify = e.appr
	e.a.ApprovalWindow = e.win
	e.a.NotifyShow = e.shown.show
	e.a.OnStoresReady = func(cs *capability.Store, _ *worksession.Store) { f.db = cs.DB }
	e.a.start()
	e.b.start()
	waitRelayConnected(t, r, e.a.key, e.b.key)
	harnessPair(t, e.a, e.b)
	e.teamID = harnessSharedTeam(t, e.a, e.b, "x")
	f.qEnv = e
	f.dir = qDir(t)
	// audit.SetErrorLog is process-wide and the last daemon started owns it:
	// point it at this test's buffer.
	audit.SetErrorLog(slog.New(slog.NewTextHandler(f, nil)))
	t.Cleanup(func() { audit.SetErrorLog(nil) })
	return f
}

// inject makes the audit insert of action fail: RAISE(ABORT) ends the statement
// only, RAISE(ROLLBACK) ends the whole transaction.
func (f *faultEnv) inject(t *testing.T, action, raise string) {
	t.Helper()
	f.clear(t)
	if _, err := f.db.Exec(fmt.Sprintf(`CREATE TEMP TRIGGER audit_fail BEFORE INSERT ON main.audit_events
WHEN NEW.action = '%s' BEGIN SELECT RAISE(%s, 'injected'); END`, action, raise)); err != nil {
		t.Fatal(err)
	}
	f.assertInjected(t)
}

func (f *faultEnv) clear(t *testing.T) {
	t.Helper()
	if _, err := f.db.Exec(`DROP TRIGGER IF EXISTS audit_fail`); err != nil {
		t.Fatal(err)
	}
}

func (f *faultEnv) assertInjected(t *testing.T) {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM sqlite_temp_master WHERE name = 'audit_fail'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the audit_fail trigger is gone (%d, %v): the connection was recycled", n, err)
	}
}

// waitFailed waits for the central log to hold one audit_error line for action.
func (f *faultEnv) waitFailed(t *testing.T, action string) {
	t.Helper()
	harnessWait(t, "the audit_error log line for "+action, func() bool {
		return strings.Contains(f.logText(), "event=audit_error action="+action+" ")
	})
}

func (f *faultEnv) rows(q string) int { return f.a.count(q) }

func (f *faultEnv) submitAccept(t *testing.T, title string) string {
	t.Helper()
	var sub daemon.RequestSubmitResult
	f.a.call("request_submit", daemon.RequestSubmitParams{To: f.b.key, Type: "task", Team: f.teamID, Title: title, Brief: "What: " + title + "\n"}, &sub)
	harnessWait(t, "B to see "+title, func() bool {
		return f.b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+sub.ID+`' AND state = 'pending'`) == 1
	})
	var acc daemon.RequestLifecycleResult
	f.b.call("request_accept", map[string]any{"id": sub.ID}, &acc)
	sid := acc.Request.Session.ID
	harnessWait(t, "A's session", func() bool {
		return f.a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'open'`) == 1
	})
	return sid
}

func grantState(f *faultEnv, id string) string {
	var s string
	if err := f.a.query(`SELECT state FROM grants WHERE id = '`+id+`'`, &s); err != nil {
		return "?"
	}
	return s
}

func approvalState(f *faultEnv, id string) string {
	var s string
	if err := f.a.query(`SELECT state FROM approvals WHERE id = '`+id+`'`, &s); err != nil {
		return "?"
	}
	return s
}

// TestAuditFaultInjection covers T4 (S rows abort their action), T5 (S- rows
// do not) and T6 (N rows only log).
func TestAuditFaultInjection(t *testing.T) {
	f := newFaultEnv(t)
	sid := f.submitAccept(t, "fault one")

	// A denied approval keeps the grant pending and sends nothing; once the
	// fault is gone the approval, still pending, goes through.
	grantDenied := func(t *testing.T, action string) {
		t.Helper()
		gid, apprID := f.grantPending(t, sid, "fs.read", f.dir, false)
		mails := f.rows(`SELECT COUNT(*) FROM outbox WHERE kind = 'grant'`)
		f.inject(t, action, "ABORT")
		f.approve(t, apprID)
		f.waitFailed(t, action)
		f.assertInjected(t)
		if s := grantState(f, gid); s != "pending_approval" {
			t.Errorf("grant state %q, want pending_approval", s)
		}
		if n := f.rows(`SELECT COUNT(*) FROM outbox WHERE kind = 'grant'`); n != mails {
			t.Errorf("%d grant mails queued, want none", n-mails)
		}
		if s := approvalState(f, apprID); s != "pending" {
			t.Errorf("approval state %q, want pending", s)
		}
		if n := f.rows(`SELECT COUNT(*) FROM audit_events WHERE action = 'approval.approve' AND instr(detail, '` + apprID + `') > 0`); n != 0 {
			t.Errorf("an approval.approve row exists although the action failed")
		}
		f.clear(t)
		f.approve(t, apprID)
		harnessWait(t, "the same code to approve it now", func() bool { return grantState(f, gid) == "active" })
		if n := f.rows(`SELECT COUNT(*) FROM audit_events WHERE action = 'approval.approve' AND instr(detail, '` + apprID + `') > 0`); n != 1 {
			t.Errorf("approval.approve rows %d, want 1", n)
		}
	}
	t.Run("T4.1 grant.issue", func(t *testing.T) { grantDenied(t, "grant.issue") })
	t.Run("T4.2 approval.approve", func(t *testing.T) { grantDenied(t, "approval.approve") })

	t.Run("T4.3 grant.create on the policy path", func(t *testing.T) {
		var pol approvalIDResult
		f.a.call("grant_policy_add", daemon.GrantPolicyAddParams{Peer: f.b.key, Action: "fs.read", Resource: f.dir, MaxExpires: "72h"}, &pol)
		f.approve(t, pol.Approval.ID)
		harnessWait(t, "the policy", func() bool { return f.rows(`SELECT COUNT(*) FROM grant_policies`) == 1 })
		before := f.rows(`SELECT COUNT(*) FROM grants`)
		mails := f.rows(`SELECT COUNT(*) FROM outbox WHERE kind = 'grant'`)
		f.inject(t, "grant.create", "ABORT")
		err := ipcCall(f.a, "grant_create", daemon.GrantCreateParams{Peer: f.b.key, Session: sid, Action: "fs.read", Resource: f.dir}, &struct{}{})
		if err == nil {
			t.Fatal("grant_create succeeded although its audit row failed")
		}
		f.assertInjected(t)
		if n := f.rows(`SELECT COUNT(*) FROM grants`); n != before {
			t.Errorf("grants %d -> %d: a row was left behind", before, n)
		}
		if n := f.rows(`SELECT COUNT(*) FROM outbox WHERE kind = 'grant'`); n != mails {
			t.Errorf("a grant mail was queued")
		}
		f.clear(t)
		var out struct {
			Grant daemon.GrantView `json:"grant"`
		}
		f.a.call("grant_create", daemon.GrantCreateParams{Peer: f.b.key, Session: sid, Action: "fs.read", Resource: f.dir}, &out)
		if grantState(f, out.Grant.ID) != "active" {
			t.Errorf("grant_create without the fault: %+v", out)
		}
		// Later cases need grants that wait for an approval.
		var polID string
		if err := f.a.query(`SELECT id FROM grant_policies`, &polID); err != nil {
			t.Fatal(err)
		}
		f.a.call("grant_policy_remove", map[string]any{"id": polID}, &struct{}{})
	})

	t.Run("T4.4 peer.verify", func(t *testing.T) {
		fp := mustFingerprint(t, f.b.key)
		var pv daemon.PeerVerifyResult
		f.a.call("peers_verify", daemon.PeerVerifyParams{Peer: f.b.key, Fingerprint: fp}, &pv)
		f.inject(t, "peer.verify", "ABORT")
		f.a.humanApprove(pv.Approval.ID)
		f.waitFailed(t, "peer.verify")
		var trust string
		if err := f.a.query(`SELECT trust FROM peers WHERE public_key = '`+f.b.key+`'`, &trust); err != nil || trust == "fingerprint" {
			t.Errorf("trust %q (%v) after a failed row", trust, err)
		}
		f.clear(t)
		f.a.humanApprove(pv.Approval.ID)
		harnessWait(t, "the trust to be raised", func() bool {
			var tr string
			return f.a.query(`SELECT trust FROM peers WHERE public_key = '`+f.b.key+`'`, &tr) == nil && tr == "fingerprint"
		})
	})

	t.Run("T4.6 ws.release", func(t *testing.T) {
		sid2 := f.submitAccept(t, "fault release")
		f.grant(t, sid2, "fs.read", f.dir, false)
		f.result(t, sid2, qMarkerResult("FAULT"))
		f.waitState(t, sid2, "quarantined")
		var rel approvalIDResult
		f.a.call("ws_release", map[string]any{"id": sid2}, &rel)
		stateMails := f.rows(`SELECT COUNT(*) FROM outbox WHERE kind = 'ws.state'`)
		f.inject(t, "ws.release", "ABORT")
		f.approve(t, rel.Approval.ID)
		f.waitFailed(t, "ws.release")
		f.assertInjected(t)
		var st string
		if err := f.a.query(`SELECT state FROM work_sessions WHERE id = '`+sid2+`'`, &st); err != nil || st != "quarantined" {
			t.Errorf("session state %q (%v), want quarantined", st, err)
		}
		if n := f.rows(`SELECT COUNT(*) FROM outbox WHERE kind = 'ws.state'`); n != stateMails {
			t.Errorf("a ws.state mail was queued")
		}
		f.clear(t)
		f.approve(t, rel.Approval.ID)
		f.waitState(t, sid2, "awaiting_result")
	})

	t.Run("T5.2 grant.revoke aborts only the row", func(t *testing.T) {
		gid := f.grant(t, sid, "fs.read", f.dir, false)
		f.inject(t, "grant.revoke", "ABORT")
		var res daemon.GrantRevokeResult
		f.a.call("grant_revoke", daemon.GrantRevokeParams{ID: gid}, &res)
		f.waitFailed(t, "grant.revoke")
		if s := grantState(f, gid); s != "revoked" {
			t.Errorf("grant state %q, want revoked", s)
		}
		if n := f.rows(`SELECT COUNT(*) FROM audit_events WHERE action = 'grant.revoke' AND instr(detail, '` + gid + `') > 0`); n != 0 {
			t.Errorf("a grant.revoke row exists although it was injected to fail")
		}
	})

	t.Run("T5.4 a lost transaction retries without the row", func(t *testing.T) {
		gid := f.grant(t, sid, "fs.read", f.dir, false)
		before := strings.Count(f.logText(), "event=audit_error action=grant.revoke ")
		f.inject(t, "grant.revoke", "ROLLBACK")
		var res daemon.GrantRevokeResult
		f.a.call("grant_revoke", daemon.GrantRevokeParams{ID: gid}, &res)
		harnessWait(t, "a second audit_error line", func() bool {
			return strings.Count(f.logText(), "event=audit_error action=grant.revoke ") > before
		})
		if s := grantState(f, gid); s != "revoked" {
			t.Errorf("grant state %q after the lost transaction, want revoked", s)
		}
		f.assertInjected(t)
		f.clear(t)
	})

	t.Run("T6 N rows only log", func(t *testing.T) {
		keyOfB := f.b.key
		// request.submit
		f.inject(t, "request.submit", "ABORT")
		var sub daemon.RequestSubmitResult
		f.a.call("request_submit", daemon.RequestSubmitParams{To: keyOfB, Type: "task", Team: f.teamID, Title: "n row", Brief: "What: n\n"}, &sub)
		f.waitFailed(t, "request.submit")
		if n := f.rows(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = '` + sub.ID + `'`); n != 1 {
			t.Errorf("the request is not queued (%d rows)", n)
		}
		// presence.mode
		f.inject(t, "presence.mode", "ABORT")
		f.a.call("presence_set", daemon.PresenceSetParams{Mode: "invisible"}, &struct{}{})
		f.waitFailed(t, "presence.mode")
		f.clear(t)
		f.a.call("presence_set", daemon.PresenceSetParams{Mode: "visible"}, &struct{}{})
		// team.member_remove
		var tr daemon.TeamResult
		f.a.call("team_create", daemon.TeamCreateParams{Name: "n-team"}, &tr)
		if j := harnessTeamJoin(t, f.b, harnessTeamInvite(t, f.a, tr.Team.ID).Code); j.State != "complete" {
			t.Fatalf("join: %+v", j)
		}
		harnessWait(t, "the roster on both sides", func() bool {
			return len(teamMemberKeys(t, f.a, tr.Team.ID)) == 2 && len(teamMemberKeys(t, f.b, tr.Team.ID)) == 2
		})
		rosters := f.rows(`SELECT COUNT(*) FROM outbox WHERE kind = 'team.roster'`)
		f.inject(t, "team.member_remove", "ABORT")
		f.a.call("team_remove", daemon.TeamRemoveParams{Team: tr.Team.ID, Peer: keyOfB}, &struct{}{})
		f.waitFailed(t, "team.member_remove")
		if n := f.rows(`SELECT COUNT(*) FROM outbox WHERE kind = 'team.roster'`); n <= rosters {
			t.Errorf("the roster was not broadcast after a failed row (%d -> %d)", rosters, n)
		}
		f.clear(t)
		// Each failure is one line with the action and the driver error, and
		// no detail (no key, no id).
		for _, line := range strings.Split(f.logText(), "\n") {
			if !strings.Contains(line, "event=audit_error") {
				continue
			}
			if strings.Contains(line, keyOfB) || strings.Contains(line, sub.ID) {
				t.Errorf("an audit_error line carries detail: %s", line)
			}
		}
	})

	t.Run("T4.8 team.invite_issued", func(t *testing.T) {
		var tr daemon.TeamResult
		f.a.call("team_create", daemon.TeamCreateParams{Name: "inv-team"}, &tr)
		var res daemon.TeamInviteResult
		f.a.call("team_invite", daemon.TeamInviteParams{Team: tr.Team.ID}, &res)
		if res.Approval == nil {
			t.Fatalf("team_invite without an approval: %+v", res)
		}
		id := res.Approval.ID
		f.a.humanApprove(id)
		harnessWait(t, "the approval to be approved", func() bool { return approvalState(f, id) == "approved" })
		issued := f.rows(`SELECT COUNT(*) FROM audit_events WHERE action = 'team.invite_issued'`)
		f.inject(t, "team.invite_issued", "ABORT")
		err := ipcCall(f.a, "team_invite", daemon.TeamInviteParams{Team: tr.Team.ID, Approval: id}, &res)
		if err == nil {
			t.Fatal("team_invite returned a code although its audit row failed")
		}
		f.waitFailed(t, "team.invite_issued")
		f.assertInjected(t)
		if n := f.rows(`SELECT COUNT(*) FROM audit_events WHERE action = 'team.invite_issued'`); n != issued {
			t.Errorf("a team.invite_issued row was written (%d -> %d)", issued, n)
		}
		harnessWait(t, "the cancelled pairing's pair.fail row", func() bool {
			return f.rows(`SELECT COUNT(*) FROM audit_events WHERE action = 'pair.fail' AND instr(detail, '"code":"cancelled"') > 0`) == 1
		})
		var pid string
		if err := f.a.query(`SELECT json_extract(detail, '$.id') FROM audit_events WHERE action = 'pair.fail' AND instr(detail, '"code":"cancelled"') > 0`, &pid); err != nil {
			t.Fatal(err)
		}
		var st daemon.PairStatus
		f.a.call("pair_status", daemon.PairStatusParams{PairingID: pid}, &st)
		if st.State != "failed" || st.Code != "" {
			t.Errorf("pairing %s: state %q code %q, want failed with no code", pid, st.State, st.Code)
		}
		f.clear(t)
	})

	t.Run("T4.5 pair.complete", func(t *testing.T) {
		c := newHarnessNode(t, "carol", f.relay)
		c.start()
		waitRelayConnected(t, f.relay, f.a.key, c.key)
		f.inject(t, "pair.complete", "ABORT")
		var issued daemon.PairStatus
		f.a.call("pair_new", nil, &issued)
		harnessWait(t, "pairing code", func() bool {
			f.a.call("pair_status", daemon.PairStatusParams{PairingID: issued.ID}, &issued)
			return issued.Code != ""
		})
		var red daemon.PairStatus
		c.call("pair_redeem", daemon.PairRedeemParams{Code: issued.Code}, &red)
		harnessWait(t, "A's pairing to fail", func() bool {
			f.a.call("pair_status", daemon.PairStatusParams{PairingID: issued.ID}, &issued)
			return issued.State == "failed"
		})
		if issued.Error == nil || issued.Error.Code != "store_error" {
			t.Errorf("A's pairing: %+v, want store_error", issued)
		}
		if n := f.rows(`SELECT COUNT(*) FROM peers WHERE public_key = '` + c.key + `'`); n != 0 {
			t.Errorf("A stored the peer although pair.complete failed")
		}
		f.assertInjected(t)
		f.clear(t)
	})
}

func mustFingerprint(t *testing.T, key string) string {
	t.Helper()
	fp, err := fingerprintOf(key)
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func fingerprintOf(key string) (string, error) {
	raw, err := envelope.KeyFingerprint(key)
	if err != nil {
		return "", err
	}
	return envelope.FormatFingerprint(raw), nil
}
