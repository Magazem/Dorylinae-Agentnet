package daemon_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// Offline delivery harness: two real daemons and a real relay with a
// persistent queue, started and stopped independently.

const harnessSecret = "the-plaintext-marker-must-never-reach-the-relay"

// leaksSecret reports whether b holds harnessSecret literally or in any
// base64 or base64url encoding, at any byte alignment. A frame carries its
// payload base64-encoded, so a literal search alone would miss a plaintext
// payload (review 10).
func leaksSecret(b []byte) bool {
	if bytes.Contains(b, []byte(harnessSecret)) {
		return true
	}
	for off := 0; off < 3; off++ {
		src := append(make([]byte, off), harnessSecret...)
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawURLEncoding} {
			s := enc.EncodeToString(src)
			// The first and last 4-character groups may mix in neighbouring bytes.
			if bytes.Contains(b, []byte(s[4:len(s)-4])) {
				return true
			}
		}
	}
	return false
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type harnessRelay struct {
	t     *testing.T
	addr  string
	queue string
	logs  *syncBuf
	rs    *relay.Server
	srv   *http.Server
}

func (r *harnessRelay) start() {
	r.t.Helper()
	rs, err := relay.Open(relay.Options{
		Logger:    slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		QueuePath: r.queue,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	var ln net.Listener
	for i := 0; ; i++ { // the port may linger for a moment after a restart
		if ln, err = net.Listen("tcp", r.addr); err == nil {
			break
		}
		if i > 100 {
			r.t.Fatalf("relay cannot listen on %s: %v", r.addr, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.rs = rs
	r.srv = &http.Server{Handler: rs, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = r.srv.Serve(ln) }()
}

func (r *harnessRelay) stop() {
	_ = r.srv.Close()
	r.rs.Close()
}

func (r *harnessRelay) url() string { return "ws://" + r.addr }

func newHarnessRelay(t *testing.T) *harnessRelay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	dir, err := os.MkdirTemp("", "rq")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	r := &harnessRelay{t: t, addr: addr, queue: filepath.Join(dir, "relay-queue.db"), logs: &syncBuf{}}
	r.start()
	t.Cleanup(func() { r.stop() })
	return r
}

// harnessNode is one daemon that can be stopped and started again on the same
// data directory, so it keeps its identity, keys and outbox.
type harnessNode struct {
	t      *testing.T
	name   string
	p      paths.Paths
	relay  *harnessRelay
	key    string
	logs   *syncBuf
	cancel context.CancelFunc
	done   chan error
}

func newHarnessNode(t *testing.T, name string, r *harnessRelay) *harnessNode {
	t.Helper()
	dir, err := os.MkdirTemp("", "hn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := &harnessNode{t: t, name: name, p: p, relay: r, logs: &syncBuf{}}
	t.Cleanup(n.stop)
	return n
}

func (n *harnessNode) start() {
	n.t.Helper()
	ks, err := identity.NewKeystore(n.p.Dir, "file") // never touch the real keychain from tests
	if err != nil {
		n.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.RunWithOptions(ctx, n.p, ready, daemon.Options{
			Keystore:  ks,
			Identity:  &identity.Options{Name: n.name, Harness: "test-harness"},
			RelayURL:  n.relay.url(),
			Logger:    slog.New(slog.NewTextHandler(n.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
			MailKinds: map[string]mail.Kind{"note": {Inbox: true}},
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		n.t.Fatalf("daemon %s exited: %v", n.name, err)
	case <-time.After(15 * time.Second):
		cancel()
		n.t.Fatalf("daemon %s not ready", n.name)
	}
	n.cancel, n.done = cancel, done
	if n.key == "" {
		var id daemon.IdentityResult
		n.call("identity", nil, &id)
		n.key = id.Card.PublicKey
	}
}

func (n *harnessNode) stop() {
	if n.cancel == nil {
		return
	}
	n.cancel()
	select {
	case <-n.done:
	case <-time.After(15 * time.Second):
		n.t.Errorf("daemon %s did not stop", n.name)
	}
	n.cancel = nil
}

func (n *harnessNode) call(method string, params, out any) {
	n.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ipc.Call(ctx, n.p.Endpoint, method, params, out); err != nil {
		n.t.Fatalf("%s %s: %v", n.name, method, err)
	}
}

// query runs a read-only query on the node's database (the node may be stopped).
func (n *harnessNode) query(q string, dest ...any) error {
	n.t.Helper()
	st, err := store.Open(context.Background(), n.p.DB)
	if err != nil {
		n.t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	return st.DB().QueryRow(q).Scan(dest...)
}

func (n *harnessNode) count(q string) int {
	n.t.Helper()
	var c int
	if err := n.query(q, &c); err != nil {
		n.t.Fatal(err)
	}
	return c
}

func (n *harnessNode) outboxState(id string) (state string, plaintext bool) {
	n.t.Helper()
	var s, signed, frame sql.NullString
	if err := n.query(`SELECT state, signed, frame FROM outbox WHERE id = '`+id+`'`, &s, &signed, &frame); err != nil {
		n.t.Fatal(err)
	}
	return s.String, signed.Valid || frame.Valid
}

func harnessWait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func harnessPair(t *testing.T, a, b *harnessNode) {
	t.Helper()
	var issued daemon.PairStatus
	// The relay lists a daemon slightly before the daemon sees its own ready.
	harnessWait(t, "A's relay connection", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := ipc.Call(ctx, a.p.Endpoint, "pair_new", nil, &issued)
		if err != nil && !strings.Contains(err.Error(), daemon.CodeRelayUnavailable) {
			t.Fatalf("pair_new: %v", err)
		}
		return err == nil
	})
	harnessWait(t, "pairing code", func() bool {
		a.call("pair_status", daemon.PairStatusParams{PairingID: issued.ID}, &issued)
		return issued.Code != ""
	})
	var red daemon.PairStatus
	b.call("pair_redeem", daemon.PairRedeemParams{Code: issued.Code}, &red)
	harnessWait(t, "pairing to complete", func() bool {
		b.call("pair_status", daemon.PairStatusParams{PairingID: red.ID}, &red)
		a.call("pair_status", daemon.PairStatusParams{PairingID: issued.ID}, &issued)
		return red.State == "complete" && issued.State == "complete"
	})
}

func (n *harnessNode) submit(to, kind, text string) daemon.MailSubmitResult {
	n.t.Helper()
	var res daemon.MailSubmitResult
	n.call("mail_submit", daemon.MailSubmitParams{To: to, Kind: kind, Body: []byte(`{"text":"` + text + `"}`)}, &res)
	return res
}

func waitRelayConnected(t *testing.T, r *harnessRelay, keys ...string) {
	t.Helper()
	harnessWait(t, "daemons to connect to the relay", func() bool {
		for _, k := range keys {
			if !r.rs.Connected(k) {
				return false
			}
		}
		return true
	})
}

func testOfflineDelivery(t *testing.T, restartRelay bool) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)

	// B goes offline, A sends, A goes offline.
	b.stop()
	harnessWait(t, "relay to see B leave", func() bool { return !r.rs.Connected(b.key) })
	begin := time.Now()
	res := a.submit(b.key, "note", harnessSecret)
	if res.State != "queued" || !mail.ValidID(res.ID) {
		t.Fatalf("submit = %+v, want state queued", res)
	}
	if d := time.Since(begin); d >= 2*time.Second {
		t.Errorf("submit took %v, want < 2s", d)
	}
	harnessWait(t, "A's mail to reach the relay", func() bool { s, _ := a.outboxState(res.ID); return s == "relayed" })
	a.stop()

	if restartRelay {
		r.stop()
		r.start()
	}

	// B comes back: the mail is delivered from the relay queue, exactly once, and acked.
	b.start()
	harnessWait(t, "B to process the mail", func() bool { return b.count(`SELECT COUNT(*) FROM mail_inbox`) == 1 })
	if n := b.count(`SELECT COUNT(*) FROM mail_seen WHERE id = '` + res.ID + `'`); n != 1 {
		t.Fatalf("mail_seen rows = %d, want 1", n)
	}
	// The audit row is written asynchronously after the inbox row: poll for it.
	harnessWait(t, "the mail.in audit row", func() bool { return b.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'mail.in'`) >= 1 })
	if n := b.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'mail.in'`); n != 1 {
		t.Fatalf("mail.in audit rows = %d, want 1", n)
	}
	var signed string
	if err := b.query(`SELECT signed FROM mail_inbox WHERE id = '`+res.ID+`'`, &signed); err != nil || !strings.Contains(signed, harnessSecret) {
		t.Fatalf("inbox row does not keep the verified plaintext: %v", err)
	}

	// A comes back and learns of the ack.
	a.start()
	harnessWait(t, "A's mail to be delivered", func() bool { s, _ := a.outboxState(res.ID); return s == "delivered" })
	if _, left := a.outboxState(res.ID); left {
		t.Error("delivered row still holds plaintext")
	}
	var st daemon.StatusResult
	a.call("status", nil, &st)
	if st.Outbox.Queued != 0 || st.Outbox.Relayed != 0 {
		t.Errorf("status outbox = %+v, want nothing pending", st.Outbox)
	}

	// Exactly one delivery: B still has one inbox row after everything settled.
	time.Sleep(300 * time.Millisecond)
	if n := b.count(`SELECT COUNT(*) FROM mail_inbox`); n != 1 {
		t.Fatalf("inbox rows = %d, want 1", n)
	}

	// The relay only ever saw ciphertext: not in its logs, not in its queue
	// file, in any encoding. Neither daemon logs the plaintext either.
	if leaksSecret([]byte(r.logs.String())) {
		t.Error("relay log contains the plaintext")
	}
	for _, n := range []*harnessNode{a, b} {
		if leaksSecret([]byte(n.logs.String())) {
			t.Errorf("%s's log contains the plaintext", n.name)
		}
	}
	r.stop()
	for _, f := range []string{r.queue, r.queue + "-wal"} {
		if raw, err := os.ReadFile(f); err == nil && leaksSecret(raw) { //nolint:gosec // test reads its own temp-dir files
			t.Errorf("%s contains the plaintext", filepath.Base(f))
		}
	}
	r.start() // for the cleanup
}

func TestOfflineDeliveryTwoDaemons(t *testing.T) { testOfflineDelivery(t, false) }

func TestOfflineDeliveryRelayRestart(t *testing.T) { testOfflineDelivery(t, true) }

// A wire check: what the relay holds for an offline peer is a sealed mail
// envelope whose payload starts with the version byte and never the plaintext.
func TestRelayQueueHoldsOnlyCiphertext(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	b.stop()
	harnessWait(t, "relay to see B leave", func() bool { return !r.rs.Connected(b.key) })
	res := a.submit(b.key, "note", harnessSecret)
	harnessWait(t, "A's mail to reach the relay", func() bool { s, _ := a.outboxState(res.ID); return s == "relayed" })

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(r.queue)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var frame []byte
	harnessWait(t, "the envelope in the relay queue", func() bool {
		return db.QueryRow(`SELECT frame FROM queue WHERE id = ?`, res.ID).Scan(&frame) == nil
	})
	if leaksSecret(frame) {
		t.Fatal("queued frame contains the plaintext")
	}
	e, err := envelope.Parse(frame)
	if err != nil || e.Type != "mail" || e.ID != res.ID || e.To != b.key || len(e.Payload) < mail.MinPayload || e.Payload[0] != mail.PayloadVersion {
		t.Fatalf("queued envelope = %+v, %v", e, err)
	}
	// The decoded payload is version, key_id, enc and an AEAD ciphertext of
	// exactly the signed plaintext A keeps, and the plaintext is not in it.
	var signed, keyID string
	if err := a.query(`SELECT signed, key_id FROM outbox WHERE id = '`+res.ID+`'`, &signed, &keyID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(signed, harnessSecret) {
		t.Fatal("A's outbox row does not hold the signed plaintext")
	}
	if leaksSecret(e.Payload) {
		t.Fatal("the decoded payload contains the plaintext")
	}
	if len(e.Payload) != mail.MinPayload+len(signed) {
		t.Fatalf("payload is %d bytes, want %d (the sealed signed plaintext)", len(e.Payload), mail.MinPayload+len(signed))
	}
	if got := hex.EncodeToString(e.Payload[1 : 1+mail.KeyIDLen]); got != keyID {
		t.Fatalf("payload key_id %s, outbox key_id %s", got, keyID)
	}
}

// The leak check itself finds an encoded plaintext at every alignment.
func TestLeaksSecretFindsEncodings(t *testing.T) {
	for _, prefix := range []string{"", "a", "ab", "abc"} {
		plain := []byte(`{"msg":` + prefix + harnessSecret + `}`)
		for _, s := range []string{string(plain), base64.StdEncoding.EncodeToString(plain), base64.RawURLEncoding.EncodeToString(plain)} {
			if !leaksSecret([]byte(s)) {
				t.Errorf("prefix %q: %s not detected", prefix, s)
			}
		}
	}
	if leaksSecret([]byte(base64.StdEncoding.EncodeToString([]byte("unrelated")))) {
		t.Error("false positive")
	}
}
