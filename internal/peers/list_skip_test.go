package peers

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

type listAudit struct{ peers []string }

func (a *listAudit) Append(_ context.Context, _, action string, detail any) error {
	if action == ActionListSkip {
		a.peers = append(a.peers, detail.(map[string]any)["peer"].(string))
	}
	return nil
}

// Review L6: one row with a non-canonical key must not make List fail.
func TestListSkipsAndAuditsRowWithBadKey(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	good := envelope.KeyString(pub)
	// The last character of a 43-character key carries only 4 bits; "B" has a non-zero trailing bit.
	bad := good[:42] + "B"
	if bad == good {
		bad = good[:42] + "C"
	}
	for i, k := range []string{good, bad, "short"} {
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO peers (public_key, name, harness, skills, card, paired_at)
VALUES (?, 'n', 'h', '[]', '{}', ?)`, k, "2026-01-0"+string(rune('1'+i))+"T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	s := NewStore(st.DB())
	a := &listAudit{}
	s.SetAudit(a)
	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List failed because of one bad row: %v", err)
	}
	if len(got) != 1 || got[0].PublicKey != good {
		t.Fatalf("List = %+v, want only the good peer", got)
	}
	if len(a.peers) != 2 {
		t.Errorf("audited %v, want both bad rows", a.peers)
	}
}
