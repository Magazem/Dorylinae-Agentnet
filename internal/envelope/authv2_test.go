package envelope_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

func TestOriginForm(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"wss://relay.example.com/v1/connect", "wss://relay.example.com"},
		{"wss://Relay.EXAMPLE.com", "wss://relay.example.com"},
		{"WSS://relay.example.com:443/", "wss://relay.example.com"},
		{"wss://relay.example.com:8443", "wss://relay.example.com:8443"},
		{"wss://relay.example.com:0443", "wss://relay.example.com"},
		{"ws://relay.example.com:80", "ws://relay.example.com"},
		{"ws://relay.example.com:443", "ws://relay.example.com:443"},
		{"wss://relay.example.com.:443", "wss://relay.example.com"},
		{"ws://127.0.0.1:8787/v1/connect?x=1", "ws://127.0.0.1:8787"},
		{"wss://[2001:DB8::1]:443", "wss://[2001:db8::1]"},
		{"ws://[::1]:8787", "ws://[::1]:8787"},
		{"wss://bücher.example", "wss://xn--bcher-kva.example"},
		{"wss://BÜCHER.example:8443", "wss://xn--bcher-kva.example:8443"},
	} {
		got, err := envelope.Origin(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("Origin(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{
		"", "http://relay.example.com", "wss://", "wss://user@relay.example.com",
		"wss://relay.example.com:0", "wss://relay.example.com:70000", "wss://relay.example.com:",
		"wss://[fe80::1%25eth0]:443", "relay.example.com:443",
	} {
		if got, err := envelope.Origin(bad); err == nil {
			t.Errorf("Origin(%q) = %q, want an error", bad, got)
		}
	}
}

func TestAuthMessageV2Layout(t *testing.T) {
	nonce := bytes.Repeat([]byte{7}, envelope.NonceSize)
	msg, err := envelope.AuthMessageV2(nonce, "wss://r.example")
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte("dorylinae-relay-auth-v2\n"), nonce...)
	want = append(want, 0, byte(len("wss://r.example")))
	want = append(want, "wss://r.example"...)
	if !bytes.Equal(msg, want) {
		t.Fatalf("message = %q\nwant      %q", msg, want)
	}
	if _, err := envelope.AuthMessageV2(nonce[:31], "wss://r.example"); err == nil {
		t.Error("short nonce accepted")
	}
	if _, err := envelope.AuthMessageV2(nonce, ""); err == nil {
		t.Error("empty origin accepted")
	}
}

func TestAuthV2SignVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sign := func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }
	nonce := make([]byte, envelope.NonceSize)
	_, _ = rand.Read(nonce)

	a, err := envelope.SignAuthV2(pub, nonce, "wss://y.example", sign)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(a)
	if !bytes.Contains(raw, []byte(`"v":2`)) {
		t.Errorf("v2 auth frame lacks \"v\":2: %s", raw)
	}
	if got, err := envelope.VerifyAuthV2(a, nonce, []string{"wss://x.example", "wss://y.example"}); err != nil || !got.Equal(pub) {
		t.Errorf("own origin: %v", err)
	}
	// The relay-in-the-middle: a signature made for X's origin is useless at Y.
	if _, err := envelope.VerifyAuthV2(a, nonce, []string{"wss://x.example"}); err == nil {
		t.Error("signature for another origin accepted")
	}
	other := bytes.Clone(nonce)
	other[0] ^= 1
	if _, err := envelope.VerifyAuthV2(a, other, []string{"wss://y.example"}); err == nil {
		t.Error("signature for another nonce accepted")
	}
	// Versions do not cross: v1 verification refuses a v2 frame and the
	// reverse, and a v1 signature relabelled v2 does not verify.
	if _, err := envelope.VerifyAuth(a, nonce); err == nil {
		t.Error("VerifyAuth accepted a v2 frame")
	}
	v1, _ := envelope.SignAuth(pub, nonce, sign)
	if _, err := envelope.VerifyAuthV2(v1, nonce, []string{"wss://y.example"}); err == nil {
		t.Error("VerifyAuthV2 accepted a v1 frame")
	}
	v1.V = 2
	if _, err := envelope.VerifyAuthV2(v1, nonce, []string{"wss://y.example"}); err == nil {
		t.Error("v1 signature accepted as v2")
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for h, want := range map[string]bool{
		"localhost": true, "LocalHost.": true, "127.0.0.1": true, "127.9.9.9": true, "::1": true,
		"10.0.0.1": false, "relay.example.com": false, "::2": false, "localhost.example.com": false, "": false,
	} {
		if got := envelope.IsLoopbackHost(h); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v", h, got)
		}
	}
}
