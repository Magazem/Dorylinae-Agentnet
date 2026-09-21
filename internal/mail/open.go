package mail

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hpke"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// Reject reasons, see Docs/protocol/mail.md §Receiving: verification order.
const (
	ReasonUnpaired       = "unpaired"
	ReasonMalformed      = "malformed"
	ReasonKeyMiss        = "key_miss"
	ReasonDecrypt        = "decrypt"
	ReasonSenderMismatch = "sender_mismatch"
	ReasonBadSignature   = "bad_signature"
	ReasonWrongRecipient = "wrong_recipient"
	ReasonIDMismatch     = "id_mismatch"
	ReasonStale          = "stale"
	ReasonBadKeys        = "bad_keys"
)

// RejectError is returned by Open when a verification step fails. Nothing
// should be stored and no ack sent. Reason is the audit reason; Step is the
// number in the spec table.
type RejectError struct {
	Reason string
	Step   int
	Err    error
}

func (e *RejectError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("mail: rejected at step %d (%s): %v", e.Step, e.Reason, e.Err)
	}
	return fmt.Sprintf("mail: rejected at step %d (%s)", e.Step, e.Reason)
}

func (e *RejectError) Unwrap() error { return e.Err }

// ReasonOf returns the reject reason carried by err, or "" if it is not a RejectError.
func ReasonOf(err error) string {
	var re *RejectError
	if errors.As(err, &re) {
		return re.Reason
	}
	return ""
}

func reject(step int, reason string, err error) error {
	return &RejectError{Reason: reason, Step: step, Err: err}
}

// Peers answers whether an identity key belongs to a paired peer.
type Peers interface {
	IsPaired(key string) bool
}

// Keys looks up the private half of a live own mailbox key by key_id. A key
// that was never created or has been deleted is not live: ok is false.
type Keys interface {
	MailboxKey(id KeyID) (priv *ecdh.PrivateKey, ok bool)
}

// Opener verifies and opens mail addressed to Self.
type Opener struct {
	Self  string // own identity key, wire form
	Peers Peers
	Keys  Keys
	Audit *RejectAudit     // optional; receives every rejection
	Now   func() time.Time // defaults to time.Now
}

// Opened is a mail that passed steps 1 to 12.
type Opened struct {
	Msg    Msg
	Signed []byte // verified canonical plaintext, kept as proof of origin
	KeyID  KeyID
}

// Open runs the verification steps of Docs/protocol/mail.md in order. env is
// an envelope of type mail whose Payload is already base64-decoded. On failure
// the error is a *RejectError and Audit, if set, has been told.
func (o *Opener) Open(env envelope.Envelope) (*Opened, error) {
	op, err := o.open(env)
	if err != nil && o.Audit != nil {
		o.Audit.Report(env.From, env.ID, ReasonOf(err))
	}
	return op, err
}

func (o *Opener) open(env envelope.Envelope) (*Opened, error) {
	now := time.Now()
	if o.Now != nil {
		now = o.Now()
	}

	// 1. Envelope from is a paired peer.
	if !o.Peers.IsPaired(env.From) {
		return nil, reject(1, ReasonUnpaired, nil)
	}

	// 2. Payload length and version.
	p := env.Payload
	if len(p) < MinPayload || len(p) > MaxPayload || p[0] != PayloadVersion {
		return nil, reject(2, ReasonMalformed, errors.New("bad payload length or version"))
	}

	// 3. key_id names a live own mailbox key.
	kid := KeyID(p[1 : 1+KeyIDLen])
	priv, ok := o.Keys.MailboxKey(kid)
	if !ok || priv == nil {
		return nil, reject(3, ReasonKeyMiss, nil)
	}

	// 4. HPKE open with info from envelope from/to and aad = envelope id.
	plain, err := hpkeOpen(priv, p[1+KeyIDLen:1+KeyIDLen+encLen], p[1+KeyIDLen+encLen:], buildInfo(env.From, env.To, kid), []byte(env.ID))
	if err != nil {
		return nil, reject(4, ReasonDecrypt, err)
	}

	// 5. Strict parse: exactly msg and sig, right types.
	gen, msg, sig, err := parseSigned(plain)
	if err != nil {
		return nil, reject(5, ReasonMalformed, err)
	}

	// 6. msg.from = envelope from.
	if msg.From != env.From {
		return nil, reject(6, ReasonSenderMismatch, nil)
	}

	// 7. Signature over the generically parsed msg.
	fromKey, err := decodeKey(msg.From)
	if err != nil {
		return nil, reject(7, ReasonBadSignature, err)
	}
	canonMsg, err := agentcard.CanonicalValue(gen)
	if err != nil {
		return nil, reject(7, ReasonBadSignature, err)
	}
	if !ed25519.Verify(fromKey, append([]byte(msgTag), canonMsg...), sig) {
		return nil, reject(7, ReasonBadSignature, nil)
	}

	// 8. msg.to is us.
	if msg.To != o.Self {
		return nil, reject(8, ReasonWrongRecipient, nil)
	}

	// 9. msg.id = envelope id and has the id format.
	if msg.ID != env.ID || !ValidID(msg.ID) {
		return nil, reject(9, ReasonIDMismatch, nil)
	}

	// 10. Version.
	if msg.V != 1 {
		return nil, reject(10, ReasonMalformed, fmt.Errorf("unsupported v %d", msg.V))
	}

	// 11. created window on the receiver clock.
	if msg.Created.Before(now.Add(-MaxAge)) || msg.Created.After(now.Add(MaxSkew)) {
		return nil, reject(11, ReasonStale, nil)
	}

	// 12. Body of kinds ack and keys.
	switch msg.Kind {
	case "ack":
		if err := checkAckBody(msg.Body); err != nil {
			return nil, reject(12, ReasonMalformed, err)
		}
	case "keys":
		if err := checkKeysBody(msg.Body, msg.From, now); err != nil {
			reason := ReasonMalformed
			if errors.Is(err, errBadAnnouncement) {
				reason = ReasonBadKeys
			}
			return nil, reject(12, reason, err)
		}
	}
	return &Opened{Msg: msg, Signed: plain, KeyID: kid}, nil
}

func hpkeOpen(priv *ecdh.PrivateKey, enc, ct, info, aad []byte) ([]byte, error) {
	sk, err := hpke.NewDHKEMPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	r, err := hpke.NewRecipient(enc, sk, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), info)
	if err != nil {
		return nil, err
	}
	return r.Open(aad, ct)
}

// parseSigned parses plaintext strictly. It returns the msg as parsed
// generically (the form the signature covers), the typed message and sig.
func parseSigned(plain []byte) (gen map[string]any, msg Msg, sig []byte, err error) {
	doc, err := agentcard.ParseStrict(plain)
	if err != nil {
		return nil, Msg{}, nil, err
	}
	top, ok := doc.(map[string]any)
	if !ok || len(top) != 2 {
		return nil, Msg{}, nil, errors.New("plaintext must be an object with exactly msg and sig")
	}
	gen, ok = top["msg"].(map[string]any)
	if !ok {
		return nil, Msg{}, nil, errors.New("missing msg object")
	}
	sigStr, ok := top["sig"].(string)
	if !ok {
		return nil, Msg{}, nil, errors.New("missing sig")
	}
	if sig, err = b64u.DecodeString(sigStr); err != nil || len(sig) != ed25519.SignatureSize {
		return nil, Msg{}, nil, errors.New("sig must be 64 bytes, base64url without padding")
	}
	if len(gen) != 7 {
		return nil, Msg{}, nil, errors.New("msg must have exactly v, id, from, to, created, kind, body")
	}
	v, ok := gen["v"].(json.Number)
	if !ok {
		return nil, Msg{}, nil, errors.New("v must be an integer")
	}
	vi, err := v.Int64()
	if err != nil {
		return nil, Msg{}, nil, errors.New("v must be an integer")
	}
	msg.V = int(vi)
	strs := map[string]*string{"id": &msg.ID, "from": &msg.From, "to": &msg.To, "kind": &msg.Kind}
	for k, dst := range strs {
		s, ok := gen[k].(string)
		if !ok {
			return nil, Msg{}, nil, fmt.Errorf("%s must be a string", k)
		}
		*dst = s
	}
	if msg.Created, err = parseTime(gen["created"]); err != nil {
		return nil, Msg{}, nil, fmt.Errorf("created: %w", err)
	}
	if !kindPattern.MatchString(msg.Kind) {
		return nil, Msg{}, nil, errors.New("bad kind")
	}
	if msg.Body, ok = gen["body"].(map[string]any); !ok {
		return nil, Msg{}, nil, errors.New("body must be an object")
	}
	if _, err := agentcard.CanonicalValue(gen); err != nil { // integers only, in range
		return nil, Msg{}, nil, err
	}
	return gen, msg, sig, nil
}
