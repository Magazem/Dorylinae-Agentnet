package peers_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// n18Key is the public key of agent-card.md N18 (seed 00..1f).
const n18Key = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"

// R55-F10 A13 (peers): a stored row holding N18 (a bidi control in the
// name, allowed before R55-F10) is kept, rewritten only to its canonical
// form, and reported as legacy, not as bad.
func TestMigrateCardsKeepsLegacyTextCard(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := peers.NewStore(st.DB())
	n18 := vectorEnvelope(t, "N18")
	plant(t, s, n18Key, n18, peers.TrustCode)

	for run := 0; run < 2; run++ { // idempotent
		bad, legacy, err := s.MigrateCards(ctx)
		if err != nil || len(bad) != 0 || len(legacy) != 1 || legacy[0] != n18Key {
			t.Fatalf("run %d: bad %+v, legacy %v, %v", run, bad, legacy, err)
		}
		rows := allRows(t, st)
		if got, want := rows[n18Key].card, storedForm(t, []byte(n18)); got != want {
			t.Fatalf("run %d: card = %s\nwant %s", run, got, want)
		}
		if rows[n18Key].trust != peers.TrustCode {
			t.Fatalf("run %d: trust = %s", run, rows[n18Key].trust)
		}
	}
	bad, legacy, err := peers.CheckStoredCards(ctx, st.DB())
	if err != nil || len(bad) != 0 || len(legacy) != 1 || legacy[0] != n18Key {
		t.Fatalf("CheckStoredCards = %+v, %v, %v", bad, legacy, err)
	}
}

// legacyCardFor re-signs id's card with U+202E in the name: Verify refuses
// it, VerifyStored accepts it.
func legacyCardFor(t *testing.T, id *ident) json.RawMessage {
	t.Helper()
	sc, err := agentcard.Verify(id.card)
	if err != nil {
		t.Fatal(err)
	}
	c := sc.Card
	c.Name = "Ada " + string(rune(0x202E)) + "tset"
	canon, err := agentcard.Canonical(c)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(id.priv, append([]byte("dorylinae-agent-card-v1\n"), canon...))
	raw, err := json.Marshal(map[string]any{"card": json.RawMessage(canon), "signature": base64.RawURLEncoding.EncodeToString(sig)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentcard.VerifyStored(raw); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	return raw
}

// R55-F10 A15: pairing v1 with N18's card fails as bad_card and stores no
// row; so does pairing v2 with a card whose name holds U+202E.
func TestPairingRefusesLegacyTextCard(t *testing.T) {
	t.Run("v1", func(t *testing.T) {
		e := newEnv(t, 50*time.Millisecond)
		st, err := e.m.Redeem(context.Background(), "abcde-fghjk", true)
		if err != nil || st.State != peers.StatePending {
			t.Fatalf("Redeem = %+v, %v", st, err)
		}
		e.m.HandleControl(envelope.Control{Op: envelope.OpPairPeer, PublicKey: n18Key, Card: json.RawMessage(vectorEnvelope(t, "N18")), Ref: st.ID})
		got, ok := e.m.Get(st.ID)
		if !ok || got.State != peers.StateFailed || got.Error == nil || got.Error.Code != peers.FailBadCard ||
			!strings.Contains(got.Error.Message, "name contains a bidi control or line separator") {
			t.Fatalf("status = %+v", got)
		}
		if n := len(e.peerList(t)); n != 0 {
			t.Fatalf("%d peers stored", n)
		}
	})
	t.Run("v2", func(t *testing.T) {
		p := newIdent(t, "p")
		e := newEnv(t, 50*time.Millisecond)
		id, _ := startFake(t, e)
		e.m.HandleControl(envelope.Control{Op: envelope.OpPairPeer, PublicKey: p.key, Card: legacyCardFor(t, p), Mbox: p.mbox, Ref: id})
		waitFor(t, "attempt_fail", func() bool { return countActions(t, e, peers.ActionPairAttemptFail) == 1 })
		if a := strings.Join(actionsOf(t, e), "; "); !strings.Contains(a, `"code":"bad_card"`) {
			t.Errorf("audit = %s, want bad_card", a)
		}
		var rows int
		if err := e.db.DB().QueryRow(`SELECT COUNT(*) FROM peers WHERE public_key = ?`, p.key).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("peers rows for the key: %d, %v", rows, err)
		}
	})
}
