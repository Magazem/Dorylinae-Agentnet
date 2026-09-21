package noise

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

type party struct {
	priv ed25519.PrivateKey
	key  string
	st   *Static
}

func newParty(t *testing.T) party {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	st, err := NewStatic(pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil })
	if err != nil {
		t.Fatal(err)
	}
	return party{priv: priv, key: envelope.KeyString(pub), st: st}
}

// handshake runs XX between a (initiator) and b (responder).
func handshake(t *testing.T, a, b party) (ta, tb *Transport) {
	t.Helper()
	ia, err := NewHandshake(a.st, b.key, true)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := NewHandshake(b.st, a.key, false)
	if err != nil {
		t.Fatal(err)
	}
	m1, tr, err := ia.Write()
	if err != nil || tr != nil {
		t.Fatalf("msg1: %v", err)
	}
	if tr, err := rb.Read(m1); err != nil || tr != nil {
		t.Fatalf("read msg1: %v", err)
	}
	m2, tr, err := rb.Write()
	if err != nil || tr != nil {
		t.Fatalf("msg2: %v", err)
	}
	if tr, err := ia.Read(m2); err != nil || tr != nil {
		t.Fatalf("read msg2: %v", err)
	}
	m3, ta, err := ia.Write()
	if err != nil || ta == nil {
		t.Fatalf("msg3: %v", err)
	}
	tb, err = rb.Read(m3)
	if err != nil || tb == nil {
		t.Fatalf("read msg3: %v", err)
	}
	return ta, tb
}

func ad(n uint64) []byte { b := make([]byte, CounterSize); PutCounter(b, n); return b }

func TestHandshakeAndTransport(t *testing.T) {
	a, b := newParty(t), newParty(t)
	ta, tb := handshake(t, a, b)

	n, ct, err := ta.Seal(ad, []byte("hello"))
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if bytes.Contains(ct, []byte("hello")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	pt, err := tb.Open(n, ad(n), ct)
	if err != nil || string(pt) != "hello" {
		t.Fatalf("open: %q %v", pt, err)
	}
	n, ct, _ = tb.Seal(ad, []byte("back"))
	if pt, err := ta.Open(n, ad(n), ct); err != nil || string(pt) != "back" {
		t.Fatalf("reverse: %q %v", pt, err)
	}
	// A message sealed for one direction does not open in the other.
	n, ct, _ = ta.Seal(ad, []byte("x"))
	if _, err := ta.Open(n, ad(n), ct); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("own direction opened: %v", err)
	}
}

func TestReplayReorderTamper(t *testing.T) {
	a, b := newParty(t), newParty(t)
	ta, tb := handshake(t, a, b)
	type msg struct {
		n  uint64
		ct []byte
	}
	var ms []msg
	for i := 0; i < 4; i++ {
		n, ct, err := ta.Seal(ad, []byte{byte('a' + i)})
		if err != nil {
			t.Fatal(err)
		}
		ms = append(ms, msg{n, ct})
	}

	// Tampered message 0 is rejected and does not advance the counter.
	bad := bytes.Clone(ms[0].ct)
	bad[0] ^= 1
	if _, err := tb.Open(ms[0].n, ad(ms[0].n), bad); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered: %v", err)
	}
	// Wrong associated data is rejected.
	if _, err := tb.Open(ms[0].n, []byte("other"), ms[0].ct); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong ad: %v", err)
	}
	// A wrong counter (tampered header) is rejected.
	if _, err := tb.Open(ms[0].n+7, ad(ms[0].n), ms[0].ct); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong counter: %v", err)
	}
	// Message 1 still opens after the failure; message 0 is now too old.
	if pt, err := tb.Open(ms[1].n, ad(ms[1].n), ms[1].ct); err != nil || string(pt) != "b" {
		t.Fatalf("after tamper: %q %v", pt, err)
	}
	if _, err := tb.Open(ms[0].n, ad(ms[0].n), ms[0].ct); !errors.Is(err, ErrReplay) {
		t.Fatalf("reordered: %v", err)
	}
	// Replay of message 1.
	if _, err := tb.Open(ms[1].n, ad(ms[1].n), ms[1].ct); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay: %v", err)
	}
	// Gaps are allowed: 3 before 2, then 2 is out of order.
	if _, err := tb.Open(ms[3].n, ad(ms[3].n), ms[3].ct); err != nil {
		t.Fatalf("gap: %v", err)
	}
	if _, err := tb.Open(ms[2].n, ad(ms[2].n), ms[2].ct); !errors.Is(err, ErrReplay) {
		t.Fatalf("late: %v", err)
	}
}

func TestBindingRejectsWrongIdentity(t *testing.T) {
	a, b, mallory := newParty(t), newParty(t), newParty(t)

	// B expects A, but Mallory (a different identity) answers as initiator.
	im, _ := NewHandshake(mallory.st, b.key, true)
	rb, _ := NewHandshake(b.st, a.key, false)
	m1, _, _ := im.Write()
	if _, err := rb.Read(m1); err != nil {
		t.Fatal(err)
	}
	// The prologues differ (Mallory used her own key), so msg 2 fails in Noise.
	m2, _, _ := rb.Write()
	if _, err := im.Read(m2); !errors.Is(err, ErrHandshake) {
		t.Fatalf("prologue mismatch: %v", err)
	}

	// Mallory's static key signed by Mallory, presented as A's: the binding fails.
	forged := *mallory.st
	forged.identity = a.key
	forged.payload = mallory.st.payload // says identity = mallory
	ia, _ := NewHandshake(&forged, b.key, true)
	rb, _ = NewHandshake(b.st, a.key, false)
	m1, _, _ = ia.Write()
	_, _ = rb.Read(m1)
	m2, _, _ = rb.Write()
	if _, err := ia.Read(m2); err != nil {
		t.Fatal(err)
	}
	m3, _, _ := ia.Write()
	if _, err := rb.Read(m3); !errors.Is(err, ErrBinding) {
		t.Fatalf("forged binding: %v", err)
	}
}

func TestBindingRejectsUnsignedStatic(t *testing.T) {
	a, b := newParty(t), newParty(t)
	// A's binding carries a signature over a different static key.
	other := newParty(t)
	st := *a.st
	sig := ed25519.Sign(a.priv, BindingMessage(other.st.key.Public))
	st.payload = []byte(`{"v":1,"identity":"` + a.key + `","sig":"` + b64.EncodeToString(sig) + `"}`)
	ia, _ := NewHandshake(&st, b.key, true)
	rb, _ := NewHandshake(b.st, a.key, false)
	m1, _, _ := ia.Write()
	_, _ = rb.Read(m1)
	m2, _, _ := rb.Write()
	_, _ = ia.Read(m2)
	m3, _, _ := ia.Write()
	if _, err := rb.Read(m3); !errors.Is(err, ErrBinding) {
		t.Fatalf("signature over another key: %v", err)
	}
}

func TestHandshakeOrderAndTamper(t *testing.T) {
	a, b := newParty(t), newParty(t)
	ia, _ := NewHandshake(a.st, b.key, true)
	rb, _ := NewHandshake(b.st, a.key, false)
	if _, _, err := rb.Write(); !errors.Is(err, ErrHandshake) {
		t.Fatalf("responder wrote first: %v", err)
	}
	m1, _, _ := ia.Write()
	if _, _, err := ia.Write(); !errors.Is(err, ErrHandshake) {
		t.Fatalf("initiator wrote twice: %v", err)
	}
	_, _ = rb.Read(m1)
	m2, _, _ := rb.Write()
	m2[len(m2)-1] ^= 1
	if _, err := ia.Read(m2); !errors.Is(err, ErrHandshake) {
		t.Fatalf("tampered msg2: %v", err)
	}
	if _, _, err := ia.Write(); !errors.Is(err, ErrHandshake) {
		t.Fatal("handshake continued after a failure")
	}
}

func TestPrologueBindsBothIdentities(t *testing.T) {
	if bytes.Equal(Prologue("a", "b"), Prologue("b", "a")) {
		t.Fatal("prologue is symmetric")
	}
}
