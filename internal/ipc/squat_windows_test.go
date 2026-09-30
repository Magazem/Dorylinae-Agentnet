//go:build windows

package ipc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// asOtherUser makes this process expect pipes owned by LocalSystem, so pipes
// this test creates count as another user's: one account cannot create a
// pipe owned by someone else.
func asOtherUser(t *testing.T) {
	t.Helper()
	sys, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	orig := currentUser
	currentUser = func() (*windows.SID, error) { return sys, nil }
	t.Cleanup(func() { currentUser = orig })
}

// Review 55 R55-008 (C16-01): a pipe name squatted by another user makes the
// daemon's Listen fail as "held by another user", not "already running", and
// the client sends it nothing.
func TestPipeSquatRefused(t *testing.T) {
	// A fresh name per run: a previous run's squatter instance may still be
	// closing, which fails this create with access denied.
	ep := fmt.Sprintf(`\\.\pipe\dorylinae-test-squat-%s-%d`, strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
	sq, err := winio.ListenPipe(ep, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;WD)"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sq.Close() }()
	got := make(chan string, 4)
	accepted := make(chan struct{}, 4)
	go func() {
		for {
			c, err := sq.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			line, _ := bufio.NewReader(c).ReadString('\n')
			got <- line
			_, _ = c.Write([]byte(`{"id":"1","ok":true,"result":{"pid":4242}}` + "\n"))
			_ = c.Close()
		}
	}()
	asOtherUser(t)
	// go-winio v0.6.2 drops a client that closes before Accept picks it up
	// (ConnectNamedPipe reports ERROR_NO_DATA and the listener loops), so the
	// squatter could miss the refused connections. Hold each owner check,
	// with the client's end open, until the squatter has accepted it: then
	// the squatter reads everything the client sends. The first currentUser
	// call is Listen's own, before any pipe is opened.
	other, calls := currentUser, 0
	currentUser = func() (*windows.SID, error) {
		if calls++; calls > 1 {
			select {
			case <-accepted:
			case <-time.After(3 * time.Second):
			}
		}
		return other()
	}

	_, err = Listen(ep)
	if !errors.Is(err, ErrForeignOwner) || errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Listen on squatted pipe: %v, want ErrForeignOwner", err)
	}
	if !strings.Contains(err.Error(), "held by another user") {
		t.Fatalf("Listen error does not say who holds the pipe: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var res map[string]any
	err = Call(ctx, ep, "mail_submit", map[string]string{"body": "secret content"}, &res)
	if !errors.Is(err, ErrForeignOwner) {
		t.Fatalf("Call to squatted pipe: %v, want ErrForeignOwner", err)
	}
	if res != nil {
		t.Fatalf("client accepted a forged result: %v", res)
	}
	// Both the Listen probe and Call connected; neither sent a byte.
	for range 2 {
		select {
		case line := <-got:
			if line != "" {
				t.Fatalf("squatter received %q", line)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("squatter saw no connection")
		}
	}
}

// Our own pipe is owned by the current user even from an elevated token.
// Only an elevated run proves it (CI's windows-latest is one): a plain token's
// default owner is the user anyway, so this passes without the explicit O:.
func TestOwnPipeOwnerIsUser(t *testing.T) {
	ep := `\\.\pipe\dorylinae-test-own-` + strings.ReplaceAll(t.Name(), "/", "-")
	ln, err := Listen(ep)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	c, err := Dial(context.Background(), ep)
	if err != nil {
		t.Fatalf("Dial own pipe: %v", err)
	}
	_ = c.Close()
}

// Review 60 F6: a pipe that denies the current user access is refused as
// another user's, before anything is sent.
func TestDialAccessDeniedIsForeign(t *testing.T) {
	ep := `\\.\pipe\dorylinae-test-denied-` + strings.ReplaceAll(t.Name(), "/", "-")
	ln, err := winio.ListenPipe(ep, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;SY)"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if _, err := Dial(context.Background(), ep); !errors.Is(err, ErrForeignOwner) {
		t.Fatalf("Dial: %v, want ErrForeignOwner", err)
	}
}

// Review 60 F2: a daemon from before the instance lock, serving the old pipe
// name, keeps a new daemon from starting on the same home.
func TestLockInstanceSeesLegacyPipe(t *testing.T) {
	dir := t.TempDir()
	ln, err := Listen(legacyEndpoint(dir))
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
		t.Fatalf("LockInstance beside an old daemon: %v, want ErrAlreadyRunning", err)
	}
	_ = ln.Close()
	l, err := LockInstance(dir)
	if err != nil {
		t.Fatalf("LockInstance after the old daemon stopped: %v", err)
	}
	_ = l.Close()
}
