package envelope

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBindCode(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		code, err := NewBindCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != BindCodeLen || strings.Trim(code, PairAlphabet) != "" {
			t.Fatalf("code %q is not %d characters of the alphabet", code, BindCodeLen)
		}
		seen[code] = true
		f := FormatBindCode(code)
		if len(f) != 9 || f[4] != '-' {
			t.Fatalf("formatted %q", f)
		}
		if n, ok := NormalizeBindCode(strings.ToLower(f)); !ok || n != code {
			t.Fatalf("normalise %q = %q, %v", f, n, ok)
		}
	}
	if len(seen) < 49 {
		t.Fatalf("only %d distinct codes of 50", len(seen))
	}
	if n, ok := NormalizeBindCode("wdjb mjht"); !ok || n != "WDJBMJHT" {
		t.Fatalf("spaces: %q %v", n, ok)
	}
	if n, ok := NormalizeBindCode("OIL0-1111"); !ok || n != "01101111" {
		t.Fatalf("aliases: %q %v", n, ok)
	}
	for _, bad := range []string{"", "WDJB-MJH", "WDJB-MJHTX", "WDJB-MJHU", "https://x/?code=WDJBMJHT"} {
		if _, ok := NormalizeBindCode(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBindStartLabels(t *testing.T) {
	for _, ok := range []string{"laptop", "Desk top_2.home-1", strings.Repeat("a", MaxDeviceLen)} {
		if !ValidDevice(ok) {
			t.Errorf("device %q refused", ok)
		}
	}
	for _, bad := range []string{"", strings.Repeat("a", MaxDeviceLen+1), "<b>", "a\nb", "café", "a" + string(rune(0x202e)) + "b"} {
		if ValidDevice(bad) {
			t.Errorf("device %q accepted", bad)
		}
	}
	for _, ok := range []string{"windows", "linux", "darwin", "freebsd", "plan9"} {
		if !ValidOS(ok) {
			t.Errorf("os %q refused", ok)
		}
	}
	for _, bad := range []string{"", "Linux", "linux ", strings.Repeat("a", MaxOSLen+1)} {
		if ValidOS(bad) {
			t.Errorf("os %q accepted", bad)
		}
	}
}

func TestAccountFramesWireForm(t *testing.T) {
	raw, err := json.Marshal(Control{Op: OpReady, PublicKey: "k", Features: []string{FeatureEphemeral, FeatureAccounts},
		Account: &Account{State: AccountBound, ID: "acc_1", Display: "@octocat", Group: "qg_1"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"op":"ready","public_key":"k","features":["ephemeral","accounts"],"account":{"state":"bound","id":"acc_1","display":"@octocat","group":"qg_1"}}`
	if string(raw) != want {
		t.Fatalf("ready =\n%s\nwant\n%s", raw, want)
	}
	raw, _ = json.Marshal(Control{Op: OpBindPending, Ref: "bnd_1", UserCode: "WDJB-MJHT", URL: "https://relay.example/login", Expires: "2026-01-01T00:00:00Z", Interval: 5})
	want = `{"op":"bind_pending","expires":"2026-01-01T00:00:00Z","ref":"bnd_1","user_code":"WDJB-MJHT","url":"https://relay.example/login","interval":5}`
	if string(raw) != want {
		t.Fatalf("bind_pending =\n%s\nwant\n%s", raw, want)
	}
	raw, _ = json.Marshal(Control{Op: OpAccountChanged})
	if string(raw) != `{"op":"account_changed"}` {
		t.Fatalf("account_changed = %s", raw)
	}
	raw, _ = json.Marshal(Control{Op: OpReady, PublicKey: "k", Features: []string{FeatureEphemeral}})
	if strings.Contains(string(raw), "account") {
		t.Fatalf("ready without accounts carries %s", raw)
	}
}
