package peers

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Review 55, R55-031 (C06-01): 40 redemptions that failed at once (no relay
// connection) used to leave ~40 Argon2id derivations of 64 MiB each running
// together, well past the 16-pending bound. A redemption whose pair_redeem
// never reached the relay must derive nothing.
func TestFailedRedeemsLeaveNoDerivations(t *testing.T) {
	p := newKDFProbe(t)
	m, _, _, _ := newRevManager(t, func(c *Config) { c.Sender = &failSender{err: relayclient.ErrNotConnected} })
	unlimitedStarts(m) // every redemption reaches the failed send
	before := runtime.NumGoroutine()
	const n = 40
	for i := 0; i < n; i++ {
		code, err := NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Redeem(context.Background(), code, false); !errors.Is(err, relayclient.ErrNotConnected) {
			t.Fatalf("redeem without a relay: %v, want ErrNotConnected", err)
		}
	}
	// Each store query leaves a database/sql and a SQLite context watcher
	// that exits only once scheduled, so a raw count taken at once can be
	// high (91 with GOMAXPROCS=1). Wait for them; derivations would not end.
	extra := runtime.NumGoroutine() - before
	for deadline := time.Now().Add(5 * time.Second); extra > maxPending && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		extra = runtime.NumGoroutine() - before
	}
	if extra > maxPending {
		t.Errorf("%d goroutines still running after %d failed redemptions (derivations must not outlive them)", extra, n)
	}
	if s := p.started.Load(); s != 0 {
		t.Errorf("%d derivations started for %d failed redemptions", s, n)
	}
}
