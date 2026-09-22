package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

// dashKey is a syntactically valid base64url Ed25519 public key (32 bytes)
// that starts with '-': about 1 in 32 real peer keys do, since base64url
// index 62 ('-') is a legal leading 6-bit group. It is not paired with any
// daemon, so commands that take it are expected to fail with a daemon-level
// error (e.g. unknown_peer), not a usage error: reaching that error is proof
// the CLI parsed it as a value, not as a flag.
var dashKey = func() string {
	raw := make([]byte, 32)
	raw[0] = 0xf8 // top 6 bits 111110 = base64url index 62 = '-'
	return base64.RawURLEncoding.EncodeToString(raw)
}()

func TestDashPrefixedKeyIsNotTreatedAsAFlag(t *testing.T) {
	if !strings.HasPrefix(dashKey, "-") {
		t.Fatalf("test setup: dashKey = %q, want a leading '-'", dashKey)
	}
	n := startNode(t, "alice", "")

	// (name, args using the plain form, args using the "--" form)
	cases := []struct {
		name string
		args []string
		dash []string
	}{
		{"peers verify", []string{"peers", "verify", dashKey, "AAAAAAAAAAAAAAAAAAAA", "--json"},
			[]string{"peers", "verify", "--json", "--", dashKey, "AAAAAAAAAAAAAAAAAAAA"}},
		{"peers remove", []string{"peers", "remove", dashKey, "--json"},
			[]string{"peers", "remove", "--json", "--", dashKey}},
		{"ping", []string{"ping", dashKey, "--json"},
			[]string{"ping", "--json", "--", dashKey}},
		{"request", []string{"request", dashKey, "task", "--title", "T", "--brief", "B", "--json"},
			[]string{"request", "--title", "T", "--brief", "B", "--json", "--", dashKey, "task"}},
		{"team remove", []string{"team", "remove", "backend", dashKey, "--json"},
			[]string{"team", "remove", "--json", "--", "backend", dashKey}},
	}
	for _, c := range cases {
		t.Run(c.name+"/plain", func(t *testing.T) { assertNotUsageError(t, n, c.args) })
		t.Run(c.name+"/dashdash", func(t *testing.T) { assertNotUsageError(t, n, c.dash) })
	}

	fromCases := []struct {
		name string
		args []string
	}{
		{"accept --from", []string{"accept", "req-1", "--from", dashKey, "--json"}},
		{"decline --from", []string{"decline", "req-1", "--from", dashKey, "--reason", "r", "--json"}},
		{"defer --from", []string{"defer", "req-1", "--from", dashKey, "--until", "1h", "--json"}},
		{"complete --from", []string{"complete", "req-1", "--from", dashKey, "--json"}},
	}
	for _, c := range fromCases {
		t.Run(c.name, func(t *testing.T) { assertNotUsageError(t, n, c.args) })
	}
}

// assertNotUsageError runs args against n and fails if the CLI rejected the
// dash-prefixed key/value as an unrecognized flag (exit code 2, "flag
// provided but not defined"). Any other outcome, including a daemon-level
// error such as unknown_peer, means the argument was parsed correctly.
func assertNotUsageError(t *testing.T, n *testNode, args []string) {
	t.Helper()
	code, out, errs := cli(t, n, args...)
	if code == exitUsage {
		t.Fatalf("agentnet %s: code %d (usage error), out %q err %q; the dash-prefixed value was mistaken for a flag",
			strings.Join(args, " "), code, out, errs)
	}
	if strings.Contains(errs, "flag provided but not defined") {
		t.Fatalf("agentnet %s: stderr %q; the dash-prefixed value was mistaken for a flag", strings.Join(args, " "), errs)
	}
}
