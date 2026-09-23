package capability

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Test vectors from Docs/protocol/grant.md §Test vectors.
const (
	vecIss     = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	vecAud     = "Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc"
	vecSession = "s-36375782ceb6baea9cee4d4273dfb035"

	vecCanonical = `{"action":"git.read","aud":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc","exp":"2026-01-02T05:00:00Z","id":"g-00112233445566778899aabbccddeeff","iss":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","nbf":"2026-01-02T03:00:00Z","resource":{"branch":"feat-x","kind":"git","label":"agentnet-3f2a"},"scope":"internal/mail","sensitive":true,"session":"s-36375782ceb6baea9cee4d4273dfb035","v":1}`
	vecHashHex   = "78da9339e8a1500ec1781d5d4be3b9b52149eac7e7f815a77b0b6dab0abe239d"
	vecSig       = "l3c5wKLJPH0BGLjAXQk0Z1cjYlQ0aWb0ueo1ZnI4ooCBLXMqbMH8r6n1ox3Wem0q1jXl-MQEpGcGgewtCa_CCg"
)

// seed returns the deterministic Ed25519 key derived from 32 sequential
// bytes starting at start (pairing.md §Test vectors: issuer seed 00..1f,
// holder/redeemer seed 20..3f).
func seed(start byte) ed25519.PrivateKey {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = start + byte(i)
	}
	return ed25519.NewKeyFromSeed(s)
}

func vecGrant() Grant {
	nbf := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	return Grant{
		V:         1,
		ID:        "g-00112233445566778899aabbccddeeff",
		Iss:       vecIss,
		Aud:       vecAud,
		Session:   vecSession,
		Action:    ActionGitRead,
		Resource:  Resource{Kind: KindGit, Label: "agentnet-3f2a", Branch: "feat-x"},
		Scope:     "internal/mail",
		Nbf:       nbf,
		Exp:       nbf.Add(2 * time.Hour),
		Sensitive: true,
	}
}

func TestVector(t *testing.T) {
	privIss := seed(0x00)
	if got := base64.RawURLEncoding.EncodeToString(privIss.Public().(ed25519.PublicKey)); got != vecIss {
		t.Fatalf("iss key: got %s want %s", got, vecIss)
	}
	privAud := seed(0x20)
	if got := base64.RawURLEncoding.EncodeToString(privAud.Public().(ed25519.PublicKey)); got != vecAud {
		t.Fatalf("aud key: got %s want %s", got, vecAud)
	}

	g := vecGrant()

	// Canonical grant, independent of the sig envelope.
	grantOnly, err := agentcard.CanonicalValue(grantMap(g))
	if err != nil {
		t.Fatal(err)
	}
	if string(grantOnly) != vecCanonical {
		t.Fatalf("canonical grant mismatch:\n got  %s\n want %s", grantOnly, vecCanonical)
	}
	sum := sha256.Sum256(grantOnly)
	if got := hex.EncodeToString(sum[:]); got != vecHashHex {
		t.Fatalf("hash mismatch: got %s want %s", got, vecHashHex)
	}

	// Sign reproduces the published signature byte for byte (Ed25519 is
	// deterministic).
	tok, err := Sign(privIss, g)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if tok.Sig != vecSig {
		t.Fatalf("signature mismatch:\n got  %s\n want %s", tok.Sig, vecSig)
	}

	sig, err := b64u.DecodeString(tok.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		t.Fatalf("bad sig encoding: %v", err)
	}
	if !ed25519.Verify(privIss.Public().(ed25519.PublicKey), append([]byte(domain), grantOnly...), sig) {
		t.Fatal("signature does not verify")
	}

	// The token as a whole round-trips through Verify, both sides.
	wire, err := Canonical(tok)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)
	sessionOpen := func(id, requester, worker string) (known, open bool) {
		return id == vecSession && requester == vecIss && worker == vecAud, true
	}

	verified, err := Verify(wire, VerifyParams{
		Role:         RoleHolder,
		Self:         vecAud,
		Counterparty: vecIss,
		Now:          now,
		SessionOpen:  sessionOpen,
	})
	if err != nil {
		t.Fatalf("Verify (holder): %v", err)
	}
	if verified.ID != g.ID || verified.Scope != g.Scope {
		t.Fatalf("unexpected verified grant: %+v", verified)
	}

	verified, err = Verify(wire, VerifyParams{
		Role:         RoleGrantor,
		Self:         vecIss,
		Counterparty: vecAud,
		Now:          now,
		SessionOpen:  sessionOpen,
	})
	if err != nil {
		t.Fatalf("Verify (grantor): %v", err)
	}
	if verified.Resource.Branch != "feat-x" {
		t.Fatalf("unexpected verified grant: %+v", verified)
	}
}
