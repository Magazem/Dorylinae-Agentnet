package daemon_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

func TestLifecycle(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file") // never touch the real keychain from tests
	dir, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, p, ready) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}

	// status reports our PID, and answers quickly.
	var res daemon.StatusResult
	start := time.Now()
	cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
	if err := ipc.Call(cctx, p.Endpoint, "status", nil, &res); err != nil {
		t.Fatalf("status: %v", err)
	}
	ccancel()
	if time.Since(start) > 2*time.Second {
		t.Fatal("status too slow")
	}
	if res.PID != os.Getpid() || res.UptimeSeconds < 0 || res.StartedAt == "" {
		t.Fatalf("unexpected status: %+v", res)
	}

	// A second daemon on the same home must refuse to start.
	if err := daemon.Run(context.Background(), p, nil); err == nil {
		t.Fatal("second daemon should fail")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop")
	}

	// Endpoint is gone after stop.
	cctx, ccancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer ccancel()
	if err := ipc.Call(cctx, p.Endpoint, "status", nil, nil); !errors.Is(err, ipc.ErrNotRunning) {
		t.Fatalf("after stop: %v, want ErrNotRunning", err)
	}

	// DB exists with a migrations table and a start, identity.create and stop row.
	st, err := store.Open(context.Background(), p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM migrations`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("migrations rows = %d, err = %v", n, err)
	}
	evs, err := audit.New(st.DB()).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// mailbox.rotate is written by a background task, so whether it lands before
	// the stop row depends on timing; it is not part of the lifecycle under test.
	kept := evs[:0]
	for _, e := range evs {
		if e.Action != "mailbox.rotate" {
			kept = append(kept, e)
		}
	}
	evs = kept
	if len(evs) != 3 || evs[0].Action != audit.ActionDaemonStart || evs[1].Action != identity.ActionCreate || evs[2].Action != audit.ActionDaemonStop {
		t.Fatalf("audit events = %+v", evs)
	}
}

// TestIdentityIPCNeverExposesPrivateKey starts the daemon twice on one home
// and checks the "identity" method: the card verifies, the second start
// reuses the key, and no raw IPC response (for any request shape) or audit
// row contains the private key in any common encoding.
func TestIdentityIPCNeverExposesPrivateKey(t *testing.T) {
	dir, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	ks, err := identity.NewKeystore(p.Dir, "file")
	if err != nil {
		t.Fatal(err)
	}
	opts := daemon.Options{Keystore: ks, Identity: &identity.Options{Name: "ipc-agent", Harness: "hermes"}}

	var raw [][]byte
	var cards []daemon.IdentityResult
	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		ready := make(chan struct{})
		done := make(chan error, 1)
		go func() { done <- daemon.RunWithOptions(ctx, p, ready, opts) }()
		select {
		case <-ready:
		case err := <-done:
			t.Fatalf("daemon exited early: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("daemon not ready")
		}

		var res daemon.IdentityResult
		cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
		if err := ipc.Call(cctx, p.Endpoint, "identity", nil, &res); err != nil {
			t.Fatalf("identity: %v", err)
		}
		ccancel()
		cards = append(cards, res)

		// Raw responses to a plain call and to requests fishing for the key.
		for _, req := range []string{
			`{"id":"1","method":"identity"}`,
			`{"id":"2","method":"identity","params":{"private":true,"include_private_key":true}}`,
			`{"id":"3","method":"private_key"}`,
			`{"id":"4","method":"identity.export"}`,
		} {
			conn, err := ipc.Dial(context.Background(), p.Endpoint)
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			_, _ = conn.Write([]byte(req + "\n"))
			line, err := bufio.NewReader(conn).ReadBytes('\n')
			_ = conn.Close()
			if err != nil {
				t.Fatalf("read %s: %v", req, err)
			}
			raw = append(raw, line)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	if cards[0].Card.PublicKey != cards[1].Card.PublicKey || cards[0].Signature != cards[1].Signature {
		t.Fatal("second start did not reuse the identity")
	}
	env, _ := json.Marshal(agentcard.Signed{Card: cards[0].Card, Signature: cards[0].Signature})
	if _, err := agentcard.Verify(env); err != nil {
		t.Fatalf("card from IPC does not verify: %v", err)
	}
	if cards[0].Card.Name != "ipc-agent" || cards[0].KeyBackend != "file" {
		t.Fatalf("unexpected result: %+v", cards[0])
	}

	seed, _, err := ks.Load()
	if err != nil {
		t.Fatal(err)
	}
	full := ed25519.NewKeyFromSeed(seed)
	var forms []string
	for _, b := range [][]byte{seed, full} {
		forms = append(forms, base64.RawURLEncoding.EncodeToString(b), base64.StdEncoding.EncodeToString(b),
			base64.RawStdEncoding.EncodeToString(b), hex.EncodeToString(b))
	}
	check := func(what string, data []byte) {
		for _, f := range forms {
			if bytes.Contains(data, []byte(f)) {
				t.Errorf("%s contains private key material", what)
			}
		}
	}
	for i, r := range raw {
		check(fmt.Sprintf("IPC response %d", i), r)
	}

	st, err := store.Open(context.Background(), p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	evs, err := audit.New(st.DB()).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	creates := 0
	for _, e := range evs {
		check("audit "+e.Action, e.Detail)
		if e.Action == identity.ActionCreate {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("identity.create rows = %d, want 1", creates)
	}
}
