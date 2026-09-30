package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// R55-F13 A4: the session path's IsPaired is a one-row lookup, not
// peers.List. With a row whose skills column does not decode as a list, List fails
// for the whole table, while IsPaired still answers for that key and others.
func TestSessionIsPairedDoesNotList(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	ps := peers.NewStore(db)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c, err := agentcard.New(pub, "p", "custom", nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sc, err := agentcard.Sign(priv, c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Add(ctx, &sc, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE peers SET skills = '{}' WHERE public_key = ?`, sc.Card.PublicKey); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.List(ctx); err == nil {
		t.Fatal("precondition: peers.List must fail on the broken row")
	}

	if ok, err := isPairedCtx(ctx, db, sc.Card.PublicKey); err != nil || !ok {
		t.Fatalf("paired key: %v, %v", ok, err)
	}
	if ok, err := isPairedCtx(ctx, db, "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"); err != nil || ok {
		t.Fatalf("unknown key: %v, %v", ok, err)
	}
	_ = st.Close()
	if _, err := isPairedCtx(ctx, db, sc.Card.PublicKey); err == nil {
		t.Fatal("a database error must be returned, not read as not paired")
	}
}
