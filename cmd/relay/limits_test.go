package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

// The flag defaults are the numbers of Docs/protocol/relay-hosted.md §2.
func TestLimitFlagDefaults(t *testing.T) {
	var lf limitFlags
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	lf.register(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	var o relay.Options
	lf.apply(&o)
	want := relay.Options{
		UpgradesPerMinute: 30, UpgradeBurst: 60, MaxConnsPerPrefix: 64, AuthFailuresPerPrefix: 10,
		MaxUnauthConns: 256, MaxConns: 5000, MaxKeysPerPrefix: 64,
		PrefixEnvelopesPerMinute: 600, PrefixBytesPerMinute: 64 << 20,
		KeyEnvelopesPerMinute: 120, KeyEnvelopeBurst: 240, KeyBytesPerMinute: 32 << 20,
		ControlPerMinute: 60, ReconnectsPerMinute: 20,
		ConnBufferBytes: 4 << 20, MaxInflight: 256 << 20, FrameReadTimeout: 30 * time.Second,
		QueuePairMaxEnvelopes: 300, QueuePairMaxBytes: 8 << 20,
		QueueSenderMaxEnvelopes: 2000, QueueSenderMaxBytes: 64 << 20,
		QueueMaxTotal: 4 << 30, QueueMinFreeDisk: 1 << 30,
	}
	if !reflect.DeepEqual(o, want) {
		t.Fatalf("defaults\n got %+v\nwant %+v", o, want)
	}
	if err := lf.validate(false); err != nil {
		t.Fatal(err)
	}
	var help bytes.Buffer
	fs.SetOutput(&help)
	fs.PrintDefaults()
	for _, s := range []string{"-max-conns int", "(default 5000)", "-queue-max-total value", "(default 4GiB)", "-max-inflight value", "(default 256MiB)",
		"-max-inflight-ephemeral value", "(default --max-inflight / 8, at least 1MiB)", "-frame-read-timeout duration", "(default 30s)"} {
		if !strings.Contains(help.String(), s) {
			t.Errorf("help lacks %q", s)
		}
	}
}

// R55-F1 (acceptance test 16): the two new memory flags parse with units,
// and the start line prints the budgets in force.
func TestMemoryBudgetFlags(t *testing.T) {
	var lf limitFlags
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	lf.register(fs)
	if err := fs.Parse([]string{"--max-inflight", "48MiB", "--max-inflight-ephemeral", "6MiB", "--frame-read-timeout", "45s"}); err != nil {
		t.Fatal(err)
	}
	if err := lf.validate(false); err != nil {
		t.Fatal(err)
	}
	var o relay.Options
	lf.apply(&o)
	if o.MaxInflight != 48<<20 || o.MaxInflightEphemeral != 6<<20 || o.FrameReadTimeout != 45*time.Second {
		t.Fatalf("got max-inflight %d, ephemeral %d, frame timeout %v", o.MaxInflight, o.MaxInflightEphemeral, o.FrameReadTimeout)
	}
	for _, args := range [][]string{{"--frame-read-timeout", "0s"}, {"--max-inflight-ephemeral", "lots"}} {
		var out, errb bytes.Buffer
		if code := run(context.Background(), args, &out, &errb); code != 2 {
			t.Errorf("%v: exit %d, want 2 (%s)", args, code, errb.String())
		}
	}

	_, errb := startRelayLog(t, "--max-inflight", "48MiB", "--frame-read-timeout", "30s")
	waitLogLine(t, errb, "memory budgets: max-inflight 48MiB (and as much again for frames being read); max-inflight-ephemeral 6MiB; frame-read-timeout 30s")
	_, errb = startRelayLog(t, "--max-inflight-ephemeral", "2MiB", "--frame-read-timeout", "1m")
	waitLogLine(t, errb, "max-inflight 256MiB (and as much again for frames being read); max-inflight-ephemeral 2MiB; frame-read-timeout 1m0s")
}

// waitLogLine waits for the relay's stderr to contain want.
func waitLogLine(t *testing.T, errb *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(errb.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("start lines lack %q:\n%s", want, errb.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLimitFlagUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		msg  string
	}{
		{[]string{"--behind-proxy", "--public-origin", "wss://r.example"}, "--behind-proxy needs --client-ip-header"},
		{[]string{"--behind-proxy", "--public-origin", "wss://r.example", "--client-ip-header", "Fly-Client-IP"}, "needs at least one --trusted-proxy"},
		{[]string{"--client-ip-header", "Fly-Client-IP", "--trusted-proxy", "10.0.0.0/8"}, "--client-ip-header needs --behind-proxy"},
		{[]string{"--trusted-proxy", "10.0.0.0/8"}, "--trusted-proxy needs --client-ip-header"},
		{[]string{"--trusted-proxy", "10.0.0.0/33"}, "not a CIDR"},
		{[]string{"--trusted-proxy", "proxy.example"}, "not an IP or CIDR"},
		{[]string{"--max-conns", "0"}, "--max-conns must be positive"},
		{[]string{"--key-envelopes-per-min", "-1"}, "--key-envelopes-per-min must be positive"},
		{[]string{"--queue-max-total", "lots"}, "not a byte size"},
		{[]string{"--conn-buffer", "0"}, "--conn-buffer must be positive"},
	} {
		var out, errb bytes.Buffer
		if code := run(context.Background(), tc.args, &out, &errb); code != 2 || !strings.Contains(errb.String(), tc.msg) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errb.String(), tc.msg)
		}
	}
}

func TestByteSizeFlag(t *testing.T) {
	for in, want := range map[string]int64{
		"1048576": 1 << 20, "4MiB": 4 << 20, "4mib": 4 << 20, "4 MiB": 4 << 20, "1GiB": 1 << 30, "4GB": 4 << 30,
		"512K": 512 << 10, "64MB": 64 << 20, "10B": 10,
	} {
		var b byteSize
		if err := b.Set(in); err != nil || int64(b) != want {
			t.Errorf("Set(%q) = %d, %v; want %d", in, b, err, want)
		}
	}
	for _, in := range []string{"", "MiB", "-1MiB", "1.5GiB", "1TiB", "99999999999GiB"} {
		var b byteSize
		if err := b.Set(in); err == nil {
			t.Errorf("Set(%q) accepted", in)
		}
	}
	for v, want := range map[int64]string{4 << 30: "4GiB", 256 << 20: "256MiB", 3 << 10: "3KiB", 1000: "1000"} {
		b := byteSize(v)
		if b.String() != want {
			t.Errorf("String(%d) = %q, want %q", v, b.String(), want)
		}
	}
	if p, err := parseProxy("10.1.2.3"); err != nil || p != netip.MustParsePrefix("10.1.2.3/32") {
		t.Errorf("parseProxy IP = %v, %v", p, err)
	}
	if p, err := parseProxy("10.1.2.3/8"); err != nil || p != netip.MustParsePrefix("10.0.0.0/8") {
		t.Errorf("parseProxy CIDR = %v, %v", p, err)
	}
}

// Behind a proxy: the client IP header is honoured only from a
// --trusted-proxy peer. Here the test dials from 127.0.0.1, which is trusted
// in one relay and not in the other; with one upgrade per prefix, two
// different client IPs pass only where the header is honoured.
func TestBehindProxyTrustsOnlyTrustedPeers(t *testing.T) {
	dial := func(addr, clientIP string) int {
		t.Helper()
		h := http.Header{}
		h.Set("Fly-Client-IP", clientIP)
		c, resp, err := websocket.Dial(context.Background(), "ws://"+addr+envelope.ConnectPath, &websocket.DialOptions{HTTPHeader: h}) //nolint:bodyclose // closed below
		if err != nil {
			if resp == nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			return resp.StatusCode
		}
		_ = c.CloseNow()
		return http.StatusSwitchingProtocols
	}
	base := []string{"--behind-proxy", "--public-origin", "wss://relay.example", "--client-ip-header", "Fly-Client-IP",
		"--max-upgrades-per-min", "1", "--upgrade-burst", "1"}

	spoofed, _ := startRelayLog(t, append(base, "--trusted-proxy", "10.0.0.0/8")...)
	if st := dial(spoofed, "198.51.100.1"); st != http.StatusSwitchingProtocols {
		t.Fatalf("first dial: HTTP %d", st)
	}
	if st := dial(spoofed, "203.0.113.1"); st != http.StatusTooManyRequests {
		t.Fatalf("a spoofed Fly-Client-IP from an untrusted peer was honoured: HTTP %d, want 429", st)
	}

	trusted, _ := startRelayLog(t, append(base, "--trusted-proxy", "127.0.0.1")...)
	for _, ip := range []string{"198.51.100.1", "203.0.113.1"} {
		if st := dial(trusted, ip); st != http.StatusSwitchingProtocols {
			t.Fatalf("trusted proxy, client %s: HTTP %d", ip, st)
		}
	}
	if st := dial(trusted, "203.0.113.2"); st != http.StatusTooManyRequests {
		t.Fatalf("second client of 203.0.113.0/24: HTTP %d, want 429", st)
	}
}
