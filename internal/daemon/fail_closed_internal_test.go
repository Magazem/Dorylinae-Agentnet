package daemon

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Review 55 R55-081 (C09-02): a session read error during the holder's grant
// apply is not an orphan. Apply returns the error, so the mail is retried,
// instead of acking it and losing the grant for good.
func TestGrantKindSessionReadErrorIsNotOrphan(t *testing.T) {
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
		Nbf:      nbf, Exp: nbf.Add(2 * time.Hour), Sensitive: true}
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

	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	k := grantKind(&capability.Store{DB: st.DB()}, &worksession.Store{DB: st.DB(), Self: aud}, aud, nil)
	apply := func(breakSessions bool) (*mail.Opened, error) {
		t.Helper()
		tx, err := st.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if breakSessions {
			// Any read of work_sessions now fails with a DB error, not
			// "no such session".
			if _, err := tx.ExecContext(ctx, `ALTER TABLE work_sessions RENAME TO work_sessions_gone`); err != nil {
				t.Fatal(err)
			}
		}
		op := &mail.Opened{Msg: mail.Msg{V: 1, ID: mail.NewID(), From: iss, To: aud, Kind: "grant",
			Body: map[string]any{"token": gen}}}
		return op, k.Apply(ctx, tx, op)
	}

	op, err := apply(true)
	if err == nil || errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("Apply with a failing session read = %v, want a retryable error", err)
	}
	if op.Outcome != nil {
		t.Fatalf("outcome = %+v, want none (no orphan)", op.Outcome)
	}

	// The control: a session that really is unknown is still an orphan.
	op, err = apply(false)
	if err != nil {
		t.Fatal(err)
	}
	if out, ok := op.Outcome.(*grantOutcome); !ok || out.kind != "orphan" {
		t.Fatalf("outcome = %+v, want an orphan", op.Outcome)
	}
}

// Review 55 R55-104 (C11-08): the policy path re-reads the matched policy
// inside the issuing transaction. A policy removed, or past its until, since
// Match issues nothing; a read error refuses as well.
func TestRecheckPolicyTx(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	caps := &capability.Store{DB: db}
	now := time.Now()
	insert := func(id string, until time.Time) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := caps.PolicyInsertTx(ctx, tx, capability.Policy{
			ID: id, Peer: "peer", Action: capability.ActionFSRead, Path: "/p",
			MaxExpiresS: 3600, Until: until, Approval: "a-1", Created: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	live := "p-00000000000000000000000000000001"
	ended := "p-00000000000000000000000000000002"
	insert(live, now.Add(time.Hour))
	insert(ended, now.Add(time.Second))

	check := func(id string, at time.Time, breakTable bool) error {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if breakTable {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE grant_policies RENAME TO grant_policies_gone`); err != nil {
				t.Fatal(err)
			}
		}
		return recheckPolicyTx(ctx, tx, id, at)
	}
	refused := func(name string, err error) {
		t.Helper()
		var ie *ipc.Error
		if !errors.As(err, &ie) || ie.Code != CodeBadState {
			t.Errorf("%s: err = %v, want %s", name, err, CodeBadState)
		}
	}

	if err := check(live, now, false); err != nil {
		t.Fatalf("live policy: %v", err)
	}
	refused("removed", check("p-00000000000000000000000000000003", now, false))
	refused("past until", check(ended, now.Add(2*time.Second), false))
	if err := check(live, now, true); err == nil {
		t.Error("read error: err = nil, want an error")
	}
}
