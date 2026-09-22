package notify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestValidateWebhookURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://hooks.example.com/x", true},
		{"https://hooks.example.com", true}, // empty path is fine
		{"http://localhost:8080/hook", true},
		{"http://127.0.0.1/hook", true},
		{"http://[::1]/hook", true},
		{"http://example.com/hook", false},            // http to a non-loopback host
		{"http://evil.example/hook", false},           // http, not loopback
		{"ftp://example.com/hook", false},             // wrong scheme
		{"https://user:pass@example.com/hook", false}, // user info
		{"not a url", false},
		{"https://" + strings.Repeat("a", 2048) + ".com/hook", false}, // over the 2048 byte cap
	}
	for _, c := range cases {
		err := ValidateWebhookURL(c.url)
		if (err == nil) != c.ok {
			t.Errorf("ValidateWebhookURL(%q) = %v, want ok=%v", c.url, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrBadWebhookURL) {
			t.Errorf("ValidateWebhookURL(%q) error %v does not wrap ErrBadWebhookURL", c.url, err)
		}
	}
}

func TestCheckDialAddressMatrix(t *testing.T) {
	cases := []struct {
		name   string
		scheme string
		ip     string
		block  bool
	}{
		{"http loopback ok", "http", "127.0.0.1", false},
		{"http loopback v6 ok", "http", "::1", false},
		{"http public blocked", "http", "93.184.216.34", true},
		{"http private blocked", "http", "10.0.0.5", true},
		{"http link-local blocked", "http", "169.254.169.254", true},
		{"https public ok", "https", "93.184.216.34", false},
		{"https loopback ok", "https", "127.0.0.1", false},
		{"https link-local blocked", "https", "169.254.169.254", true},
		{"https link-local v6 blocked", "https", "fe80::1", true},
		{"https unspecified blocked", "https", "0.0.0.0", true},
		{"https multicast blocked", "https", "224.0.0.1", true},
		{"https private blocked", "https", "10.1.2.3", true},
		{"https private 172 blocked", "https", "172.16.0.5", true},
		{"https private 192 blocked", "https", "192.168.1.1", true},
		{"https unspecified v6 blocked", "https", "::", true},
		{"https v4-mapped loopback ok", "https", "::ffff:127.0.0.1", false},
		{"http v4-mapped loopback ok", "http", "::ffff:127.0.0.1", false},
		{"https v4-mapped metadata blocked", "https", "::ffff:169.254.169.254", true},
		{"https v4-mapped private blocked", "https", "::ffff:10.0.0.1", true},
		{"https unique-local v6 blocked", "https", "fd00::1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkDialAddress(c.scheme, net.ParseIP(c.ip))
			blocked := err != nil
			if blocked != c.block {
				t.Fatalf("checkDialAddress(%q, %q) blocked=%v (%v), want %v", c.scheme, c.ip, blocked, err, c.block)
			}
			if blocked && !errors.Is(err, ErrBlockedAddress) {
				t.Fatalf("error %v does not wrap ErrBlockedAddress", err)
			}
		})
	}
}

// TestDialBlocksDNSRebinding simulates a hostname that resolves to the cloud
// metadata address: the dial-time check must reject it even though the URL
// itself is well-formed https (Docs/protocol/notify.md §Configuration,
// review 12).
func TestDialBlocksDNSRebinding(t *testing.T) {
	resolve := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}
	client := httpClient("https", resolve)
	resp, err := client.Get("https://rebind.invalid.test/hook")
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err == nil {
		t.Fatal("expected the dial to be blocked")
	}
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("error %v does not wrap ErrBlockedAddress", err)
	}
}

// TestDialAllowsLoopbackReceiver is the positive case: an httptest server on
// loopback must still be reachable through the same guarded client.
func TestDialAllowsLoopbackReceiver(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client := httpClient("http", nil)
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("loopback receiver blocked: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestNoRedirectsFollowed checks that a 3xx response is returned as-is
// instead of being followed, even toward a private address
// (Docs/protocol/notify.md §Delivery "Redirects are not followed", review
// 12).
func TestNoRedirectsFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/internal", http.StatusFound)
	}))
	defer srv.Close()
	client := httpClient("http", nil)
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 (not followed)", resp.StatusCode)
	}
}

// errFakeDial stands in for a real connection attempt in the proxy tests.
var errFakeDial = errors.New("fake dial")

// proxyTestClient builds a guarded client whose environment proxy is
// proxyURL (empty: none), whose resolver maps every name to ip, and whose
// dials are recorded instead of made.
func proxyTestClient(t *testing.T, proxyURL, ip string, dialed *[]string) *http.Client {
	t.Helper()
	proxyFrom := func(*http.Request) (*url.URL, error) {
		if proxyURL == "" {
			return nil, nil
		}
		return url.Parse(proxyURL)
	}
	resolve := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
	}
	dial := func(_ context.Context, _, addr string) (net.Conn, error) {
		*dialed = append(*dialed, addr)
		return nil, errFakeDial
	}
	return newHTTPClient("https", resolve, proxyFrom, dial)
}

// TestDialProxyPolicy checks that a proxy from the environment is held to
// the base list only (a private proxy is allowed, it is trusted by the
// user's configuration), while a direct dial keeps the private-range
// hardening and a link-local proxy is still refused
// (Docs/protocol/notify.md §Configuration).
func TestDialProxyPolicy(t *testing.T) {
	cases := []struct {
		name, proxy, ip string
		wantDial        string // "" means blocked before dialling
	}{
		{"private proxy allowed", "http://Proxy.Corp.test:3128", "10.0.0.8", "10.0.0.8:3128"},
		{"private proxy default port", "http://proxy.corp.test", "10.0.0.8", "10.0.0.8:80"},
		{"link-local proxy blocked", "http://proxy.corp.test:3128", "169.254.169.254", ""},
		{"private target without proxy blocked", "", "10.0.0.8", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dialed []string
			resp, err := proxyTestClient(t, c.proxy, c.ip, &dialed).Get("https://hooks.example.test/x")
			if resp != nil {
				_ = resp.Body.Close()
			}
			if c.wantDial == "" {
				if !errors.Is(err, ErrBlockedAddress) || len(dialed) != 0 {
					t.Fatalf("err = %v, dialed %v; want blocked_address and no dial", err, dialed)
				}
				return
			}
			if !errors.Is(err, errFakeDial) || len(dialed) != 1 || dialed[0] != c.wantDial {
				t.Fatalf("err = %v, dialed %v; want one dial to %s", err, dialed, c.wantDial)
			}
		})
	}
}
