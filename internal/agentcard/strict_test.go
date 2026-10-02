package agentcard

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Inverted review 55 test T1-01: a card signed by K1 under its exact
// "public_key" member also carries "public_Key" (U+212A KELVIN SIGN) = K2 and
// "harneſſ" (U+017F). encoding/json folds both onto the struct fields, so the
// old Verify returned the card of K2. The card must be refused.
func TestVerifyRefusesFoldedMember(t *testing.T) {
	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	k1, k2 := b64.EncodeToString(pub1), b64.EncodeToString(pub2)

	for name, extra := range map[string]map[string]any{
		"public_Key":           {"public_Key": k2},
		"harneſſ":              {"harneſſ": "spoofed-harness"},
		"both":                 {"public_Key": k2, "harneſſ": "spoofed-harness"},
		"ascii public_Key":     {"public_Key": k2},
		"ascii Public_Key":     {"Public_Key": k2},
		"ascii HARNESS member": {"HARNESS": "spoofed-harness"},
	} {
		t.Run(name, func(t *testing.T) {
			card := map[string]any{
				"version":    json.Number("1"),
				"name":       "alice",
				"public_key": k1, // what the signature is checked under
				"harness":    "claude-code",
				"skills":     []any{},
				"created":    "2026-01-01T00:00:00Z",
			}
			for k, v := range extra {
				card[k] = v
			}
			canon, err := canonicalBytes(card)
			if err != nil {
				t.Fatal(err)
			}
			sig := ed25519.Sign(priv1, signingInput(canon))
			env := []byte(`{"card":` + string(canon) + `,"signature":"` + b64.EncodeToString(sig) + `"}`)

			sc, err := Verify(env)
			if err == nil {
				t.Fatalf("Verify accepted a card with the folded member(s) %v: public_key %s…, harness %q",
					extra, sc.Card.PublicKey[:8], sc.Card.Harness)
			}
			if !strings.Contains(err.Error(), "does not match schema") {
				t.Fatalf("refused, but not by the schema step: %v", err)
			}
		})
	}
}

type cardCase struct {
	Name     string `json:"name"`
	Envelope string `json:"envelope"`
	FailsAt  int    `json:"fails_at"`
}

// loadCardCases reads the agent-card.md vectors P1-P3 and N1-N19 from
// tools/verifyvectors/vectors.json, where they are transcribed in full.
func loadCardCases(t *testing.T) []cardCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tools", "verifyvectors", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		AgentCard struct {
			Cases []cardCase `json:"cases"`
		} `json:"agent_card"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.AgentCard.Cases) != 22 {
		t.Fatalf("want 22 agent_card cases (P1-P3, N1-N19), got %d", len(v.AgentCard.Cases))
	}
	return v.AgentCard.Cases
}

// A2: P1 and P2 verify; each N vector is refused at the step agent-card.md
// names (R55-F13 A2: N16 for its size at step 1, N17 for 33 skills at step 5).
func TestNegativeVectors(t *testing.T) {
	for _, c := range loadCardCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			sc, err := Verify([]byte(c.Envelope))
			if c.FailsAt == 0 {
				if err != nil {
					t.Fatalf("%s refused: %v", c.Name, err)
				}
				wantName := `Ada "test" <é>`
				if c.Name == "P3" {
					wantName = p3Name
				}
				if sc.Card.Name != wantName || sc.Card.PublicKey != "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg" {
					t.Fatalf("%s card = %+v", c.Name, sc.Card)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted; must fail at step %d", c.FailsAt)
			}
			doc, perr := ParseStrict([]byte(c.Envelope))
			var cerr error
			if perr == nil {
				_, cerr = CanonicalValue(doc)
			}
			switch c.FailsAt {
			case 1:
				if c.Name == "N16" {
					if len(c.Envelope) != 16727 || !strings.Contains(err.Error(), "16727 bytes, over the limit of 16384") {
						t.Fatalf("want a size refusal of 16727 bytes, got %v", err)
					}
					return
				}
				if perr == nil && cerr == nil {
					t.Fatalf("step 1 passes (ParseStrict and rule 4); Verify said %v", err)
				}
				if c.Name != "N15" && perr == nil {
					t.Fatalf("ParseStrict itself must refuse %s", c.Name)
				}
			case 2:
				if perr != nil || cerr != nil || !strings.Contains(err.Error(), "strict base64url") {
					t.Fatalf("want a step 2 refusal, got %v (parse %v, rule 4 %v)", err, perr, cerr)
				}
			case 5:
				if c.Name == "N18" || c.Name == "N19" { // R55-F10: the text rule
					if perr != nil || cerr != nil || !strings.Contains(err.Error(), "contains a bidi control or line separator") {
						t.Fatalf("want a step 5 text refusal, got %v", err)
					}
					return
				}
				if perr != nil || cerr != nil || !strings.Contains(err.Error(), "does not match schema") {
					t.Fatalf("want a step 5 refusal, got %v", err)
				}
			default:
				t.Fatalf("unexpected fails_at %d", c.FailsAt)
			}
		})
	}
	// validate() refuses a public_key of N13's form (an embedded LF).
	var n13 cardCase
	for _, c := range loadCardCases(t) {
		if c.Name == "N13" {
			n13 = c
		}
	}
	doc, err := ParseStrict([]byte(n13.Envelope))
	if err != nil {
		t.Fatal(err)
	}
	pk := doc.(map[string]any)["card"].(map[string]any)["public_key"].(string)
	if !strings.Contains(pk, "\n") {
		t.Fatalf("N13 key has no LF: %q", pk)
	}
	if _, err := b64.DecodeString(pk); err != nil {
		t.Fatalf("precondition: RawURLEncoding.Strict() is expected to skip the LF: %v", err)
	}
	c := Card{Version: 1, Name: "n", PublicKey: pk, Harness: "h", Skills: []Skill{}, Created: "2026-01-02T03:04:05Z"}
	if err := c.validate(false); err == nil {
		t.Fatal("validate accepted a public_key with an embedded LF")
	}
}

// A3: rule 7 applies to member names and nested values, in either hex case,
// and an escaped backslash followed by "ud800" is not an escape.
func TestParseStrictSurrogates(t *testing.T) {
	refused := map[string]string{
		"lone high in name":     `{"\ud800":1}`,
		"lone low in name":      `{"a\udc00b":1}`,
		"nested value":          `{"a":[{"b":"x\ud800"}]}`,
		"upper case hex":        `{"a":"\uD800"}`,
		"low before high":       `{"a":"\udc00\ud800"}`,
		"high then non-low":     `{"a":"` + esc("d800") + esc("0041") + `"}`,
		"high then literal":     `{"a":"\ud800A"}`,
		"high then high":        `{"a":"\ud800\ud800"}`,
		"high at end of string": `{"a":"\ud800"}`,
		"top-level string":      `"\udfff"`,
	}
	for name, in := range refused {
		if _, err := ParseStrict([]byte(in)); err == nil || !strings.Contains(err.Error(), "lone surrogate") {
			t.Errorf("%s: %s: got %v, want a lone surrogate refusal", name, in, err)
		}
	}
	accepted := map[string]string{
		"escaped backslash":     `{"a":"\\ud800"}`,
		"escaped quote then u":  `{"a":"\"ud800"}`,
		"pair lower":            `{"a":"` + esc("d83d") + esc("de00") + `"}`,
		"pair upper":            `{"a":"` + esc("D83D") + esc("DE00") + `"}`,
		"non-surrogate escape":  `{"a":"` + esc("00e9") + esc("0041") + `"}`,
		"outside string (text)": `{"\\":"\\u"}`,
	}
	for name, in := range accepted {
		if _, err := ParseStrict([]byte(in)); err != nil {
			t.Errorf("%s: %s refused: %v", name, in, err)
		}
	}
	v, err := ParseStrict([]byte(`{"a":"\\ud800"}`))
	if err != nil || v.(map[string]any)["a"] != `\ud800` {
		t.Fatalf("escaped backslash: got %#v, %v", v, err)
	}
	v, err = ParseStrict([]byte(`{"a":"` + esc("d83d") + esc("de00") + `"}`))
	if err != nil || v.(map[string]any)["a"] != string(rune(0x1F600)) {
		t.Fatalf("pair: got %#v, %v", v, err)
	}
	for _, in := range refused {
		// The legacy parse (RescueStored only) still maps them to U+FFFD.
		if _, err := parseLegacy([]byte(in)); err != nil {
			t.Errorf("parseLegacy(%s): %v", in, err)
		}
	}
}

// esc returns the six-character JSON escape of the code unit hex. Test
// sources build escapes this way so that no tool rewrites them.
func esc(hex string) string {
	return string(rune(0x5C)) + "u" + hex
}

func TestDecodeStrict(t *testing.T) {
	good := "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	if _, err := decodeStrict(good, 32); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]string{
		"trailing bits": good[:42] + "h",
		"padding":       good[:42] + "=",
		"LF":            good[:22] + "\n" + good[22:42],
		"CR":            good[:22] + "\r" + good[22:42],
		"std alphabet":  good[:4] + "+" + good[5:],
		"short":         good[:42],
		"long":          good + "A",
	} {
		if _, err := decodeStrict(s, 32); err == nil {
			t.Errorf("%s: accepted %q", name, s)
		}
	}
}

// RescueStored keeps exactly {card, signature}, reads the stored bytes with
// the legacy parse, and verifies under the current rules.
func TestRescueStored(t *testing.T) {
	cases := loadCardCases(t)
	byName := map[string]string{}
	for _, c := range cases {
		byName[c.Name] = c.Envelope
	}
	key := "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	want, err := StoredForm([]byte(byName["P1"]))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"P1", "N6", "N7", "N8", "N9", "N15"} {
		got, sc, err := RescueStored([]byte(byName[n]), key)
		if err != nil || string(got) != string(want) || sc == nil || sc.Card.PublicKey != key {
			t.Errorf("%s: got %s, %v; want %s", n, got, err, want)
		}
	}
	for _, n := range []string{"N1", "N2", "N3", "N4", "N5", "N10", "N11", "N12", "N13", "N14"} {
		if got, _, err := RescueStored([]byte(byName[n]), key); err == nil {
			t.Errorf("%s: rescued as %s", n, got)
		}
	}
	if _, _, err := RescueStored([]byte(byName["P1"]), "Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc"); err == nil {
		t.Error("rescued a card for another key")
	}
}
