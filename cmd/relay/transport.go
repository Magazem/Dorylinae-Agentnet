package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// HTTP server limits (Docs/protocol/relay-hosted.md §1). readHeaderTimeout is
// a variable so a test can show a slow client is cut off without waiting 10 s.
var readHeaderTimeout = 10 * time.Second

const (
	idleTimeout    = 60 * time.Second
	maxHeaderBytes = 8 << 10
)

// newHTTPServer serves h over HTTP/1.1 only (WebSocket) with the header and
// idle limits. ReadHeaderTimeout does not cover a hijacked WebSocket; the
// challenge TTL bounds a slow client there.
func newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		TLSNextProto:      map[string]func(*http.Server, *tls.Conn, http.Handler){}, // no HTTP/2
	}
}

// transportFlags are the TLS and relay authentication flags (ticket 4.0a).
type transportFlags struct {
	tlsCert, tlsKey                  string
	acmeDomain, acmeCache, acmeEmail string
	behindProxy, allowAuthV1         bool
	origins                          []string // canonical (envelope.Origin)
}

func (t *transportFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&t.tlsCert, "tls-cert", "", "serve TLS with this PEM certificate (chain) file; needs --tls-key")
	fs.StringVar(&t.tlsKey, "tls-key", "", "PEM private key for --tls-cert")
	fs.StringVar(&t.acmeDomain, "acme-domain", "", "serve TLS with a Let's Encrypt certificate for this name (TLS-ALPN-01 on the listen port, which must be reachable on port 443)")
	fs.StringVar(&t.acmeCache, "acme-cache", "", "directory for the ACME account and certificates (default: relay-acme next to the queue database)")
	fs.StringVar(&t.acmeEmail, "acme-email", "", "contact address given to Let's Encrypt (optional)")
	fs.BoolVar(&t.behindProxy, "behind-proxy", false, "TLS is terminated by a reverse proxy or the platform in front of this relay; the relay is public even when it listens on 127.0.0.1")
	fs.Func("public-origin", "origin daemons dial to reach this relay, e.g. wss://relay.example.com (repeatable; required with --tls-cert, --acme-domain and --behind-proxy); relay auth v2 signatures must name one of them", func(v string) error {
		o, err := envelope.Origin(v)
		if err != nil {
			return err
		}
		t.origins = append(t.origins, o)
		return nil
	})
	fs.BoolVar(&t.allowAuthV1, "allow-auth-v1", false, "also accept relay auth v1 (Phase 0-3 daemons) on a public relay, for a migration window; v1 does not name the relay")
}

// validate checks the flag combination and reports whether the relay is
// public (review 50 H1): reachable from other machines, directly or through
// a proxy on this host, which a loopback listen address does not rule out.
func (t *transportFlags) validate(loopbackListen bool) (public bool, err error) {
	if (t.tlsCert == "") != (t.tlsKey == "") {
		return false, errors.New("--tls-cert and --tls-key go together")
	}
	modes := 0
	for _, on := range []bool{t.tlsCert != "", t.acmeDomain != "", t.behindProxy} {
		if on {
			modes++
		}
	}
	if modes > 1 {
		return false, errors.New("use only one of --tls-cert/--tls-key, --acme-domain and --behind-proxy")
	}
	if t.acmeDomain == "" && (t.acmeCache != "" || t.acmeEmail != "") {
		return false, errors.New("--acme-cache and --acme-email need --acme-domain")
	}
	if !loopbackListen && modes == 0 {
		return false, errors.New("a non-loopback relay needs TLS: --tls-cert/--tls-key, --acme-domain or --behind-proxy")
	}
	if modes > 0 && len(t.origins) == 0 {
		return false, errors.New("--public-origin is required with --tls-cert, --acme-domain or --behind-proxy (the URL daemons dial, e.g. wss://relay.example.com)")
	}
	public = !loopbackListen || modes > 0
	for _, o := range t.origins {
		if u, err := url.Parse(o); err != nil || !envelope.IsLoopbackHost(u.Hostname()) {
			public = true
		}
	}
	return public, nil
}

// tlsConfig loads the certificate or sets up ACME. Nil means plain HTTP:
// loopback, or TLS terminated in front of the relay.
func (t *transportFlags) tlsConfig(stateDir string) (*tls.Config, error) {
	switch {
	case t.tlsCert != "":
		cert, err := tls.LoadX509KeyPair(t.tlsCert, t.tlsKey)
		if err != nil {
			return nil, fmt.Errorf("load --tls-cert/--tls-key: %w", err)
		}
		return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}}, nil
	case t.acmeDomain != "":
		cache := t.acmeCache
		if cache == "" {
			cache = filepath.Join(stateDir, "relay-acme")
		}
		if err := os.MkdirAll(cache, 0o700); err != nil {
			return nil, fmt.Errorf("create --acme-cache: %w", err)
		}
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(t.acmeDomain),
			Cache:      autocert.DirCache(cache),
			Email:      t.acmeEmail,
		}
		c := m.TLSConfig()
		c.MinVersion = tls.VersionTLS12
		c.NextProtos = []string{"http/1.1", acme.ALPNProto}
		return c, nil
	}
	return nil, nil
}

// loopbackOrigins are the origins of a relay that is not public: ws:// on
// each loopback name, at the port it listens on.
func loopbackOrigins(addr net.Addr) []string {
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return nil
	}
	return []string{"ws://127.0.0.1:" + port, "ws://localhost:" + port, "ws://[::1]:" + port}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
