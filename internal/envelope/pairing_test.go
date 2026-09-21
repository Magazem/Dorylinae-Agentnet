package envelope

import "testing"

func TestPairCodes(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := NewPairCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != PairCodeLen || seen[c] {
			t.Fatalf("bad or repeated code %q", c)
		}
		seen[c] = true
		got, ok := NormalizePairCode(FormatPairCode(c))
		if !ok || got != c {
			t.Fatalf("round trip of %q gave %q, %v", c, got, ok)
		}
	}
}

func TestNormalizePairCode(t *testing.T) {
	if got, ok := NormalizePairCode("oOlIl-abcde"); !ok || got != "00111ABCDE" {
		t.Errorf("got %q, %v", got, ok)
	}
	if got, ok := NormalizePairCode(" a b-c d e f g h j k "); !ok || got != "ABCDEFGHJK" {
		t.Errorf("got %q, %v", got, ok)
	}
	for _, bad := range []string{"", "ABCDE-FGHJ", "ABCDE-FGHJKM", "ABCDE-FGHJU", "ABCDE-FGHJ!"} {
		if _, ok := NormalizePairCode(bad); ok {
			t.Errorf("NormalizePairCode(%q) accepted", bad)
		}
	}
}
