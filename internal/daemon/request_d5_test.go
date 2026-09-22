package daemon

// D5 (Docs/protocol/request.md §Submitting step 2, §Receiving step 3): which
// relay URLs count as loopback, the receive-side trust lookup and the
// submit-side refusal.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/presence"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestRelayIsNonLoopback(t *testing.T) {
	cases := map[string]bool{
		"":                          false, // no relay configured
		"ws://127.0.0.1:8787":       false,
		"ws://127.1.2.3:8787/v1":    false,
		"ws://localhost:8787":       false,
		"wss://LOCALHOST":           false,
		"ws://[::1]:8787":           false,
		"wss://relay.example.com":   true,
		"wss://localhost.evil.com":  true,
		"ws://127.0.0.1.nip.io":     true,
		"ws://10.0.0.1:8787":        true,
		"ws://[::2]:8787":           true,
		"127.0.0.1:8787":            true, // unparseable as a URL with a host: fail closed
		"ws://":                     true,
		"ws://user@127.0.0.1:8787":  false,
		"ws://127.0.0.1@evil.com/x": true,
	}
	for url, want := range cases {
		if got := relayIsNonLoopback(url); got != want {
			t.Errorf("relayIsNonLoopback(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestUnverifiedPeerReadsTrustInTx(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	for key, trust := range map[string]string{"relaypeer": "relay", "codepeer": "code", "teampeer": "team"} {
		if _, err := db.Exec(`INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust) VALUES (?, ?, 'h', '[]', '{}', 'now', ?)`,
			key, key, trust); err != nil {
			t.Fatal(err)
		}
	}
	check := func(nonLoopback bool, peer string, want bool) {
		t.Helper()
		rs := newRequestStore(db, "self", nil, nil, nil, nonLoopback)
		// Inside a transaction that holds the pool's only connection, as apply does.
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		got, err := rs.UnverifiedPeer(tx, peer)
		if err != nil || got != want {
			t.Errorf("nonLoopback=%v peer %s: = %v, %v; want %v", nonLoopback, peer, got, err, want)
		}
	}
	check(true, "relaypeer", true)
	check(true, "codepeer", false)
	check(true, "teampeer", false)
	check(true, "unknown", false)
	check(false, "relaypeer", false)

	// A read error is returned, never treated as "not relay".
	rs := newRequestStore(db, "self", nil, nil, nil, true)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if _, err := rs.UnverifiedPeer(tx, "relaypeer"); err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Errorf("UnverifiedPeer on a finished tx: err = %v, want an error", err)
	}
}

// TestRequestSubmitRefusesRelayTrust is the submit side of D5 (ticket 1.4c
// acceptance, Docs/protocol/request.md §Submitting step 2): on a non-loopback
// relay a trust=relay peer is refused with unverified_peer before team
// resolution. A code-paired peer, or any peer on a loopback relay, gets past
// D5 and fails later on no_shared_team (no teams exist here).
func TestRequestSubmitRefusesRelayTrust(t *testing.T) {
	ctx := context.Background()
	dir := testutil.TempDir(t)
	st, err := store.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	keyOf := map[string]string{}
	for _, trust := range []string{peers.TrustRelay, "code"} {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		keyOf[trust] = envelope.KeyString(pub)
		if _, err := db.Exec(`INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust) VALUES (?, ?, 'h', '[]', '{}', 'now', ?)`,
			keyOf[trust], trust+"peer", trust); err != nil {
			t.Fatal(err)
		}
	}
	ps := peers.NewStore(db)
	ts := team.NewStore(db, ps, "self")

	for i, nonLoopback := range []bool{true, false} {
		p, err := paths.In(filepath.Join(dir, fmt.Sprint(i)))
		if err != nil {
			t.Fatal(err)
		}
		ln, err := ipc.Listen(p.Endpoint)
		if err != nil {
			t.Fatal(err)
		}
		srv := ipc.NewServer()
		registerRequest(srv, presence.NewStore(db), newRequestStore(db, "self", nil, nil, ts, nonLoopback), ps, ts, nil, nonLoopback)
		sctx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- srv.Serve(sctx, ln) }()

		for trust, want := range map[string]string{peers.TrustRelay: CodeUnverifiedPeer, "code": CodeNoSharedTeam} {
			if !nonLoopback {
				want = CodeNoSharedTeam
			}
			err := ipc.Call(ctx, p.Endpoint, "request_submit", RequestSubmitParams{
				To: keyOf[trust], Type: "task", Title: "t", Brief: "b",
			}, nil)
			var ie *ipc.Error
			if !errors.As(err, &ie) || ie.Code != want {
				t.Errorf("nonLoopback=%v trust=%s: err = %v, want %s", nonLoopback, trust, err, want)
			}
		}
		cancel()
		<-done
	}
}
