package relay_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

func mboxFor(p peer, marker string) json.RawMessage {
	return json.RawMessage(`{"announcement":{"identity":"` + p.key + `","marker":"` + marker + `"},"signature":"x"}`)
}

func issueV2(t *testing.T, c *websocket.Conn, p peer, lookup, ref string) envelope.Control {
	t.Helper()
	send(t, c, envelope.Control{Op: envelope.OpPairNew, Lookup: lookup, Card: cardFor(p, "issuer"), Mbox: mboxFor(p, "issuer"), Ref: ref})
	return readControl(t, c)
}

func redeemV2(t *testing.T, c *websocket.Conn, p peer, lookup, ref string) {
	t.Helper()
	send(t, c, envelope.Control{Op: envelope.OpPairRedeem, Lookup: lookup, Card: cardFor(p, "redeemer"), Mbox: mboxFor(p, "redeemer"), Ref: ref})
}

func TestPairV2RedeemByLookup(t *testing.T) {
	srv, url := start(t, relay.Options{Now: newClock().Now})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)

	r := issueV2(t, ca, a, "7KQ2M", "n1")
	if r.Op != envelope.OpPairCode || r.Code != "" || r.Expires == "" || r.Ref != "n1" {
		t.Fatalf("got %+v, want pair_code without code", r)
	}
	redeemV2(t, cb, b, "7kq2m", "r1") // normalised

	toIssuer := readControl(t, ca)
	if toIssuer.Op != envelope.OpPairPeer || toIssuer.PublicKey != b.key || toIssuer.Ref != "n1" ||
		!bytes.Equal(toIssuer.Card, cardFor(b, "redeemer")) || !bytes.Equal(toIssuer.Mbox, mboxFor(b, "redeemer")) {
		t.Fatalf("issuer got %+v", toIssuer)
	}
	toRedeemer := readControl(t, cb)
	if toRedeemer.Op != envelope.OpPairPeer || toRedeemer.PublicKey != a.key || toRedeemer.Ref != "r1" ||
		!bytes.Equal(toRedeemer.Card, cardFor(a, "issuer")) || !bytes.Equal(toRedeemer.Mbox, mboxFor(a, "issuer")) {
		t.Fatalf("redeemer got %+v", toRedeemer)
	}
	if st := srv.PairStats(); st.Issued != 1 || st.Redeemed != 1 {
		t.Errorf("stats = %+v", st)
	}
}

func TestPairV2LookupCollisionRejected(t *testing.T) {
	srv, url := start(t, relay.Options{Now: newClock().Now})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)

	if r := issueV2(t, ca, a, "7KQ2M", "1"); r.Op != envelope.OpPairCode {
		t.Fatalf("got %+v", r)
	}
	// Another key, and the same key, both collide.
	send(t, cb, envelope.Control{Op: envelope.OpPairNew, Lookup: "7KQ2M", Card: cardFor(b, "x"), Mbox: mboxFor(b, "x"), Ref: "2"})
	expectError(t, cb, envelope.CodeLookupTaken, "2")
	send(t, ca, envelope.Control{Op: envelope.OpPairNew, Lookup: "7kq2m", Card: cardFor(a, "x"), Mbox: mboxFor(a, "x"), Ref: "3"})
	expectError(t, ca, envelope.CodeLookupTaken, "3")
	if st := srv.PairStats(); st.Issued != 1 || st.LookupTaken != 2 {
		t.Errorf("stats = %+v", st)
	}
	// The original entry is untouched and a different lookup works.
	if r := issueV2(t, cb, b, "AAAAA", "4"); r.Op != envelope.OpPairCode {
		t.Fatalf("got %+v", r)
	}
}

func TestPairV2EntryEndsOnCancel(t *testing.T) {
	_, url := start(t, relay.Options{Now: newClock().Now})
	a, b, c := newPeer(t), newPeer(t), newPeer(t)
	ca, cb, cc := rawAuthed(t, url, a), rawAuthed(t, url, b), rawAuthed(t, url, c)
	issueV2(t, ca, a, "7KQ2M", "1")

	// Persists after a redemption.
	redeemV2(t, cb, b, "7KQ2M", "r1")
	readControl(t, ca)
	readControl(t, cb)

	// Cancel by another key is ignored silently; redemption still works.
	send(t, cc, envelope.Control{Op: envelope.OpPairCancel, Lookup: "7KQ2M"})
	redeemV2(t, cb, b, "7KQ2M", "r2")
	if r := readControl(t, cb); r.Op != envelope.OpPairPeer {
		t.Fatalf("entry vanished after foreign cancel: %+v", r)
	}
	readControl(t, ca)

	// The issuer's cancel ends it, without a reply.
	send(t, ca, envelope.Control{Op: envelope.OpPairCancel, Lookup: "7KQ2M"})
	time.Sleep(100 * time.Millisecond) // cancel has no reply and travels on another connection
	redeemV2(t, cb, b, "7KQ2M", "r3")
	expectError(t, cb, envelope.CodePairInvalid, "r3")
	send(t, ca, envelope.Control{Op: envelope.OpPairCancel, Lookup: "ZZZZZ"}) // unknown: no reply, connection stays open
	issueV2(t, ca, a, "7KQ2M", "again")                                       // and the lookup is free again
}

func TestPairV2EntryEndsOnTTL(t *testing.T) {
	clock := newClock()
	_, url := start(t, relay.Options{Now: clock.Now})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)
	issueV2(t, ca, a, "7KQ2M", "1")
	clock.Advance(10*time.Minute - time.Second)
	redeemV2(t, cb, b, "7KQ2M", "ok")
	if r := readControl(t, cb); r.Op != envelope.OpPairPeer {
		t.Fatalf("got %+v", r)
	}
	readControl(t, ca)
	clock.Advance(time.Second)
	redeemV2(t, cb, b, "7KQ2M", "late")
	expectError(t, cb, envelope.CodePairInvalid, "late")
	// An expired lookup can be issued again.
	issueV2(t, ca, a, "7KQ2M", "2")
}

func TestPairV2EntryEndsOnThirdRedemption(t *testing.T) {
	srv, url := start(t, relay.Options{Now: newClock().Now})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)
	issueV2(t, ca, a, "7KQ2M", "1")
	for i := 0; i < 3; i++ {
		redeemV2(t, cb, b, "7KQ2M", "r")
		if r := readControl(t, ca); r.Op != envelope.OpPairPeer {
			t.Fatalf("redemption %d: issuer got %+v", i+1, r)
		}
		if r := readControl(t, cb); r.Op != envelope.OpPairPeer {
			t.Fatalf("redemption %d: redeemer got %+v", i+1, r)
		}
	}
	redeemV2(t, cb, b, "7KQ2M", "4th")
	expectError(t, cb, envelope.CodePairInvalid, "4th")
	if st := srv.PairStats(); st.Redeemed != 3 || st.Invalid != 1 {
		t.Errorf("stats = %+v", st)
	}
}

func TestPairV2OfflineIssuerIsNotARedemption(t *testing.T) {
	srv, url := start(t, relay.Options{Now: newClock().Now})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)
	issueV2(t, ca, a, "7KQ2M", "1")
	_ = ca.Close(websocket.StatusNormalClosure, "")
	waitConnected(t, srv, a.key, false)
	for i := 0; i < 4; i++ {
		redeemV2(t, cb, b, "7KQ2M", "x")
		expectError(t, cb, envelope.CodePeerOffline, "x")
	}
	ca2 := rawAuthed(t, url, a)
	redeemV2(t, cb, b, "7KQ2M", "y")
	if r := readControl(t, ca2); r.Op != envelope.OpPairPeer {
		t.Fatalf("got %+v", r)
	}
}

func TestPairV2BadRequests(t *testing.T) {
	_, url := start(t, relay.Options{Now: newClock().Now, PairFailLimit: 1})
	a := newPeer(t)
	ca := rawAuthed(t, url, a)
	card, mbox := cardFor(a, "c"), mboxFor(a, "m")

	for name, ctl := range map[string]envelope.Control{
		"new short lookup":   {Op: envelope.OpPairNew, Lookup: "7KQ2", Card: card, Mbox: mbox},
		"new bad char":       {Op: envelope.OpPairNew, Lookup: "7KQ2U", Card: card, Mbox: mbox},
		"new long lookup":    {Op: envelope.OpPairNew, Lookup: "7KQ2M9", Card: card, Mbox: mbox},
		"new no mbox":        {Op: envelope.OpPairNew, Lookup: "7KQ2M", Card: card},
		"new mbox not obj":   {Op: envelope.OpPairNew, Lookup: "7KQ2M", Card: card, Mbox: json.RawMessage(`"x"`)},
		"new mbox too big":   {Op: envelope.OpPairNew, Lookup: "7KQ2M", Card: card, Mbox: json.RawMessage(`{"x":"` + strings.Repeat("a", envelope.MaxMboxBytes) + `"}`)},
		"redeem both":        {Op: envelope.OpPairRedeem, Lookup: "7KQ2M", Code: "AAAAA-AAAAA", Card: card, Mbox: mbox},
		"redeem neither":     {Op: envelope.OpPairRedeem, Card: card, Mbox: mbox},
		"redeem short":       {Op: envelope.OpPairRedeem, Lookup: "7K", Card: card, Mbox: mbox},
		"redeem no mbox":     {Op: envelope.OpPairRedeem, Lookup: "7KQ2M", Card: card},
		"redeem lookup+code": {Op: envelope.OpPairRedeem, Lookup: "7KQ2M", Code: "x", Card: card, Mbox: mbox},
	} {
		ctl.Ref = "r"
		send(t, ca, ctl)
		if r := readControl(t, ca); r.Op != envelope.OpError || r.Code != envelope.CodeBadPairing {
			t.Errorf("%s: got %+v, want bad_pairing", name, r)
		}
	}
	// Own lookup.
	issueV2(t, ca, a, "7KQ2M", "1")
	redeemV2(t, ca, a, "7KQ2M", "self")
	expectError(t, ca, envelope.CodeBadPairing, "self")
}

func TestPairV2SharesOutstandingLimitWithV1(t *testing.T) {
	_, url := start(t, relay.Options{Now: newClock().Now})
	a := newPeer(t)
	ca := rawAuthed(t, url, a)
	for _, l := range []string{"AAAAA", "BBBBB", "CCCCC"} {
		issueV2(t, ca, a, l, "")
	}
	issue(t, ca, a, "")
	issue(t, ca, a, "")
	send(t, ca, envelope.Control{Op: envelope.OpPairNew, Lookup: "DDDDD", Card: cardFor(a, "c"), Mbox: mboxFor(a, "c"), Ref: "6"})
	expectError(t, ca, envelope.CodePairLimit, "6")
}

func TestPairV1DisabledRefusedAndEnabledAccepted(t *testing.T) {
	// Disabled: both v1 frames get pair_v1_disabled and touch no state.
	srv, url := start(t, relay.Options{Now: newClock().Now, DisablePairingV1: true, PairFailLimit: 1})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)
	send(t, ca, envelope.Control{Op: envelope.OpPairNew, Card: cardFor(a, "c"), Ref: "n"})
	expectError(t, ca, envelope.CodePairV1Disabled, "n")
	for i := 0; i < 3; i++ { // not counted by the limiter
		redeem(t, cb, b, "AAAAA-AAAAA", "v1")
		expectError(t, cb, envelope.CodePairV1Disabled, "v1")
	}
	if st := srv.PairStats(); st.Issued != 0 || st.Invalid != 0 || st.RateLimited != 0 {
		t.Errorf("stats = %+v", st)
	}
	// v2 still works on the same connections.
	if r := issueV2(t, ca, a, "7KQ2M", "v2"); r.Op != envelope.OpPairCode {
		t.Fatalf("got %+v", r)
	}
	redeemV2(t, cb, b, "7KQ2M", "v2")
	if r := readControl(t, cb); r.Op != envelope.OpPairPeer {
		t.Fatalf("got %+v", r)
	}

	// Enabled (the Options zero value): v1 still works end to end.
	_, url2 := start(t, relay.Options{Now: newClock().Now})
	a2, b2 := newPeer(t), newPeer(t)
	ca2, cb2 := rawAuthed(t, url2, a2), rawAuthed(t, url2, b2)
	code := issue(t, ca2, a2, "n")
	redeem(t, cb2, b2, code.Code, "r")
	if r := readControl(t, cb2); r.Op != envelope.OpPairPeer || r.PublicKey != a2.key {
		t.Fatalf("got %+v", r)
	}
}

func TestPairV2FailuresUseTheLimiter(t *testing.T) {
	clock := newClock()
	srv, url := start(t, relay.Options{Now: clock.Now, PairFailLimit: 3, PairFailWindow: time.Minute})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)
	issueV2(t, ca, a, "7KQ2M", "1")
	for _, l := range []string{"00000", "00001", "00002"} {
		redeemV2(t, cb, b, l, "g")
		expectError(t, cb, envelope.CodePairInvalid, "g")
	}
	redeemV2(t, cb, b, "7KQ2M", "g")
	expectError(t, cb, envelope.CodePairRateLimited, "g")
	if st := srv.PairStats(); st.Invalid != 3 || st.RateLimited != 1 {
		t.Errorf("stats = %+v", st)
	}
	clock.Advance(time.Minute)
	redeemV2(t, cb, b, "7KQ2M", "ok")
	if r := readControl(t, cb); r.Op != envelope.OpPairPeer {
		t.Fatalf("got %+v", r)
	}
}

// The relay only ever sees the lookup. This drives a whole v2 exchange with a
// secret that the "daemons" keep to themselves, and checks that neither the
// relay's logs nor any frame it sent contains the secret, the lookup or the
// canonical lookup hash preimage.
func TestPairV2RelayOutputHoldsNoSecretOrLookup(t *testing.T) {
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	_, url := start(t, relay.Options{Now: newClock().Now, Logger: logger, PairFailLimit: 1})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)

	const lookup, secret = "7KQ2M", "9XHF4TRW8N"
	var frames []string
	record := func(c envelope.Control) {
		raw, _ := json.Marshal(c)
		frames = append(frames, string(raw))
	}

	record(issueV2(t, ca, a, lookup, "n"))
	redeemV2(t, cb, b, "00000", "bad") // failure path logs too
	record(readControl(t, cb))
	redeemV2(t, cb, b, lookup, "limited")
	record(readControl(t, cb))
	clockless := newPeer(t)
	cc := rawAuthed(t, url, clockless)
	redeemV2(t, cc, clockless, lookup, "r")
	record(readControl(t, ca))
	record(readControl(t, cc))
	send(t, ca, envelope.Control{Op: envelope.OpPairCancel, Lookup: lookup})
	send(t, ca, envelope.Control{Op: envelope.OpPairNew, Lookup: lookup, Card: cardFor(a, "c"), Mbox: mboxFor(a, "m"), Ref: "again"})
	record(readControl(t, ca))
	_ = ca.CloseNow()
	_ = cb.CloseNow()
	_ = cc.CloseNow()
	time.Sleep(100 * time.Millisecond)

	out := buf.String()
	if !strings.Contains(out, "pair_issue") || !strings.Contains(out, "pair_cancel") {
		t.Errorf("expected pairing events in log:\n%s", out)
	}
	for _, s := range append(frames, out) {
		for _, needle := range []string{secret, lookup, strings.ToLower(lookup), "9XHF4"} {
			if strings.Contains(s, needle) {
				t.Errorf("relay output leaks %q:\n%s", needle, s)
			}
		}
	}
}
