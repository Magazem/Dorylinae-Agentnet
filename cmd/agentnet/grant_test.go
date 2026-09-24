package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

// captured holds the params of the last call a fake handler received; the
// handler runs on the IPC server's goroutine, so access is guarded.
type captured struct {
	mu  sync.Mutex
	raw json.RawMessage
}

func (c *captured) set(p json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.raw = append(json.RawMessage(nil), p...)
}

func (c *captured) into(t *testing.T, v any) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := json.Unmarshal(c.raw, v); err != nil {
		t.Fatalf("captured params %q: %v", c.raw, err)
	}
}

func testGrantView(id, state string) daemon.GrantView {
	return daemon.GrantView{
		ID: id, Direction: "issued", Peer: daemon.GrantPeerRef{Name: "bob", PublicKey: "KEY"}, Session: "s-1",
		Action: "git.read", Resource: daemon.GrantResourceView{Kind: "git", Label: "repo-ab12", Branch: "main", Path: "/tmp/repo"},
		Scope: "internal", Nbf: "2026-01-01T00:00:00Z", Exp: "2026-01-01T02:00:00Z", Sensitive: true, State: state,
	}
}

func TestGrantPendingApprovalAndPolicyIssued(t *testing.T) {
	p := shortHome(t)
	var got captured
	withApproval := true
	var mu sync.Mutex
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"grant_create": func(_ context.Context, params json.RawMessage) (any, error) {
			got.set(params)
			mu.Lock()
			defer mu.Unlock()
			if withApproval {
				return map[string]any{
					"grant":    testGrantView("g-aaa", "pending_approval"),
					"approval": approval.View{ID: "a-123", Kind: "grant", State: "pending", Summary: "approve grant"},
				}, nil
			}
			return map[string]any{"grant": testGrantView("g-aaa", "active")}, nil
		},
	})
	args := []string{"grant", "@bob", "--session", "s-1", "--action", "git.read", "--resource", "/tmp/repo#main", "--scope", "internal", "--expires", "1h", "--public"}

	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitOK {
		t.Fatalf("grant: code %d, stderr %q", code, errb.String())
	}
	for _, want := range []string{"g-aaa", "pending approval a-123", "agentnet approve --open a-123", "bob"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout %q lacks %q", out.String(), want)
		}
	}
	var params daemon.GrantCreateParams
	got.into(t, &params)
	if params != (daemon.GrantCreateParams{Peer: "@bob", Session: "s-1", Action: "git.read", Resource: "/tmp/repo#main", Scope: "internal", Expires: "1h", Public: true}) {
		t.Errorf("params = %+v", params)
	}

	out.Reset()
	if code := run(append(args, "--json"), &out, &errb); code != exitOK {
		t.Fatalf("grant --json: code %d", code)
	}
	var body struct {
		OK       bool `json:"ok"`
		Grant    daemon.GrantView
		Approval struct{ ID, State string }
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil || !body.OK || body.Grant.ID != "g-aaa" || body.Grant.State != "pending_approval" || body.Approval.ID != "a-123" {
		t.Fatalf("json = %q (%v)", out.String(), err)
	}

	mu.Lock()
	withApproval = false
	mu.Unlock()
	out.Reset()
	if code := run(args, &out, &errb); code != exitOK || !strings.Contains(out.String(), "issued") || strings.Contains(out.String(), "approval") {
		t.Fatalf("policy-issued grant: code %d, stdout %q", code, out.String())
	}
	out.Reset()
	run(append(args, "--json"), &out, &errb)
	if strings.Contains(out.String(), `"approval"`) {
		t.Errorf("json without an approval carries one: %q", out.String())
	}
}

func TestGrantErrorsAndUsage(t *testing.T) {
	p := shortHome(t)
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"grant_create": func(context.Context, json.RawMessage) (any, error) {
			return nil, &ipc.Error{Code: "forbidden_resource", Message: "resource may not be the home directory"}
		},
	})
	var out, errb bytes.Buffer
	args := []string{"grant", "@bob", "--session", "s-1", "--action", "fs.read", "--resource", "/home/x"}
	if code := run(args, &out, &errb); code != exitError || !strings.Contains(errb.String(), "home directory") {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
	out.Reset()
	if code := run(append(args, "--json"), &out, &errb); code != exitError || !strings.Contains(out.String(), `"code":"forbidden_resource"`) || !strings.Contains(out.String(), `"ok":false`) {
		t.Fatalf("json: code %d, stdout %q", code, out.String())
	}

	for _, bad := range [][]string{
		{"grant", "@bob"},
		{"grant", "@bob", "--session", "s-1", "--action", "fs.read"},
		{"grant", "--session", "s-1", "--action", "fs.read", "--resource", "/x"},
		{"grant", "@a", "@b", "--session", "s-1", "--action", "fs.read", "--resource", "/x"},
		{"grant", "@bob", "--nope"},
		{"grants", "--issued", "--held"},
		{"grants", "extra"},
		{"revoke"},
		{"revoke", "a", "b"},
		{"grant", "policy", "nope"},
		{"grant", "policy", "add", "@bob"},
		{"grant", "policy", "remove"},
		{"grant", "policy", "list", "extra"},
	} {
		out.Reset()
		errb.Reset()
		if code := run(bad, &out, &errb); code != exitUsage {
			t.Errorf("%v: code %d, want %d", bad, code, exitUsage)
		}
	}
	for _, help := range [][]string{{"grant"}, {"grant", "--help"}, {"grants", "-h"}, {"revoke", "--help"}, {"grant", "policy"}, {"grant", "policy", "add", "--help"}} {
		out.Reset()
		if code := run(help, &out, &errb); code != exitOK || !strings.Contains(out.String(), "Usage:") {
			t.Errorf("%v: code %d, stdout %q", help, code, out.String())
		}
	}
}

func TestGrantCommandsWithoutDaemon(t *testing.T) {
	shortHome(t)
	for _, args := range [][]string{
		{"grant", "@bob", "--session", "s-1", "--action", "fs.read", "--resource", "/x"},
		{"grants"},
		{"revoke", "g-1"},
		{"grant", "policy", "list"},
		{"fetch", "g-1", "f"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitDaemonNotFound {
			t.Errorf("%v: code %d, want %d", args, code, exitDaemonNotFound)
		}
	}
}

func TestGrantsListing(t *testing.T) {
	p := shortHome(t)
	var got captured
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"grant_list": func(_ context.Context, params json.RawMessage) (any, error) {
			got.set(params)
			held := testGrantView("g-held", "active")
			held.Direction, held.Resource.Path, held.Scope = "held", "", ""
			return map[string]any{"grants": []daemon.GrantView{testGrantView("g-aaa", "revoked"), held}}, nil
		},
	})
	var out, errb bytes.Buffer
	if code := run([]string{"grants", "--held", "--session", "s-1"}, &out, &errb); code != exitOK {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
	var params daemon.GrantListParams
	got.into(t, &params)
	if params != (daemon.GrantListParams{Session: "s-1", Direction: "held"}) {
		t.Errorf("params = %+v", params)
	}
	for _, want := range []string{"ID", "DIR", "g-aaa", "g-held", "bob", "repo-ab12#main", "internal", "revoked", "active"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table %q lacks %q", out.String(), want)
		}
	}
	if strings.Contains(out.String(), "/tmp/repo") {
		t.Errorf("the table shows a local path: %q", out.String())
	}
	out.Reset()
	if code := run([]string{"grants", "--issued", "--json"}, &out, &errb); code != exitOK {
		t.Fatal(code)
	}
	got.into(t, &params)
	var body struct {
		OK     bool
		Grants []daemon.GrantView
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil || !body.OK || len(body.Grants) != 2 || params.Direction != "issued" {
		t.Fatalf("json = %q (%v), params %+v", out.String(), err, params)
	}
}

func TestGrantsEmpty(t *testing.T) {
	p := shortHome(t)
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"grant_list": func(context.Context, json.RawMessage) (any, error) { return map[string]any{"grants": nil}, nil },
	})
	var out, errb bytes.Buffer
	if code := run([]string{"grants"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "No grants") {
		t.Fatalf("code %d, stdout %q", code, out.String())
	}
	out.Reset()
	run([]string{"grants", "--json"}, &out, &errb)
	if !strings.Contains(out.String(), `"grants":[]`) {
		t.Fatalf("json = %q, want an empty array", out.String())
	}
}

func TestRevokeOutputAndErrors(t *testing.T) {
	p := shortHome(t)
	var got captured
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"grant_revoke": func(_ context.Context, params json.RawMessage) (any, error) {
			got.set(params)
			var q daemon.GrantRevokeParams
			_ = json.Unmarshal(params, &q)
			switch q.ID {
			case "g-dup":
				return daemon.GrantRevokeResult{Grant: testGrantView("g-dup", "revoked"), Duplicate: true}, nil
			case "g-holder":
				return nil, &ipc.Error{Code: "not_grantor", Message: "only the grantor may revoke a grant"}
			case "g-gone":
				return nil, &ipc.Error{Code: "unknown_grant", Message: "no such grant"}
			}
			return daemon.GrantRevokeResult{Grant: testGrantView(q.ID, "revoked"), MailID: "m-1"}, nil
		},
	})
	var out, errb bytes.Buffer
	if code := run([]string{"revoke", "g-aaa"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "Revoked grant g-aaa") {
		t.Fatalf("code %d, stdout %q", code, out.String())
	}
	var params daemon.GrantRevokeParams
	got.into(t, &params)
	if params.ID != "g-aaa" {
		t.Errorf("params = %+v", params)
	}
	out.Reset()
	if code := run([]string{"revoke", "g-dup"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "already revoked") {
		t.Fatalf("duplicate: code %d, stdout %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"revoke", "g-aaa", "--json"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), `"mail_id":"m-1"`) || !strings.Contains(out.String(), `"ok":true`) {
		t.Fatalf("json: code %d, stdout %q", code, out.String())
	}
	for id, want := range map[string]string{"g-holder": "not_grantor", "g-gone": "unknown_grant"} {
		out.Reset()
		if code := run([]string{"revoke", id, "--json"}, &out, &errb); code != exitError || !strings.Contains(out.String(), want) {
			t.Errorf("revoke %s: code %d, stdout %q", id, code, out.String())
		}
	}
}

func TestGrantPolicyCommands(t *testing.T) {
	p := shortHome(t)
	var added, removed captured
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"grant_policy_add": func(_ context.Context, params json.RawMessage) (any, error) {
			added.set(params)
			return map[string]any{"approval": approval.View{ID: "a-pol", Kind: "grant_policy", State: "pending"}}, nil
		},
		"grant_policy_list": func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"policies": []daemon.GrantPolicyView{{
				ID: "p-1", Peer: daemon.GrantPeerRef{Name: "bob"}, Action: "git.read", Branch: "main", Public: true, MaxExpiresS: 3600, Until: "2026-02-01T00:00:00Z",
			}}}, nil
		},
		"grant_policy_remove": func(_ context.Context, params json.RawMessage) (any, error) {
			removed.set(params)
			return map[string]bool{"ok": true}, nil
		},
	})
	var out, errb bytes.Buffer
	if code := run([]string{"grant", "policy", "add", "@bob", "--action", "git.read", "--resource", "/r#main", "--public", "--max-expires", "1h", "--until", "48h"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "a-pol") {
		t.Fatalf("add: code %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
	var params daemon.GrantPolicyAddParams
	added.into(t, &params)
	if params != (daemon.GrantPolicyAddParams{Peer: "@bob", Action: "git.read", Resource: "/r#main", Public: true, MaxExpires: "1h", Until: "48h"}) {
		t.Errorf("params = %+v", params)
	}
	out.Reset()
	if code := run([]string{"grant", "policy", "list"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "p-1") || !strings.Contains(out.String(), "3600s") {
		t.Fatalf("list: code %d, stdout %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"grant", "policy", "remove", "p-1"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "Removed policy p-1") {
		t.Fatalf("remove: code %d, stdout %q", code, out.String())
	}
	var rp struct{ ID string }
	removed.into(t, &rp)
	if rp.ID != "p-1" {
		t.Errorf("remove params = %+v", rp)
	}
}
