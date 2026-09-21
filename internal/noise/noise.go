// Package noise wraps flynn/noise for Dorylinae sessions: the XX handshake,
// the binding of the Noise static key to the Ed25519 identity, and transport
// encryption with explicit counters and replay rejection. The specification
// is Docs/protocol/session.md; change that first.
package noise

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	fnoise "github.com/flynn/noise"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

const (
	staticDomain   = "dorylinae-noise-static-v1\n"
	prologueDomain = "dorylinae-noise-xx-v1\n"
	bindingVersion = 1
)

var (
	suite = fnoise.NewCipherSuite(fnoise.DH25519, fnoise.CipherChaChaPoly, fnoise.HashSHA256)
	b64   = base64.RawURLEncoding

	// ErrBinding means the peer's static key is not bound to the expected identity.
	ErrBinding = errors.New("noise: static key binding does not verify")
	// ErrHandshake means Noise rejected a handshake message or it came out of turn.
	ErrHandshake = errors.New("noise: handshake failed")
	// ErrReplay means a transport counter was already used or is out of order.
	ErrReplay = errors.New("noise: replayed or reordered message")
	// ErrDecrypt means a transport ciphertext failed authentication.
	ErrDecrypt = errors.New("noise: message failed authentication")
)

// Static is this daemon's Noise static key and its identity binding.
type Static struct {
	key      fnoise.DHKey
	identity string
	payload  []byte // encoded binding, sent in handshake messages 2 and 3
}

// NewStatic generates a Noise static key and binds it to identity with sign,
// which must return an Ed25519 signature by identity's private key.
func NewStatic(identity ed25519.PublicKey, sign func([]byte) ([]byte, error)) (*Static, error) {
	key, err := suite.GenerateKeypair(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("noise: generate static key: %w", err)
	}
	sig, err := sign(BindingMessage(key.Public))
	if err != nil {
		return nil, fmt.Errorf("noise: sign static key: %w", err)
	}
	id := envelope.KeyString(identity)
	payload, err := json.Marshal(binding{V: bindingVersion, Identity: id, Sig: b64.EncodeToString(sig)})
	if err != nil {
		return nil, err
	}
	return &Static{key: key, identity: id, payload: payload}, nil
}

// Identity is the wire form of the identity key the static key is bound to.
func (s *Static) Identity() string { return s.identity }

// BindingMessage is the byte string the identity key signs to bind a static key.
func BindingMessage(staticPub []byte) []byte {
	return append([]byte(staticDomain), staticPub...)
}

type binding struct {
	V        int    `json:"v"`
	Identity string `json:"identity"`
	Sig      string `json:"sig"`
}

// verifyBinding checks a received binding payload against the static key Noise
// authenticated and the identity the handshake is with.
func verifyBinding(payload, peerStatic []byte, want string) error {
	var b binding
	if err := json.Unmarshal(payload, &b); err != nil || b.V != bindingVersion || b.Identity != want {
		return ErrBinding
	}
	pub, err := envelope.ParseKey(b.Identity)
	if err != nil {
		return ErrBinding
	}
	sig, err := b64.DecodeString(b.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize || len(peerStatic) != 32 {
		return ErrBinding
	}
	if !ed25519.Verify(pub, BindingMessage(peerStatic), sig) {
		return ErrBinding
	}
	return nil
}

// Prologue binds a handshake to the routing identities of both sides.
func Prologue(initiator, responder string) []byte {
	return []byte(prologueDomain + initiator + "\n" + responder)
}

// Handshake is one side of a Noise XX handshake with a known peer identity.
// Initiator: Write (msg 1), Read (msg 2), Write (msg 3, returns the transport).
// Responder: Read (msg 1), Write (msg 2), Read (msg 3, returns the transport).
type Handshake struct {
	hs        *fnoise.HandshakeState
	st        *Static
	peer      string
	initiator bool
	failed    bool
}

// NewHandshake starts a handshake between st's identity and peer (wire form).
func NewHandshake(st *Static, peer string, initiator bool) (*Handshake, error) {
	if _, err := envelope.ParseKey(peer); err != nil {
		return nil, fmt.Errorf("noise: peer: %w", err)
	}
	pro := Prologue(st.identity, peer)
	if !initiator {
		pro = Prologue(peer, st.identity)
	}
	hs, err := fnoise.NewHandshakeState(fnoise.Config{
		CipherSuite:   suite,
		Random:        rand.Reader,
		Pattern:       fnoise.HandshakeXX,
		Initiator:     initiator,
		Prologue:      pro,
		StaticKeypair: st.key,
	})
	if err != nil {
		return nil, fmt.Errorf("noise: %w", err)
	}
	return &Handshake{hs: hs, st: st, peer: peer, initiator: initiator}, nil
}

// Peer is the identity this handshake is with.
func (h *Handshake) Peer() string { return h.peer }

// Initiator reports whether this side started the handshake.
func (h *Handshake) Initiator() bool { return h.initiator }

// myTurn reports whether the next message is ours to write.
func (h *Handshake) myTurn() bool { return (h.hs.MessageIndex()%2 == 0) == h.initiator }

// Write produces the next handshake message. On the initiator's final message
// it also returns the transport.
func (h *Handshake) Write() ([]byte, *Transport, error) {
	if h.failed || !h.myTurn() || h.hs.MessageIndex() > 2 {
		return nil, nil, ErrHandshake
	}
	var payload []byte
	if h.hs.MessageIndex() > 0 {
		payload = h.st.payload
	}
	msg, cs1, cs2, err := h.hs.WriteMessage(nil, payload)
	if err != nil {
		h.failed = true
		return nil, nil, ErrHandshake
	}
	if cs1 == nil {
		return msg, nil, nil
	}
	return msg, newTransport(h.initiator, cs1, cs2), nil
}

// Read consumes the peer's next handshake message, verifying the identity
// binding in messages 2 and 3. On the responder's final message it returns
// the transport. Any error ends the handshake.
func (h *Handshake) Read(msg []byte) (*Transport, error) {
	if h.failed || h.myTurn() || h.hs.MessageIndex() > 2 {
		return nil, ErrHandshake
	}
	idx := h.hs.MessageIndex()
	payload, cs1, cs2, err := h.hs.ReadMessage(nil, msg)
	if err != nil {
		h.failed = true
		return nil, ErrHandshake
	}
	if idx == 0 {
		if len(payload) != 0 {
			h.failed = true
			return nil, ErrHandshake
		}
		return nil, nil
	}
	if err := verifyBinding(payload, h.hs.PeerStatic(), h.peer); err != nil {
		h.failed = true
		return nil, err
	}
	if cs1 == nil {
		return nil, nil
	}
	return newTransport(h.initiator, cs1, cs2), nil
}

// CounterSize is the length of the explicit counter prefixed to ciphertexts.
const CounterSize = 8

// Transport encrypts and decrypts session messages with explicit counters.
// It is not safe for concurrent use.
type Transport struct {
	send, recv fnoise.Cipher
	sendN      uint64
	recvNext   uint64
}

func newTransport(initiator bool, cs1, cs2 *fnoise.CipherState) *Transport {
	// cs1 protects initiator -> responder, cs2 the other direction.
	if initiator {
		return &Transport{send: cs1.Cipher(), recv: cs2.Cipher()}
	}
	return &Transport{send: cs2.Cipher(), recv: cs1.Cipher()}
}

// Seal encrypts plaintext with the next counter. ad is computed by the caller
// from the counter (see Docs/protocol/session.md). It returns the counter used.
func (t *Transport) Seal(ad func(counter uint64) []byte, plaintext []byte) (uint64, []byte, error) {
	if t.sendN == math.MaxUint64 {
		return 0, nil, errors.New("noise: session exhausted its counters")
	}
	n := t.sendN
	t.sendN++
	return n, t.send.Encrypt(nil, n, ad(n), plaintext), nil
}

// Open decrypts a message with the given counter. Counters below the next
// acceptable one fail with ErrReplay; authentication failures with ErrDecrypt.
// Only a successful Open advances the counter.
func (t *Transport) Open(counter uint64, ad, ciphertext []byte) ([]byte, error) {
	if counter < t.recvNext || counter == math.MaxUint64 {
		return nil, ErrReplay
	}
	pt, err := t.recv.Decrypt(nil, counter, ad, ciphertext)
	if err != nil {
		return nil, ErrDecrypt
	}
	t.recvNext = counter + 1
	return pt, nil
}

// PutCounter and Counter encode the explicit counter.
func PutCounter(b []byte, n uint64) { binary.BigEndian.PutUint64(b, n) }

// Counter decodes an explicit counter.
func Counter(b []byte) uint64 { return binary.BigEndian.Uint64(b) }
