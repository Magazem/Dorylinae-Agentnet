package experience

import (
	"strings"
	"testing"
)

// R55-F29 test 11 (R55-222): verification is always in the enum, and
// verification_by is present iff it is not none.
func TestBuildVerificationAlwaysInEnum(t *testing.T) {
	for _, tc := range []struct {
		name              string
		mutate            func(*Input)
		want, wantBy      string
		wantByMissing     bool
		wantWorkedVerific string
	}{
		{"work accepted, none", func(in *Input) { in.Verification, in.VerificationBy = "", "" }, "none", "", true, ""},
		{"work cancelled with a stale value", func(in *Input) {
			in.Outcome, in.Verification, in.VerificationBy = "cancelled", "tests_passed", "worker"
		}, "none", "", true, ""},
		{"work accepted, human_accepted", func(in *Input) {
			in.Verification, in.VerificationBy = "human_accepted", "requester"
			in.WorkedStatus = "pass"
		}, "human_accepted", "requester", false, "human_accepted"},
		{"debate", func(in *Input) {
			in.Kind, in.Role, in.Outcome, in.Verification, in.VerificationBy = KindDebate, "initiator", "agreed", "", ""
		}, "none", "", true, ""},
	} {
		in := baseInput()
		tc.mutate(&in)
		canon, _, err := Build(in)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		m := decodeCanon(t, canon)
		if m["verification"] != tc.want {
			t.Errorf("%s: verification = %v, want %q", tc.name, m["verification"], tc.want)
		}
		by, has := m["verification_by"]
		if has == tc.wantByMissing || (has && by != tc.wantBy) {
			t.Errorf("%s: verification_by = %v (present %v), want %q", tc.name, by, has, tc.wantBy)
		}
		if tc.wantWorkedVerific != "" {
			if w, _ := m["worked"].(map[string]any); w["verification"] != tc.wantWorkedVerific {
				t.Errorf("%s: worked = %v", tc.name, m["worked"])
			}
		}
	}
}

// An escalated debate always records the Decision's count, 0 included
// (OD-F29-6), and a cancelled one carries cancelled_by and cause.
func TestBuildDebateFailedMembers(t *testing.T) {
	in := baseInput()
	in.Kind, in.Role, in.Outcome = KindDebate, "respondent", "escalated"
	canon, _, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := decodeCanon(t, canon)["failed"].(map[string]any); f["remaining_disagreement_points"] != float64(0) {
		t.Fatalf("failed = %v, want remaining_disagreement_points 0", f)
	}

	in.Outcome, in.CancelledBy, in.Cause = "cancelled", "respondent", CauseDecisionRefused
	in.RemainingDisagreementPoints = 2
	in.Decision = &Decision{ID: "d-1", Hash: "abc"}
	canon, _, err = Build(in)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeCanon(t, canon)
	f, _ := m["failed"].(map[string]any)
	if len(f) != 2 || f["cancelled_by"] != "respondent" || f["cause"] != CauseDecisionRefused {
		t.Fatalf("failed = %v", f)
	}
	acc, _ := m["acceptance"].(map[string]any)
	if d, _ := acc["decision"].(map[string]any); acc["outcome"] != "cancelled" || d["id"] != "d-1" || d["hash"] != "abc" {
		t.Fatalf("acceptance = %v", acc)
	}
	if _, ok := m["worked"]; ok {
		t.Fatalf("worked on a cancelled record: %v", m["worked"])
	}
}

// The 4th truncation step drops approach.grants: a work session with 2000
// grants and a 16 KiB brief fits the cap.
func TestBuildDropsGrantsLast(t *testing.T) {
	in := baseInput()
	in.ProblemBrief = strings.Repeat("b", 16384)
	for range 2000 {
		in.Grants = append(in.Grants, Grant{Action: "fs.read", Sensitive: true, State: "revoked"})
	}
	canon, truncated, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(canon) > MaxRecordBytes {
		t.Fatalf("truncated %v, %d bytes", truncated, len(canon))
	}
	m := decodeCanon(t, canon)
	if _, ok := m["approach"].(map[string]any)["grants"]; ok {
		t.Fatal("approach.grants survived truncation")
	}
	if m["truncated"] != true {
		t.Fatal(`"truncated" missing`)
	}
}
