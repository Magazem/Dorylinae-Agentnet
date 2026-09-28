package version

import "testing"

func TestParseRelease(t *testing.T) {
	good := map[string]Release{
		"0.0.0":         {0, 0, 0},
		"1.2.3":         {1, 2, 3},
		"10.20.300":     {10, 20, 300},
		"0.4.0":         {0, 4, 0},
		"999999999.0.1": {999999999, 0, 1},
	}
	for s, want := range good {
		got, ok := ParseRelease(s)
		if !ok || got != want {
			t.Errorf("ParseRelease(%q) = %v, %v; want %v, true", s, got, ok, want)
		}
		if got.String() != s {
			t.Errorf("String() = %q, want %q", got.String(), s)
		}
	}
	for _, s := range []string{"", "1", "1.2", "1.2.3.4", "v1.2.3", "01.2.3", "1.02.3", "1.2.3-beta.1",
		"1.2.3+x", "0.0.0-dev", "0.0.0-dev+a1b2c3d (2026-09-25)", "1..3", "1.2.-3", " 1.2.3", "1.2.3 ",
		"1000000000.0.0", "１.2.3"} {
		if _, ok := ParseRelease(s); ok {
			t.Errorf("ParseRelease(%q) accepted", s)
		}
	}
}

func TestMeetsMinimum(t *testing.T) {
	tests := []struct {
		v, min    string
		meets, ok bool
	}{
		{"1.2.3", "1.2.3", true, true},
		{"1.2.4", "1.2.3", true, true},
		{"1.10.0", "1.9.9", true, true},
		{"2.0.0", "1.99.99", true, true},
		{"1.2.2", "1.2.3", false, true},
		{"0.9.9", "1.0.0", false, true},
		{"1.9.0", "1.10.0", false, true},
		{"0.0.0-dev+abc", "1.0.0", false, false},
		{"1.0.0", "garbage", false, false},
	}
	for _, tc := range tests {
		meets, ok := MeetsMinimum(tc.v, tc.min)
		if meets != tc.meets || ok != tc.ok {
			t.Errorf("MeetsMinimum(%q, %q) = %v, %v; want %v, %v", tc.v, tc.min, meets, ok, tc.meets, tc.ok)
		}
	}
}
