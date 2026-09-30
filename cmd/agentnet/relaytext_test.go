package main

// R55-F9 tests 5-7 (CLI side): status, doctor, ping and pair re-apply
// displayLine to what the daemon serves, and ping and pair show only
// daemon-owned text for a relay code (OD-R55F9-10). The fake daemons here
// return raw relay text, as an older daemon would.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

const hostileText = "\x1b]8;;https://evil.example/\x07upgrade now\x1b]8;;\x07\x1b[2K\r\nrelay: connected"

func TestStatusLastErrorIsOneLine(t *testing.T) {
	p := shortHome(t)
	lastErr := hostileText + strings.Repeat("x", 300)
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"status": func(context.Context, json.RawMessage) (any, error) {
			return daemon.StatusResult{Version: "0.0.0", Relay: &daemon.RelayStatus{URL: "wss://relay.example", Auth: "v2", LastError: lastErr}}, nil
		},
	})
	var out, errb bytes.Buffer
	if code := run([]string{"status"}, &out, &errb); code != exitOK {
		t.Fatalf("status: code %d, stderr %q", code, errb.String())
	}
	if strings.ContainsAny(out.String(), "\x1b\r\x07") {
		t.Fatalf("status output holds a control character: %q", out.String())
	}
	var relayLine string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "  relay:") {
			relayLine = l
		}
		if strings.HasPrefix(l, "relay: connected") || strings.HasPrefix(l, "xxx") {
			t.Fatalf("the last error broke the line: %q", out.String())
		}
	}
	_, shown, ok := strings.Cut(relayLine, "(last error: ")
	if !ok || len(strings.TrimSuffix(shown, ")")) > maxLastError {
		t.Fatalf("relay line %q: want the last error, at most %d bytes", relayLine, maxLastError)
	}
}

func TestDoctorRelayRowLastErrorIsOneLine(t *testing.T) {
	res := daemon.StatusResult{Relay: &daemon.RelayStatus{URL: "wss://relay.example", LastError: hostileText + strings.Repeat("x", 300)}}
	got := checkRelay("wss://relay.example", true, res, nil)
	if got.State != doctorWarn || strings.ContainsAny(got.Detail, "\x1b\r\n\x07") {
		t.Fatalf("relay row = %+v", got)
	}
	if n := len(strings.TrimPrefix(got.Detail, "not connected: ")); n > maxLastError {
		t.Fatalf("relay row shows %d bytes of error", n)
	}
	raw, _ := json.Marshal(got)
	if bytes.Contains(raw, []byte(`\u001b`)) || bytes.Contains(raw, []byte(`\n`)) {
		t.Fatalf("--json detail holds a control character: %s", raw)
	}
}

// failLine runs the CLI and returns its one stderr line.
func failLine(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitError {
		t.Fatalf("%v: code %d, want %d; stderr %q", args, code, exitError, errb.String())
	}
	line := strings.TrimSuffix(errb.String(), "\n")
	if strings.ContainsAny(line, "\x1b\r\n\x07") {
		t.Fatalf("%v: stderr is not one clean line: %q", args, errb.String())
	}
	return line
}

func TestPingFailureShowsNoRelayText(t *testing.T) {
	p := shortHome(t)
	var mu sync.Mutex // the handler runs on the fake daemon's goroutine
	var code, msg string
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"ping": func(context.Context, json.RawMessage) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			return map[string]any{"ping_id": "ping-1", "peer": map[string]string{"public_key": "k", "name": "bob"}, "state": "failed",
				"error": map[string]string{"code": code, "message": msg}}, nil
		},
	})
	for _, tc := range []struct{ code, want string }{
		{"x\x1b[2K", "(x [2K)"}, // unknown code from an older daemon: sanitised
		{envelope.CodeQueueFull, envelope.ErrorText(envelope.CodeQueueFull)}, // a relay code: the daemon's text only
		{envelope.CodeRelayError, "the relay refused the request"},
	} {
		mu.Lock()
		code, msg = tc.code, hostileText
		mu.Unlock()
		line := failLine(t, "ping", "@bob")
		if !strings.HasPrefix(line, "agentnet: ping to @bob failed: ") || !strings.Contains(line, tc.want) {
			t.Errorf("code %q: stderr %q, want %q", tc.code, line, tc.want)
		}
		if tc.code != "x\x1b[2K" && strings.Contains(line, "evil.example") {
			t.Errorf("code %q: stderr shows relay text: %q", tc.code, line)
		}
	}
}

func TestPairFailureShowsNoRelayText(t *testing.T) {
	p := shortHome(t)
	var mu sync.Mutex // the handler runs on the fake daemon's goroutine
	var code, msg string
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"pair_status": func(context.Context, json.RawMessage) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			return map[string]any{"pairing_id": "pair-1", "role": "redeemer", "state": "failed",
				"error": map[string]string{"code": code, "message": msg}}, nil
		},
	})
	for _, tc := range []struct{ code, msg, want string }{
		{"x\x1b", "\x1b[2K\rPaired with alice", "agentnet: pairing failed: [2K Paired with alice (x)"},
		{envelope.CodePairInvalid, "\x1b[2K\rPaired with alice", "agentnet: pairing failed: " + envelope.ErrorText(envelope.CodePairInvalid) + " (pair_invalid)"},
		{"bad_card", "rejected the peer's Agent Card: \u202ex\ny", "agentnet: pairing failed: rejected the peer's Agent Card: x y (bad_card)"},
	} {
		mu.Lock()
		code, msg = tc.code, tc.msg
		mu.Unlock()
		if line := failLine(t, "pair", "--status", "pair-1"); line != tc.want {
			t.Errorf("code %q: stderr %q, want %q", tc.code, line, tc.want)
		}
	}
}
