package approval

import "testing"

// TestCodeMACVector reproduces the vector of Docs/protocol/approval.md
// §Object byte for byte: approval_key = bytes 00..1f, id =
// a-0123456789abcdef0123456789abcdef, code = 482913.
func TestCodeMACVector(t *testing.T) {
	var key [32]byte
	for i := range key {
		key[i] = byte(i)
	}
	got := CodeMACHex(key, "a-0123456789abcdef0123456789abcdef", "482913")
	want := "8580a34986f6affcb96bbee33cc575eea83ac9b65c8e050b1ff64850680bb5ce"
	if got != want {
		t.Fatalf("code_mac = %s, want %s", got, want)
	}
}
