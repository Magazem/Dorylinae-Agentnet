package peers_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Review 55, R55-031 (C06-01): 40 redemptions that failed at once (no relay
// connection) used to leave ~40 Argon2id derivations of 64 MiB each running
// together, well past the 16-pending bound. A redemption whose pair_redeem
// never reached the relay must derive nothing.
func TestFailedRedeemsLeaveNoDerivations(t *testing.T) {
	e := newEnv(t, time.Second)
	e.send.err = relayclient.ErrNotConnected
	before := runtime.NumGoroutine()
	const n = 40
	for i := 0; i < n; i++ {
		code, err := peers.NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.m.Redeem(context.Background(), code, false); err == nil {
			t.Fatal("redeem succeeded without a relay")
		}
	}
	if extra := runtime.NumGoroutine() - before; extra > 16 {
		t.Errorf("%d goroutines still running after %d failed redemptions (derivations must not outlive them)", extra, n)
	}
}
