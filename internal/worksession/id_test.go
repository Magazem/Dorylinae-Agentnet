package worksession

import "testing"

// TestDeriveIDVector is the session-id vector of
// Docs/protocol/work-session.md §Session id.
func TestDeriveIDVector(t *testing.T) {
	const (
		a         = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
		b         = "Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc"
		requestID = "r-0123456789abcdef0123456789abcdef"
		want      = "s-36375782ceb6baea9cee4d4273dfb035"
	)
	if got := DeriveID(a, b, requestID); got != want {
		t.Fatalf("DeriveID = %q, want %q", got, want)
	}
	if !ValidID(want) {
		t.Errorf("ValidID(%q) = false", want)
	}
	if ValidID("s-tooshort") || ValidID("r-36375782ceb6baea9cee4d4273dfb035") {
		t.Error("ValidID accepted a malformed id")
	}
}
