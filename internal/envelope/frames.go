package envelope

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
)

// Control frame operations. Control frames carry "op"; envelopes never do.
const (
	OpChallenge = "challenge"
	OpAuth      = "auth"
	OpReady     = "ready"
	OpError     = "error"

	// Offline queue ops, see Docs/protocol/envelope.md. The relay answers an
	// envelope it stored for an offline peer with OpQueued; a daemon confirms
	// each envelope it received with OpAck so the relay can delete its copy.
	OpQueued = "queued"
	OpAck    = "ack"

	// Pairing ops, see Docs/protocol/pairing.md.
	OpPairNew    = "pair_new"
	OpPairCode   = "pair_code"
	OpPairRedeem = "pair_redeem"
	OpPairPeer   = "pair_peer"
	OpPairCancel = "pair_cancel"
)

// ProtocolVersion is the relay protocol version.
const ProtocolVersion = 1

// FeatureEphemeral is the ready feature that says the relay forwards ephemeral
// envelope types without queueing them (Docs/protocol/presence.md).
const FeatureEphemeral = "ephemeral"

// TypePresence is the ephemeral envelope type of presence heartbeats.
const TypePresence = "presence"

// IsEphemeral reports whether envelopes of type t are never queued, never
// acked and not deduplicated by the relay client.
func IsEphemeral(t string) bool { return t == TypePresence }

// Error codes carried by an ErrorFrame.
const (
	CodeAuthFailed  = "auth_failed"
	CodeBadEnvelope = "bad_envelope"
	CodeBadSender   = "bad_sender"
	CodePeerOffline = "peer_offline"
	CodePeerBusy    = "peer_busy"
	CodeQueueFull   = "queue_full"
	CodeInternal    = "internal"

	// Pairing failures, see Docs/protocol/pairing.md.
	CodePairInvalid     = "pair_invalid"
	CodePairRateLimited = "pair_rate_limited"
	CodePairLimit       = "pair_limit"
	CodeBadPairing      = "bad_pairing"
	CodeLookupTaken     = "pair_lookup_taken"
	CodePairV1Disabled  = "pair_v1_disabled"
)

// NonceSize is the challenge nonce length in bytes.
const NonceSize = 32

const authDomain = "dorylinae-relay-auth-v1\n"

// Control is any control frame; unused fields are omitted on the wire.
type Control struct {
	Op        string `json:"op"`
	Version   int    `json:"version,omitempty"`
	Nonce     string `json:"nonce,omitempty"`
	Expires   string `json:"expires,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
	Signature string `json:"signature,omitempty"`
	// Features lists the optional relay features a ready frame advertises. Older
	// clients ignore the member.
	Features []string `json:"features,omitempty"`
	Code     string   `json:"code,omitempty"`
	// Lookup is the 5-character v2 pairing lookup. It is not the secret half.
	Lookup  string `json:"lookup,omitempty"`
	Message string `json:"message,omitempty"`
	Ref     string `json:"ref,omitempty"`
	// From is the sender key of the envelope an ack refers to (Ref is its id).
	From string `json:"from,omitempty"`
	// Card is an opaque signed Agent Card carried by pairing frames.
	Card json.RawMessage `json:"card,omitempty"`
	// Mbox is an opaque signed mailbox key announcement carried by v2 pairing frames.
	Mbox json.RawMessage `json:"mbox,omitempty"`
}

// ErrorFrame is the decoded form of an error control frame.
type ErrorFrame struct {
	Code    string
	Message string
	// Ref is the id of the envelope that caused the error, if any.
	Ref string
}

func (e ErrorFrame) Error() string { return "relay: " + e.Code + ": " + e.Message }

// Frame classifies a received frame: exactly one of Control or Envelope is set.
type Frame struct {
	Control *Control
	// Envelope is the raw envelope frame, untouched.
	Envelope []byte
}

// Classify reports whether frame is a control frame (has "op") or an envelope.
func Classify(frame []byte) (Frame, error) {
	var probe struct {
		Op *string `json:"op"`
	}
	if err := json.Unmarshal(frame, &probe); err != nil {
		return Frame{}, errors.New("not a valid JSON object")
	}
	if probe.Op == nil {
		return Frame{Envelope: frame}, nil
	}
	var c Control
	if err := json.Unmarshal(frame, &c); err != nil {
		return Frame{}, errors.New("malformed control frame")
	}
	return Frame{Control: &c}, nil
}

// AuthMessage is the byte string a daemon signs to answer a challenge.
func AuthMessage(nonce []byte) []byte {
	return append([]byte(authDomain), nonce...)
}

// SignAuth builds the auth frame answering nonce with sign, which must produce
// an Ed25519 signature made by the key pub.
func SignAuth(pub ed25519.PublicKey, nonce []byte, sign func([]byte) ([]byte, error)) (Control, error) {
	sig, err := sign(AuthMessage(nonce))
	if err != nil {
		return Control{}, err
	}
	return Control{Op: OpAuth, PublicKey: KeyString(pub), Signature: b64.EncodeToString(sig)}, nil
}

// VerifyAuth checks an auth frame against the challenge nonce and returns the
// authenticated public key.
func VerifyAuth(c Control, nonce []byte) (ed25519.PublicKey, error) {
	if c.Op != OpAuth {
		return nil, errors.New("not an auth frame")
	}
	pub, err := ParseKey(c.PublicKey)
	if err != nil {
		return nil, err
	}
	sig, err := b64.DecodeString(c.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, errors.New("malformed signature")
	}
	if !ed25519.Verify(pub, AuthMessage(nonce), sig) {
		return nil, errors.New("signature does not verify")
	}
	return pub, nil
}

// EncodeNonce and DecodeNonce convert a challenge nonce to and from its wire form.
func EncodeNonce(n []byte) string { return b64.EncodeToString(n) }

// DecodeNonce parses a wire nonce.
func DecodeNonce(s string) ([]byte, error) {
	n, err := b64.DecodeString(s)
	if err != nil || len(n) != NonceSize {
		return nil, errors.New("malformed nonce")
	}
	return n, nil
}
