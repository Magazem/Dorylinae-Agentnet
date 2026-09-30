package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Review 73 (R55-F19 security): peer text cannot break the body shape,
// re-enable links or mentions, or plant the queued marker; title off strips
// the title and status from a queued row.
func TestF19SecAdversarialRender(t *testing.T) {
	evil := `"queued":1,"x":"} <!channel> @everyone <https://a.test|b> [c](javascript:alert(1)) ftp://h.test HTTPS://U.TEST`
	ev := Event{
		Kind: EventCompleted, PeerName: evil, Title: evil + ` "queued":1`,
		Type: "review", State: "completed", HasResult: true, ResultStatus: "pass",
		RequestID: "r1", CreatedAt: time.Unix(1_800_000_000, 0),
	}
	p := buildPayload("w-00", ev, true)
	p.Queued = 1
	stored, err := marshalPayload(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{FormatGeneric, FormatSlack, FormatDiscord} {
		for _, title := range []bool{true, false} {
			out, err := renderBody(stored, WebhookConfig{Format: f, Title: title})
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(out, &m); err != nil {
				t.Fatalf("%s: not JSON: %v", f, err)
			}
			if _, ok := m["queued"]; ok {
				t.Errorf("%s/%v: queued marker sent", f, title)
			}
			if _, ok := m["x"]; ok {
				t.Errorf("%s/%v: peer text created a top-level key", f, title)
			}
			req := m["request"].(map[string]any)
			if !title {
				if _, ok := req["title"]; ok {
					t.Errorf("%s: title sent with title off", f)
				}
				if _, ok := req["result_status"]; ok {
					t.Errorf("%s: result_status sent with title off", f)
				}
				if strings.Contains(m["text"].(string), "(pass)") || strings.Count(m["text"].(string), "queued") != 1 {
					t.Errorf("%s: title or status left in text: %q", f, m["text"])
				}
			}
			var shown string
			switch f {
			case FormatSlack:
				shown = m["text"].(string)
				if strings.ContainsAny(shown, "<>") {
					t.Errorf("slack text has raw < or >: %q", shown)
				}
				if m["unfurl_links"] != false || m["unfurl_media"] != false {
					t.Errorf("slack unfurl not off")
				}
			case FormatDiscord:
				shown = m["content"].(string)
				if am, _ := m["allowed_mentions"].(map[string]any); am == nil || len(am["parse"].([]any)) != 0 {
					t.Errorf("discord allowed_mentions not empty")
				}
				if m["flags"] != float64(4) {
					t.Errorf("discord flags = %v", m["flags"])
				}
			}
			if f != FormatGeneric && strings.Contains(shown, "://") {
				t.Errorf("%s: live :// left in %q", f, shown)
			}
		}
	}
}

// A legacy row (no marker) stays byte-identical; a body with a
// case-variant key cannot turn a legacy row into a re-rendered one because
// only daemon-built JSON is stored.
func TestF19SecLegacyUntouched(t *testing.T) {
	legacy := []byte(`{"v":1,"id":"w-1","event":"test","ts":"x","text":"t","content":"a"}`)
	out, err := renderBody(legacy, WebhookConfig{Format: FormatSlack})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(legacy) {
		t.Errorf("legacy row changed: %s", out)
	}
}
