//go:build !windows

package ipc

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Review 60b F8b-01: a daemon from before the instance lock holds no lock
// file but answers on the socket; a new daemon must stop before it opens the
// database. A stale socket file (nothing answering) does not stop it.
func TestLockInstanceSeesPreLockDaemon(t *testing.T) {
	dir := filepath.Dir(shortEndpoint(t))
	ln, err := net.Listen("unix", filepath.Join(dir, socketName))
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
	if _, err := os.Lstat(filepath.Join(dir, socketName)); err != nil {
		t.Fatalf("stale socket file: %v", err)
	}
	l, err := LockInstance(dir)
	if err != nil {
		t.Fatalf("LockInstance beside a stale socket file: %v", err)
	}
	_ = l.Close()
}
