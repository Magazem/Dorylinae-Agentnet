package relay_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

const testLoginURL = "https://relay.example/login"

// accountsRelay starts an in-memory relay with --accounts github.
func accountsRelay(t *testing.T, opts relay.Options) (*relay.Server, string) {
	t.Helper()
	opts.Accounts = relay.AccountsGitHub
	if opts.LoginURL == "" {
		opts.LoginURL = testLoginURL
	}
	return start(t, opts)
}

// fileAccountsRelay starts a relay with accounts on a database file, which
// relay admin can change from outside (start uses an in-memory database).
func fileAccountsRelay(t *testing.T) (*relay.Server, string, string) {
	t.Helper()
	db := filepath.Join(testutil.TempDir(t), "relay.db")
	s, err := relay.Open(relay.Options{QueuePath: db, Accounts: relay.AccountsGitHub, LoginURL: testLoginURL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, "ws" + strings.TrimPrefix(ts.URL, "http") + envelope.ConnectPath, db
}

// authedReady authenticates p on url and returns the connection and its ready frame.
func authedReady(t *testing.T, url string, p peer) (*websocket.Conn, envelope.Control) {
	t.Helper()
	c, nonce := rawDial(t, url)
	if err := c.Write(ctx(t), websocket.MessageText, authFrame(t, p, nonce)); err != nil {
		t.Fatal(err)
	}
	r := readControl(t, c)
	if r.Op != envelope.OpReady || r.PublicKey != p.key {
		t.Fatalf("got %+v, want ready", r)
	}
	return c, r
}

var accountSeq int

// newAccount creates an account (github, a fresh subject) and puts it in
// group if that is not empty.
func newAccount(t *testing.T, s *relay.Server, display, group string) string {
	t.Helper()
	accountSeq++
	acc, err := s.EnsureAccount("github", fmt.Sprint(1000+accountSeq), display)
	if err != nil {
		t.Fatal(err)
	}
	if group != "" {
		if err := s.SetGroupForTest(acc, group); err != nil {
			t.Fatal(err)
		}
	}
	return acc
}

// Account states of the routing matrix.
const (
	stUnbound = "unbound"
	stNoGroup = "bound, no group"
	stGroup   = "bound, group"
)

// peerIn returns a connected peer in state st, with its queue drained when bound.
func peerIn(t *testing.T, s *relay.Server, url, st string) (peer, *websocket.Conn) {
	t.Helper()
	p := newPeer(t)
	switch st {
	case stNoGroup:
		if err := s.BindKeyForTest(p.key, newAccount(t, s, "@nogroup", "")); err != nil {
			t.Fatal(err)
		}
	case stGroup:
		if err := s.BindKeyForTest(p.key, newAccount(t, s, "@member", "qg_matrix")); err != nil {
			t.Fatal(err)
		}
	}
	c, r := authedReady(t, url, p)
	want := map[string]string{stUnbound: envelope.AccountUnbound, stNoGroup: envelope.AccountBound, stGroup: envelope.AccountBound}[st]
	if r.Account == nil || r.Account.State != want || (st == stGroup) != (r.Account.Group != "") {
		t.Fatalf("%s: ready account = %+v", st, r.Account)
	}
	if st != stUnbound {
		waitDrained(t, s, p.key)
	}
	return p, c
}

func writeEnv(t *testing.T, c *websocket.Conn, e envelope.Envelope) []byte {
	t.Helper()
	frame, err := e.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx(t), websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	return frame
}

func readFrame(t *testing.T, c *websocket.Conn) []byte {
	t.Helper()
	_, raw, err := c.Read(ctx(t))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return raw
}

// expectClosed reads until c closes and checks the status and that it came within limit.
func expectClosed(t *testing.T, c *websocket.Conn, status websocket.StatusCode, limit time.Duration) {
	t.Helper()
	start := time.Now()
	for {
		_, _, err := c.Read(ctx(t))
		if err != nil {
			if got := websocket.CloseStatus(err); got != status {
				t.Fatalf("closed with %v (%v), want %v", got, err, status)
			}
			if d := time.Since(start); d > limit {
				t.Fatalf("closed after %v, want within %v", d, limit)
			}
			return
		}
	}
}

func TestAccountsReadyAdvertisesState(t *testing.T) {
	s, url := accountsRelay(t, relay.Options{})
	_, r := authedReady(t, url, newPeer(t))
	if !slices.Contains(r.Features, envelope.FeatureAccounts) || !slices.Contains(r.Features, envelope.FeatureEphemeral) {
		t.Fatalf("features = %v", r.Features)
	}
	if r.Account == nil || *r.Account != (envelope.Account{State: envelope.AccountUnbound}) {
		t.Fatalf("unbound ready account = %+v", r.Account)
	}

	p := newPeer(t)
	acc := newAccount(t, s, "@octocat", "qg_one")
	if err := s.BindKeyForTest(p.key, acc); err != nil {
		t.Fatal(err)
	}
	_, r = authedReady(t, url, p)
	if want := (envelope.Account{State: envelope.AccountBound, ID: acc, Display: "@octocat", Group: "qg_one"}); r.Account == nil || *r.Account != want {
		t.Fatalf("bound ready account = %+v, want %+v", r.Account, want)
	}

	// Without --accounts nothing changes: no feature, no account member.
	_, plain := start(t, relay.Options{})
	_, r = authedReady(t, plain, newPeer(t))
	if r.Account != nil || slices.Contains(r.Features, envelope.FeatureAccounts) {
		t.Fatalf("relay without accounts: ready = %+v", r)
	}
}

// TestAccountsBindFlowWithoutReconnect is plan 4.2's acceptance line: an
// unbound key's mail, presence and pair_new are refused with
// account_required; after the bind is confirmed (the confirm page's Go API
// standing in for the browser) the same connection sends mail.
func TestAccountsBindFlowWithoutReconnect(t *testing.T) {
	clk := newClock()
	s, url := accountsRelay(t, relay.Options{Now: clk.Now})
	peerB, cb := peerIn(t, s, url, stGroup)
	a := newPeer(t)
	ca, _ := authedReady(t, url, a)

	writeEnv(t, ca, a.env(peerB.key, "m-1", []byte("x")))
	expectError(t, ca, envelope.CodeAccountRequired, "m-1")
	pres := a.env(peerB.key, "p-1", nil)
	pres.Type = envelope.TypePresence
	writeEnv(t, ca, pres)
	expectError(t, ca, envelope.CodeAccountRequired, "p-1")
	send(t, ca, envelope.Control{Op: envelope.OpPairNew, Lookup: "ABCDE", Card: cardFor(a, "a"), Mbox: mboxFor(a, "a"), Ref: "pn"})
	if r := readControl(t, ca); r.Op != envelope.OpError || r.Code != envelope.CodeAccountRequired {
		t.Fatalf("pair_new while unbound: %+v", r)
	}
	if n, _ := s.Queued(peerB.key); n != 0 {
		t.Fatalf("queued for B = %d, want 0", n)
	}

	send(t, ca, envelope.Control{Op: envelope.OpBindStart, Device: "laptop", OS: "linux"})
	pend := readControl(t, ca)
	if pend.Op != envelope.OpBindPending || !strings.HasPrefix(pend.Ref, "bnd_") || pend.URL != testLoginURL || pend.Interval != 5 {
		t.Fatalf("bind_pending = %+v", pend)
	}
	if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`).MatchString(pend.UserCode) {
		t.Fatalf("user_code %q is not XXXX-XXXX Crockford", pend.UserCode)
	}
	if strings.Contains(pend.URL, "?") || strings.Contains(pend.URL, strings.ReplaceAll(pend.UserCode, "-", "")) {
		t.Fatalf("the code is in the URL: %q", pend.URL)
	}
	if exp, err := time.Parse(time.RFC3339, pend.Expires); err != nil || !exp.Equal(clk.Now().Add(10*time.Minute).Truncate(time.Second)) {
		t.Fatalf("expires = %q, want 10 minutes", pend.Expires)
	}

	// Polling faster than the interval is rate limited; after it, pending.
	send(t, ca, envelope.Control{Op: envelope.OpBindPoll, Ref: pend.Ref})
	expectError(t, ca, envelope.CodeRateLimited, pend.Ref)
	clk.Advance(5 * time.Second)
	send(t, ca, envelope.Control{Op: envelope.OpBindPoll, Ref: pend.Ref})
	if r := readControl(t, ca); r.Op != envelope.OpBindPending || r.Ref != pend.Ref || r.UserCode != "" {
		t.Fatalf("poll while pending = %+v", r)
	}

	// The confirm page: a typed code (any case, without the dash) finds the bind.
	req, err := s.PendingBind(strings.ToLower(strings.ReplaceAll(pend.UserCode, "-", "")))
	if err != nil {
		t.Fatal(err)
	}
	fp, _ := envelope.KeyFingerprint(a.key)
	if req.Key != a.key || req.Device != "laptop" || req.OS != "linux" || req.Fingerprint != envelope.FormatFingerprint(fp) {
		t.Fatalf("pending bind = %+v", req)
	}
	acc := newAccount(t, s, "@alice", "qg_matrix")
	if err := s.ConfirmBind(req.Ref, acc); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PendingBind(pend.UserCode); !errors.Is(err, relay.ErrBindInvalid) {
		t.Fatalf("code reused after confirm: %v", err)
	}

	clk.Advance(5 * time.Second)
	send(t, ca, envelope.Control{Op: envelope.OpBindPoll, Ref: pend.Ref})
	done := readControl(t, ca)
	if want := (envelope.Account{State: envelope.AccountBound, ID: acc, Display: "@alice", Group: "qg_matrix"}); done.Op != envelope.OpBindDone || done.Account == nil || *done.Account != want {
		t.Fatalf("bind_done = %+v", done)
	}

	// Same connection, no reconnect: mail flows now.
	waitDrained(t, s, a.key)
	frame := writeEnv(t, ca, a.env(peerB.key, "m-2", []byte("hello")))
	if got := readFrame(t, cb); !bytes.Equal(got, frame) {
		t.Fatalf("B got %s", got)
	}
	send(t, ca, envelope.Control{Op: envelope.OpBindStart, Device: "laptop", OS: "linux", Ref: "again"})
	expectError(t, ca, envelope.CodeAlreadyBound, "again")
}

func TestAccountsBindEndings(t *testing.T) {
	clk := newClock()
	s, url := accountsRelay(t, relay.Options{Now: clk.Now})
	start := func() (*websocket.Conn, envelope.Control) {
		c, _ := authedReady(t, url, newPeer(t))
		send(t, c, envelope.Control{Op: envelope.OpBindStart, Device: "desk top_1.x-y", OS: "windows"})
		r := readControl(t, c)
		if r.Op != envelope.OpBindPending {
			t.Fatalf("bind_start: %+v", r)
		}
		return c, r
	}
	poll := func(c *websocket.Conn, ref, wantCode string) {
		t.Helper()
		clk.Advance(5 * time.Second)
		send(t, c, envelope.Control{Op: envelope.OpBindPoll, Ref: ref})
		expectError(t, c, wantCode, ref)
	}

	// Expired after 10 minutes, for the poll and for the confirm page.
	c1, p1 := start()
	clk.Advance(10 * time.Minute)
	if _, err := s.PendingBind(p1.UserCode); !errors.Is(err, relay.ErrBindInvalid) {
		t.Fatalf("expired code: %v", err)
	}
	poll(c1, p1.Ref, envelope.CodeBindExpired)

	// Denied in the browser.
	c2, p2 := start()
	req, err := s.PendingBind(p2.UserCode)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DenyBind(req.Ref); err != nil {
		t.Fatal(err)
	}
	poll(c2, p2.Ref, envelope.CodeBindDenied)

	// Cancelled by the daemon, and a new bind_start replaces the pending one.
	c3, p3 := start()
	send(t, c3, envelope.Control{Op: envelope.OpBindCancel, Ref: p3.Ref})
	poll(c3, p3.Ref, envelope.CodeBindExpired)
	if _, err := s.PendingBind(p3.UserCode); !errors.Is(err, relay.ErrBindInvalid) {
		t.Fatalf("cancelled code: %v", err)
	}
	send(t, c3, envelope.Control{Op: envelope.OpBindStart, Device: "a", OS: "linux"})
	first := readControl(t, c3)
	send(t, c3, envelope.Control{Op: envelope.OpBindStart, Device: "b", OS: "linux"})
	second := readControl(t, c3)
	if _, err := s.PendingBind(first.UserCode); !errors.Is(err, relay.ErrBindInvalid) {
		t.Fatalf("replaced bind still pending: %v", err)
	}
	if _, err := s.PendingBind(second.UserCode); err != nil {
		t.Fatalf("new bind: %v", err)
	}

	// A wrong code, and another key's ref, find nothing.
	if _, err := s.PendingBind("0000-0000"); !errors.Is(err, relay.ErrBindInvalid) {
		t.Fatalf("wrong code: %v", err)
	}
	if _, err := s.PendingBind("not a code"); !errors.Is(err, relay.ErrBindInvalid) {
		t.Fatalf("malformed code: %v", err)
	}
	poll(c1, second.Ref, envelope.CodeBindExpired)

	// A bad device label or os is refused.
	for _, bad := range []envelope.Control{
		{Op: envelope.OpBindStart, Device: "", OS: "linux"},
		{Op: envelope.OpBindStart, Device: strings.Repeat("x", 33), OS: "linux"},
		{Op: envelope.OpBindStart, Device: "<script>", OS: "linux"},
		{Op: envelope.OpBindStart, Device: "ok", OS: "Linux\n"},
	} {
		send(t, c1, bad)
		if r := readControl(t, c1); r.Op != envelope.OpError || r.Code != envelope.CodeBadEnvelope {
			t.Fatalf("bind_start %+v: got %+v", bad, r)
		}
	}
}

func TestAccountsFifthKeyNeedsUnbind(t *testing.T) {
	s, url := accountsRelay(t, relay.Options{})
	acc := newAccount(t, s, "@many", "")
	bindVia := func(p peer) error {
		c, _ := authedReady(t, url, p)
		send(t, c, envelope.Control{Op: envelope.OpBindStart, Device: "d", OS: "linux"})
		pend := readControl(t, c)
		req, err := s.PendingBind(pend.UserCode)
		if err != nil {
			t.Fatal(err)
		}
		return s.ConfirmBind(req.Ref, acc)
	}
	var keys []peer
	for i := range relay.MaxKeysPerAccount {
		p := newPeer(t)
		if err := bindVia(p); err != nil {
			t.Fatalf("key %d: %v", i+1, err)
		}
		keys = append(keys, p)
	}
	fifth := newPeer(t)
	c, _ := authedReady(t, url, fifth)
	send(t, c, envelope.Control{Op: envelope.OpBindStart, Device: "d", OS: "linux"})
	req, err := s.PendingBind(readControl(t, c).UserCode)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmBind(req.Ref, acc); !errors.Is(err, relay.ErrAccountKeysFull) {
		t.Fatalf("5th key: %v, want ErrAccountKeysFull", err)
	}
	if err := s.UnbindKey(keys[0].key); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmBind(req.Ref, acc); err != nil {
		t.Fatalf("5th key after an unbind: %v", err)
	}
}

// TestAccountsRoutingMatrix is the 4.2a routing table: sender and recipient
// state (unbound / bound without a quota group / bound with one) × mail,
// presence and pair.confirm envelopes, and the pairing frames by sender state.
func TestAccountsRoutingMatrix(t *testing.T) {
	const (
		delivered = "delivered"
		refused   = "account_required" // to the sender, and nothing queued
		dropped   = "dropped silently"
	)
	type row struct {
		from, to, typ, want string
	}
	var rows []row
	add := func(typ string, want map[[2]string]string) {
		for _, f := range []string{stUnbound, stNoGroup, stGroup} {
			for _, to := range []string{stUnbound, stNoGroup, stGroup} {
				rows = append(rows, row{f, to, typ, want[[2]string{f, to}]})
			}
		}
	}
	add("mail", map[[2]string]string{
		{stUnbound, stUnbound}: refused, {stUnbound, stNoGroup}: refused, {stUnbound, stGroup}: refused,
		{stNoGroup, stUnbound}: refused, {stNoGroup, stNoGroup}: refused, {stNoGroup, stGroup}: refused,
		{stGroup, stUnbound}: refused, {stGroup, stNoGroup}: refused, {stGroup, stGroup}: delivered,
	})
	add(envelope.TypePresence, map[[2]string]string{
		{stUnbound, stUnbound}: refused, {stUnbound, stNoGroup}: refused, {stUnbound, stGroup}: refused,
		{stNoGroup, stUnbound}: refused, {stNoGroup, stNoGroup}: refused, {stNoGroup, stGroup}: refused,
		{stGroup, stUnbound}: dropped, {stGroup, stNoGroup}: dropped, {stGroup, stGroup}: delivered,
	})
	add(envelope.TypePairConfirm, map[[2]string]string{
		{stUnbound, stUnbound}: refused, {stUnbound, stNoGroup}: refused, {stUnbound, stGroup}: refused,
		{stNoGroup, stUnbound}: refused, {stNoGroup, stNoGroup}: delivered, {stNoGroup, stGroup}: delivered,
		{stGroup, stUnbound}: refused, {stGroup, stNoGroup}: delivered, {stGroup, stGroup}: delivered,
	})

	// Every connection comes from 127.0.0.0/24: the 4.0b per-prefix limits are
	// off here (they have their own tests).
	s, url := accountsRelay(t, relay.Options{UpgradesPerMinute: -1, UpgradeBurst: -1, MaxConnsPerPrefix: -1, MaxKeysPerPrefix: -1,
		PrefixEnvelopesPerMinute: -1, PrefixBytesPerMinute: -1, ReconnectsPerMinute: -1})
	for i, r := range rows {
		t.Run(fmt.Sprintf("%s/%s->%s", r.typ, r.from, r.to), func(t *testing.T) {
			if r.want == "" {
				t.Fatal("row without an expectation")
			}
			a, ca := peerIn(t, s, url, r.from)
			b, cb := peerIn(t, s, url, r.to)
			e := a.env(b.key, fmt.Sprintf("row-%d", i), []byte("p"))
			e.Type = r.typ
			frame := writeEnv(t, ca, e)
			switch r.want {
			case delivered:
				if got := readFrame(t, cb); !bytes.Equal(got, frame) {
					t.Fatalf("recipient got %s", got)
				}
			case refused:
				expectError(t, ca, envelope.CodeAccountRequired, e.ID)
				if n, err := s.Queued(b.key); err != nil || n != 0 {
					t.Fatalf("queued for recipient = %d, %v; want 0", n, err)
				}
			case dropped:
				// No error to the sender: the next thing it reads is its own
				// echo; and a bound recipient's first frame is a later pair.confirm.
				if r.from == stGroup {
					stillAlive(t, ca, a)
				}
				if r.to == stNoGroup {
					follow := a.env(b.key, e.ID+"-f", nil)
					follow.Type = envelope.TypePairConfirm
					want := writeEnv(t, ca, follow)
					if got := readFrame(t, cb); !bytes.Equal(got, want) {
						t.Fatalf("recipient got %s, want the follow-up only", got)
					}
				}
			}
		})
	}

	// Pairing frames, by sender state: account_required unless allowed.
	pairing := []struct {
		st, op, wantOp, wantCode string
	}{
		{stUnbound, envelope.OpPairNew, envelope.OpError, envelope.CodeAccountRequired},
		{stNoGroup, envelope.OpPairNew, envelope.OpError, envelope.CodeAccountRequired},
		{stGroup, envelope.OpPairNew, envelope.OpPairCode, ""},
		{stUnbound, envelope.OpPairRedeem, envelope.OpError, envelope.CodeAccountRequired},
		{stNoGroup, envelope.OpPairRedeem, envelope.OpError, envelope.CodePairInvalid}, // allowed: joins a team
		{stGroup, envelope.OpPairRedeem, envelope.OpError, envelope.CodePairInvalid},
		{stUnbound, envelope.OpPairCancel, envelope.OpError, envelope.CodeAccountRequired},
		{stNoGroup, envelope.OpPairCancel, envelope.OpError, envelope.CodeAccountRequired},
		{stUnbound, envelope.OpAck, envelope.OpError, envelope.CodeAccountRequired},
		{stUnbound, envelope.OpUnbind, envelope.OpError, envelope.CodeAccountRequired},
	}
	for i, pr := range pairing {
		t.Run(fmt.Sprintf("%s/%s", pr.op, pr.st), func(t *testing.T) {
			p, c := peerIn(t, s, url, pr.st)
			lookup := []string{"ABCDE", "FGHJK", "MNPQR", "STVWX", "YZ012", "34567", "89ABC", "DEFGH", "JKMNP", "QRSTV"}[i]
			send(t, c, envelope.Control{Op: pr.op, Lookup: lookup, Card: cardFor(p, "c"), Mbox: mboxFor(p, "m"), Ref: "r", From: p.key})
			r := readControl(t, c)
			if r.Op != pr.wantOp || r.Code != pr.wantCode || r.Ref != "r" {
				t.Fatalf("got %+v, want %s %s", r, pr.wantOp, pr.wantCode)
			}
			stillOpen := p.env(p.key, "open", nil)
			switch pr.st {
			case stGroup:
				stillAlive(t, c, p)
			case stUnbound: // the connection stays open for binding
				writeEnv(t, c, stillOpen)
				expectError(t, c, envelope.CodeAccountRequired, "open")
			}
		})
	}
}

// TestAccountsQueueWaitsForRebind: envelopes queued for a key stay while it
// is unbound (nothing is delivered to an unbound connection) and arrive once
// it is bound again, on the same connection.
func TestAccountsQueueWaitsForRebind(t *testing.T) {
	s, url := accountsRelay(t, relay.Options{})
	a, ca := peerIn(t, s, url, stGroup)
	b := newPeer(t)
	accB := newAccount(t, s, "@b", "qg_matrix")
	if err := s.BindKeyForTest(b.key, accB); err != nil {
		t.Fatal(err)
	}
	frame := writeEnv(t, ca, a.env(b.key, "q-1", []byte("queued")))
	if r := readControl(t, ca); r.Op != envelope.OpQueued || r.Ref != "q-1" {
		t.Fatalf("got %+v, want queued", r)
	}
	if err := s.UnbindKey(b.key); err != nil {
		t.Fatal(err)
	}
	cb, r := authedReady(t, url, b)
	if r.Account.State != envelope.AccountUnbound {
		t.Fatalf("ready = %+v", r.Account)
	}
	if err := s.BindKeyForTest(b.key, accB); err != nil {
		t.Fatal(err)
	}
	if got := readFrame(t, cb); !bytes.Equal(got, frame) {
		t.Fatalf("after re-bind B got %s", got)
	}
}

// TestAccountsUnbindClosesWithinOneSecond covers the three ways to unbind:
// the relay's own API (account page), the daemon's unbind frame (agentnet
// logout) and relay admin on the database file from another process.
func TestAccountsUnbindClosesWithinOneSecond(t *testing.T) {
	s, url := accountsRelay(t, relay.Options{})

	a, ca := peerIn(t, s, url, stGroup)
	if err := s.UnbindKey(a.key); err != nil {
		t.Fatal(err)
	}
	expectError(t, ca, envelope.CodeAccountRevoked, "")
	expectClosed(t, ca, websocket.StatusPolicyViolation, time.Second)
	if _, r := authedReady(t, url, a); r.Account.State != envelope.AccountUnbound {
		t.Fatalf("after unbind: %+v", r.Account)
	}

	b, cb := peerIn(t, s, url, stNoGroup)
	send(t, cb, envelope.Control{Op: envelope.OpUnbind})
	expectError(t, cb, envelope.CodeAccountRevoked, "")
	expectClosed(t, cb, websocket.StatusPolicyViolation, time.Second)
	if _, r := authedReady(t, url, b); r.Account.State != envelope.AccountUnbound {
		t.Fatalf("after logout: %+v", r.Account)
	}

	// relay admin as another process would run it: its own database handle
	// on the relay's file.
	s2, furl, db := fileAccountsRelay(t)
	c, cc := peerIn(t, s2, furl, stGroup)
	adm, err := relay.OpenAdmin(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adm.Close() })
	startAt := time.Now()
	if err := adm.Unbind(c.key); err != nil {
		t.Fatal(err)
	}
	expectError(t, cc, envelope.CodeAccountRevoked, "")
	expectClosed(t, cc, websocket.StatusPolicyViolation, time.Second)
	if d := time.Since(startAt); d > time.Second {
		t.Fatalf("admin unbind closed the connection after %v, want within 1 s", d)
	}
}

func TestAccountsSuspension(t *testing.T) {
	s, url, db := fileAccountsRelay(t)
	a := newPeer(t)
	acc := newAccount(t, s, "@sus", "qg_s")
	if err := s.BindKeyForTest(a.key, acc); err != nil {
		t.Fatal(err)
	}
	ca, _ := authedReady(t, url, a)
	// A live connection is closed when the account is suspended.
	if err := s.SetAccountSuspended(acc, true); err != nil {
		t.Fatal(err)
	}
	expectError(t, ca, envelope.CodeAccountSuspended, "")
	expectClosed(t, ca, websocket.StatusPolicyViolation, time.Second)

	// A suspended account's key is closed at ready.
	c, r := authedReady(t, url, a)
	if r.Account == nil || r.Account.State != envelope.AccountSuspended {
		t.Fatalf("ready = %+v", r.Account)
	}
	expectError(t, c, envelope.CodeAccountSuspended, "")
	expectClosed(t, c, websocket.StatusPolicyViolation, time.Second)

	// Reversible; then the quota group, suspended by relay admin, closes it again.
	if err := s.SetAccountSuspended(acc, false); err != nil {
		t.Fatal(err)
	}
	ca, r = authedReady(t, url, a)
	if r.Account.State != envelope.AccountBound {
		t.Fatalf("after unsuspend: %+v", r.Account)
	}
	adm, err := relay.OpenAdmin(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adm.Close() })
	if err := adm.SetGroupSuspended("qg_s", true); err != nil {
		t.Fatal(err)
	}
	expectError(t, ca, envelope.CodeAccountSuspended, "")
	expectClosed(t, ca, websocket.StatusPolicyViolation, time.Second)
	if err := adm.SetGroupSuspended("qg_s", false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(wait)
	for {
		_, r = authedReady(t, url, a)
		if r.Account.State == envelope.AccountBound || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if r.Account.State != envelope.AccountBound {
		t.Fatalf("after group unsuspend: %+v", r.Account)
	}

	// Deleting the account revokes its keys.
	ca, _ = authedReady(t, url, a)
	if err := s.DeleteAccount(acc); err != nil {
		t.Fatal(err)
	}
	expectError(t, ca, envelope.CodeAccountRevoked, "")
	expectClosed(t, ca, websocket.StatusPolicyViolation, time.Second)
}

func TestAccountsUnboundConnectionsPerPrefix(t *testing.T) {
	_, url := accountsRelay(t, relay.Options{})
	var conns []*websocket.Conn
	for range 16 {
		c, _ := authedReady(t, url, newPeer(t))
		conns = append(conns, c)
	}
	_, _ = authedReady(t, url, newPeer(t)) // the 17th from 127.0.0.0/24
	expectClosed(t, conns[0], websocket.StatusTryAgainLater, 2*time.Second)
	for _, c := range conns[1:] {
		send(t, c, envelope.Control{Op: envelope.OpBindCancel, Ref: "bnd_x"}) // allowed, no reply
	}
	send(t, conns[1], envelope.Control{Op: envelope.OpAck, Ref: "x", From: "y"})
	expectError(t, conns[1], envelope.CodeAccountRequired, "x")
}

func TestAccountsAccountChangedOnNewBind(t *testing.T) {
	s, url := accountsRelay(t, relay.Options{})
	acc := newAccount(t, s, "@two", "qg_matrix")
	a := newPeer(t)
	if err := s.BindKeyForTest(a.key, acc); err != nil {
		t.Fatal(err)
	}
	ca, _ := authedReady(t, url, a)
	waitDrained(t, s, a.key)
	b := newPeer(t)
	cb, _ := authedReady(t, url, b)
	send(t, cb, envelope.Control{Op: envelope.OpBindStart, Device: "new", OS: "darwin"})
	req, err := s.PendingBind(readControl(t, cb).UserCode)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmBind(req.Ref, acc); err != nil {
		t.Fatal(err)
	}
	if r := readControl(t, ca); r.Op != envelope.OpAccountChanged || r.Account != nil || r.Ref != "" {
		t.Fatalf("other key got %+v, want a bare account_changed", r)
	}
}

func TestAccountsBindFramesUnknownWithoutAccounts(t *testing.T) {
	_, url := start(t, relay.Options{})
	c, _ := authedReady(t, url, newPeer(t))
	send(t, c, envelope.Control{Op: envelope.OpBindStart, Device: "d", OS: "linux"})
	expectClosed(t, c, websocket.StatusPolicyViolation, wait)
}

// TestAccountsLogsHoldNoEmail is the marker test: no email address (or
// GitHub login) appears in any relay log line or journal entry, through
// sign-in, bind, routing, unbind, suspension and deletion.
func TestAccountsLogsHoldNoEmail(t *testing.T) {
	mu := &lockedWriter{w: &bytes.Buffer{}}
	var journal bytes.Buffer
	jw := relay.NewJournalWriter(&journal)
	s, url := start(t, relay.Options{Accounts: relay.AccountsBoth, LoginURL: testLoginURL, Journal: jw,
		Logger: slog.New(slog.NewTextHandler(mu, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	const email, login = "Marker.Person+relay@example-marker.invalid", "@markerlogin"

	accE, err := s.EnsureAccount("email", email, email)
	if err != nil {
		t.Fatal(err)
	}
	accG, err := s.EnsureAccount("github", "424242", login)
	if err != nil {
		t.Fatal(err)
	}
	for _, acc := range []string{accE, accG} {
		if err := s.SetGroupForTest(acc, "qg_marker"); err != nil {
			t.Fatal(err)
		}
	}
	a, b := newPeer(t), newPeer(t)
	ca, _ := authedReady(t, url, a)
	send(t, ca, envelope.Control{Op: envelope.OpBindStart, Device: "laptop", OS: "linux"})
	req, err := s.PendingBind(readControl(t, ca).UserCode)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmBind(req.Ref, accE); err != nil {
		t.Fatal(err)
	}
	if err := s.BindKeyForTest(b.key, accG); err != nil {
		t.Fatal(err)
	}
	cb, rb := authedReady(t, url, b)
	if rb.Account.Display != login {
		t.Fatalf("ready display = %q", rb.Account.Display)
	}
	waitDrained(t, s, a.key)
	waitDrained(t, s, b.key)
	writeEnv(t, ca, a.env(b.key, "m-1", []byte("x")))
	readFrame(t, cb)
	if err := s.UnbindKey(b.key); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccountSuspended(accE, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupSuspended("qg_marker", true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(accE); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // let the closing connections log

	out := mu.String() + journal.String()
	if !strings.Contains(out, "event=bind_done") || !strings.Contains(out, `"event":"unbind"`) {
		t.Fatalf("expected account events in the output:\n%s", out)
	}
	for _, marker := range []string{"example-marker", "Marker.Person", "marker.person", "markerlogin", "424242"} {
		if strings.Contains(out, marker) {
			t.Fatalf("output contains %q:\n%s", marker, out)
		}
	}
}

// TestAccountsJournalReplayAfterRestore: an unbind and a suspension made
// after a backup survive a restore with --replay-journal (review 50 M6).
func TestAccountsJournalReplayAfterRestore(t *testing.T) {
	dir := testutil.TempDir(t)
	db := filepath.Join(dir, "relay.db")
	journalPath := filepath.Join(dir, "journal.jsonl")
	jf, err := os.OpenFile(journalPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // path under testutil.TempDir(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = jf.Close() })
	s, err := relay.Open(relay.Options{QueuePath: db, Accounts: relay.AccountsGitHub, Journal: relay.NewJournalWriter(jf)})
	if err != nil {
		t.Fatal(err)
	}
	a, b := newPeer(t), newPeer(t)
	accA := newAccount(t, s, "@a", "qg_r")
	accB := newAccount(t, s, "@b", "qg_r")
	accC := newAccount(t, s, "@c", "")
	for _, kb := range [][2]string{{a.key, accA}, {b.key, accB}} {
		if err := s.BindKeyForTest(kb[0], kb[1]); err != nil {
			t.Fatal(err)
		}
	}
	backup := filepath.Join(dir, "backup.db")
	if err := relay.Backup(db, backup); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := os.Chtimes(backup, past, past); err != nil {
		t.Fatal(err)
	}
	if err := s.UnbindKey(a.key); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccountSuspended(accB, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(accC); err != nil {
		t.Fatal(err)
	}
	s.Close()

	restored := filepath.Join(dir, "restored.db")
	if err := relay.Restore(backup, restored, false); err != nil {
		t.Fatal(err)
	}
	applied, skipped, err := relay.ReplayJournal(restored, journalPath, past)
	if err != nil || applied != 3 || skipped != 0 {
		t.Fatalf("replay: applied %d skipped %d err %v; want 3, 0", applied, skipped, err)
	}
	adm, err := relay.OpenAdmin(restored, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adm.Close() }()
	if sa, keys, err := adm.Account(accA); err != nil || len(keys) != 0 || sa.Keys != 0 {
		t.Fatalf("A after replay: %+v %v %v, want no keys", sa, keys, err)
	}
	if sb, _, err := adm.Account(accB); err != nil || sb.State != "suspended" {
		t.Fatalf("B after replay: %+v %v, want suspended", sb, err)
	}
	if _, _, err := adm.Account(accC); !errors.Is(err, relay.ErrNoAccount) {
		t.Fatalf("C after replay: %v, want deleted", err)
	}
}

func TestAccountsModeValidated(t *testing.T) {
	if _, err := relay.Open(relay.Options{Accounts: "gitlab"}); err == nil {
		t.Fatal("accounts mode gitlab accepted")
	}
	s := relay.New(relay.Options{Accounts: relay.AccountsGitHub})
	t.Cleanup(s.Close)
	if _, err := s.EnsureAccount("email", "a@b.invalid", "a@b.invalid"); !errors.Is(err, relay.ErrProviderDisabled) {
		t.Fatalf("email on a github-only relay: %v", err)
	}
	id1, err := s.EnsureAccount("github", "7", "@old")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.EnsureAccount("github", "7", "@renamed")
	if err != nil || id1 != id2 || !strings.HasPrefix(id1, "acc_") {
		t.Fatalf("same subject: %q %q %v", id1, id2, err)
	}
}

// lockedWriter is a mutex-guarded writer for loggers shared by goroutines.
type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (l *lockedWriter) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.String()
}
