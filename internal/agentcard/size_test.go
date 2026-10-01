package agentcard

import (
	"crypto/ed25519"
	"fmt"
	"strings"
	"testing"
	"time"
)

// countParses replaces the envelope parse of Verify with one that counts its
// calls, for the length of the test.
func countParses(t *testing.T) *int {
	t.Helper()
	n := new(int)
	orig := parseEnvelope
	parseEnvelope = func(data []byte) (any, error) {
		*n++
		return orig(data)
	}
	t.Cleanup(func() { parseEnvelope = orig })
	return n
}

// R55-F13 A2: N16 is refused for its size before any parse (the counting
// parser sees no call), while P1 and P2 are parsed once and verify, P2 with
// its 32 skills. N17 (33 skills) passes the signature and fails the schema.
func TestSizeVectors(t *testing.T) {
	byName := map[string]string{}
	for _, c := range loadCardCases(t) {
		byName[c.Name] = c.Envelope
	}
	calls := countParses(t)

	if _, err := Verify([]byte(byName["N16"])); err == nil || !strings.Contains(err.Error(), "over the limit of 16384") {
		t.Fatalf("N16: got %v, want a size refusal", err)
	}
	if *calls != 0 {
		t.Fatalf("N16 was parsed %d times; the size must be checked first", *calls)
	}
	// One byte over the limit is refused, even when it is not JSON at all.
	if _, err := Verify([]byte(strings.Repeat("{", MaxCardBytes+1))); err == nil || *calls != 0 {
		t.Fatalf("MaxCardBytes+1: err %v, %d parses", err, *calls)
	}
	// At the limit the parse runs.
	if _, err := Verify([]byte(strings.Repeat("{", MaxCardBytes))); err == nil || *calls != 1 {
		t.Fatalf("MaxCardBytes: err %v, %d parses, want 1", err, *calls)
	}

	for _, n := range []string{"P1", "P2"} {
		sc, err := Verify([]byte(byName[n]))
		if err != nil {
			t.Fatalf("%s refused: %v", n, err)
		}
		if n == "P2" && len(sc.Card.Skills) != MaxSkills {
			t.Fatalf("P2 has %d skills", len(sc.Card.Skills))
		}
	}
	if _, err := Verify([]byte(byName["N17"])); err == nil || !strings.Contains(err.Error(), "does not match schema: 33 skills, at most 32") {
		t.Fatalf("N17: got %v, want a schema refusal for 33 skills", err)
	}
	if *calls != 4 {
		t.Fatalf("%d parses, want 4", *calls)
	}
}

func skillsOf(n int, text string) []Skill {
	out := make([]Skill, n)
	for i := range out {
		out[i] = Skill{ID: fmt.Sprintf("s%02d", i+1), Name: text, Description: text}
	}
	return out
}

// R55-F13 A3 (with the review 71b addition): New and Sign refuse 33 skills
// and a card over 16 KiB, measured as json.MarshalIndent writes it, so a card
// whose canonical envelope fits but whose indented, escaped form does not is
// refused too.
func TestNewSignRefuseLimits(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	key := b64.EncodeToString(pub)
	direct := func(skills []Skill) Card {
		return Card{Version: Version, Name: "n", PublicKey: key, Harness: "custom", Skills: skills, Created: created.Format(time.RFC3339)}
	}

	if _, err := New(pub, "n", "custom", skillsOf(MaxSkills, "x"), created); err != nil {
		t.Fatalf("32 skills refused: %v", err)
	}
	if _, err := New(pub, "n", "custom", skillsOf(MaxSkills+1, "x"), created); err == nil || !strings.Contains(err.Error(), "33 skills, at most 32") {
		t.Fatalf("New with 33 skills: %v", err)
	}
	if _, err := Sign(priv, direct(skillsOf(MaxSkills+1, "x"))); err == nil || !strings.Contains(err.Error(), "33 skills, at most 32") {
		t.Fatalf("Sign with 33 skills: %v", err)
	}

	// 32 skills whose name and description are 128 '<': about 10 KiB canonical,
	// but MarshalIndent writes each '<' as the 6-byte <.
	big := skillsOf(MaxSkills, strings.Repeat("<", maxTextLen))
	canon, err := Canonical(direct(big))
	if err != nil {
		t.Fatal(err)
	}
	if env := len(`{"card":,"signature":""}`) + len(canon) + sigChars; env > MaxCardBytes {
		t.Fatalf("precondition: canonical envelope is %d bytes, want at most %d", env, MaxCardBytes)
	}
	if _, err := New(pub, "n", "custom", big, created); err == nil || !strings.Contains(err.Error(), "over the limit of 16384") {
		t.Fatalf("New with an indented form over 16 KiB: %v", err)
	}
	if _, err := Sign(priv, direct(big)); err == nil || !strings.Contains(err.Error(), "over the limit of 16384") {
		t.Fatalf("Sign with an indented form over 16 KiB: %v", err)
	}
}
