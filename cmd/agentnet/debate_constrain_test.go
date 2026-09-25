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

// `agentnet debate <id> --constrain TEXT` (ticket 3.4) calls debate_constrain
// with the id and text as given, prints the approval id, and maps the
// daemon's errors to exit 1.
func TestDebateConstrainCLI(t *testing.T) {
	p := shortHome(t)
	var mu sync.Mutex
	var got daemon.DebateConstrainParams
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"debate_constrain": func(_ context.Context, raw json.RawMessage) (any, error) {
			var params daemon.DebateConstrainParams
			if err := json.Unmarshal(raw, &params); err != nil {
				return nil, err
			}
			mu.Lock()
			got = params
			mu.Unlock()
			if params.Text == "limit" {
				return nil, &ipc.Error{Code: daemon.CodeConstraintLimit, Message: "the debate already has 10 constraints"}
			}
			return daemon.DebateConstrainResult{Approval: approval.View{ID: "a-0123456789abcdef0123456789abcdef", Kind: approval.KindDebateConstraint, State: approval.StatePending}}, nil
		},
	})
	const sid = "s-0123456789abcdef0123456789abcdef"
	var out, errb bytes.Buffer
	if c := run([]string{"debate", sid, "--constrain", "No new dependency"}, &out, &errb); c != exitOK {
		t.Fatalf("code %d: %s", c, errb.String())
	}
	mu.Lock()
	if got.ID != sid || got.Text != "No new dependency" {
		t.Fatalf("params %+v", got)
	}
	mu.Unlock()
	if !strings.Contains(out.String(), "a-0123456789abcdef0123456789abcdef") || !strings.Contains(out.String(), "approval window") {
		t.Fatalf("human output %q", out.String())
	}
	out.Reset()
	if c := run([]string{"debate", "--constrain", "No new dependency", sid, "--json"}, &out, &errb); c != exitOK {
		t.Fatalf("--json code %d", c)
	}
	var body struct {
		OK       bool          `json:"ok"`
		Approval approval.View `json:"approval"`
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil || !body.OK || body.Approval.Kind != approval.KindDebateConstraint {
		t.Fatalf("json %q (%v)", out.String(), err)
	}
	out.Reset()
	if c := run([]string{"debate", sid, "--constrain", "limit", "--json"}, &out, &errb); c != exitError || !strings.Contains(out.String(), "constraint_limit") {
		t.Fatalf("limit: code %d, out %q", c, out.String())
	}
}

func TestDebateConstrainUsage(t *testing.T) {
	shortHome(t)
	for name, args := range map[string][]string{
		"no id":          {"debate", "--constrain", "x"},
		"no --constrain": {"debate", "s-0123456789abcdef0123456789abcdef"},
		"two ids":        {"debate", "s-1", "s-2", "--constrain", "x"},
	} {
		var out, errb bytes.Buffer
		if c := run(args, &out, &errb); c != exitUsage {
			t.Errorf("%s: code %d", name, c)
		}
	}
	var out, errb bytes.Buffer
	if c := run([]string{"debate", "--help"}, &out, &errb); c != exitOK || !strings.Contains(out.String(), "--constrain") {
		t.Fatalf("--help: code %d, %q", c, out.String())
	}
}
