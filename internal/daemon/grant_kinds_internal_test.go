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

// A15 (R55-F13, review 55 R55-064, review 71b F6): per session a holder keeps
// at most 64 live held grants and 512 in total. The 65th live grant is
// refused as a limit (mail.ErrLimit: no row), a redelivered or conflicting
// grant is not refused, another session is not affected, and once the grants
// expire new ones are stored up to 512 in total.
func TestGrantKindHeldCaps(t *testing.T) {
	priv := ed25519.NewKeyFromSeed(make([]byte, 32))
	hseed := make([]byte, 32)
	hseed[0] = 1
	hpriv := ed25519.NewKeyFromSeed(hseed)
	enc := base64.RawURLEncoding
	iss := enc.EncodeToString(priv.Public().(ed25519.PublicKey))
	aud := enc.EncodeToString(hpriv.Public().(ed25519.PublicKey))

	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	caps := &capability.Store{DB: db}
	k := grantKind(caps, &worksession.Store{DB: db, Self: aud}, aud, nil)
	session := func(req string) string {
		sid := worksession.DeriveID(iss, aud, req)
		if _, err := db.Exec(`INSERT INTO work_sessions (id, role, peer, request_id, team_id, state, opened, state_at, updated)
			VALUES (?, 'worker', ?, ?, 't', 'open', 'o', 's', 'u')`, sid, iss, req); err != nil {
			t.Fatal(err)
		}
		return sid
	}
	sidA := session("r-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	sidB := session("r-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	nbf := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	mint := func(sid, id string, ttl time.Duration) any {
		g := capability.Grant{V: 1, ID: id, Iss: iss, Aud: aud, Session: sid, Action: capability.ActionFSRead,
			Resource: capability.Resource{Kind: capability.KindFS, Label: "docs-3f2a"}, Nbf: nbf, Exp: nbf.Add(ttl), Sensitive: true}
		tok, err := capability.Sign(priv, g)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := capability.Canonical(tok)
		if err != nil {
			t.Fatal(err)
		}
		gen, err := agentcard.ParseStrict(wire)
		if err != nil {
			t.Fatal(err)
		}
		return gen
	}
	apply := func(tok any) (*mail.Opened, error) {
		op := &mail.Opened{Msg: mail.Msg{V: 1, ID: mail.NewID(), From: iss, To: aud, Kind: "grant",
			Body: map[string]any{"token": tok}}}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := k.Apply(ctx, tx, op); err != nil {
			_ = tx.Rollback()
			return op, err
		}
		return op, tx.Commit()
	}
	held := func(sid string) int {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM grants WHERE direction = 'held' AND session = ?`, sid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	expireAll := func(sid string) {
		past := time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := db.Exec(`UPDATE grants SET exp = ? WHERE direction = 'held' AND session = ?`, past, sid); err != nil {
			t.Fatal(err)
		}
	}

	firstID := capability.NewID()
	first := mint(sidA, firstID, 2*time.Hour)
	if _, err := apply(first); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < capability.MaxHeldLivePerSession; i++ {
		if _, err := apply(mint(sidA, capability.NewID(), 2*time.Hour)); err != nil {
			t.Fatalf("grant %d: %v", i+1, err)
		}
	}
	refusedID := capability.NewID()
	if _, err := apply(mint(sidA, refusedID, 2*time.Hour)); !errors.Is(err, mail.ErrLimit) {
		t.Fatalf("65th live grant: err = %v, want mail.ErrLimit", err)
	}
	if _, err := caps.Get(ctx, refusedID); !errors.Is(err, capability.ErrUnknownGrant) || held(sidA) != 64 {
		t.Fatalf("refused grant: Get err = %v, %d held rows; want no row and 64", err, held(sidA))
	}
	// Duplicate and conflict come first: neither is refused at the cap.
	if op, err := apply(first); err != nil || op.Outcome != nil {
		t.Fatalf("redelivered grant at the cap: %v, outcome %+v", err, op.Outcome)
	}
	if op, err := apply(mint(sidA, firstID, time.Hour)); err != nil {
		t.Fatalf("conflicting grant at the cap: %v", err)
	} else if out, ok := op.Outcome.(*grantOutcome); !ok || out.kind != "conflict" {
		t.Fatalf("conflicting grant: outcome %+v, want conflict", op.Outcome)
	}
	if _, err := apply(mint(sidB, capability.NewID(), 2*time.Hour)); err != nil || held(sidB) != 1 {
		t.Fatalf("grant in another session: %v, %d held rows", err, held(sidB))
	}

	// Expired grants do not count against the live cap, only the total.
	for held(sidA) < capability.MaxHeldTotalPerSession {
		expireAll(sidA)
		if _, err := apply(mint(sidA, capability.NewID(), 2*time.Hour)); err != nil {
			t.Fatalf("grant %d after expiry: %v", held(sidA)+1, err)
		}
	}
	expireAll(sidA)
	if _, err := apply(mint(sidA, capability.NewID(), 2*time.Hour)); !errors.Is(err, mail.ErrLimit) {
		t.Fatalf("513th grant: err = %v, want mail.ErrLimit", err)
	}
	if held(sidA) != capability.MaxHeldTotalPerSession {
		t.Fatalf("%d held rows, want %d", held(sidA), capability.MaxHeldTotalPerSession)
	}
}
