package relay

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// conn is one authenticated daemon connection. Writes go through a bounded
// queue and a single writer goroutine so a slow peer cannot stall a sender.
type conn struct {
	ws  *websocket.Conn
	key string
	out chan []byte

	// mu guards draining and cursor. While draining, envelopes for this peer go
	// through the offline queue instead of straight to out, which keeps them
	// behind everything already queued.
	mu       sync.Mutex
	draining bool  // queued envelopes may still be waiting to be sent
	cursor   int64 // highest queue seq already sent on this connection
	ctx      context.Context
}

func newConn(ws *websocket.Conn, key string, queue int) *conn {
	return &conn{ws: ws, key: key, out: make(chan []byte, queue), draining: true}
}

type directResult int

const (
	directSent directResult = iota
	directBusy
	directDraining
)

// direct forwards frame immediately unless queued envelopes are still being
// delivered to this peer (directDraining) or its send buffer is full
// (directBusy). Either way the caller queues the envelope instead.
func (c *conn) direct(frame []byte) directResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.draining {
		return directDraining
	}
	if !c.send(frame) {
		return directBusy
	}
	return directSent
}

// directEphemeral forwards an ephemeral frame immediately, ignoring any queued
// backlog, but only while the send buffer is at most half full. The other half
// stays free for mail and control frames. False means the frame was dropped.
func (c *conn) directEphemeral(frame []byte) bool {
	if len(c.out) > cap(c.out)/2 {
		return false
	}
	return c.send(frame)
}

// send queues a frame without blocking; false means the queue is full.
func (c *conn) send(frame []byte) bool {
	select {
	case c.out <- frame:
		return true
	default:
		return false
	}
}

// kick closes the connection with a policy-violation status.
func (c *conn) kick(reason string) {
	_ = c.ws.Close(websocket.StatusPolicyViolation, reason)
}

func (c *conn) writeLoop(ctx context.Context, stop context.CancelFunc) {
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-c.out:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.ws.Write(wctx, websocket.MessageText, frame)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func control(c envelope.Control) []byte {
	raw, _ := json.Marshal(c) // Control has only string and int fields
	return raw
}

func writeControl(ctx context.Context, ws *websocket.Conn, c envelope.Control) error {
	return ws.Write(ctx, websocket.MessageText, control(c))
}
