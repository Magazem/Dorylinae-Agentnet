package relay_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)} }

func cardFor(p peer, marker string) json.RawMessage {
	return json.RawMessage(`{"card":{"name":"` + marker + `","public_key":"` + p.key + `"},"signature":"x"}`)
}

func send(t *testing.T, c *websocket.Conn, ctl envelope.Control) {
	t.Helper()
	raw, err := json.Marshal(ctl)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx(t), websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
}

// issue has c request a code and returns the relay's reply.
func issue(t *testing.T, c *websocket.Conn, p peer, ref string) envelope.Control {
	t.Helper()
	send(t, c, envelope.Control{Op: envelope.OpPairNew, Card: cardFor(p, "issuer"), Ref: ref})
	r := readControl(t, c)
	if r.Op != envelope.OpPairCode || r.Code == "" || r.Ref != ref {
		t.Fatalf("got %+v, want pair_code", r)
	}
	return r
}

func redeem(t *testing.T, c *websocket.Conn, p peer, code, ref string) {
	t.Helper()
	send(t, c, envelope.Control{Op: envelope.OpPairRedeem, Code: code, Card: cardFor(p, "redeemer"), Ref: ref})
}

func expectError(t *testing.T, c *websocket.Conn, code, ref string) {
	t.Helper()
	r := readControl(t, c)
	if r.Op != envelope.OpError || r.Code != code || r.Ref != ref {
		t.Fatalf("got %+v, want error %s ref %q", r, code, ref)
	}
}

func TestPairIssueAndRedeemExchangesCards(t *testing.T) {
	clock := newClock()
	srv, url := start(t, relay.Options{Now: clock.Now})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)

	code := issue(t, ca, a, "n1")
	if want := clock.Now().Add(10 * time.Minute).Format(time.RFC3339); code.Expires != want {
		t.Errorf("expires = %s, want %s", code.Expires, want)
	}
	if len(code.Code) != 11 || code.Code[5] != '-' {
		t.Errorf("code %q is not XXXXX-XXXXX", code.Code)
	}

	// Lower-case and unhyphenated input is accepted.
	redeem(t, cb, b, strings.ToLower(strings.ReplaceAll(code.Code, "-", "")), "r1")

	toIssuer := readControl(t, ca)
	if toIssuer.Op != envelope.OpPairPeer || toIssuer.PublicKey != b.key || toIssuer.Ref != "n1" {
		t.Fatalf("issuer got %+v", toIssuer)
	}
	if !bytes.Equal(toIssuer.Card, cardFor(b, "redeemer")) {
		t.Errorf("issuer got card %s", toIssuer.Card)
	}
	toRedeemer := readControl(t, cb)
	if toRedeemer.Op != envelope.OpPairPeer || toRedeemer.PublicKey != a.key || toRedeemer.Ref != "r1" {
		t.Fatalf("redeemer got %+v", toRedeemer)
	}
	if !bytes.Equal(toRedeemer.Card, cardFor(a, "issuer")) {
		t.Errorf("redeemer got card %s", toRedeemer.Card)
	}
	if st := srv.PairStats(); st.Issued != 1 || st.Redeemed != 1 {
		t.Errorf("stats = %+v", st)
	}
}

func TestPairCodeIsSingleUse(t *testing.T) {
	_, url := start(t, relay.Options{Now: newClock().Now})
	a, b, c := newPeer(t), newPeer(t), newPeer(t)
	ca, cb, cc := rawAuthed(t, url, a), rawAuthed(t, url, b), rawAuthed(t, url, c)

	code := issue(t, ca, a, "")
	redeem(t, cb, b, code.Code, "")
	if r := readControl(t, cb); r.Op != envelope.OpPairPeer {
		t.Fatalf("first redemption: %+v", r)
	}
	// The same redeemer and a different one both fail.
	redeem(t, cb, b, code.Code, "again")
	expectError(t, cb, envelope.CodePairInvalid, "again")
	redeem(t, cc, c, code.Code, "third")
	expectError(t, cc, envelope.CodePairInvalid, "third")
}

func TestPairCodeExpires(t *testing.T) {
	clock := newClock()
	_, url := start(t, relay.Options{Now: clock.Now})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)

	code := issue(t, ca, a, "")
	clock.Advance(10*time.Minute - time.Second)
	code2 := issue(t, ca, a, "") // issued 1s before the first code expires
	clock.Advance(time.Second)   // first code is now exactly 10 minutes old
	redeem(t, cb, b, code.Code, "x")
	expectError(t, cb, envelope.CodePairInvalid, "x")

	redeem(t, cb, b, code2.Code, "y") // 1s old: still good
	if r := readControl(t, cb); r.Op != envelope.OpPairPeer {
		t.Fatalf("got %+v", r)
	}
}

func TestPairBruteForceIsRateLimited(t *testing.T) {
	clock := newClock()
	srv, url := start(t, relay.Options{Now: clock.Now, PairFailLimit: 3, PairFailWindow: time.Minute})
	a, b, other := newPeer(t), newPeer(t), newPeer(t)
	ca, cb, co := rawAuthed(t, url, a), rawAuthed(t, url, b), rawAuthed(t, url, other)
	code := issue(t, ca, a, "")

	for i := 0; i < 3; i++ {
		redeem(t, cb, b, "00000-0000"+string(rune('1'+i)), "g")
		expectError(t, cb, envelope.CodePairInvalid, "g")
	}
	// Limited: even the correct code is refused, and not consumed.
	redeem(t, cb, b, code.Code, "g")
	expectError(t, cb, envelope.CodePairRateLimited, "g")
	if st := srv.PairStats(); st.Invalid != 3 || st.RateLimited != 1 {
		t.Errorf("stats = %+v", st)
	}

	// The limit is per key: another key is unaffected...
	redeem(t, co, other, "00000-00009", "o")
	expectError(t, co, envelope.CodePairInvalid, "o")

	// ...and the window resets, after which the code still works.
	clock.Advance(time.Minute)
	redeem(t, cb, b, code.Code, "ok")
	if r := readControl(t, cb); r.Op != envelope.OpPairPeer || r.PublicKey != a.key {
		t.Fatalf("got %+v", r)
	}
}

func TestPairMalformedCodeCountsAsFailure(t *testing.T) {
	_, url := start(t, relay.Options{Now: newClock().Now, PairFailLimit: 1})
	b := newPeer(t)
	cb := rawAuthed(t, url, b)
	redeem(t, cb, b, "not a code!", "1")
	expectError(t, cb, envelope.CodePairInvalid, "1")
	redeem(t, cb, b, "nope", "2")
	expectError(t, cb, envelope.CodePairRateLimited, "2")
}

func TestPairRedeemDoesNotConsumeWhenIssuerOffline(t *testing.T) {
	srv, url := start(t, relay.Options{Now: newClock().Now})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)
	code := issue(t, ca, a, "")
	_ = ca.Close(websocket.StatusNormalClosure, "")

	// Wait for the relay to notice the disconnect.
	deadline := time.Now().Add(wait)
	for srv.Connected(a.key) {
		if time.Now().After(deadline) {
			t.Fatal("relay never noticed the issuer disconnecting")
		}
		time.Sleep(10 * time.Millisecond)
	}
	redeem(t, cb, b, code.Code, "x")
	expectError(t, cb, envelope.CodePeerOffline, "x")

	// The issuer comes back; the code was not burned.
	ca2 := rawAuthed(t, url, a)
	redeem(t, cb, b, code.Code, "y")
	if r := readControl(t, cb); r.Op != envelope.OpPairPeer {
		t.Fatalf("got %+v", r)
	}
	if r := readControl(t, ca2); r.Op != envelope.OpPairPeer || r.PublicKey != b.key {
		t.Fatalf("got %+v", r)
	}
}

func TestPairRejectsBadRequests(t *testing.T) {
	_, url := start(t, relay.Options{Now: newClock().Now})
	a := newPeer(t)
	ca := rawAuthed(t, url, a)

	send(t, ca, envelope.Control{Op: envelope.OpPairNew, Ref: "r"}) // no card
	expectError(t, ca, envelope.CodeBadPairing, "r")
	send(t, ca, envelope.Control{Op: envelope.OpPairNew, Card: json.RawMessage(`"str"`), Ref: "r"})
	expectError(t, ca, envelope.CodeBadPairing, "r")
	big := json.RawMessage(`{"x":"` + strings.Repeat("a", envelope.MaxCardBytes) + `"}`)
	send(t, ca, envelope.Control{Op: envelope.OpPairNew, Card: big, Ref: "r"})
	expectError(t, ca, envelope.CodeBadPairing, "r")
	send(t, ca, envelope.Control{Op: envelope.OpPairNew, Card: cardFor(a, "c"), Ref: strings.Repeat("r", 129)})
	expectError(t, ca, envelope.CodeBadPairing, "")

	// Redeeming your own code is refused and the code survives.
	code := issue(t, ca, a, "")
	redeem(t, ca, a, code.Code, "self")
	expectError(t, ca, envelope.CodeBadPairing, "self")

	// The connection is still usable.
	issue(t, ca, a, "again")
}

func TestPairOutstandingCodeLimit(t *testing.T) {
	clock := newClock()
	_, url := start(t, relay.Options{Now: clock.Now})
	a := newPeer(t)
	ca := rawAuthed(t, url, a)
	for i := 0; i < 5; i++ {
		issue(t, ca, a, "")
	}
	send(t, ca, envelope.Control{Op: envelope.OpPairNew, Card: cardFor(a, "c"), Ref: "6"})
	expectError(t, ca, envelope.CodePairLimit, "6")
	clock.Advance(10 * time.Minute)
	issue(t, ca, a, "")
}

func TestOtherControlFramesStillCloseConnection(t *testing.T) {
	_, url := start(t, relay.Options{})
	a := newPeer(t)
	ca := rawAuthed(t, url, a)
	send(t, ca, envelope.Control{Op: envelope.OpPairCode, Code: "AAAAA-AAAAA"})
	if _, _, err := ca.Read(ctx(t)); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("err = %v, want policy violation close", err)
	}
}

func TestPairLogsHoldNoCodesOrCards(t *testing.T) {
	var buf syncBuffer
	_, url := start(t, relay.Options{Logger: slog.New(slog.NewTextHandler(&buf, nil)), PairFailLimit: 1})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)
	send(t, ca, envelope.Control{Op: envelope.OpPairNew, Card: cardFor(a, "SECRETCARDNAME"), Ref: "n"})
	code := readControl(t, ca)
	redeem(t, cb, b, "ZZZZZ-ZZZZZ", "bad")
	expectError(t, cb, envelope.CodePairInvalid, "bad")
	redeem(t, cb, b, code.Code, "limited")
	expectError(t, cb, envelope.CodePairRateLimited, "limited")

	out := buf.String()
	if !strings.Contains(out, "pair_issue") || !strings.Contains(out, "pair_fail") {
		t.Errorf("expected pairing events in log:\n%s", out)
	}
	for _, secret := range []string{"SECRETCARDNAME", code.Code, strings.ReplaceAll(code.Code, "-", ""), "ZZZZZ"} {
		if strings.Contains(out, secret) {
			t.Errorf("log leaks %q:\n%s", secret, out)
		}
	}
}
