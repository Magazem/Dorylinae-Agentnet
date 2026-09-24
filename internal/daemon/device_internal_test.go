package daemon

import (
	"errors"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

const testNonce = "00112233445566778899aabbccddeeff"

func offerBodyOf(mut func(map[string]any)) map[string]any {
	b := map[string]any{"at": "2026-10-01T09:00:00Z", "controller": "C", "helper": "H", "nonce": testNonce, "role": "controller"}
	if mut != nil {
		mut(b)
	}
	return b
}

// The sender's key must be the one in its role and the recipient's the other
// (Docs/protocol/device.md §Kinds); every other shape is a bad body.
func TestParseOfferIsStrict(t *testing.T) {
	// A "controller" offer comes from C to H.
	if _, _, err := parseOffer(offerBodyOf(nil), "C", "H"); err != nil {
		t.Fatalf("valid offer: %v", err)
	}
	if _, _, err := parseOffer(offerBodyOf(func(b map[string]any) { b["role"] = "helper" }), "H", "C"); err != nil {
		t.Fatalf("valid helper offer: %v", err)
	}
	cases := map[string]struct {
		body     map[string]any
		from, to string
	}{
		"sender is not the key in its role": {offerBodyOf(nil), "X", "H"},
		"recipient is not the other key":    {offerBodyOf(nil), "C", "X"},
		"role and keys swapped":             {offerBodyOf(func(b map[string]any) { b["role"] = "helper" }), "C", "H"},
		"extra member":                      {offerBodyOf(func(b map[string]any) { b["scope"] = "x" }), "C", "H"},
		"missing member":                    {offerBodyOf(func(b map[string]any) { delete(b, "nonce") }), "C", "H"},
		"unknown role":                      {offerBodyOf(func(b map[string]any) { b["role"] = "admin" }), "C", "H"},
		"short nonce":                       {offerBodyOf(func(b map[string]any) { b["nonce"] = "00ff" }), "C", "H"},
		"uppercase nonce":                   {offerBodyOf(func(b map[string]any) { b["nonce"] = strings.ToUpper(testNonce) }), "C", "H"},
		"bad timestamp":                     {offerBodyOf(func(b map[string]any) { b["at"] = "yesterday" }), "C", "H"},
		"number for a string":               {offerBodyOf(func(b map[string]any) { b["at"] = 5 }), "C", "H"},
	}
	for name, tc := range cases {
		_, _, err := parseOffer(tc.body, tc.from, tc.to)
		if !errors.Is(err, mail.ErrBadBody) {
			t.Errorf("%s: err = %v, want a bad body", name, err)
		}
	}
}

// Peer-supplied text cannot show a decoy approval code (review 26 N4).
func TestStripLongDigits(t *testing.T) {
	for in, want := range map[string]string{
		"bob":                  "bob",
		"bob 123456":           "bob …",
		"bob12345":             "bob12345",
		"a1234567b":            "a…b",
		"482913":               "…",
		"12 345 678":           "12 345 678",
		"x999999y111111":       "x…y…",
		"code 000000 then 42.": "code … then 42.",
	} {
		if got := stripLongDigits(in); got != want {
			t.Errorf("stripLongDigits(%q) = %q, want %q", in, got, want)
		}
	}
}
