package notify

import (
	"context"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
)

// ApprovalWindow opens the daemon-owned approval dialog per OS
// (Docs/protocol/approval.md §The approval window). The zero value is ready
// to use. It satisfies approval.WindowRunner.
type ApprovalWindow struct{}

// Start launches the fixed dialog program for id (Docs/protocol/approval.md
// §The approval window, per-platform table). The code is never passed here.
func (ApprovalWindow) Start(ctx context.Context, id, tag, kind, summary string, expires time.Time) (approval.WindowHandle, error) {
	tag, kind, summary = windowText(tag, kind, summary)
	return startDialog(ctx, id, tag, kind, summary, expires)
}

// maxWindowSummary bounds the summary shown in the dialog, in code points.
const maxWindowSummary = 600

// windowText sanitises every value before any platform code sees it: no
// control character (NUL, newline, escape) and a bounded length, whatever the
// caller passed (review 30, L1: a NUL fails exec, a newline or a huge value
// reaches argv on Linux). It appends where the code is
// (Docs/protocol/approval.md §The approval window, review 29 L5).
func windowText(tag, kind, summary string) (string, string, string) {
	tag = Clean(tag, 40)
	kind = Clean(kind, 40)
	summary = Clean(summary, maxWindowSummary) +
		" The code is in the AgentNet notification for " + tag +
		". If notifications are silenced (Do Not Disturb, Focus Assist), open the notification centre."
	return tag, kind, summary
}

// maxAnswerLine is "The daemon reads at most 256 bytes"
// (Docs/protocol/approval.md §The approval window, "Answer format").
const maxAnswerLine = 256

// dialogAnswer is one decoded reply from a dialog process.
type dialogAnswer struct {
	kind, code string
}

// dialogHandle is the common approval.WindowHandle implementation shared by
// every platform's startDialog: a ready signal, a one-shot answer channel
// and a kill function. Per-OS code only needs to produce these three things.
type dialogHandle struct {
	readyOnce sync.Once
	readyCh   chan struct{}
	ready     bool

	answerOnce sync.Once
	answerCh   chan dialogAnswer

	killOnce sync.Once
	killFn   func()
}

func newDialogHandle(kill func()) *dialogHandle {
	return &dialogHandle{
		readyCh:  make(chan struct{}),
		answerCh: make(chan dialogAnswer, 1),
		killFn:   kill,
	}
}

// markReady signals readiness exactly once.
func (h *dialogHandle) markReady() {
	h.readyOnce.Do(func() {
		h.ready = true
		close(h.readyCh)
	})
}

// markNotReady unblocks any Ready waiter with a false result (the ready
// check failed some other way, e.g. the process exited before showing).
func (h *dialogHandle) markNotReady() {
	h.readyOnce.Do(func() { close(h.readyCh) })
}

// deliver sends the dialog's decoded final answer, exactly once.
func (h *dialogHandle) deliver(a dialogAnswer) {
	h.answerOnce.Do(func() { h.answerCh <- a })
}

// Ready implements approval.WindowHandle.
func (h *dialogHandle) Ready(ctx context.Context) bool {
	select {
	case <-h.readyCh:
		return h.ready
	case <-ctx.Done():
		return false
	}
}

// Answer implements approval.WindowHandle.
func (h *dialogHandle) Answer(ctx context.Context) (kind, code string, err error) {
	select {
	case a := <-h.answerCh:
		return a.kind, a.code, nil
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

// Kill implements approval.WindowHandle.
func (h *dialogHandle) Kill() {
	h.killOnce.Do(func() {
		if h.killFn != nil {
			h.killFn()
		}
	})
}
