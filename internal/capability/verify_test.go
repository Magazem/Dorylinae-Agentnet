package capability

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// vecTokenBytes returns the canonical wire token of the published vector,
// and the sig bytes for tests that need to re-sign a modified grant.
func vecTokenBytes(t *testing.T) []byte {
	t.Helper()
	return mustVecWire()
}

func vecParams(now time.Time) VerifyParams {
	return VerifyParams{
		Role:         RoleHolder,
		Self:         vecAud,
		Counterparty: vecIss,
		Now:          now,
		SessionOpen: func(id, requester, worker string) (known, open bool) {
			return id == vecSession && requester == vecIss && worker == vecAud, true
		},
	}
}

// mutateGrant re-marshals the wire token with grant["field"] set to newVal
// (or deleted, if newVal is nil), keeping the original signature: exactly
// the "holder changes a member and re-serialises" scenario of grant.md
// §Verification, widened caveat.
func mutateGrant(t *testing.T, wire []byte, field string, newVal any) []byte {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(wire, &env); err != nil {
		t.Fatal(err)
	}
	grant, ok := env["grant"].(map[string]any)
	if !ok {
		t.Fatal("no grant object")
	}
	if newVal == nil {
		delete(grant, field)
	} else {
		grant[field] = newVal
	}
	gen, err := agentcard.ParseStrict(mustMarshal(t, grant))
	if err != nil {
		t.Fatal(err)
	}
	out, err := agentcard.CanonicalValue(map[string]any{"grant": gen, "sig": env["sig"]})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var vecNow = time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)

func TestNegativeWidenedExpiry(t *testing.T) {
	wire := mutateGrant(t, vecTokenBytes(t), "exp", "2026-01-03T05:00:00Z")
	_, err := Verify(wire, vecParams(vecNow))
	assertReject(t, err, 4, ReasonBadSignature)
}

func TestNegativeScopeRemoved(t *testing.T) {
	wire := mutateGrant(t, vecTokenBytes(t), "scope", nil)
	_, err := Verify(wire, vecParams(vecNow))
	assertReject(t, err, 4, ReasonBadSignature)
}

func TestNegativeWrongAudience(t *testing.T) {
	other := seed(0x40)
	p := vecParams(vecNow)
	p.Self = base64ify(t, other.Public().(ed25519.PublicKey))
	_, err := Verify(vecTokenBytes(t), p)
	assertReject(t, err, 5, ReasonWrongAudience)
}

func TestNegativeExpired(t *testing.T) {
	now := time.Date(2026, 1, 2, 5, 0, 0, 0, time.UTC) // == exp
	_, err := Verify(vecTokenBytes(t), vecParams(now))
	assertReject(t, err, 6, ReasonExpired)
}

func TestNegativeExtraMember(t *testing.T) {
	wire := mutateGrant(t, vecTokenBytes(t), "write", true)
	_, err := Verify(wire, vecParams(vecNow))
	assertReject(t, err, 1, ReasonMalformed)
}

func TestNegativeSensitiveWrongType(t *testing.T) {
	wire := mutateGrant(t, vecTokenBytes(t), "sensitive", 1)
	_, err := Verify(wire, vecParams(vecNow))
	assertReject(t, err, 1, ReasonMalformed)
}

func TestNegativeUnknownSession(t *testing.T) {
	p := vecParams(vecNow)
	p.SessionOpen = func(string, string, string) (bool, bool) { return false, false }
	_, err := Verify(vecTokenBytes(t), p)
	assertReject(t, err, 7, ReasonUnknownSession)
}

func TestNegativeSessionNotOpen(t *testing.T) {
	p := vecParams(vecNow)
	p.SessionOpen = func(string, string, string) (bool, bool) { return true, false }
	_, err := Verify(vecTokenBytes(t), p)
	assertReject(t, err, 7, ReasonSessionNotOpen)
}

func TestNegativeWrongIssuer(t *testing.T) {
	p := vecParams(vecNow)
	p.Counterparty = base64ify(t, seed(0x40).Public().(ed25519.PublicKey))
	_, err := Verify(vecTokenBytes(t), p)
	assertReject(t, err, 3, ReasonWrongIssuer)
}

func TestNegativeActionResourceMismatch(t *testing.T) {
	// A grantor issues fs.read over a git resource: valid shape, valid
	// enums individually, but action does not match resource.kind. Sign a
	// fresh, self-consistent-looking (but cross-inconsistent) grant so the
	// signature still verifies and only step 8 catches it.
	g := vecGrant()
	g.Action = ActionFSRead // resource.kind stays "git"
	// checkFormats would also reject this (git kind requires a branch,
	// which is fine here; kind/action mismatch is not itself a §Grant
	// object format rule), so Sign succeeds and Verify must catch it at 8.
	tok, err := Sign(seed(0x00), g)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wire, err := Canonical(tok)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(wire, vecParams(vecNow))
	assertReject(t, err, 8, ReasonMalformed)
}

func assertReject(t *testing.T, err error, step int, reason string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var ve *VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("not a *VerifyError: %v", err)
	}
	if ve.Step != step || ve.Reason != reason {
		t.Fatalf("got step %d reason %s, want step %d reason %s (%v)", ve.Step, ve.Reason, step, reason, err)
	}
	if ReasonOf(err) != reason {
		t.Fatalf("ReasonOf: got %s want %s", ReasonOf(err), reason)
	}
}

func base64ify(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	return b64u.EncodeToString(pub)
}

// TestFuzzNeverAcceptsMutation is the ticket 2.2b acceptance fuzz test:
// Verify never accepts any single-byte mutation of a valid token's canonical
// bytes.
func TestFuzzNeverAcceptsMutation(t *testing.T) {
	wire := vecTokenBytes(t)
	accepted := 0
	for i := range wire {
		for _, delta := range []byte{1, 0x40, 0x80} {
			mut := append([]byte(nil), wire...)
			mut[i] ^= delta
			if !bytes.Equal(mut, wire) {
				if _, err := Verify(mut, vecParams(vecNow)); err == nil {
					accepted++
				}
			}
		}
	}
	if accepted != 0 {
		t.Fatalf("Verify accepted %d mutated tokens", accepted)
	}
}

func FuzzVerifyNeverAcceptsMutation(f *testing.F) {
	wire := mustVecWire()
	f.Add(wire)
	f.Fuzz(func(t *testing.T, data []byte) {
		if bytes.Equal(data, wire) {
			return // the valid token itself must still verify
		}
		if _, err := Verify(data, vecParams(vecNow)); err == nil {
			t.Fatalf("Verify accepted a mutated token: %x", data)
		}
	})
}

// mustVecWire is vecTokenBytes without a *testing.T, for the fuzz seed corpus.
func mustVecWire() []byte {
	tok, err := Sign(seed(0x00), vecGrant())
	if err != nil {
		panic(err)
	}
	wire, err := Canonical(tok)
	if err != nil {
		panic(err)
	}
	return wire
}
