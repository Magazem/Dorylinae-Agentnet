package daemon

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// A device.unlink naming a link id revokes only links with msg.from: a third
// peer that learned the id changes nothing (review 24 M8, ticket 2.D1). The
// e2e test cannot forge this mail any more (mail_submit refuses device.*,
// review 36 M1), so the naming case is checked at the Kind.
func TestDeviceUnlinkNamingAnotherPeersLink(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ds := &device.Store{DB: st.DB(), Self: "H"}
	const id = "l-00112233445566778899aabbccddeeff"
	if _, err := st.DB().Exec(`INSERT INTO device_links (id, peer, role, state, nonce, created, activated_at, updated)
		VALUES (?, 'C', 'helper', 'active', ?, '2026-10-01T09:00:00.000Z', '2026-10-01T09:00:00.000Z', '2026-10-01T09:00:00.000Z')`, id, testNonce); err != nil {
		t.Fatal(err)
	}
	apply := func(from string) {
		t.Helper()
		tx, err := st.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		op := &mail.Opened{Msg: mail.Msg{From: from, Kind: device.KindUnlink, Body: map[string]any{"at": "2026-10-01T09:05:00Z", "link": id}}}
		if err := deviceUnlinkKind(ds).Apply(ctx, tx, op); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	state := func() string {
		t.Helper()
		var s string
		if err := st.DB().QueryRow(`SELECT state FROM device_links WHERE id = ?`, id).Scan(&s); err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		return s
	}
	apply("X")
	if s := state(); s != device.StateActive {
		t.Fatalf("a third peer's device.unlink naming the link: state = %s, want active", s)
	}
	apply("C")
	if s := state(); s != device.StateRevoked {
		t.Fatalf("the linked peer's device.unlink: state = %s, want revoked", s)
	}
}

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
		"12 345 678":           "…",
		"x999999y111111":       "x…y…",
		"code 000000 then 42.": "code … then 42.",
		// review 36 L2: split or non-ASCII digits read as the same code.
		"bob? Code 482 913":   "bob? Code …",
		"48-29-13":            "…",
		"４８２９１３":              "…",
		"v1.2.3 and 12, 3456": "v1.2.3 and 12, 3456",
		"a 12 b 3456":         "a 12 b 3456",
	} {
		if got := stripLongDigits(in); got != want {
			t.Errorf("stripLongDigits(%q) = %q, want %q", in, got, want)
		}
	}
}
