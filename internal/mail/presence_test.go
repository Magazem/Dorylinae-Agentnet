package mail

import (
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

func sealPresenceTo(t *testing.T, from, to party, id string, body any, created time.Time) Sealed {
	t.Helper()
	sl, err := SealPresence(SealInput{
		Priv: from.priv, To: to.key, MailboxPub: to.mbox.PublicKey().Bytes(),
		ID: id, Kind: "presence", Body: body, Created: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sl
}

func presenceEnv(from, to party, sl Sealed) envelope.Envelope {
	return envelope.Envelope{From: from.key, To: to.key, Type: "presence", ID: sl.ID, TS: "2026-01-02T03:10:00Z", Payload: sl.Payload}
}

func TestPresenceRoundTrip(t *testing.T) {
	s, r, o := fixture(t)
	body := map[string]any{"state": "online"}
	sl := sealPresenceTo(t, s, r, "", body, vectorNow)
	if !ValidPresenceID(sl.ID) {
		t.Fatalf("bad presence id %q", sl.ID)
	}
	op, err := o.OpenPresence(presenceEnv(s, r, sl))
	if err != nil {
		t.Fatal(err)
	}
	if op.Msg.Kind != "presence" || op.Msg.ID != sl.ID || op.Msg.Body["state"] != "online" {
		t.Fatalf("unexpected msg %+v", op.Msg)
	}
}

func TestSealPresenceValidation(t *testing.T) {
	s, r, _ := fixture(t)
	pub := r.mbox.PublicKey().Bytes()
	if _, err := SealPresence(SealInput{Priv: s.priv, To: r.key, MailboxPub: pub, Kind: "note", Created: vectorNow}); err == nil {
		t.Fatal("want error for non-presence kind")
	}
	if _, err := SealPresence(SealInput{Priv: s.priv, To: r.key, MailboxPub: pub, Kind: "presence", ID: "m-0123456789abcdef0123456789abcdef", Created: vectorNow}); err == nil {
		t.Fatal("want error for mail-shaped id")
	}
}

// TestPresenceRelabelling checks the mutual protection of Docs/protocol/presence.md
// §Envelope: a presence message relabelled as mail fails mail step 9 (id
// format), and a mail relabelled as presence also fails step 9, since the
// prefixes are disjoint.
func TestPresenceRelabelling(t *testing.T) {
	s, r, o := fixture(t)

	presenceSl := sealPresenceTo(t, s, r, "", map[string]any{"state": "online"}, vectorNow)
	relabelledAsMail := envelope.Envelope{From: s.key, To: r.key, Type: "mail", ID: presenceSl.ID, TS: "2026-01-02T03:10:00Z", Payload: presenceSl.Payload}
	_, err := o.Open(relabelledAsMail)
	wantReason(t, err, 9, ReasonIDMismatch)

	mailSl := sealTo(t, s, r, "", "note", nil, vectorNow)
	relabelledAsPresence := envelope.Envelope{From: s.key, To: r.key, Type: "presence", ID: mailSl.ID, TS: "2026-01-02T03:10:00Z", Payload: mailSl.Payload}
	_, err = o.OpenPresence(relabelledAsPresence)
	wantReason(t, err, 9, ReasonIDMismatch)
}

// TestPresenceUnpairedBeforeCrypto checks the mandatory rule from review 15: an
// unpaired/unknown sender is rejected at step 1, before any HPKE open or
// signature check, so a Sybil flood costs the receiver no crypto work.
func TestPresenceUnpairedBeforeCrypto(t *testing.T) {
	s, r, o := fixture(t)
	sl := sealPresenceTo(t, s, r, "", map[string]any{"state": "online"}, vectorNow)
	o.Peers = fakePeers{} // s is no longer paired
	_, err := o.OpenPresence(presenceEnv(s, r, sl))
	wantReason(t, err, 1, ReasonUnpaired)
}

// TestPresenceNeverAudits checks that OpenPresence does not call o.Audit even
// when it is set, because presence rejections must not reach the audit log
// (Docs/protocol/presence.md §Receiving step 1).
func TestPresenceNeverAudits(t *testing.T) {
	s, r, o := fixture(t)
	sink := &recSink{}
	o.Audit = NewRejectAudit(sink, nil)
	o.Peers = fakePeers{}
	sl := sealPresenceTo(t, s, r, "", map[string]any{"state": "online"}, vectorNow)
	if _, err := o.OpenPresence(presenceEnv(s, r, sl)); err == nil {
		t.Fatal("want reject")
	}
	if sink.n != 0 {
		t.Fatalf("audited %d presence rejects, want 0", sink.n)
	}
}
