package presence

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// R55-108: human_present is false, not null, for a peer whose daemon is
// offline (a goodbye, a lapsed interval) or that was never heard;
// Docs/protocol/presence.md §Receiving, "Effective state of a peer".
func TestViewHumanPresentFalseWhenOffline(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ps := NewStore(st.DB())
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rx := now.Format(storeTimeFmt)
	insert := func(key, state string, human int) {
		t.Helper()
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO presence_peers (key, boot, seq, created, state, agent, human, interval, last_rx) VALUES (?, 'b', 1, ?, ?, 0, ?, 30, ?)`,
			key, rx, state, human, rx); err != nil {
			t.Fatal(err)
		}
	}
	insert("gone", "offline", 2)
	insert("online-unknown", "online", 2)
	insert("online-human", "online", 1)

	check := func(key string, want *bool) {
		t.Helper()
		v, err := ps.View(ctx, key, now)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case want == nil && v.HumanPresent != nil:
			t.Errorf("%s: HumanPresent = %v, want nil", key, *v.HumanPresent)
		case want != nil && (v.HumanPresent == nil || *v.HumanPresent != *want):
			t.Errorf("%s: HumanPresent = %v, want %v", key, v.HumanPresent, *want)
		}
	}
	f, tr := false, true
	check("gone", &f)
	check("never-heard", &f)
	check("online-unknown", nil)
	check("online-human", &tr)
}
