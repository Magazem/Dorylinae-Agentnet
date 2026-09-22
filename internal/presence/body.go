package presence

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// bootPattern is the boot id format, Docs/protocol/presence.md §Body.
var bootPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// ErrBadBody is returned by Parse when a presence body fails validation.
var ErrBadBody = errors.New("presence: bad body")

// Body is a validated presence message body, Docs/protocol/presence.md §Body.
type Body struct {
	State    string // "online" or "offline"
	Agent    int    // 0 or 1
	Human    int    // 0, 1 or 2
	Boot     string // 16 lowercase hex
	Seq      int64
	Interval int              // 1-300
	Epochs   map[string]int64 // team id -> epoch
}

// wireBody is the JSON shape of Body, member order does not matter: canonical
// serialisation sorts keys.
type wireBody struct {
	Agent    int              `json:"agent"`
	Boot     string           `json:"boot"`
	Epochs   map[string]int64 `json:"epochs"`
	Human    int              `json:"human"`
	Interval int              `json:"interval"`
	Pad      string           `json:"pad"`
	Seq      int64            `json:"seq"`
	State    string           `json:"state"`
}

// Build returns the body ready for mail.SealInput.Body: pad is computed so
// that sealing it as a presence message with these from/to/id/created
// produces a canonical signed plaintext (Docs/protocol/mail.md §Message)
// whose length is always exactly fixedBodyLen(from, to, id, created) bytes,
// regardless of state, flags, seq or the epochs member (Docs/protocol/
// presence.md §Body; review 17 L1).
func Build(b Body, from, to, id string, created time.Time) (any, error) {
	epochs := b.Epochs
	if epochs == nil {
		epochs = map[string]int64{}
	}
	w := wireBody{
		Agent: b.Agent, Boot: b.Boot, Epochs: epochs, Human: b.Human,
		Interval: b.Interval, Pad: "", Seq: b.Seq, State: b.State,
	}
	l, err := canonicalLen(from, to, id, created, w)
	if err != nil {
		return nil, err
	}
	fixed, err := fixedBodyLen(from, to, id, created)
	if err != nil {
		return nil, err
	}
	if l > fixed {
		return nil, fmt.Errorf("presence: body %d bytes exceeds the fixed padded size %d", l, fixed)
	}
	w.Pad = strings.Repeat("0", fixed-l)
	return w, nil
}

// fixedBodyLen is the one padded plaintext length every presence body (a
// heartbeat or a goodbye) seals to for this from/to/id/created: the
// smallest multiple of padBlock that fits the longest possible body —
// state "offline" (one byte longer than "online"), interval at its 3-digit
// maximum, seq and every epoch value at maxIntValue, and the maximum 32
// epochs. It depends only on the lengths of from/to/id/created, which are
// fixed by their formats (identity keys, the "p-"+32hex id, RFC 3339 to the
// second), so it is the same number for every presence message, and a
// hostile relay learns nothing about the content from the size on the wire
// (Docs/protocol/presence.md §Body; review 17 L1).
func fixedBodyLen(from, to, id string, created time.Time) (int, error) {
	epochs := make(map[string]int64, maxEpochs)
	for i := 0; i < maxEpochs; i++ {
		epochs[fmt.Sprintf("t-%032x", i)] = maxIntValue
	}
	w := wireBody{
		Agent: 1, Boot: strings.Repeat("f", 16), Epochs: epochs, Human: 2,
		Interval: maxInterval, Pad: "", Seq: maxIntValue, State: "offline",
	}
	l, err := canonicalLen(from, to, id, created, w)
	if err != nil {
		return 0, err
	}
	return ((l + padBlock - 1) / padBlock) * padBlock, nil
}

// maxPad is the largest pad length Parse accepts: the fixed body length
// minus the shortest possible body (state "online", seq 1, interval at its
// 1-digit minimum, no epochs). Both terms include the same placeholder
// from/to/id/created, so their difference does not depend on those lengths
// (only the body content does), and this bound holds for every real message.
var maxPad = func() int {
	from := strings.Repeat("A", 43) // envelope.KeyString: base64 RawURLEncoding of a 32-byte key
	to := strings.Repeat("A", 43)
	id := "p-" + strings.Repeat("0", 32)
	created := time.Unix(0, 0).UTC()
	fixed, err := fixedBodyLen(from, to, id, created)
	if err != nil {
		panic(err)
	}
	minimal := wireBody{
		Agent: 0, Boot: strings.Repeat("0", 16), Epochs: map[string]int64{}, Human: 2,
		Interval: minInterval, Pad: "", Seq: 1, State: "online",
	}
	l, err := canonicalLen(from, to, id, created, minimal)
	if err != nil {
		panic(err)
	}
	return fixed - l
}()

// canonicalLen returns the length of the canonical signed plaintext that
// sealing this body would produce, computed with a placeholder signature: a
// real Ed25519 signature is always 64 bytes, so its base64 encoding is always
// the same length regardless of content, and the pad computed from this is
// therefore exact.
func canonicalLen(from, to, id string, created time.Time, w wireBody) (int, error) {
	raw, err := json.Marshal(w)
	if err != nil {
		return 0, fmt.Errorf("presence: marshal body: %w", err)
	}
	genBody, err := agentcard.ParseStrict(raw)
	if err != nil {
		return 0, fmt.Errorf("presence: body: %w", err)
	}
	msg := map[string]any{
		"v":       json.Number("1"),
		"id":      id,
		"from":    from,
		"to":      to,
		"created": created.UTC().Truncate(time.Second).Format(wireTimeFmt),
		"kind":    Kind,
		"body":    genBody,
	}
	sig := make([]byte, ed25519.SignatureSize)
	plain, err := agentcard.CanonicalValue(map[string]any{
		"msg": msg, "sig": base64.RawURLEncoding.EncodeToString(sig),
	})
	if err != nil {
		return 0, fmt.Errorf("presence: canonicalize: %w", err)
	}
	return len(plain), nil
}

// Parse validates a presence message body against Docs/protocol/presence.md
// §Body: exactly the required members, each in range.
func Parse(body map[string]any) (Body, error) {
	if len(body) != 8 {
		return Body{}, fmt.Errorf("%w: must have exactly 8 members", ErrBadBody)
	}
	var b Body

	state, ok := body["state"].(string)
	if !ok || (state != "online" && state != "offline") {
		return Body{}, fmt.Errorf("%w: state must be online or offline", ErrBadBody)
	}
	b.State = state

	agent, err := intField(body, "agent")
	if err != nil || agent < 0 || agent > 1 {
		return Body{}, fmt.Errorf("%w: agent must be 0 or 1", ErrBadBody)
	}
	b.Agent = int(agent)
	if state == "offline" && b.Agent != 0 {
		return Body{}, fmt.Errorf("%w: agent must be 0 when offline", ErrBadBody)
	}

	human, err := intField(body, "human")
	if err != nil || human < 0 || human > 2 {
		return Body{}, fmt.Errorf("%w: human must be 0, 1 or 2", ErrBadBody)
	}
	b.Human = int(human)
	if state == "offline" && b.Human != 2 {
		return Body{}, fmt.Errorf("%w: human must be 2 when offline", ErrBadBody)
	}

	boot, ok := body["boot"].(string)
	if !ok || !bootPattern.MatchString(boot) {
		return Body{}, fmt.Errorf("%w: boot must be 16 lowercase hex characters", ErrBadBody)
	}
	b.Boot = boot

	seq, err := intField(body, "seq")
	if err != nil || seq < 1 || seq > maxIntValue {
		return Body{}, fmt.Errorf("%w: seq must be 1 to 2^53-1", ErrBadBody)
	}
	b.Seq = seq

	interval, err := intField(body, "interval")
	if err != nil || interval < minInterval || interval > maxInterval {
		return Body{}, fmt.Errorf("%w: interval must be 1-300", ErrBadBody)
	}
	b.Interval = int(interval)

	epochsRaw, ok := body["epochs"].(map[string]any)
	if !ok {
		return Body{}, fmt.Errorf("%w: epochs must be an object", ErrBadBody)
	}
	if len(epochsRaw) > maxEpochs {
		return Body{}, fmt.Errorf("%w: epochs holds at most 32 entries", ErrBadBody)
	}
	epochs := make(map[string]int64, len(epochsRaw))
	for k, v := range epochsRaw {
		if !team.ValidID(k) {
			return Body{}, fmt.Errorf("%w: epochs key is not a team id", ErrBadBody)
		}
		n, ok := v.(json.Number)
		if !ok {
			return Body{}, fmt.Errorf("%w: epochs value must be an integer", ErrBadBody)
		}
		ev, err := n.Int64()
		if err != nil || ev < 0 || ev > maxIntValue {
			return Body{}, fmt.Errorf("%w: epochs value must be 0 to 2^53-1", ErrBadBody)
		}
		epochs[k] = ev
	}
	b.Epochs = epochs

	pad, ok := body["pad"].(string)
	if !ok || len(pad) > maxPad || strings.ContainsFunc(pad, func(r rune) bool { return r != '0' }) {
		return Body{}, fmt.Errorf("%w: pad must be 0-1023 '0' characters", ErrBadBody)
	}

	return b, nil
}

func intField(body map[string]any, key string) (int64, error) {
	n, ok := body[key].(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s: not an integer", key)
	}
	return n.Int64()
}
