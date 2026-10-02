package capability

import (
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// hiddenBranches hold a rune of displaytext.Hidden (grant.md §Grant fields,
// R55-F10 OD-F10-5): RLO, a zero-width space, C1 NEL, VS16 and an
// ideographic space.
var hiddenBranches = []string{"main\u202E", "ma\u200Bin", "x\u0085", "x\uFE0F", "x\u3000"}

// R55-F10 A16: checkBranch refuses every hidden rune and keeps ordinary
// Unicode.
func TestBranchRefusesHiddenRunes(t *testing.T) {
	for _, b := range hiddenBranches {
		if err := checkBranch(b); err == nil {
			t.Errorf("%+q accepted", b)
		}
		g := vecGrant()
		g.Resource.Branch = b
		if _, err := Sign(seed(0x00), g); err == nil {
			t.Errorf("Sign accepted branch %+q", b)
		}
	}
	for _, b := range []string{"feature/ü", "日本"} {
		if err := checkBranch(b); err != nil {
			t.Errorf("%+q refused: %v", b, err)
		}
	}
}

// A16: a token whose branch holds a hidden rune is malformed at step 2, on
// the holder when received and on the grantor when presented; a stored
// record with such a branch is not_found when served.
func TestTokenWithHiddenBranchIsMalformed(t *testing.T) {
	for _, b := range hiddenBranches {
		g := vecGrant()
		g.Resource.Branch = b
		// Sign refuses it, so sign by hand as an older grantor would.
		canon, err := agentcard.CanonicalValue(grantMap(g))
		if err != nil {
			t.Fatal(err)
		}
		sig := ed25519.Sign(seed(0x00), append([]byte(domain), canon...))
		wire, err := Canonical(Token{Grant: g, Sig: b64u.EncodeToString(sig)})
		if err != nil {
			t.Fatal(err)
		}
		_, err = Verify(wire, vecParams(vecNow))
		assertReject(t, err, 2, ReasonMalformed)
		grantor := vecParams(vecNow)
		grantor.Role, grantor.Self, grantor.Counterparty = RoleGrantor, vecIss, vecAud
		_, err = Verify(wire, grantor)
		assertReject(t, err, 2, ReasonMalformed)

		repo, err := filepath.Abs(testutil.TempDir(t))
		if err != nil {
			t.Fatal(err)
		}
		var fe *FetchError
		err = GitBackend{Git: "git"}.check(Record{Path: repo, Branch: b})
		if !errors.As(err, &fe) || fe.Code != CodeNotFound {
			t.Errorf("check(%+q) = %v, want not_found", b, err)
		}
	}
}
