package presence

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func newSettingsFixture(t *testing.T) *Settings {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewSettings(st.DB())
}

// TestSettingsModeDefaultsVisible checks the unset default and a round trip
// for all three modes, Docs/protocol/presence.md §Visibility.
func TestSettingsModeDefaultsVisible(t *testing.T) {
	ctx := context.Background()
	s := newSettingsFixture(t)

	got, err := s.GetMode(ctx)
	if err != nil || got.Mode != ModeVisible || got.Team != "" {
		t.Fatalf("default mode = %+v, err = %v", got, err)
	}

	for _, want := range []VisibilityMode{
		{Mode: ModeInvisible},
		{Mode: ModeOnlyTeam, Team: "t-0123456789abcdef0123456789abcdef"},
		{Mode: ModeVisible},
	} {
		if err := s.SetMode(ctx, want, testNow); err != nil {
			t.Fatalf("SetMode(%+v): %v", want, err)
		}
		got, err := s.GetMode(ctx)
		if err != nil || got != want {
			t.Fatalf("GetMode after SetMode(%+v) = %+v, err = %v", want, got, err)
		}
	}
}

// TestSettingsHumanShareDefaultsTrue checks the unset default and a round
// trip, Docs/protocol/presence.md §Human sharing.
func TestSettingsHumanShareDefaultsTrue(t *testing.T) {
	ctx := context.Background()
	s := newSettingsFixture(t)

	share, err := s.GetHumanShare(ctx)
	if err != nil || !share {
		t.Fatalf("default human share = %v, err = %v", share, err)
	}

	if err := s.SetHumanShare(ctx, false, testNow); err != nil {
		t.Fatal(err)
	}
	if share, err := s.GetHumanShare(ctx); err != nil || share {
		t.Fatalf("after SetHumanShare(false) = %v, err = %v", share, err)
	}

	if err := s.SetHumanShare(ctx, true, testNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if share, err := s.GetHumanShare(ctx); err != nil || !share {
		t.Fatalf("after SetHumanShare(true) = %v, err = %v", share, err)
	}
}
