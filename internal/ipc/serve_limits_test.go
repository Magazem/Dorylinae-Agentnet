package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// chanListener hands out the connections sent on conns. Each Accept first
// returns the errors queued in errs.
type chanListener struct {
	conns    chan net.Conn
	closed   chan struct{}
	once     sync.Once
	mu       sync.Mutex
	errs     []error
	accepted atomic.Int32
}

func newChanListener(errs ...error) *chanListener {
	return &chanListener{conns: make(chan net.Conn), closed: make(chan struct{}), errs: errs}
}

func (l *chanListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if len(l.errs) > 0 {
		err := l.errs[0]
		l.errs = l.errs[1:]
		l.mu.Unlock()
		return nil, err
	}
	l.mu.Unlock()
	select {
	case c := <-l.conns:
		l.accepted.Add(1)
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *chanListener) Addr() net.Addr { return nil }

func serveOn(t *testing.T, s *Server, ln net.Listener) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return after cancel")
		}
	})
}

func roundTrip(t *testing.T, c net.Conn, method string) Response {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte(`{"id":"1","method":"` + method + `"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func pingServer() *Server {
	s := NewServer()
	s.Handle("ping", func(context.Context, json.RawMessage) (any, error) { return "pong", nil })
	return s
}

// A temporary Accept error (too many open files) is retried; it used to end
// Serve and with it the daemon (review 55, R55-083).
func TestServeRetriesAcceptErrors(t *testing.T) {
	ln := newChanListener(syscall.EMFILE, syscall.EMFILE, errors.New("transient"))
	serveOn(t, pingServer(), ln)
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	select {
	case ln.conns <- b:
	case <-time.After(10 * time.Second):
		t.Fatal("Serve stopped accepting after an Accept error")
	}
	if resp := roundTrip(t, a, "ping"); !resp.OK {
		t.Fatalf("response %+v", resp)
	}
}

// testEndpoint is the IPC endpoint of a fresh home directory.
func testEndpoint(t *testing.T) string {
	t.Helper()
	p, err := paths.In(testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	return p.Endpoint
}

// holdSilent opens n connections that never send a request, as a local
// client squatting on the slots would (review 77, M1).
func holdSilent(t *testing.T, ln *chanListener, n int) []net.Conn {
	t.Helper()
	var held []net.Conn
	t.Cleanup(func() {
		for _, c := range held {
			_ = c.Close()
		}
	})
	for i := 0; i < n; i++ {
		a, b := net.Pipe()
		held = append(held, a)
		ln.conns <- b
	}
	return held
}

// offer hands one more client to the server without blocking the test.
func offer(ln *chanListener) net.Conn {
	a, b := net.Pipe()
	go func() {
		select {
		case ln.conns <- b:
		case <-ln.closed:
			_ = b.Close()
		}
	}()
	return a
}

// At most maxConns connections are served at once. A client past the cap
// is answered busy at once, not left hanging (review 55, R55-083; review 77,
// M1).
func TestServeAnswersBusyAtTheCap(t *testing.T) {
	ln := newChanListener()
	serveOn(t, pingServer(), ln)
	holdSilent(t, ln, maxConns)
	a := offer(ln)
	defer func() { _ = a.Close() }()
	start := time.Now()
	resp := roundTrip(t, a, "ping")
	if resp.OK || resp.Error == nil || resp.Error.Code != CodeBusy || resp.ID != "1" {
		t.Fatalf("response %+v, want busy for id 1", resp)
	}
	if d := time.Since(start); d > busyTimeout {
		t.Fatalf("busy answer took %v", d)
	}
}

// A connection that sends no request within firstRequestTimeout is closed,
// so silent connections free their slots long before the idle timeout
// (review 77, M1).
func TestServeClosesSilentConnections(t *testing.T) {
	ln := newChanListener()
	serveOn(t, pingServer(), ln)
	held := holdSilent(t, ln, maxConns)
	_ = held[0].SetReadDeadline(time.Now().Add(firstRequestTimeout + 5*time.Second))
	start := time.Now()
	if _, err := held[0].Read(make([]byte, 1)); err == nil {
		t.Fatal("a silent connection got data")
	}
	if d := time.Since(start); d > firstRequestTimeout+2*time.Second {
		t.Fatalf("silent connection closed after %v, want about %v", d, firstRequestTimeout)
	}
	a := offer(ln)
	defer func() { _ = a.Close() }()
	if resp := roundTrip(t, a, "ping"); !resp.OK {
		t.Fatalf("after the silent connections timed out: %+v", resp)
	}
}

// A client of a real listener (a named pipe on Windows, a Unix socket
// elsewhere) gets the busy answer while every slot is held, instead of a
// dial failure or a hang (review 77, M1).
func TestListenerAnswersBusyAtTheCap(t *testing.T) {
	endpoint := testEndpoint(t)
	ln, err := Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	serveOn(t, pingServer(), ln)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	}
	// Every held connection must be accepted and in a slot before the probe.
	time.Sleep(200 * time.Millisecond)
	cctx, ccancel := context.WithTimeout(context.Background(), busyTimeout+time.Second)
	defer ccancel()
	err = Call(cctx, endpoint, "ping", nil, nil)
	var ie *Error
	if !errors.As(err, &ie) || ie.Code != CodeBusy {
		t.Fatalf("Call while every slot is held: %v, want busy", err)
	}
}

// A handler that panics answers "internal error" and the server keeps
// serving (review 55, R55-143).
func TestHandlerPanicIsInternalError(t *testing.T) {
	s := pingServer()
	s.Handle("boom", func(context.Context, json.RawMessage) (any, error) { panic("handler bug with secret") })
	var logs syncBuffer
	s.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	ln := newChanListener()
	serveOn(t, s, ln)
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	ln.conns <- b
	resp := roundTrip(t, a, "boom")
	if resp.OK || resp.Error == nil || resp.Error.Code != CodeInternal || resp.Error.Message != "internal error" {
		t.Fatalf("response %+v, want an internal error", resp)
	}
	a2, b2 := net.Pipe()
	defer func() { _ = a2.Close() }()
	ln.conns <- b2
	if resp := roundTrip(t, a2, "ping"); !resp.OK {
		t.Fatalf("after a panic: %+v", resp)
	}
	// Logged once, with the method and the stack, never the panic's text.
	out := logs.String()
	if n := strings.Count(out, "event=ipc_handler_panic"); n != 1 {
		t.Fatalf("%d panic log lines, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "method=boom") || !strings.Contains(out, "panic=string") || !strings.Contains(out, "goroutine") {
		t.Errorf("panic log lacks the method, the value's type or the stack:\n%s", out)
	}
	if strings.Contains(out, "secret") {
		t.Errorf("panic log holds the panic's text:\n%s", out)
	}
}

// A handler that panics while holding a lock it unlocks by hand still
// leaves the server answering other methods (review 77, M2; the store
// side is covered in internal/approval).
func TestServeKeepsServingAfterPanics(t *testing.T) {
	s := pingServer()
	s.Handle("boom", func(context.Context, json.RawMessage) (any, error) { panic("x") })
	s.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	ln := newChanListener()
	serveOn(t, s, ln)
	for i := 0; i < maxConns+5; i++ {
		// A closed connection's slot is freed by its server goroutine, which
		// may lag the close; a busy answer just means that release is pending,
		// so retry it within a bound. Any other answer fails at once.
		deadline := time.Now().Add(10 * time.Second)
		for {
			a := offer(ln)
			resp := roundTrip(t, a, "boom")
			_ = a.Close()
			if resp.Error != nil && resp.Error.Code == CodeBusy && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
				continue
			}
			if resp.Error == nil || resp.Error.Code != CodeInternal {
				t.Fatalf("call %d: %+v", i, resp)
			}
			break
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		a := offer(ln)
		resp := roundTrip(t, a, "ping")
		_ = a.Close()
		if !resp.OK && resp.Error != nil && resp.Error.Code == CodeBusy && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
			continue
		}
		if !resp.OK {
			t.Fatalf("after %d panics: %+v", maxConns+5, resp)
		}
		break
	}
}

// A run of failing Accept calls is logged once when it starts, not once per
// retry (review 77, L3).
func TestServeLogsAcceptFailures(t *testing.T) {
	errs := make([]error, 8) // about 1.3 s of backoff
	for i := range errs {
		errs[i] = syscall.EMFILE
	}
	ln := newChanListener(errs...)
	s := pingServer()
	var logs syncBuffer
	s.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	serveOn(t, s, ln)
	a := offer(ln)
	defer func() { _ = a.Close() }()
	_ = a.SetDeadline(time.Now().Add(30 * time.Second))
	if resp := roundTrip(t, a, "ping"); !resp.OK {
		t.Fatalf("response %+v", resp)
	}
	out := logs.String()
	if n := strings.Count(out, "event=ipc_accept_error"); n != 1 {
		t.Fatalf("%d accept error log lines for one run of failures, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "event=ipc_accept_recovered") {
		t.Errorf("no recovery log line:\n%s", out)
	}
}

// syncBuffer is a bytes.Buffer safe for the server's goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
