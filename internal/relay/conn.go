package relay

import (
	"context"
	"encoding/json"
	"net"
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

	// The connection's ledger (review 50 M2, R55-F1). bmu guards outBytes,
	// ephBytes, resv, readHeld and dead, and makes each push into out one
	// step with its accounting. Every charge and uncharge of any budget goes
	// through led under bmu and is skipped once dead is set, by release or
	// by eviction, so nothing is uncharged twice. maxBytes caps the frames
	// waiting in out (0: no cap). space is signalled after each write, for a
	// queue drain waiting for room. raw is the socket under ws (nil in some
	// tests), closed outright when an evicted peer does not answer the close.
	bmu      sync.Mutex
	outBytes int64 // envelopes waiting in out (outbound budget)
	ephBytes int64 // presence and control frames waiting in out (ephemeral budget)
	resv     int64 // queue-drain reservation not yet in out (outbound budget)
	readHeld int64 // buffer of the frame being read (read budget)
	dead     bool
	maxBytes int64
	led      *ledger
	space    chan struct{}
	raw      net.Conn

	// Guarded by led.mu: what c holds of each budget, when its current
	// frame started, and the frames waiting in out, oldest first.
	hold      [numKinds]int64
	readStart time.Time
	queue     []pending

	// Account state on a relay with accounts, guarded by Server.mu.
	// acctInit is set once register has recorded the state; acctSeen is the
	// account the connection was last seen bound to ("" while unbound);
	// acctClosing marks a connection being closed for revocation or
	// suspension; unboundListed that it is in Server.unbound.
	acctInit      bool
	acctSeen      string
	acctClosing   bool
	unboundListed bool
}

// newConn returns a connection with no byte cap and private budgets without
// limits; the relay sets maxBytes and led before the connection is used.
func newConn(ws *websocket.Conn, key string, queue int, prefix string) *conn {
	return &conn{ws: ws, key: key, prefix: prefix, out: make(chan []byte, queue), draining: true,
		led: newLedger(0, 0, 0, time.Now), space: make(chan struct{}, 1)}
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

// send queues a presence or control frame without blocking, charged to the
// ephemeral budget. False means the buffer is full, or the budget is spent
// even after one eviction, and the frame is dropped (OD-R55F1-4).
func (c *conn) send(frame []byte) bool {
	return c.push(frame, kindEphemeral, false)
}

// sendReady queues the ready frame, charged to the ephemeral budget without
// a check: one per connection, bounded by --max-conns.
func (c *conn) sendReady(frame []byte) bool {
	return c.push(frame, kindEphemeral, true)
}

// sendCapped queues an envelope without blocking unless the connection's
// waiting bytes would pass maxBytes or the outbound budget is spent even
// after one eviction; false sends the envelope down the queue path. An empty
// buffer always takes one frame, so a cap below the frame size cannot stall
// a peer.
func (c *conn) sendCapped(frame []byte) bool {
	return c.push(frame, kindOutbound, false)
}

// push queues frame, charged to budget k. If only the budget refuses it,
// the holder that pays is evicted once (with no bmu held) and the push
// retried once; nothing waits.
func (c *conn) push(frame []byte, k budgetKind, force bool) bool {
	ok, refused, over := c.tryPush(frame, k, force)
	if refused && c.led.evictFor(c, k, int64(len(frame)), over) {
		ok, refused, over = c.tryPush(frame, k, force)
	}
	if refused && k == kindEphemeral && c.led.hit != nil {
		// Presence and control frames dropped for the ephemeral budget
		// are counted in the log (OD-R55F1-4); mail is logged by route.
		if over {
			c.led.hit(limitEphemeral, "prefix", c.prefix)
		} else {
			c.led.hit(limitEphemeral, "relay", "all")
		}
	}
	return ok
}

// tryPush is one attempt of push. refused is true when only the budget
// stood in the way (over: c's prefix share of it).
func (c *conn) tryPush(frame []byte, k budgetKind, force bool) (ok, refused, over bool) {
	n := int64(len(frame))
	c.bmu.Lock()
	defer c.bmu.Unlock()
	if c.dead || len(c.out) == cap(c.out) {
		return false, false, false
	}
	if held := c.outBytes + c.ephBytes; k == kindOutbound && c.maxBytes > 0 && held > 0 && held+n > c.maxBytes {
		return false, false, false
	}
	if ok, over := c.led.charge(c, k, n, force, true); !ok {
		return false, true, over
	}
	c.out <- frame // cannot block: the length was checked and every push holds bmu
	if k == kindOutbound {
		c.outBytes += n
	} else {
		c.ephBytes += n
	}
	return true, false, false
}

// spaceRecheck is how often a waiting queue drain rechecks the relay-wide
// budget; drainEvictEvery is how often at most one of its rechecks may look
// for a holder to evict (review 63 S-4: the scan runs under the ledger lock).
const (
	spaceRecheck    = 50 * time.Millisecond
	drainEvictEvery = 250 * time.Millisecond
)

// reserve charges n bytes to the outbound budget for a queue batch before it
// is read from the database, so frames loaded but not yet in out are counted
// too (R-4.0 H1). It waits while c's own buffer has no room for n bytes or
// the budget is spent, unless wait is false; each try is a new charge that
// may evict once (R55-F1). False means no reservation was made.
func (c *conn) reserve(ctx context.Context, n int64, wait bool) bool {
	var lastEvict time.Time
	for {
		ok, refused, over, dead := c.tryReserve(n)
		if ok {
			return true
		}
		if dead {
			return false
		}
		if refused && time.Since(lastEvict) >= drainEvictEvery {
			lastEvict = time.Now()
			if c.led.evictFor(c, kindOutbound, n, over) {
				if ok, _, _, _ = c.tryReserve(n); ok {
					return true
				}
			}
		}
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

// tryReserve is one attempt of reserve.
func (c *conn) tryReserve(n int64) (ok, refused, over, dead bool) {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	if c.dead {
		return false, false, false, true
	}
	if held := c.outBytes + c.ephBytes; c.maxBytes > 0 && held > 0 && held+n > c.maxBytes {
		return false, false, false, false
	}
	if ok, over := c.led.charge(c, kindOutbound, n, false, false); !ok {
		return false, true, over, false
	}
	c.resv += n
	return true, false, false, false
}

// unreserve returns up to n bytes of c's drain reservation that the batch
// did not use. After release or eviction it does nothing: those have
// uncharged the reservation already.
func (c *conn) unreserve(n int64) {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	if c.dead {
		return
	}
	if n = min(n, c.resv); n <= 0 {
		return
	}
	c.resv -= n
	c.led.uncharge(c, kindOutbound, n)
}

// sendReserved queues a frame whose bytes reserve already charged to the
// outbound budget, waiting only for a free slot in out. It moves the frame's
// bytes from *reserved (and c's reservation) into c's buffer. False means
// ctx ended or the connection is gone; the caller returns what is left of
// *reserved with unreserve.
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
			c.resv -= n
			*reserved -= n
			c.led.pushed(c, kindOutbound, n)
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

// chargeRead charges n more bytes of the frame being read to the read
// budget. dead means c was evicted (or its frame timed out).
func (c *conn) chargeRead(n int64) (ok, refused, over, dead bool) {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	if c.dead {
		return false, false, false, true
	}
	if ok, over := c.led.charge(c, kindRead, n, false, false); !ok {
		return false, true, over, false
	}
	c.readHeld += n
	return true, false, false, false
}

// startRead records the start of a new frame (the read eviction age rule).
func (c *conn) startRead(at time.Time) {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	c.led.startRead(c, at)
}

// doneRead uncharges the frame read, once it has been routed or dropped.
func (c *conn) doneRead() {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	if c.dead || c.readHeld == 0 {
		return
	}
	c.led.uncharge(c, kindRead, c.readHeld)
	c.readHeld = 0
}

// written uncounts a frame the write loop has finished with.
func (c *conn) written([]byte) {
	c.bmu.Lock()
	if !c.dead {
		if f, ok := c.led.popWritten(c); ok {
			if f.kind == kindOutbound {
				c.outBytes -= f.n
			} else {
				c.ephBytes -= f.n
			}
		}
	}
	c.bmu.Unlock()
	select {
	case c.space <- struct{}{}:
	default:
	}
}

// release uncounts everything c holds when the connection ends and refuses
// further sends, so no budget leaks. ServeHTTP defers it (R55-144); after an
// eviction, which has uncharged c already, it does nothing.
func (c *conn) release() {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	c.dieLocked()
}

// dieLocked marks c dead and uncharges everything it holds in every budget
// at once. The caller holds bmu. It reports false if c was dead already.
func (c *conn) dieLocked() bool {
	if c.dead {
		return false
	}
	c.dead = true
	c.led.releaseAll(c)
	c.outBytes, c.ephBytes, c.resv, c.readHeld = 0, 0, 0, 0
	for {
		select {
		case <-c.out:
		default:
			return true
		}
	}
}

// evict marks c dead and uncharges it (relay-hosted.md "Evict H"), then
// closes it 1013 in its own goroutine, never on the path of the connection
// that caused the eviction. A frame c finishes reading afterwards is
// discarded. It reports false if c was already dead.
func (c *conn) evict(reason string) bool {
	c.bmu.Lock()
	ok := c.dieLocked()
	c.bmu.Unlock()
	if ok {
		go c.closeBounded(websocket.StatusTryAgainLater, reason)
	}
	return ok
}

// gone reports whether c has been released or evicted.
func (c *conn) gone() bool {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	return c.dead
}

// closeBounded closes c with status, waiting at most evictCloseWait for the
// close handshake before closing the socket outright: ws.Close alone waits
// up to 5 s + 5 s for a peer that does not answer.
func (c *conn) closeBounded(status websocket.StatusCode, reason string) {
	if c.ws == nil {
		return
	}
	if c.raw != nil {
		t := time.AfterFunc(evictCloseWait, func() { _ = c.raw.Close() })
		defer t.Stop()
	}
	_ = c.ws.Close(status, reason)
}

// buffered reports the bytes waiting in the outbound buffer.
func (c *conn) buffered() int64 {
	c.bmu.Lock()
	defer c.bmu.Unlock()
	return c.outBytes + c.ephBytes
}

// kick closes the connection with a policy-violation status.
func (c *conn) kick(reason string) {
	_ = c.ws.Close(websocket.StatusPolicyViolation, reason)
}

// flushThenKick gives the frames already in c's buffer (such as the error
// explaining why) up to a second to be written, then kicks c.
func (c *conn) flushThenKick(reason string) {
	c.flushThenClose(websocket.StatusPolicyViolation, reason, time.Second)
}

// flushThenClose is flushThenKick with the close status and wait given.
func (c *conn) flushThenClose(status websocket.StatusCode, reason string, wait time.Duration) {
	deadline := time.Now().Add(wait)
	for c.buffered() > 0 && time.Now().Before(deadline) && c.ctx.Err() == nil { // uncounted only once written
		time.Sleep(5 * time.Millisecond)
	}
	_ = c.ws.Close(status, reason)
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
