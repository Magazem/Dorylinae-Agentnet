package relayclient

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// badFrameWindow is how often at most the client logs the frames from the
// relay it could not parse (review 55 C16-02: one line per window, not one
// per frame).
var badFrameWindow = time.Minute

// badFrames counts frames the client could not parse and logs them as one
// Warn line per badFrameWindow, or when the connection ends.
type badFrames struct {
	log   *slog.Logger
	mu    sync.Mutex
	count int // frames not parsed since the last line
	acked int // of which acked
	timer *time.Timer
}

// add counts one frame, acked or not, and schedules the line.
func (b *badFrames) add(acked bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.count++
	if acked {
		b.acked++
	}
	if b.timer == nil {
		b.timer = time.AfterFunc(badFrameWindow, b.flush)
	}
}

// flush logs the frames counted so far, if any.
func (b *badFrames) flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	if b.count == 0 {
		return
	}
	b.log.Warn("ignored frames from the relay that could not be parsed", "event", "relay_bad_frame", "count", b.count, "acked", b.acked)
	b.count, b.acked = 0, 0
}

// badEnvelope handles a frame that is not a control frame and fails
// envelope.Parse (Docs/protocol/envelope.md "Frames it cannot parse"). It is
// not handed up and does not enter the seen-set. It is acked, so it cannot
// hold a slot in the relay's queue until the TTL, when the relay's own
// header decoding (envelope.AckTarget, never a second parser) validates the
// routing fields and its type is not ephemeral: the relay checked at ingress
// that this from sent it, so the ack can only delete the sender's own row.
func (c *Client) badEnvelope(ctx context.Context, frame []byte) {
	from, id, typ, ok := envelope.AckTarget(frame)
	ok = ok && !envelope.IsEphemeral(typ)
	if ok {
		c.ack(ctx, envelope.Envelope{From: from, ID: id})
	}
	c.bad.add(ok)
}
