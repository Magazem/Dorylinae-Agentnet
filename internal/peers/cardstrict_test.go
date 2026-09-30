package peers_test

import (
	"context"
	"encoding/json"
	"os"
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

// esc returns the six-character JSON escape of the code unit hex.
func esc(hex string) string { return string(rune(0x5C)) + "u" + hex }

// storedForm is the canonical {card, signature} of a card envelope.
func storedForm(t *testing.T, raw []byte) string {
	t.Helper()
	b, err := agentcard.StoredForm(raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func cardColumn(t *testing.T, st *store.Store, key string) string {
	t.Helper()
	var card string
	if err := st.DB().QueryRow(`SELECT card FROM peers WHERE public_key = ?`, key).Scan(&card); err != nil {
		t.Fatal(err)
	}
	return card
}

// Review 68 A10: a v1 pairing whose pair_peer card carries an extra
// top-level member stores the canonical {card, signature}, not the relay's
// bytes (R55-073).
func TestV1PairingStoresCanonicalCard(t *testing.T) {
	e := newEnv(t, 50*time.Millisecond)
	card, key := signedCard(t, "good")
	withExtra := strings.Replace(string(card), `{"card"`, `{"relay_note":"added by the relay","card"`, 1)
	if withExtra == string(card) {
		t.Fatal("mutation did not apply")
	}
	st, err := e.m.Redeem(context.Background(), "abcde-fghjk", true)
	if err != nil {
		t.Fatal(err)
	}
	e.m.HandleControl(envelope.Control{Op: envelope.OpPairPeer, PublicKey: key, Card: json.RawMessage(withExtra), Ref: st.ID})
	if got, _ := e.m.Get(st.ID); got.State != peers.StateComplete {
		t.Fatalf("status = %+v", got)
	}
	got := cardColumn(t, e.db, key)
	if want := storedForm(t, card); got != want {
		t.Fatalf("peers.card = %s\nwant %s", got, want)
	}
	if strings.Contains(got, "relay_note") {
		t.Fatal("the relay's member was stored")
	}
}

// vectorEnvelope returns an agent-card.md vector from the transcription in
// tools/verifyvectors/vectors.json.
func vectorEnvelope(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tools", "verifyvectors", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		AgentCard struct {
			Cases []struct {
				Name     string `json:"name"`
				Envelope string `json:"envelope"`
			} `json:"cases"`
		} `json:"agent_card"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, c := range v.AgentCard.Cases {
		if c.Name == name {
			return c.Envelope
		}
	}
	t.Fatalf("no vector %s", name)
	return ""
}

type peerRow struct {
	key, name, harness, skills, card, pairedAt, trust, mbox string
	introducedBy                                            *string
}

func allRows(t *testing.T, st *store.Store) map[string]peerRow {
	t.Helper()
	rows, err := st.DB().Query(`SELECT public_key, name, harness, skills, card, paired_at, trust, mailbox_keys, introduced_by FROM peers`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]peerRow{}
	for rows.Next() {
		var r peerRow
		if err := rows.Scan(&r.key, &r.name, &r.harness, &r.skills, &r.card, &r.pairedAt, &r.trust, &r.mbox, &r.introducedBy); err != nil {
			t.Fatal(err)
		}
		out[r.key] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// plant stores raw as the card of a peer, exactly as an older daemon did.
func plant(t *testing.T, s *peers.Store, key, raw string, trust string) {
	t.Helper()
	sc := &agentcard.Signed{Card: agentcard.Card{Version: 1, Name: "n-" + key[:6], PublicKey: key, Harness: "h", Skills: []agentcard.Skill{}}}
	if err := s.AddTrusted(context.Background(), sc, []byte(raw), time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), trust, nil); err != nil {
		t.Fatal(err)
	}
}

// Review 68 A12: the stored-card migration (OD-3).
func TestMigrateCards(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := peers.NewStore(st.DB())

	// A v1 row with extra top-level members, stored as received.
	extraCard, extraKey := signedCard(t, "extra")
	extraRaw := strings.Replace(string(extraCard), `{"card"`, `{"ok":true,"key_backend":"file","card"`, 1)
	plant(t, s, extraKey, extraRaw, peers.TrustRelay)
	// A v1 row whose relay added a member holding a lone surrogate escape
	// (N6's note), and one holding a fraction (N15's).
	surCard, surKey := signedCard(t, "surrogate")
	surRaw := strings.Replace(string(surCard), `{"card"`, `{"note":"`+esc("d800")+`","card"`, 1)
	plant(t, s, surKey, surRaw, peers.TrustRelay)
	fracCard, fracKey := signedCard(t, "fraction")
	fracRaw := strings.Replace(string(fracCard), `{"card"`, `{"note":1.5,"card"`, 1)
	plant(t, s, fracKey, fracRaw, peers.TrustRelay)
	// An already canonical v2 row.
	okCard, okKey := signedCard(t, "canonical")
	okRaw := storedForm(t, okCard)
	plant(t, s, okKey, okRaw, peers.TrustCode)
	// A folded card (N1): signed by the vector key, carrying public_Key.
	n1 := vectorEnvelope(t, "N1")
	const n1Key = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	plant(t, s, n1Key, n1, peers.TrustFingerprint)
	if _, err := agentcard.Verify([]byte(surRaw)); err == nil {
		t.Fatal("precondition: the surrogate row must fail the new Verify")
	}

	before := allRows(t, st)
	bad, err := s.MigrateCards(ctx)
	if err != nil {
		t.Fatalf("MigrateCards: %v", err)
	}
	if len(bad) != 1 || bad[0].PublicKey != n1Key || bad[0].Fingerprint == "" || bad[0].Reason == "" {
		t.Fatalf("bad = %+v, want only the N1 row", bad)
	}
	after := allRows(t, st)
	if len(after) != 5 {
		t.Fatalf("%d rows after the migration, want 5", len(after))
	}
	for key, raw := range map[string]string{extraKey: string(extraCard), surKey: string(surCard), fracKey: string(fracCard), okKey: okRaw} {
		if got, want := after[key].card, storedForm(t, []byte(raw)); got != want {
			t.Errorf("row %s: card = %s\nwant %s", key[:8], got, want)
		}
		if _, err := agentcard.Verify([]byte(after[key].card)); err != nil {
			t.Errorf("row %s: rewritten card does not verify: %v", key[:8], err)
		}
	}
	for key, b := range before {
		a := after[key]
		a.card, b.card = "", ""
		if !sameRow(a, b) {
			t.Errorf("row %s: other columns changed: %+v -> %+v", key[:8], b, a)
		}
	}
	if after[n1Key].card != n1 {
		t.Errorf("the N1 row was rewritten: %s", after[n1Key].card)
	}
	if after[n1Key].trust != peers.TrustFingerprint {
		t.Errorf("the N1 row was downgraded to %s", after[n1Key].trust)
	}

	// Idempotent: a second run changes nothing and reports the same row.
	bad2, err := s.MigrateCards(ctx)
	if err != nil || len(bad2) != 1 || bad2[0].PublicKey != n1Key {
		t.Fatalf("second run: %+v, %v", bad2, err)
	}
	again := allRows(t, st)
	for key, r := range after {
		if !sameRow(again[key], r) {
			t.Errorf("row %s changed on the second run", key[:8])
		}
	}

	// The read-only check (agentnet doctor) agrees.
	chk, err := peers.CheckStoredCards(ctx, st.DB())
	if err != nil || len(chk) != 1 || chk[0].PublicKey != n1Key {
		t.Fatalf("CheckStoredCards = %+v, %v", chk, err)
	}
}

// sameRow compares two rows column by column (introduced_by by value).
func sameRow(a, b peerRow) bool {
	if (a.introducedBy == nil) != (b.introducedBy == nil) ||
		(a.introducedBy != nil && *a.introducedBy != *b.introducedBy) {
		return false
	}
	a.introducedBy, b.introducedBy = nil, nil
	return a == b
}
