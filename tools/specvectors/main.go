// Command specvectors prints the test vectors published in
// Docs/protocol/pairing.md (pairing v2, fingerprints) and
// Docs/protocol/mail.md (mailbox announcements, sealed mail), the grant
// vector of Docs/protocol/grant.md, the audit chain of Docs/protocol/audit.md,
// the debate commitment of Docs/protocol/debate.md, the Decision of
// Docs/protocol/decision.md, relay auth v2 of Docs/protocol/envelope.md and
// the Agent Card size vectors P2, N16 and N17 and charset vectors N18, N19
// and P3 of Docs/protocol/agent-card.md.
//
// Pairing values are deterministic. HPKE sealing draws its ephemeral key from
// crypto/rand, so every run prints a new mail payload; the published payload
// is checked on the Open side with -open <base64 payload>.
//
// It is a spec tool, not production code: it deliberately re-implements the
// constructions from the documents instead of calling the daemon packages
// (apart from agentcard, to sign the reference cards).
package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/hpke"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var b64u = base64.RawURLEncoding

func seq(start byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

// canonical renders v (maps, slices, strings, ints) as canonical JSON. Only
// the value shapes used by the vectors are supported; strings go through
// encoding/json with HTML escaping off, which matches the canonical escaping
// rules for the characters used here.
func canonical(v any) []byte {
	var buf bytes.Buffer
	write(&buf, v)
	return buf.Bytes()
}

func write(buf *bytes.Buffer, v any) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys) // ASCII keys only: byte order == UTF-16 order
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			write(buf, k)
			buf.WriteByte(':')
			write(buf, t[k])
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			write(buf, e)
		}
		buf.WriteByte(']')
	case json.RawMessage:
		buf.Write(t)
	case string:
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(t); err != nil {
			log.Fatal(err)
		}
		buf.Truncate(buf.Len() - 1) // Encode appends '\n'
	case int:
		fmt.Fprintf(buf, "%d", t)
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	default:
		log.Fatalf("canonical: unsupported %T", v)
	}
}

func u32(n int) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(n)) //nolint:gosec // n is a small non-negative length
	return b[:]
}

func crockfordEncode(data []byte) string {
	var out []byte
	var acc uint32
	bits := 0
	for _, b := range data {
		acc = acc<<8 | uint32(b)
		bits += 8
		for bits >= 5 {
			out = append(out, crockford[(acc>>(bits-5))&31])
			bits -= 5
		}
	}
	if bits > 0 {
		out = append(out, crockford[(acc<<(5-bits))&31])
	}
	return string(out)
}

func fingerprint(pub ed25519.PublicKey) string {
	h := sha256.Sum256(append([]byte("dorylinae-fingerprint-v1\n"), pub...))
	fp := crockfordEncode(h[:])[:20]
	return fp[0:4] + " " + fp[4:8] + " " + fp[8:12] + " " + fp[12:16] + " " + fp[16:20]
}

// signedCard returns canonical({"card":...,"signature":...}).
func signedCard(priv ed25519.PrivateKey, name, harness string, skills []agentcard.Skill, created time.Time) []byte {
	c, err := agentcard.New(priv.Public().(ed25519.PublicKey), name, harness, skills, created)
	if err != nil {
		log.Fatal(err)
	}
	s, err := agentcard.Sign(priv, c)
	if err != nil {
		log.Fatal(err)
	}
	canon, err := agentcard.Canonical(c)
	if err != nil {
		log.Fatal(err)
	}
	return canonical(map[string]any{"card": json.RawMessage(canon), "signature": s.Signature})
}

type mbox struct {
	priv     *ecdh.PrivateKey
	keyID    []byte
	signed   []byte // canonical signed announcement
	announce []byte // canonical announcement
}

func announcement(id ed25519.PrivateKey, mboxSeed []byte, created, notAfter string) mbox {
	priv, err := ecdh.X25519().NewPrivateKey(mboxSeed)
	if err != nil {
		log.Fatal(err)
	}
	pub := priv.PublicKey().Bytes()
	h := sha256.Sum256(pub)
	keyID := h[:8]
	a := canonical(map[string]any{
		"v":         1,
		"identity":  b64u.EncodeToString(id.Public().(ed25519.PublicKey)),
		"key_id":    hex.EncodeToString(keyID),
		"pub":       b64u.EncodeToString(pub),
		"created":   created,
		"not_after": notAfter,
	})
	sig := ed25519.Sign(id, append([]byte("dorylinae-mailbox-key-v1\n"), a...))
	s := canonical(map[string]any{"announcement": json.RawMessage(a), "signature": b64u.EncodeToString(sig)})
	return mbox{priv: priv, keyID: keyID, signed: s, announce: a}
}

func main() {
	open := flag.String("open", "", "open this base64 mail payload with the vector recipient key instead of sealing")
	flag.Parse()

	privI := ed25519.NewKeyFromSeed(seq(0x00))
	privR := ed25519.NewKeyFromSeed(seq(0x20))
	keyI := b64u.EncodeToString(privI.Public().(ed25519.PublicKey))
	keyR := b64u.EncodeToString(privR.Public().(ed25519.PublicKey))

	cardI := signedCard(privI, `Ada "test" <é>`, "custom",
		[]agentcard.Skill{{ID: "review", Name: "Code review", Description: "a/b & c"}},
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	cardR := signedCard(privR, "bob-laptop", "claude-code", nil, time.Date(2026, 1, 2, 3, 5, 0, 0, time.UTC))

	mbI := announcement(privI, seq(0x40), "2026-01-02T03:00:00Z", "2026-01-16T03:00:00Z")
	mbR := announcement(privR, seq(0x60), "2026-01-02T03:00:00Z", "2026-01-16T03:00:00Z")

	if *open != "" {
		openMail(*open, privI, keyI, keyR, mbR)
		return
	}

	fmt.Println("== identities")
	fmt.Println("key_I       ", keyI)
	fmt.Println("key_R       ", keyR)
	fmt.Println("fp(key_I)   ", fingerprint(privI.Public().(ed25519.PublicKey)))
	fmt.Println("fp(key_R)   ", fingerprint(privR.Public().(ed25519.PublicKey)))

	fmt.Println("== mailbox announcements")
	for _, m := range []struct {
		n string
		m mbox
	}{{"I", mbI}, {"R", mbR}} {
		fmt.Printf("mbox_%s priv  %x\n", m.n, m.m.priv.Bytes())
		fmt.Printf("mbox_%s pub   %x\n", m.n, m.m.priv.PublicKey().Bytes())
		fmt.Printf("mbox_%s keyid %x\n", m.n, m.m.keyID)
		fmt.Printf("mbox_%s announcement %s\n", m.n, m.m.announce)
		fmt.Printf("mbox_%s signed %s\n", m.n, m.m.signed)
	}

	fmt.Println("== pairing v2")
	lookup, secret := "7KQ2M", "9XHF4TRW8N" //nolint:gosec // published test vector, not a credential
	fmt.Println("code        ", lookup+"-"+secret[:5]+"-"+secret[5:])
	fmt.Printf("card_I      %s\n", cardI)
	fmt.Printf("card_R      %s\n", cardR)
	fmt.Println("len card_I  ", len(cardI), " len card_R ", len(cardR), " len mbox_I ", len(mbI.signed), " len mbox_R ", len(mbR.signed))

	salt := append([]byte("dorylinae-pair-v2\n"), lookup...)
	k := argon2.IDKey([]byte(secret), salt, 3, 64*1024, 1, 32)
	fmt.Printf("salt        %x\n", salt)
	fmt.Printf("K           %x\n", k)

	var t []byte
	t = append(t, "dorylinae-pair-v2-transcript\n"...)
	t = append(t, lookup...)
	for _, part := range [][]byte{cardI, cardR, mbI.signed, mbR.signed} {
		t = append(t, u32(len(part))...)
		t = append(t, part...)
	}
	T := sha256.Sum256(t)
	fmt.Printf("T input len %d\n", len(t))
	fmt.Printf("T           %x\n", T)
	tag := func(role string) []byte {
		m := hmac.New(sha256.New, k)
		m.Write([]byte(role + "\n"))
		m.Write(T[:])
		return m.Sum(nil)
	}
	tagI, tagR := tag("issuer"), tag("redeemer")
	fmt.Printf("tag_I       %x\n", tagI)
	fmt.Printf("tag_I b64u  %s\n", b64u.EncodeToString(tagI))
	fmt.Printf("tag_R       %x\n", tagR)
	fmt.Printf("tag_R b64u  %s\n", b64u.EncodeToString(tagR))
	for _, c := range []struct {
		role string
		tag  []byte
	}{{"redeemer", tagR}, {"issuer", tagI}} {
		confirm := canonical(map[string]any{"v": 2, "lookup": lookup, "tag": b64u.EncodeToString(c.tag)})
		fmt.Printf("pair.confirm plaintext (%s) %s\n", c.role, confirm)
		fmt.Printf("pair.confirm payload (%s)   %s\n", c.role, base64.StdEncoding.EncodeToString(confirm))
	}

	fmt.Println("== mail (seal, random ephemeral)")
	plain, info, aad := mailInputs(privI, keyI, keyR, mbR)
	fmt.Printf("signed plaintext %s\n", plain)
	fmt.Printf("info  %x\n", info)
	fmt.Printf("aad   %x (%s)\n", aad, aad)
	pk, err := hpke.NewDHKEMPublicKey(mbR.priv.PublicKey())
	if err != nil {
		log.Fatal(err)
	}
	enc, s, err := hpke.NewSender(pk, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), info)
	if err != nil {
		log.Fatal(err)
	}
	ct, err := s.Seal(aad, plain)
	if err != nil {
		log.Fatal(err)
	}
	payload := append(append(append([]byte{0x01}, mbR.keyID...), enc...), ct...)
	fmt.Printf("enc   %x\n", enc)
	fmt.Printf("ct    %x\n", ct)
	fmt.Printf("payload (base64) %s\n", base64.StdEncoding.EncodeToString(payload))

	fmt.Println("== capability grant (2.2b, grant.md §Test vectors)")
	printGrantVector(privI, keyI, keyR)

	fmt.Println("== audit chain (3.6a, audit.md §Vector)")
	printAuditVector()
	fmt.Println("== debate commitment (3.1a, debate.md §Commit-reveal)")
	printDebateVector(keyI, keyR)
	fmt.Println("== decision (3.3a, decision.md §Vector)")
	printDecisionVector(privI, privR, keyI, keyR)
	fmt.Println("== relay auth v2 (4.0a, envelope.md §Relay auth v2 vector)")
	printRelayAuthVector(privI, keyI)
	fmt.Println("== agent card size vectors (R55-F13, agent-card.md §Size vectors)")
	printCardSizeVectors(privI, cardI)
	fmt.Println("== agent card charset vectors (R55-F10, agent-card.md §Charset vectors)")
	printCardCharsetVectors(privI)
}

// printCardCharsetVectors prints N18 (U+202E in the name, refused at step 5),
// N19 (U+2028 in a skill description, refused at step 5) and P3 (an emoji ZWJ
// sequence and VS16 in the name, accepted) of agent-card.md §Charset vectors,
// each as the {name, envelope, fails_at} case of
// tools/verifyvectors/vectors.json. The card is the Test vector card with one
// text member changed, signed with ed25519 directly (agentcard.New refuses
// N18 and N19). In the envelope every non-ASCII rune that is not a letter is
// written as a JSON escape, so the published line holds no raw bidi control
// or line separator (review 82b F8); the signature covers the canonical form,
// which rule 7 reads the same.
func printCardCharsetVectors(privI ed25519.PrivateKey) {
	card := func(name, desc string) []byte {
		return canonical(map[string]any{
			"version": 1, "name": name, "harness": "custom", "created": "2026-01-02T03:04:05Z",
			"public_key": b64u.EncodeToString(privI.Public().(ed25519.PublicKey)),
			"skills":     []any{map[string]any{"id": "review", "name": "Code review", "description": desc}},
		})
	}
	for _, c := range []struct {
		name, cardName, desc string
		failsAt              int
	}{
		{"N18", "Ada \u202Etset", "a/b & c", 5},
		{"N19", `Ada "test" <é>`, "a/b\u2028c", 5},
		{"P3", "Ada \U0001F469\u200D\U0001F4BB \u2764\uFE0F", "a/b & c", 0},
	} {
		// canonical writes strings with encoding/json, which escapes U+2028;
		// the canonical form (agent-card.md rule 3) writes it raw.
		cc := bytes.ReplaceAll(card(c.cardName, c.desc), []byte(`\u2028`), []byte("\u2028"))
		sig := b64u.EncodeToString(ed25519.Sign(privI, append([]byte("dorylinae-agent-card-v1\n"), cc...)))
		env := escapeNonLetters(canonical(map[string]any{"card": json.RawMessage(cc), "signature": sig}))
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(map[string]any{"name": c.name, "envelope": string(env), "fails_at": c.failsAt}); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("len %s card %d\n", c.name, len(cc))
		fmt.Print(buf.String())
	}
}

// escapeNonLetters writes every non-ASCII rune of b that is not a letter as
// a lowercase JSON escape (a surrogate pair above U+FFFF). b is canonical
// JSON, where such runes occur only inside strings.
func escapeNonLetters(b []byte) []byte {
	var out bytes.Buffer
	for _, r := range string(b) {
		switch {
		case r < utf8.RuneSelf || unicode.IsLetter(r):
			out.WriteRune(r)
		case r > 0xffff:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&out, `\u%04x\u%04x`, hi, lo)
		default:
			fmt.Fprintf(&out, `\u%04x`, r)
		}
	}
	return out.Bytes()
}

// printCardSizeVectors prints P2 (32 skills), N16 (P1, the Test vector card
// cardI with a note of 16384 "a", over MaxCardBytes) and N17 (33 skills) of agent-card.md §Size vectors, each
// as the {name, envelope, fails_at} case of tools/verifyvectors/vectors.json.
// The cards are signed here with ed25519 directly: agentcard.New and Sign
// refuse N17's 33 skills.
func printCardSizeVectors(privI ed25519.PrivateKey, cardI []byte) {
	card := func(n int) []byte {
		skills := make([]any, n)
		for i := range skills {
			skills[i] = map[string]any{"id": fmt.Sprintf("s%02d", i+1), "name": fmt.Sprintf("Skill %02d", i+1), "description": ""}
		}
		return canonical(map[string]any{
			"version": 1, "name": `Ada "test" <é>`, "harness": "custom", "created": "2026-01-02T03:04:05Z",
			"public_key": b64u.EncodeToString(privI.Public().(ed25519.PublicKey)), "skills": skills,
		})
	}
	sign := func(c []byte) string {
		return b64u.EncodeToString(ed25519.Sign(privI, append([]byte("dorylinae-agent-card-v1\n"), c...)))
	}
	for _, c := range []struct {
		name     string
		envelope []byte
		failsAt  int
	}{
		{"P2", canonical(map[string]any{"card": json.RawMessage(card(32)), "signature": sign(card(32))}), 0},
		{"N16", bytes.Replace(cardI, []byte(`,"signature":`), []byte(`,"note":"`+strings.Repeat("a", 16384)+`","signature":`), 1), 1},
		{"N17", canonical(map[string]any{"card": json.RawMessage(card(33)), "signature": sign(card(33))}), 5},
	} {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(map[string]any{"name": c.name, "envelope": string(c.envelope), "fails_at": c.failsAt}); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("len %s %d\n", c.name, len(c.envelope))
		fmt.Print(buf.String())
	}
	fmt.Println("len P2 card", len(card(32)))
}

// printRelayAuthVector prints Docs/protocol/envelope.md §Relay auth v2
// vector: for each relay URL, its origin, the signed message and the
// signature by key_I over nonce seq(0x80).
func printRelayAuthVector(privI ed25519.PrivateKey, keyI string) {
	nonce := seq(0x80)
	fmt.Printf("seed        %x\n", privI.Seed())
	fmt.Printf("key         %s\n", keyI)
	fmt.Printf("nonce b64u  %s\n", b64u.EncodeToString(nonce))
	for i, u := range []string{
		"wss://Relay.Example.COM/v1/connect",
		"wss://relay.example.com:443",
		"wss://[2001:DB8::1]:8443/v1/connect",
		"wss://relay.example.com.:8443",
		"ws://127.0.0.1:8787",
	} {
		origin := relayOrigin(u)
		msg := append([]byte("dorylinae-relay-auth-v2\n"), nonce...)
		msg = binary.BigEndian.AppendUint16(msg, uint16(len(origin))) //nolint:gosec // short test origins
		msg = append(msg, origin...)
		sig := ed25519.Sign(privI, msg)
		fmt.Printf("url         %s\norigin      %s\nmessage     %x\nsignature   %s\n", u, origin, msg, b64u.EncodeToString(sig))
		if i == 0 {
			frame, _ := json.Marshal(struct {
				Op        string `json:"op"`
				V         int    `json:"v"`
				PublicKey string `json:"public_key"`
				Signature string `json:"signature"`
			}{"auth", 2, keyI, b64u.EncodeToString(sig)})
			fmt.Printf("auth frame  %s\n", frame)
		}
	}
}

// relayOrigin is the origin rule of relay-hosted.md §1 for ASCII hosts:
// lowercase scheme and host, no trailing dot, IPv6 in brackets, the default
// port (wss 443, ws 80) omitted, no path.
func relayOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		log.Fatal(err)
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if (scheme == "wss" && port == "443") || (scheme == "ws" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	return scheme + "://" + host
}

// printDebateVector prints the session id and the commitment of
// Docs/protocol/debate.md §Commit-reveal, re-implemented from the text.
func printDebateVector(keyI, keyR string) {
	const reqID = "r-0123456789abcdef0123456789abcdef"
	h := sha256.Sum256([]byte("dorylinae-ws-id-v1\n" + keyI + "\n" + keyR + "\n" + reqID))
	sid := "s-" + hex.EncodeToString(h[:16])
	nonce := hex.EncodeToString(seq(0x40))
	position := canonical(map[string]any{
		"claim":                 "Use capped exponential backoff for outbox retries",
		"assumptions":           []any{"Clock skew between peers is under 5 s"},
		"evidence":              []any{map[string]any{"kind": "file", "ref": "internal/mail/outbox.go"}},
		"rejected_alternatives": []any{map[string]any{"option": "Fixed 30 s retry", "reason": "Floods the relay after an outage"}},
		"argument":              "Retries should back off exponentially, capped at 10 minutes.",
	})
	sum := sha256.Sum256(append([]byte("dorylinae-debate-commit-v1\n"+sid+"\n"+keyI+"\n"+nonce+"\n"), position...))
	fmt.Println("request_id  ", reqID)
	fmt.Println("session     ", sid)
	fmt.Println("nonce       ", nonce)
	fmt.Printf("position    %s\n", position)
	fmt.Printf("commitment  %x\n", sum)
}

// grantDomain and grantSession are Docs/protocol/grant.md's signing domain
// and the session id vector from work-session.md §Session id (not rederived
// here: that derivation is ticket 2.1a's vector).
const (
	grantDomain  = "dorylinae-grant-v1\n"
	grantSession = "s-36375782ceb6baea9cee4d4273dfb035"
)

// printGrantVector prints the canonical grant, its hash and signature, and
// the wire token, for Docs/protocol/grant.md §Test vectors. privI signs as
// the grantor (iss = keyI); keyR is the holder (aud).
func printGrantVector(privI ed25519.PrivateKey, keyI, keyR string) {
	grant := map[string]any{
		"v":       1,
		"id":      "g-00112233445566778899aabbccddeeff",
		"iss":     keyI,
		"aud":     keyR,
		"session": grantSession,
		"action":  "git.read",
		"resource": map[string]any{
			"kind":   "git",
			"label":  "agentnet-3f2a",
			"branch": "feat-x",
		},
		"scope":     "internal/mail",
		"nbf":       "2026-01-02T03:00:00Z",
		"exp":       "2026-01-02T05:00:00Z",
		"sensitive": true,
	}
	canon := canonical(grant)
	sum := sha256.Sum256(canon)
	sig := ed25519.Sign(privI, append([]byte(grantDomain), canon...))
	sigB64 := b64u.EncodeToString(sig)
	token := canonical(map[string]any{"grant": json.RawMessage(canon), "sig": sigB64})
	fmt.Printf("grant canonical %s\n", canon)
	fmt.Printf("grant hash      %x\n", sum)
	fmt.Printf("grant sig       %s\n", sigB64)
	fmt.Printf("token           %s\n", token)
	fmt.Printf("token len       %d\n", len(token))
}

const mailID = "m-0123456789abcdef0123456789abcdef"

func mailInputs(privI ed25519.PrivateKey, keyI, keyR string, mbR mbox) (plain, info, aad []byte) {
	msg := canonical(map[string]any{
		"v":       1,
		"id":      mailID,
		"from":    keyI,
		"to":      keyR,
		"created": "2026-01-02T03:10:00Z",
		"kind":    "ack",
		"body":    map[string]any{"ids": []any{"m-fedcba9876543210fedcba9876543210"}},
	})
	sig := ed25519.Sign(privI, append([]byte("dorylinae-mail-v1\n"), msg...))
	plain = canonical(map[string]any{"msg": json.RawMessage(msg), "sig": b64u.EncodeToString(sig)})
	info = append([]byte("dorylinae-mail-v1\n"+keyI+"\n"+keyR+"\n"), mbR.keyID...)
	aad = []byte(mailID)
	return plain, info, aad
}

func openMail(p64 string, privI ed25519.PrivateKey, keyI, keyR string, mbR mbox) {
	payload, err := base64.StdEncoding.DecodeString(p64)
	if err != nil {
		log.Fatal(err)
	}
	if payload[0] != 0x01 || !bytes.Equal(payload[1:9], mbR.keyID) {
		log.Fatal("bad version or key_id")
	}
	want, info, aad := mailInputs(privI, keyI, keyR, mbR)
	sk, err := hpke.NewDHKEMPrivateKey(mbR.priv)
	if err != nil {
		log.Fatal(err)
	}
	r, err := hpke.NewRecipient(payload[9:41], sk, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), info)
	if err != nil {
		log.Fatal(err)
	}
	got, err := r.Open(aad, payload[41:])
	if err != nil {
		log.Fatal("open: ", err)
	}
	if !bytes.Equal(got, want) {
		log.Fatal("plaintext differs")
	}
	fmt.Printf("open OK: %s\n", got)
	os.Exit(0)
}

// printAuditVector prints genesis and, for each row of Docs/protocol/audit.md
// §Vector, row_c and its hash (the legacy row's hash is virtual: computed,
// never stored). detail enters row_c as the stored JSON text, a string.
func printAuditVector() {
	genesis := sha256.Sum256([]byte("dorylinae-audit-genesis-v1"))
	fmt.Printf("genesis  %x\n", genesis)
	rows := []struct {
		id                        int
		ts, actor, action, detail string
		legacy                    bool
	}{
		{1, "2026-10-01T09:00:00.123456789Z", "daemon", "daemon.start", `{"pid":4242,"version":"0.3.0"}`, true},
		{2, "2026-10-01T09:00:05.5Z", "daemon", "audit.chain_start", `{"legacy_last_id":1,"legacy_rows":1}`, false},
		{3, "2026-10-01T09:00:06Z", "cli", "peer.verify", `{"fingerprint":"abcd","peer":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc"}`, false},
	}
	prev := genesis[:]
	for _, r := range rows {
		rowC := canonical(map[string]any{"action": r.action, "actor": r.actor, "detail": r.detail, "id": r.id, "ts": r.ts})
		h := sha256.New()
		h.Write([]byte("dorylinae-audit-v1\n"))
		h.Write(prev)
		h.Write(rowC)
		prev = h.Sum(nil)
		label := "hash"
		if r.legacy {
			label = "virtual hash"
		}
		fmt.Printf("row %d\n  %s\n  %s %x\n", r.id, rowC, label, prev)
	}
}

// printDecisionVector prints the Decision of Docs/protocol/decision.md
// §Vector (one round of passes, a proposal and an accepting answer), its id,
// decision_hash and both signatures, re-implemented from the text: the
// entries are the debate messages as the transcript holds them, copied into
// the object by derivation rules 1-12.
func printDecisionVector(privI, privR ed25519.PrivateKey, keyI, keyR string) {
	const session = "s-36375782ceb6baea9cee4d4273dfb035"
	idSum := sha256.Sum256([]byte("dorylinae-decision-id-v1\n" + session))
	id := "d-" + hex.EncodeToString(idSum[:16])
	posI := map[string]any{
		"claim":                 "Use capped exponential backoff for outbox retries",
		"assumptions":           []any{"Clock skew between peers is under 5 s"},
		"evidence":              []any{map[string]any{"kind": "file", "ref": "internal/mail/outbox.go"}},
		"rejected_alternatives": []any{map[string]any{"option": "Fixed 30 s retry", "reason": "Floods the relay after an outage"}},
		"argument":              "Retries should back off exponentially, capped at 10 minutes.",
	}
	posR := map[string]any{"claim": "Add full jitter to the existing backoff", "argument": "Jitter matters more than the curve."}
	agreement := map[string]any{"decision": "Capped exponential backoff with full jitter"}
	d := map[string]any{
		"v": 1, "id": id, "session": session, "request": "r-0123456789abcdef0123456789abcdef",
		"team":         "t-00112233445566778899aabbccddeeff",
		"participants": map[string]any{"initiator": keyI, "respondent": keyR},
		"problem":      map[string]any{"title": "Outbox retry policy", "topic": "How should the outbox retry?"},
		"rounds_max":   1,
		"positions": map[string]any{
			"initiator": map[string]any{"initial": posI}, "respondent": map[string]any{"initial": posR},
		},
		"rounds": []any{map[string]any{
			"n": 1, "initiator": map[string]any{"challenges": []any{}}, "respondent": map[string]any{"challenges": []any{}},
		}},
		"converge":        map[string]any{"proposal": map[string]any{"agreement": agreement}, "answer": map[string]any{"accept": true}},
		"final_agreement": agreement,
		"outcome":         "agreed", "reason": "accepted",
		"opened": "2026-10-01T09:00:00Z", "closed": "2026-10-01T09:20:00Z",
	}
	canon := canonical(d)
	msg := append([]byte("dorylinae-decision-v1\n"), canon...)
	fmt.Printf("canonical       %s\n", canon)
	fmt.Printf("id              %s   (from session %s)\n", id, session)
	fmt.Printf("decision_hash   %x\n", sha256.Sum256(msg))
	fmt.Printf("sig initiator   %s\n", b64u.EncodeToString(ed25519.Sign(privI, msg)))
	fmt.Printf("sig respondent  %s\n", b64u.EncodeToString(ed25519.Sign(privR, msg)))
}
