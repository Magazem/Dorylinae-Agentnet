package presence

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// Reject/drop reasons for Handle, Docs/protocol/presence.md §Receiving.
const (
	ReasonBadKind = "bad_kind"
	ReasonStale   = "stale"          // created outside the receive window
	ReasonBadBody = "bad_body"       // strict body parse failed
	ReasonReplay  = "presence_stale" // order/replay rule rejected it
)

// Receiver processes presence envelopes end to end: open, the kind and
// created-window checks, the strict body parse, and the order/replay/store
// rule (Docs/protocol/presence.md §Receiving). It never audits (step 1): a
// rejection is logged at debug level, at most once per peer per minute.
type Receiver struct {
	Opener *mail.Opener // Self, Peers and Keys must be set
	Store  *Store
	Log    *slog.Logger
	Now    func() time.Time // defaults to time.Now

	mu        sync.Mutex
	lastDrop  map[string]time.Time
	lastSweep time.Time
}

func (r *Receiver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Receiver) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// Handle processes one presence envelope. accepted is true only when the
// message passed every check and the store row was updated. edge is true when
// this is an online edge (Docs/protocol/presence.md §Receiving step 6); wiring
// it to Outbox.OnPeerOnline is ticket 1.2c. reason is "" on acceptance, else
// one of the Reason constants (or a mail.RejectError reason for the opener
// steps). err is non-nil only for a real failure (e.g. a store error), never
// for a rejected or dropped message.
func (r *Receiver) Handle(ctx context.Context, env envelope.Envelope) (accepted, edge bool, reason string, err error) {
	op, oerr := r.Opener.OpenPresence(env)
	if oerr != nil {
		reason := mail.ReasonOf(oerr)
		r.drop(env.From, reason)
		return false, false, reason, nil
	}

	if op.Msg.Kind != Kind {
		r.drop(env.From, ReasonBadKind)
		return false, false, ReasonBadKind, nil
	}

	now := r.now()
	if op.Msg.Created.Before(now.Add(-windowBefore)) || op.Msg.Created.After(now.Add(windowAfter)) {
		r.drop(env.From, ReasonStale)
		return false, false, ReasonStale, nil
	}

	body, perr := Parse(op.Msg.Body)
	if perr != nil {
		r.drop(env.From, ReasonBadBody)
		return false, false, ReasonBadBody, nil
	}

	ok, e, serr := r.Store.Accept(ctx, env.From, body, op.Msg.Created, now)
	if serr != nil {
		return false, false, "", fmt.Errorf("presence: %w", serr)
	}
	if !ok {
		r.drop(env.From, ReasonReplay)
		return false, false, ReasonReplay, nil
	}
	return true, e, "", nil
}

// maxDropPeers bounds the per-peer log limiter. Rejected senders are
// attacker-chosen (step 1 rejects any unpaired key), so without a bound a
// Sybil flood would grow the map without limit.
const maxDropPeers = 1024

// drop logs a rejection at debug level, at most once per peer per minute.
func (r *Receiver) drop(peer, reason string) {
	now := r.now()
	r.mu.Lock()
	if r.lastDrop == nil {
		r.lastDrop = map[string]time.Time{}
	}
	if last, seen := r.lastDrop[peer]; seen && now.Sub(last) < time.Minute {
		r.mu.Unlock()
		return
	}
	if len(r.lastDrop) >= maxDropPeers {
		// Sweep at most once a minute, so a flood costs O(1) per frame.
		if now.Sub(r.lastSweep) >= time.Minute {
			r.lastSweep = now
			for k, t := range r.lastDrop {
				if now.Sub(t) >= time.Minute {
					delete(r.lastDrop, k)
				}
			}
		}
		if len(r.lastDrop) >= maxDropPeers {
			r.mu.Unlock() // still full of fresh entries: stay silent until they age out
			return
		}
	}
	r.lastDrop[peer] = now
	r.mu.Unlock()
	r.log().Debug("presence rejected", "event", "presence_reject", "reason", reason)
}
