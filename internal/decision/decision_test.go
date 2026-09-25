package decision_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/decision"
)

// The vector of Docs/protocol/decision.md §Vector, read from the document so
// the test and the spec cannot drift apart.
type docVector struct {
	canon      []byte
	id, hash   string
	sigI, sigR string
}

func readVector(t testing.TB) docVector {
	t.Helper()
	raw, err := os.ReadFile("../../Docs/protocol/decision.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	var v docVector
	for _, line := range strings.Split(doc, "\n") {
		switch f := strings.Fields(line); {
		case strings.HasPrefix(line, `{"closed":"2026-10-01T09:20:00Z"`):
			v.canon = []byte(line)
		case len(f) >= 2 && f[0] == "id" && strings.HasPrefix(f[1], "d-"):
			v.id = f[1]
		case len(f) == 2 && f[0] == "decision_hash":
			v.hash = f[1]
		case len(f) == 3 && f[0] == "sig" && f[1] == "initiator":
			v.sigI = f[2]
		case len(f) == 3 && f[0] == "sig" && f[1] == "respondent":
			v.sigR = f[2]
		}
	}
	if v.canon == nil || v.id == "" || v.hash == "" || v.sigI == "" || v.sigR == "" {
		t.Fatalf("vector not found in decision.md: %+v", v)
	}
	return v
}

func seedPriv(start byte) ed25519.PrivateKey {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = start + byte(i)
	}
	return ed25519.NewKeyFromSeed(s)
}

func canonOf(t testing.TB, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p, err := agentcard.ParseStrict(raw)
	if err != nil {
		t.Fatal(err)
	}
	c, err := agentcard.CanonicalValue(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const (
	vecSession = "s-36375782ceb6baea9cee4d4273dfb035"
	vecRequest = "r-0123456789abcdef0123456789abcdef"
	vecKeyI    = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	vecKeyR    = "Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc"
)

// vectorInput is the debate behind the vector: one round of passes, a
// proposal and an accepting answer.
func vectorInput(t *testing.T) decision.Input {
	t.Helper()
	req := canonOf(t, map[string]any{
		"v": 1, "id": vecRequest, "from": vecKeyI, "to": vecKeyR, "team": "t-00112233445566778899aabbccddeeff",
		"type": "debate", "title": "Outbox retry policy", "brief": "How should the outbox retry?", "urgency": "normal",
		"artifacts": []any{}, "created": "2026-10-01T09:00:00Z",
		"debate": map[string]any{"commitment": strings.Repeat("0", 64), "rounds": 1, "turn_timeout_s": 3600},
	})
	e := func(slot int, author, kind string, v any) decision.Entry {
		return decision.Entry{Slot: slot, Author: author, Kind: kind, Canon: canonOf(t, v)}
	}
	return decision.Input{
		Session: vecSession, Initiator: vecKeyI, Respondent: vecKeyR, Request: req, RoundsMax: 1, Count: 6,
		Entries: []decision.Entry{
			e(0, "initiator", "position", map[string]any{
				"claim":                 "Use capped exponential backoff for outbox retries",
				"assumptions":           []any{"Clock skew between peers is under 5 s"},
				"evidence":              []any{map[string]any{"kind": "file", "ref": "internal/mail/outbox.go"}},
				"rejected_alternatives": []any{map[string]any{"option": "Fixed 30 s retry", "reason": "Floods the relay after an outage"}},
				"argument":              "Retries should back off exponentially, capped at 10 minutes.",
			}),
			e(1, "respondent", "position", map[string]any{"claim": "Add full jitter to the existing backoff", "argument": "Jitter matters more than the curve."}),
			e(2, "initiator", "move", map[string]any{"challenges": []any{}}),
			e(3, "respondent", "move", map[string]any{"challenges": []any{}}),
			e(4, "initiator", "proposal", map[string]any{"agreement": map[string]any{"decision": "Capped exponential backoff with full jitter"}}),
			e(5, "respondent", "answer", map[string]any{"accept": true}),
			// A late entry (slot >= Count) is never in the Decision.
			e(6, "respondent", "move", map[string]any{"challenges": []any{}}),
		},
		Outcome: "agreed", Reason: "accepted", Closed: "2026-10-01T09:20:00Z",
	}
}

// Ticket 3.3a: the Decision vector byte for byte (derivation, id, hash and
// both signatures).
func TestDecisionVector(t *testing.T) {
	v := readVector(t)
	canon, err := decision.Derive(vectorInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canon, v.canon) {
		t.Fatalf("derived:\n%s\nwant:\n%s", canon, v.canon)
	}
	if got := decision.ID(vecSession); got != v.id {
		t.Fatalf("id %s, want %s", got, v.id)
	}
	if got := decision.Hash(canon); got != v.hash {
		t.Fatalf("hash %s, want %s", got, v.hash)
	}
	if got := decision.Sign(seedPriv(0x00), canon); got != v.sigI {
		t.Fatalf("initiator sig %s, want %s", got, v.sigI)
	}
	if got := decision.Sign(seedPriv(0x20), canon); got != v.sigR {
		t.Fatalf("respondent sig %s, want %s", got, v.sigR)
	}
	if !decision.VerifySignature(vecKeyI, canon, v.sigI) || decision.VerifySignature(vecKeyR, canon, v.sigI) {
		t.Fatal("VerifySignature")
	}
}

// Rule 6: the outcome must follow from the answer.
func TestDeriveRefusesInconsistentOutcome(t *testing.T) {
	for _, c := range []struct{ outcome, reason string }{
		{"escalated", "timeout"}, {"escalated", "rejected"},
	} {
		in := vectorInput(t)
		in.Outcome, in.Reason = c.outcome, c.reason
		if _, err := decision.Derive(in); !errors.Is(err, decision.ErrInconsistent) {
			t.Errorf("%s/%s: %v", c.outcome, c.reason, err)
		}
	}
	in := vectorInput(t)
	in.Count = 5 // the answer cut: agreed no longer follows
	if _, err := decision.Derive(in); !errors.Is(err, decision.ErrInconsistent) {
		t.Errorf("cut answer: %v", err)
	}
	in.Outcome, in.Reason = "escalated", "timeout"
	if _, err := decision.Derive(in); err != nil {
		t.Errorf("timeout without the answer: %v", err)
	}
	in.Count = 1
	if _, err := decision.Derive(in); !errors.Is(err, decision.ErrIncomplete) {
		t.Errorf("one position: %v", err)
	}
}

func signedFile(t testing.TB, d map[string]any, sigs map[string]string, hash string) []byte {
	t.Helper()
	s := map[string]any{}
	for k, v := range sigs {
		s[k] = v
	}
	return canonOf(t, map[string]any{"decision": d, "hash": hash, "signatures": s})
}

func vectorObject(t testing.TB, v docVector) map[string]any {
	t.Helper()
	p, err := agentcard.ParseStrict(v.canon)
	if err != nil {
		t.Fatal(err)
	}
	return p.(map[string]any)
}

// resign recomputes the hash and both signatures of d with the vector keys.
func resign(t *testing.T, d map[string]any) ([]byte, string, map[string]string) {
	t.Helper()
	c := canonOf(t, d)
	return c, decision.Hash(c), map[string]string{
		"initiator": decision.Sign(seedPriv(0x00), c), "respondent": decision.Sign(seedPriv(0x20), c),
	}
}

// Ticket 3.3a: the vector verifies with two signatures; every negative check
// of decision.md §Vector fails at the stated step; without the respondent
// signature it is valid but unconfirmed (exit 6, review 43 H1).
func TestVerifyVector(t *testing.T) {
	v := readVector(t)
	schema := debate.DecisionSchema()
	both := map[string]string{"initiator": v.sigI, "respondent": v.sigR}
	res := decision.Verify(signedFile(t, vectorObject(t, v), both, v.hash), schema)
	if !res.Valid || !res.Complete || res.Hash != v.hash || res.ID != v.id || len(res.SignedBy) != 2 {
		t.Fatalf("vector: %+v", res)
	}
	res = decision.Verify(signedFile(t, vectorObject(t, v), map[string]string{"initiator": v.sigI}, v.hash), schema)
	if !res.Valid || res.Complete || len(res.SignedBy) != 1 {
		t.Fatalf("without the respondent signature: %+v (want valid, not complete)", res)
	}

	type neg struct {
		name string
		file func() []byte
		step int
	}
	negs := []neg{
		{"respondent signature moved to initiator", func() []byte {
			return signedFile(t, vectorObject(t, v), map[string]string{"initiator": v.sigR}, v.hash)
		}, 3},
		{"respondent signature only", func() []byte {
			return signedFile(t, vectorObject(t, v), map[string]string{"respondent": v.sigR}, v.hash)
		}, 1},
		{"outcome escalated", func() []byte {
			d := vectorObject(t, v)
			d["outcome"] = "escalated"
			return signedFile(t, d, both, v.hash)
		}, 2},
		{"outcome escalated, hash recomputed", func() []byte {
			d := vectorObject(t, v)
			d["outcome"] = "escalated"
			return signedFile(t, d, both, decision.Hash(canonOf(t, d)))
		}, 3},
		{"member added", func() []byte {
			d := vectorObject(t, v)
			d["note"] = "x"
			return signedFile(t, d, both, v.hash)
		}, 1},
		{"id of another session", func() []byte {
			d := vectorObject(t, v)
			d["id"] = decision.ID("s-00000000000000000000000000000000")
			_, h, s := resign(t, d)
			return signedFile(t, d, s, h)
		}, 4},
		{"claim with a newline", func() []byte {
			d := vectorObject(t, v)
			d["positions"].(map[string]any)["respondent"].(map[string]any)["initial"].(map[string]any)["claim"] = "Add full\njitter"
			_, h, s := resign(t, d)
			return signedFile(t, d, s, h)
		}, 1},
		{"final_agreement changed, resigned", func() []byte {
			d := vectorObject(t, v)
			d["final_agreement"] = map[string]any{"decision": "Fixed 30 s retry"}
			_, h, s := resign(t, d)
			return signedFile(t, d, s, h)
		}, 5},
		{"not JSON", func() []byte { return []byte(`{"decision":`) }, 1},
	}
	for _, n := range negs {
		res := decision.Verify(n.file(), schema)
		if res.Valid || res.Step != n.step {
			t.Errorf("%s: valid=%v step=%d (%s), want step %d", n.name, res.Valid, res.Step, res.Reason, n.step)
		}
	}
}

var reHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Review 43 M6: the worst case (14 entries of 32768 bytes, two revisions, a
// maximal proposal and answer, a maximal escaped topic, 10 constraints of 500
// code points) derives under MaxDecision.
func TestWorstCaseDecisionSize(t *testing.T) {
	const entryMax = 32768
	pad := func(prefix map[string]any, member string) []byte {
		// Grow member until canonical(prefix) is exactly entryMax bytes.
		prefix[member] = ""
		base := len(canonOf(t, prefix))
		prefix[member] = strings.Repeat("x", entryMax-base)
		c := canonOf(t, prefix)
		if len(c) != entryMax {
			t.Fatalf("pad: %d", len(c))
		}
		return c
	}
	var es []decision.Entry
	es = append(es,
		decision.Entry{Slot: 0, Author: "initiator", Kind: "position", Canon: pad(map[string]any{"claim": "a"}, "argument")},
		decision.Entry{Slot: 1, Author: "respondent", Kind: "position", Canon: pad(map[string]any{"claim": "b"}, "argument")})
	for s := 2; s <= 11; s++ {
		author := "initiator"
		if s%2 == 1 {
			author = "respondent"
		}
		mv := map[string]any{"challenges": []any{}, "revision": map[string]any{"claim": "r", "argument": ""}}
		mv["revision"].(map[string]any)["argument"] = strings.Repeat("y", entryMax-len(canonOf(t, mv)))
		es = append(es, decision.Entry{Slot: s, Author: author, Kind: "move", Canon: canonOf(t, mv)})
	}
	dis := func(tag string) []any {
		var out []any
		for i := 0; i < 10; i++ {
			out = append(out, map[string]any{"point": strings.Repeat(tag, 280), "initiator": strings.Repeat("i", 280), "respondent": strings.Repeat(string(rune('0'+i)), 280)})
		}
		return out
	}
	prop := map[string]any{"agreement": map[string]any{"decision": "d", "argument": ""}, "remaining_disagreement": dis("p")}
	prop["agreement"].(map[string]any)["argument"] = strings.Repeat("z", entryMax-len(canonOf(t, prop)))
	proposal := canonOf(t, prop)
	es = append(es,
		decision.Entry{Slot: 12, Author: "initiator", Kind: "proposal", Canon: proposal},
		decision.Entry{Slot: 13, Author: "respondent", Kind: "answer", Canon: pad(map[string]any{
			"accept": true, "remaining_disagreement": dis("q"),
		}, "argument")})
	var cs []decision.Constraint
	for i := 0; i < 10; i++ {
		cs = append(cs, decision.Constraint{ID: "c-" + strings.Repeat(string(rune('a'+i)), 32), Author: "initiator", At: "2026-10-01T09:00:00Z",
			Text: strings.Repeat(" ", 0) + strings.Repeat("é\"", 250)})
	}
	req := canonOf(t, map[string]any{
		"id": vecRequest, "team": "t-00112233445566778899aabbccddeeff", "title": strings.Repeat("T", 120),
		"brief": strings.Repeat("\"", 16384), "created": "2026-10-01T09:00:00Z",
		"context": []any{map[string]any{"name": strings.Repeat("n", 255), "text": "x"}},
	})
	canon, err := decision.Derive(decision.Input{
		Session: vecSession, Initiator: vecKeyI, Respondent: vecKeyR, Request: req, RoundsMax: 5, Count: 14,
		Entries: es, Constraints: cs, Outcome: "agreed", Reason: "accepted", Closed: "2026-10-01T10:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(canon) > decision.MaxDecision || len(canon) < 550000 {
		t.Fatalf("worst case is %d bytes (MaxDecision %d)", len(canon), decision.MaxDecision)
	}
	if !reHex.MatchString(decision.Hash(canon)) {
		t.Fatal("hash")
	}
	t.Logf("worst-case Decision: %d bytes", len(canon))
}

// Review 47 M1: a close earlier than the request's created derives nothing;
// the same instant is allowed.
func TestDeriveRefusesClosedBeforeOpened(t *testing.T) {
	in := vectorInput(t)
	in.Closed = "2026-10-01T08:59:59Z"
	if _, err := decision.Derive(in); !errors.Is(err, decision.ErrClosedBeforeOpened) {
		t.Fatalf("closed before opened: %v", err)
	}
	in.Closed = "2026-10-01T09:00:00Z"
	if _, err := decision.Derive(in); err != nil {
		t.Fatalf("closed at opened: %v", err)
	}
}

// Review 47 L1: a signature or key whose last character carries non-zero
// unused bits decodes to the same bytes non-strictly; it is refused, so each
// signature has one text form.
func TestVerifyRefusesNonCanonicalBase64(t *testing.T) {
	v := readVector(t)
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	alt := func(s string) string {
		i := strings.IndexByte(alpha, s[len(s)-1])
		return s[:len(s)-1] + string(alpha[i^1])
	}
	if !decision.VerifySignature(vecKeyR, v.canon, v.sigR) {
		t.Fatal("vector signature does not verify")
	}
	if decision.VerifySignature(vecKeyR, v.canon, alt(v.sigR)) {
		t.Fatal("signature with non-zero padding bits verifies")
	}
	if decision.VerifySignature(alt(vecKeyR), v.canon, v.sigR) {
		t.Fatal("key with non-zero padding bits verifies")
	}
	res := decision.Verify(signedFile(t, vectorObject(t, v), map[string]string{"initiator": v.sigI, "respondent": alt(v.sigR)}, v.hash), debate.DecisionSchema())
	if res.Valid || res.Step != 3 {
		t.Fatalf("file with a non-canonical signature: %+v", res)
	}
}

// Review 47 L2: a hand-made file over MaxDecision fails step 1 even when each
// member is valid (here: thousands of context references).
func TestVerifyRefusesOversize(t *testing.T) {
	v := readVector(t)
	d := vectorObject(t, v)
	refs := make([]any, 9000)
	for i := range refs {
		refs[i] = map[string]any{"name": "notes.md", "bytes": json.Number("1"), "sha256": strings.Repeat("a", 64)}
	}
	d["problem"].(map[string]any)["context"] = refs
	c, h, s := resign(t, d)
	if len(c) <= decision.MaxDecision {
		t.Fatalf("test file is only %d bytes", len(c))
	}
	res := decision.Verify(signedFile(t, d, s, h), debate.DecisionSchema())
	if res.Valid || res.Step != 1 || !strings.Contains(res.Reason, "MaxDecision") {
		t.Fatalf("oversize file: %+v", res)
	}
}
