package request

import "testing"

// TestPriorityVectors is the vectors table from Docs/protocol/request.md
// §Effective priority.
func TestPriorityVectors(t *testing.T) {
	cases := []struct {
		base, n, a, want int
	}{
		{1, 0, 0, 1000},
		{1, 100, 100, 1000},
		{2, 0, 0, 2000},
		{2, 100, 100, 2000},
		{3, 0, 0, 3000},
		{4, 0, 0, 4000},
		{3, 4, 1, 2500},
		{4, 4, 1, 3000},
		{4, 10, 0, 2333},
		{3, 1, 1, 3000},
	}
	for _, tc := range cases {
		got := Priority(tc.base, tc.n, tc.a)
		if got != tc.want {
			t.Errorf("Priority(%d, %d, %d) = %d, want %d", tc.base, tc.n, tc.a, got, tc.want)
		}
	}
}

func TestUrgencyBase(t *testing.T) {
	cases := map[string]int{
		UrgencyLow: 1, UrgencyNormal: 2, UrgencyHigh: 3, UrgencyBlocking: 4, "bogus": 0,
	}
	for u, want := range cases {
		if got := UrgencyBase(u); got != want {
			t.Errorf("UrgencyBase(%q) = %d, want %d", u, got, want)
		}
	}
}
