package main

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

// R55-F29 test 2 (R55-116, OD-F29-7): a --turn-timeout that is not a positive
// whole number of seconds is a usage error with no IPC call; 90m sends 5400;
// --rounds is sent only when given.
func TestDebateStartDurationsAndRounds(t *testing.T) {
	p := shortHome(t)
	var mu sync.Mutex
	var calls []map[string]json.RawMessage
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"request_submit": func(_ context.Context, params json.RawMessage) (any, error) {
			var m map[string]json.RawMessage
			_ = json.Unmarshal(params, &m)
			mu.Lock()
			calls = append(calls, m)
			mu.Unlock()
			return daemon.RequestSubmitResult{ID: "r-0123456789abcdef0123456789abcdef", Session: "s-0123456789abcdef0123456789abcdef"}, nil
		},
	})
	pos := writeJSONFile(t, t.TempDir(), "pos.json", `{"claim":"x","argument":"y"}`)
	start := func(extra ...string) int {
		args := append([]string{"debate", "@bob", "--topic", "t", "--position-file", pos, "--json"}, extra...)
		var out, errb bytes.Buffer
		return run(args, &out, &errb)
	}
	for _, d := range []string{"500ms", "0d", "-1h", "300.5s", "0s"} {
		if code := start("--turn-timeout", d); code != exitUsage {
			t.Errorf("--turn-timeout %s: code %d, want usage", d, code)
		}
	}
	mu.Lock()
	n := len(calls)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("%d IPC calls for refused durations", n)
	}

	if code := start("--turn-timeout", "90m"); code != exitOK {
		t.Fatalf("--turn-timeout 90m: code %d", code)
	}
	if code := start("--rounds", "3"); code != exitOK {
		t.Fatalf("--rounds 3: code %d", code)
	}
	mu.Lock()
	defer mu.Unlock()
	debateOf := func(i int) map[string]json.RawMessage {
		var d map[string]json.RawMessage
		if err := json.Unmarshal(calls[i]["debate"], &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := debateOf(0)
	if string(d["turn_timeout_s"]) != "5400" {
		t.Errorf("90m sent turn_timeout_s %s, want 5400", d["turn_timeout_s"])
	}
	if _, ok := d["rounds"]; ok {
		t.Errorf("--rounds not given, but rounds %s sent", d["rounds"])
	}
	d = debateOf(1)
	if string(d["rounds"]) != "3" {
		t.Errorf("--rounds 3 sent %s", d["rounds"])
	}
	if _, ok := d["turn_timeout_s"]; ok {
		t.Errorf("--turn-timeout not given, but turn_timeout_s %s sent", d["turn_timeout_s"])
	}
}
