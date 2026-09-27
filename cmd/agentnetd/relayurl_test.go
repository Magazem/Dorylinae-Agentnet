package main

import (
	"bytes"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestCheckRelayURL(t *testing.T) {
	for _, tc := range []struct {
		url      string
		insecure bool // DORYLINAE_ALLOW_INSECURE_RELAY=1
		ok, warn bool
	}{
		{url: "", ok: true},
		{url: "ws://127.0.0.1:8787", ok: true},
		{url: "ws://127.1.2.3:8787", ok: true},
		{url: "ws://localhost:8787", ok: true},
		{url: "ws://LOCALHOST.:8787", ok: true},
		{url: "ws://[::1]:8787", ok: true},
		{url: "wss://relay.example.com", ok: true},
		{url: "wss://10.0.0.1:8443", ok: true},
		{url: "ws://relay.example.com"},
		{url: "ws://10.0.0.1:8787"},
		{url: "ws://[::2]:8787"},
		{url: "ws://10.0.0.1:8787", insecure: true, ok: true, warn: true},
		{url: "http://127.0.0.1:8787"},
		{url: "wss://user@relay.example.com"},
	} {
		warning, err := checkRelayURL(tc.url, tc.insecure)
		if (err == nil) != tc.ok || (warning != "") != tc.warn {
			t.Errorf("%q (insecure=%v): warning %q, err %v; want ok=%v warn=%v", tc.url, tc.insecure, warning, err, tc.ok, tc.warn)
		}
		if err != nil && strings.HasPrefix(tc.url, "ws://") && !strings.Contains(err.Error(), "must use wss://") {
			t.Errorf("%q: error %q does not say wss:// is needed", tc.url, err)
		}
	}
}

// TestRunRefusesRemoteWS: the URL rule at start. The refusal comes before
// the daemon opens anything.
func TestRunRefusesRemoteWS(t *testing.T) {
	t.Setenv(InsecureRelayEnv, "")
	home := filepath.Join(testutil.TempDir(t), "home")
	code, _, errs := invoke(t, "run", "--home", home, "--relay", "ws://relay.example.com")
	if code != 2 || !strings.Contains(errs, "a remote relay must use wss://") {
		t.Fatalf("exit %d, stderr %q; want 2 and the wss:// message", code, errs)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("refused start still created %s (%v)", home, err)
	}
}

// TestInstallRefusesRemoteWS: the URL rule at install, where the insecure
// escape hatch is not honoured (the service would not inherit it).
func TestInstallRefusesRemoteWS(t *testing.T) {
	home, r, _ := setup(t)
	t.Setenv(InsecureRelayEnv, "1")
	code, _, errs := invoke(t, "install", "--home", home, "--relay", "ws://10.0.0.1:8787")
	if code != 2 || !strings.Contains(errs, "a remote relay must use wss://") {
		t.Fatalf("exit %d, stderr %q; want 2 and the wss:// message", code, errs)
	}
	if len(r.ran) != 0 {
		t.Errorf("refused install ran %v", r.ran)
	}
	if code, _, errs := invoke(t, "install", "--home", home, "--relay", "ws://127.0.0.1:8787"); code != 0 {
		t.Errorf("loopback ws:// refused: exit %d, %s", code, errs)
	}
}

// testCA writes the certificate of a throwaway TLS test server as PEM.
func testCA(t *testing.T) (path string, pemCA []byte) {
	t.Helper()
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	ts.Close()
	pemCA = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})
	path = filepath.Join(testutil.TempDir(t), "ca.pem")
	if err := os.WriteFile(path, pemCA, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, pemCA
}

func TestInstallRelayCA(t *testing.T) {
	home, _, _ := setup(t)
	caPath, pemCA := testCA(t)
	stored := filepath.Join(home, relayCAFile)

	code, out, errs := invoke(t, "install", "--home", home, "--relay", "wss://relay.lan:8443", "--relay-ca", caPath, "--dry-run")
	if code != 0 || !strings.Contains(out, relayCAFile) {
		t.Fatalf("dry run: exit %d, out %q, err %q", code, out, errs)
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote the CA (%v)", err)
	}

	if code, _, errs := invoke(t, "install", "--home", home, "--relay", "wss://relay.lan:8443", "--relay-ca", caPath); code != 0 {
		t.Fatalf("install: exit %d, %s", code, errs)
	}
	got, err := os.ReadFile(stored) //nolint:gosec // test path under a temp dir
	if err != nil || !bytes.Equal(got, pemCA) {
		t.Fatalf("stored CA = %q, %v", got, err)
	}
	// run picks the stored CA up when --relay-ca is not given.
	if pool, err := relayRoots(home, ""); err != nil || pool == nil {
		t.Errorf("relayRoots with a stored CA: %v, %v", pool, err)
	}

	junk := filepath.Join(testutil.TempDir(t), "junk.pem")
	if err := os.WriteFile(junk, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := invoke(t, "install", "--home", home, "--relay-ca", junk); code != 2 || !strings.Contains(errs, "no PEM certificate") {
		t.Errorf("junk CA: exit %d, %q", code, errs)
	}
	if code, _, _ := invoke(t, "install", "--home", home, "--relay-ca", filepath.Join(home, "missing.pem")); code != 2 {
		t.Errorf("missing CA: exit %d, want 2", code)
	}
}

func TestRelayRootsDefaults(t *testing.T) {
	dir := testutil.TempDir(t)
	if pool, err := relayRoots(dir, ""); err != nil || pool != nil {
		t.Fatalf("no CA anywhere: %v, %v; want system roots (nil)", pool, err)
	}
	caPath, _ := testCA(t)
	if pool, err := relayRoots(dir, caPath); err != nil || pool == nil {
		t.Fatalf("--relay-ca: %v, %v", pool, err)
	}
	if code, _, errs := invoke(t, "run", "--home", filepath.Join(dir, "h"), "--relay-ca", filepath.Join(dir, "missing.pem")); code != 2 {
		t.Errorf("run with a missing --relay-ca: exit %d, %q", code, errs)
	}
}
