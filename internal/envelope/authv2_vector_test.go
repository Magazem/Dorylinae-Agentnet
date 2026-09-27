package envelope_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// TestAuthV2Vector checks the implementation against the published relay
// auth v2 vector (Docs/protocol/envelope.md, tools/verifyvectors/vectors.json)
// byte for byte: origin, signed message, signature and auth frame.
func TestAuthV2Vector(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "tools", "verifyvectors", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		RelayAuth struct {
			SeedHex   string `json:"seed_hex"`
			Key       string `json:"key"`
			NonceB64u string `json:"nonce_b64u"`
			Cases     []struct {
				URL, Origin   string
				MessageHex    string `json:"message_hex"`
				SignatureB64u string `json:"signature_b64u"`
			} `json:"cases"`
			AuthFrame string `json:"auth_frame"`
		} `json:"relay_auth"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	ra := v.RelayAuth
	seed, _ := hex.DecodeString(ra.SeedHex)
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	sign := func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }
	nonce, err := envelope.DecodeNonce(ra.NonceB64u)
	if err != nil || envelope.KeyString(pub) != ra.Key || len(ra.Cases) == 0 {
		t.Fatalf("vector header: %v, key %s", err, envelope.KeyString(pub))
	}
	for i, tc := range ra.Cases {
		origin, err := envelope.Origin(tc.URL)
		if err != nil || origin != tc.Origin {
			t.Errorf("case %d: Origin(%q) = %q, %v; want %q", i, tc.URL, origin, err, tc.Origin)
			continue
		}
		msg, _ := envelope.AuthMessageV2(nonce, origin)
		if hex.EncodeToString(msg) != tc.MessageHex {
			t.Errorf("case %d: message %x", i, msg)
		}
		a, err := envelope.SignAuthV2(pub, nonce, origin, sign)
		if err != nil || a.Signature != tc.SignatureB64u {
			t.Errorf("case %d: signature %s, %v", i, a.Signature, err)
		}
		if got, err := envelope.VerifyAuthV2(a, nonce, []string{tc.Origin}); err != nil || !got.Equal(pub) {
			t.Errorf("case %d: own frame does not verify: %v", i, err)
		}
		if i == 0 {
			frame, _ := json.Marshal(a)
			if string(frame) != ra.AuthFrame {
				t.Errorf("auth frame\n got  %s\n want %s", frame, ra.AuthFrame)
			}
		}
	}
}
