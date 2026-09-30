package session

// Review 79b (R55-F24 re-review): the init and ping gates do not touch a
// session that was opened while the daemon was still visible. Such a session
// never expires, and its data handlers (fetch.req in production) still answer,
// so the peer can tell "invisible" from "offline" on demand.

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenSessionDoesNotOutliveInvisible(t *testing.T) {
	_, a, b := pair(t)
	// B answers "probe" like FetchServer answers a fetch.req with no grant
	// (from its own worker goroutine: handle holds sendMu).
	b.m.Handle("probe", func(peer string, _ []byte) {
		go func() {
			reply, _ := json.Marshal(map[string]string{"type": "probe.resp"})
			_ = b.m.SendData(context.Background(), peer, reply)
		}()
	})
	var answered atomic.Int32
	a.m.Handle("probe.resp", func(string, []byte) { answered.Add(1) })

	// A and B talk while B is visible: a session opens.
	if st := mustPing(t, a, b); st.State != StateComplete {
		t.Fatalf("baseline ping = %+v", st)
	}
	// B goes invisible: both gates now refuse A.
	b.m.SetInitGate(func(context.Context, string) bool { return false })
	b.m.SetPingGate(func(context.Context, string) bool { return false })

	req, _ := json.Marshal(map[string]string{"type": "probe"})
	if err := a.m.SendData(context.Background(), b.key, req); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if answered.Load() > 0 {
			t.Fatal("invisible B answered A's request on a session opened while it was visible: A can tell B is online")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Review 79b M1b: DropGatedSessions closes the open sessions of peers the
// gate refuses, silently: the peer's next message gets no answer and no error.
func TestDropGatedSessionsIsSilent(t *testing.T) {
	r, a, b := pair(t)
	if st := mustPing(t, a, b); st.State != StateComplete {
		t.Fatalf("baseline ping = %+v", st)
	}
	var open atomic.Bool
	open.Store(true)
	b.m.SetInitGate(func(context.Context, string) bool { return open.Load() })
	b.m.DropGatedSessions(context.Background()) // gate open: nothing is dropped
	if st := mustPing(t, a, b); st.State != StateComplete || st.Handshake {
		t.Fatalf("ping after a no-op drop = %+v, want the same session", st)
	}
	open.Store(false)
	b.m.DropGatedSessions(context.Background())

	r.mu.Lock()
	before := len(r.seen)
	r.mu.Unlock()
	req, _ := json.Marshal(map[string]string{"type": "probe"})
	if err := a.m.SendData(context.Background(), b.key, req); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.seen[before:] {
		if e.From == b.key {
			t.Fatalf("the gated daemon answered with a %s envelope", e.Type)
		}
	}
}

// A gated daemon still receives the answers to what it sent itself (".resp"),
// so its own fetches and pings keep working.
func TestGatedDaemonStillReceivesResponses(t *testing.T) {
	_, a, b := pair(t)
	if st := mustPing(t, a, b); st.State != StateComplete {
		t.Fatalf("baseline ping = %+v", st)
	}
	// b refuses a as a requester, but a's ".resp" to b's own request must pass.
	b.m.SetInitGate(func(context.Context, string) bool { return false })
	var got atomic.Int32
	b.m.Handle("probe.resp", func(string, []byte) { got.Add(1) })
	resp, _ := json.Marshal(map[string]string{"type": "probe.resp"})
	if err := a.m.SendData(context.Background(), b.key, resp); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the response to reach the gated daemon", func() bool { return got.Load() == 1 })
}
