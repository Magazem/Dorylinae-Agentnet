//go:build testhooks

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// TestBindHook runs only in a testhooks build (go test -tags testhooks): the
// hook stands in for the confirm page, and binds the pending key.
func TestBindHook(t *testing.T) {
	wsURL, _ := startRelay(t, "--accounts", "github", "--testhook-bind", "--db", filepath.Join(testutil.TempDir(t), "relay.db"))
	base := "http://" + strings.TrimSuffix(strings.TrimPrefix(wsURL, "ws://"), envelope.ConnectPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _ := dialAuth(t, wsURL, nil, newPriv(t), 0, "")
	write := func(ctl envelope.Control) {
		raw, _ := json.Marshal(ctl)
		if err := c.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
	}
	write(envelope.Control{Op: envelope.OpBindStart, Device: "ci", OS: "linux"})
	pend := readCtl(ctx, t, c)

	post := func(form url.Values) (int, string) {
		resp, err := http.PostForm(base+testHookBindPath, form) //nolint:noctx // fixed loopback test URL
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if code, _ := post(url.Values{"user_code": {"0000-0000"}, "subject": {"9"}, "display": {"@ci"}}); code != http.StatusNotFound {
		t.Fatalf("wrong code: %d", code)
	}
	code, body := post(url.Values{"user_code": {pend.UserCode}, "subject": {"9"}, "display": {"@ci"}})
	if code != http.StatusOK || !strings.HasPrefix(body, "acc_") {
		t.Fatalf("hook: %d %q", code, body)
	}
	write(envelope.Control{Op: envelope.OpBindStart, Device: "ci", OS: "linux", Ref: "again"})
	if r := readCtl(ctx, t, c); r.Code != envelope.CodeAlreadyBound {
		t.Fatalf("after the hook: %+v, want already_bound", r)
	}
}
