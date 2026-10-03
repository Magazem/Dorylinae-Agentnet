//go:build !windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

// Review 60b F8b-01: a daemon from before the instance lock holds no lock
// file but answers on the socket; a new daemon must stop before it opens the
// database. A stale socket file (nothing answering) does not stop it. The old
// daemon listens on the endpoint paths names, so a socket name that drifts
// between paths and LockInstance fails here (review 99 F8c-02).
func TestLockInstanceSeesPreLockDaemon(t *testing.T) {
	dir := filepath.Dir(shortEndpoint(t))
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", p.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	if l, err := LockInstance(dir); !errors.Is(err, ErrAlreadyRunning) {
		if l != nil {
			_ = l.Close()
		}
		_ = ln.Close()
		t.Fatalf("LockInstance beside an old daemon: %v, want ErrAlreadyRunning", err)
	}
	// The old daemon is gone but left its socket file behind.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	if _, err := os.Lstat(p.Endpoint); err != nil {
		t.Fatalf("stale socket file: %v", err)
	}
	l, err := LockInstance(dir)
	if err != nil {
		t.Fatalf("LockInstance beside a stale socket file: %v", err)
	}
	_ = l.Close()
}

// Review 99 F8c-01: a socket that fails the dial other than as missing,
// stale or another user's (an older daemon with a full backlog gives EAGAIN)
// refuses the start, and the refusal releases the instance lock. A real full
// backlog needs thousands of pending connections, so the dial is replaced.
func TestLockInstanceRefusesUnansweredSocket(t *testing.T) {
	dir := filepath.Dir(shortEndpoint(t))
	dialInstance = func(context.Context, string) (net.Conn, error) {
		return nil, fmt.Errorf("ipc dial: %w", syscall.EAGAIN)
	}
	t.Cleanup(func() { dialInstance = Dial })
	l, err := LockInstance(dir)
	if err == nil {
		_ = l.Close()
		t.Fatal("LockInstance beside an unanswered socket succeeded")
	}
	if errors.Is(err, ErrAlreadyRunning) || errors.Is(err, ErrForeignOwner) || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("LockInstance: %v, want did not answer", err)
	}
	dialInstance = Dial
	l, err = LockInstance(dir)
	if err != nil {
		t.Fatalf("instance lock still held after the refusal: %v", err)
	}
	_ = l.Close()
}

// Review 99 F8c-01: a config dir too long for sun_path makes the dial fail
// with EINVAL; no daemon can listen there either, so LockInstance lets the
// start go on and Listen reports the long path.
func TestLockInstanceTooLongForSocket(t *testing.T) {
	dir := filepath.Dir(shortEndpoint(t))
	for i := 0; i < 4; i++ {
		dir = filepath.Join(dir, strings.Repeat("d", 40))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := LockInstance(dir)
	if err != nil {
		t.Fatalf("LockInstance in a dir too long for a socket: %v", err)
	}
	_ = l.Close()
}
