// Package envelope defines the relay wire format: the routed Envelope, the
// control frames, and the auth signature rules. The specification is
// Docs/protocol/envelope.md; change that first.
package envelope

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// MaxFrameBytes is the largest frame the relay accepts.
const MaxFrameBytes = 1 << 20

// MaxAuthFrameBytes limits frames sent before authentication completes.
const MaxAuthFrameBytes = 4 << 10

// ConnectPath is the relay's WebSocket endpoint.
const ConnectPath = "/v1/connect"

const (
	maxTeamLen = 128
	maxTypeLen = 64
	maxIDLen   = 128
)

// Strict: no padding, and trailing bits must be zero, so a value has one encoding.
var b64 = base64.RawURLEncoding.Strict()

// Envelope is a message from one daemon to another. Payload is opaque to the relay.
type Envelope struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Team    string `json:"team"`
	Type    string `json:"type"`
	ID      string `json:"id"`
	TS      string `json:"ts"`
	Payload []byte `json:"payload"`
}

// Header is the routing part of an Envelope. The relay decodes only this; the
// payload field of the frame is never decoded, only its shape is checked
// (ParseHeader).
type Header struct {
	From string `json:"from"`
	To   string `json:"to"`
	Team string `json:"team"`
	Type string `json:"type"`
	ID   string `json:"id"`
	TS   string `json:"ts"`
}

// Validate checks the routing fields.
func (h Header) Validate() error {
	if err := checkKey("from", h.From); err != nil {
		return err
	}
	if err := checkKey("to", h.To); err != nil {
		return err
	}
	if len(h.Team) > maxTeamLen || !allBytes(h.Team, isIDByte) {
		return errors.New("team: at most 128 characters from [A-Za-z0-9._:-]")
	}
	if h.Type == "" || len(h.Type) > maxTypeLen || !allBytes(h.Type, isTypeByte) {
		return errors.New("type: 1-64 characters from [a-z0-9._-]")
	}
	if h.ID == "" || len(h.ID) > maxIDLen || !allBytes(h.ID, isIDByte) {
		return errors.New("id: 1-128 characters from [A-Za-z0-9._:-]")
	}
	if _, err := time.Parse(time.RFC3339Nano, h.TS); err != nil {
		return errors.New("ts: not an RFC 3339 time")
	}
	return nil
}

// Header returns the routing fields of e.
func (e Envelope) Header() Header {
	return Header{From: e.From, To: e.To, Team: e.Team, Type: e.Type, ID: e.ID, TS: e.TS}
}

// Validate checks the envelope.
func (e Envelope) Validate() error { return e.Header().Validate() }

// Marshal validates e and encodes it as a frame.
func (e Envelope) Marshal() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	if e.Payload == nil {
		e.Payload = []byte{}
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("envelope: marshal: %w", err)
	}
	if len(raw) > MaxFrameBytes {
		return nil, fmt.Errorf("envelope: %d bytes exceeds the %d byte limit", len(raw), MaxFrameBytes)
	}
	return raw, nil
}

// Parse decodes and validates a full envelope frame, payload included.
// Error messages never contain frame contents.
func Parse(frame []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(frame, &e); err != nil {
		return Envelope{}, errors.New("envelope: not a valid envelope frame")
	}
	if err := e.Validate(); err != nil {
		return Envelope{}, fmt.Errorf("envelope: %w", err)
	}
	return e, nil
}

// ErrBadPayload is ParseHeader's error for a frame whose routing fields are
// valid but whose payload is not a JSON string of standard base64 (R55-010).
var ErrBadPayload = errors.New("payload: not a base64 string")

// ParseHeader decodes and validates the routing fields of a frame, then
// checks the shape of its payload without decoding it (envelope.md "Payload
// shape check"). When only the payload fails it returns the valid header and
// an error wrapping ErrBadPayload, so the caller can name the envelope's id.
func ParseHeader(frame []byte) (Header, error) {
	var w wireHeader
	if err := json.Unmarshal(frame, &w); err != nil {
		return Header{}, errors.New("not a valid JSON object")
	}
	h := w.header()
	if err := h.Validate(); err != nil {
		return h, err
	}
	if !w.Payload.ok() {
		return h, ErrBadPayload
	}
	return h, nil
}

// AckTarget reads the (from, id) a daemon acks for a frame it cannot parse
// (envelope.md "Frames it cannot parse"). It uses ParseHeader's decoding, the
// one the relay applied at ingress, and ignores only the payload verdict: ok
// is true when the frame decodes without error and every routing field
// validates, exactly as the relay required before it queued the frame.
func AckTarget(frame []byte) (from, id, typ string, ok bool) {
	var w wireHeader
	if err := json.Unmarshal(frame, &w); err != nil {
		return "", "", "", false
	}
	h := w.header()
	if h.Validate() != nil {
		return "", "", "", false
	}
	return h.From, h.ID, h.Type, true
}

// wireHeader is the decoding ParseHeader and AckTarget share. Its fields have
// the names and tags of Envelope's, so encoding/json resolves repeated and
// case-variant keys for it exactly as for the recipient's Envelope.
type wireHeader struct {
	From    string       `json:"from"`
	To      string       `json:"to"`
	Team    string       `json:"team"`
	Type    string       `json:"type"`
	ID      string       `json:"id"`
	TS      string       `json:"ts"`
	Payload payloadCheck `json:"payload"`
}

func (w wireHeader) header() Header {
	return Header{From: w.From, To: w.To, Team: w.Team, Type: w.Type, ID: w.ID, TS: w.TS}
}

// payloadCheck judges a payload value in place, keeping no copy of it. Every
// occurrence of the key is judged and one bad value refuses the frame:
// encoding/json keeps the first type error even when a later key overwrites
// the field, so the recipient's Parse fails on any bad occurrence, not only
// the last.
type payloadCheck struct {
	seen, bad bool
}

// UnmarshalJSON records the verdict for raw; null reaches it too and is
// refused. It never fails, so a bad payload does not hide the routing fields.
func (p *payloadCheck) UnmarshalJSON(raw []byte) error {
	p.seen = true
	if !validPayload(raw) {
		p.bad = true
	}
	return nil
}

func (p payloadCheck) ok() bool { return p.seen && !p.bad }

// validPayload reports whether raw is a JSON string token without escapes
// whose content base64.StdEncoding accepts: a length that is a multiple of
// 4, the standard alphabet, and "=" only as the last one or two characters.
// "" is valid.
func validPayload(raw []byte) bool {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return false
	}
	s := raw[1 : len(raw)-1]
	n := len(s)
	if n%4 != 0 {
		return false
	}
	if n > 0 && s[n-1] == '=' {
		n--
		if s[n-1] == '=' {
			n--
		}
	}
	for _, c := range s[:n] {
		if !isBase64Byte(c) {
			return false
		}
	}
	return true
}

func isBase64Byte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/'
}

// KeyString encodes a public key as used on the wire.
func KeyString(pub ed25519.PublicKey) string { return b64.EncodeToString(pub) }

// ParseKey decodes a wire public key.
func ParseKey(s string) (ed25519.PublicKey, error) {
	raw, err := b64.DecodeString(s)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("not a base64url Ed25519 public key")
	}
	return ed25519.PublicKey(raw), nil
}

func checkKey(field, s string) error {
	if _, err := ParseKey(s); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

func allBytes(s string, ok func(byte) bool) bool {
	for i := 0; i < len(s); i++ {
		if !ok(s[i]) {
			return false
		}
	}
	return true
}

func isTypeByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-'
}

func isIDByte(c byte) bool {
	return isTypeByte(c) || c >= 'A' && c <= 'Z' || c == ':'
}
