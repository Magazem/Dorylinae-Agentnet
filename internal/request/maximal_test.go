package request

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// TestMaximalRequestSealsInOneMail builds the maximal valid request (exactly
// MaxRequestBody, 65536, bytes of canonical(request)) and confirms it seals
// into one mail under mail.MaxMailPlaintext (Docs/protocol/request.md
// §Size limits: "a valid request always fits in one mail with a wide margin").
func TestMaximalRequestSealsInOneMail(t *testing.T) {
	r := nearMaxRequest()
	lo, hi := 1, maxBriefBytes
	canonLen := func(briefLen int) int {
		r.Brief = strings.Repeat("a", briefLen)
		if err := Validate(r); err != nil {
			t.Fatalf("Validate: %v", err)
		}
		canon, err := Canonical(r)
		if err != nil {
			t.Fatalf("Canonical: %v", err)
		}
		return len(canon)
	}
	for lo < hi {
		mid := (lo + hi) / 2
		if canonLen(mid) < MaxRequestBody {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	r.Brief = strings.Repeat("a", lo)
	if err := Validate(r); err != nil {
		t.Fatalf("Validate(maximal): %v", err)
	}
	canon, err := Canonical(r)
	if err != nil {
		t.Fatalf("Canonical(maximal): %v", err)
	}
	if len(canon) != MaxRequestBody {
		t.Fatalf("canonical(request) = %d bytes, want exactly %d", len(canon), MaxRequestBody)
	}

	// Wrap as the wire body {"request": <object>} and seal as kind "request".
	obj, err := agentcard.ParseStrict(canon)
	if err != nil {
		t.Fatalf("ParseStrict(canonical): %v", err)
	}
	body := map[string]any{"request": obj}

	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	mboxSeed := make([]byte, 32)
	for i := range mboxSeed {
		mboxSeed[i] = byte(i + 1)
	}
	mbox, err := ecdh.X25519().NewPrivateKey(mboxSeed)
	if err != nil {
		t.Fatalf("mailbox key: %v", err)
	}

	sealed, err := mail.Seal(mail.SealInput{
		Priv:       priv,
		To:         r.To,
		MailboxPub: mbox.PublicKey().Bytes(),
		Kind:       "request",
		Body:       body,
		Created:    r.Created,
	})
	if err != nil {
		t.Fatalf("mail.Seal(maximal request): %v", err)
	}
	if len(sealed.Signed) > mail.MaxMailPlaintext {
		t.Fatalf("signed plaintext = %d bytes, over MaxMailPlaintext %d", len(sealed.Signed), mail.MaxMailPlaintext)
	}
	if len(sealed.Payload) > mail.MaxPayload {
		t.Fatalf("payload = %d bytes, over MaxPayload %d", len(sealed.Payload), mail.MaxPayload)
	}
	t.Logf("maximal request: canonical(request)=%d, signed plaintext=%d (limit %d), payload=%d (limit %d)",
		len(canon), len(sealed.Signed), mail.MaxMailPlaintext, len(sealed.Payload), mail.MaxPayload)
}
