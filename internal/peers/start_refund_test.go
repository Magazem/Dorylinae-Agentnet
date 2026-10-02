package peers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Review 77b, R3: a start that fails locally, before any relay reply (the
// relay unreachable, a code already used), gives its token back, so a local
// agent's failing starts, or a user retrying while the relay is down, cannot
// drain the bucket for honest pairings.
func TestPairingStartRefundedOnLocalFailure(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	fail := &failSender{err: relayclient.ErrNotConnected}
	m, snd, _, st := newRevManager(t, func(c *Config) {
		c.Now = func() time.Time { return now }
		c.Sender = fail
	})
	ctx := context.Background()
	for i := 0; i < 3*startBurst; i++ {
		if _, err := m.beginIssuer(ctx, nil); !errors.Is(err, relayclient.ErrNotConnected) {
			t.Fatalf("issuer start %d with the relay down: %v, want ErrNotConnected", i, err)
		}
		if _, err := m.beginRedeemer(ctx, newRedeemCode(t), nil); !errors.Is(err, relayclient.ErrNotConnected) {
			t.Fatalf("redeemer start %d with the relay down: %v, want ErrNotConnected", i, err)
		}
		if _, err := m.beginRedeemerV1(ctx, "abcdefghjk", nil); !errors.Is(err, relayclient.ErrNotConnected) {
			t.Fatalf("v1 start %d with the relay down: %v, want ErrNotConnected", i, err)
		}
	}
	used := newRedeemCode(t)
	if fresh, err := st.MarkCodeUsed(ctx, usedCodeHash(used), now); err != nil || !fresh {
		t.Fatalf("MarkCodeUsed: %v, %v", fresh, err)
	}
	m.SetSender(snd)
	for i := 0; i < 3*startBurst; i++ {
		s, err := m.beginRedeemer(ctx, used, nil)
		if err != nil {
			t.Fatalf("used code start %d: %v", i, err)
		}
		if got := m.snapshot(s); got.State != StateFailed || got.Error == nil || got.Error.Code != FailCodeUsed {
			t.Fatalf("used code start %d: %+v, want code_used", i, got)
		}
	}
	// The whole burst is still there for honest pairings.
	for i := 0; i < startBurst; i++ {
		if _, err := m.beginIssuer(ctx, nil); err != nil {
			t.Fatalf("honest start %d after the failed ones: %v", i, err)
		}
	}
	if _, err := m.beginIssuer(ctx, nil); !errors.Is(err, ErrTooManyStarts) {
		t.Fatalf("start past the burst: %v, want ErrTooManyStarts", err)
	}
}
