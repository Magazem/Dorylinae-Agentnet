package notify

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
)

// ApprovalWindow opens the daemon-owned approval dialog per OS
// (Docs/protocol/approval.md §The approval window). The zero value is ready
// to use. It satisfies approval.WindowRunner.
type ApprovalWindow struct{}

// Start launches the fixed dialog program for id (Docs/protocol/approval.md
// §The approval window, per-platform table). The code is never passed here.
func (ApprovalWindow) Start(ctx context.Context, id, tag, kind, summary, note string, expires time.Time) (approval.WindowHandle, error) {
	tag, kind, summary, err := windowText(tag, kind, summary, note)
	if err != nil {
		return nil, err
	}
	return startDialog(ctx, id, tag, kind, summary, expires)
}

// Check implements approval.WindowRunner: whether a window could be shown
// here, without opening one, spawning a process or reading peer data. fix is
// one of a fixed set of plain-words strings (Docs/protocol/approval.md §The
// approval window, "How the check works", R55-125).
func (ApprovalWindow) Check(ctx context.Context) (bool, string) { return checkWindow(ctx) }

// MaxWindowSummary bounds the summary shown in the dialog, in code points
// (Docs/protocol/approval.md §Length). A longer summary is refused when the
// agent asks, never cut (R55-F5).
const MaxWindowSummary = displaytext.MaxSummary

// errWindowText refuses a summary the window would have to cut or could not
// show literally: a fail-closed backstop a correct handler never reaches
// (Docs/protocol/approval.md §The approval window, "The summary is shown as
// built").
var errWindowText = errors.New("notify: approval summary is too long or not display-safe")

// windowText checks and assembles every value before any platform code sees
// it. The summary is shown exactly as built (R55-F5): it is never cleaned,
// collapsed or cut here; one that is not display-safe or longer than
// MaxWindowSummary makes the window fail to open (not ready, so
// approval_unavailable). Tag, kind and the daemon's own note are cleaned (no
// control character, review 30 L1). The fixed sentence says where the code
// is (review 29 L5).
func windowText(tag, kind, summary, note string) (string, string, string, error) {
	if !displaytext.Safe(summary) {
		return "", "", "", errWindowText
	}
	tag = Clean(tag, 40)
	kind = Clean(kind, 40)
	if note = Clean(note, 200); note != "" {
		summary += " " + note
	}
	summary += " The code is in the AgentNet notification for " + tag +
		". If notifications are silenced (Do Not Disturb, Focus Assist), open the notification centre."
	return tag, kind, summary, nil
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

	// blocked is set when the window process reported that policy blocks it
	// (Windows Constrained Language Mode); see BlockedByPolicy.
	blocked atomic.Bool
}

// BlockedByPolicy reports that the window process wrote the Constrained
// Language Mode error while opening, so the daemon can name the cause in
// approval_unavailable (Docs/protocol/approval.md §The approval window,
// R55-125). It only ever carries that one fixed fact, never process output.
func (h *dialogHandle) BlockedByPolicy() bool { return h.blocked.Load() }

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
