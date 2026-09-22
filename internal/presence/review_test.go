package presence

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// TestReceiverOnlineEdgeAfterSilence checks step 6 against the effective
// state: a peer that crashed without a goodbye (row still "online", last_rx
// older than 2.5 × interval) gives an online edge when it comes back, so the
// outbox is flushed; a heartbeat inside the window does not.
func TestReceiverOnlineEdgeAfterSilence(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.seal(onlineBody("0123456789abcdef", 1), f.clock)
	if accepted, edge, _, err := f.rcv.Handle(ctx, f.env(first)); err != nil || !accepted || !edge {
		t.Fatalf("first: accepted=%v edge=%v err=%v", accepted, edge, err)
	}

	f.clock = f.clock.Add(75 * time.Second) // exactly 2.5 × 30 s: still online
	tick := f.seal(onlineBody("0123456789abcdef", 2), f.clock)
	if accepted, edge, _, err := f.rcv.Handle(ctx, f.env(tick)); err != nil || !accepted || edge {
		t.Fatalf("tick in window: accepted=%v edge=%v err=%v", accepted, edge, err)
	}

	f.clock = f.clock.Add(76 * time.Second) // silent past the window, no goodbye
	back := f.seal(onlineBody("fedcba9876543210", 1), f.clock)
	if accepted, edge, _, err := f.rcv.Handle(ctx, f.env(back)); err != nil || !accepted || !edge {
		t.Fatalf("restart after silence: accepted=%v edge=%v err=%v", accepted, edge, err)
	}
}

// TestReceiverDropLimiterBounded checks that a flood of rejected presence from
// many unpaired keys does not grow the per-peer log limiter without bound.
func TestReceiverDropLimiterBounded(t *testing.T) {
	f := newFixture(t)
	sl := f.seal(onlineBody("0123456789abcdef", 1), f.clock)
	for i := 0; i < 3*maxDropPeers; i++ {
		seed := make([]byte, 32)
		copy(seed, fmt.Sprintf("sybil-%d", i))
		key := envelope.KeyString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
		env := f.env(sl)
		env.From = key
		if accepted, _, reason, err := f.rcv.Handle(context.Background(), env); err != nil || accepted || reason != mail.ReasonUnpaired {
			t.Fatalf("sybil %d: accepted=%v reason=%q err=%v", i, accepted, reason, err)
		}
	}
	if n := len(f.rcv.lastDrop); n > maxDropPeers {
		t.Fatalf("limiter holds %d peers, bound %d", n, maxDropPeers)
	}
	// Once entries age out, the limiter sweeps and logs again.
	f.clock = f.clock.Add(2 * time.Minute)
	env := f.env(sl)
	env.From = "new-peer"
	f.rcv.drop(env.From, "x")
	if _, ok := f.rcv.lastDrop["new-peer"]; !ok || len(f.rcv.lastDrop) != 1 {
		t.Fatalf("after aging: %d entries, new-peer present %v", len(f.rcv.lastDrop), ok)
	}
}

// TestSealedPaddingAllCombinations measures the real sealed plaintext (not a
// re-computation): for every state/agent/human combination, 0-32 epochs with
// values of varying digit count up to maxIntValue, and seq from 1 to
// maxIntValue, the signed plaintext is a fixed size, one single constant
// regardless of flags, goodbye, or the digit count of seq/epoch values
// (Docs/protocol/presence.md §Body; review 17 L1: before this fix, about 1
// heartbeat in 256 crossed a padding boundary and a goodbye was 1 byte
// longer, so size alone could tell them apart).
func TestSealedPaddingAllCombinations(t *testing.T) {
	f := newFixture(t)
	pub := f.recip.mbox.PublicKey().Bytes()
	values := []int64{0, 9, 12345, 1<<53 - 1}
	var fixedSize int
	seen := false
	for n := 0; n <= maxEpochs; n++ {
		epochs := map[string]int64{}
		for i := 0; i < n; i++ {
			epochs[teamID(i)] = values[i%len(values)]
		}
		for _, state := range []string{"online", "offline"} {
			for agent := 0; agent <= 1; agent++ {
				for human := 0; human <= 2; human++ {
					if state == "offline" && (agent != 0 || human != 2) {
						continue // not a valid goodbye
					}
					for _, seq := range []int64{1, 1<<53 - 1} {
						b := Body{State: state, Agent: agent, Human: human, Boot: "0123456789abcdef", Seq: seq, Interval: 300, Epochs: epochs}
						sl, err := Seal(SealInput{Priv: f.sender.priv, To: f.recip.key, MailboxPub: pub, Created: f.clock, Body: b})
						if err != nil {
							t.Fatalf("n=%d %s/%d/%d: %v", n, state, agent, human, err)
						}
						if len(sl.Signed)%padBlock != 0 {
							t.Fatalf("n=%d %s/%d/%d seq=%d: plaintext %d bytes", n, state, agent, human, seq, len(sl.Signed))
						}
						if len(sl.Payload) != mail.MinPayload+len(sl.Signed) {
							t.Fatalf("payload %d for plaintext %d", len(sl.Payload), len(sl.Signed))
						}
						if !seen {
							fixedSize = len(sl.Payload)
							seen = true
						} else if len(sl.Payload) != fixedSize {
							t.Fatalf("n=%d %s/%d/%d seq=%d: payload size %d, want the fixed size %d (every combination must seal to the same size)",
								n, state, agent, human, seq, len(sl.Payload), fixedSize)
						}
						if op, err := f.rcv.Opener.OpenPresence(f.env(sl)); err != nil {
							t.Fatalf("open: %v", err)
						} else if _, err := Parse(op.Msg.Body); err != nil {
							t.Fatalf("parse: %v", err)
						}
					}
				}
			}
		}
	}
}
