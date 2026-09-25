package notify

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// maxPayloadBytes is the payload size cap (Docs/protocol/notify.md §Payload).
const maxPayloadBytes = 8 * 1024

// EventTest is the synthetic event fired by "agentnet notify --test"
// (Docs/protocol/notify.md §Delivery): request is omitted and text is fixed.
const EventTest = "test"

// payload is the "generic" webhook body (Docs/protocol/notify.md §Payload).
type payload struct {
	V       int             `json:"v"`
	ID      string          `json:"id"`
	Event   string          `json:"event"`
	TS      string          `json:"ts"`
	Request *payloadRequest `json:"request,omitempty"`
	Peer    *payloadPeer    `json:"peer,omitempty"`
	Team    *payloadTeam    `json:"team,omitempty"`
	Text    string          `json:"text"`
}

type payloadRequest struct {
	ID           string `json:"id"`
	Session      string `json:"session,omitempty"`
	Type         string `json:"type"`
	Urgency      string `json:"urgency,omitempty"`
	State        string `json:"state"`
	Title        string `json:"title,omitempty"`
	ResultStatus string `json:"result_status,omitempty"`
}

type payloadPeer struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type payloadTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// newDeliveryID returns a fresh replay id: "w-" + 32 hex characters
// (Docs/protocol/notify.md §Payload).
func newDeliveryID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("notify: generate delivery id: %w", err)
	}
	return "w-" + hex.EncodeToString(b[:]), nil
}

// buildPayload renders the generic-format event object for ev, which must
// already have Kind == EventTest handled by the caller for the test event.
// title selects whether the request title and completion status are
// included (Docs/protocol/notify.md §Payload, §Configuration
// "--webhook-title").
func buildPayload(id string, ev Event, title bool) payload {
	p := payload{
		V:     1,
		ID:    id,
		Event: ev.Kind,
		TS:    ev.CreatedAt.UTC().Format(time.RFC3339),
		Text:  buildWebhookText(ev, title),
	}
	if ev.Kind == EventTest {
		return p
	}
	req := &payloadRequest{
		ID:      ev.RequestID,
		Session: ev.Session,
		Type:    ev.Type,
		Urgency: ev.Urgency,
		State:   ev.State,
	}
	if title {
		req.Title = Clean(ev.Title, maxTitleCodePoints)
		if ev.Kind == EventCompleted && ev.HasResult {
			req.ResultStatus = ev.ResultStatus
		}
	}
	p.Request = req
	p.Peer = &payloadPeer{Name: Clean(ev.PeerName, maxNameCodePoints), Fingerprint: ev.PeerFP}
	if ev.TeamID != "" {
		p.Team = &payloadTeam{ID: ev.TeamID, Name: Clean(ev.TeamName, maxNameCodePoints)}
	}
	return p
}

// marshalPayload encodes p and enforces the 8 KiB cap
// (Docs/protocol/notify.md §Payload).
func marshalPayload(p payload) ([]byte, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("notify: encode webhook payload: %w", err)
	}
	if len(body) > maxPayloadBytes {
		return nil, fmt.Errorf("notify: webhook payload is %d bytes, over the %d byte cap", len(body), maxPayloadBytes)
	}
	return body, nil
}

// applyFormat adapts the generic body bytes to the slack or discord shape
// (Docs/protocol/notify.md §Payload). generic is returned unchanged.
func applyFormat(format string, body []byte, text string) ([]byte, error) {
	switch format {
	case "", FormatGeneric:
		return body, nil
	case FormatSlack:
		return addFields(body, map[string]any{"text": slackEscape(text)})
	case FormatDiscord:
		return addFields(body, map[string]any{
			"content":          discordEscape(text),
			"allowed_mentions": map[string]any{"parse": []string{}},
		})
	default:
		return nil, fmt.Errorf("notify: unknown webhook format %q", format)
	}
}

// addFields decodes generic JSON, overlays extra on top (extra wins), and
// re-encodes. Field order in the output is not significant.
func addFields(generic []byte, extra map[string]any) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(generic, &m); err != nil {
		return nil, err
	}
	for k, v := range extra {
		m[k] = v
	}
	return json.Marshal(m)
}

// slackEscape applies Slack's required text escaping so that a peer-supplied
// "<!channel>" or "<https://evil|click>" renders as inert text instead of a
// channel ping or a disguised link (Docs/protocol/notify.md §Payload,
// review 12).
func slackEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			out = append(out, "&amp;"...)
		case '<':
			out = append(out, "&lt;"...)
		case '>':
			out = append(out, "&gt;"...)
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

// discordEscapeChars are the characters Discord markdown gives a meaning:
// emphasis, code, spoilers, quotes, headings, lists, masked links
// "[label](url)" and "<...>" mentions, channels and timestamps.
const discordEscapeChars = "\\*_~`|>#-[]()<@"

// discordEscape backslash-escapes Discord markdown in s, so that a
// peer-supplied "[click](https://evil)" renders as inert text instead of a
// disguised link, and "# big" or "||spoiler||" as typed. Mentions are
// already inert through allowed_mentions (Docs/protocol/notify.md §Payload,
// review 12).
func discordEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(discordEscapeChars, s[i]) >= 0 {
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}
