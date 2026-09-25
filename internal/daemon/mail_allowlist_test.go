package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// mail_submit is an allowlist (review 36 L7): it refuses every kind the
// daemon's own receiver registers, walked from the receiver the daemon
// builds with real stores (plus the device kinds and keys/ack), and lets
// only kinds the daemon does not own through.
func TestMailSubmitRefusesEveryDaemonKind(t *testing.T) {
	t.Setenv(mail.DebugEnv, "1") // so the debug kind is registered too, and must stay sendable
	ctx := context.Background()
	dir := testutil.TempDir(t)
	st, err := store.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	self := envelope.KeyString(pub)
	ps := peers.NewStore(db)
	log := audit.New(db)
	ts := team.NewStore(db, ps, self)
	ws := &worksession.Store{DB: db, Self: self}
	rs := &request.Store{DB: db, Self: self, Sessions: ws}
	rs.Debates = &debate.Store{DB: db, Self: self, Requests: rs, Sessions: ws}
	caps := &capability.Store{DB: db}
	rcv, _ := newMailReceiver(db, log, nil, pub, nil, nil, ts, rs, ws, caps)
	kinds := withDeviceKinds(rcv.Kinds, &device.Store{DB: db, Self: self}, self, deviceHooks{})
	kinds["ack"] = mail.Kind{}
	if len(kinds) < 20 {
		t.Fatalf("only %d kinds registered: the walk would prove little", len(kinds))
	}

	p, err := paths.In(filepath.Join(dir, "ep"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	ln, err := ipc.Listen(p.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ob := &mail.Outbox{DB: db, Priv: func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), priv...), nil }, Peers: fakeMailPeers{pub: make([]byte, 32)}}
	srv := ipc.NewServer()
	registerMail(srv, ob, ps)
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(sctx, ln) }()
	t.Cleanup(func() { cancel(); <-done })
	submit := func(kind string) error {
		cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
		defer ccancel()
		var res MailSubmitResult
		return ipc.Call(cctx, p.Endpoint, "mail_submit", MailSubmitParams{To: "@nobody", Kind: kind, Body: []byte(`{}`)}, &res)
	}
	code := func(err error) string {
		var ie *ipc.Error
		if errors.As(err, &ie) {
			return ie.Code
		}
		return ""
	}

	for _, k := range []string{debate.MailEntry, debate.MailReveal, debate.MailClose} {
		if _, ok := kinds[k]; !ok {
			t.Errorf("the receiver does not register %s", k)
		}
	}
	for kind := range kinds {
		if kind == mail.DebugKind {
			continue
		}
		if !daemonOwnedKind(kind) {
			t.Errorf("kind %q is registered by the daemon but not refused by mail_submit", kind)
		}
		if err := submit(kind); code(err) != ipc.CodeBadRequest {
			t.Errorf("mail_submit %q: err = %v, want bad_request", kind, err)
		}
	}
	// Reserved namespaces are refused even for a kind no build registers yet.
	for _, kind := range []string{"device.scope", "ws.future", "grant.extend", "request.nudge", "team.kick",
		"debate.x", "debate.constraint", "debate.sign", "decision.x"} {
		if err := submit(kind); code(err) != ipc.CodeBadRequest {
			t.Errorf("mail_submit %q: err = %v, want bad_request", kind, err)
		}
	}
	// A kind the daemon does not own gets past the check (and then fails on
	// the unknown peer, which proves the check let it through).
	for _, kind := range []string{mail.DebugKind, "x.test", "app.custom"} {
		if err := submit(kind); code(err) != CodeUnknownPeer {
			t.Errorf("mail_submit %q: err = %v, want unknown_peer (allowed kind)", kind, err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("outbox rows = %d (%v), want none", n, err)
	}
}
