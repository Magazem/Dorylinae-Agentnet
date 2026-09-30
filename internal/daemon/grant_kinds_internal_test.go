package daemon

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Review 55 R55-065 (C09-01): the holder checks the grant token in its
// canonical form. A valid token whose scope holds many '&' is within
// MaxTokenBytes canonically but not after json.Marshal's HTML escaping; it
// must still reach the session check (here: an orphan, the session is
// unknown), not be refused as a bad body.
func TestGrantKindCanonicalToken(t *testing.T) {
	priv := ed25519.NewKeyFromSeed(make([]byte, 32))
	hseed := make([]byte, 32)
	hseed[0] = 1
	hpriv := ed25519.NewKeyFromSeed(hseed)
	enc := base64.RawURLEncoding
	iss := enc.EncodeToString(priv.Public().(ed25519.PublicKey))
	aud := enc.EncodeToString(hpriv.Public().(ed25519.PublicKey))
	nbf := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	g := capability.Grant{V: 1, ID: "g-00112233445566778899aabbccddeeff", Iss: iss, Aud: aud,
		Session: "s-36375782ceb6baea9cee4d4273dfb035", Action: capability.ActionFSRead,
		Resource: capability.Resource{Kind: capability.KindFS, Label: "docs-3f2a"},
		Scope:    "R&D/" + strings.Repeat("a&b", 250), Nbf: nbf, Exp: nbf.Add(2 * time.Hour), Sensitive: true}
	tok, err := capability.Sign(priv, g)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wire, err := capability.Canonical(tok)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := agentcard.ParseStrict(wire)
	if err != nil {
		t.Fatal(err)
	}
	if escaped, _ := json.Marshal(gen); len(wire) > capability.MaxTokenBytes || len(escaped) <= capability.MaxTokenBytes {
		t.Fatalf("fixture: canonical %d, json.Marshal %d bytes; want within and over %d", len(wire), len(escaped), capability.MaxTokenBytes)
	}

	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	k := grantKind(&capability.Store{DB: st.DB()}, &worksession.Store{DB: st.DB(), Self: aud}, aud, nil)
	op := &mail.Opened{Msg: mail.Msg{V: 1, ID: mail.NewID(), From: iss, To: aud, Kind: "grant",
		Body: map[string]any{"token": gen}}}
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := k.Apply(ctx, tx, op); err != nil {
		if errors.Is(err, mail.ErrBadBody) {
			t.Fatalf("a valid token was refused as a bad body: %v", err)
		}
		t.Fatal(err)
	}
	out, ok := op.Outcome.(*grantOutcome)
	if !ok || out.kind != "orphan" || out.grant != g.ID {
		t.Fatalf("outcome = %+v, want an orphan of %s", op.Outcome, g.ID)
	}
}
