package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
)

// autoHuman stands in for the person at the approval window in CLI tests that
// only need a trust change to go through (peers verify and team invite need a
// human, D48): it types the code the notifier showed into the window of a
// peer_verify or team_invite approval, and opens no window for any other
// kind. It is both the approval.Notifier and the approval.WindowRunner.
type autoHuman struct {
	mu    sync.Mutex
	codes map[string]string
}

func (h *autoHuman) Show(_ context.Context, id string, _ time.Time, title, _ string) error {
	const marker = "code "
	i := strings.Index(strings.ToLower(title), marker)
	if i < 0 || len(title) < i+len(marker)+6 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.codes == nil {
		h.codes = map[string]string{}
	}
	h.codes[id] = title[i+len(marker) : i+len(marker)+6]
	return nil
}

func (h *autoHuman) Remove(context.Context, string) {}

func (h *autoHuman) code(id string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.codes[id]
	return c, ok
}

func (h *autoHuman) Check(context.Context) (bool, string) { return true, "" }

func (h *autoHuman) Start(_ context.Context, id, _, kind, _, _ string, _ time.Time) (approval.WindowHandle, error) {
	if kind != approval.KindPeerVerify && kind != approval.KindTeamInvite {
		return nil, errors.New("autoHuman: no window for " + kind)
	}
	return &autoHumanHandle{h: h, id: id}, nil
}

type autoHumanHandle struct {
	h  *autoHuman
	id string
}

func (a *autoHumanHandle) Ready(context.Context) bool { return true }

func (a *autoHumanHandle) Answer(ctx context.Context) (string, string, error) {
	for {
		if c, ok := a.h.code(a.id); ok {
			return "approve", c, nil
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (a *autoHumanHandle) Kill() {}

func init() { inviteApprovalPoll = 20 * time.Millisecond }

// waitTrust waits until the only peer of n has the given trust.
func waitTrust(t *testing.T, n *testNode, trust string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if ps := listPeers(t, n); len(ps) == 1 && ps[0].Trust == trust {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for trust %q: %+v", trust, listPeers(t, n))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// mixedApprover serves peer_verify and team_invite approvals with autoHuman
// (D48: a test only needs them to go through) and every other kind with the
// test's own fake window, so the test still answers those itself. Every code
// is shown to both notifiers.
type mixedApprover struct {
	human    *autoHuman
	win      *approveFakeWindow
	notifier *approveFakeNotifier
}

func (m *mixedApprover) Show(ctx context.Context, id string, exp time.Time, title, body string) error {
	_ = m.human.Show(ctx, id, exp, title, body)
	return m.notifier.Show(ctx, id, exp, title, body)
}

func (m *mixedApprover) Remove(ctx context.Context, id string) { m.notifier.Remove(ctx, id) }

func (m *mixedApprover) Check(context.Context) (bool, string) { return true, "" }

func (m *mixedApprover) Start(ctx context.Context, id, tag, kind, summary, note string, expires time.Time) (approval.WindowHandle, error) {
	if kind == approval.KindPeerVerify || kind == approval.KindTeamInvite {
		return m.human.Start(ctx, id, tag, kind, summary, note, expires)
	}
	return m.win.Start(ctx, id, tag, kind, summary, note, expires)
}
