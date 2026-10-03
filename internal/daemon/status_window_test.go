package daemon_test

// R55-125 (T8): status carries approval_window and, only when it is missing,
// approval_window_fix; an approval that cannot open its window fails
// approval_unavailable and names the fix.

import (
	"errors"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

func TestStatusApprovalWindow(t *testing.T) {
	r := newHarnessRelay(t)
	ok, missing := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	okWin := newFakeWindowRunner()
	ok.ApprovalNotify, ok.ApprovalWindow = &fakeApprovalNotifier{}, okWin
	missWin := newFakeWindowRunner()
	missWin.missing, missWin.fix, missWin.notReady = true, "install zenity (or kdialog)", true
	missing.ApprovalNotify, missing.ApprovalWindow = &fakeApprovalNotifier{}, missWin
	ok.start()
	missing.start()

	var res daemon.StatusResult
	ok.call("status", nil, &res)
	if res.ApprovalWindow != "ok" || res.ApprovalWindowFix != "" {
		t.Fatalf("working window: %q %q, want ok with no fix", res.ApprovalWindow, res.ApprovalWindowFix)
	}
	res = daemon.StatusResult{}
	missing.call("status", nil, &res)
	if res.ApprovalWindow != "missing" || res.ApprovalWindowFix != "install zenity (or kdialog)" {
		t.Fatalf("missing window: %q %q", res.ApprovalWindow, res.ApprovalWindowFix)
	}

	// An approval on the missing side fails approval_unavailable and says why.
	waitRelayConnected(t, r, ok.key, missing.key)
	harnessPair(t, ok, missing)
	var pv daemon.PeerVerifyResult
	err := ipcCall(missing, "peers_verify", daemon.PeerVerifyParams{Peer: ok.key, Fingerprint: mustFingerprint(t, ok.key)}, &pv)
	var ie *ipc.Error
	if !errors.As(err, &ie) || ie.Code != "approval_unavailable" || !strings.Contains(ie.Message, "install zenity (or kdialog)") {
		t.Fatalf("peers_verify = %v, want approval_unavailable naming the fix", err)
	}
}
