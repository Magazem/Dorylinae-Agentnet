package presence

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// sentEnvelopes is a fake relay connection.
type sentEnvelopes struct {
	mu   sync.Mutex
	sent []envelope.Envelope
}

func (s *sentEnvelopes) Send(_ context.Context, e envelope.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, e)
	return nil
}

func (s *sentEnvelopes) to(peer string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.sent {
		if e.To == peer {
			n++
		}
	}
	return n
}

// mailboxKeys is a MailboxLookup whose keys can be withdrawn.
type mailboxKeys struct {
	mu   sync.Mutex
	keys map[string][]byte
}

func (m *mailboxKeys) MailboxPub(peer string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[peer]
	return k, ok
}

func (m *mailboxKeys) drop(peer string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, peer)
}

func newPeerKey(t *testing.T) (string, []byte) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	x, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return envelope.KeyString(pub), x.PublicKey().Bytes()
}

// newSendingSender is a Sender on a real team store with one team and one
// member, a fake relay and a withdrawable mailbox key for the member.
func newSendingSender(t *testing.T) (*Sender, *team.Store, team.Team, string, *sentEnvelopes, *mailboxKeys) {
	t.Helper()
	clock := func() time.Time { return testNow }
	ts, _, tm := newResyncFixture(t, clock)
	member, x := newPeerKey(t)
	if _, err := ts.AddMember(context.Background(), tm.ID, member, testNow); err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	relay := &sentEnvelopes{}
	keys := &mailboxKeys{keys: map[string][]byte{member: x}}
	s := &Sender{
		Priv: func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), priv...), nil },
		Self: ts.Self, Team: ts, Peers: keys, Now: clock,
	}
	s.SetSender(relay)
	return s, ts, tm, member, relay, keys
}

// R55-106: only a team that is not found or not active degrades only_team to
// invisible; another lookup error leaves the mode alone.
func TestCheckTeamGoneKeepsModeOnLookupError(t *testing.T) {
	s, _, tm, _, _, _ := newSendingSender(t)
	if err := s.SetMode(context.Background(), VisibilityMode{Mode: ModeOnlyTeam, Team: tm.ID}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.checkTeamGone(ctx) // Team.Get fails with a context error, not ErrNotFound
	if m := s.Mode(); m.Mode != ModeOnlyTeam || m.Team != tm.ID {
		t.Fatalf("mode after a lookup error = %+v, want only_team unchanged", m)
	}
}

// R55-107, R55-F31 (T6.2): presence.mode is an N row. An audit failure is
// logged centrally and SetMode returns nil; the mode is applied and the
// goodbye/online diff still goes out.
func TestSetModeAuditErrorStillSyncs(t *testing.T) {
	s, _, _, member, relay, _ := newSendingSender(t)
	ctx := context.Background()
	s.SyncVisibility(ctx)
	if relay.to(member) != 1 {
		t.Fatalf("initial online heartbeats = %d, want 1", relay.to(member))
	}
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.DB().ExecContext(ctx, `DROP TABLE audit_events`); err != nil {
		t.Fatal(err)
	}
	s.Audit = audit.New(st.DB())
	var logbuf bytes.Buffer
	audit.SetErrorLog(slog.New(slog.NewTextHandler(&logbuf, nil)))
	t.Cleanup(func() { audit.SetErrorLog(nil) })
	if err := s.SetMode(ctx, VisibilityMode{Mode: ModeInvisible}); err != nil {
		t.Fatalf("SetMode = %v, want nil for an N row", err)
	}
	if out := logbuf.String(); strings.Count(out, "event=audit_error") != 1 || !strings.Contains(out, "action=presence.mode") {
		t.Fatalf("central log: %q", out)
	}
	if m := s.Mode(); m.Mode != ModeInvisible {
		t.Fatalf("mode = %+v, want invisible", m)
	}
	if relay.to(member) != 2 {
		t.Fatalf("heartbeats to the member = %d, want 2 (online, then the goodbye)", relay.to(member))
	}
}

// R55-109: with sharing off, the local value is still detected; only the wire
// flag is unknown.
func TestHumanPresentDetectedWhenNotShared(t *testing.T) {
	s := &Sender{Idle: func(context.Context) (time.Duration, bool) { return time.Minute, true }}
	s.humanSet, s.humanShare = true, false
	if got := s.HumanPresent(context.Background()); got == nil || !*got {
		t.Fatalf("HumanPresent = %v, want true even when not shared", got)
	}
	if f := s.humanFlag(context.Background()); f != 2 {
		t.Fatalf("wire human flag = %d, want 2 (unknown) when not shared", f)
	}
	s.humanShare = true
	if f := s.humanFlag(context.Background()); f != 1 {
		t.Fatalf("wire human flag = %d, want 1 when shared", f)
	}
}

// R55-069: a peer that leaves the visible set because its mailbox row was
// garbage-collected still gets its goodbye.
func TestGoodbyeSentAfterMailboxKeyGone(t *testing.T) {
	s, ts, tm, member, relay, keys := newSendingSender(t)
	ctx := context.Background()
	s.SyncVisibility(ctx)
	if relay.to(member) != 1 {
		t.Fatalf("online heartbeats = %d, want 1", relay.to(member))
	}
	keys.drop(member) // the peer row is gone (GC of an introduced peer)
	if _, err := ts.RemoveMember(ctx, tm.ID, member, testNow); err != nil {
		t.Fatal(err)
	}
	s.SyncVisibility(ctx)
	if relay.to(member) != 2 {
		t.Fatalf("heartbeats to the departed peer = %d, want 2 (a goodbye)", relay.to(member))
	}
}

// R55-077: an invisible daemon does not answer pings; only_team answers only
// the team's members; visible answers every paired peer.
func TestPingAllowedByMode(t *testing.T) {
	s, _, tm, member, _, _ := newSendingSender(t)
	ctx := context.Background()
	stranger, _ := newPeerKey(t)
	if !s.PingAllowed(ctx, stranger) {
		t.Fatal("visible: a paired peer outside every team must get a pong")
	}
	if err := s.SetMode(ctx, VisibilityMode{Mode: ModeOnlyTeam, Team: tm.ID}); err != nil {
		t.Fatal(err)
	}
	if !s.PingAllowed(ctx, member) || s.PingAllowed(ctx, stranger) {
		t.Fatal("only_team: want the member answered and the stranger not")
	}
	if err := s.SetMode(ctx, VisibilityMode{Mode: ModeInvisible}); err != nil {
		t.Fatal(err)
	}
	if s.PingAllowed(ctx, member) {
		t.Fatal("invisible: a ping must not be answered")
	}
}
