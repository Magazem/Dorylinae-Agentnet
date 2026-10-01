package daemon_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/noise"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/session"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// R55-F14 acceptance test (Docs/review/72-r55-f14-spec.md §5 test 1; inverts
// review 55 T10-01): frames a relay alone can send write no audit row while
// the daemon runs, and a clean stop adds exactly one relay.reject_summary row
// counting them, minus the unpaired ones (OD-F14-7 (b)).

// rgRelay is a relay that authenticates any client and lets the test push
// arbitrary frames to it. Envelopes the daemon sends go to onEnvelope.
type rgRelay struct {
	mu         sync.Mutex
	conn       *websocket.Conn
	ready      chan struct{}
	onEnvelope func(envelope.Envelope)
}

func newRGRelay(t *testing.T) (*rgRelay, string) {
	t.Helper()
	r := &rgRelay{ready: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ws, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		ws.SetReadLimit(envelope.MaxFrameBytes)
		ctx := req.Context()
		challenge, _ := json.Marshal(envelope.Control{Op: envelope.OpChallenge, Version: 1, Nonce: envelope.EncodeNonce(make([]byte, envelope.NonceSize))})
		_ = ws.Write(ctx, websocket.MessageText, challenge)
		if _, _, err := ws.Read(ctx); err != nil {
			return
		}
		readyFrame, _ := json.Marshal(envelope.Control{Op: envelope.OpReady})
		_ = ws.Write(ctx, websocket.MessageText, readyFrame)
		r.mu.Lock()
		first := r.conn == nil
		r.conn = ws
		r.mu.Unlock()
		if first {
			close(r.ready)
		}
		for {
			_, frame, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if f, err := envelope.Classify(frame); err != nil || f.Control != nil {
				continue // acks
			}
			e, err := envelope.Parse(frame)
			if err != nil {
				continue
			}
			r.mu.Lock()
			h := r.onEnvelope
			r.mu.Unlock()
			if h != nil {
				h(e)
			}
		}
	}))
	t.Cleanup(srv.Close)
	return r, "ws" + strings.TrimPrefix(srv.URL, "http")
}

func (r *rgRelay) push(t *testing.T, e envelope.Envelope) {
	t.Helper()
	frame, err := e.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.conn.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
}

// rgLink is the test peer's session sender: into the relay, towards the daemon.
type rgLink struct {
	t *testing.T
	r *rgRelay

	mu   sync.Mutex
	data []envelope.Envelope // session.data frames the peer sent
}

func (l *rgLink) Connected() bool { return true }

func (l *rgLink) Send(_ context.Context, e envelope.Envelope) error {
	if e.Type == session.TypeData {
		l.mu.Lock()
		e.Payload = bytes.Clone(e.Payload)
		l.data = append(l.data, e)
		l.mu.Unlock()
	}
	frame, err := e.Marshal()
	if err != nil {
		return err
	}
	l.r.mu.Lock()
	defer l.r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return l.r.conn.Write(ctx, websocket.MessageText, frame)
}

func rgID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func rgKey() (string, ed25519.PrivateKey) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return envelope.KeyString(pub), priv
}

type rgKeys struct {
	kid  mail.KeyID
	priv *ecdh.PrivateKey
}

func (k rgKeys) MailboxKey(id mail.KeyID) (*ecdh.PrivateKey, bool) {
	if id != k.kid {
		return nil, false
	}
	return k.priv, true
}

type rgRow struct {
	action string
	detail string
}

func rgAuditRows(t *testing.T, path string) []rgRow {
	t.Helper()
	db, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT action, detail FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []rgRow
	for rows.Next() {
		var r rgRow
		var d sql.NullString
		if err := rows.Scan(&r.action, &d); err != nil {
			t.Fatal(err)
		}
		r.detail = d.String
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRelayDrivenFramesWriteNoAuditRows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := testutil.TempDir(t)
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	ks, err := identity.NewKeystore(p.Dir, "file")
	if err != nil {
		t.Fatal(err)
	}

	// The paired peer: its identity and a row in peers (the daemon checks
	// pairing against the table).
	peerKey, peerPriv := rgKey()
	peerPub := peerPriv.Public().(ed25519.PublicKey)
	st, err := store.Open(ctx, p.DB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys)
VALUES (?, 'peer', 'h', '[]', '{}', '2026-01-01T00:00:00Z', 'code', '[]')`, peerKey); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// The daemon's mailbox key, known to the test so it can seal genuine mail.
	mbox, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mboxPub := mbox.PublicKey().Bytes()
	keys := rgKeys{kid: mail.KeyIDOf(mboxPub), priv: mbox}

	relay, url := newRGRelay(t)
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.RunWithOptions(ctx, p, ready, daemon.Options{Keystore: ks, RelayURL: url, MailboxKeys: keys})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("daemon not ready")
	}
	var idRes daemon.IdentityResult
	cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
	if err := ipc.Call(cctx, p.Endpoint, "identity", nil, &idRes); err != nil {
		t.Fatal(err)
	}
	ccancel()
	self := idRes.Card.PublicKey
	select {
	case <-relay.ready:
	case <-time.After(20 * time.Second):
		t.Fatal("daemon never connected to the relay")
	}

	// The peer's session manager, talking to the daemon through the relay.
	pst, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "peer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pst.Close() })
	static, err := noise.NewStatic(peerPub, func(m []byte) ([]byte, error) { return ed25519.Sign(peerPriv, m), nil })
	if err != nil {
		t.Fatal(err)
	}
	link := &rgLink{t: t, r: relay}
	peer := session.NewManager(session.Config{
		Static: static, Audit: audit.New(pst.DB()), Sender: link, PingTimeout: 10 * time.Second,
		IsPaired: func(_ context.Context, k string) (bool, error) { return k == self, nil },
	})
	t.Cleanup(peer.Close)
	relay.mu.Lock()
	relay.onEnvelope = func(e envelope.Envelope) {
		if strings.HasPrefix(e.Type, "session.") {
			peer.HandleEnvelope(e)
		}
	}
	relay.mu.Unlock()

	// ping is the session barrier: the daemon's session worker is FIFO, so a
	// completed ping means every earlier session frame was handled.
	ping := func() {
		t.Helper()
		st, err := peer.Ping(ctx, session.PeerRef{PublicKey: self})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for st.State == session.StatePending {
			if time.Now().After(deadline) {
				t.Fatal("ping did not complete")
			}
			time.Sleep(10 * time.Millisecond)
			st, _ = peer.Get(st.ID)
		}
		if st.State != session.StateComplete {
			t.Fatalf("ping = %+v", st)
		}
	}
	ping() // opens the session: session.open is a peer-caused row

	seal := func(kind string, body any, created time.Time, pub []byte) envelope.Envelope {
		t.Helper()
		sl, err := mail.Seal(mail.SealInput{Priv: peerPriv, To: self, MailboxPub: pub, Kind: kind, Body: body, Created: created})
		if err != nil {
			t.Fatal(err)
		}
		return envelope.Envelope{From: peerKey, To: self, Type: "mail", ID: sl.ID, TS: time.Now().UTC().Format(time.RFC3339), Payload: sl.Payload}
	}
	announcement := func(created time.Time) (map[string]any, string) {
		t.Helper()
		x, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := mail.SignAnnouncement(peerPub, func(m []byte) ([]byte, error) { return ed25519.Sign(peerPriv, m), nil }, x.PublicKey().Bytes(), created)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m, envelope.KeyString(x.PublicKey().Bytes())
	}
	// mailBarrier sends a valid keys mail and waits until the daemon has
	// merged it: the mail worker is FIFO, so every earlier mail was handled.
	// A keys mail writes no audit row (mail.md §Receiver). Each announcement
	// is newer than the last, or the merge ignores it.
	annBase, annN := time.Now().Add(-time.Hour), 0
	mailBarrier := func() {
		t.Helper()
		annN++
		ann, pub := announcement(annBase.Add(time.Duration(annN) * time.Second))
		relay.push(t, seal("keys", map[string]any{"announcement": ann}, time.Now(), mboxPub))
		deadline := time.Now().Add(15 * time.Second)
		for {
			db, err := store.OpenReadOnly(p.DB)
			if err != nil {
				t.Fatal(err)
			}
			var mk string
			err = db.QueryRow(`SELECT mailbox_keys FROM peers WHERE public_key = ?`, peerKey).Scan(&mk)
			_ = db.Close()
			if err == nil && strings.Contains(mk, pub) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the keys barrier was never applied")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	mailBarrier()

	link.mu.Lock()
	captured := link.data[len(link.data)-1] // a session.data the daemon accepted
	link.mu.Unlock()
	baseline := rgAuditRows(t, p.DB)

	const chunk = 200 // below the 256-envelope mail queue and session inbox
	// 1500 unpaired mail and 1500 unpaired session.data frames.
	for i := range 1500 {
		from, _ := rgKey()
		relay.push(t, envelope.Envelope{From: from, To: self, Type: "mail", ID: "m-" + rgID(), TS: "2026-01-02T03:04:05Z", Payload: []byte("junk")})
		relay.push(t, envelope.Envelope{From: from, To: self, Type: session.TypeData, ID: rgID(), TS: "2026-01-02T03:04:05Z", Payload: make([]byte, 40)})
		if (i+1)%chunk == 0 {
			mailBarrier()
			ping()
		}
	}
	mailBarrier()
	ping()

	// 300 mail frames with the paired peer's from, failing steps 2 to 4.
	other, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		relay.push(t, envelope.Envelope{From: peerKey, To: self, Type: "mail", ID: "m-" + rgID(), TS: "2026-01-02T03:04:05Z", Payload: []byte{9}}) // step 2
		relay.push(t, seal("request", map[string]any{}, time.Now(), other.PublicKey().Bytes()))                                                    // step 3
		bad := seal("request", map[string]any{}, time.Now(), mboxPub)
		bad.Payload[len(bad.Payload)-1] ^= 1
		relay.push(t, bad) // step 4
		if (i+1)%60 == 0 {
			mailBarrier()
		}
	}
	mailBarrier()

	// A forged session.init then session.fin with the paired peer's from.
	// Message 1 is only an ephemeral key, so the relay makes it with a key of
	// its own and names the peer as from.
	_, relayPriv := rgKey()
	forger, err := noise.NewStatic(relayPriv.Public().(ed25519.PublicKey), func(m []byte) ([]byte, error) { return ed25519.Sign(relayPriv, m), nil })
	if err != nil {
		t.Fatal(err)
	}
	hs, err := noise.NewHandshake(forger, self, true)
	if err != nil {
		t.Fatal(err)
	}
	msg1, _, err := hs.Write()
	if err != nil {
		t.Fatal(err)
	}
	sid := make([]byte, session.SIDSize)
	_, _ = rand.Read(sid)
	relay.push(t, envelope.Envelope{From: peerKey, To: self, Type: session.TypeInit, ID: rgID(), TS: "2026-01-02T03:04:05Z", Payload: append(bytes.Clone(sid), msg1...)})
	garbage := make([]byte, 96)
	_, _ = rand.Read(garbage)
	relay.push(t, envelope.Envelope{From: peerKey, To: self, Type: session.TypeFin, ID: rgID(), TS: "2026-01-02T03:04:05Z", Payload: append(bytes.Clone(sid), garbage...)})

	// 100 replays of one captured session.data, each under a fresh envelope id
	// (the relay re-ids them to pass relayclient's seen-set).
	for i := range 100 {
		r := captured
		r.ID = rgID()
		relay.push(t, r)
		if (i+1)%chunk == 0 {
			ping()
		}
	}
	ping()

	// One genuine mail replayed with created 31 days old, and one genuine keys
	// mail replayed after its announcement's not_after.
	stale := seal("request", map[string]any{}, time.Now().Add(-31*24*time.Hour), mboxPub)
	expiredAnn, _ := announcement(time.Now().Add(-mail.AnnouncementLifetime - time.Hour))
	expired := seal("keys", map[string]any{"announcement": expiredAnn}, time.Now(), mboxPub)
	for range 5 {
		relay.push(t, stale)
		relay.push(t, expired)
	}
	mailBarrier()
	ping()

	rows := rgAuditRows(t, p.DB)
	if len(rows) != len(baseline) {
		t.Fatalf("relay-driven frames wrote %d audit rows: %v", len(rows)-len(baseline), rows[len(baseline):])
	}
	for _, r := range rows {
		if r.action == mail.ActionReject || r.action == "session.reject" {
			t.Fatalf("reject row: %+v", r)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("daemon did not stop")
	}
	after := rgAuditRows(t, p.DB)[len(baseline):]
	var summaries []rgRow
	for _, r := range after {
		switch r.action {
		case daemon.ActionRejectSummary:
			summaries = append(summaries, r)
		case audit.ActionDaemonStop:
		default:
			t.Errorf("unexpected row at stop: %+v", r)
		}
	}
	if len(summaries) != 1 {
		t.Fatalf("%d relay.reject_summary rows, want 1: %v", len(summaries), after)
	}
	var d struct {
		Since   string         `json:"since"`
		Until   string         `json:"until"`
		Mail    map[string]int `json:"mail"`
		Session map[string]int `json:"session"`
	}
	dec := json.NewDecoder(strings.NewReader(summaries[0].detail))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		t.Fatalf("detail %s: %v", summaries[0].detail, err)
	}
	wantMail := map[string]int{mail.ReasonMalformed: 100, mail.ReasonKeyMiss: 100, mail.ReasonDecrypt: 100, mail.ReasonStale: 5, mail.ReasonBadKeys: 5}
	wantSession := map[string]int{session.ReasonReplay: 100, session.ReasonBadHandshake: 1}
	if !reflect.DeepEqual(d.Mail, wantMail) || !reflect.DeepEqual(d.Session, wantSession) {
		t.Fatalf("summary = %s\nwant mail %v session %v", summaries[0].detail, wantMail, wantSession)
	}
	if _, err := time.Parse(time.RFC3339, d.Since); err != nil {
		t.Fatalf("since %q", d.Since)
	}
	if _, err := time.Parse(time.RFC3339, d.Until); err != nil {
		t.Fatalf("until %q", d.Until)
	}
	if strings.Contains(summaries[0].detail, peerKey) {
		t.Fatal("the summary names a peer")
	}
}
