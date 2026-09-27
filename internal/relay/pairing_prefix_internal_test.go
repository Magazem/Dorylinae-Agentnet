package relay

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// prefixLookup returns a distinct, valid 5-character v2 lookup for index i, so
// many pair_new calls in one test never collide on pair_lookup_taken.
func prefixLookup(i int) string {
	b := make([]byte, envelope.PairLookupLen)
	for j := range b {
		b[j] = envelope.PairAlphabet[i%len(envelope.PairAlphabet)]
		i /= len(envelope.PairAlphabet)
	}
	return string(b)
}

// pairNewFrom drives one pair_new from a conn with the given prefix and
// returns the relay's reply, without a real network connection (as the
// ephemeral tests in this package do).
func pairNewFrom(t *testing.T, s *Server, prefix, lookup, ref string) envelope.Control {
	t.Helper()
	c := newConn(nil, testKey(t), 4, prefix)
	s.pairNew(c, &envelope.Control{
		Op: envelope.OpPairNew, Lookup: lookup,
		Card: json.RawMessage(`{}`), Mbox: json.RawMessage(`{}`), Ref: ref,
	})
	select {
	case raw := <-c.out:
		var ctl envelope.Control
		if err := json.Unmarshal(raw, &ctl); err != nil {
			t.Fatal(err)
		}
		return ctl
	default:
		t.Fatal("pair_new produced no reply")
		return envelope.Control{}
	}
}

// 21 pair_new from 21 different keys sharing one network prefix: the 21st is
// refused pair_rate_limited, while a key from a different prefix is
// unaffected (review-08b L5, Sybil defence).
func TestPairNewPerPrefixRateLimited(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := New(Options{Now: func() time.Time { return now }})
	t.Cleanup(s.Close)

	const prefix = "203.0.113.0"
	for i := 0; i < maxPairNewPerPrefixWindow; i++ {
		r := pairNewFrom(t, s, prefix, prefixLookup(i), fmt.Sprintf("r%d", i))
		if r.Op != envelope.OpPairCode {
			t.Fatalf("pair_new %d: got %+v, want pair_code", i, r)
		}
	}
	r := pairNewFrom(t, s, prefix, prefixLookup(maxPairNewPerPrefixWindow), "over")
	if r.Op != envelope.OpError || r.Code != envelope.CodePairRateLimited {
		t.Fatalf("21st pair_new in the prefix = %+v, want pair_rate_limited", r)
	}

	other := pairNewFrom(t, s, "198.51.100.0", prefixLookup(maxPairNewPerPrefixWindow+1), "other")
	if other.Op != envelope.OpPairCode {
		t.Fatalf("a different prefix was affected: got %+v", other)
	}
}

// 51 outstanding v2 codes for one prefix (each from a different key, so the
// per-key cap of 5 never applies) are refused pair_limit; a different prefix
// is unaffected. PairTTL is set well beyond the time advanced here, so
// entries do not expire while the per-prefix pair_new rate window is reset
// every 20 issues.
func TestPairNewPerPrefixOutstandingCap(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := New(Options{Now: func() time.Time { return now }, PairTTL: time.Hour})
	t.Cleanup(s.Close)

	const prefix = "203.0.113.0"
	for i := 0; i < maxOutstandingPerPrefix; i++ {
		if i > 0 && i%maxPairNewPerPrefixWindow == 0 {
			now = now.Add(pairNewPrefixWindow + time.Second) // reset the rate window, not the TTL
		}
		r := pairNewFrom(t, s, prefix, prefixLookup(i), fmt.Sprintf("r%d", i))
		if r.Op != envelope.OpPairCode {
			t.Fatalf("pair_new %d: got %+v, want pair_code", i, r)
		}
	}
	now = now.Add(pairNewPrefixWindow + time.Second)
	r := pairNewFrom(t, s, prefix, prefixLookup(maxOutstandingPerPrefix), "over")
	if r.Op != envelope.OpError || r.Code != envelope.CodePairLimit {
		t.Fatalf("51st outstanding code in the prefix = %+v, want pair_limit", r)
	}

	other := pairNewFrom(t, s, "198.51.100.0", prefixLookup(maxOutstandingPerPrefix+1), "other")
	if other.Op != envelope.OpPairCode {
		t.Fatalf("a different prefix was affected: got %+v", other)
	}
}

// Failed pair_redeem attempts from different keys sharing one prefix are also
// bounded together, on top of the unaffected per-key limiter.
func TestPairRedeemPerPrefixRateLimited(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := New(Options{Now: func() time.Time { return now }})
	t.Cleanup(s.Close)

	const prefix = "203.0.113.0"
	redeemFrom := func(prefix, lookup, ref string) envelope.Control {
		t.Helper()
		c := newConn(nil, testKey(t), 4, prefix)
		s.pairRedeem(c, &envelope.Control{
			Op: envelope.OpPairRedeem, Lookup: lookup,
			Card: json.RawMessage(`{}`), Mbox: json.RawMessage(`{}`), Ref: ref,
		})
		select {
		case raw := <-c.out:
			var ctl envelope.Control
			if err := json.Unmarshal(raw, &ctl); err != nil {
				t.Fatal(err)
			}
			return ctl
		default:
			t.Fatal("pair_redeem produced no reply")
			return envelope.Control{}
		}
	}

	for i := 0; i < maxFailedRedeemPerPrefixWin; i++ {
		r := redeemFrom(prefix, prefixLookup(1000+i), fmt.Sprintf("r%d", i)) // unknown lookups: always invalid
		if r.Op != envelope.OpError || r.Code != envelope.CodePairInvalid {
			t.Fatalf("redeem %d: got %+v, want pair_invalid", i, r)
		}
	}
	r := redeemFrom(prefix, prefixLookup(2000), "over")
	if r.Op != envelope.OpError || r.Code != envelope.CodePairRateLimited {
		t.Fatalf("11th failed redeem in the prefix = %+v, want pair_rate_limited", r)
	}

	other := redeemFrom("198.51.100.0", prefixLookup(2001), "other")
	if other.Op != envelope.OpError || other.Code != envelope.CodePairInvalid {
		t.Fatalf("a different prefix was affected: got %+v", other)
	}
}
