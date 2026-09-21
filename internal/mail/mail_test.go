package mail

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hpke"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

func seq(start byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

type party struct {
	priv ed25519.PrivateKey
	key  string
	mbox *ecdh.PrivateKey
}

func newParty(t *testing.T, idSeed, mboxSeed byte) party {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(seq(idSeed))
	mb, err := ecdh.X25519().NewPrivateKey(seq(mboxSeed))
	if err != nil {
		t.Fatal(err)
	}
	return party{priv: priv, key: b64u.EncodeToString(priv.Public().(ed25519.PublicKey)), mbox: mb}
}

type fakePeers map[string]bool

func (p fakePeers) IsPaired(k string) bool { return p[k] }

type fakeKeys map[KeyID]*ecdh.PrivateKey

func (k fakeKeys) MailboxKey(id KeyID) (*ecdh.PrivateKey, bool) {
	v, ok := k[id]
	return v, ok
}

const vectorPayload = "ARZ4bU5e90QRQkgULLKh1Et8jApG3DumD8XNKK+2Zw2wRYVXXr68pxgeYqBc1ar0KIhhaf8Tl+47niurJhywPLRWZCXsDGGslWQEv/SnR/hqBhfOUDjou5C+rWZiMqJhjknXlXMqrq3qn0KwThhdXPp6Cx0jJSaxUFtCMYdIeN8kGcH3kZip9u7ajY9br9eeGc4gosXwiiMPj5OFFBEG6RMV4jHnt62DN3LKVfa68nB/UgOztd5kmj3UuC0sMzFwt9qBMYQk7TUXwHczduUp8ZQm94TSvzeI6Lag/Gem+1MnOZIudTwJO2P8YYkMcKpwLOxLxGJsdSNTMPtrx9f6fmqX5wMz8rfyb3q14hiwr1HSQZYdPExiiVmqYb9TyV/D05z4ePocgc1udqXix93nXltMqXH8vyeYSUxuUBmdlFB44Kv306bdjRx8nk5s9x5jxwZsSH3M3eGpJIGPudSweP38JYu5pG5UFQtomtgW2cTo/Kp0BU/XxDBcSduCzANDv09UdK8G3sHs0NNYLj2kHF7xsS3+BEFrU2ljwVs="

const vectorID = "m-0123456789abcdef0123456789abcdef"

var vectorNow = time.Date(2026, 1, 2, 3, 15, 0, 0, time.UTC)

func fixture(t *testing.T) (sender, recip party, o *Opener) {
	t.Helper()
	sender = newParty(t, 0x00, 0x40)
	recip = newParty(t, 0x20, 0x60)
	kid := KeyIDOf(recip.mbox.PublicKey().Bytes())
	o = &Opener{
		Self:  recip.key,
		Peers: fakePeers{sender.key: true},
		Keys:  fakeKeys{kid: recip.mbox},
		Now:   func() time.Time { return vectorNow },
	}
	return sender, recip, o
}

func vectorEnv(t *testing.T, s, r party, payload []byte) envelope.Envelope {
	t.Helper()
	return envelope.Envelope{From: s.key, To: r.key, Type: "mail", ID: vectorID, TS: "2026-01-02T03:10:00Z", Payload: payload}
}

func wantReason(t *testing.T, err error, step int, reason string) {
	t.Helper()
	var re *RejectError
	if err == nil {
		t.Fatalf("want reject %s at step %d, got success", reason, step)
	}
	if !asReject(err, &re) || re.Reason != reason || re.Step != step {
		t.Fatalf("want %s at step %d, got %v", reason, step, err)
	}
}

func asReject(err error, re **RejectError) bool {
	if r, ok := err.(*RejectError); ok {
		*re = r
		return true
	}
	return false
}

func TestSpecVector(t *testing.T) {
	s, r, o := fixture(t)
	if got := KeyIDOf(r.mbox.PublicKey().Bytes()).String(); got != "16786d4e5ef74411" {
		t.Fatalf("key_id = %s", got)
	}
	payload, err := base64.StdEncoding.DecodeString(vectorPayload)
	if err != nil {
		t.Fatal(err)
	}
	op, err := o.Open(vectorEnv(t, s, r, payload))
	if err != nil {
		t.Fatal(err)
	}
	if op.Msg.Kind != "ack" || op.Msg.ID != vectorID || op.Msg.From != s.key {
		t.Fatalf("unexpected msg %+v", op.Msg)
	}
	if !strings.HasPrefix(string(op.Signed), `{"msg":{"body":{"ids":["m-fedcba9876543210fedcba9876543210"]},"created":"2026-01-02T03:10:00Z"`) {
		t.Fatalf("plaintext = %s", op.Signed)
	}
}

func TestSpecVectorNegatives(t *testing.T) {
	s, r, o := fixture(t)
	payload, _ := base64.StdEncoding.DecodeString(vectorPayload)
	mut := func(f func(p []byte)) []byte {
		p := bytes.Clone(payload)
		f(p)
		return p
	}

	_, err := o.Open(vectorEnv(t, s, r, mut(func(p []byte) { p[9] ^= 1 })))
	wantReason(t, err, 4, ReasonDecrypt)
	_, err = o.Open(vectorEnv(t, s, r, mut(func(p []byte) { p[len(p)-1] ^= 1 })))
	wantReason(t, err, 4, ReasonDecrypt)

	env := vectorEnv(t, s, r, payload)
	env.ID = "m-0123456789abcdef0123456789abcdee"
	_, err = o.Open(env)
	wantReason(t, err, 4, ReasonDecrypt)

	env = vectorEnv(t, s, r, payload)
	env.From, env.To = r.key, s.key
	o2 := *o
	o2.Peers = fakePeers{r.key: true}
	_, err = o2.Open(env)
	wantReason(t, err, 4, ReasonDecrypt)

	_, err = o.Open(vectorEnv(t, s, r, mut(func(p []byte) { p[0] = 0x02 })))
	wantReason(t, err, 2, ReasonMalformed)
	_, err = o.Open(vectorEnv(t, s, r, payload[:MinPayload-1]))
	wantReason(t, err, 2, ReasonMalformed)
	_, err = o.Open(vectorEnv(t, s, r, mut(func(p []byte) { p[1] ^= 1 })))
	wantReason(t, err, 3, ReasonKeyMiss)

	o3 := *o
	o3.Now = func() time.Time { return time.Date(2026, 2, 2, 3, 10, 1, 0, time.UTC) }
	_, err = o3.Open(vectorEnv(t, s, r, payload))
	wantReason(t, err, 11, ReasonStale)
}

func sealTo(t *testing.T, from party, to party, id, kind string, body any, created time.Time) Sealed {
	t.Helper()
	sl, err := Seal(SealInput{Priv: from.priv, To: to.key, MailboxPub: to.mbox.PublicKey().Bytes(), ID: id, Kind: kind, Body: body, Created: created})
	if err != nil {
		t.Fatal(err)
	}
	return sl
}

func env(from, to party, sl Sealed) envelope.Envelope {
	return envelope.Envelope{From: from.key, To: to.key, Type: "mail", ID: sl.ID, TS: "2026-01-02T03:10:00Z", Payload: sl.Payload}
}

func TestRoundTrip(t *testing.T) {
	s, r, o := fixture(t)
	body := map[string]any{"text": "héllo", "n": 42, "nested": map[string]any{"a": []int{1, 2}}}
	sl := sealTo(t, s, r, "", "request", body, vectorNow)
	if !ValidID(sl.ID) || len(sl.Payload) < MinPayload {
		t.Fatalf("bad seal %+v", sl)
	}
	op, err := o.Open(env(s, r, sl))
	if err != nil {
		t.Fatal(err)
	}
	if op.Msg.Kind != "request" || op.Msg.Body["text"] != "héllo" || !bytes.Equal(op.Signed, sl.Signed) {
		t.Fatalf("round trip mismatch: %+v", op.Msg)
	}
	if !op.Msg.Created.Equal(vectorNow) {
		t.Fatalf("created = %v", op.Msg.Created)
	}
}

func TestTamperRejected(t *testing.T) {
	s, r, o := fixture(t)
	sl := sealTo(t, s, r, "", "request", map[string]any{}, vectorNow)
	for _, off := range []int{1 + KeyIDLen, 1 + KeyIDLen + encLen, len(sl.Payload) - 1} { // enc, ct, tag
		p := bytes.Clone(sl.Payload)
		p[off] ^= 0x80
		e := env(s, r, Sealed{ID: sl.ID, Payload: p})
		_, err := o.Open(e)
		wantReason(t, err, 4, ReasonDecrypt)
	}
	e := env(s, r, sl)
	e.ID = NewID() // aad
	_, err := o.Open(e)
	wantReason(t, err, 4, ReasonDecrypt)
}

func TestFromMismatch(t *testing.T) {
	s, r, o := fixture(t)
	other := newParty(t, 0x80, 0x90)
	o.Peers = fakePeers{s.key: true, other.key: true}
	// other seals a mail to r; the envelope claims s. info is bound to other's
	// key, so it fails at decryption.
	sl := sealTo(t, other, r, "", "request", nil, vectorNow)
	_, err := o.Open(env(s, r, sl))
	wantReason(t, err, 4, ReasonDecrypt)
	// Under the envelope's own from it opens.
	if _, err := o.Open(env(other, r, sl)); err != nil {
		t.Fatal(err)
	}
}

func TestSenderMismatchStep6(t *testing.T) {
	s, r, o := fixture(t)
	other := newParty(t, 0x80, 0x90)
	// A paired peer (s) seals a message that claims to be from other.
	sl := sealForged(t, s.key, r, other.priv, r.key, vectorNow)
	_, err := o.Open(env(s, r, sl))
	wantReason(t, err, 6, ReasonSenderMismatch)
}

// resealPlain encrypts plain under HPKE info built from envFrom and rcpt, as a
// sender that controls the plaintext would.
func resealPlain(t *testing.T, plain []byte, envFrom string, rcpt party, id string) Sealed {
	t.Helper()
	kid := KeyIDOf(rcpt.mbox.PublicKey().Bytes())
	pk, err := hpke.NewDHKEMPublicKey(rcpt.mbox.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	enc, snd, err := hpke.NewSender(pk, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), buildInfo(envFrom, rcpt.key, kid))
	if err != nil {
		t.Fatal(err)
	}
	ct, err := snd.Seal([]byte(id), plain)
	if err != nil {
		t.Fatal(err)
	}
	p := append(append([]byte{PayloadVersion}, kid[:]...), enc...)
	return Sealed{ID: id, KeyID: kid, Signed: plain, Payload: append(p, ct...)}
}

// sealForged seals a message signed by signer and addressed to `to`, under
// envelope-level sender envFrom.
func sealForged(t *testing.T, envFrom string, rcpt party, signer ed25519.PrivateKey, to string, created time.Time) Sealed {
	t.Helper()
	sl, err := Seal(SealInput{Priv: signer, To: to, MailboxPub: rcpt.mbox.PublicKey().Bytes(), Kind: "request", Created: created})
	if err != nil {
		t.Fatal(err)
	}
	return resealPlain(t, sl.Signed, envFrom, rcpt, sl.ID)
}

// sealBadSig seals a message whose msg.from is s but whose signature is made by signer.
func sealBadSig(t *testing.T, s party, signer ed25519.PrivateKey, rcpt party) Sealed {
	t.Helper()
	sl, err := Seal(SealInput{Priv: signer, To: rcpt.key, MailboxPub: rcpt.mbox.PublicKey().Bytes(), Kind: "request", Created: vectorNow})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := agentcard.ParseStrict(sl.Signed)
	if err != nil {
		t.Fatal(err)
	}
	doc.(map[string]any)["msg"].(map[string]any)["from"] = s.key
	plain, err := agentcard.CanonicalValue(doc)
	if err != nil {
		t.Fatal(err)
	}
	return resealPlain(t, plain, s.key, rcpt, sl.ID)
}

func TestWrongRecipient(t *testing.T) {
	s, r, o := fixture(t)
	third := newParty(t, 0xa0, 0xb0)
	sl := sealForged(t, s.key, r, s.priv, third.key, vectorNow)
	_, err := o.Open(env(s, r, sl))
	wantReason(t, err, 8, ReasonWrongRecipient)
}

func TestStaleAndFuture(t *testing.T) {
	s, r, o := fixture(t)
	old := sealTo(t, s, r, "", "request", nil, vectorNow.Add(-MaxAge-time.Second))
	_, err := o.Open(env(s, r, old))
	wantReason(t, err, 11, ReasonStale)
	future := sealTo(t, s, r, "", "request", nil, vectorNow.Add(MaxSkew+time.Second))
	_, err = o.Open(env(s, r, future))
	wantReason(t, err, 11, ReasonStale)
	edge := sealTo(t, s, r, "", "request", nil, vectorNow.Add(-MaxAge))
	if _, err := o.Open(env(s, r, edge)); err != nil {
		t.Fatal(err)
	}
}

func TestSignatureByOtherPairedKey(t *testing.T) {
	s, r, o := fixture(t)
	other := newParty(t, 0x80, 0x90)
	o.Peers = fakePeers{s.key: true, other.key: true}
	// msg.from = s but the signature is made by another paired key.
	sl := sealBadSig(t, s, other.priv, r)
	_, err := o.Open(env(s, r, sl))
	wantReason(t, err, 7, ReasonBadSignature)
}

func TestUnpairedAndMisc(t *testing.T) {
	s, r, o := fixture(t)
	sl := sealTo(t, s, r, "", "request", nil, vectorNow)
	o.Peers = fakePeers{}
	_, err := o.Open(env(s, r, sl))
	wantReason(t, err, 1, ReasonUnpaired)
	if ReasonOf(err) != ReasonUnpaired || ReasonOf(nil) != "" {
		t.Fatal("ReasonOf")
	}
}

func TestSealValidation(t *testing.T) {
	s, r, _ := fixture(t)
	pub := r.mbox.PublicKey().Bytes()
	bad := []SealInput{
		{Priv: s.priv, To: r.key, MailboxPub: pub, Kind: "Bad Kind", Created: vectorNow},
		{Priv: s.priv, To: r.key, MailboxPub: pub, Kind: "x", ID: "m-short", Created: vectorNow},
		{Priv: s.priv, To: r.key, MailboxPub: pub[:31], Kind: "x", Created: vectorNow},
		{Priv: s.priv, To: r.key, MailboxPub: pub, Kind: "x", Body: map[string]any{"f": 1.5}, Created: vectorNow},
		{Priv: s.priv, To: r.key, MailboxPub: pub, Kind: "x", Body: []int{1}, Created: vectorNow},
		{Priv: s.priv, To: r.key, MailboxPub: pub, Kind: "x", Body: map[string]any{"big": strings.Repeat("a", MaxMailPlaintext)}, Created: vectorNow},
	}
	for i, in := range bad {
		if _, err := Seal(in); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestAckBodyStep12(t *testing.T) {
	s, r, o := fixture(t)
	good := "m-fedcba9876543210fedcba9876543210"
	for name, body := range map[string]any{
		"empty":       map[string]any{},
		"unknown":     map[string]any{"ids": []string{good}, "x": 1},
		"bad id":      map[string]any{"ids": []string{"m-nothex"}},
		"empty list":  map[string]any{"ids": []string{}},
		"not a list":  map[string]any{"ids": good},
		"too many":    map[string]any{"ids": manyIDs(257)},
		"bad unsupp.": map[string]any{"unsupported": []string{"x"}},
	} {
		sl := sealTo(t, s, r, "", "ack", body, vectorNow)
		_, err := o.Open(env(s, r, sl))
		if ReasonOf(err) != ReasonMalformed {
			t.Errorf("%s: got %v", name, err)
		}
	}
	for _, body := range []any{
		map[string]any{"ids": []string{good}},
		map[string]any{"unsupported": []string{good}},
		map[string]any{"ids": manyIDs(256), "unsupported": []string{good}},
	} {
		sl := sealTo(t, s, r, "", "ack", body, vectorNow)
		if _, err := o.Open(env(s, r, sl)); err != nil {
			t.Errorf("valid ack: %v", err)
		}
	}
}

func manyIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("m-%032x", i)
	}
	return out
}

func signedAnnouncement(t *testing.T, id ed25519.PrivateKey, pub []byte, created, notAfter string, tamper func(map[string]any)) map[string]any {
	t.Helper()
	ann := map[string]any{
		"v": json.Number("1"), "identity": b64u.EncodeToString(id.Public().(ed25519.PublicKey)),
		"key_id": KeyIDOf(pub).String(), "pub": b64u.EncodeToString(pub), "created": created, "not_after": notAfter,
	}
	if tamper != nil {
		tamper(ann)
	}
	c, err := agentcard.CanonicalValue(ann)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(id, append([]byte(annTag), c...))
	return map[string]any{"announcement": ann, "signature": b64u.EncodeToString(sig)}
}

func TestKeysBodyStep12(t *testing.T) {
	s, r, o := fixture(t)
	pub := s.mbox.PublicKey().Bytes()
	created, notAfter := "2026-01-02T03:00:00Z", "2026-01-16T03:00:00Z"
	good := signedAnnouncement(t, s.priv, pub, created, notAfter, nil)

	sl := sealTo(t, s, r, "", "keys", map[string]any{"announcement": good, "retry": []string{"m-fedcba9876543210fedcba9876543210"}}, vectorNow)
	if _, err := o.Open(env(s, r, sl)); err != nil {
		t.Fatal(err)
	}

	other := newParty(t, 0x80, 0x90)
	cases := map[string]any{
		"wrong identity": signedAnnouncement(t, other.priv, pub, created, notAfter, nil),
		"bad key_id":     signedAnnouncement(t, s.priv, pub, created, notAfter, func(a map[string]any) { a["key_id"] = "0000000000000000" }),
		"expired":        signedAnnouncement(t, s.priv, pub, "2025-12-01T00:00:00Z", "2025-12-15T00:00:00Z", nil),
		"too long":       signedAnnouncement(t, s.priv, pub, created, "2026-03-01T03:00:00Z", nil),
		"extra member":   signedAnnouncement(t, s.priv, pub, created, notAfter, func(a map[string]any) { a["x"] = "y" }),
	}
	for name, ann := range cases {
		sl := sealTo(t, s, r, "", "keys", map[string]any{"announcement": ann}, vectorNow)
		_, err := o.Open(env(s, r, sl))
		if ReasonOf(err) != ReasonBadKeys {
			t.Errorf("%s: got %v", name, err)
		}
	}
	forged := signedAnnouncement(t, s.priv, pub, created, notAfter, nil)
	forged["signature"] = b64u.EncodeToString(make([]byte, 64))
	for name, body := range map[string]any{
		"forged sig": map[string]any{"announcement": forged},
		"no ann":     map[string]any{"retry": []string{"m-fedcba9876543210fedcba9876543210"}},
		"bad retry":  map[string]any{"announcement": good, "retry": []string{"nope"}},
		"extra":      map[string]any{"announcement": good, "z": 1},
	} {
		sl := sealTo(t, s, r, "", "keys", body, vectorNow)
		_, err := o.Open(env(s, r, sl))
		want := ReasonMalformed
		if name == "forged sig" {
			want = ReasonBadKeys
		}
		if ReasonOf(err) != want {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

type recSink struct{ n int }

func (r *recSink) Append(_ context.Context, actor, action string, detail any) error {
	if actor != "daemon" || action != "mail.reject" {
		panic("bad audit call")
	}
	r.n++
	return nil
}

func TestRejectAuditRateLimit(t *testing.T) {
	s, r, o := fixture(t)
	sink := &recSink{}
	now := vectorNow
	a := NewRejectAudit(sink, slog.New(slog.DiscardHandler))
	a.now = func() time.Time { return now }
	o.Audit = a
	o.Peers = fakePeers{}
	e := env(s, r, sealTo(t, s, r, "", "request", nil, vectorNow))
	for i := 0; i < 50; i++ {
		if _, err := o.Open(e); err == nil {
			t.Fatal("want reject")
		}
	}
	if sink.n != 30 {
		t.Fatalf("audited %d, want 30", sink.n)
	}
	now = now.Add(time.Minute)
	_, _ = o.Open(e)
	if sink.n != 31 {
		t.Fatalf("audited %d after window, want 31", sink.n)
	}
}
