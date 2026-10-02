package ipc

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

// A local client that keeps maxConns connections open between requests (a
// keep-alive squatter) no longer locks the CLI out: at the cap, the
// connection idle longest gives its slot to the new client (review 77b, R1).
func TestListenerEvictsIdleSquatterForCall(t *testing.T) {
	endpoint := testEndpoint(t)
	ln, err := Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	serveOn(t, pingServer(), ln)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var held []net.Conn
	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()
	for i := 0; i < maxConns; i++ {
		c, err := Dial(ctx, endpoint)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		held = append(held, c)
		if resp := roundTrip(t, c, "ping"); !resp.OK {
			t.Fatalf("squatter %d: %+v", i, resp)
		}
		if i == 0 {
			// The first squatter is idle longest whatever order the server
			// goroutines record the others in.
			time.Sleep(evictMinIdle + 200*time.Millisecond)
		}
	}
	cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ccancel()
	var got string
	if err := Call(cctx, endpoint, "ping", nil, &got); err != nil || got != "pong" {
		t.Fatalf("Call while squatters hold every slot: %q, %v; want pong", got, err)
	}
	// Exactly one squatter was evicted, the first one, idle longest.
	_ = held[0].SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := held[0].Read(make([]byte, 1)); err == nil {
		t.Error("the squatter idle longest was not closed")
	}
	if resp := roundTrip(t, held[maxConns-1], "ping"); !resp.OK {
		t.Errorf("a later squatter lost its connection: %+v", resp)
	}
}

// A connection idle for less than evictMinIdle, or one that has not sent its
// first request, keeps its slot; the new client is answered busy.
// evictMinIdle is lengthened so that a slow runner (race detector, loaded CI)
// cannot age the answered connections past it (review 94, L1).
func TestServeKeepsRecentConnectionsAtTheCap(t *testing.T) {
	old := evictMinIdle
	evictMinIdle = time.Minute
	t.Cleanup(func() { evictMinIdle = old })
	ln := newChanListener()
	serveOn(t, pingServer(), ln)
	held := holdSilent(t, ln, maxConns/2)
	for i := len(held); i < maxConns; i++ {
		a := offer(ln)
		held = append(held, a)
		if resp := roundTrip(t, a, "ping"); !resp.OK {
			t.Fatalf("client %d: %+v", i, resp)
		}
	}
	a := offer(ln)
	defer func() { _ = a.Close() }()
	if resp := roundTrip(t, a, "ping"); resp.Error == nil || resp.Error.Code != CodeBusy {
		t.Fatalf("response %+v, want busy", resp)
	}
}

// Each eviction frees exactly one slot: a second new client takes the next
// idlest connection, and the evicted ones never serve again.
func TestServeEvictsIdlestFirst(t *testing.T) {
	ln := newChanListener()
	serveOn(t, pingServer(), ln)
	var held []net.Conn
	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()
	for i := 0; i < maxConns; i++ {
		a := offer(ln)
		held = append(held, a)
		if resp := roundTrip(t, a, "ping"); !resp.OK {
			t.Fatalf("client %d: %+v", i, resp)
		}
		if i == 1 {
			// Clients 0 and 1 are idle longest whatever order the server
			// goroutines record the others in.
			time.Sleep(evictMinIdle + 100*time.Millisecond)
		}
	}
	for i := 0; i < 2; i++ {
		a := offer(ln)
		held = append(held, a)
		if resp := roundTrip(t, a, "ping"); !resp.OK {
			t.Fatalf("new client %d: %+v", i, resp)
		}
	}
	for i := 0; i < 2; i++ {
		_ = held[i].SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = held[i].Write([]byte(`{"id":"1","method":"ping"}` + "\n"))
		if _, err := held[i].Read(make([]byte, 1)); err == nil {
			t.Errorf("evicted client %d was served", i)
		}
	}
	if resp := roundTrip(t, held[2], "ping"); !resp.OK {
		t.Errorf("third client lost its connection: %+v", resp)
	}
}

// Panics of one method are logged at most once per panicLogEvery, with the
// count of those left out (review 77b, I1).
func TestHandlerPanicLogIsRateLimited(t *testing.T) {
	s := pingServer()
	s.Handle("boom", func(context.Context, json.RawMessage) (any, error) { panic("x") })
	s.Handle("bang", func(context.Context, json.RawMessage) (any, error) { panic("y") })
	var logs syncBuffer
	s.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	ln := newChanListener()
	serveOn(t, s, ln)
	for _, m := range []string{"boom", "boom", "boom", "bang"} {
		a := offer(ln)
		if resp := roundTrip(t, a, m); resp.Error == nil || resp.Error.Code != CodeInternal {
			t.Fatalf("%s: %+v", m, resp)
		}
		_ = a.Close()
	}
	out := logs.String()
	if n := strings.Count(out, "event=ipc_handler_panic"); n != 2 {
		t.Fatalf("%d panic log lines, want one per method:\n%s", n, out)
	}
	if strings.Count(out, "method=boom") != 1 || strings.Count(out, "method=bang") != 1 {
		t.Fatalf("want one line per method:\n%s", out)
	}
	// The next line after the interval reports the two left out.
	later := time.Now().Add(panicLogEvery)
	if n, ok := s.panicLogDue("boom", later); !ok || n != 2 {
		t.Fatalf("after the interval: suppressed %d, logged %v; want 2, true", n, ok)
	}
	if _, ok := s.panicLogDue("boom", later.Add(time.Second)); ok {
		t.Fatal("a second panic within the interval was logged")
	}
}
