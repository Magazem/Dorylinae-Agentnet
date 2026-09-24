package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/noise"
	"github.com/Magazem/Dorylinae-Agentnet/internal/session"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// memRelay routes envelopes between session managers in memory.
type memRelay struct {
	mu    sync.Mutex
	nodes map[string]*session.Manager
}

type memLink struct{ r *memRelay }

func (memLink) Connected() bool { return true }

func (l memLink) Send(_ context.Context, e envelope.Envelope) error {
	e.Payload = bytes.Clone(e.Payload)
	l.r.mu.Lock()
	to := l.r.nodes[e.To]
	l.r.mu.Unlock()
	if to != nil {
		to.HandleEnvelope(e)
	}
	return nil
}

func newMemManager(t *testing.T, r *memRelay, pub ed25519.PublicKey, priv ed25519.PrivateKey, h *grantHarness) *session.Manager {
	t.Helper()
	st, err := noise.NewStatic(pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil })
	if err != nil {
		t.Fatal(err)
	}
	m := session.NewManager(session.Config{
		Static: st, Audit: h.log, Sender: memLink{r}, PingTimeout: 5 * time.Second,
		IsPaired: func(context.Context, string) (bool, error) { return true, nil },
	})
	t.Cleanup(m.Close)
	r.mu.Lock()
	r.nodes[st.Identity()] = m
	r.mu.Unlock()
	return m
}

// TestFetchServedOverNoiseSession drives the daemon's fetch server through a
// real Noise session: a stat and a read, then the grantor's own row decides
// (revoked, then session no longer open) on the very next message.
func TestFetchServedOverNoiseSession(t *testing.T) {
	h, _ := newGrantHarness(t, "code", false)
	ctx := context.Background()
	dir := testutil.TempDir(t)
	content := bytes.Repeat([]byte("0123456789abcdef"), 5000) // 80000 bytes: 3 fragments
	if err := os.WriteFile(filepath.Join(dir, "f.bin"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	// The holder needs its private key for the Noise session: a fresh identity.
	hpub, hpriv, _ := ed25519.GenerateKey(rand.Reader)
	holder := envelope.KeyString(hpub)
	holderSID := h.openSession(holder, "r-"+strings.Repeat("b", 32))

	now := time.Now().UTC()
	tok, err := capability.Sign(h.priv, capability.Grant{
		V: 1, ID: capability.NewID(), Iss: h.self, Aud: holder, Session: holderSID, Action: capability.ActionFSRead,
		Resource: capability.Resource{Kind: capability.KindFS, Label: "res-ab12"},
		Nbf:      now.Add(-time.Minute), Exp: now.Add(time.Hour), Sensitive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := capability.Canonical(tok)
	rec := capability.Record{
		ID: tok.Grant.ID, Direction: capability.DirectionIssued, Peer: holder, Session: holderSID, Action: capability.ActionFSRead,
		Label: "res-ab12", Path: dir, Sensitive: true, Nbf: tok.Grant.Nbf, Exp: tok.Grant.Exp, Token: string(wire),
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.caps.InsertActiveTx(ctx, tx, rec); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	r := &memRelay{nodes: map[string]*session.Manager{}}
	gpub := ed25519.PublicKey(h.priv.Public().(ed25519.PublicKey))
	grantorMgr := newMemManager(t, r, gpub, h.priv, h)
	stop := startFetchServer(grantorMgr, h.caps, h.ws, h.log, h.self)
	t.Cleanup(stop)
	holderMgr := newMemManager(t, r, hpub, hpriv, h)
	resps := make(chan map[string]any, 64)
	holderMgr.Handle(capability.TypeFetchResp, func(_ string, pt []byte) {
		var m map[string]any
		_ = json.Unmarshal(pt, &m)
		resps <- m
	})
	if st, err := holderMgr.Ping(ctx, session.PeerRef{PublicKey: h.self, Name: "grantor"}); err != nil || st.State != session.StateComplete {
		t.Fatalf("ping = %+v, %v", st, err)
	}

	n := 0
	fetch := func(op, path string, extra map[string]any) []map[string]any {
		t.Helper()
		n++
		req := fmt.Sprintf("f-%032x", n)
		m := map[string]any{"type": "fetch.req", "req": req, "ts": time.Now().UTC().Format(time.RFC3339), "token": json.RawMessage(wire), "op": op, "path": path}
		for k, v := range extra {
			m[k] = v
		}
		pt, _ := json.Marshal(m)
		if err := holderMgr.SendData(ctx, h.self, pt); err != nil {
			t.Fatal(err)
		}
		var got []map[string]any
		deadline := time.After(5 * time.Second)
		for {
			select {
			case m := <-resps:
				got = append(got, m)
				if m["ok"] != true || m["data"] == nil || m["frag"].(float64) == m["frags"].(float64)-1 {
					return got
				}
			case <-deadline:
				t.Fatalf("no response to %s (got %v)", op, got)
			}
		}
	}

	st := fetch("stat", "f.bin", nil)
	if e := st[0]["entry"].(map[string]any); st[0]["ok"] != true || e["size"].(float64) != float64(len(content)) {
		t.Fatalf("stat = %v", st)
	}
	rd := fetch("read", "f.bin", map[string]any{"offset": 0, "length": 80000})
	var got []byte
	for _, m := range rd {
		b, _ := base64.StdEncoding.DecodeString(m["data"].(string))
		got = append(got, b...)
	}
	if len(rd) != 3 || !bytes.Equal(got, content) {
		t.Fatalf("read: %d fragments, equal = %v", len(rd), bytes.Equal(got, content))
	}
	// the audit rows carry no path
	waitAudit(t, h, "grant.fetch", 2)

	// the work session leaves open: the next message fails
	if _, err := h.db.Exec(`UPDATE work_sessions SET state = 'awaiting_result' WHERE id = ?`, holderSID); err != nil {
		t.Fatal(err)
	}
	if e := fetch("stat", "f.bin", nil); e[0]["error"] != capability.ReasonSessionNotOpen {
		t.Fatalf("after the session left open: %v", e)
	}
	if _, err := h.db.Exec(`UPDATE work_sessions SET state = 'open' WHERE id = ?`, holderSID); err != nil {
		t.Fatal(err)
	}
	// revoke: the next message fails with revoked
	rtx, _ := h.db.BeginTx(ctx, nil)
	if _, err := h.caps.RevokeTx(ctx, rtx, rec.ID, capability.ReasonUser, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := rtx.Commit(); err != nil {
		t.Fatal(err)
	}
	if e := fetch("stat", "f.bin", nil); e[0]["error"] != capability.CodeRevoked {
		t.Fatalf("after revoke: %v", e)
	}
}

func waitAudit(t *testing.T, h *grantHarness, action string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		evs, err := h.log.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range evs {
			if e.Action == action {
				n++
				if strings.Contains(string(e.Detail), "f.bin") {
					t.Fatalf("audit row leaks a path: %s", e.Detail)
				}
			}
		}
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d %s audit rows, want %d", n, action, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
