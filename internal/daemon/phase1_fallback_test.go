package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// R55-061, review 69b F6/F8: stop does not return before a running check
// ends (a blocking check, released only after stop was called, not timing),
// its context is cancelled, and a start after stop runs nothing and does not
// panic. Run with -race in CI.
func TestFallbackRunnerStopWaits(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var ran, sawCancel atomic.Int32
	f := newFallbackRunner(context.Background(), func(ctx context.Context, _ string) {
		ran.Add(1)
		close(entered)
		<-release
		// stop cancels before it waits; bounded so a missing cancel fails
		// instead of hanging.
		select {
		case <-ctx.Done():
			sawCancel.Add(1)
		case <-time.After(10 * time.Second):
		}
	})
	f.start("peer-1")
	<-entered

	var stopped atomic.Bool
	stopDone := make(chan struct{})
	go func() {
		f.stop()
		stopped.Store(true)
		close(stopDone)
	}()
	// stop is now blocked in Wait (or about to be): the check has not ended.
	// Wait until stop has set closed, then check it has not returned.
	for {
		f.mu.Lock()
		closed := f.closed
		f.mu.Unlock()
		if closed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if stopped.Load() {
		t.Fatal("stop returned while a check was still running")
	}
	close(release)
	<-stopDone
	if sawCancel.Load() != 1 {
		t.Fatal("the running check's context was not cancelled by stop")
	}

	f.start("peer-2") // after stop: nothing runs, no panic
	f.stop()          // a second stop is harmless
	if n := ran.Load(); n != 1 {
		t.Fatalf("checks run = %d, want 1 (none after stop)", n)
	}
}
