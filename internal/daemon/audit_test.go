package daemon_test

// Ticket 3.6b: the audit_list, audit_verify and audit_head IPC methods
// (Docs/protocol/audit.md §Verification, §agentnet log).

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

func auditList(t *testing.T, n *harnessNode, p audit.ListParams) []audit.Entry {
	t.Helper()
	var res daemon.AuditListResult
	n.call("audit_list", p, &res)
	return res.Events
}

func auditActions(evs []audit.Entry) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Action
	}
	return out
}

func hasAction(evs []audit.Entry, action string) bool {
	for _, e := range evs {
		if e.Action == action {
			return true
		}
	}
	return false
}

func TestAuditListVerifyHeadIPC(t *testing.T) {
	r := newHarnessRelay(t)
	a := newHarnessNode(t, "alice", r)
	a.start()

	evs := auditList(t, a, audit.ListParams{})
	if len(evs) < 2 || evs[0].Action != audit.ActionChainStart || evs[0].Hash == "" {
		t.Fatalf("audit_list must show every row, chain_start first: %v", auditActions(evs))
	}
	if got := auditList(t, a, audit.ListParams{Action: "daemon."}); len(got) != 1 || got[0].Action != audit.ActionDaemonStart {
		t.Fatalf("action prefix: %v", auditActions(got))
	}
	if got := auditList(t, a, audit.ListParams{Since: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}); len(got) != 0 {
		t.Fatalf("future since: %v", auditActions(got))
	}
	var page daemon.AuditListResult
	a.call("audit_list", audit.ListParams{Limit: 1}, &page)
	if len(page.Events) != 1 || page.NextAfterID != page.Events[0].ID {
		t.Fatalf("limit 1 = %+v", page)
	}
	a.call("audit_list", audit.ListParams{Limit: 1, AfterID: page.NextAfterID}, &page)
	if len(page.Events) != 1 || page.Events[0].ID != 2 {
		t.Fatalf("second page = %+v", page)
	}

	var head daemon.AuditHeadResult
	a.call("audit_head", nil, &head)
	var ver daemon.AuditVerifyResult
	a.call("audit_verify", nil, &ver)
	// The daemon appends its own rows in the background, so the head may have moved
	// between the two calls; it can only be at or past the one audit_head returned.
	if ver.Verify.Status != audit.StatusOK || head.Head == nil || ver.Verify.Head.ID < head.Head.ID {
		t.Fatalf("verify = %+v, head = %+v", ver.Verify, head.Head)
	}
	// Row 1 is audit.chain_start: its own hash is a valid anchor, a wrong one is tampering.
	a.call("audit_verify", daemon.AuditVerifyParams{Anchors: []string{"1:" + evs[0].Hash, head.Head.Anchor()}}, &ver)
	if ver.Verify.Status != audit.StatusOK {
		t.Fatalf("anchors of the real rows = %+v", ver.Verify)
	}
	a.call("audit_verify", daemon.AuditVerifyParams{Anchors: []string{"1:" + strings.Repeat("0", 64)}}, &ver)
	if ver.Verify.Status != audit.StatusBroken || ver.Verify.Reason != audit.ReasonAnchorMismatch || ver.Verify.FirstBad != 1 {
		t.Fatalf("wrong anchor = %+v", ver.Verify)
	}

	// Caller mistakes are bad_request.
	for method, params := range map[string]any{
		"audit_list":   audit.ListParams{Since: "yesterday"},
		"audit_verify": daemon.AuditVerifyParams{Anchors: []string{"nonsense"}},
	} {
		err := ipcCall(a, method, params, nil)
		var ie *ipc.Error
		if !asIPCError(err, &ie) || ie.Code != ipc.CodeBadRequest {
			t.Errorf("%s with bad params = %v, want bad_request", method, err)
		}
	}
	if err := ipcCall(a, "audit_list", audit.ListParams{Session: "nope"}, nil); err == nil {
		t.Error("audit_list with a session that is neither s- nor r- succeeded")
	}
}

func asIPCError(err error, target **ipc.Error) bool {
	ie, ok := err.(*ipc.Error) //nolint:errorlint // ipc.Call returns the error unwrapped
	if ok {
		*target = ie
	}
	return ok
}

// A session's view holds its request, grant, approval and ws.* rows and none of
// another session's (3.6b acceptance).
func TestAuditListSessionView(t *testing.T) {
	e := newQEnv(t)
	req1, sid1 := openSession(t, e.a, e.b, e.teamID, "first")
	req2, sid2 := openSession(t, e.a, e.b, e.teamID, "second")
	g1 := e.grant(t, sid1, "fs.read", qDir(t), false)
	g2 := e.grant(t, sid2, "fs.read", qDir(t), false)
	e.result(t, sid1, qMarkerResult("V1"))
	e.waitState(t, sid1, "quarantined")

	mentions := func(evs []audit.Entry, ids ...string) bool {
		for _, ev := range evs {
			for _, id := range ids {
				if strings.Contains(string(ev.Detail), `"`+id+`"`) {
					return true
				}
			}
		}
		return false
	}
	v1 := auditList(t, e.a, audit.ListParams{Session: sid1})
	v2 := auditList(t, e.a, audit.ListParams{Session: sid2})
	byReq := auditList(t, e.a, audit.ListParams{Session: req1})
	for _, want := range []string{"request.submit", "request.state", "grant.create", "grant.issue", "approval.create", "approval.approve", "ws.result_in"} {
		if !hasAction(v1, want) {
			t.Errorf("session view lacks %s: %v", want, auditActions(v1))
		}
	}
	if !mentions(v1, sid1, req1, g1) {
		t.Error("session 1 view does not mention its own ids")
	}
	if mentions(v1, sid2, req2, g2) {
		t.Errorf("session 1 view holds rows of session 2: %v", v1)
	}
	if mentions(v2, sid1, req1, g1) {
		t.Errorf("session 2 view holds rows of session 1: %v", v2)
	}
	if len(byReq) != len(v1) {
		t.Errorf("an r- id must resolve to its session: %d rows, want %d", len(byReq), len(v1))
	}
	seen := map[int64]bool{}
	for _, ev := range v1 {
		seen[ev.ID] = true
	}
	for _, ev := range v2 {
		if seen[ev.ID] {
			t.Errorf("row %d (%s) is in both session views", ev.ID, ev.Action)
		}
	}
}

// Review 44 L5 / review 43 M8 at the IPC level: while audit_verify is walking a
// log of several pages, another IPC call and a mail apply complete.
func TestAuditVerifyLetsIPCAndMailThrough(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a multi-page log")
	}
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)

	// Build about 4500 rows through a second handle (a second process appends the same way).
	st, err := store.Open(context.Background(), a.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	al := audit.New(st.DB())
	for i := 0; i < 4500; i++ {
		if err := al.Append(context.Background(), audit.ActorCLI, "x.bulk", map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	var (
		once    sync.Once
		paused  = make(chan struct{})
		release = make(chan struct{})
	)
	restore := audit.SetPageHook(func() {
		once.Do(func() {
			close(paused)
			<-release
		})
	})
	defer restore()

	type verifyOut struct {
		res daemon.AuditVerifyResult
		err error
	}
	done := make(chan verifyOut, 1)
	go func() {
		var o verifyOut
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		o.err = ipc.Call(ctx, a.p.Endpoint, "audit_verify", nil, &o.res)
		done <- o
	}()
	select {
	case <-paused:
	case <-time.After(30 * time.Second):
		close(release)
		t.Fatal("audit_verify never reached its first page")
	}

	// The walk is mid-way and must not hold A's only connection.
	var st2 daemon.StatusResult
	a.call("status", nil, &st2)
	a.call("audit_list", audit.ListParams{Limit: 1}, &daemon.AuditListResult{})
	before := a.count(`SELECT COUNT(*) FROM mail_seen`)
	b.submit(a.key, "note", "during-verify")
	harnessWait(t, "A to apply mail while audit_verify is walking", func() bool {
		return a.count(`SELECT COUNT(*) FROM mail_seen`) > before
	})
	close(release)

	select {
	case o := <-done:
		if o.err != nil || o.res.Verify.Status != audit.StatusOK || o.res.Verify.Rows < 4500 {
			t.Fatalf("audit_verify = %+v, %v", o.res.Verify, o.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("audit_verify did not finish")
	}
}

// Review 28 L9: a grant for a session the holder does not know is audited as
// grant.orphan with the grant id (only after the signature check, so the id is
// trustworthy), and the row is a well-formed JSON object.
func TestAuditGrantOrphanCarriesTheGrantID(t *testing.T) {
	e := newQEnv(t)
	_, sid := openSession(t, e.a, e.b, e.teamID, "orphan grant")
	if err := e.b.exec(`DELETE FROM work_sessions WHERE id = '` + sid + `'`); err != nil {
		t.Fatalf("drop B's session: %v", err)
	}
	gid, apprID := e.grantPending(t, sid, "fs.read", qDir(t), false)
	e.approve(t, apprID)

	harnessWait(t, "B's grant.orphan audit", func() bool {
		return e.b.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'grant.orphan'`) == 1
	})
	evs := auditList(t, e.b, audit.ListParams{Action: "grant.orphan"})
	var d map[string]string
	if len(evs) != 1 || json.Unmarshal(evs[0].Detail, &d) != nil || d["grant"] != gid || d["peer"] != e.a.key {
		t.Fatalf("grant.orphan = %+v, want {grant: %s, peer: A}", evs, gid)
	}
}
