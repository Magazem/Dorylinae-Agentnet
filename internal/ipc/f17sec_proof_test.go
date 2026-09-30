package ipc

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

// Review 77 (R55-F17 security) proof: maxConns silent connections lock every
// other client out until the idle timeout; a client that sends one request
// per <30 s per connection holds them indefinitely.
func TestF17SecProofSilentConnsLockOut(t *testing.T) {
	ln := newChanListener()
	serveOn(t, pingServer(), ln)
	var held []net.Conn
	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()
	for i := 0; i < maxConns; i++ {
		a, b := net.Pipe()
		held = append(held, a)
		ln.conns <- b
	}
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	go func() {
		select {
		case ln.conns <- b:
		case <-ln.closed:
			_ = b.Close()
		}
	}()
	_ = a.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := a.Write([]byte(`{"id":"1","method":"ping"}` + "\n")); err == nil {
		t.Fatal("honest client was served while maxConns silent connections were held")
	}
}

// Review 77 proof: a handler that panics between Lock and a non-deferred
// Unlock is recovered, but leaves its mutex locked; every later call of a
// handler needing that mutex hangs and keeps its connection slot.
func TestF17SecProofPanicLeavesLockHeld(t *testing.T) {
	var mu sync.Mutex
	s := pingServer()
	s.Handle("lockpanic", func(context.Context, json.RawMessage) (any, error) {
		mu.Lock()
		var m map[string]int
		m["x"] = 1 // nil-map panic while mu is held
		mu.Unlock()
		return nil, nil
	})
	s.Handle("locked", func(context.Context, json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		return "ok", nil
	})
	ln := newChanListener()
	serveOn(t, s, ln)
	a, b := net.Pipe()
	ln.conns <- b
	if resp := roundTrip(t, a, "lockpanic"); resp.OK || resp.Error == nil || resp.Error.Code != CodeInternal {
		t.Fatalf("response %+v", resp)
	}
	_ = a.Close()
	a2, b2 := net.Pipe()
	ln.conns <- b2
	_ = a2.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := a2.Write([]byte(`{"id":"2","method":"locked"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := a2.Read(buf); err == nil {
		t.Fatal("handler answered; expected it to hang on the mutex left locked by the panic")
	}
	// Unblock the hung handler so Serve can stop at cleanup.
	mu.Unlock()
	_ = a2.Close()
}
