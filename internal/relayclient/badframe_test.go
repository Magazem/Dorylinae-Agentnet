package relayclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Frames the daemon cannot parse (R55-F2, R55-010; Docs/protocol/envelope.md
// "Frames it cannot parse"): acceptance tests 12 and 13 of
// Docs/review/66-r55-f2-spec.md.

// badFrameRelay authenticates any client, sends it frames on its first
// connection, closes that connection if closeAfter, and reports every ack.
func badFrameRelay(t *testing.T, frames [][]byte, closeAfter bool) (url string, acks <-chan envelope.Control) {
	t.Helper()
	ch := make(chan envelope.Control, 1024)
	var mu sync.Mutex
	first := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		ctx := r.Context()
		challenge, _ := json.Marshal(envelope.Control{Op: envelope.OpChallenge, Version: 1, Nonce: envelope.EncodeNonce(make([]byte, envelope.NonceSize))})
		_ = ws.Write(ctx, websocket.MessageText, challenge)
		if _, _, err := ws.Read(ctx); err != nil {
			return
		}
		ready, _ := json.Marshal(envelope.Control{Op: envelope.OpReady})
		_ = ws.Write(ctx, websocket.MessageText, ready)
		mu.Lock()
		send := first
		first = false
		mu.Unlock()
		if send {
			for _, f := range frames {
				_ = ws.Write(ctx, websocket.MessageText, f)
			}
		}
		for {
			_, frame, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var c envelope.Control
			if json.Unmarshal(frame, &c) == nil && c.Op == envelope.OpAck {
				ch <- c
				if send && closeAfter && len(ch) == len(frames) {
					_ = ws.Close(websocket.StatusNormalClosure, "done")
					return
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), ch
}

// header is the start of a frame with routing fields from `from` to `to`,
// without the closing brace.
func header(from, to, typ, id string) string {
	return `{"from":"` + from + `","to":"` + to + `","team":"t","type":"` + typ + `","id":"` + id + `","ts":"2026-01-02T03:04:05Z"`
}

// Test 12: a frame with valid routing fields and a bad payload is acked with
// its own (from, id), not handed up and not put in the seen-set; an
// ephemeral one is not acked; one whose from is invalid is not acked.
func TestUnparseableFrameIsAcked(t *testing.T) {
	rPub, priv := newKey(t) // the client's own key: F9 drops envelopes addressed to another key
	sPub, _ := newKey(t)
	from, to := envelope.KeyString(sPub), envelope.KeyString(rPub)
	good, err := envelope.Envelope{From: from, To: to, Team: "t", Type: "ping", ID: "bad-1", TS: "2026-01-02T03:04:05Z", Payload: []byte("x")}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	frames := [][]byte{
		[]byte(header(from, to, "ping", "bad-1") + `,"payload":1}`),
		[]byte(header(from, to, "presence", "p-1") + `,"payload":1}`),
		[]byte(header("not-a-key", to, "ping", "bad-2") + `,"payload":1}`),
		[]byte(header(from, to, "ping", "bad-3") + `,"team":5,"payload":1}`),
		good, // the same (from, id) as the first: handed up, it never entered the seen-set
	}
	url, acks := badFrameRelay(t, frames, false)
	var mu sync.Mutex
	var got []envelope.Envelope
	c, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv), OnEnvelope: func(e envelope.Envelope) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	for i, want := range []string{"bad-1", "bad-1"} { // the bad frame, then the good one
		select {
		case a := <-acks:
			if a.From != from || a.Ref != want {
				t.Fatalf("ack %d = (%s, %s), want (%s, %s)", i, a.From, a.Ref, from, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("ack %d never arrived", i)
		}
	}
	select {
	case a := <-acks:
		t.Fatalf("unexpected ack %+v (ephemeral or invalid routing fields)", a)
	case <-time.After(200 * time.Millisecond):
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].ID != "bad-1" || string(got[0].Payload) != "x" {
		t.Fatalf("handed up %+v, want only the good envelope", got)
	}
}

// Test 12: frames the client cannot parse are logged as one line with a
// count, not one line per frame.
func TestUnparseableFramesLoggedOnce(t *testing.T) {
	_, priv := newKey(t)
	sPub, _ := newKey(t)
	rPub, _ := newKey(t)
	from, to := envelope.KeyString(sPub), envelope.KeyString(rPub)
	var frames [][]byte
	for i := range 100 {
		frames = append(frames, []byte(header(from, to, "ping", "bad-"+string(rune('a'+i%26))+string(rune('a'+i/26)))+`,"payload":1}`))
	}
	url, acks := badFrameRelay(t, frames, true)
	_, logs := runClient(t, relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv)})
	eventually(t, "100 acks", func() bool { return len(acks) == 100 })
	eventually(t, "the log line", func() bool { return strings.Contains(logs.String(), "event=relay_bad_frame") })
	lines := 0
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "event=relay_bad_frame") {
			lines++
			if !strings.Contains(line, "count=100") || !strings.Contains(line, "acked=100") {
				t.Errorf("log line %q, want count=100 acked=100", line)
			}
		}
	}
	if lines != 1 {
		t.Fatalf("%d relay_bad_frame lines, want 1:\n%s", lines, logs.String())
	}
}

// Test 13: for repeated and case-variant from and id keys, the (from, id)
// the client acks is what the relay's ParseHeader decoding yields for the
// same frame, never another sender's.
func TestUnparseableFrameAckMatchesRelayDecoding(t *testing.T) {
	_, priv := newKey(t)
	aPub, _ := newKey(t)
	pPub, _ := newKey(t)
	rPub, _ := newKey(t)
	a, p, to := envelope.KeyString(aPub), envelope.KeyString(pPub), envelope.KeyString(rPub)
	rest := `"to":"` + to + `","team":"t","type":"ping","ts":"2026-01-02T03:04:05Z"`
	corpus := []string{
		`{"from":"` + p + `","from":"` + a + `",` + rest + `,"id":"victim","id":"own-1","payload":1}`,
		`{"from":"` + a + `","From":"` + p + `",` + rest + `,"id":"own-2","payload":1}`,
		`{"FROM":"` + p + `","from":"` + a + `",` + rest + `,"ID":"own-3","payload":null}`,
		`{"fRoM":"` + a + `",` + rest + `,"Id":"victim","iD":"own-4","payload":"QQ==","payload":1}`,
	}
	var frames [][]byte
	want := map[string]string{}
	for _, f := range corpus {
		frames = append(frames, []byte(f))
		h, err := envelope.ParseHeader([]byte(f))
		if err == nil {
			t.Fatalf("%s: the relay's decoding accepts it", f)
		}
		want[h.ID] = h.From
	}
	url, acks := badFrameRelay(t, frames, false)
	runClient(t, relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv)})
	for range corpus {
		select {
		case ack := <-acks:
			if from, ok := want[ack.Ref]; !ok || ack.From != from {
				t.Fatalf("acked (%s, %s), want one of %v", ack.From, ack.Ref, want)
			}
			delete(want, ack.Ref)
		case <-time.After(5 * time.Second):
			t.Fatalf("acks missing for %v", want)
		}
	}
	for id, from := range want {
		t.Errorf("no ack for (%s, %s)", from, id)
	}
}
