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

	// Account ops on a relay with accounts, see Docs/protocol/accounts.md. A
	// daemon sends them only when ready lists FeatureAccounts.
	OpBindStart      = "bind_start"
	OpBindPending    = "bind_pending"
	OpBindPoll       = "bind_poll"
	OpBindDone       = "bind_done"
	OpBindCancel     = "bind_cancel"
	OpUnbind         = "unbind"
	OpAccountChanged = "account_changed"
)

// ProtocolVersion is the relay protocol version.
const ProtocolVersion = 1

// FeatureEphemeral is the ready feature that says the relay forwards ephemeral
// envelope types without queueing them (Docs/protocol/presence.md).
const FeatureEphemeral = "ephemeral"

// FeatureAccounts is the ready feature of a relay that requires a bound
// account (Docs/protocol/accounts.md); its ready carries Account.
const FeatureAccounts = "accounts"

// TypePairConfirm is the envelope type of the pairing confirmation
// (Docs/protocol/pairing.md). On a relay with accounts it is the one type a
// bound key without a quota group may send and receive.
const TypePairConfirm = "pair.confirm"

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

	// Abuse limits, see Docs/protocol/relay-hosted.md §2. rate_limited
	// refuses one envelope or control frame (ref names it), or a reconnect
	// right after auth; relay_full refuses a connection after auth. Both mean
	// retry later.
	CodeRateLimited = "rate_limited"
	CodeRelayFull   = "relay_full"

	// Accounts, see Docs/protocol/accounts.md. account_required refuses a
	// frame or envelope the key's account state does not allow (or, to a
	// sender, a recipient that may not receive it); account_suspended and
	// account_revoked come before close 1008; already_bound answers
	// bind_start from a bound key; bind_expired and bind_denied end a bind.
	CodeAccountRequired  = "account_required"
	CodeAccountSuspended = "account_suspended"
	CodeAccountRevoked   = "account_revoked"
	CodeAlreadyBound     = "already_bound"
	CodeBindExpired      = "bind_expired"
	CodeBindDenied       = "bind_denied"

	// Pairing failures, see Docs/protocol/pairing.md.
	CodePairInvalid     = "pair_invalid"
	CodePairRateLimited = "pair_rate_limited"
	CodePairLimit       = "pair_limit"
	CodeBadPairing      = "bad_pairing"
	CodeLookupTaken     = "pair_lookup_taken"
	CodePairV1Disabled  = "pair_v1_disabled"

	// CodeRelayError is daemon-side only, never sent by a relay: the code an
	// error frame gets when its code is not one of the above (R55-F9).
	CodeRelayError = "relay_error"
)

// errorCodes is every code a relay may send, the table of
// Docs/protocol/envelope.md §error frame. Keep it next to the consts: a test
// fails if a Code* constant is missing here.
var errorCodes = []string{
	CodeAuthFailed, CodeBadEnvelope, CodeBadSender, CodePeerOffline, CodePeerBusy, CodeQueueFull, CodeInternal,
	CodeRateLimited, CodeRelayFull,
	CodeAccountRequired, CodeAccountSuspended, CodeAccountRevoked, CodeAlreadyBound, CodeBindExpired, CodeBindDenied,
	CodePairInvalid, CodePairRateLimited, CodePairLimit, CodeBadPairing, CodeLookupTaken, CodePairV1Disabled,
}

// KnownErrorCode reports whether code is one a relay may send. CodeRelayError
// is not: a relay sending it is converted to the same value anyway.
func KnownErrorCode(code string) bool {
	for _, c := range errorCodes {
		if c == code {
			return true
		}
	}
	return false
}

// errorTexts is the daemon's own text for each relay error code, shown by
// ping and pair in place of the relay's message (OD-R55F9-10 = b).
var errorTexts = map[string]string{
	CodeAuthFailed:       "the relay refused the authentication",
	CodeBadEnvelope:      "the relay refused a malformed message",
	CodeBadSender:        "the relay refused the sender key",
	CodePeerOffline:      "the peer is not connected to the relay",
	CodePeerBusy:         "the peer is busy",
	CodeQueueFull:        "the relay's queue for the peer is full",
	CodeInternal:         "the relay had an internal error",
	CodeRateLimited:      "the relay is rate limiting this daemon; try again later",
	CodeRelayFull:        "the relay is full; try again later",
	CodeAccountRequired:  "the relay requires a bound account for this",
	CodeAccountSuspended: "the relay account is suspended",
	CodeAccountRevoked:   "the relay account was revoked",
	CodeAlreadyBound:     "this key is already bound to an account",
	CodeBindExpired:      "the account binding expired",
	CodeBindDenied:       "the account binding was denied",
	CodePairInvalid:      "the pairing code is invalid, expired or already used",
	CodePairRateLimited:  "too many pairing attempts; try again later",
	CodePairLimit:        "too many pending pairing codes; try again later",
	CodeBadPairing:       "the relay refused the pairing request",
	CodeLookupTaken:      "the pairing code clashed with another; ask for a new one",
	CodePairV1Disabled:   "the relay no longer supports legacy (v1) pairing",
}

// ErrorText is the daemon-owned text for a relay error code: fixed per known
// code, and "the relay refused the request" for CodeRelayError or anything
// else. It never contains relay-chosen words.
func ErrorText(code string) string {
	if t, ok := errorTexts[code]; ok {
		return t
	}
	return "the relay refused the request"
}

// ValidID reports whether s is a valid envelope id: 1 to 128 bytes of
// [A-Za-z0-9._:-], the rule Validate applies.
func ValidID(s string) bool {
	return s != "" && len(s) <= maxIDLen && allBytes(s, isIDByte)
}

// NonceSize is the challenge nonce length in bytes.
const NonceSize = 32

const authDomain = "dorylinae-relay-auth-v1\n"

// Control is any control frame; unused fields are omitted on the wire.
type Control struct {
	Op      string `json:"op"`
	Version int    `json:"version,omitempty"`
	Nonce   string `json:"nonce,omitempty"`
	Expires string `json:"expires,omitempty"`
	// Auth lists the relay authentication versions a challenge offers
	// (AuthV1, AuthV2). Absent means v1 only.
	Auth []string `json:"auth,omitempty"`
	// V is the relay authentication version of an auth frame: 2 for v2,
	// absent (0) for v1.
	V         int    `json:"v,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
	Signature string `json:"signature,omitempty"`
	// Features lists the optional relay features a ready frame advertises. Older
	// clients ignore the member.
	Features []string `json:"features,omitempty"`
	// MinClient is the oldest daemon release ("MAJOR.MINOR.PATCH") a ready
	// frame says the relay supports (ticket 4.4a). Advisory: the daemon
	// keeps its connection, logs a warning and reports it in status/doctor.
	// Absent means no minimum.
	MinClient string `json:"min_client,omitempty"`
	Code      string `json:"code,omitempty"`
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

	// Account is the key's account state in ready and bind_done on a relay
	// with accounts (Docs/protocol/accounts.md).
	Account *Account `json:"account,omitempty"`
	// Device and OS label a bind_start for the confirm page.
	Device string `json:"device,omitempty"`
	OS     string `json:"os,omitempty"`
	// UserCode, URL and Interval are the bind_pending fields: the code the
	// human types (XXXX-XXXX), the fixed login page, and the minimum seconds
	// between bind_poll frames.
	UserCode string `json:"user_code,omitempty"`
	URL      string `json:"url,omitempty"`
	Interval int    `json:"interval,omitempty"`
}

// Account states carried in Account.State.
const (
	AccountUnbound   = "unbound"
	AccountBound     = "bound"
	AccountSuspended = "suspended"
)

// Account is the account state of a key: State, and for a bound key the
// account id, its display (@login or the email) and its quota group, if any.
type Account struct {
	State   string `json:"state"`
	ID      string `json:"id,omitempty"`
	Display string `json:"display,omitempty"`
	Group   string `json:"group,omitempty"`
}

// ErrorFrame is the decoded form of an error control frame.
type ErrorFrame struct {
	Code    string
	Message string
	// Ref is the id of the envelope that caused the error, if any.
	Ref string
}

// Error is "relay: <code>": the message is relay text and stays out of
// errors, logs and last_error (R55-F9).
func (e ErrorFrame) Error() string { return "relay: " + e.Code }

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

// VerifyAuth checks a v1 auth frame against the challenge nonce and returns
// the authenticated public key. A v2 frame is checked with VerifyAuthV2.
func VerifyAuth(c Control, nonce []byte) (ed25519.PublicKey, error) {
	if c.Op != OpAuth || (c.V != 0 && c.V != 1) {
		return nil, errors.New("not a v1 auth frame")
	}
	pub, sig, err := authParts(c)
	if err != nil {
		return nil, err
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
