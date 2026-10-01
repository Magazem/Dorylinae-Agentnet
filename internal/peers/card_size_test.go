package peers_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

// hugeCard returns a card envelope with 1,700 skills (about 480 KiB), signed
// with ed25519 directly because agentcard.New and Sign refuse it: the card of
// review 55 T10-05 that a team owner could introduce before R55-F13.
func hugeCard(t *testing.T) (json.RawMessage, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	skills := make([]agentcard.Skill, 1700)
	for i := range skills {
		skills[i] = agentcard.Skill{ID: fmt.Sprintf("s%d", i), Name: strings.Repeat("n", 120), Description: strings.Repeat("d", 128)}
	}
	if _, err := agentcard.New(pub, "big", "custom", skills, time.Now()); err == nil {
		t.Fatal("New accepted 1,700 skills")
	}
	c := agentcard.Card{Version: agentcard.Version, Name: "big", PublicKey: envelope.KeyString(pub), Harness: "custom",
		Skills: skills, Created: time.Now().UTC().Format(time.RFC3339)}
	canon, err := agentcard.Canonical(c)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, append([]byte("dorylinae-agent-card-v1\n"), canon...))
	raw, err := json.Marshal(map[string]any{"card": json.RawMessage(canon), "signature": base64.RawURLEncoding.EncodeToString(sig)})
	if err != nil {
		t.Fatal(err)
	}
	return raw, c.PublicKey
}

// R55-F13 A1 (review 55 T10-05, inverted): the 1,700-skill card, over 16 KiB,
// is refused by Verify and a pairing that carries it stores nothing.
func TestHugeCardRefusedAndNotStored(t *testing.T) {
	card, key := hugeCard(t)
	if len(card) <= agentcard.MaxCardBytes {
		t.Fatalf("precondition: card is %d bytes", len(card))
	}
	if _, err := agentcard.Verify(card); err == nil || !strings.Contains(err.Error(), "over the limit of 16384") {
		t.Fatalf("Verify: got %v, want a size refusal", err)
	}

	e := newEnv(t, 50*time.Millisecond)
	st, err := e.m.Redeem(context.Background(), "abcde-fghjk", true)
	if err != nil || st.State != peers.StatePending {
		t.Fatalf("Redeem = %+v, %v", st, err)
	}
	e.m.HandleControl(envelope.Control{Op: envelope.OpPairPeer, PublicKey: key, Card: card, Ref: st.ID})
	got, ok := e.m.Get(st.ID)
	if !ok || got.State != peers.StateFailed || got.Error == nil || got.Error.Code != peers.FailBadCard {
		t.Fatalf("status = %+v", got)
	}
	if n := len(e.peerList(t)); n != 0 {
		t.Fatalf("%d peers stored after an oversize card", n)
	}
	var rows int
	if err := e.db.DB().QueryRow(`SELECT COUNT(*) FROM peers WHERE public_key = ?`, key).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("peers rows for the key: %d, %v", rows, err)
	}
}
