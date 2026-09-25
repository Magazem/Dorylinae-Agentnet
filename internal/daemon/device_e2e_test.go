package daemon_test

// Ticket 2.D1 acceptance (Docs/review/23-phase2-tickets.md "2.D1 Device link",
// Docs/protocol/device.md): the own-device link between two real daemons and a
// real relay, with approvals confirmed through a fake approval window and the
// link's clock injected (Options.DeviceNow).

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

type devClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *devClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *devClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// devNode is a harness daemon with a fake approval notifier and window and an
// injected device clock.
type devNode struct {
	*harnessNode
	clk      *devClock
	notifier *fakeApprovalNotifier
	win      *fakeWindowRunner
	shown    *qShown // desktop notifications (D22: device.linked)
}

func newDevNode(t *testing.T, name string, r *harnessRelay) *devNode {
	t.Helper()
	d := &devNode{harnessNode: newHarnessNode(t, name, r), clk: &devClock{t: time.Now()},
		notifier: &fakeApprovalNotifier{}, win: newFakeWindowRunner(), shown: &qShown{}}
	d.ApprovalNotify = d.notifier
	d.ApprovalWindow = d.win
	return d
}

// start is harnessNode.start with DeviceNow set.
func (d *devNode) start() {
	d.t.Helper()
	ks, err := identity.NewKeystore(d.p.Dir, "file")
	if err != nil {
		d.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.RunWithOptions(ctx, d.p, ready, daemon.Options{
			Keystore:       ks,
			Identity:       &identity.Options{Name: d.name, Harness: "test-harness"},
			RelayURL:       d.relay.url(),
			Logger:         slog.New(slog.NewTextHandler(d.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
			MailKinds:      map[string]mail.Kind{"note": {Inbox: true}},
			ApprovalNotify: d.notifier,
			ApprovalWindow: d.win,
			DeviceNow:      d.clk.Now,
			DeviceRunWatch: 100 * time.Millisecond,
			NotifyShow:     d.shown.show,
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		d.t.Fatalf("daemon %s exited: %v", d.name, err)
	case <-time.After(15 * time.Second):
		cancel()
		d.t.Fatalf("daemon %s not ready", d.name)
	}
	d.cancel, d.done = cancel, done
	if d.key == "" {
		var id daemon.IdentityResult
		d.call("identity", nil, &id)
		d.key = id.Card.PublicKey
	}
}

func devFP(t *testing.T, n *devNode) string {
	t.Helper()
	fp, err := envelope.KeyFingerprint(n.key)
	if err != nil {
		t.Fatal(err)
	}
	return envelope.FormatFingerprint(fp)
}

func (d *devNode) linkCount(state string) int {
	d.t.Helper()
	return d.count(fmt.Sprintf(`SELECT COUNT(*) FROM device_links WHERE state = '%s'`, state))
}

func (d *devNode) linkWith(peer *devNode, state string) int {
	d.t.Helper()
	return d.count(fmt.Sprintf(`SELECT COUNT(*) FROM device_links WHERE peer = '%s' AND state = '%s'`, peer.key, state))
}

func (d *devNode) activeID() string {
	d.t.Helper()
	var id string
	if err := d.query(`SELECT id FROM device_links WHERE state = 'active'`, &id); err != nil {
		d.t.Fatalf("%s has no active link: %v", d.name, err)
	}
	return id
}

func (d *devNode) auditCount(action string) int {
	d.t.Helper()
	return d.count(fmt.Sprintf(`SELECT COUNT(*) FROM audit_events WHERE action = '%s'`, action))
}

// requestLink runs device_link and returns the raw error, if any.
func (d *devNode) requestLink(peer *devNode, role, fp string) (daemon.DeviceLinkResult, error) {
	d.t.Helper()
	var res daemon.DeviceLinkResult
	err := ipcCallErr(d.harnessNode, "device_link", daemon.DeviceLinkParams{Peer: peer.key, As: role, Fingerprint: fp}, &res)
	return res, err
}

// link runs device_link with the right fingerprint and confirms its approval
// in the fake window, as the human on this device would.
func (d *devNode) link(peer *devNode, role string) {
	d.t.Helper()
	res, err := d.requestLink(peer, role, devFP(d.t, peer))
	if err != nil {
		d.t.Fatalf("%s: device_link as %s: %v", d.name, role, err)
	}
	if res.Approval.ID == "" || res.Link.State != "pending_approval" || res.Link.Role != role {
		d.t.Fatalf("%s: device_link = %+v", d.name, res)
	}
	d.win.answer(res.Approval.ID, "approve", d.notifier.lastCode(d.t))
	harnessWait(d.t, d.name+"'s approval to be applied", func() bool { return d.linkCount("pending_approval") == 0 })
}

// activatePair links ctrl as controller of help, both confirming, and returns
// the link id once both sides are active.
func activatePair(t *testing.T, ctrl, help *devNode) string {
	t.Helper()
	ctrl.link(help, "controller")
	help.link(ctrl, "helper")
	harnessWait(t, "both sides to be active", func() bool { return ctrl.linkCount("active") == 1 && help.linkCount("active") == 1 })
	id := ctrl.activeID()
	if id != help.activeID() {
		t.Fatalf("link ids differ: %s vs %s", id, help.activeID())
	}
	return id
}

func newDevPair(t *testing.T) (*devNode, *devNode) {
	t.Helper()
	r := newHarnessRelay(t)
	a, b := newDevNode(t, "laptop", r), newDevNode(t, "desktop", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a.harnessNode, b.harnessNode)
	return a, b
}

// Both sides confirm, in either order: both active with the same link id, the
// peer verified, and an offer alone created nothing.
func TestDeviceLinkBothConfirmEitherOrder(t *testing.T) {
	for _, controllerFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("controllerFirst=%v", controllerFirst), func(t *testing.T) {
			ctrl, help := newDevPair(t)
			first, second := ctrl, help
			firstRole, secondRole := "controller", "helper"
			if !controllerFirst {
				first, second = help, ctrl
				firstRole, secondRole = "helper", "controller"
			}
			first.link(second, firstRole)
			// The other side holds the offer, and no link: an offer creates nothing.
			harnessWait(t, "the offer to arrive", func() bool { return second.count(`SELECT COUNT(*) FROM device_offers`) == 1 })
			if n := second.count(`SELECT COUNT(*) FROM device_links`); n != 0 {
				t.Fatalf("an offer without an intent created %d device_links rows", n)
			}
			if first.linkCount("waiting") != 1 || first.linkCount("active") != 0 {
				t.Fatal("the first side must be waiting")
			}
			second.link(first, secondRole)
			harnessWait(t, "both sides to be active", func() bool { return ctrl.linkCount("active") == 1 && help.linkCount("active") == 1 })

			id := ctrl.activeID()
			if id != help.activeID() || len(id) != 34 || id[:2] != "l-" {
				t.Fatalf("link ids: %s / %s", id, help.activeID())
			}
			if ctrl.linkWith(help, "active") != 1 || help.linkWith(ctrl, "active") != 1 {
				t.Error("links are not bound to the peer's key")
			}
			var role string
			if err := ctrl.query(`SELECT role FROM device_links WHERE state = 'active'`, &role); err != nil || role != "controller" {
				t.Errorf("controller's role = %q (%v)", role, err)
			}
			if err := help.query(`SELECT role FROM device_links WHERE state = 'active'`, &role); err != nil || role != "helper" {
				t.Errorf("helper's role = %q (%v)", role, err)
			}
			for _, n := range []*devNode{ctrl, help} {
				var trust string
				if err := n.query(`SELECT trust FROM peers`, &trust); err != nil || trust != "fingerprint" {
					t.Errorf("%s: peer trust = %q (%v), want fingerprint", n.name, trust, err)
				}
				if got := n.count(`SELECT COUNT(*) FROM device_offers`); got != 0 {
					t.Errorf("%s: %d offers left after activation", n.name, got)
				}
				harnessWait(t, n.name+"'s audit rows", func() bool {
					return n.auditCount("device.link_intent") == 1 && n.auditCount("device.link_active") == 1 && n.auditCount("peer.verify") == 1
				})
				var list daemon.DeviceListResult
				n.call("device_list", nil, &list)
				if len(list.Links) != 1 || list.Links[0].State != "active" || list.Links[0].ID != id || list.Links[0].ActivatedAt == "" {
					t.Errorf("%s: device_list = %+v", n.name, list)
				}
			}
		})
	}
}

// Only one side confirms: nothing is active, ever, and after 10 minutes the
// attempts have lapsed; a late confirmation on the other side does not link.
func TestDeviceLinkOneSideOnlyLapses(t *testing.T) {
	a, b := newDevPair(t)
	a.link(b, "controller")
	harnessWait(t, "B to keep the offer", func() bool { return b.count(`SELECT COUNT(*) FROM device_offers`) == 1 })
	if a.linkCount("active")+b.linkCount("active") != 0 || b.count(`SELECT COUNT(*) FROM device_links`) != 0 {
		t.Fatal("a link exists after one confirmation")
	}
	a.clk.Advance(10*time.Minute + time.Second)
	b.clk.Advance(10*time.Minute + time.Second)
	var list daemon.DeviceListResult
	a.call("device_list", nil, &list)
	if len(list.Links) != 1 || list.Links[0].State != "revoked" {
		t.Fatalf("A's lapsed attempt = %+v, want revoked", list.Links)
	}
	// B confirms too late: its kept offer is older than 10 minutes, and A's
	// intent has lapsed, so B's offer is only kept.
	b.link(a, "helper")
	harnessWait(t, "A to receive B's offer", func() bool { return a.count(`SELECT COUNT(*) FROM device_offers`) == 1 })
	time.Sleep(200 * time.Millisecond)
	if a.linkCount("active")+b.linkCount("active") != 0 {
		t.Fatal("a link became active from lapsed halves")
	}
	if b.linkCount("waiting") != 1 {
		t.Fatal("B's late intent should be waiting")
	}
}

// A wrong fingerprint changes nothing: no approval, no row, no trust change.
func TestDeviceLinkFingerprintMismatch(t *testing.T) {
	a, b := newDevPair(t)
	var before string
	if err := a.query(`SELECT trust FROM peers`, &before); err != nil {
		t.Fatal(err)
	}
	_, err := a.requestLink(b, "controller", devFP(t, a)) // the wrong device's fingerprint
	if errCode(err) != "fingerprint_mismatch" {
		t.Fatalf("device_link with a wrong fingerprint: %v, want fingerprint_mismatch", err)
	}
	_, err = a.requestLink(b, "controller", "not a fingerprint")
	if errCode(err) != "bad_fingerprint" {
		t.Fatalf("device_link with a malformed fingerprint: %v, want bad_fingerprint", err)
	}
	var after string
	if err := a.query(`SELECT trust FROM peers`, &after); err != nil || after != before {
		t.Errorf("trust changed %q -> %q (%v)", before, after, err)
	}
	if a.count(`SELECT COUNT(*) FROM device_links`) != 0 || a.count(`SELECT COUNT(*) FROM approvals`) != 0 || a.win.startCount() != 0 {
		t.Error("a mismatch left a link row, an approval or a window")
	}
	if n := a.auditCount("peer.verify_fail"); n != 1 {
		t.Errorf("peer.verify_fail audit rows = %d, want 1", n)
	}
	if b.count(`SELECT COUNT(*) FROM device_offers`) != 0 {
		t.Error("the peer received an offer")
	}
}

// Pairing, a team with its roster and team.join, and peers verify never create
// a device link, an offer or a scope (D13).
func TestDeviceTrustOnlyFromLinkFlow(t *testing.T) {
	a, b := newDevPair(t)
	harnessSharedTeam(t, a.harnessNode, b.harnessNode, "x") // team_create, invite, join, roster mails
	var pr daemon.PeerResult
	a.call("peers_verify", daemon.PeerVerifyParams{Peer: b.key, Fingerprint: devFP(t, b)}, &pr)
	b.call("peers_verify", daemon.PeerVerifyParams{Peer: a.key, Fingerprint: devFP(t, a)}, &pr)
	// A request over the team, to exercise the mail paths, then a settle time
	// for late roster and presence mail.
	time.Sleep(500 * time.Millisecond)
	for _, n := range []*devNode{a, b} {
		for _, table := range []string{"device_links", "device_scopes", "device_offers"} {
			if got := n.count(`SELECT COUNT(*) FROM ` + table); got != 0 {
				t.Errorf("%s: %s has %d rows after pairing, team join and peers verify", n.name, table, got)
			}
		}
		var list daemon.DeviceListResult
		n.call("device_list", nil, &list)
		if len(list.Links) != 0 {
			t.Errorf("%s: device_list = %+v, want none", n.name, list.Links)
		}
	}
}

// Reverse links and chains are refused with device_cycle, before any approval.
func TestDeviceHierarchyOverIPC(t *testing.T) {
	r := newHarnessRelay(t)
	a, b, c := newDevNode(t, "laptop", r), newDevNode(t, "desktop", r), newDevNode(t, "server", r)
	a.start()
	b.start()
	c.start()
	waitRelayConnected(t, r, a.key, b.key, c.key)
	harnessPair(t, a.harnessNode, b.harnessNode)
	harnessPair(t, b.harnessNode, c.harnessNode)
	harnessPair(t, a.harnessNode, c.harnessNode)
	activatePair(t, a, b) // a controls b

	starts := map[*devNode]int{a: a.win.startCount(), b: b.win.startCount()}
	cases := []struct {
		name     string
		from, to *devNode
		role     string
		code     string
	}{
		{"reverse link from the helper", b, a, "controller", "device_cycle"},
		{"reverse link from the controller", a, b, "helper", "device_cycle"},
		{"a helper cannot control a third device", b, c, "controller", "device_cycle"},
		{"a controller cannot be controlled by a third device", a, c, "helper", "device_cycle"},
		{"a helper has one controller", b, c, "helper", "bad_state"},
	}
	for _, tc := range cases {
		_, err := tc.from.requestLink(tc.to, tc.role, devFP(t, tc.to))
		if errCode(err) != tc.code {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.code)
		}
	}
	if a.win.startCount() != starts[a] || b.win.startCount() != starts[b] {
		t.Error("a refused link opened an approval window")
	}
	if a.count(`SELECT COUNT(*) FROM device_links`) != 1 || b.count(`SELECT COUNT(*) FROM device_links`) != 1 {
		t.Error("a refused link left a row")
	}
}

// Unlink from either side ends the link on both, deletes the helper's scope and
// audits both sides.
func TestDeviceUnlinkFromEitherSide(t *testing.T) {
	for _, fromController := range []bool{true, false} {
		t.Run(fmt.Sprintf("fromController=%v", fromController), func(t *testing.T) {
			ctrl, help := newDevPair(t)
			id := activatePair(t, ctrl, help)
			if err := help.exec(`INSERT INTO device_scopes (link, scope, expires, approval, created) VALUES (?, '{}', '2099-01-01T00:00:00.000Z', 'a-x', '2026-01-01T00:00:00.000Z')`, id); err != nil {
				t.Fatal(err)
			}
			unlinker, other := ctrl, help
			if !fromController {
				unlinker, other = help, ctrl
			}
			var res daemon.DeviceUnlinkResult
			unlinker.call("device_unlink", daemon.DeviceUnlinkParams{Peer: other.key}, &res)
			if res.MailID == "" || res.Link == nil || res.Link.ID != id || res.Link.State != "revoked" {
				t.Fatalf("device_unlink = %+v", res)
			}
			harnessWait(t, "both sides to be revoked", func() bool { return ctrl.linkCount("revoked") == 1 && help.linkCount("revoked") == 1 })
			if ctrl.linkCount("active")+help.linkCount("active") != 0 {
				t.Error("a link is still active")
			}
			harnessWait(t, "the scope to be deleted", func() bool { return help.count(`SELECT COUNT(*) FROM device_scopes`) == 0 })
			harnessWait(t, "the audit rows", func() bool {
				return unlinker.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'device.unlink' AND detail LIKE '%local%'`) == 1 &&
					other.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'device.unlink' AND detail LIKE '%remote%'`) == 1
			})
			// The pair can link again afterwards.
			activatePair(t, ctrl, help)
		})
	}
}

// Asymmetric activation: the controller's intent lapses before the helper's
// offer arrives, so only the helper is active. device unlink on the controller
// still sends device.unlink and ends the helper's link.
func TestDeviceUnlinkAsymmetricActivation(t *testing.T) {
	ctrl, help := newDevPair(t)
	ctrl.link(help, "controller")
	harnessWait(t, "the helper to keep the offer", func() bool { return help.count(`SELECT COUNT(*) FROM device_offers`) == 1 })
	ctrl.clk.Advance(10*time.Minute + time.Second) // only the controller's clock: its intent lapses
	help.link(ctrl, "helper")
	harnessWait(t, "the helper to be active", func() bool { return help.linkCount("active") == 1 })
	var offerID string
	if err := help.query(`SELECT id FROM outbox WHERE kind = 'device.link'`, &offerID); err != nil {
		t.Fatal(err)
	}
	harnessWait(t, "the controller to receive the helper's offer", func() bool {
		return ctrl.count(`SELECT COUNT(*) FROM mail_seen WHERE id = '`+offerID+`'`) == 1
	})
	if ctrl.linkCount("active") != 0 {
		t.Fatal("the controller must not be active: its intent lapsed")
	}
	// Its clock is 10 min ahead, so by that clock the helper's offer is already
	// stale: it is not even kept (D22, review 36 L4).
	if n := ctrl.count(`SELECT COUNT(*) FROM device_offers`); n != 0 {
		t.Fatalf("a stale offer was kept (%d)", n)
	}

	var res daemon.DeviceUnlinkResult
	ctrl.call("device_unlink", daemon.DeviceUnlinkParams{Peer: help.key}, &res)
	if res.MailID == "" {
		t.Fatalf("device_unlink sent no mail: %+v", res)
	}
	harnessWait(t, "the helper's link to be revoked", func() bool { return help.linkCount("revoked") == 1 && help.linkCount("active") == 0 })
	if n := ctrl.count(`SELECT COUNT(*) FROM device_offers`); n != 0 {
		t.Errorf("the controller's kept offer survived the unlink (%d)", n)
	}
}

// A device.unlink from a third peer naming the link id changes nothing: a peer
// can only end its own links (review 24 M8).
func TestDeviceUnlinkFromThirdPeerChangesNothing(t *testing.T) {
	r := newHarnessRelay(t)
	a, b, c := newDevNode(t, "laptop", r), newDevNode(t, "desktop", r), newDevNode(t, "server", r)
	a.start()
	b.start()
	c.start()
	waitRelayConnected(t, r, a.key, b.key, c.key)
	harnessPair(t, a.harnessNode, b.harnessNode)
	harnessPair(t, b.harnessNode, c.harnessNode)
	id := activatePair(t, a, b)

	// C's real device.unlink to B (C holds no link). A device.unlink naming
	// the link id cannot be forged through mail_submit any more (review 36
	// M1); that case is TestDeviceUnlinkNamingAnotherPeersLink.
	var res daemon.DeviceUnlinkResult
	c.call("device_unlink", daemon.DeviceUnlinkParams{Peer: b.key}, &res)
	harnessWait(t, "B to process C's mail", func() bool {
		return b.count(`SELECT COUNT(*) FROM mail_seen WHERE id = '`+res.MailID+`'`) == 1
	})
	time.Sleep(200 * time.Millisecond)
	if a.linkCount("active") != 1 || b.linkCount("active") != 1 || b.activeID() != id {
		t.Fatal("a device.unlink from a third peer changed a link")
	}
	if c.count(`SELECT COUNT(*) FROM device_links`) != 0 {
		t.Error("the third peer holds a link row")
	}
	// A device.link naming the wrong keys cannot even be sent through
	// mail_submit; at the receiver it is a bad body (TestParseOfferIsStrict).
	bad := fmt.Sprintf(`{"at":%q,"controller":%q,"helper":%q,"nonce":"00112233445566778899aabbccddeeff","role":"controller"}`,
		time.Now().UTC().Format("2006-01-02T15:04:05Z"), a.key, b.key)
	var sent daemon.MailSubmitResult
	if err := ipcCallErr(c.harnessNode, "mail_submit", daemon.MailSubmitParams{To: b.key, Kind: "device.link", Body: []byte(bad)}, &sent); errCode(err) != "bad_request" {
		t.Errorf("mail_submit device.link from a third peer: %v, want bad_request", err)
	}
	if b.count(`SELECT COUNT(*) FROM device_offers`) != 0 {
		t.Error("a device.link naming other keys was kept as an offer")
	}
}

// Review 36 M1: a local agent on the controller cannot stand in for its human.
// The helper's human confirms; an agent on the controller then tries to send
// the controller's half itself through mail_submit. It is refused, and the
// helper stays waiting.
func TestDeviceLinkOfferCannotBeForgedThroughMailSubmit(t *testing.T) {
	ctrl, help := newDevPair(t)
	help.link(ctrl, "helper")
	harnessWait(t, "the controller to keep the helper's offer", func() bool { return ctrl.count(`SELECT COUNT(*) FROM device_offers`) == 1 })
	forged := fmt.Sprintf(`{"at":%q,"controller":%q,"helper":%q,"nonce":"00112233445566778899aabbccddeeff","role":"controller"}`,
		time.Now().UTC().Format("2006-01-02T15:04:05Z"), ctrl.key, help.key)
	for _, kind := range []string{"device.link", "device.unlink"} {
		var sent daemon.MailSubmitResult
		if err := ipcCallErr(ctrl.harnessNode, "mail_submit", daemon.MailSubmitParams{To: help.key, Kind: kind, Body: []byte(forged)}, &sent); errCode(err) != "bad_request" {
			t.Errorf("mail_submit %s: %v, want bad_request", kind, err)
		}
	}
	if n := ctrl.count(`SELECT COUNT(*) FROM outbox WHERE kind LIKE 'device.%'`); n != 0 {
		t.Errorf("the controller queued %d device.* mails without its human", n)
	}
	time.Sleep(200 * time.Millisecond)
	if help.linkCount("active") != 0 || help.linkCount("waiting") != 1 {
		t.Fatal("the helper activated without the controller's human")
	}
}

// peers remove of the other device revokes the link locally.
func TestDevicePeersRemoveRevokesLink(t *testing.T) {
	a, b := newDevPair(t)
	activatePair(t, a, b)
	var pr daemon.PeerResult
	a.call("peers_remove", daemon.PeerRemoveParams{Peer: b.key}, &pr)
	if a.linkCount("revoked") != 1 || a.linkCount("active") != 0 {
		t.Fatal("peers remove left the link active")
	}
	if n := a.auditCount("device.unlink"); n != 1 {
		t.Errorf("device.unlink audit rows = %d, want 1", n)
	}
}

// Review 36 L3: device unlink during a link attempt that still waits for its
// code rejects that approval too, so no dead code stays on screen.
func TestDeviceUnlinkRejectsPendingApproval(t *testing.T) {
	a, b := newDevPair(t)
	res, err := a.requestLink(b, "controller", devFP(t, b))
	if err != nil {
		t.Fatal(err)
	}
	var un daemon.DeviceUnlinkResult
	a.call("device_unlink", daemon.DeviceUnlinkParams{Peer: b.key}, &un)
	harnessWait(t, "the approval to be rejected", func() bool {
		return a.count(`SELECT COUNT(*) FROM approvals WHERE id = '`+res.Approval.ID+`' AND state = 'rejected'`) == 1
	})
	if a.linkCount("pending_approval") != 0 {
		t.Error("the attempt is still pending")
	}
}
