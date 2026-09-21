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
// payload field of the frame is never looked at.
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

// ParseHeader decodes and validates only the routing fields of a frame.
func ParseHeader(frame []byte) (Header, error) {
	var h Header
	if err := json.Unmarshal(frame, &h); err != nil {
		return Header{}, errors.New("not a valid JSON object")
	}
	return h, h.Validate()
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
