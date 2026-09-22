package notify

import (
	"crypto/rand"
	"sync"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	body := []byte(`{"v":1}`)
	ts := time.Now().Unix()
	sig := Sign(secret, "w-abc", ts, body)
	if !Verify(secret, "w-abc", ts, body, sig) {
		t.Fatal("verify failed on a freshly computed signature")
	}
	if Verify(secret, "w-abc", ts, []byte(`{"v":2}`), sig) {
		t.Fatal("verify accepted a tampered body")
	}
	if Verify(secret, "w-other", ts, body, sig) {
		t.Fatal("verify accepted a mismatched id")
	}
	wrong := make([]byte, 32)
	if Verify(wrong, "w-abc", ts, body, sig) {
		t.Fatal("verify accepted a wrong secret")
	}
}

func TestParseSignatureHeader(t *testing.T) {
	sig, ok := ParseSignatureHeader("v1=abc123")
	if !ok || sig != "abc123" {
		t.Fatalf("got %q, %v", sig, ok)
	}
	if _, ok := ParseSignatureHeader("abc123"); ok {
		t.Fatal("accepted a header with no v1= prefix")
	}
}

// referenceVerifier is a minimal stand-in for the receiver-side verification
// documented in Docs/protocol/notify.md §Signature ("Receiver verification"):
// it recomputes the signature, rejects a stale timestamp, and does not
// reprocess a replayed id within 24h, while still answering it 2xx.
type referenceVerifier struct {
	secret []byte
	now    func() time.Time

	mu      sync.Mutex
	seen    map[string]time.Time
	handled int
}

func newReferenceVerifier(secret []byte, now func() time.Time) *referenceVerifier {
	return &referenceVerifier{secret: secret, now: now, seen: map[string]time.Time{}}
}

// verify returns (accept, processed): accept is whether the delivery gets a
// 2xx, processed is whether the handler logic actually ran (false for a
// deduplicated replay).
func (v *referenceVerifier) verify(id string, ts int64, body []byte, sig string) (accept, processed bool) {
	if !Verify(v.secret, id, ts, body, sig) {
		return false, false
	}
	now := v.now()
	if diff := now.Unix() - ts; diff > 300 || diff < -300 {
		return false, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for k, at := range v.seen {
		if now.Sub(at) > 24*time.Hour {
			delete(v.seen, k)
		}
	}
	if _, dup := v.seen[id]; dup {
		return true, false
	}
	v.seen[id] = now
	v.handled++
	return true, true
}

func TestReferenceVerifierDedupesRetryAndRejectsStale(t *testing.T) {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	v := newReferenceVerifier(secret, func() time.Time { return now })

	body := []byte(`{"v":1,"id":"w-abc"}`)
	ts1 := now.Unix()
	sig1 := Sign(secret, "w-abc", ts1, body)
	accept, processed := v.verify("w-abc", ts1, body, sig1)
	if !accept || !processed {
		t.Fatalf("first delivery: accept=%v processed=%v, want true true", accept, processed)
	}

	// A retry re-signs with a new timestamp but the same id and body.
	now = now.Add(10 * time.Second)
	ts2 := now.Unix()
	sig2 := Sign(secret, "w-abc", ts2, body)
	accept, processed = v.verify("w-abc", ts2, body, sig2)
	if !accept || processed {
		t.Fatalf("retry: accept=%v processed=%v, want true false (2xx without reprocessing)", accept, processed)
	}
	if v.handled != 1 {
		t.Fatalf("handled = %d, want 1", v.handled)
	}

	// A stale timestamp (> 300s old) is rejected outright.
	staleTS := ts1 - 400
	staleSig := Sign(secret, "w-stale", staleTS, body)
	accept, _ = v.verify("w-stale", staleTS, body, staleSig)
	if accept {
		t.Fatal("a stale timestamp was accepted")
	}
}
