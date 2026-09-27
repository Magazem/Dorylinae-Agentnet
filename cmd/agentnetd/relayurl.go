package main

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// InsecureRelayEnv set to 1 lets `agentnetd run` use a remote relay over
// plain ws:// (LAN tests only). It is never honoured by install.
const InsecureRelayEnv = "DORYLINAE_ALLOW_INSECURE_RELAY"

// relayCAFile is where `install --relay-ca` stores the relay's private CA in
// the config directory; `run` uses it when --relay-ca is not given.
const relayCAFile = "relay_ca.pem"

// checkRelayURL applies the relay URL rule (Docs/protocol/relay-hosted.md §1):
// a remote relay must use wss://. It returns a warning to print when the
// insecure escape hatch let a remote ws:// URL through.
func checkRelayURL(raw string, allowInsecure bool) (warning string, err error) {
	if raw == "" {
		return "", nil
	}
	insecure, err := relayclient.CheckURL(raw, allowInsecure)
	if err != nil {
		return "", err
	}
	if insecure {
		return fmt.Sprintf("warning: %s=1: the remote relay %s is reached over plain ws://; routing metadata travels unencrypted (LAN tests only)", InsecureRelayEnv, raw), nil
	}
	return "", nil
}

// readRelayCA reads a PEM CA file and checks it holds a certificate. It
// returns the file's bytes and the roots to verify the relay with: the
// system roots plus that CA.
func readRelayCA(path string) ([]byte, *x509.CertPool, error) {
	pemCA, err := os.ReadFile(path) //nolint:gosec // the user names the file
	if err != nil {
		return nil, nil, fmt.Errorf("--relay-ca: %w", err)
	}
	pool, err := relayclient.LoadRoots(pemCA)
	if err != nil {
		return nil, nil, fmt.Errorf("--relay-ca %s: %w", path, err)
	}
	return pemCA, pool, nil
}

// relayRoots returns the roots for the relay connection: --relay-ca if
// given, else the CA stored by install in dir, else nil (system roots).
func relayRoots(dir, flagPath string) (*x509.CertPool, error) {
	path := flagPath
	if path == "" {
		path = filepath.Join(dir, relayCAFile)
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
	}
	_, pool, err := readRelayCA(path)
	return pool, err
}
