package request

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Review 55 C17-01 (review 68 A6): an optional member present as "" must be
// refused by Decode, not read as absent. request.md makes each 1+ units when
// present (receiver: bad_body).
func TestDecodeRefusesEmptyOptionalMembers(t *testing.T) {
	base := &Request{
		V: 1, ID: "r-" + "0123456789abcdef0123456789abcdef",
		From: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", To: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
		Team: "t-0123456789abcdef0123456789abcdef", Type: TypeTask, Title: "t", Brief: "b",
		Urgency: UrgencyNormal, Artifacts: []Artifact{{Commit: "abcdef1"}},
		Created: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	decode := func(t *testing.T, mut func(m map[string]any)) error {
		t.Helper()
		b, _ := json.Marshal(WireBody(base)["request"])
		v, err := agentcard.ParseStrict(b)
		if err != nil {
			t.Fatal(err)
		}
		m := v.(map[string]any)
		mut(m)
		_, err = Decode(m)
		return err
	}
	if err := decode(t, func(map[string]any) {}); err != nil {
		t.Fatalf("the base request must decode: %v", err)
	}
	cases := map[string]func(m map[string]any){
		"urgency_reason":   func(m map[string]any) { m["urgency_reason"] = "" },
		"urgency_declared": func(m map[string]any) { m["urgency_declared"] = "" },
		"requested_grant.note": func(m map[string]any) {
			m["requested_grant"] = map[string]any{"action": "read", "resource": "x", "note": ""}
		},
		"artifacts[0].url": func(m map[string]any) { m["artifacts"] = []any{map[string]any{"url": "", "commit": "abcdef1"}} },
	}
	for name, mut := range cases {
		if err := decode(t, mut); err == nil {
			t.Errorf("%s = \"\": Decode accepted it; request.md requires 1+ units when present (bad_body)", name)
		}
	}
}
