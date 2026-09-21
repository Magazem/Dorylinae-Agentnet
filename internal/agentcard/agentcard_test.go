package agentcard_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Test vector from Docs/protocol/agent-card.md.
const (
	vecCanonical = `{"created":"2026-01-02T03:04:05Z","harness":"custom","name":"Ada \"test\" <é>","public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","skills":[{"description":"a/b & c","id":"review","name":"Code review"}],"version":1}`
	vecSig       = "XN3GYSED9twF4mei-x7TUzHYzOMQU7aonCRQkebGdcXr8MvkkjLQVjZmtPiCNLTNigKIskMMBqF9hgQW5jdPDA"
)

func vecKey() ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func vecCard(t *testing.T) agentcard.Card {
	t.Helper()
	c, err := agentcard.New(vecKey().Public().(ed25519.PublicKey), `Ada "test" <é>`, "custom",
		[]agentcard.Skill{{ID: "review", Name: "Code review", Description: "a/b & c"}},
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVector(t *testing.T) {
	c := vecCard(t)
	canon, err := agentcard.Canonical(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(canon) != vecCanonical {
		t.Fatalf("canonical mismatch:\n got %s\nwant %s", canon, vecCanonical)
	}
	s, err := agentcard.Sign(vecKey(), c)
	if err != nil {
		t.Fatal(err)
	}
	if s.Signature != vecSig {
		t.Fatalf("signature mismatch: %s", s.Signature)
	}
}

func signedJSON(t *testing.T) []byte {
	t.Helper()
	s, err := agentcard.Sign(vecKey(), vecCard(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVerifyRoundTripIgnoresExtraTopLevel(t *testing.T) {
	var env map[string]any
	if err := json.Unmarshal(signedJSON(t), &env); err != nil {
		t.Fatal(err)
	}
	env["ok"] = true
	env["key_backend"] = "file"
	b, _ := json.Marshal(env)
	got, err := agentcard.Verify(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Card.Name != `Ada "test" <é>` || got.Signature != vecSig {
		t.Fatalf("unexpected card: %+v", got)
	}
}

// Any change to any card field, an extra field, or a changed signature must fail.
func TestTamperFailsVerification(t *testing.T) {
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32)).Public().(ed25519.PublicKey)
	mutations := map[string]func(card map[string]any){
		"name":       func(c map[string]any) { c["name"] = "Eve" },
		"harness":    func(c map[string]any) { c["harness"] = "codex" },
		"version":    func(c map[string]any) { c["version"] = 2 },
		"created":    func(c map[string]any) { c["created"] = "2026-01-02T03:04:06Z" },
		"public_key": func(c map[string]any) { c["public_key"] = base64URL(other) },
		"skill id":   func(c map[string]any) { c["skills"].([]any)[0].(map[string]any)["id"] = "x" },
		"skill name": func(c map[string]any) { c["skills"].([]any)[0].(map[string]any)["name"] = "x" },
		"skill desc": func(c map[string]any) { c["skills"].([]any)[0].(map[string]any)["description"] = "x" },
		"skill add": func(c map[string]any) {
			c["skills"] = append(c["skills"].([]any), map[string]any{"id": "y", "name": "y", "description": ""})
		},
		"skills removed": func(c map[string]any) { c["skills"] = []any{} },
		"extra field":    func(c map[string]any) { c["extra"] = "x" },
		"field removed":  func(c map[string]any) { delete(c, "harness") },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var env map[string]any
			if err := json.Unmarshal(signedJSON(t), &env); err != nil {
				t.Fatal(err)
			}
			mutate(env["card"].(map[string]any))
			b, _ := json.Marshal(env)
			if _, err := agentcard.Verify(b); err == nil {
				t.Fatal("tampered card verified")
			}
		})
	}
	t.Run("signature", func(t *testing.T) {
		var env map[string]any
		_ = json.Unmarshal(signedJSON(t), &env)
		sig := env["signature"].(string)
		flipped := "A"
		if sig[0] == 'A' {
			flipped = "B"
		}
		env["signature"] = flipped + sig[1:]
		b, _ := json.Marshal(env)
		if _, err := agentcard.Verify(b); err == nil {
			t.Fatal("tampered signature verified")
		}
	})
}

func base64URL(pub ed25519.PublicKey) string {
	c, _ := agentcard.New(pub, "n", "h", nil, time.Unix(0, 0))
	return c.PublicKey
}

func TestVerifyRejectsMalformed(t *testing.T) {
	good := string(signedJSON(t))
	cases := map[string]string{
		"not json":        `nope`,
		"array":           `[]`,
		"no card":         `{"signature":"x"}`,
		"no signature":    `{"card":{}}`,
		"duplicate key":   strings.Replace(good, `"card":{`, `"card":{"name":"a","name":"b",`, 1),
		"trailing data":   good + ` {}`,
		"invalid utf8":    strings.Replace(good, "Ada", "Ad\xff", 1),
		"float in card":   strings.Replace(good, `"version":1`, `"version":1.5`, 1),
		"exp in card":     strings.Replace(good, `"version":1`, `"version":1e0`, 1),
		"short pub key":   strings.Replace(good, "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg", "AAAA", 1),
		"short signature": strings.Replace(good, vecSig, "AAAA", 1),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := agentcard.Verify([]byte(in)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSignRejectsWrongKey(t *testing.T) {
	_, other, _ := ed25519.GenerateKey(nil)
	if _, err := agentcard.Sign(other, vecCard(t)); err == nil {
		t.Fatal("signing with a key that does not match the card must fail")
	}
}

func TestNewValidates(t *testing.T) {
	pub := vecKey().Public().(ed25519.PublicKey)
	now := time.Now()
	for name, f := range map[string]func() error{
		"empty name":     func() error { _, err := agentcard.New(pub, "", "h", nil, now); return err },
		"empty harness":  func() error { _, err := agentcard.New(pub, "n", "", nil, now); return err },
		"control char":   func() error { _, err := agentcard.New(pub, "a\nb", "h", nil, now); return err },
		"long name":      func() error { _, err := agentcard.New(pub, strings.Repeat("a", 129), "h", nil, now); return err },
		"empty skill id": func() error { _, err := agentcard.New(pub, "n", "h", []agentcard.Skill{{Name: "x"}}, now); return err },
		"short key":      func() error { _, err := agentcard.New(pub[:5], "n", "h", nil, now); return err },
	} {
		if err := f(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestCanonicalKeyOrderAndEscapes(t *testing.T) {
	c := vecCard(t)
	c.Name = "a\x7f\"\\/"
	canon, err := agentcard.Canonical(c)
	if err != nil {
		t.Fatal(err)
	}
	// DEL is written literally; quote and backslash are escaped; slash is not.
	if !strings.Contains(string(canon), "\"name\":\"a\x7f\\\"\\\\/\"") {
		t.Fatalf("unexpected escaping: %s", canon)
	}
	if !strings.HasPrefix(string(canon), `{"created":`) {
		t.Fatalf("keys not sorted: %s", canon)
	}
}
