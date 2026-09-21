// Command specvectors prints the test vectors published in
// Docs/protocol/pairing.md (pairing v2, fingerprints) and
// Docs/protocol/mail.md (mailbox announcements, sealed mail).
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
	"os"
	"sort"
	"time"

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
	default:
		log.Fatalf("canonical: unsupported %T", v)
	}
}

func u32(n int) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(n))
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
	lookup, secret := "7KQ2M", "9XHF4TRW8N"
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
	confirm := canonical(map[string]any{"v": 2, "lookup": lookup, "tag": b64u.EncodeToString(tagI)})
	fmt.Printf("pair.confirm plaintext (issuer) %s\n", confirm)
	fmt.Printf("pair.confirm payload (base64)   %s\n", base64.StdEncoding.EncodeToString(confirm))

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
