package main

import "testing"

// TestElideWebhookURL checks the human output hides the webhook path, which
// is a bearer secret for Slack and Discord (Docs/cli/notify.md).
func TestElideWebhookURL(t *testing.T) {
	cases := map[string]string{
		"https://chat.example.com/services/a/b/c": "https://chat.example.com/…",
		"https://hooks.example.com":               "https://hooks.example.com",
		"https://hooks.example.com/":              "https://hooks.example.com",
		"http://127.0.0.1:8080/hook?k=x":          "http://127.0.0.1:8080/…",
		"https://h.example/?k=x":                  "https://h.example/…",
		"::bad":                                   "…",
	}
	for in, want := range cases {
		if got := elideWebhookURL(in); got != want {
			t.Errorf("elideWebhookURL(%q) = %q, want %q", in, got, want)
		}
	}
}
