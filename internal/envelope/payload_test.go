package envelope_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// headerJSON is the routing part of e as JSON object members, without braces.
func headerJSON(t *testing.T, e envelope.Envelope) string {
	t.Helper()
	raw, err := json.Marshal(e.Header())
	if err != nil {
		t.Fatal(err)
	}
	return string(raw[1 : len(raw)-1])
}

// The relay's payload rule (envelope.md "Payload shape check", R55-010).
func TestParseHeaderPayloadShape(t *testing.T) {
	e := valid(t)
	head := headerJSON(t, e)
	big := `"` + base64.StdEncoding.EncodeToString(make([]byte, 700<<10)) + `"`
	for _, tc := range []struct {
		name, members string
		ok            bool
	}{
		{"number", `,"payload":1`, false},
		{"object", `,"payload":{}`, false},
		{"array", `,"payload":[]`, false},
		{"null", `,"payload":null`, false},
		{"missing", ``, false},
		{"not base64", `,"payload":"not base64!"`, false},
		{"unpadded", `,"payload":"QQ"`, false},
		{"padding inside", `,"payload":"QQ==QUFB"`, false},
		{"escape", `,"payload":"\u0051UFB"`, false},
		{"escaped slash", `,"payload":"QU\/B"`, false},
		{"three pads", `,"payload":"Q==="`, false},
		{"url alphabet", `,"payload":"QU-_"`, false},
		{"good then number", `,"payload":"QQ==","payload":1`, false},
		{"good then case variant", `,"payload":"QQ==","PAYLOAD":1`, false},
		// encoding/json keeps the first type error even when a later key
		// overwrites the field, so the recipient's Parse refuses this frame:
		// the relay must refuse it too (spec delta to test 10's "last wins").
		{"number then good", `,"payload":1,"payload":"QQ=="`, false},
		{"empty", `,"payload":""`, true},
		{"two pads", `,"payload":"QQ=="`, true},
		{"no pad", `,"payload":"QUFB"`, true},
		{"one pad", `,"payload":"QUE="`, true},
		{"case variant", `,"Payload":"QUFB"`, true},
		{"700 KiB", `,"payload":` + big, true},
	} {
		frame := []byte("{" + head + tc.members + "}")
		h, err := envelope.ParseHeader(frame)
		if tc.ok {
			if err != nil {
				t.Errorf("%s: ParseHeader refused: %v", tc.name, err)
				continue
			}
			// Whatever the relay accepts, the recipient can parse.
			if _, err := envelope.Parse(frame); err != nil {
				t.Errorf("%s: relay accepts but Parse refuses: %v", tc.name, err)
			}
			continue
		}
		if !errors.Is(err, envelope.ErrBadPayload) {
			t.Errorf("%s: err = %v, want ErrBadPayload", tc.name, err)
			continue
		}
		if h != e.Header() {
			t.Errorf("%s: header not returned with the payload error: %+v", tc.name, h)
		}
	}
}

// A frame whose routing fields fail is refused for them, not the payload.
func TestParseHeaderRoutingErrorFirst(t *testing.T) {
	e := valid(t)
	e.ID = ""
	raw, _ := json.Marshal(e) // bypasses Marshal's validation
	if _, err := envelope.ParseHeader(raw); err == nil || errors.Is(err, envelope.ErrBadPayload) {
		t.Fatalf("err = %v, want a routing field error", err)
	}
}

// Every Marshal output passes the relay's check (test 11).
func TestMarshalOutputPassesParseHeader(t *testing.T) {
	e := valid(t)
	var b byte
	for n := range 70 {
		e.Payload = make([]byte, n)
		for i := range e.Payload {
			e.Payload[i], b = b, b*37+11
		}
		raw, err := e.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := envelope.ParseHeader(raw); err != nil {
			t.Fatalf("payload of %d bytes: %v", n, err)
		}
	}
	e.Payload = nil
	raw, err := e.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := envelope.ParseHeader(raw); err != nil {
		t.Fatalf("nil payload: %v", err)
	}
}

// FuzzPayloadCheckEqualsDecoder: for escape-free JSON strings the relay's
// check accepts exactly what base64.StdEncoding accepts, and what it accepts
// the recipient's Parse accepts (test 11).
func FuzzPayloadCheckEqualsDecoder(f *testing.F) {
	for _, s := range []string{"", "QQ==", "QUFB", "QQ", "QQ==QUFB", "Q===", "====", "QUE=", "QU-_", "AAAA", "A+/=", "ab=c", "QQ=A"} {
		f.Add(s)
	}
	pa, pb := keyString(f), keyString(f)
	head := `"from":"` + pa + `","to":"` + pb + `","team":"t","type":"ping","id":"i","ts":"2026-01-02T03:04:05Z"`
	f.Fuzz(func(t *testing.T, s string) {
		for i := 0; i < len(s); i++ {
			if c := s[i]; c < 0x20 || c == '"' || c == '\\' || c >= 0x80 {
				return // not an escape-free, ASCII JSON string
			}
		}
		frame := []byte("{" + head + `,"payload":"` + s + `"}`)
		_, err := envelope.ParseHeader(frame)
		_, derr := base64.StdEncoding.DecodeString(s)
		if (err == nil) != (derr == nil) {
			t.Fatalf("%q: ParseHeader err=%v, StdEncoding err=%v", s, err, derr)
		}
		if err == nil {
			if _, perr := envelope.Parse(frame); perr != nil {
				t.Fatalf("%q: relay accepts but Parse refuses: %v", s, perr)
			}
		}
	})
}

func keyString(f *testing.F) string {
	f.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	return envelope.KeyString(pub)
}

// AckTarget uses ParseHeader's decoding: for repeated and case-variant keys it
// names exactly the (from, id) the relay checked at ingress (test 13), and it
// ignores only the payload verdict.
func TestAckTargetMatchesParseHeader(t *testing.T) {
	e := valid(t)
	other := valid(t)
	ts := `"to":"` + e.To + `","team":"t","type":"ping","ts":"2026-01-02T03:04:05Z"`
	corpus := []string{
		`{"from":"` + other.From + `","from":"` + e.From + `",` + ts + `,"id":"victim-id","id":"own-id","payload":1}`,
		`{"from":"` + e.From + `","From":"` + other.From + `",` + ts + `,"id":"a","payload":1}`,
		`{"FROM":"` + other.From + `",` + ts + `,"ID":"x","iD":"y","payload":"QQ=="}`,
		`{"from":"` + other.From + `","fRoM":"` + e.From + `",` + ts + `,"Id":"z","payload":null}`,
		`{"from":"` + e.From + `",` + ts + `,"id":"q","payload":"QQ==","PAYLOAD":"!"}`,
	}
	for _, frame := range corpus {
		from, id, typ, ok := envelope.AckTarget([]byte(frame))
		h, err := envelope.ParseHeader([]byte(frame))
		if err != nil && !errors.Is(err, envelope.ErrBadPayload) {
			t.Fatalf("%s: ParseHeader: %v", frame, err)
		}
		if !ok || from != h.From || id != h.ID || typ != h.Type {
			t.Errorf("%s: AckTarget = (%s, %s, %s, %v), ParseHeader = (%s, %s, %s)", frame, from, id, typ, ok, h.From, h.ID, h.Type)
		}
	}
	// A frame the relay would not have accepted is not acked (review 66b L1).
	for _, frame := range []string{
		`{"from":"` + e.From + `",` + ts + `,"id":"a","team":5,"payload":1}`,
		`{"from":"abc",` + ts + `,"id":"a","payload":1}`,
		`{"from":"` + e.From + `",` + ts + `,"payload":1}`,
		`not json`,
	} {
		if _, _, _, ok := envelope.AckTarget([]byte(frame)); ok {
			t.Errorf("%s: AckTarget ok for a frame the relay refuses", frame)
		}
	}
}
