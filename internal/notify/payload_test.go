package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildPayloadPrivacy(t *testing.T) {
	ev := Event{
		Kind: EventCompleted, PeerName: "bob", PeerFP: "FP123", Type: "review",
		Title: "the brief must never appear", RequestID: "r-1", State: "completed",
		HasResult: true, ResultStatus: "pass", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	// title: false (default) must omit the title and the result status.
	p := buildPayload("w-1", ev, false)
	body, err := marshalPayload(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	req := m["request"].(map[string]any)
	if _, ok := req["title"]; ok {
		t.Error("title present with title:false")
	}
	if _, ok := req["result_status"]; ok {
		t.Error("result_status present with title:false")
	}
	if strings.Contains(string(body), "the brief must never appear") {
		t.Error("title text leaked into the payload with title:false")
	}

	// title: true includes both.
	p = buildPayload("w-2", ev, true)
	body, err = marshalPayload(p)
	if err != nil {
		t.Fatal(err)
	}
	m = nil
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	req = m["request"].(map[string]any)
	if req["title"] != "the brief must never appear" {
		t.Errorf("title = %v", req["title"])
	}
	if req["result_status"] != "pass" {
		t.Errorf("result_status = %v", req["result_status"])
	}
	peer := m["peer"].(map[string]any)
	if peer["fingerprint"] != "FP123" {
		t.Errorf("fingerprint = %v", peer["fingerprint"])
	}
}

func TestBuildPayloadTestEvent(t *testing.T) {
	p := buildPayload("w-3", Event{Kind: EventTest, CreatedAt: time.Now()}, true)
	if p.Request != nil {
		t.Error("request must be omitted for the test event")
	}
	if p.Text != "agentnet test notification" {
		t.Errorf("text = %q", p.Text)
	}
}

func TestApplyFormatSlackEscapesMentions(t *testing.T) {
	generic, err := marshalPayload(buildPayload("w-4", Event{
		Kind: EventReceived, PeerName: "<!channel>", Type: "review", Urgency: "high",
		CreatedAt: time.Now(),
	}, false))
	if err != nil {
		t.Fatal(err)
	}
	text := "High review request from <!channel>"
	out, err := applyFormat(FormatSlack, generic, text)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	got := m["text"].(string)
	if strings.Contains(got, "<!channel>") {
		t.Errorf("slack text still contains a raw channel ping: %q", got)
	}
	if got != "High review request from &lt;!channel&gt;" {
		t.Errorf("text = %q", got)
	}

	link := "<https://evil.example|click>"
	escaped := slackEscape(link)
	if strings.Contains(escaped, "<") || strings.Contains(escaped, ">") {
		t.Errorf("slackEscape left an angle bracket: %q", escaped)
	}
}

func TestApplyFormatDiscordAllowedMentions(t *testing.T) {
	generic, err := marshalPayload(buildPayload("w-5", Event{
		Kind: EventReceived, PeerName: "@everyone", Type: "review", Urgency: "high",
		CreatedAt: time.Now(),
	}, false))
	if err != nil {
		t.Fatal(err)
	}
	out, err := applyFormat(FormatDiscord, generic, "High review request from @everyone")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["content"] != "High review request from @everyone" {
		t.Errorf("content = %v", m["content"])
	}
	am, ok := m["allowed_mentions"].(map[string]any)
	if !ok {
		t.Fatal("allowed_mentions missing")
	}
	parse, ok := am["parse"].([]any)
	if !ok || len(parse) != 0 {
		t.Errorf("allowed_mentions.parse = %v, want an empty list", am["parse"])
	}
}
