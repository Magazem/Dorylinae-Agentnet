package daemon_test

// Ticket 3.6b: TestAuditInventory (Docs/protocol/audit.md §Scope: every daemon
// action). Every IPC method and every registered mail kind is listed with the
// audit action its use documents; the e2e scenarios below call each
// state-changing method, and the test asserts the action is in the audit log of
// the node that runs it (or of the peer, for what the peer applies). Methods
// that only read are listed as exempt. A method or mail kind that is added
// without an entry here fails TestAuditInventoryIsComplete, so a new action
// cannot skip the audit question.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// invEntry is one row of the inventory. Exempt methods only read. For the
// others, Actions lists audit actions of which at least one must appear (a
// method can audit differently depending on the state it meets), on the node
// named in On: "A" (the caller in the scenario), "B" (its peer) or "any".
type invEntry struct {
	Exempt  bool
	Actions []string
	On      string
	// Harness marks methods the shared scenario setup (harnessPair,
	// harnessSharedTeam) calls itself; the action is still asserted.
	Harness bool
}

func exempt() invEntry                           { return invEntry{Exempt: true} }
func acts(on string, a ...string) invEntry       { return invEntry{Actions: a, On: on} }
func viaHarness(on string, a ...string) invEntry { return invEntry{Actions: a, On: on, Harness: true} }

// methodInventory: every method registered with srv.Handle.
var methodInventory = map[string]invEntry{
	// Read only.
	"approval_list": exempt(), "device_list": exempt(), "device_scope_show": exempt(), "fetch_status": exempt(),
	"grant_list": exempt(), "grant_policy_list": exempt(), "grant_show": exempt(), "identity": exempt(),
	"inbox_list": exempt(), "notify_get": exempt(), "notify_test": exempt(), // notify_test sends one probe; only its failure is audited (notify.fail)
	"pair_status": exempt(), "peers": exempt(),
	"ping_status": exempt(), "presence_get": exempt(), "request_list": exempt(), "request_show": exempt(),
	"status": exempt(), "team_list": exempt(), "team_show": exempt(), "ws_list": exempt(), "ws_show": exempt(),
	"audit_list": exempt(), "audit_verify": exempt(), "audit_head": exempt(),
	// shutdown audits daemon.stop_requested, but calling it would stop the shared
	// scenario's daemons; TestShutdownIPC (daemon_test.go) asserts that row.
	"shutdown": exempt(),
	"debate_list": exempt(), "debate_show": exempt(),

	// Approvals.
	"approval_open":   acts("A", "approval.open"),
	"approval_reject": acts("A", "approval.reject"),

	// Pairing and trust.
	"pair_new":     viaHarness("any", "pair.start"),
	"pair_redeem":  viaHarness("any", "pair.complete"),
	"peers_verify": acts("A", "peer.verify"),
	"peers_remove": acts("A", "peer.remove"),
	"ping":         acts("A", "session.open"),

	// Mail and presence and notifications.
	"mail_submit":  acts("B", "mail.in"),
	"presence_set": acts("A", "presence.mode", "presence.human"),
	"notify_set":   acts("A", "notify.config"),

	// Teams.
	"team_create": viaHarness("A", "team.create"),
	"team_invite": viaHarness("A", "team.invite"),
	"team_join":   viaHarness("B", "team.join"),
	"team_rename": acts("A", "team.rename"),
	"team_remove": acts("A", "team.member_remove"),
	"team_leave":  acts("B", "team.leave"),
	"team_delete": acts("A", "team.delete"),

	// Requests.
	"request_submit":   acts("A", "request.submit"),
	"request_accept":   acts("B", "request.accept"),
	"request_decline":  acts("B", "request.decline"),
	"request_defer":    acts("B", "request.defer"),
	"request_cancel":   acts("A", "request.cancel"),
	"request_resend":   acts("A", "request.resend"),
	"request_complete": acts("B", "request.complete", "ws.result"),

	// Work sessions.
	"ws_result":          acts("B", "ws.result"),
	"ws_release":         acts("A", "ws.release"),
	"ws_request_changes": acts("A", "ws.request_changes"),
	"ws_accept_result":   acts("A", "ws.accept_result"),
	"ws_discard":         acts("A", "ws.discard"),
	"ws_cancel":          acts("A", "ws.cancel"),

	// Grants and fetch.
	"grant_create":        acts("A", "grant.create"),
	"grant_revoke":        acts("A", "grant.revoke"),
	"grant_policy_add":    acts("A", "grant.policy_add"),
	"grant_policy_remove": acts("A", "grant.policy_remove"),
	"fetch_start":         acts("A", "grant.fetch"),

	// Debates (3.1b): B's debate_submit audits debate.entry, whether it is an
	// ordinary entry or the one-step accept + position.
	"debate_submit": acts("B", "debate.entry"),

	// Own devices.
	"device_link":        acts("A", "device.link_intent"),
	"device_unlink":      acts("A", "device.unlink"),
	"device_scope_set":   acts("B", "device.scope_set"),
	"device_scope_clear": acts("B", "device.scope_clear"),
}

// mailKindInventory: every mail kind the daemon registers, with the action the
// receiving daemon audits when it applies one.
var mailKindInventory = map[string]invEntry{
	"keys":              exempt(), // no mail.in by design (mail.md §Receiver); rotation is audited as mailbox.rotate
	"team.roster":       acts("B", "team.roster_apply"),
	"team.join":         acts("A", "team.member_add"),
	"team.leave":        acts("A", "team.member_leave"),
	"request":           acts("B", "request.in"),
	"request.accept":    acts("A", "request.state"),
	"request.decline":   acts("A", "request.state"),
	"request.defer":     acts("A", "request.state"),
	"request.complete":  acts("A", "request.state"),
	"request.cancelled": acts("A", "request.state"),
	"request.cancel":    acts("B", "request.cancel_in"),
	"ws.result":         acts("A", "ws.result_in"),
	"ws.state":          acts("B", "ws.state"),
	"ws.cancel":         acts("A", "ws.cancel_in"),
	"grant":             acts("B", "grant.in"),
	"grant.revoke":      acts("B", "grant.revoked_in"),
	"device.link":       acts("any", "device.link_active"),
	"device.unlink":     acts("B", "device.unlink"),
}

var (
	handleRE   = regexp.MustCompile(`srv\.Handle\("([a-z_]+)"`)
	kindsRE    = regexp.MustCompile(`kinds\["([a-z.]+)"\]`)
	kindConsRE = regexp.MustCompile(`(?m)^\s*Kind[A-Za-z]*\s+=\s+"([a-z.]+)"`)
	keysKindRE = regexp.MustCompile(`"([a-z]+)":\s+mail\.KeysKind`)
)

// sourceNames scans the non-test Go files of the given directories.
func sourceNames(t *testing.T, re *regexp.Regexp, dirs ...string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no sources in %s: %v", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f) //nolint:gosec // test scanning the repo's own sources
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				out[m[1]] = true
			}
		}
	}
	return out
}

// TestAuditInventoryIsComplete: the inventory names exactly the methods and mail
// kinds the code registers.
func TestAuditInventoryIsComplete(t *testing.T) {
	methods := sourceNames(t, handleRE, ".")
	kinds := sourceNames(t, kindsRE, ".")
	for k := range sourceNames(t, kindConsRE, "../request", "../worksession", "../device") {
		kinds[k] = true
	}
	for k := range sourceNames(t, keysKindRE, ".") {
		kinds[k] = true
	}
	kinds["request"] = true // request.Kind() is registered as kinds["request"]
	compare := func(what string, code map[string]bool, inv map[string]invEntry) {
		for name := range code {
			if _, ok := inv[name]; !ok {
				t.Errorf("%s %q is registered but missing from the audit inventory", what, name)
			}
		}
		for name := range inv {
			if !code[name] {
				t.Errorf("audit inventory lists %s %q, which the code does not register", what, name)
			}
		}
	}
	compare("IPC method", methods, methodInventory)
	compare("mail kind", kinds, mailKindInventory)
}

// invRun records which methods a scenario called.
type invRun struct {
	mu     sync.Mutex
	called map[string]bool
}

func newInvRun() *invRun { return &invRun{called: map[string]bool{}} }

func (r *invRun) call(n *harnessNode, method string, params, out any) {
	n.t.Helper()
	n.call(method, params, out)
	r.mu.Lock()
	r.called[method] = true
	r.mu.Unlock()
}

// auditHas reports whether n's audit log has an action.
func auditHas(n *harnessNode, action string) bool {
	return n.count(fmt.Sprintf(`SELECT COUNT(*) FROM audit_events WHERE action = '%s'`, action)) > 0
}

// waitAuditAny polls the nodes' audit logs (peers apply mail asynchronously)
// until one holds one of the actions, and reports whether it did.
func waitAuditAny(nodes []*harnessNode, actions []string) bool {
	deadline := time.Now().Add(20 * time.Second)
	for {
		for _, n := range nodes {
			for _, a := range actions {
				if auditHas(n, a) {
					return true
				}
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// checkInventory asserts, for each entry (a method the scenario must have
// called, or a mail kind, which the scenario only causes), that one of its
// actions is in the audit log where the entry says.
func checkInventory(t *testing.T, run *invRun, inv map[string]invEntry, names []string, needCalled bool, a, b *harnessNode) {
	t.Helper()
	for _, name := range names {
		e := inv[name]
		if e.Exempt {
			continue
		}
		run.mu.Lock()
		called := run.called[name]
		run.mu.Unlock()
		if needCalled && !e.Harness && !called {
			t.Errorf("%s: the scenario never called it", name)
			continue
		}
		nodes := []*harnessNode{a, b}
		switch e.On {
		case "A":
			nodes = []*harnessNode{a}
		case "B":
			nodes = []*harnessNode{b}
		}
		if !waitAuditAny(nodes, e.Actions) {
			t.Errorf("%s: none of %v in the audit log of %s", name, e.Actions, e.On)
		}
	}
}

// sortedKeys returns the inventory names, device ones or the rest.
func sortedKeys(m map[string]invEntry, device bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if strings.HasPrefix(k, "device") == device {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
