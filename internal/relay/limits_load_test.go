package relay_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

// Load test (ticket 4.0b acceptance, Docs/protocol/relay-hosted.md §2
// "Memory bound"): the relay stays within a memory bound with 5000 idle
// authenticated connections (the --max-conns default; the 5001st key gets
// relay_full), and maximum-size frames sent to non-reading recipients stay
// within the per-connection 4 MiB and the relay-wide --max-inflight.
//
// It runs only with DORYLINAE_RELAY_LOADTEST=1 (the weekly job): its 10000
// sockets (both ends of every connection live in this process) exhaust the
// ephemeral ports of a Windows or macOS host for a while, which fails the
// socket tests of packages running in parallel. It is also skipped with
// -short, under the race detector (which multiplies memory use), and where
// the process may not open 10000+ descriptors.
const (
	loadConns = 5000
	// loadBytesPerConn bounds the heap and stack growth per idle
	// connection, both ends together (this process is also the 5000
	// clients). Measured 58 KiB on Windows/amd64 (2026-09-28); the
	// bound leaves room for platform differences.
	loadBytesPerConn = 128 << 10
)

func TestLimitMemoryBound5000IdleConns(t *testing.T) {
	if testing.Short() {
		t.Skip("load test (5000 connections): skipped with -short; runs in the weekly job")
	}
	if os.Getenv("DORYLINAE_RELAY_LOADTEST") != "1" {
		t.Skip("load test (5000 connections): set DORYLINAE_RELAY_LOADTEST=1 (weekly job); it exhausts ephemeral ports for parallel tests")
	}
	if raceEnabled {
		t.Skip("load test: the race detector multiplies memory use, so the bound is not measured under -race")
	}
	if n := openFilesLimit(); n < 2*loadConns+1000 {
		t.Skipf("load test: open file limit %d is under %d", n, 2*loadConns+1000)
	}
	e := newLimitEnv(t, relay.Options{ChallengeTTL: 30 * time.Second})

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	type client struct {
		p peer
		c *websocket.Conn
	}
	clients := make([]client, loadConns)
	var wg sync.WaitGroup
	errs := make(chan error, loadConns)
	next := make(chan int)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				// 100 prefixes of 50 keys: under the per-prefix upgrade burst (60),
				// connection cap (64) and key cap (64).
				p := newPeer(t)
				c, err := loadAuth(e, p, fmt.Sprintf("10.0.%d.%d", i/50, i%50+1))
				if err != nil {
					errs <- fmt.Errorf("connection %d: %w", i, err)
					continue // keep taking work, so the feeder never blocks
				}
				clients[i] = client{p, c}
			}
		}()
	}
	for i := range loadConns {
		next <- i
	}
	close(next)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, c := range clients {
			if c.c != nil {
				_ = c.c.CloseNow()
			}
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		st, err := e.s.Stats()
		if err != nil {
			t.Fatal(err)
		}
		if st.Connections == loadConns {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d connections registered, want %d", st.Connections, loadConns)
		}
		time.Sleep(10 * time.Millisecond)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	grew := int64(after.HeapInuse+after.StackInuse) - int64(before.HeapInuse+before.StackInuse) //nolint:gosec // heap sizes are far below 2^63
	t.Logf("%d idle connections: heap+stack grew %d MiB, %d KiB per connection (both ends)", loadConns, grew>>20, grew/loadConns>>10)
	if grew > loadConns*loadBytesPerConn {
		t.Fatalf("heap+stack grew %d bytes for %d idle connections, bound %d", grew, loadConns, loadConns*loadBytesPerConn)
	}

	// --max-conns (5000): the next key is refused.
	extra := newPeer(t)
	c, reply := e.auth(extra, "10.1.0.1")
	expectCode(t, reply, envelope.CodeRelayFull, "")
	expectClose(t, c, websocket.StatusTryAgainLater)

	// Maximum-size frames to 20 recipients that do not read, from 20 other keys.
	big := payload(700_000)
	for i := range 20 {
		s, r := clients[i], clients[loadConns-1-i]
		for j := range 5 {
			frame := []byte(mustJSON(s.p.env(r.p.key, fmt.Sprintf("big-%d-%d", i, j), big)))
			if err := s.c.Write(ctx(t), websocket.MessageText, frame); err != nil {
				t.Fatal(err)
			}
		}
	}
	time.Sleep(time.Second)
	for i := range 20 {
		if n := e.s.Buffered(clients[loadConns-1-i].p.key); n > 4<<20 {
			t.Errorf("recipient %d holds %d bytes, cap 4 MiB", i, n)
		}
	}
	if n := e.s.Inflight(); n > 256<<20 {
		t.Errorf("in-flight %d bytes, --max-inflight 256 MiB", n)
	}
}

// loadAuth is limitEnv.auth for worker goroutines: errors instead of t.Fatal.
func loadAuth(e *limitEnv, p peer, ip string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set(limIPHeader, ip)
	c, resp, err := websocket.Dial(ctx, e.url, &websocket.DialOptions{HTTPHeader: h}) //nolint:bodyclose // closed below when present
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, err
	}
	fail := func(err error) (*websocket.Conn, error) { _ = c.CloseNow(); return nil, err }
	_, raw, err := c.Read(ctx)
	if err != nil {
		return fail(err)
	}
	f, err := envelope.Classify(raw)
	if err != nil || f.Control == nil {
		return fail(errors.New("no challenge"))
	}
	nonce, err := envelope.DecodeNonce(f.Control.Nonce)
	if err != nil {
		return fail(err)
	}
	if err := c.Write(ctx, websocket.MessageText, authFrameV2(e.t, p, nonce, limOrigin)); err != nil {
		return fail(err)
	}
	_, raw, err = c.Read(ctx)
	if err != nil {
		return fail(err)
	}
	if f, err := envelope.Classify(raw); err != nil || f.Control == nil || f.Control.Op != envelope.OpReady {
		return fail(fmt.Errorf("no ready: %s", raw))
	}
	return c, nil
}
