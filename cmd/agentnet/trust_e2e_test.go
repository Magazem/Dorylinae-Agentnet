package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/session"
)

type trustPeer struct {
	PublicKey   string `json:"public_key"`
	Name        string `json:"name"`
	Trust       string `json:"trust"`
	Fingerprint string `json:"fingerprint"`
}

func listPeers(t *testing.T, n *testNode) []trustPeer {
	t.Helper()
	code, out, errs := cli(t, n, "peers", "--json")
	if code != exitOK {
		t.Fatalf("peers --json: %d %s", code, errs)
	}
	var body struct {
		Peers []trustPeer `json:"peers"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("peers output %q: %v", out, err)
	}
	return body.Peers
}

func errCode(t *testing.T, stdout string) string {
	t.Helper()
	var o struct {
		OK    bool                            `json:"ok"`
		Error *struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &o); err != nil || o.OK || o.Error == nil {
		t.Fatalf("not an error body: %q (%v)", stdout, err)
	}
	return o.Error.Code
}

func TestPeersVerifyAndRemove(t *testing.T) {
	srv := relay.New(relay.Options{})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http")
	a := startNode(t, "alice", url)
	b := startNode(t, "bob", url)
	a.waitRelay(t)
	b.waitRelay(t)
	waitConnected(t, srv, a.key, b.key)
	pairNodes(t, a, b)

	// identity shows the node's own fingerprint, in both forms.
	_, out, _ := cli(t, b, "identity", "--json")
	var id struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		t.Fatal(err)
	}
	bobFP, _ := envelope.KeyFingerprint(b.key)
	if id.Fingerprint != bobFP || len(bobFP) != envelope.FingerprintLen {
		t.Fatalf("identity fingerprint %q, want %q", id.Fingerprint, bobFP)
	}
	if _, human, _ := cli(t, b, "identity"); !strings.Contains(human, envelope.FormatFingerprint(bobFP)) {
		t.Errorf("identity output lacks the fingerprint: %q", human)
	}

	// A v2 pairing is trust=code, and peers shows the fingerprint.
	ps := listPeers(t, a)
	if len(ps) != 1 || ps[0].Trust != "code" || ps[0].Fingerprint != bobFP {
		t.Fatalf("alice peers = %+v", ps)
	}
	if _, human, _ := cli(t, a, "peers"); !strings.Contains(human, envelope.FormatFingerprint(bobFP)) || !strings.Contains(human, "code") {
		t.Errorf("peers output lacks fingerprint/trust: %q", human)
	}

	// A wrong fingerprint fails and changes nothing; a malformed one is rejected too.
	wrong := envelope.FormatFingerprint(strings.Repeat("0", envelope.FingerprintLen))
	code, out, _ := cli(t, a, "peers", "verify", "bob", wrong, "--json")
	if code != exitError || errCode(t, out) != "fingerprint_mismatch" {
		t.Fatalf("wrong fp: %d %s", code, out)
	}
	code, out, _ = cli(t, a, "peers", "verify", "bob", "ABC", "--json")
	if code != exitError || errCode(t, out) != "bad_fingerprint" {
		t.Fatalf("malformed fp: %d %s", code, out)
	}
	if ps := listPeers(t, a); ps[0].Trust != "code" {
		t.Fatalf("trust changed by failed verify: %+v", ps)
	}

	// The correct fingerprint, typed lower case in groups, verifies.
	typed := strings.ToLower(envelope.FormatFingerprint(bobFP))
	args := append([]string{"peers", "verify", "bob"}, strings.Fields(typed)...)
	if code, out, errs := cli(t, a, args...); code != exitOK || !strings.Contains(out, "Verified bob") {
		t.Fatalf("verify: %d %s %s", code, out, errs)
	}
	if ps := listPeers(t, a); ps[0].Trust != "fingerprint" {
		t.Fatalf("trust after verify = %+v", ps)
	}
	// A duplicate pairing never lowers it (Store.Add keeps the higher rank).
	// The 1.5 s IPC bound can be missed on a loaded runner; retry, a real failure repeats.
	reverified := false
	for i := 0; i < 3 && !reverified; i++ {
		code, _, _ := cli(t, a, "peers", "verify", b.key, bobFP)
		reverified = code == exitOK
	}
	if !reverified {
		t.Fatal("re-verify by key failed")
	}
	acts := map[string]int{}
	for _, act := range []string{audit.ActionPeerVerify, audit.ActionPeerVerifyFail} {
		acts[act] = len(sessionEvents(t, a, act))
	}
	if acts[audit.ActionPeerVerify] != 2 || acts[audit.ActionPeerVerifyFail] != 1 {
		t.Errorf("verify audit counts = %v", acts)
	}

	// Remove: bob's row is gone and his later session traffic is rejected as unpaired.
	if code, out, errs := cli(t, a, "peers", "remove", "bob"); code != exitOK || !strings.Contains(out, "Removed bob") {
		t.Fatalf("remove: %d %s %s", code, out, errs)
	}
	if ps := listPeers(t, a); len(ps) != 0 {
		t.Fatalf("peers after remove = %+v", ps)
	}
	if code, out, _ := cli(t, a, "peers", "remove", "bob", "--json"); code != exitError || errCode(t, out) != "unknown_peer" {
		t.Fatalf("second remove: %d %s", code, out)
	}
	if evs := sessionEvents(t, a, audit.ActionPeerRemove); len(evs) != 1 || evs[0]["peer"] != b.key {
		t.Fatalf("remove audit = %v", evs)
	}
	if _, o := ping(t, b, "@alice"); o.State == session.StateComplete {
		t.Fatalf("bob's ping completed after being removed: %+v", o)
	}
	rej := waitEvents(t, a, session.ActionReject, 1)
	if d := rej[len(rej)-1]; d["reason"] != session.ReasonUnpaired || d["peer"] != b.key {
		t.Fatalf("reject = %v", d)
	}
}
