package session

import (
	"context"
	"sync/atomic"
	"testing"
)

// R55-077: a daemon whose ping gate refuses the peer does not answer, so the
// ping times out; with the gate open it is answered again.
func TestPingGateSilencesPongs(t *testing.T) {
	_, a, b := pair(t)
	if st := mustPing(t, a, b); st.State != StateComplete {
		t.Fatalf("baseline ping = %+v", st)
	}
	var open atomic.Bool
	var asked atomic.Value
	b.m.SetPingGate(func(_ context.Context, peer string) bool {
		asked.Store(peer)
		return open.Load()
	})
	st := mustPing(t, a, b)
	waitFor(t, "the ping to time out", func() bool { g, _ := a.m.Get(st.ID); return g.State == StateFailed })
	if g, _ := a.m.Get(st.ID); g.Error == nil || g.Error.Code != FailTimeout {
		t.Fatalf("status = %+v, want a timeout", g)
	}
	if got, _ := asked.Load().(string); got != a.key {
		t.Fatalf("gate asked about %q, want the pinging peer %q", got, a.key)
	}
	open.Store(true)
	if st := mustPing(t, a, b); st.State != StateComplete {
		t.Fatalf("ping with the gate open = %+v", st)
	}
}

// Review 79 M1: a refused Init gets no Resp, no error and no audit row, so the
// pinger sees what it sees for an offline daemon; with the gate open the
// session opens.
func TestInitGateLooksOffline(t *testing.T) {
	_, a, b := pair(t)
	var open atomic.Bool
	b.m.SetInitGate(func(context.Context, string) bool { return open.Load() })

	st := mustPing(t, a, b)
	waitFor(t, "the ping to time out", func() bool { g, _ := a.m.Get(st.ID); return g.State == StateFailed })
	if g, _ := a.m.Get(st.ID); g.Error == nil || g.Error.Code != FailTimeout {
		t.Fatalf("status = %+v, want a plain timeout", g)
	}
	if n := len(a.events(t, ActionOpen)) + len(b.events(t, ActionOpen)); n != 0 {
		t.Fatalf("%d session.open rows although the handshake was refused", n)
	}
	open.Store(true)
	if st := mustPing(t, a, b); st.State != StateComplete {
		t.Fatalf("ping with the init gate open = %+v", st)
	}
}
