package peers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// startTrace reports what failed starts left behind: sessions, audit rows
// and the start tokens left in the bucket.
func startTrace(t *testing.T, m *Manager, log *audit.Log) (sessions, rows int, tokens float64) {
	t.Helper()
	evs, err := log.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions), len(evs), m.startTokens
}

// Review 94, M1: a start that would fail before any relay reply (the relay
// down, a code already used from this daemon) is refused before it has a
// session, a start token or an audit row. A local agent looping such starts
// therefore cannot flood the audit log or m.sessions, and cannot drain the
// bucket for honest pairings.
func TestPairingStartRefusedBeforeSession(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	down := &failSender{err: relayclient.ErrNotConnected, down: true}
	m, snd, log, st := newRevManager(t, func(c *Config) {
		c.Now = func() time.Time { return now }
		c.Sender = down
	})
	ctx := context.Background()
	for i := 0; i < 3*startBurst; i++ {
		if _, err := m.Start(ctx); !errors.Is(err, relayclient.ErrNotConnected) {
			t.Fatalf("issuer start %d with the relay down: %v, want ErrNotConnected", i, err)
		}
		if _, err := m.Redeem(ctx, newRedeemCode(t), false); !errors.Is(err, relayclient.ErrNotConnected) {
			t.Fatalf("redeemer start %d with the relay down: %v, want ErrNotConnected", i, err)
		}
		if _, err := m.beginRedeemerV1(ctx, "abcdefghjk", nil); !errors.Is(err, relayclient.ErrNotConnected) {
			t.Fatalf("v1 start %d with the relay down: %v, want ErrNotConnected", i, err)
		}
	}
	if n, rows, tokens := startTrace(t, m, log); n != 0 || rows != 0 || tokens != startBurst {
		t.Fatalf("relay down: %d sessions, %d audit rows, %v tokens; want 0, 0, %d", n, rows, tokens, startBurst)
	}
	used := newRedeemCode(t)
	if fresh, err := st.MarkCodeUsed(ctx, usedCodeHash(used), now); err != nil || !fresh {
		t.Fatalf("MarkCodeUsed: %v, %v", fresh, err)
	}
	m.SetSender(snd)
	for i := 0; i < 3*startBurst; i++ {
		got, err := m.Redeem(ctx, used, false)
		if err != nil {
			t.Fatalf("used code start %d: %v", i, err)
		}
		if got.ID == "" || got.Role != RoleRedeemer || got.State != StateFailed || got.Error == nil || got.Error.Code != FailCodeUsed {
			t.Fatalf("used code start %d: %+v, want a failed status with code_used", i, got)
		}
		if _, ok := m.Get(got.ID); ok {
			t.Fatalf("used code start %d registered a session", i)
		}
	}
	if n, rows, tokens := startTrace(t, m, log); n != 0 || rows != 0 || tokens != startBurst {
		t.Fatalf("used code: %d sessions, %d audit rows, %v tokens; want 0, 0, %d", n, rows, tokens, startBurst)
	}
	snd.mu.Lock()
	sent := len(snd.ctl)
	snd.mu.Unlock()
	if got := sent; got != 0 {
		t.Fatalf("%d frames sent for a used code", got)
	}

	// The whole burst is still there for honest pairings.
	for i := 0; i < startBurst; i++ {
		if _, err := m.beginIssuer(ctx, nil); err != nil {
			t.Fatalf("honest start %d after the refused ones: %v", i, err)
		}
	}
	if _, err := m.beginIssuer(ctx, nil); !errors.Is(err, ErrTooManyStarts) {
		t.Fatalf("start past the burst: %v, want ErrTooManyStarts", err)
	}
}

// A start whose send fails after precheck passed (the connection dropped in
// between) keeps its token: it has a session and pair.start and pair.fail
// rows, so the bucket is what bounds them (review 94, M1).
func TestPairingStartFailedSendKeepsToken(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	m, _, log, _ := newRevManager(t, func(c *Config) {
		c.Now = func() time.Time { return now }
		c.Sender = &failSender{err: relayclient.ErrNotConnected}
	})
	ctx := context.Background()
	for i := 0; i < startBurst; i++ {
		if _, err := m.beginIssuer(ctx, nil); !errors.Is(err, relayclient.ErrNotConnected) {
			t.Fatalf("start %d: %v, want ErrNotConnected", i, err)
		}
	}
	if _, err := m.beginIssuer(ctx, nil); !errors.Is(err, ErrTooManyStarts) {
		t.Fatalf("start past the burst: %v, want ErrTooManyStarts", err)
	}
	if _, rows, tokens := startTrace(t, m, log); rows != 2*startBurst || tokens >= 1 {
		t.Fatalf("%d audit rows, %v tokens; want %d rows (pair.start and pair.fail each) and an empty bucket", rows, tokens, 2*startBurst)
	}
}
