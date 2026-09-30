package daemon

import (
	"context"
	"sync"
)

// fallbackRunner runs the Phase 1 fallback checks
// (worksession.Store.CheckPhase1FallbackForPeer) in the background, tracked
// so that shutdown cancels and waits for them before the store closes
// (R55-061, review 69b F6). start is called from the relay and outbox
// goroutines (outbox.OnFinal) and from start-up: the closed flag, set under
// the same mutex as wg.Add, keeps an Add from racing Wait.
type fallbackRunner struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	check  func(ctx context.Context, peer string)
}

// newFallbackRunner returns a runner whose checks run under a context derived
// from parent (the daemon's lifetime) and cancelled by stop.
func newFallbackRunner(parent context.Context, check func(ctx context.Context, peer string)) *fallbackRunner {
	ctx, cancel := context.WithCancel(parent)
	return &fallbackRunner{ctx: ctx, cancel: cancel, check: check}
}

// start runs one check for peer on its own goroutine. After stop it does
// nothing: the next start-up's rescan covers a trigger lost that way.
func (f *fallbackRunner) start(peer string) {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.wg.Add(1)
	f.mu.Unlock()
	go func() {
		defer f.wg.Done()
		f.check(f.ctx, peer)
	}()
}

// stop refuses new checks, cancels the running ones and waits for them.
func (f *fallbackRunner) stop() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.cancel()
	f.wg.Wait()
}
