//go:build windows

package ipc

import (
	"context"
	"net"
	"testing"
	"time"
)

// lossyListener plays go-winio v0.6.2 when a close request races a pending
// connect: the listener takes the first request but keeps waiting for a
// client, so that Close and Accept block until a second request arrives.
type lossyListener struct {
	closeCh chan struct{}
	doneCh  chan struct{}
}

func newLossyListener() *lossyListener {
	l := &lossyListener{closeCh: make(chan struct{}), doneCh: make(chan struct{})}
	go func() {
		<-l.closeCh // lost: the aborted connect reported ERROR_NO_DATA
		<-l.closeCh
		close(l.doneCh)
	}()
	return l
}

func (l *lossyListener) Accept() (net.Conn, error) {
	<-l.doneCh
	return nil, net.ErrClosed
}

func (l *lossyListener) Close() error {
	select {
	case l.closeCh <- struct{}{}:
		<-l.doneCh
	case <-l.doneCh:
	}
	return nil
}

func (l *lossyListener) Addr() net.Addr { return nil }

// Serve over a pipe listener that loses the first close request still
// returns after cancel.
func TestServeStopsWhenPipeCloseIsLost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- NewServer().Serve(ctx, pipeListener{newLossyListener()}) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return: the listener lost its close request")
	}
}
