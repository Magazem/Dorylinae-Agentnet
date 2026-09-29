//go:build !windows

package ipc

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// shortEndpoint returns a socket path in a fresh short temp dir: Unix socket
// paths are length limited.
func shortEndpoint(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// asOtherUser makes this process expect another uid, so the socket and
// daemon this test creates count as another user's.
func asOtherUser(t *testing.T) {
	t.Helper()
	orig := expectedUID
	expectedUID = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { expectedUID = orig })
}

// Review 55 R55-008, Unix side: the client sends nothing to a socket served by
// another uid, and a daemon does not take another user's socket for its own.
func TestUnixForeignPeerRefused(t *testing.T) {
	ep := shortEndpoint(t)
	ln, err := Listen(ep)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		line, _ := bufio.NewReader(c).ReadString('\n')
		got <- line
		_ = c.Close()
	}()
	asOtherUser(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Call(ctx, ep, "mail_submit", map[string]string{"body": "secret content"}, nil); !errors.Is(err, ErrForeignOwner) {
		t.Fatalf("Call: %v, want ErrForeignOwner", err)
	}
	select {
	case line := <-got:
		if line != "" {
			t.Fatalf("foreign daemon received %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no connection seen")
	}
	if _, err := Listen(ep); !errors.Is(err, ErrForeignOwner) {
		t.Fatalf("Listen: %v, want ErrForeignOwner", err)
	}
}

// R5 lead: two daemons starting at once must not both find the socket stale
// and unlink each other's; the lock lets exactly one win.
func TestUnixConcurrentListenOneWins(t *testing.T) {
	for range 20 {
		ep := shortEndpoint(t)
		var wg sync.WaitGroup
		lns := make([]net.Listener, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				lns[i], errs[i] = Listen(ep)
			}()
		}
		wg.Wait()
		won := 0
		for i := range 2 {
			if errs[i] == nil {
				won++
				defer func() { _ = lns[i].Close() }()
			} else if !errors.Is(errs[i], ErrAlreadyRunning) {
				t.Fatalf("Listen: %v", errs[i])
			}
		}
		if won != 1 {
			t.Fatalf("%d listeners won, want 1", won)
		}
		if _, err := os.Stat(ep); err != nil {
			t.Fatalf("winner's socket is gone: %v", err)
		}
	}
}

func TestUnixStaleSocketReplaced(t *testing.T) {
	ep := shortEndpoint(t)
	old, err := net.Listen("unix", ep)
	if err != nil {
		t.Fatal(err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = old.Close()
	ln, err := Listen(ep)
	if err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	_ = ln.Close()
	// Closing releases the lock: the next daemon starts.
	ln, err = Listen(ep)
	if err != nil {
		t.Fatalf("Listen after Close: %v", err)
	}
	_ = ln.Close()
}
