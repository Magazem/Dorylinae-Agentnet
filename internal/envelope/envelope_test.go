package envelope_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

func key(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func valid(t *testing.T) envelope.Envelope {
	pa, _ := key(t)
	pb, _ := key(t)
	return envelope.Envelope{From: envelope.KeyString(pa), To: envelope.KeyString(pb), Team: "core", Type: "ping", ID: "id-1", TS: "2026-01-02T03:04:05Z", Payload: []byte{0, 1, 2, 255}}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	e := valid(t)
	raw, err := e.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := envelope.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != e.ID || got.From != e.From || !bytes.Equal(got.Payload, e.Payload) {
		t.Fatalf("round trip changed envelope: %+v", got)
	}
	h, err := envelope.ParseHeader(raw)
	if err != nil || h != e.Header() {
		t.Fatalf("ParseHeader = %+v, %v", h, err)
	}
}

func TestValidateRejects(t *testing.T) {
	for name, mutate := range map[string]func(*envelope.Envelope){
		"short from":     func(e *envelope.Envelope) { e.From = "abc" },
		"bad to":         func(e *envelope.Envelope) { e.To = strings.Repeat("!", 43) },
		"empty type":     func(e *envelope.Envelope) { e.Type = "" },
		"upper type":     func(e *envelope.Envelope) { e.Type = "Ping" },
		"empty id":       func(e *envelope.Envelope) { e.ID = "" },
		"long id":        func(e *envelope.Envelope) { e.ID = strings.Repeat("a", 129) },
		"newline in id":  func(e *envelope.Envelope) { e.ID = "a\nb" },
		"space in team":  func(e *envelope.Envelope) { e.Team = "a b" },
		"bad ts":         func(e *envelope.Envelope) { e.TS = "yesterday" },
		"empty ts":       func(e *envelope.Envelope) { e.TS = "" },
		"unicode in typ": func(e *envelope.Envelope) { e.Type = "pïng" },
	} {
		e := valid(t)
		mutate(&e)
		if _, err := e.Marshal(); err == nil {
			t.Errorf("%s: Marshal accepted an invalid envelope", name)
		}
	}
	e := valid(t)
	e.Team = ""
	if _, err := e.Marshal(); err != nil {
		t.Errorf("empty team should be allowed: %v", err)
	}
}

func TestMarshalRejectsOversize(t *testing.T) {
	e := valid(t)
	e.Payload = make([]byte, envelope.MaxFrameBytes)
	if _, err := e.Marshal(); err == nil {
		t.Fatal("oversize envelope accepted")
	}
}

func TestParseErrorsDoNotEchoInput(t *testing.T) {
	_, err := envelope.Parse([]byte(`{"from":"SECRET-MARKER`))
	if err == nil || strings.Contains(err.Error(), "SECRET-MARKER") {
		t.Fatalf("err = %v", err)
	}
}

func TestClassify(t *testing.T) {
	f, err := envelope.Classify([]byte(`{"op":"error","code":"x"}`))
	if err != nil || f.Control == nil || f.Control.Code != "x" {
		t.Fatalf("control: %+v %v", f, err)
	}
	f, err = envelope.Classify([]byte(`{"from":"a"}`))
	if err != nil || f.Control != nil || f.Envelope == nil {
		t.Fatalf("envelope: %+v %v", f, err)
	}
	if _, err := envelope.Classify([]byte(`[1]`)); err == nil {
		t.Fatal("array accepted")
	}
}

func TestAuthSignVerify(t *testing.T) {
	pub, priv := key(t)
	nonce := make([]byte, envelope.NonceSize)
	_, _ = rand.Read(nonce)
	sign := func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }
	c, err := envelope.SignAuth(pub, nonce, sign)
	if err != nil {
		t.Fatal(err)
	}
	got, err := envelope.VerifyAuth(c, nonce)
	if err != nil || !got.Equal(pub) {
		t.Fatalf("VerifyAuth: %v", err)
	}
	other := make([]byte, envelope.NonceSize)
	if _, err := envelope.VerifyAuth(c, other); err == nil {
		t.Fatal("signature verified against a different nonce")
	}
	// The raw nonce without the domain prefix must not be an acceptable signed message.
	raw := ed25519.Sign(priv, nonce)
	c.Signature = envelope.KeyString(raw)
	if _, err := envelope.VerifyAuth(c, nonce); err == nil {
		t.Fatal("signature without domain prefix accepted")
	}
}
