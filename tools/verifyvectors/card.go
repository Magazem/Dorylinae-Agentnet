package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// agentCardVectors is Docs/protocol/agent-card.md §Negative test vectors and
// §Size vectors: P1 and P2 (fails_at 0, accepted) and N1-N17, each with the
// first Verification step that must refuse it.
type agentCardVectors struct {
	Cases []struct {
		Name     string `json:"name"`
		Envelope string `json:"envelope"`
		FailsAt  int    `json:"fails_at"`
	} `json:"cases"`
}

// cardStepErr is a refusal at a Verification step of agent-card.md.
type cardStepErr struct {
	step int
	why  string
}

func (e *cardStepErr) Error() string { return fmt.Sprintf("step %d: %s", e.step, e.why) }

func refuse(step int, format string, a ...any) error {
	return &cardStepErr{step, fmt.Sprintf(format, a...)}
}

// agentCard runs this tool's own reading of agent-card.md §Verification on
// every case and checks that it stops at the stated step.
func agentCard(c *checker, v *vectors) {
	cases := v.AgentCard.Cases
	c.ok("agent_card cases present", len(cases) == 19, fmt.Sprintf("%d cases, want 19 (P1, P2, N1-N17)", len(cases)))
	for _, tc := range cases {
		name, err := verifyCardEnvelope([]byte(tc.Envelope))
		got := 0
		var se *cardStepErr
		if errors.As(err, &se) {
			got = se.step
		}
		detail := "accepted"
		if err != nil {
			detail = err.Error()
		}
		label := fmt.Sprintf("agent_card %s fails at step %d", tc.Name, tc.FailsAt)
		if tc.FailsAt == 0 {
			label = fmt.Sprintf("agent_card %s verifies", tc.Name)
		}
		c.ok(label, got == tc.FailsAt, fmt.Sprintf("got step %d (%s)", got, detail))
		if tc.FailsAt == 0 && err == nil {
			c.eqs("agent_card "+tc.Name+" name", name, `Ada "test" <é>`)
		}
	}
}

// verifyCardEnvelope returns the card name, or a *cardStepErr naming the step.
func verifyCardEnvelope(env []byte) (string, error) {
	// Step 1: the size, before any parsing (agent-card.md §Size).
	if len(env) > maxCardBytes {
		return "", refuse(1, "%d bytes, over %d", len(env), maxCardBytes)
	}
	// Then the strict parse of the whole envelope, then rule 4 on every number
	// (canonicalise it once and discard the result).
	doc, err := parseDoc(env)
	if err != nil {
		return "", refuse(1, "%v", err)
	}
	if err := writeValue(&bytes.Buffer{}, doc); err != nil {
		return "", refuse(1, "%v", err)
	}
	top, ok := doc.([]member)
	if !ok {
		return "", refuse(1, "not an object")
	}
	card, ok := lookup(top, "card").([]member)
	if !ok {
		return "", refuse(1, "no card object")
	}
	sigText, ok := lookup(top, "signature").(string)
	if !ok {
		return "", refuse(1, "no signature string")
	}
	// Step 2: strict base64url, checked on the string.
	keyText, _ := lookup(card, "public_key").(string)
	pub, err := b64Exact(keyText, ed25519.PublicKeySize)
	if err != nil {
		return "", refuse(2, "public_key: %v", err)
	}
	sig, err := b64Exact(sigText, ed25519.SignatureSize)
	if err != nil {
		return "", refuse(2, "signature: %v", err)
	}
	// Step 3: canonical form of the generic card.
	var canon bytes.Buffer
	if err := writeValue(&canon, card); err != nil {
		return "", refuse(3, "%v", err)
	}
	// Step 4.
	if !ed25519.Verify(pub, append([]byte("dorylinae-agent-card-v1\n"), canon.Bytes()...), sig) {
		return "", refuse(4, "signature does not verify")
	}
	// Step 5: schema by exact member name.
	if err := cardSchema(card); err != nil {
		return "", refuse(5, "%v", err)
	}
	name, _ := lookup(card, "name").(string)
	return name, nil
}

// lookup returns the value of the member named exactly k (nil if absent).
func lookup(ms []member, k string) any {
	for _, m := range ms {
		if m.key == k {
			return m.val
		}
	}
	return nil
}

// Limits of agent-card.md §Size.
const (
	maxCardBytes = 16384
	maxSkills    = 32
)

const b64uChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

// b64Exact decodes strict base64url of exactly n bytes. The decoder in the
// standard library skips CR and LF even in Strict mode, so the length, the
// alphabet and the zero trailing bits are checked here first.
func b64Exact(s string, n int) ([]byte, error) {
	chars := (8*n + 5) / 6
	if len(s) != chars {
		return nil, fmt.Errorf("length %d, want %d", len(s), chars)
	}
	if i := strings.IndexFunc(s, func(r rune) bool { return !strings.ContainsRune(b64uChars, r) }); i >= 0 {
		return nil, fmt.Errorf("byte %d outside the alphabet", i)
	}
	spare := chars*6 - 8*n
	if strings.IndexByte(b64uChars, s[chars-1])&(1<<spare-1) != 0 {
		return nil, errors.New("trailing bits are not zero")
	}
	return base64.RawURLEncoding.DecodeString(s)
}

var createdPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

// cardSchema is agent-card.md §Card, read from the generic object.
func cardSchema(card []member) error {
	if err := onlyMembers(card, "created", "harness", "name", "public_key", "skills", "version"); err != nil {
		return err
	}
	if n, ok := lookup(card, "version").(json.Number); !ok || n.String() != "1" {
		return errors.New("version is not the integer 1")
	}
	for _, k := range []string{"name", "harness"} {
		if err := isText(lookup(card, k), 1); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	skills, ok := lookup(card, "skills").([]any)
	if !ok {
		return errors.New("skills is not an array")
	}
	if len(skills) > maxSkills {
		return fmt.Errorf("%d skills, at most %d", len(skills), maxSkills)
	}
	for i, s := range skills {
		sk, ok := s.([]member)
		if !ok {
			return fmt.Errorf("skills[%d] is not an object", i)
		}
		if err := onlyMembers(sk, "description", "id", "name"); err != nil {
			return fmt.Errorf("skills[%d]: %w", i, err)
		}
		for k, lo := range map[string]int{"id": 1, "name": 1, "description": 0} {
			if err := isText(lookup(sk, k), lo); err != nil {
				return fmt.Errorf("skills[%d].%s: %w", i, k, err)
			}
		}
	}
	created, _ := lookup(card, "created").(string)
	t, err := time.Parse(time.RFC3339, created)
	if !createdPattern.MatchString(created) || err != nil || t.UTC().Format(time.RFC3339) != created {
		return fmt.Errorf("created %q is not YYYY-MM-DDThh:mm:ssZ", created)
	}
	return nil
}

// onlyMembers requires exactly the given member names (compared exactly).
func onlyMembers(ms []member, names ...string) error {
	if len(ms) != len(names) {
		return fmt.Errorf("%d members, want %d", len(ms), len(names))
	}
	for _, m := range ms {
		found := false
		for _, n := range names {
			found = found || m.key == n
		}
		if !found {
			return fmt.Errorf("unexpected member %q", m.key)
		}
	}
	return nil
}

// isText is agent-card.md "text": lo..128 code points, no Cc, no U+FFFD.
func isText(v any, lo int) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("not a string")
	}
	if n := utf8.RuneCountInString(s); n < lo || n > 128 {
		return fmt.Errorf("%d code points", n)
	}
	for _, r := range s {
		if r < 0x20 || (r >= 0x7F && r < 0xA0) || r == 0xFFFD {
			return fmt.Errorf("forbidden code point U+%04X", r)
		}
	}
	return nil
}
