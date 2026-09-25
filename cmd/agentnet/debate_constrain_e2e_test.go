package main

// Ticket 3.4-i acceptance (the human-constraints/debate-IPC integration): a
// constraint added through `agentnet debate <id> --constrain`, confirmed in
// a fake approval window (never a real dialog), over two real daemons and a
// relay. Checks it lands in `agentnet debate <id> --json` on both sides and
// fires the debate.constraint desktop notification on the peer.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

// desktopCalls records the daemon's content-free desktop notifications (a
// test option: daemon.Options.NotifyShow), guarded by mu since notify.Trigger
// fires from an outbox/mail-apply goroutine (review 31).
type desktopCalls struct {
	mu    sync.Mutex
	shown []string
}

func (d *desktopCalls) show(_ context.Context, title, body string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.shown = append(d.shown, title+": "+body)
	return nil
}

func (d *desktopCalls) has(substr string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.shown {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// startApprovalNode is startNode (pair_test.go) plus a fake approval window
// and notifier and a captured desktop channel, so a CLI e2e test can drive
// an approval-gated command end to end without a real dialog or OS notifier.
func startApprovalNode(t *testing.T, name, relayURL string) (n *testNode, win *approveFakeWindow, notifier *approveFakeNotifier, desk *desktopCalls) {
	t.Helper()
	dir, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	ks, err := identity.NewKeystore(p.Dir, "file") // never touch the real keychain from tests
	if err != nil {
		t.Fatal(err)
	}
	w := &connectWatcher{ch: make(chan struct{})}
	win = newApproveFakeWindow()
	notifier = &approveFakeNotifier{}
	desk = &desktopCalls{}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.RunWithOptions(ctx, p, ready, daemon.Options{
			Logger:         slog.New(slog.NewTextHandler(w, nil)),
			Keystore:       ks,
			Identity:       &identity.Options{Name: name, Harness: "test-harness"},
			RelayURL:       relayURL,
			ApprovalWindow: win,
			ApprovalNotify: notifier,
			NotifyShow:     desk.show,
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon %s exited: %v", name, err)
	case <-time.After(10 * time.Second):
		t.Fatalf("daemon %s not ready", name)
	}
	var id daemon.IdentityResult
	cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
	defer ccancel()
	if err := ipc.Call(cctx, p.Endpoint, "identity", nil, &id); err != nil {
		t.Fatal(err)
	}
	return &testNode{p: p, key: id.Card.PublicKey, connected: w.ch}, win, notifier, desk
}

func TestDebateConstrainCLIE2E(t *testing.T) {
	oldInterval := waitPollInterval
	waitPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = oldInterval })

	srv := relay.New(relay.Options{})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http")
	a, awin, anotif, adesk := startApprovalNode(t, "alice", url)
	b, _, _, bdesk := startApprovalNode(t, "bob", url)
	a.waitRelay(t)
	b.waitRelay(t)

	// Pair a shared team (consultTeam's pairing, inlined: consultTeam starts
	// its own nodes, and this test needs nodes with fake approval windows).
	code, out, errs := cli(t, a, "team", "create", "backend", "--json")
	if code != exitOK {
		t.Fatalf("team create: %d %s %s", code, out, errs)
	}
	var created teamOut
	if err := json.Unmarshal([]byte(out), &created); err != nil || !created.OK {
		t.Fatalf("team create --json: %q: %v", out, err)
	}
	code, out, errs = cli(t, a, "team", "invite", created.Team.ID, "--json")
	if code != exitOK {
		t.Fatalf("team invite: %d %s %s", code, out, errs)
	}
	norm, ok := envelope.NormalizePairCodeV2(decodeTeamInvite(t, out).Code)
	if !ok {
		t.Fatalf("no v2 invite code in %q", out)
	}
	code, out, errs = cli(t, b, "team", "join", norm, "--json")
	if code != exitOK {
		t.Fatalf("team join: %d %s %s", code, out, errs)
	}
	var joined pairOut
	_ = json.Unmarshal([]byte(out), &joined)
	deadline := time.Now().Add(10 * time.Second)
	for joined.State == "pending" {
		if time.Now().After(deadline) {
			t.Fatalf("join never finished: %+v", joined)
		}
		time.Sleep(20 * time.Millisecond)
		_, out, _ = cli(t, b, "pair", "--status", joined.PairingID, "--json")
		_ = json.Unmarshal([]byte(out), &joined)
	}
	for _, n := range []*testNode{a, b} {
		for {
			_, out, _ := cli(t, n, "team", "show", created.Team.ID, "--json")
			var show teamShowOut
			if err := json.Unmarshal([]byte(out), &show); err == nil && show.OK && len(show.Team.Members) == 2 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("a node never saw the roster: %q", out)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Start a debate and get past "invited" (§Human constraints: constraints
	// are added in positions, rounds or converge).
	dir := t.TempDir()
	posA := writeJSONFile(t, dir, "posA.json", `{"claim":"Use capped backoff","argument":"Keeps retries bounded."}`)
	code, out, errs = cli(t, a, "debate", "@bob", "--topic", "How should the outbox retry?",
		"--position-file", posA, "--rounds", "1", "--json")
	if code != exitOK {
		t.Fatalf("debate start: %d %s %s", code, out, errs)
	}
	var sub requestBody
	if err := json.Unmarshal([]byte(out), &sub); err != nil || !sub.OK || sub.Session == "" {
		t.Fatalf("debate start --json = %q: %v", out, err)
	}
	pollCLI(t, b, "the debate invitation", func(o string) bool { return strings.Contains(o, sub.ID) }, "inbox", "--json")
	posB := writeJSONFile(t, dir, "posB.json", `{"claim":"Use a fixed retry interval","argument":"Simpler to reason about."}`)
	if code, out, errs := cli(t, b, "debate", sub.Session, "--position-file", posB, "--json"); code != exitOK {
		t.Fatalf("B one-step accept + position: %d %s %s", code, out, errs)
	}
	pollCLI(t, a, "A to leave invited", func(o string) bool { return !strings.Contains(o, `"phase":"invited"`) }, "debate", sub.Session, "--json")

	// The CLI path: --constrain creates a pending approval and stores nothing
	// yet (Docs/cli/debate.md §Human constraints).
	code, out, errs = cli(t, a, "debate", sub.Session, "--constrain", "No new dependency", "--json")
	if code != exitOK {
		t.Fatalf("debate --constrain: %d %s %s", code, out, errs)
	}
	var cbody struct {
		OK       bool          `json:"ok"`
		Approval approval.View `json:"approval"`
	}
	if err := json.Unmarshal([]byte(out), &cbody); err != nil || !cbody.OK || cbody.Approval.ID == "" {
		t.Fatalf("debate --constrain --json = %q: %v", out, err)
	}

	// Confirm through the fake window, as the human would.
	awin.answer(cbody.Approval.ID, "approve", anotif.lastCode(t))

	// It lands in `debate show` on both sides.
	pollCLI(t, a, "A to store the approved constraint", func(o string) bool { return strings.Contains(o, "No new dependency") }, "debate", sub.Session, "--json")
	out = pollCLI(t, b, "B to receive the constraint", func(o string) bool { return strings.Contains(o, "No new dependency") }, "debate", sub.Session, "--json")
	var shown struct {
		OK     bool `json:"ok"`
		Debate struct {
			Constraints []struct {
				ID     string `json:"id"`
				Author string `json:"author"`
				Text   string `json:"text"`
				State  string `json:"state"`
			} `json:"constraints"`
		} `json:"debate"`
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil || len(shown.Debate.Constraints) != 1 {
		t.Fatalf("B's debate show = %q: %v", out, err)
	}
	c := shown.Debate.Constraints[0]
	if c.Text != "No new dependency" || c.State != "active" {
		t.Fatalf("B's constraint = %+v", c)
	}

	// It fires the notification on the peer (B); A's own action never
	// notifies itself.
	deadline = time.Now().Add(5 * time.Second)
	for !bdesk.has("added a constraint") {
		if time.Now().After(deadline) {
			t.Fatalf("no debate.constraint notification reached B; shown = %v", bdesk.shown)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if adesk.has("added a constraint") {
		t.Fatal("A notified itself of its own constraint")
	}
}
