package agentcard

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"
)

// p3Name is the name of agent-card.md vector P3: an emoji ZWJ sequence and
// a variation selector.
const p3Name = "Ada \U0001F469\u200D\U0001F4BB \u2764\uFE0F"

// R55-F10 A10: VerifyStored accepts N18, N19 and P3 (the legacy text rule)
// and refuses N1-N17 as Verify does; Verify refuses N18 and N19 and accepts
// P3 (TestNegativeVectors checks the steps).
func TestVerifyStoredVectors(t *testing.T) {
	for _, c := range loadCardCases(t) {
		_, verr := Verify([]byte(c.Envelope))
		sc, serr := VerifyStored([]byte(c.Envelope))
		switch c.Name {
		case "N18", "N19":
			if verr == nil {
				t.Errorf("%s: Verify accepted it", c.Name)
			}
			if serr != nil {
				t.Errorf("%s: VerifyStored refused it: %v", c.Name, serr)
			} else if err := TextRuleError(sc.Card); err == nil || !strings.Contains(err.Error(), "bidi control or line separator") {
				t.Errorf("%s: TextRuleError = %v", c.Name, err)
			}
		default:
			if (verr == nil) != (serr == nil) || (verr != nil && verr.Error() != serr.Error()) {
				t.Errorf("%s: Verify %v, VerifyStored %v; want the same", c.Name, verr, serr)
			}
			if serr == nil {
				if err := TextRuleError(sc.Card); err != nil {
					t.Errorf("%s: TextRuleError = %v", c.Name, err)
				}
			}
		}
	}
}

// R55-F10 A11: card creation applies the text rule and names the field.
func TestNewRefusesBidiAndLineSeparators(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	skill := func(desc string) []Skill { return []Skill{{ID: "review", Name: "Code review", Description: desc}} }
	for _, tc := range []struct {
		name, harness string
		skills        []Skill
		field         string
	}{
		{"Ada \u202Etset", "custom", nil, "name"},
		{"Ada \u2066x", "custom", nil, "name"},
		{"Ada\u2028x", "custom", nil, "name"},
		{"Ada", "cu\u200Fstom", nil, "harness"},
		{"Ada", "custom", skill("a\u2029b"), "skills[0].description"},
	} {
		_, err := New(pub, tc.name, tc.harness, tc.skills, created)
		if err == nil || !strings.Contains(err.Error(), tc.field+" contains a bidi control or line separator") {
			t.Errorf("New(%+q, %+q): %v; want a refusal naming %s", tc.name, tc.harness, err, tc.field)
		}
		// Sign applies the same rule to a card built by hand.
		c := Card{Version: Version, Name: tc.name, PublicKey: b64.EncodeToString(pub), Harness: tc.harness,
			Skills: tc.skills, Created: created.Format(time.RFC3339)}
		if c.Skills == nil {
			c.Skills = []Skill{}
		}
		if _, err := Sign(priv, c); err == nil {
			t.Errorf("Sign(%+q, %+q) accepted the card", tc.name, tc.harness)
		}
	}
	c, err := New(pub, p3Name, "custom", skill("a/b & c"), created)
	if err != nil {
		t.Fatalf("New(P3's name): %v", err)
	}
	if _, err := Sign(priv, c); err != nil {
		t.Fatalf("Sign(P3's name): %v", err)
	}
}
