package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
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

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
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

// At most maxConns connections are served at once; the next is accepted
// only once one of them ends (review 55, R55-083).
func TestServeBoundsConnections(t *testing.T) {
	ln := newChanListener()
	serveOn(t, pingServer(), ln)
	var clients []net.Conn
	defer func() {
		for _, c := range clients {
			_ = c.Close()
		}
	}()
	offer := func() {
		a, b := net.Pipe()
		clients = append(clients, a)
		go func() {
			select {
			case ln.conns <- b:
			case <-ln.closed:
				_ = b.Close()
			}
		}()
	}
	for i := 0; i < maxConns+3; i++ {
		offer()
	}
	waitFor(t, "maxConns accepted connections", func() bool { return ln.accepted.Load() == maxConns })
	time.Sleep(50 * time.Millisecond) // an unbounded Serve would accept the rest by now
	if n := ln.accepted.Load(); n != maxConns {
		t.Fatalf("%d connections accepted at once, want %d", n, maxConns)
	}
	_ = clients[0].Close()
	waitFor(t, "a waiting connection to be accepted", func() bool { return ln.accepted.Load() == maxConns+1 })
}

// A handler that panics answers "internal error" and the server keeps
// serving (review 55, R55-143).
func TestHandlerPanicIsInternalError(t *testing.T) {
	s := pingServer()
	s.Handle("boom", func(context.Context, json.RawMessage) (any, error) { panic("handler bug") })
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
}
