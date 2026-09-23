package daemon_test

// A fake approval.WindowRunner for daemon-level tests: it never spawns a
// real dialog process (Docs/review/23-phase2-tickets.md 2.2d, "All dialog
// runs in tests go through a fake runner").

import (
	"context"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
)

type fakeWindowStart struct {
	id, tag, kind, summary string
	expires                time.Time
}

type fakeWindowRunner struct {
	mu      sync.Mutex
	starts  []fakeWindowStart
	handles map[string]*fakeWindowHandle
	// notReady makes every Start return a handle that never becomes ready.
	notReady bool
}

func newFakeWindowRunner() *fakeWindowRunner {
	return &fakeWindowRunner{handles: map[string]*fakeWindowHandle{}}
}

func (r *fakeWindowRunner) Start(_ context.Context, id, tag, kind, summary string, expires time.Time) (approval.WindowHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, fakeWindowStart{id: id, tag: tag, kind: kind, summary: summary, expires: expires})
	h := &fakeWindowHandle{ready: make(chan struct{}), answerCh: make(chan fakeAnswer, 1)}
	if r.notReady {
		close(h.ready)
	} else {
		h.readyOK = true
		close(h.ready)
	}
	r.handles[id] = h
	return h, nil
}

// answer delivers a decoded reply to the most recently opened window for id.
func (r *fakeWindowRunner) answer(id, kind, code string) {
	r.mu.Lock()
	h := r.handles[id]
	r.mu.Unlock()
	if h == nil {
		return
	}
	h.answerCh <- fakeAnswer{kind: kind, code: code}
}

func (r *fakeWindowRunner) startCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.starts)
}

type fakeAnswer struct{ kind, code string }

type fakeWindowHandle struct {
	ready    chan struct{}
	readyOK  bool
	answerCh chan fakeAnswer
	killed   bool
	mu       sync.Mutex
}

func (h *fakeWindowHandle) Ready(ctx context.Context) bool {
	select {
	case <-h.ready:
		return h.readyOK
	case <-ctx.Done():
		return false
	}
}

func (h *fakeWindowHandle) Answer(ctx context.Context) (kind, code string, err error) {
	select {
	case a := <-h.answerCh:
		return a.kind, a.code, nil
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

func (h *fakeWindowHandle) Kill() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.killed = true
}
