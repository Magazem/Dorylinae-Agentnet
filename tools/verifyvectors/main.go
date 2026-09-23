// Command verifyvectors independently recomputes the pairing v2, sealed-mail
// and capability-grant test vectors published in Docs/protocol/pairing.md,
// Docs/protocol/mail.md and Docs/protocol/grant.md, and compares them with
// the values in vectors.json (transcribed from those docs).
//
// It is deliberately self-contained: it uses only the Go standard library and
// golang.org/x/crypto, and imports no internal/ package and nothing from
// tools/specvectors. Everything is re-implemented from the spec text.
//
// Usage: go run ./tools/verifyvectors   (exit status 1 on any mismatch)
package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/hpke"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

//go:embed vectors.json
var vectorsJSON []byte

type vectors struct {
	Pairing struct {
		KeyI            string `json:"key_I"`
		KeyR            string `json:"key_R"`
		Lookup          string `json:"lookup"`
		Secret          string `json:"secret"`
		CardI           string `json:"card_I"`
		CardR           string `json:"card_R"`
		MboxI           string `json:"mbox_I"`
		MboxR           string `json:"mbox_R"`
		SaltHex         string `json:"salt_hex"`
		KHex            string `json:"K_hex"`
		THex            string `json:"T_hex"`
		TagIHex         string `json:"tag_I_hex"`
		TagRHex         string `json:"tag_R_hex"`
		TagIB64u        string `json:"tag_I_b64u"`
		TagRB64u        string `json:"tag_R_b64u"`
		ConfirmRPlain   string `json:"confirm_R_plain"`
		ConfirmRPayload string `json:"confirm_R_payload"`
		ConfirmIPlain   string `json:"confirm_I_plain"`
		ConfirmIPayload string `json:"confirm_I_payload"`
	} `json:"pairing"`
	Mail struct {
		PrivHex    string `json:"priv_hex"`
		PubHex     string `json:"pub_hex"`
		KeyIDHex   string `json:"key_id_hex"`
		Plaintext  string `json:"plaintext"`
		InfoHex    string `json:"info_hex"`
		AADHex     string `json:"aad_hex"`
		EncHex     string `json:"enc_hex"`
		CtHex      string `json:"ct_hex"`
		PayloadB64 string `json:"payload_b64"`
		MsgID      string `json:"msg_id"`
		Created    string `json:"created"`
		StaleNow   string `json:"stale_now"`
	} `json:"mail"`
	Capability struct {
		Iss       string `json:"iss"`
		Aud       string `json:"aud"`
		Session   string `json:"session"`
		Canonical string `json:"canonical"`
		HashHex   string `json:"hash_hex"`
		Sig       string `json:"sig"`
		Token     string `json:"token"`
	} `json:"capability"`
}

// --- canonical JSON (agent-card.md §Canonical serialisation) ---

// canonical parses in as generic JSON and re-serialises it canonically.
func canonical(in []byte) ([]byte, error) {
	if !utf8.Valid(in) {
		return nil, errors.New("invalid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	var out bytes.Buffer
	if err := writeValue(&out, v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type member struct {
	key string
	val any
}

// parseValue reads one value; objects become []member so duplicates are detectable.
func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			var ms []member
			seen := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k := kt.(string)
				if seen[k] {
					return nil, fmt.Errorf("duplicate key %q", k)
				}
				seen[k] = true
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				ms = append(ms, member{k, v})
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			if ms == nil {
				ms = []member{}
			}
			return ms, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, errors.New("unexpected delimiter")
	default:
		return tok, nil // string, json.Number, bool, nil
	}
}

func writeValue(b *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case string:
		writeString(b, t)
	case json.Number:
		s := t.String()
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != s || n >= 1<<53 || n <= -(1<<53) {
			return fmt.Errorf("non-canonical number %q", s)
		}
		b.WriteString(s)
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeValue(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case []member:
		sort.Slice(t, func(i, j int) bool {
			return lessUTF16(t[i].key, t[j].key)
		})
		b.WriteByte('{')
		for i, m := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, m.key)
			b.WriteByte(':')
			if err := writeValue(b, m.val); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("unsupported type %T", v)
	}
	return nil
}

func lessUTF16(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

func writeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// member2 returns canonical({name1: obj[name1], name2: obj[name2]}) from an envelope
// such as {"card":…,"signature":…}, dropping other top-level members (pairing.md).
func member2(envelope []byte, n1, n2 string) (inner1, sig, canon []byte, err error) {
	dec := json.NewDecoder(bytes.NewReader(envelope))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, nil, nil, err
	}
	ms, ok := v.([]member)
	if !ok {
		return nil, nil, nil, errors.New("not an object")
	}
	var a, s any
	var haveA, haveS bool
	for _, m := range ms {
		switch m.key {
		case n1:
			a, haveA = m.val, true
		case n2:
			s, haveS = m.val, true
		}
	}
	if !haveA || !haveS {
		return nil, nil, nil, errors.New("missing member")
	}
	var ib, sb, ob bytes.Buffer
	if err := writeValue(&ib, a); err != nil {
		return nil, nil, nil, err
	}
	if err := writeValue(&sb, s); err != nil {
		return nil, nil, nil, err
	}
	if err := writeValue(&ob, []member{{n1, a}, {n2, s}}); err != nil {
		return nil, nil, nil, err
	}
	return ib.Bytes(), sb.Bytes(), ob.Bytes(), nil
}

// --- pairing primitives (pairing.md §Keys and tags) ---

func u32(n int) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(n)) //nolint:gosec // n is a small non-negative length
	return b[:]
}

func deriveK(secret, lookup string) (salt, k []byte) {
	salt = append([]byte("dorylinae-pair-v2\n"), lookup...)
	k = argon2.IDKey([]byte(secret), salt, 3, 64*1024, 1, 32)
	return
}

func transcript(lookup string, cardI, cardR, mboxI, mboxR []byte) []byte {
	h := sha256.New()
	h.Write([]byte("dorylinae-pair-v2-transcript\n"))
	h.Write([]byte(lookup))
	for _, p := range [][]byte{cardI, cardR, mboxI, mboxR} {
		h.Write(u32(len(p)))
		h.Write(p)
	}
	return h.Sum(nil)
}

func tag(k []byte, role string, t []byte) []byte {
	m := hmac.New(sha256.New, k)
	m.Write([]byte(role + "\n"))
	m.Write(t)
	return m.Sum(nil)
}

// confirmPayload is the canonical JSON {"lookup":…,"tag":<base64url>,"v":2} and its
// standard-base64 envelope form (keys already in sorted order).
func confirmPayload(lookup string, tg []byte) (plain, b64 string) {
	plain = `{"lookup":"` + lookup + `","tag":"` + base64.RawURLEncoding.EncodeToString(tg) + `","v":2}`
	return plain, base64.StdEncoding.EncodeToString([]byte(plain))
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// fingerprint per pairing.md §Fingerprints: first 20 Crockford-base32 characters.
func fingerprint(pub []byte) string {
	d := sha256.Sum256(append([]byte("dorylinae-fingerprint-v1\n"), pub...))
	var sb strings.Builder
	var acc, bits int
	for _, by := range d {
		acc = acc<<8 | int(by)
		bits += 8
		for bits >= 5 && sb.Len() < 20 {
			sb.WriteByte(crockford[(acc>>(bits-5))&31])
			bits -= 5
		}
		acc &= (1 << bits) - 1
	}
	s := sb.String()
	return s[0:4] + " " + s[4:8] + " " + s[8:12] + " " + s[12:16] + " " + s[16:20]
}

// --- reporting ---

type checker struct {
	w    io.Writer
	fail int
}

func (c *checker) eq(name string, got, want []byte) {
	if bytes.Equal(got, want) {
		_, _ = fmt.Fprintf(c.w, "PASS %s\n", name)
		return
	}
	c.fail++
	_, _ = fmt.Fprintf(c.w, "FAIL %s\n  got  %x\n  want %x\n", name, got, want)
}

func (c *checker) eqs(name, got, want string) { c.eq(name, []byte(got), []byte(want)) }

func (c *checker) ok(name string, cond bool, detail string) {
	if cond {
		_, _ = fmt.Fprintf(c.w, "PASS %s\n", name)
		return
	}
	c.fail++
	_, _ = fmt.Fprintf(c.w, "FAIL %s: %s\n", name, detail)
}

func mustHex(c *checker, name, s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		c.ok(name, false, "bad hex in vectors.json: "+err.Error())
		return nil
	}
	return b
}

// run executes every check, writing PASS/FAIL lines to w, and returns the number of failures.
func run(w io.Writer, raw []byte) int {
	c := &checker{w: w}
	var v vectors
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		_, _ = fmt.Fprintf(w, "FAIL vectors.json: %v\n", err)
		return 1
	}
	pairing(c, &v)
	mail(c, &v)
	capability(c, &v)
	return c.fail
}

func pairing(c *checker, v *vectors) {
	p := &v.Pairing

	// Identity keys: Ed25519 seeds 00..1f (issuer) and 20..3f (redeemer).
	seed := func(start byte) ed25519.PrivateKey {
		s := make([]byte, 32)
		for i := range s {
			s[i] = start + byte(i)
		}
		return ed25519.NewKeyFromSeed(s)
	}
	privI, privR := seed(0x00), seed(0x20)
	pubI := privI.Public().(ed25519.PublicKey)
	pubR := privR.Public().(ed25519.PublicKey)
	c.eqs("key_I", base64.RawURLEncoding.EncodeToString(pubI), p.KeyI)
	c.eqs("key_R", base64.RawURLEncoding.EncodeToString(pubR), p.KeyR)
	c.eqs("fp(key_I)", fingerprint(pubI), "2ED9 TGVE R471 63MC C451")
	c.eqs("fp(key_R)", fingerprint(pubR), "2P56 R8XN KZYG XBXC JB4S")

	// Cards and announcements: canonical form is a fixed point, signatures verify.
	type doc struct {
		name, raw, inner, domain string
		pub                      ed25519.PublicKey
	}
	docs := []doc{
		{"card_I", p.CardI, "card", "dorylinae-agent-card-v1\n", pubI},
		{"card_R", p.CardR, "card", "dorylinae-agent-card-v1\n", pubR},
		{"mbox_I", p.MboxI, "announcement", "dorylinae-mailbox-key-v1\n", pubI},
		{"mbox_R", p.MboxR, "announcement", "dorylinae-mailbox-key-v1\n", pubR},
	}
	canon := map[string][]byte{}
	for _, d := range docs {
		inner, sig, out, err := member2([]byte(d.raw), d.inner, "signature")
		if err != nil {
			c.ok(d.name+" canonical", false, err.Error())
			continue
		}
		canon[d.name] = out
		c.eqs(d.name+" canonical(parse(doc))==doc", string(out), d.raw)
		var sigStr string
		_ = json.Unmarshal(sig, &sigStr)
		sb, err := base64.RawURLEncoding.DecodeString(sigStr)
		c.ok(d.name+" signature verifies",
			err == nil && len(sb) == 64 && ed25519.Verify(d.pub, append([]byte(d.domain), inner...), sb),
			"Ed25519 verification failed")
	}

	// The relay re-marshals with encoding/json, escaping < > & as six-character  escapes.
	// Canonicalising must undo that.
	esc := func(r string) string { return "\\" + "u" + r }
	relayed := strings.NewReplacer("<", esc("003c"), ">", esc("003e"), "&", esc("0026")).Replace(p.CardI)
	if relayed == p.CardI {
		c.ok("card_I contains <, > or &", false, "nothing to escape")
	}
	if _, _, out, err := member2([]byte(relayed), "card", "signature"); err != nil {
		c.ok("card_I canonical after relay re-escaping", false, err.Error())
	} else {
		c.eqs("card_I canonical after relay re-escaping", string(out), p.CardI)
	}

	// K.
	salt, k := deriveK(p.Secret, p.Lookup)
	c.eq("salt", salt, mustHex(c, "salt", p.SaltHex))
	c.eq("K", k, mustHex(c, "K", p.KHex))
	_, kBad := deriveK(p.Secret[:9]+"P", p.Lookup)
	c.ok("negative: secret 9XHF4TRW8P gives different K", !bytes.Equal(kBad, k), "K unchanged")

	// T, tags.
	t := transcript(p.Lookup, canon["card_I"], canon["card_R"], canon["mbox_I"], canon["mbox_R"])
	c.eq("T", t, mustHex(c, "T", p.THex))
	inputLen := len("dorylinae-pair-v2-transcript\n") + len(p.Lookup) + 16 +
		len(canon["card_I"]) + len(canon["card_R"]) + len(canon["mbox_I"]) + len(canon["mbox_R"])
	c.ok("T input length 1314", inputLen == 1314, fmt.Sprintf("got %d", inputLen))
	tagI, tagR := tag(k, "issuer", t), tag(k, "redeemer", t)
	c.eq("tag_I", tagI, mustHex(c, "tag_I", p.TagIHex))
	c.eq("tag_R", tagR, mustHex(c, "tag_R", p.TagRHex))
	c.eqs("tag_I base64url", base64.RawURLEncoding.EncodeToString(tagI), p.TagIB64u)
	c.eqs("tag_R base64url", base64.RawURLEncoding.EncodeToString(tagR), p.TagRB64u)

	// pair.confirm payloads.
	plain, b64 := confirmPayload(p.Lookup, tagR)
	c.eqs("confirm redeemer plaintext", plain, p.ConfirmRPlain)
	c.eqs("confirm redeemer payload", b64, p.ConfirmRPayload)
	plain, b64 = confirmPayload(p.Lookup, tagI)
	c.eqs("confirm issuer plaintext", plain, p.ConfirmIPlain)
	c.eqs("confirm issuer payload", b64, p.ConfirmIPayload)
	if canonPlain, err := canonical([]byte(p.ConfirmRPlain)); err != nil {
		c.ok("confirm redeemer plaintext is canonical", false, err.Error())
	} else {
		c.eqs("confirm redeemer plaintext is canonical", string(canonPlain), p.ConfirmRPlain)
	}

	// Negative checks: any single-byte change to card_R / mbox_R changes the tag; role swap differs.
	flipped := 0
	for _, name := range []string{"card_R", "mbox_R"} {
		orig := canon[name]
		for i := range orig {
			mod := append([]byte(nil), orig...)
			mod[i] ^= 1
			args := map[string][]byte{"card_I": canon["card_I"], "card_R": canon["card_R"], "mbox_I": canon["mbox_I"], "mbox_R": canon["mbox_R"]}
			args[name] = mod
			t2 := transcript(p.Lookup, args["card_I"], args["card_R"], args["mbox_I"], args["mbox_R"])
			if bytes.Equal(tag(k, "redeemer", t2), tagR) {
				flipped++
			}
		}
	}
	c.ok("negative: any single-byte change of card_R/mbox_R changes tag_R", flipped == 0,
		fmt.Sprintf("%d byte flips left the tag unchanged", flipped))
	c.ok("negative: tag_I computed with \"redeemer\\n\" differs", !bytes.Equal(tag(k, "redeemer", t), tagI), "tags equal")
}

func mail(c *checker, v *vectors) {
	m := &v.Mail
	privB := mustHex(c, "priv", m.PrivHex)
	priv, err := ecdh.X25519().NewPrivateKey(privB)
	if err != nil {
		c.ok("mailbox private key", false, err.Error())
		return
	}
	pub := priv.PublicKey().Bytes()
	c.eq("mailbox pub", pub, mustHex(c, "pub", m.PubHex))
	kid := sha256.Sum256(pub)
	c.eqs("mailbox key_id", hex.EncodeToString(kid[:8]), m.KeyIDHex)

	// Rebuild info and aad from the message fields, and compare with the published hex.
	var msgEnv struct {
		Msg struct {
			From, To, ID string
		}
	}
	// Extract from/to/id from the plaintext without relying on struct tags' case rules.
	var generic struct {
		Msg map[string]any `json:"msg"`
	}
	if err := json.Unmarshal([]byte(m.Plaintext), &generic); err != nil {
		c.ok("plaintext parses", false, err.Error())
		return
	}
	msgEnv.Msg.From, _ = generic.Msg["from"].(string)
	msgEnv.Msg.To, _ = generic.Msg["to"].(string)
	msgEnv.Msg.ID, _ = generic.Msg["id"].(string)
	info := []byte("dorylinae-mail-v1\n" + msgEnv.Msg.From + "\n" + msgEnv.Msg.To + "\n")
	info = append(info, kid[:8]...)
	aad := []byte(msgEnv.Msg.ID)
	c.eq("info", info, mustHex(c, "info", m.InfoHex))
	c.eq("aad", aad, mustHex(c, "aad", m.AADHex))
	c.eqs("msg id", msgEnv.Msg.ID, m.MsgID)

	// Payload layout: 0x01 ‖ key_id(8) ‖ enc(32) ‖ ct.
	payload, err := base64.StdEncoding.DecodeString(m.PayloadB64)
	if err != nil || len(payload) < 57 {
		c.ok("payload decodes", false, fmt.Sprint("err=", err, " len=", len(payload)))
		return
	}
	enc, ct := mustHex(c, "enc", m.EncHex), mustHex(c, "ct", m.CtHex)
	c.eq("payload version byte", payload[:1], []byte{1})
	c.eq("payload key_id", payload[1:9], kid[:8])
	c.eq("payload enc", payload[9:41], enc)
	c.eq("payload ct", payload[41:], ct)

	open := func(payload, info, aad []byte) ([]byte, error) {
		sk, err := hpke.NewDHKEMPrivateKey(priv)
		if err != nil {
			return nil, err
		}
		r, err := hpke.NewRecipient(payload[9:41], sk, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), info)
		if err != nil {
			return nil, err
		}
		return r.Open(aad, payload[41:])
	}
	pt, err := open(payload, info, aad)
	if err != nil {
		c.ok("HPKE open", false, err.Error())
		return
	}
	c.eqs("HPKE open plaintext", string(pt), m.Plaintext)

	// Plaintext is canonical, and the sender signature verifies (mail.md §Message).
	if cp, err := canonical(pt); err != nil {
		c.ok("plaintext canonical", false, err.Error())
	} else {
		c.eqs("plaintext canonical", string(cp), m.Plaintext)
	}
	msgRaw, sigRaw, _, err := member2(pt, "msg", "sig")
	if err != nil {
		c.ok("plaintext structure", false, err.Error())
	} else {
		var sigStr string
		_ = json.Unmarshal(sigRaw, &sigStr)
		sig, err := base64.RawURLEncoding.DecodeString(sigStr)
		seedI := make([]byte, 32)
		for i := range seedI {
			seedI[i] = byte(i)
		}
		pubI := ed25519.NewKeyFromSeed(seedI).Public().(ed25519.PublicKey)
		c.ok("mail sig verifies under sender key",
			err == nil && len(sig) == 64 && ed25519.Verify(pubI, append([]byte("dorylinae-mail-v1\n"), msgRaw...), sig),
			"Ed25519 verification failed")
	}

	// Negative checks (mail.md).
	for _, part := range []struct {
		name string
		off  int
	}{{"enc", 9}, {"ct", 41}} {
		bad := append([]byte(nil), payload...)
		bad[part.off] ^= 0x01
		_, err := open(bad, info, aad)
		c.ok("negative: flipped bit in "+part.name+" fails decrypt", err != nil, "opened")
	}
	_, err = open(payload, info, []byte("m-0123456789abcdef0123456789abcdee"))
	c.ok("negative: wrong aad fails decrypt", err != nil, "opened")
	swapped := []byte("dorylinae-mail-v1\n" + msgEnv.Msg.To + "\n" + msgEnv.Msg.From + "\n")
	swapped = append(swapped, kid[:8]...)
	_, err = open(payload, swapped, aad)
	c.ok("negative: swapped from/to in info fails decrypt", err != nil, "opened")

	created, e1 := parseRFC3339Z(m.Created)
	stale, e2 := parseRFC3339Z(m.StaleNow)
	c.ok("negative: receiver clock 2026-02-02T03:10:01Z is stale (>30 d after created)",
		e1 == nil && e2 == nil && stale-created > 30*24*3600, fmt.Sprint(e1, e2))
}

// --- capability grant (Docs/protocol/grant.md §Test vectors) ---

// capability rebuilds the grant object as ordinary Go values (so its JSON
// key order is irrelevant), re-canonicalises with this file's own canonical
// (the RFC 8785-style canonicaliser used for pairing/mail above, not
// internal/agentcard), and checks the hash, signature and wire token
// independently of internal/capability.
func capability(c *checker, v *vectors) {
	cp := &v.Capability

	type resource struct {
		Kind   string `json:"kind"`
		Label  string `json:"label"`
		Branch string `json:"branch"`
	}
	type grant struct {
		V         int      `json:"v"`
		ID        string   `json:"id"`
		Iss       string   `json:"iss"`
		Aud       string   `json:"aud"`
		Session   string   `json:"session"`
		Action    string   `json:"action"`
		Resource  resource `json:"resource"`
		Scope     string   `json:"scope"`
		Nbf       string   `json:"nbf"`
		Exp       string   `json:"exp"`
		Sensitive bool     `json:"sensitive"`
	}
	g := grant{
		V:         1,
		ID:        "g-00112233445566778899aabbccddeeff",
		Iss:       cp.Iss,
		Aud:       cp.Aud,
		Session:   cp.Session,
		Action:    "git.read",
		Resource:  resource{Kind: "git", Label: "agentnet-3f2a", Branch: "feat-x"},
		Scope:     "internal/mail",
		Nbf:       "2026-01-02T03:00:00Z",
		Exp:       "2026-01-02T05:00:00Z",
		Sensitive: true,
	}
	canon, err := canonicalOf(c, "capability grant", g)
	if err != nil {
		return
	}
	c.eqs("capability canonical grant", string(canon), cp.Canonical)

	sum := sha256.Sum256(canon)
	c.eqs("capability grant hash", hex.EncodeToString(sum[:]), cp.HashHex)

	issPub, err := base64.RawURLEncoding.DecodeString(cp.Iss)
	if err != nil || len(issPub) != ed25519.PublicKeySize {
		c.ok("capability iss key decodes", false, fmt.Sprint(err))
		return
	}
	sig, err := base64.RawURLEncoding.DecodeString(cp.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		c.ok("capability sig decodes", false, fmt.Sprint(err))
		return
	}
	c.ok("capability signature verifies",
		ed25519.Verify(issPub, append([]byte("dorylinae-grant-v1\n"), canon...), sig),
		"Ed25519 verification failed")

	// grant.md: keys as in pairing.md (grantor seed 00..1f, holder 20..3f),
	// and the signature recomputed, not only verified (Ed25519 is
	// deterministic).
	c.eqs("capability iss = pairing key_I", cp.Iss, v.Pairing.KeyI)
	c.eqs("capability aud = pairing key_R", cp.Aud, v.Pairing.KeyR)
	grantorSeed := make([]byte, ed25519.SeedSize)
	for i := range grantorSeed {
		grantorSeed[i] = byte(i)
	}
	grantorPriv := ed25519.NewKeyFromSeed(grantorSeed)
	c.eqs("capability iss from seed 00..1f",
		base64.RawURLEncoding.EncodeToString(grantorPriv.Public().(ed25519.PublicKey)), cp.Iss)
	c.eqs("capability sig recomputed",
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(grantorPriv, append([]byte("dorylinae-grant-v1\n"), canon...))), cp.Sig)

	tokenCanon, err := canonicalOf(c, "capability token", struct {
		Grant json.RawMessage `json:"grant"`
		Sig   string          `json:"sig"`
	}{Grant: canon, Sig: cp.Sig})
	if err != nil {
		return
	}
	c.eqs("capability token", string(tokenCanon), cp.Token)

	// Negative: the widened caveat. The holder changes exp and re-serialises;
	// the original signature must not verify over the new canonical bytes.
	widened := g
	widened.Exp = "2026-01-03T05:00:00Z"
	widenedCanon, err := canonicalOf(c, "capability widened exp", widened)
	if err == nil {
		c.ok("negative: widened exp fails signature",
			!ed25519.Verify(issPub, append([]byte("dorylinae-grant-v1\n"), widenedCanon...), sig),
			"signature verified after widening exp")
	}

	// Negative: scope removed, same sig.
	type grantNoScope struct {
		V         int      `json:"v"`
		ID        string   `json:"id"`
		Iss       string   `json:"iss"`
		Aud       string   `json:"aud"`
		Session   string   `json:"session"`
		Action    string   `json:"action"`
		Resource  resource `json:"resource"`
		Nbf       string   `json:"nbf"`
		Exp       string   `json:"exp"`
		Sensitive bool     `json:"sensitive"`
	}
	noScope := grantNoScope{g.V, g.ID, g.Iss, g.Aud, g.Session, g.Action, g.Resource, g.Nbf, g.Exp, g.Sensitive}
	noScopeCanon, err := canonicalOf(c, "capability scope removed", noScope)
	if err == nil {
		c.ok("negative: scope removed fails signature",
			!ed25519.Verify(issPub, append([]byte("dorylinae-grant-v1\n"), noScopeCanon...), sig),
			"signature verified after removing scope")
	}
}

// canonicalOf marshals v with encoding/json and re-canonicalises the result,
// recording a check failure (and a non-nil error) if either step fails.
func canonicalOf(c *checker, name string, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		c.ok(name+" marshals", false, err.Error())
		return nil, err
	}
	canon, err := canonical(raw)
	if err != nil {
		c.ok(name+" canonicalises", false, err.Error())
		return nil, err
	}
	return canon, nil
}

// parseRFC3339Z converts "YYYY-MM-DDThh:mm:ssZ" to unix seconds without the time package's
// helpers being needed elsewhere; kept tiny and strict.
func parseRFC3339Z(s string) (int64, error) {
	if len(s) != 20 || s[19] != 'Z' {
		return 0, fmt.Errorf("bad time %q", s)
	}
	atoi := func(a, b int) int { n, _ := strconv.Atoi(s[a:b]); return n }
	y, mo, d, h, mi, se := atoi(0, 4), atoi(5, 7), atoi(8, 10), atoi(11, 13), atoi(14, 16), atoi(17, 19)
	// Days from civil (Howard Hinnant).
	if mo <= 2 {
		y--
	}
	era := y / 400
	yoe := y - era*400
	mp := (mo + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	days := int64(era*146097 + doe - 719468)
	return days*86400 + int64(h*3600+mi*60+se), nil
}

func main() {
	if n := run(os.Stdout, vectorsJSON); n != 0 {
		_, _ = fmt.Fprintf(os.Stdout, "%d check(s) FAILED\n", n)
		os.Exit(1)
	}
	fmt.Println("all vectors reproduced")
}
