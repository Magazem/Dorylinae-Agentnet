package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// R55-F14 (Docs/review/72-r55-f14-spec.md §5 tests 2, 4 and 5): which mail
// rejects are audited, how often, and the limited mail_reject log line.

// syncDetailRec is detailRec safe for concurrent use.
type syncDetailRec struct {
	mu     sync.Mutex
	events []map[string]string
}

func (d *syncDetailRec) Append(_ context.Context, _, action string, detail any) error {
	m, _ := detail.(map[string]string)
	out := map[string]string{"action": action}
	for k, v := range m {
		out[k] = v
	}
	d.mu.Lock()
	d.events = append(d.events, out)
	d.mu.Unlock()
	return nil
}

func (d *syncDetailRec) all() []map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]map[string]string(nil), d.events...)
}

// Test 2: one genuine envelope failing step 8, 9, 10 or 12, replayed 50
// times, gives exactly one mail.reject row; an expired keys announcement and
// the steps a relay can cause give none.
func TestAuditedRejectOncePerEnvelope(t *testing.T) {
	s, r, _ := fixture(t)
	third := newParty(t, 0xa0, 0xb0)
	pub := s.mbox.PublicKey().Bytes()
	created, notAfter := "2026-01-02T03:00:00Z", "2026-01-16T03:00:00Z"
	forged := signedAnnouncement(t, s.priv, pub, created, notAfter, nil)
	forged["signature"] = b64u.EncodeToString(make([]byte, 64))
	expired := signedAnnouncement(t, s.priv, pub, "2025-12-01T00:00:00Z", "2025-12-15T00:00:00Z", nil)
	future := signedAnnouncement(t, s.priv, pub, "2026-03-01T00:00:00Z", "2026-03-10T00:00:00Z", nil)
	longLived := signedAnnouncement(t, s.priv, pub, "2025-12-31T00:00:00Z", "2026-03-01T00:00:00Z", nil)
	other := newParty(t, 0x80, 0x90)
	v2 := baseMsg(s, r, vectorID)
	v2["v"] = json.Number("2")
	odd := "not-a-mail-id"

	cases := []struct {
		name   string
		env    envelope.Envelope
		reason string
		rows   int
		withID bool
	}{
		{"step 8", env(s, r, sealForged(t, s.key, r, s.priv, third.key, vectorNow)), ReasonWrongRecipient, 1, true},
		{"step 9", env(s, r, resealPlain(t, signedPlain(t, s.priv, baseMsg(s, r, "m-fedcba9876543210fedcba9876543210")), s.key, r, vectorID)), ReasonIDMismatch, 1, true},
		{"step 9 bad id", env(s, r, resealPlain(t, signedPlain(t, s.priv, baseMsg(s, r, odd)), s.key, r, odd)), ReasonIDMismatch, 1, false},
		{"step 10", env(s, r, resealPlain(t, signedPlain(t, s.priv, v2), s.key, r, vectorID)), ReasonMalformed, 1, true},
		{"step 12 ack", env(s, r, sealTo(t, s, r, "", "ack", map[string]any{"x": 1}, vectorNow)), ReasonMalformed, 1, true},
		{"step 12 forged announcement", env(s, r, sealTo(t, s, r, "", "keys", map[string]any{"announcement": forged}, vectorNow)), ReasonBadKeys, 1, true},
		{"step 12 expired announcement", env(s, r, sealTo(t, s, r, "", "keys", map[string]any{"announcement": expired}, vectorNow)), ReasonBadKeys, 0, false},
		{"step 12 announcement created in the future", env(s, r, sealTo(t, s, r, "", "keys", map[string]any{"announcement": future}, vectorNow)), ReasonBadKeys, 1, true},
		{"step 12 announcement lifetime over 30 days", env(s, r, sealTo(t, s, r, "", "keys", map[string]any{"announcement": longLived}, vectorNow)), ReasonBadKeys, 1, true},
		{"step 5", env(s, r, resealPlain(t, []byte("nope"), s.key, r, vectorID)), ReasonMalformed, 0, false},
		{"step 6", env(s, r, sealForged(t, s.key, r, other.priv, r.key, vectorNow)), ReasonSenderMismatch, 0, false},
		{"step 7", env(s, r, sealBadSig(t, s, third.priv, r)), ReasonBadSignature, 0, false},
		{"stale", env(s, r, sealTo(t, s, r, "", "request", nil, vectorNow.Add(-MaxAge-1))), ReasonStale, 0, false},
	}
	for _, c := range cases {
		_, _, o := fixture(t)
		rec := &syncDetailRec{}
		ra := NewRejectAudit(rec, slog.New(slog.DiscardHandler))
		counted := map[string]int{}
		ra.CountLogged = func(reason string) { counted[reason]++ }
		o.Audit = ra
		for range 50 {
			if _, err := o.Open(c.env); ReasonOf(err) != c.reason {
				t.Fatalf("%s: err = %v, want %s", c.name, err, c.reason)
			}
		}
		ev := rec.all()
		if len(ev) != c.rows {
			t.Fatalf("%s: %d rows, want %d: %v", c.name, len(ev), c.rows, ev)
		}
		if c.rows == 0 {
			// Logged only, and counted for the reject summary.
			if counted[c.reason] != 50 {
				t.Fatalf("%s: counted %v, want 50 %s", c.name, counted, c.reason)
			}
			continue
		}
		want := map[string]string{"action": ActionReject, "peer": s.key, "reason": c.reason}
		if c.withID {
			want["id"] = c.env.ID
		}
		if !reflect.DeepEqual(ev[0], want) {
			t.Fatalf("%s: detail = %v, want %v", c.name, ev[0], want)
		}
	}
}

// OD-F14-7 (b): every logged-only reject except unpaired is counted for the
// daily summary; audited rejects are not.
func TestRejectAuditCountLogged(t *testing.T) {
	a := NewRejectAudit(&syncDetailRec{}, slog.New(slog.DiscardHandler))
	got := map[string]int{}
	a.CountLogged = func(reason string) { got[reason]++ }
	a.Report("p", vectorID, reject(1, ReasonUnpaired, nil))
	a.Report("p", vectorID, reject(4, ReasonDecrypt, nil))
	a.Report("p", vectorID, reject(7, ReasonBadSignature, nil))
	a.Report("p", vectorID, reject(11, ReasonStale, nil))
	a.Report("p", vectorID, reject(12, ReasonBadKeys, fmt.Errorf("%w: %w", errBadAnnouncement, fmt.Errorf("%w: %w", ErrAnnouncementExpired, errAnnouncementPast))))
	a.Report("p", vectorID, reject(8, ReasonWrongRecipient, nil))
	a.Report("p", vectorID, reject(11, ReasonBadBody, nil))
	a.Report("p", vectorID, reject(11, ReasonLimit, nil))
	want := map[string]int{ReasonDecrypt: 1, ReasonBadSignature: 1, ReasonStale: 1, ReasonBadKeys: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("counted %v, want %v", got, want)
	}
}

// R55-F13's limit refusal is a verified paired peer's mail: audited, once.
func TestLimitRejectAudited(t *testing.T) {
	rec := &syncDetailRec{}
	a := NewRejectAudit(rec, slog.New(slog.DiscardHandler))
	for range 5 {
		a.Report("p", vectorID, reject(11, ReasonLimit, nil))
	}
	if ev := rec.all(); len(ev) != 1 || ev[0]["reason"] != ReasonLimit {
		t.Fatalf("rows = %v", ev)
	}
}

// The dedupe set is bounded: the oldest pair is evicted first.
func TestAuditedSeenBounded(t *testing.T) {
	rec := &syncDetailRec{}
	a := NewRejectAudit(rec, slog.New(slog.DiscardHandler))
	now := vectorNow
	a.now = func() time.Time { return now }
	for i := range maxAuditedSeen + 1 {
		now = now.Add(time.Minute) // stay under the per-minute budget
		a.Report("p", fmt.Sprintf("m-%032x", i), reject(8, ReasonWrongRecipient, nil))
	}
	if len(a.seen) != maxAuditedSeen || len(a.seenRing) != maxAuditedSeen {
		t.Fatalf("seen = %d/%d, want %d", len(a.seen), len(a.seenRing), maxAuditedSeen)
	}
	now = now.Add(time.Minute)
	a.Report("p", fmt.Sprintf("m-%032x", 0), reject(8, ReasonWrongRecipient, nil)) // evicted: audited again
	a.Report("p", fmt.Sprintf("m-%032x", 5), reject(8, ReasonWrongRecipient, nil)) // still remembered
	if n := len(rec.all()); n != maxAuditedSeen+2 {
		t.Fatalf("%d rows, want %d", n, maxAuditedSeen+2)
	}
}

// Test 5 (mail part): 1000 unpaired rejects and one bad_signature in a
// minute give nothing during the minute and one line at stop, with the count,
// the per-reason counts and the first reject's fields, and never the id.
func TestMailRejectLogLine(t *testing.T) {
	s, r, o := fixture(t)
	rec := &testutil.LogRecorder{}
	a := NewRejectAudit(&syncDetailRec{}, rec.Logger())
	o.Audit = a
	unpaired := newParty(t, 0xc0, 0xd0)
	third := newParty(t, 0xa0, 0xb0)
	e := env(unpaired, r, sealTo(t, unpaired, r, "", "request", nil, vectorNow))
	for range 1000 {
		_, _ = o.Open(e)
	}
	o.Peers = fakePeers{s.key: true, third.key: true}
	bad := env(s, r, sealBadSig(t, s, third.priv, r))
	if _, err := o.Open(bad); ReasonOf(err) != ReasonBadSignature {
		t.Fatal(err)
	}
	if n := len(rec.Lines()); n != 0 {
		t.Fatalf("%d lines during the minute, want 0: %v", n, rec.Lines())
	}
	a.Flush()
	lines := rec.Event("mail_reject")
	if len(lines) != 1 || len(rec.Lines()) != 1 {
		t.Fatalf("lines = %v", rec.Lines())
	}
	at := lines[0].Attrs
	if at["count"] != "1001" || at["reasons"] != "bad_signature=1 unpaired=1000" ||
		at["reason"] != ReasonUnpaired || at["peer"] != unpaired.key || at["step"] != "1" {
		t.Fatalf("line = %v", at)
	}
	for k, v := range at {
		if strings.Contains(v, e.ID) || strings.Contains(v, bad.ID) || k == "id" {
			t.Fatalf("line carries the envelope id: %v", at)
		}
	}
}

// mail_ack_failed: one limited line with per-reason counts, not one Warn per
// replayed mail.
func TestAckFailedLimited(t *testing.T) {
	f := newRecvFixture(t)
	rec := &testutil.LogRecorder{}
	f.rcv.Log = rec.Logger()
	f.rcv.Peers = peerKeys{} // no mailbox key: every ack fails
	e := f.mailEnv(testID, "request", nil)
	for range 20 {
		if err := f.rcv.Handle(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(rec.Lines()); n != 0 {
		t.Fatalf("%d lines before the window ends: %v", n, rec.Lines())
	}
	f.rcv.Flush()
	lines := rec.Event("mail_ack_failed")
	if len(lines) != 1 || lines[0].Attrs["count"] != "20" || lines[0].Attrs["reasons"] != "no_mailbox_key=20" || lines[0].Level != slog.LevelWarn {
		t.Fatalf("lines = %v", rec.Lines())
	}
}

// Test 4: an unknown kind is audited as mail.in {kind: "unknown"}; a
// registered kind keeps its name.
func TestMailInUnknownKind(t *testing.T) {
	f := newRecvFixture(t)
	rec := &detailRec{}
	f.rcv.Audit = rec
	if err := f.rcv.Handle(context.Background(), f.mailEnv("m-00000000000000000000000000000001", "future.kind", nil)); err != nil {
		t.Fatal(err)
	}
	if err := f.rcv.Handle(context.Background(), f.mailEnv("m-00000000000000000000000000000002", "request", nil)); err != nil {
		t.Fatal(err)
	}
	if len(rec.events) != 2 || rec.events[0]["kind"] != "unknown" || rec.events[1]["kind"] != "request" {
		t.Fatalf("mail.in = %v", rec.events)
	}
}
