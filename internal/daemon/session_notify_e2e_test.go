package daemon_test

// D25: session.result on the requester and session.changes on the worker,
// through two live daemons. Both are content-free.

import (
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

const sessionNotifyTitle = "notify-title-zq"

// sessionNotifyEnv is two paired daemons that each record desktop notifications.
type sessionNotifyEnv struct {
	a, b           *harnessNode
	aShown, bShown *qShown
	teamID         string
}

func newSessionNotifyEnv(t *testing.T, quarantine bool) *sessionNotifyEnv {
	t.Helper()
	r := newHarnessRelay(t)
	e := &sessionNotifyEnv{aShown: &qShown{}, bShown: &qShown{}}
	e.a, e.b = newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	e.a.NotifyShow = e.aShown.show
	e.b.NotifyShow = e.bShown.show
	if quarantine {
		e.a.Quarantine = alwaysQuarantineDaemon
	}
	e.a.start()
	e.b.start()
	waitRelayConnected(t, r, e.a.key, e.b.key)
	harnessPair(t, e.a, e.b)
	e.teamID = harnessSharedTeam(t, e.a, e.b, "x")
	return e
}

// countShown counts notifications whose title line contains substr.
func countShown(s *qShown, substr string) int {
	n := 0
	for _, x := range s.all() {
		if strings.Contains(x, substr) {
			n++
		}
	}
	return n
}

func waitShown(t *testing.T, s *qShown, substr string, want int) {
	t.Helper()
	harnessWait(t, "notification "+substr, func() bool { return countShown(s, substr) >= want })
}

func TestSessionResultAndChangesNotify(t *testing.T) {
	const marker = "SNMARK-7f3"
	e := newSessionNotifyEnv(t, false)
	_, sid := openSession(t, e.a, e.b, e.teamID, sessionNotifyTitle)

	var res daemon.SessionResult
	e.b.call("ws_result", map[string]any{"id": sid, "result": map[string]any{
		"status": "pass", "summary": marker + "-summary", "output": marker + "-out", "verification": "tests_passed",
	}}, &res)
	waitShown(t, e.aShown, "result is ready for your review|"+sessionNotifyTitle, 1)

	var rc daemon.SessionResult
	e.a.call("ws_request_changes", map[string]any{"id": sid, "changes": marker + "-changes"}, &rc)
	waitShown(t, e.bShown, "asked for changes|"+sessionNotifyTitle, 1)

	// A second round yields a second result notification, and no more changes ones.
	e.b.call("ws_result", map[string]any{"id": sid, "result": map[string]any{
		"status": "pass", "summary": marker + "-two", "verification": "tests_passed",
	}}, &res)
	waitShown(t, e.aShown, "result is ready for your review", 2)

	time.Sleep(300 * time.Millisecond) // let any stray duplicate arrive
	if n := countShown(e.aShown, "ready for your review"); n != 2 {
		t.Errorf("A saw %d session.result notifications, want 2 (one per round): %v", n, e.aShown.all())
	}
	if n := countShown(e.bShown, "asked for changes"); n != 1 {
		t.Errorf("B saw %d session.changes notifications, want 1: %v", n, e.bShown.all())
	}
	if n := countShown(e.aShown, "asked for changes") + countShown(e.bShown, "ready for your review"); n != 0 {
		t.Errorf("an event reached the wrong side")
	}
	for _, s := range append(e.aShown.all(), e.bShown.all()...) {
		if strings.Contains(s, marker) {
			t.Fatalf("notification leaked content: %q", s)
		}
	}
}

// A result that lands quarantined fires session.quarantined only.
func TestSessionResultNotFiredWhenQuarantined(t *testing.T) {
	e := newSessionNotifyEnv(t, true)
	_, sid := openSession(t, e.a, e.b, e.teamID, sessionNotifyTitle)
	var res daemon.SessionResult
	e.b.call("ws_result", map[string]any{"id": sid, "result": map[string]any{
		"status": "pass", "summary": "s", "verification": "tests_passed",
	}}, &res)
	waitShown(t, e.aShown, "quarantined", 1)
	time.Sleep(300 * time.Millisecond)
	if n := countShown(e.aShown, "ready for your review"); n != 0 {
		t.Fatalf("session.result fired for a quarantined result: %v", e.aShown.all())
	}
}

func TestSessionResultToggleOff(t *testing.T) {
	e := newSessionNotifyEnv(t, false)
	var set daemon.NotifyGetResult
	e.a.call("notify_set", daemon.NotifySetParams{Events: map[string]bool{"session.result": false}}, &set)
	if set.Events["session.result"] {
		t.Fatalf("toggle not stored: %+v", set.Events)
	}
	_, sid := openSession(t, e.a, e.b, e.teamID, sessionNotifyTitle)
	var res daemon.SessionResult
	e.b.call("ws_result", map[string]any{"id": sid, "result": map[string]any{
		"status": "pass", "summary": "s", "verification": "tests_passed",
	}}, &res)
	harnessWait(t, "A to see awaiting_result", func() bool {
		return e.a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'awaiting_result'`) == 1
	})
	time.Sleep(500 * time.Millisecond)
	if n := countShown(e.aShown, "ready for your review"); n != 0 {
		t.Fatalf("session.result fired while off: %v", e.aShown.all())
	}
}
