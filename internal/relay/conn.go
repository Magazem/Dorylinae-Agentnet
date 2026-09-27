package relay

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// drainCloseTimeout bounds how long drainClose waits for a connection's own
// outbound buffer to empty before closing it anyway.
const drainCloseTimeout = 5 * time.Second

// conn is one authenticated daemon connection. Writes go through a bounded
// queue and a single writer goroutine so a slow peer cannot stall a sender.
type conn struct {
	ws  *websocket.Conn
	key string
	// prefix groups the connecting client's network for the per-prefix
	// pairing (and future abuse) limits: /24 for IPv4, /48 for IPv6.
	prefix string
	out    chan []byte

	// mu guards draining and cursor. While draining, envelopes for this peer go
	// through the offline queue instead of straight to out, which keeps them
	// behind everything already queued.
	mu       sync.Mutex
	draining bool  // queued envelopes may still be waiting to be sent
	cursor   int64 // highest queue seq already sent on this connection
	ctx      context.Context

	// ephMu makes the half-full check and the send of an ephemeral frame one
	// step. Without it, senders racing past the check could fill the buffer
	// beyond half and push the recipient's mail into the queue path.
	ephMu sync.Mutex

	// Outbound byte accounting (review 50 M2). bmu guards outBytes and dead
	// and makes each push into out one step with its accounting. maxBytes caps
	// the envelopes waiting in out (0: no cap); inflight is the relay-wide
	// budget every connection's waiting bytes are charged to. space is
	// signalled after each write, for a queue drain waiting for room.
	bmu      sync.Mutex
	outBytes int64
	dead     bool
	maxBytes int64
	inflight *budget
	space    chan struct{}
}

// newConn returns a connection with no byte cap and a private budget; the
// relay sets maxBytes and inflight before the connection is used.
func newConn(ws *websocket.Conn, key string, queue int, prefix string) *conn {
	return &conn{ws: ws, key: key, prefix: prefix, out: make(chan []byte, queue), draining: true,
		inflight: &budget{}, space: make(chan struct{}, 1)}
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
	if !c.sendCapped(frame) {
		return directBusy
	}
	return directSent
}

// directEphemeral forwards an ephemeral frame immediately, ignoring any queued
// backlog, but only while the send buffer is at most half full. The other half
// stays free for mail and control frames. False means the frame was dropped.
func (c *conn) directEphemeral(frame []byte) bool {
	c.ephMu.Lock()
	defer c.ephMu.Unlock()
	if len(c.out) > cap(c.out)/2 {
		return false
	}
	return c.send(frame)
}

// send queues a frame without blocking; false means the queue is full. It
// is for control frames and ephemeral envelopes, which are small and bounded
// by the frame count: their bytes are counted but never refused.
func (c *conn) send(frame []byte) bool {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	if c.dead {
		return false
	}
	select {
	case c.out <- frame:
		c.charge(int64(len(frame)))
		return true
	default:
		return false
	}
}

// sendCapped queues an envelope without blocking unless the connection's
// waiting bytes would pass maxBytes or the relay-wide in-flight budget is
// spent; false sends the envelope down the queue path. An empty buffer
// always takes one frame, so a cap below the frame size cannot stall a peer.
func (c *conn) sendCapped(frame []byte) bool {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	return !c.dead && c.sendCappedLocked(frame)
}

// charge counts n bytes pushed into out. Callers hold bmu.
func (c *conn) charge(n int64) {
	c.outBytes += n
	c.inflight.add(n)
}

// spaceRecheck is how often a waiting queue drain rechecks the relay-wide budget.
const spaceRecheck = 50 * time.Millisecond

// reserve charges n bytes to the relay-wide budget for a queue batch before
// it is read from the database, so frames loaded but not yet in out are
// counted too (R-4.0 H1). It waits while c's own buffer has no room for n
// bytes or the budget is spent, unless wait is false. False means no
// reservation was made.
func (c *conn) reserve(ctx context.Context, n int64, wait bool) bool {
	for {
		c.bmu.Lock()
		if c.dead {
			c.bmu.Unlock()
			return false
		}
		room := c.maxBytes <= 0 || c.outBytes == 0 || c.outBytes+n <= c.maxBytes
		if room && c.inflight.tryAdd(n) {
			c.bmu.Unlock()
			return true
		}
		c.bmu.Unlock()
		if !wait {
			return false
		}
		select {
		case <-c.space:
		case <-time.After(spaceRecheck):
		case <-ctx.Done():
			return false
		}
	}
}

// sendReserved queues a frame whose bytes reserve already charged to the
// relay-wide budget, waiting only for a free slot in out. It moves the
// frame's bytes from *reserved into c's buffer. False means ctx ended or the
// connection is gone; the caller returns what is left of *reserved.
func (c *conn) sendReserved(ctx context.Context, frame []byte, reserved *int64) bool {
	n := int64(len(frame))
	for {
		c.bmu.Lock()
		if c.dead {
			c.bmu.Unlock()
			return false
		}
		if len(c.out) < cap(c.out) {
			c.out <- frame // cannot block: the length was checked and every push holds bmu
			c.outBytes += n
			*reserved -= n
			c.bmu.Unlock()
			return true
		}
		c.bmu.Unlock()
		select {
		case <-c.space:
		case <-time.After(spaceRecheck):
		case <-ctx.Done():
			return false
		}
	}
}

// sendCappedLocked is sendCapped with bmu held and dead already checked.
func (c *conn) sendCappedLocked(frame []byte) bool {
	n := int64(len(frame))
	if len(c.out) == cap(c.out) {
		return false
	}
	if c.maxBytes > 0 && c.outBytes > 0 && c.outBytes+n > c.maxBytes {
		return false
	}
	if !c.inflight.tryAdd(n) {
		return false
	}
	c.out <- frame // cannot block: the length was checked and every push holds bmu
	c.outBytes += n
	return true
}

// written uncounts a frame the write loop has finished with.
func (c *conn) written(frame []byte) {
	c.bmu.Lock()
	if !c.dead {
		n := int64(len(frame))
		c.outBytes -= n
		c.inflight.add(-n)
	}
	c.bmu.Unlock()
	select {
	case c.space <- struct{}{}:
	default:
	}
}

// release uncounts everything still waiting when the connection ends and
// refuses further sends, so the relay-wide budget does not leak.
func (c *conn) release() {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	if c.dead {
		return
	}
	c.dead = true
	c.inflight.add(-c.outBytes)
	c.outBytes = 0
	for {
		select {
		case <-c.out:
		default:
			return
		}
	}
}

// buffered reports the bytes waiting in the outbound buffer.
func (c *conn) buffered() int64 {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	return c.outBytes
}

// kick closes the connection with a policy-violation status.
func (c *conn) kick(reason string) {
	_ = c.ws.Close(websocket.StatusPolicyViolation, reason)
}

// flushThenKick gives the frames already in c's buffer (such as the error
// explaining why) up to a second to be written, then kicks c.
func (c *conn) flushThenKick(reason string) {
	deadline := time.Now().Add(time.Second)
	for c.buffered() > 0 && time.Now().Before(deadline) && c.ctx.Err() == nil { // uncounted only once written
		time.Sleep(5 * time.Millisecond)
	}
	c.kick(reason)
}

// drainClose waits, up to drainCloseTimeout, for c's outbound buffer to empty
// so a frame already accepted for direct delivery is actually written before
// the connection closes, then closes it with "going away". Used by Close's
// SIGTERM drain, not by kick (a protocol violation should not wait).
func (c *conn) drainClose(reason string) {
	deadline := time.NewTimer(drainCloseTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for len(c.out) > 0 {
		select {
		case <-deadline.C:
			goto closeNow
		case <-c.ctx.Done():
			goto closeNow
		case <-ticker.C:
		}
	}
closeNow:
	_ = c.ws.Close(websocket.StatusGoingAway, reason)
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
			c.written(frame)
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
