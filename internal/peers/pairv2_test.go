package peers

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// The vectors of Docs/protocol/pairing.md §Test vectors.
const (
	vecLookup = "7KQ2M"
	vecSecret = "9XHF4TRW8N"
	vecCardI  = `{"card":{"created":"2026-01-02T03:04:05Z","harness":"custom","name":"Ada \"test\" <é>","public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","skills":[{"description":"a/b & c","id":"review","name":"Code review"}],"version":1},"signature":"XN3GYSED9twF4mei-x7TUzHYzOMQU7aonCRQkebGdcXr8MvkkjLQVjZmtPiCNLTNigKIskMMBqF9hgQW5jdPDA"}`
	vecCardR  = `{"card":{"created":"2026-01-02T03:05:00Z","harness":"claude-code","name":"bob-laptop","public_key":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc","skills":[],"version":1},"signature":"iQYZKqktLeJmQ-5oOyZGlE0x0mOIQig-IehHbzqfg_vequeSqg_q1jHkj4up6ZEx3D6_la-yxcDzfWqaD0eHAg"}`
	vecMboxI  = `{"announcement":{"created":"2026-01-02T03:00:00Z","identity":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","key_id":"67ca2ffd6fe9efab","not_after":"2026-01-16T03:00:00Z","pub":"eaYx7t4b-cmPEgMs3q3Q56B5OY_HhriMyEbsia-FpRo","v":1},"signature":"atOsE_ZJ-DFU33ceBR82Ws02HDvWF_Rslw6OqGxYgSQLn3E973R7yQ3doveCNdVLVitHrc7wksmLa7qIx50OBA"}`
	vecMboxR  = `{"announcement":{"created":"2026-01-02T03:00:00Z","identity":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc","key_id":"16786d4e5ef74411","not_after":"2026-01-16T03:00:00Z","pub":"Z13VdO13iTELPS52gfN5C0ZsdzsVIf7PNld5WDcepS8","v":1},"signature":"pwWy0FSMPGVng-ss-MTEcBceJ7Rb0-fmFI3gxD_iNAlKDMN6xPecIXLBE7IEu3uhrhxeHUydaM3f1A0wwFvKBQ"}`

	vecSalt  = "646f72796c696e61652d706169722d76320a374b51324d"
	vecK     = "5f88441eb745d2b0e0b7fa7dc82a9fecc174bda5e24129e133f7588830b07022"
	vecT     = "f59103e3521337c0e8fb3a0768907d916b77ad1e8128ba946bb839f50dfd5de1"
	vecTagI  = "2f507289395dea825a26c1327d1f0cbc6b83792d543af6286e540d6a40a9ecee"
	vecTagR  = "c645c221f9ca3c0e1ded71f2bc03294b7a0dc158245da4f83ebe211769dd69b9"
	vecConfR = `eyJsb29rdXAiOiI3S1EyTSIsInRhZyI6InhrWENJZm5LUEE0ZDdYSHl2QU1wUzNvTndWZ2tYYVQ0UHI0aEYybmRhYmsiLCJ2IjoyfQ==`
	vecConfI = `eyJsb29rdXAiOiI3S1EyTSIsInRhZyI6IkwxQnlpVGxkNm9KYUpzRXlmUjhNdkd1RGVTMVVPdllvYmxRTmFrQ3A3TzQiLCJ2IjoyfQ==`
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPairingVectors(t *testing.T) {
	// The vector cards and announcements are valid and already canonical.
	for name, raw := range map[string]string{"card_I": vecCardI, "card_R": vecCardR} {
		if _, err := agentcard.Verify([]byte(raw)); err != nil {
			t.Fatalf("%s does not verify: %v", name, err)
		}
	}
	now := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	keyI, keyR := "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg", "Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc"
	if _, canon, err := mail.ParseAnnouncement([]byte(vecMboxI), keyI, now); err != nil || string(canon) != vecMboxI {
		t.Fatalf("mbox_I: %v (canonical form differs: %v)", err, string(canon) != vecMboxI)
	}
	if _, canon, err := mail.ParseAnnouncement([]byte(vecMboxR), keyR, now); err != nil || string(canon) != vecMboxR {
		t.Fatalf("mbox_R: %v (canonical form differs: %v)", err, string(canon) != vecMboxR)
	}

	// canonicalPart reproduces the byte strings from the received JSON, including
	// after the relay's re-encoding (compaction and HTML escaping).
	bs := string(rune(92)) // encoding/json writes < > & as backslash-u escapes
	relayed := strings.NewReplacer("<", bs+"u003c", ">", bs+"u003e", "&", bs+"u0026").Replace(vecCardI)
	if relayed == vecCardI {
		t.Fatal("test bug: nothing was escaped")
	}
	for _, raw := range []string{vecCardI, relayed, `{"extra":1,` + vecCardI[1:]} {
		got, err := canonicalPart([]byte(raw), "card")
		if err != nil || string(got) != vecCardI {
			t.Fatalf("canonicalPart(card_I) = %q, %v", got, err)
		}
	}
	if len(vecCardI) != 333 {
		t.Errorf("card_I is %d bytes, the spec says 333", len(vecCardI))
	}

	if got := hex.EncodeToString([]byte(saltPrefix + vecLookup)); got != vecSalt {
		t.Fatalf("salt = %s", got)
	}
	k := deriveK(vecLookup, []byte(vecSecret))
	if hex.EncodeToString(k) != vecK {
		t.Fatalf("K = %x", k)
	}
	tr := transcript(vecLookup, []byte(vecCardI), []byte(vecCardR), []byte(vecMboxI), []byte(vecMboxR))
	if hex.EncodeToString(tr[:]) != vecT {
		t.Fatalf("T = %x", tr)
	}
	tagI, tagR := issuerTag(k, tr), redeemerTag(k, tr)
	if hex.EncodeToString(tagI) != vecTagI || hex.EncodeToString(tagR) != vecTagR {
		t.Fatalf("tags = %x, %x", tagI, tagR)
	}
	if got := base64.StdEncoding.EncodeToString(confirmPayload(vecLookup, tagR)); got != vecConfR {
		t.Errorf("redeemer payload = %s", got)
	}
	if got := base64.StdEncoding.EncodeToString(confirmPayload(vecLookup, tagI)); got != vecConfI {
		t.Errorf("issuer payload = %s", got)
	}
	// And the payloads parse back.
	if lk, tg, err := parseConfirm(confirmPayload(vecLookup, tagR)); err != nil || lk != vecLookup || !bytes.Equal(tg, tagR) {
		t.Errorf("parseConfirm = %q, %x, %v", lk, tg, err)
	}

	// Negative checks from the spec.
	if bytes.Equal(tagFor(k, roleRedeemerTag, tr), tagI) {
		t.Error("swapping the roles gives the same tag")
	}
	for _, mut := range []struct {
		name string
		part int
	}{{"card_R", 1}, {"mbox_R", 3}} {
		parts := [][]byte{[]byte(vecCardI), []byte(vecCardR), []byte(vecMboxI), []byte(vecMboxR)}
		for i := range parts[mut.part] {
			c := bytes.Clone(parts[mut.part])
			c[i] ^= 1
			p2 := [][]byte{parts[0], parts[1], parts[2], parts[3]}
			p2[mut.part] = c
			t2 := transcript(vecLookup, p2[0], p2[1], p2[2], p2[3])
			if bytes.Equal(redeemerTag(k, t2), tagR) {
				t.Fatalf("changing byte %d of %s did not change the tag", i, mut.name)
			}
		}
	}
	if bytes.Equal(deriveK(vecLookup, []byte("9XHF4TRW8P")), k) {
		t.Error("a different secret gave the same K")
	}
	if bytes.Equal(deriveK("7KQ2N", []byte(vecSecret)), k) {
		t.Error("a different lookup gave the same K")
	}
	if len(sha256.Sum256(nil)) != tagLen {
		t.Error("tag length")
	}
}

func TestCodeFormat(t *testing.T) {
	for i := 0; i < 50; i++ {
		c, err := NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != CodeLen || strings.Trim(c, envelope.PairAlphabet) != "" {
			t.Fatalf("code %q is not 15 characters of the alphabet", c)
		}
	}
	if got := FormatCode("7KQ2M9XHF4TRW8N"); got != "7KQ2M-9XHF4-TRW8N" {
		t.Errorf("FormatCode = %q", got)
	}
	cases := []struct {
		in   string
		want string
		v2   bool
		ok   bool
	}{
		{"7kq2m-9xhf4-trw8n", "7KQ2M9XHF4TRW8N", true, true},
		{"7KQ2M 9XHF4 TRW8N", "7KQ2M9XHF4TRW8N", true, true},
		{"OIL2M9XHF4TRW8N", "011" + "2M9XHF4TRW8N", true, true},
		{"7KQ2M-9XHF4", "7KQ2M9XHF4", false, true},
		{"7KQ2M9XHF4TRW8", "", false, false},
		{"7KQ2M9XHF4TRW8NN", "", false, false},
		{"7KQ2M9XHF4TRW8U", "", false, false}, // U is not in the alphabet
		{"", "", false, false},
	}
	for _, c := range cases {
		got, v2, ok := NormalizeCode(c.in)
		if got != c.want || v2 != c.v2 || ok != c.ok {
			t.Errorf("NormalizeCode(%q) = %q, %v, %v", c.in, got, v2, ok)
		}
	}
}

func TestParseConfirmIsStrict(t *testing.T) {
	tag := bytes.Repeat([]byte{7}, 32)
	good := string(confirmPayload("7KQ2M", tag))
	if _, _, err := parseConfirm([]byte(good)); err != nil {
		t.Fatal(err)
	}
	enc := b64u.EncodeToString(tag)
	nonCanonical := enc[:len(enc)-1] + "B" // same 32 bytes decode, but the last character is not the canonical one
	for name, p := range map[string]string{
		"not json":          `nope`,
		"array":             `[]`,
		"missing lookup":    `{"tag":"` + enc + `","v":2}`,
		"extra member":      `{"extra":1,"lookup":"7KQ2M","tag":"` + enc + `","v":2}`,
		"v is 1":            `{"lookup":"7KQ2M","tag":"` + enc + `","v":1}`,
		"v is a string":     `{"lookup":"7KQ2M","tag":"` + enc + `","v":"2"}`,
		"padded tag":        `{"lookup":"7KQ2M","tag":"` + enc + `=","v":2}`,
		"standard alphabet": `{"lookup":"7KQ2M","tag":"` + strings.Repeat("+", 43) + `","v":2}`,
		"short tag":         `{"lookup":"7KQ2M","tag":"` + enc[:40] + `","v":2}`,
		"duplicate key":     `{"lookup":"7KQ2M","lookup":"7KQ2M","tag":"` + enc + `","v":2}`,
		"non-canonical b64": `{"lookup":"7KQ2M","tag":"` + nonCanonical + `","v":2}`,
		"invalid utf8":      "{\"lookup\":\"\xff\",\"tag\":\"" + enc + "\",\"v\":2}",
	} {
		if _, _, err := parseConfirm([]byte(p)); err == nil {
			t.Errorf("%s: accepted %s", name, p)
		}
	}
}
