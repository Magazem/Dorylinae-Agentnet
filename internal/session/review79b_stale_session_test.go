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

func TestReview79bOpenSessionOutlivesInvisible(t *testing.T) {
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
