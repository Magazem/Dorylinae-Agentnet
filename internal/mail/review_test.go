package mail

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// signedPlain builds a canonical signed plaintext from a msg map, signed by
// priv over whatever the map holds, so later steps can be reached with fields
// Seal would refuse.
func signedPlain(t *testing.T, priv ed25519.PrivateKey, msg map[string]any) []byte {
	t.Helper()
	c, err := canonical(msg)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, append([]byte(msgTag), c...))
	plain, err := canonical(map[string]any{"msg": msg, "sig": b64u.EncodeToString(sig)})
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

func baseMsg(s, r party, id string) map[string]any {
	return map[string]any{
		"v": json.Number("1"), "id": id, "from": s.key, "to": r.key,
		"created": "2026-01-02T03:10:00Z", "kind": "request", "body": map[string]any{},
	}
}

func TestMalformedPlaintextStep5(t *testing.T) {
	s, r, o := fixture(t)
	id := vectorID
	sig := strings.Repeat("A", 86)
	msgJSON := func(repl ...string) string {
		m := `{"body":{},"created":"2026-01-02T03:10:00Z","from":"` + s.key + `","id":"` + id + `","kind":"request","to":"` + r.key + `","v":1}`
		for i := 0; i+1 < len(repl); i += 2 {
			m = strings.Replace(m, repl[i], repl[i+1], 1)
		}
		return m
	}
	cases := map[string]string{
		"not json":        `nope`,
		"array":           `[]`,
		"empty object":    `{}`,
		"trailing data":   `{"msg":` + msgJSON() + `,"sig":"` + sig + `"} {}`,
		"invalid utf8":    `{"msg":` + msgJSON(`"request"`, "\"req\xffuest\"") + `,"sig":"` + sig + `"}`,
		"dup key":         `{"msg":` + msgJSON() + `,"sig":"` + sig + `","sig":"` + sig + `"}`,
		"extra top":       `{"msg":` + msgJSON() + `,"sig":"` + sig + `","x":1}`,
		"no sig":          `{"msg":` + msgJSON() + `,"x":"` + sig + `"}`,
		"sig short":       `{"msg":` + msgJSON() + `,"sig":"AAAA"}`,
		"sig padded":      `{"msg":` + msgJSON() + `,"sig":"` + sig + `=="}`,
		"sig not string":  `{"msg":` + msgJSON() + `,"sig":1}`,
		"msg not object":  `{"msg":[],"sig":"` + sig + `"}`,
		"extra member":    `{"msg":` + msgJSON(`"v":1`, `"v":1,"x":1`) + `,"sig":"` + sig + `"}`,
		"missing member":  `{"msg":` + msgJSON(`"body":{},`, ``) + `,"sig":"` + sig + `"}`,
		"v float":         `{"msg":` + msgJSON(`"v":1`, `"v":1.0`) + `,"sig":"` + sig + `"}`,
		"v string":        `{"msg":` + msgJSON(`"v":1`, `"v":"1"`) + `,"sig":"` + sig + `"}`,
		"v huge":          `{"msg":` + msgJSON(`"v":1`, `"v":99999999999999999999`) + `,"sig":"` + sig + `"}`,
		"body float":      `{"msg":` + msgJSON(`"body":{}`, `"body":{"n":1.5}`) + `,"sig":"` + sig + `"}`,
		"body -0":         `{"msg":` + msgJSON(`"body":{}`, `"body":{"n":-0}`) + `,"sig":"` + sig + `"}`,
		"body 2^53":       `{"msg":` + msgJSON(`"body":{}`, `"body":{"n":9007199254740992}`) + `,"sig":"` + sig + `"}`,
		"body array":      `{"msg":` + msgJSON(`"body":{}`, `"body":[]`) + `,"sig":"` + sig + `"}`,
		"kind bad":        `{"msg":` + msgJSON(`"request"`, `"Request"`) + `,"sig":"` + sig + `"}`,
		"kind empty":      `{"msg":` + msgJSON(`"request"`, `""`) + `,"sig":"` + sig + `"}`,
		"created frac":    `{"msg":` + msgJSON(`03:10:00Z`, `03:10:00.5Z`) + `,"sig":"` + sig + `"}`,
		"created offset":  `{"msg":` + msgJSON(`03:10:00Z`, `03:10:00+00:00`) + `,"sig":"` + sig + `"}`,
		"created lower z": `{"msg":` + msgJSON(`03:10:00Z`, `03:10:00z`) + `,"sig":"` + sig + `"}`,
		"created number":  `{"msg":` + msgJSON(`"2026-01-02T03:10:00Z"`, `1`) + `,"sig":"` + sig + `"}`,
		"deep nesting":    `{"msg":` + msgJSON(`"body":{}`, `"body":{"x":`+strings.Repeat("[", 20000)+strings.Repeat("]", 20000)+`}`) + `,"sig":"` + sig + `"}`,
	}
	for name, plain := range cases {
		sl := resealPlain(t, []byte(plain), s.key, r, id)
		_, err := o.Open(env(s, r, sl))
		var re *RejectError
		if !asReject(err, &re) || re.Step != 5 || re.Reason != ReasonMalformed {
			t.Errorf("%s: want step 5 malformed, got %v", name, err)
		}
	}
}

func TestIDMismatchStep9(t *testing.T) {
	s, r, o := fixture(t)
	// Signed msg.id differs from the envelope id (= aad).
	plain := signedPlain(t, s.priv, baseMsg(s, r, "m-fedcba9876543210fedcba9876543210"))
	_, err := o.Open(env(s, r, resealPlain(t, plain, s.key, r, vectorID)))
	wantReason(t, err, 9, ReasonIDMismatch)

	// msg.id equals the envelope id but is not in mail id format.
	odd := "not-a-mail-id"
	plain = signedPlain(t, s.priv, baseMsg(s, r, odd))
	_, err = o.Open(env(s, r, resealPlain(t, plain, s.key, r, odd)))
	wantReason(t, err, 9, ReasonIDMismatch)
}

func TestVersionStep10(t *testing.T) {
	s, r, o := fixture(t)
	m := baseMsg(s, r, vectorID)
	m["v"] = json.Number("2")
	plain := signedPlain(t, s.priv, m)
	_, err := o.Open(env(s, r, resealPlain(t, plain, s.key, r, vectorID)))
	wantReason(t, err, 10, ReasonMalformed)
}

func TestPayloadBounds(t *testing.T) {
	s, r, o := fixture(t)
	kid := KeyIDOf(r.mbox.PublicKey().Bytes())
	big := make([]byte, MaxPayload+1)
	big[0] = PayloadVersion
	copy(big[1:], kid[:])
	_, err := o.Open(env(s, r, Sealed{ID: vectorID, Payload: big}))
	wantReason(t, err, 2, ReasonMalformed)
	_, err = o.Open(env(s, r, Sealed{ID: vectorID, Payload: nil}))
	wantReason(t, err, 2, ReasonMalformed)
	// Exactly MaxPayload passes step 2 and fails authentication.
	_, err = o.Open(env(s, r, Sealed{ID: vectorID, Payload: big[:MaxPayload]}))
	wantReason(t, err, 4, ReasonDecrypt)
}

func TestAnnouncementWholeSeconds(t *testing.T) {
	s, r, o := fixture(t)
	pub := s.mbox.PublicKey().Bytes()
	for name, ts := range map[string][2]string{
		"created frac":   {"2026-01-02T03:00:00.5Z", "2026-01-16T03:00:00Z"},
		"not_after frac": {"2026-01-02T03:00:00Z", "2026-01-16T03:00:00.000Z"},
		"offset":         {"2026-01-02T03:00:00+00:00", "2026-01-16T03:00:00Z"},
	} {
		ann := signedAnnouncement(t, s.priv, pub, ts[0], ts[1], nil)
		sl := sealTo(t, s, r, "", "keys", map[string]any{"announcement": ann}, vectorNow)
		_, err := o.Open(env(s, r, sl))
		if ReasonOf(err) != ReasonBadKeys {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestRejectAuditConcurrent(t *testing.T) {
	sink := &lockedSink{}
	a := NewRejectAudit(sink, slog.New(slog.DiscardHandler))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				a.Report("peer", "m-x", ReasonDecrypt)
			}
		}()
	}
	wg.Wait()
	if sink.count() != rejectsPerMinute {
		t.Fatalf("audited %d, want %d", sink.count(), rejectsPerMinute)
	}
}

// FuzzOpen feeds arbitrary payloads (step 2 onwards) and arbitrary plaintexts
// sealed under a valid HPKE context (step 5 onwards). Open must never panic,
// and anything it accepts must satisfy the invariants of steps 5 to 11.
func FuzzOpen(f *testing.F) {
	vec, _ := base64.StdEncoding.DecodeString(vectorPayload)
	f.Add(vec, []byte(nil))
	f.Add([]byte{PayloadVersion}, []byte(`{"msg":{},"sig":""}`))
	f.Add([]byte(nil), []byte(`{"msg":{"body":{"ids":["m-fedcba9876543210fedcba9876543210"]},"created":"2026-01-02T03:10:00Z","from":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","id":"m-0123456789abcdef0123456789abcdef","kind":"ack","to":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc","v":1},"sig":"bKEyJ9nXxE2wmtRQaF3EuIYy08KkKNXseuZ19j5p3i0j9-vfPvgn5aAVjMsn-OIQiKSqFT2xIpFa2h_azVZiAg"}`))
	f.Fuzz(func(t *testing.T, payload, plain []byte) {
		s, r, o := fixture(t)
		if len(plain) > MaxMailPlaintext {
			return
		}
		if plain != nil {
			payload = resealPlain(t, plain, s.key, r, vectorID).Payload
		}
		op, err := o.Open(vectorEnv(t, s, r, payload))
		if err != nil {
			if ReasonOf(err) == "" {
				t.Fatalf("non-reject error %v", err)
			}
			return
		}
		m := op.Msg
		if m.From != s.key || m.To != r.key || m.ID != vectorID || m.V != 1 || !kindPattern.MatchString(m.Kind) || m.Body == nil {
			t.Fatalf("accepted bad msg %+v", m)
		}
		if plain != nil && !bytes.Equal(op.Signed, plain) {
			t.Fatal("Signed differs from the opened plaintext")
		}
		if m.Created.Before(vectorNow.Add(-MaxAge)) || m.Created.After(vectorNow.Add(MaxSkew)) {
			t.Fatal("accepted stale msg")
		}
	})
}

type lockedSink struct {
	mu sync.Mutex
	n  int
}

func (l *lockedSink) Append(_ context.Context, _, _ string, _ any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	return nil
}

func (l *lockedSink) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}
