package capability

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"
)

// Tests for the fixes of Docs/review/25-2.2b-review.md.

// M1: a caller that forgets to wire SessionOpen must fail closed at step 7.
func TestNilSessionOpenFailsClosed(t *testing.T) {
	p := vecParams(vecNow)
	p.SessionOpen = nil
	_, err := Verify(mustVecWire(), p)
	assertReject(t, err, 7, ReasonUnknownSession)
}

// M2: encoding/base64 skips '\r' and '\n', so a sig with an escaped newline
// decoded to the same 64 bytes and a second wire form of the token verified.
func TestSigWithNewlineRejected(t *testing.T) {
	wire := string(mustVecWire())
	i := strings.Index(wire, `"sig":"`) + len(`"sig":"`) + 10
	for _, ins := range []string{`\n`, `\r`, `\r\n`} {
		mut := wire[:i] + ins + wire[i:]
		_, err := Verify([]byte(mut), vecParams(vecNow))
		assertReject(t, err, 4, ReasonBadSignature)
	}
	// Padding and the standard alphabet stay rejected too.
	for _, sig := range []string{vecSig + "==", strings.ReplaceAll(vecSig, "-", "+")} {
		mut := strings.Replace(wire, vecSig, sig, 1)
		_, err := Verify([]byte(mut), vecParams(vecNow))
		assertReject(t, err, 4, ReasonBadSignature)
	}
}

// L: a Role value other than RoleHolder/RoleGrantor is not treated as holder.
func TestUnknownRoleRejected(t *testing.T) {
	p := vecParams(vecNow)
	p.Role = Role(7)
	_, err := Verify(mustVecWire(), p)
	assertReject(t, err, 3, ReasonWrongIssuer)
}

// The grantor role verifies the vector with Self = iss, Counterparty = aud.
func TestGrantorRoleVerifies(t *testing.T) {
	p := vecParams(vecNow)
	p.Role, p.Self, p.Counterparty = RoleGrantor, vecIss, vecAud
	if _, err := Verify(mustVecWire(), p); err != nil {
		t.Fatal(err)
	}
	p.Counterparty = base64ify(t, seed(0x40).Public().(ed25519.PublicKey))
	_, err := Verify(mustVecWire(), p)
	assertReject(t, err, 5, ReasonWrongAudience)
}

// L: Sign returns the grant it actually signed (UTC, whole seconds), so the
// caller's Token.Grant equals Verify's result.
func TestSignNormalisesTimes(t *testing.T) {
	g := vecGrant()
	loc := time.FixedZone("x", 5*3600)
	g.Nbf = g.Nbf.In(loc).Add(900 * time.Millisecond)
	g.Exp = g.Exp.In(loc).Add(900 * time.Millisecond)
	tok, err := Sign(seed(0x00), g)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := Canonical(tok)
	if err != nil {
		t.Fatal(err)
	}
	if string(wire) != string(mustVecWire()) {
		t.Fatalf("wire differs from the vector:\n%s", wire)
	}
	got, err := Verify(wire, vecParams(vecNow))
	if err != nil {
		t.Fatal(err)
	}
	if *got != tok.Grant {
		t.Fatalf("Sign returned %+v, Verify %+v", tok.Grant, *got)
	}

	g = vecGrant()
	g.Nbf = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	g.Exp = g.Nbf.Add(time.Hour)
	if _, err := Sign(seed(0x00), g); err == nil {
		t.Fatal("Sign accepted a year the wire format cannot carry")
	}
}

// L: Sign refuses a grant Verify would reject at step 8.
func TestSignRejectsActionKindMismatch(t *testing.T) {
	g := vecGrant()
	g.Action = ActionFSRead
	if _, err := Sign(seed(0x00), g); err == nil {
		t.Fatal("Sign accepted fs.read on a git resource")
	}
}

// L: the branch must be a valid refs/heads/<branch> name (grant.md).
func TestBranchRefRules(t *testing.T) {
	for _, b := range []string{"feat-x", "feat/x", "v1.2", "a@b"} {
		if err := checkBranch(b); err != nil {
			t.Errorf("%q rejected: %v", b, err)
		}
	}
	for _, b := range []string{"@", "a@{1}", "-x", "x.", "/x", "x/", "a//b", ".x", "a/.b", "x.lock", "a/b.lock", "a..b", "a b", "a~1", "a\x7fb"} {
		if err := checkBranch(b); err == nil {
			t.Errorf("%q accepted", b)
		}
	}
}

// L: Windows treats "CON .txt" as the CON device.
func TestReservedNameWithTrailingSpace(t *testing.T) {
	for _, p := range []string{"a/CON .txt", "nul  .x", "Com1 .log", "LPT9"} {
		if ValidScopePath(p) {
			t.Errorf("%q accepted", p)
		}
	}
	for _, p := range []string{"a/CONX.txt", "console", "com10", "a/b c.txt"} {
		if !ValidScopePath(p) {
			t.Errorf("%q rejected", p)
		}
	}
}
