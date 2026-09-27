package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// startRelayLog runs the relay on 127.0.0.1:0 with args and returns its
// address (host:port) and stderr. The relay stops when the test ends.
func startRelayLog(t *testing.T, args ...string) (addr string, errb *syncBuffer) {
	t.Helper()
	errb = &syncBuffer{}
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	args = append([]string{"--listen", "127.0.0.1:0", "--queue-db", filepath.Join(testutil.TempDir(t), "q.db")}, args...)
	go func() { done <- run(ctx, args, &out, errb) }()
	t.Cleanup(func() {
		cancel()
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("exit code %d; stderr %s", code, errb.String())
			}
		case <-time.After(10 * time.Second):
			t.Error("relay did not stop")
		}
	})
	re := regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if m := re.FindStringSubmatch(errb.String()); m != nil && strings.Contains(errb.String(), "public: ") {
			return m[1], errb
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay never reported its address; stderr: %s", errb.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dialAuth dials url, answers the challenge with auth version v (1 or 2,
// signing origin for v2) and returns the connection and the relay's reply.
func dialAuth(t *testing.T, url string, opts *websocket.DialOptions, priv ed25519.PrivateKey, v int, origin string) (*websocket.Conn, envelope.Control) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, url, opts) //nolint:bodyclose // successful WebSocket dials have no body to close
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	ch := readCtl(ctx, t, c)
	nonce, err := envelope.DecodeNonce(ch.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	sign := relayclient.NewKeySigner(priv).Sign
	var a envelope.Control
	if v == 2 {
		a, err = envelope.SignAuthV2(pub, nonce, origin, sign)
	} else {
		a, err = envelope.SignAuth(pub, nonce, sign)
	}
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(a)
	if err := c.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	return c, readCtl(ctx, t, c)
}

func readCtl(ctx context.Context, t *testing.T, c *websocket.Conn) envelope.Control {
	t.Helper()
	_, raw, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var ctl envelope.Control
	if err := json.Unmarshal(raw, &ctl); err != nil {
		t.Fatalf("not a control frame %s: %v", raw, err)
	}
	return ctl
}

func newPriv(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func TestTransportUsageErrors(t *testing.T) {
	cert, key := testCertFiles(t)
	for _, tc := range []struct {
		args []string
		msg  string
	}{
		{[]string{"--listen", "0.0.0.0:0", "--allow-non-loopback"}, "a non-loopback relay needs TLS"},
		{[]string{"--listen", "0.0.0.0:0"}, "not a loopback address"},
		{[]string{"--tls-cert", cert}, "--tls-cert and --tls-key go together"},
		{[]string{"--tls-key", key}, "--tls-cert and --tls-key go together"},
		{[]string{"--tls-cert", cert, "--tls-key", key, "--behind-proxy", "--public-origin", "wss://r.example"}, "only one of"},
		{[]string{"--acme-domain", "r.example", "--behind-proxy", "--public-origin", "wss://r.example"}, "only one of"},
		{[]string{"--behind-proxy"}, "--public-origin is required"},
		{[]string{"--tls-cert", cert, "--tls-key", key}, "--public-origin is required"},
		{[]string{"--acme-domain", "r.example"}, "--public-origin is required"},
		{[]string{"--acme-cache", "x"}, "need --acme-domain"},
		{[]string{"--public-origin", "https://r.example"}, "scheme must be ws or wss"},
		{[]string{"--public-origin", "wss://u@r.example"}, "user info"},
	} {
		var out, errb bytes.Buffer
		if code := run(context.Background(), tc.args, &out, &errb); code != 2 || !strings.Contains(errb.String(), tc.msg) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errb.String(), tc.msg)
		}
	}
}

// A relay that listens on 127.0.0.1 behind a same-host proxy is public
// (review 50 H1): v1 auth and v1 pairing are refused, v2 for the public
// origin works.
func TestLoopbackBehindProxyIsPublic(t *testing.T) {
	for _, args := range [][]string{
		{"--behind-proxy", "--public-origin", "wss://Relay.Example"},
		{"--public-origin", "wss://relay.example"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			addr, errb := startRelayLog(t, args...)
			if !strings.Contains(errb.String(), "public: yes; origins: wss://relay.example") {
				t.Fatalf("startup does not say public: %s", errb.String())
			}
			url := "ws://" + addr + envelope.ConnectPath

			if _, reply := dialAuth(t, url, nil, newPriv(t), 1, ""); reply.Code != envelope.CodeAuthFailed || !strings.Contains(reply.Message, "auth v2") {
				t.Fatalf("v1 auth: %+v, want auth_failed", reply)
			}
			if _, reply := dialAuth(t, url, nil, newPriv(t), 2, "ws://"+addr); reply.Code != envelope.CodeAuthFailed {
				t.Fatalf("v2 for the listen address: %+v, want auth_failed (only the public origin counts)", reply)
			}
			c, reply := dialAuth(t, url, nil, newPriv(t), 2, "wss://relay.example")
			if reply.Op != envelope.OpReady {
				t.Fatalf("v2 auth: %+v, want ready", reply)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Write(ctx, websocket.MessageText, []byte(`{"op":"pair_new","ref":"r1"}`)); err != nil {
				t.Fatal(err)
			}
			if got := readCtl(ctx, t, c); got.Code != envelope.CodePairV1Disabled {
				t.Fatalf("v1 pair_new: %+v, want pair_v1_disabled", got)
			}
		})
	}
}

func TestAllowAuthV1OnPublicRelay(t *testing.T) {
	addr, errb := startRelayLog(t, "--behind-proxy", "--public-origin", "wss://relay.example", "--allow-auth-v1")
	if !strings.Contains(errb.String(), "warning: --allow-auth-v1") {
		t.Errorf("no warning at start: %s", errb.String())
	}
	if _, reply := dialAuth(t, "ws://"+addr+envelope.ConnectPath, nil, newPriv(t), 1, ""); reply.Op != envelope.OpReady {
		t.Fatalf("v1 auth with --allow-auth-v1: %+v", reply)
	}
}

// The default loopback relay is not public, offers v1 and v2, and accepts v2
// for its loopback names.
func TestLoopbackRelayNotPublic(t *testing.T) {
	addr, errb := startRelayLog(t)
	_, port, _ := net.SplitHostPort(addr)
	if !strings.Contains(errb.String(), "public: no; origins: ws://127.0.0.1:"+port+" ws://localhost:"+port+" ws://[::1]:"+port) {
		t.Fatalf("startup line: %s", errb.String())
	}
	url := "ws://" + addr + envelope.ConnectPath
	for _, origin := range []string{"ws://127.0.0.1:" + port, "ws://localhost:" + port} {
		if _, reply := dialAuth(t, url, nil, newPriv(t), 2, origin); reply.Op != envelope.OpReady {
			t.Fatalf("v2 for %s: %+v", origin, reply)
		}
	}
	if _, reply := dialAuth(t, url, nil, newPriv(t), 1, ""); reply.Op != envelope.OpReady {
		t.Fatalf("v1 on loopback: %+v", reply)
	}
	// Pairing v1 stays on for a relay that is not public.
	c, _ := dialAuth(t, url, nil, newPriv(t), 1, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"op":"pair_new","ref":"r1"}`))
	if got := readCtl(ctx, t, c); got.Code == envelope.CodePairV1Disabled {
		t.Fatalf("v1 pairing off on a loopback relay: %+v", got)
	}
}

// testCertFiles writes the httptest certificate (valid for 127.0.0.1 and
// example.com) and its key as PEM files.
func testCertFiles(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	ts := httptest.NewUnstartedServer(http.NotFoundHandler())
	ts.StartTLS()
	ts.Close()
	c := ts.TLS.Certificates[0]
	keyDER, err := x509.MarshalPKCS8PrivateKey(c.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := testutil.TempDir(t)
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Certificate[0]}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestTLSCert(t *testing.T) {
	cert, key := testCertFiles(t)
	addr, errb := startRelayLog(t, "--tls-cert", cert, "--tls-key", key, "--public-origin", "wss://example.com")
	if !strings.Contains(errb.String(), "public: yes") {
		t.Fatalf("a TLS relay is public: %s", errb.String())
	}
	pemCert, _ := os.ReadFile(cert) //nolint:gosec // test file
	roots, err := relayclient.LoadRoots(pemCert)
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	opts := &websocket.DialOptions{HTTPClient: &http.Client{Transport: tr}}
	if _, reply := dialAuth(t, "wss://"+addr+envelope.ConnectPath, opts, newPriv(t), 2, "wss://example.com"); reply.Op != envelope.OpReady {
		t.Fatalf("v2 over TLS: %+v", reply)
	}

	// HTTP/1.1 only, TLS 1.2 minimum.
	conn, err := tls.Dial("tcp", addr, &tls.Config{RootCAs: roots, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	if p := conn.ConnectionState().NegotiatedProtocol; p == "h2" {
		t.Errorf("negotiated %q", p)
	}
	_ = conn.Close()
	if c, err := tls.Dial("tcp", addr, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS11, MaxVersion: tls.VersionTLS11}); err == nil { //nolint:gosec // proving TLS 1.1 is refused
		_ = c.Close()
		t.Error("TLS 1.1 handshake succeeded")
	}
	// Plain ws:// to the TLS port does not get a challenge.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c, _, err := websocket.Dial(ctx, "ws://"+addr+envelope.ConnectPath, nil); err == nil { //nolint:bodyclose // failure expected
		_ = c.CloseNow()
		t.Error("plaintext dial to the TLS port succeeded")
	}
}

func TestHTTPServerLimits(t *testing.T) {
	srv := newHTTPServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout != 10*time.Second || srv.IdleTimeout != 60*time.Second || srv.MaxHeaderBytes != 8<<10 || srv.TLSNextProto == nil {
		t.Fatalf("server limits: %+v", srv)
	}
}

// TestReadHeaderTimeoutSlowClient: a client that never finishes its request
// header is disconnected after ReadHeaderTimeout.
func TestReadHeaderTimeoutSlowClient(t *testing.T) {
	old := readHeaderTimeout
	readHeaderTimeout = 300 * time.Millisecond
	t.Cleanup(func() { readHeaderTimeout = old })
	addr, _ := startRelayLog(t)

	// A complete request is answered (/healthz is public).
	resp, err := http.Get("http://" + addr + relay.HealthPath) //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("/healthz: %d %s", resp.StatusCode, body)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "GET "+relay.HealthPath+" HTTP/1.1\r\nHost: x\r\n"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("slow client held the connection for %v", elapsed)
	}
	// Go answers 408 or just closes; either way the request is not served.
	if err == nil && !strings.Contains(line, "408") {
		t.Fatalf("slow client got %q", line)
	}
}
