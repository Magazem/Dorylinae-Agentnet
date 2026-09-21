package envelope

import "testing"

// Vectors from Docs/protocol/pairing.md (Test vectors).
func TestFingerprintVectors(t *testing.T) {
	for key, want := range map[string]string{
		"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg": "2ED9 TGVE R471 63MC C451",
		"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc": "2P56 R8XN KZYG XBXC JB4S",
	} {
		fp, err := KeyFingerprint(key)
		if err != nil {
			t.Fatal(err)
		}
		if got := FormatFingerprint(fp); got != want {
			t.Errorf("fp(%s) = %q, want %q", key, got, want)
		}
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	if got, ok := NormalizeFingerprint("2ed9-tgve r471 63mc c451"); !ok || got != "2ED9TGVER47163MCC451" {
		t.Errorf("got %q, %v", got, ok)
	}
	if got, ok := NormalizeFingerprint("2ED9 TGVE R47l 63MC C451"); !ok || got != "2ED9TGVER47163MCC451" {
		t.Errorf("alias: got %q, %v", got, ok)
	}
	for _, bad := range []string{"", "2ED9 TGVE R471 63MC C45", "2ED9 TGVE R471 63MC C4511", "2ED9 TGVE R471 63MC C45U"} {
		if _, ok := NormalizeFingerprint(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}
