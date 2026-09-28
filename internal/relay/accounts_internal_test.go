package relay

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// boundPairNew drives one pair_new from key (on its own network prefix) and
// returns the relay's reply.
func boundPairNew(t *testing.T, s *Server, key, prefix, lookup string) envelope.Control {
	t.Helper()
	c := newConn(nil, key, 4, prefix)
	if !s.handleControl(c, &envelope.Control{Op: envelope.OpPairNew, Lookup: lookup,
		Card: json.RawMessage(`{}`), Mbox: json.RawMessage(`{}`), Ref: lookup}) {
		t.Fatal("pair_new closed the connection")
	}
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

// boundKeys makes an account in a quota group with n bound keys.
func boundKeys(t *testing.T, s *Server, subject string, n int) (string, []string) {
	t.Helper()
	acc, err := s.EnsureAccount("github", subject, "@"+subject)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupForTest(acc, "qg_"+subject); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for range n {
		k := testKey(t)
		if err := s.BindKeyForTest(k, acc); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	return acc, keys
}

// relay-hosted.md §2 L5: 30 pair_new per account per day, whatever keys and
// prefixes it uses; another account is unaffected, and the next day it resets.
func TestPairNewPerAccountPerDay(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := New(Options{Now: func() time.Time { return now }, Accounts: AccountsGitHub})
	t.Cleanup(s.Close)
	acc, keys := boundKeys(t, s, "busy", 1)
	_, other := boundKeys(t, s, "calm", 1)

	s.FillAccountPairNewForTest(acc, maxPairNewPerAccountDay)
	if r := boundPairNew(t, s, keys[0], "198.51.100.0", prefixLookup(1)); r.Op != envelope.OpError || r.Code != envelope.CodePairRateLimited {
		t.Fatalf("31st pair_new of the day: %+v, want pair_rate_limited", r)
	}
	if r := boundPairNew(t, s, other[0], "198.51.101.0", prefixLookup(2)); r.Op != envelope.OpPairCode {
		t.Fatalf("other account: %+v, want pair_code", r)
	}
	now = now.Add(pairAccountWindow)
	if r := boundPairNew(t, s, keys[0], "198.51.102.0", prefixLookup(3)); r.Op != envelope.OpPairCode {
		t.Fatalf("next day: %+v, want pair_code", r)
	}
}

// At most 20 outstanding codes per account: codes issued by the account's
// keys count together (here some held by keys bound earlier, standing in for
// the per-key cap of 5 across the account's 4 keys).
func TestPairNewPerAccountOutstandingCap(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := New(Options{Now: func() time.Time { return now }, Accounts: AccountsGitHub, PairFailWindow: time.Hour})
	t.Cleanup(s.Close)
	acc, keys := boundKeys(t, s, "codes", 4)

	n := 0
	for i, k := range keys {
		for j := range maxOutstandingPerKey {
			r := boundPairNew(t, s, k, fmt.Sprintf("203.0.%d.0", i), prefixLookup(100+n))
			if r.Op != envelope.OpPairCode {
				t.Fatalf("key %d code %d: %+v", i, j, r)
			}
			n++
		}
	}
	if n != maxOutstandingPerAccount {
		t.Fatalf("issued %d, want %d", n, maxOutstandingPerAccount)
	}
	// A 21st outstanding code of the account (as if from a fifth key slot)
	// is refused with pair_limit.
	s.pairs.mu.Lock()
	for _, e := range s.pairs.entries {
		if e.issuer == keys[0] {
			e.issuer = "moved-" + e.issuer // frees key 0's per-key cap, keeps the account's count
		}
	}
	s.pairs.mu.Unlock()
	if r := boundPairNew(t, s, keys[0], "203.0.9.0", prefixLookup(999)); r.Op != envelope.OpError || r.Code != envelope.CodePairLimit {
		t.Fatalf("21st outstanding code of %s: %+v, want pair_limit", acc, r)
	}
}
