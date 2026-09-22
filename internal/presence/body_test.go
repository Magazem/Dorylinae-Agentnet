package presence

import (
	"fmt"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

var testNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func teamID(n int) string { return fmt.Sprintf("t-%032x", n) }

// TestPaddingMultipleOf256 is the 1.2b acceptance test: the plaintext length
// is a multiple of 256 for every combination of flags and 0-32 epochs.
func TestPaddingMultipleOf256(t *testing.T) {
	from := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	to := "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	id := mail.NewPresenceID()

	for _, state := range []string{"online", "offline"} {
		for agent := 0; agent <= 1; agent++ {
			for human := 0; human <= 2; human++ {
				for _, n := range []int{0, 1, 5, 32} {
					epochs := map[string]int64{}
					for i := 0; i < n; i++ {
						epochs[teamID(i)] = int64(i)
					}
					b := Body{State: state, Agent: agent, Human: human, Boot: "0123456789abcdef", Seq: 17, Interval: 30, Epochs: epochs}
					body, err := Build(b, from, to, id, testNow)
					if err != nil {
						t.Fatalf("state=%s agent=%d human=%d n=%d: %v", state, agent, human, n, err)
					}
					l, err := canonicalLen(from, to, id, testNow, wireBody{
						Agent: b.Agent, Boot: b.Boot, Epochs: epochs, Human: b.Human,
						Interval: b.Interval, Pad: body.(wireBody).Pad, Seq: b.Seq, State: b.State,
					})
					if err != nil {
						t.Fatal(err)
					}
					if l%256 != 0 {
						t.Fatalf("state=%s agent=%d human=%d n=%d: length %d not a multiple of 256", state, agent, human, n, l)
					}
				}
			}
		}
	}
}

// TestBuildParseRoundTrip checks that a body built by Build parses back with Parse.
func TestBuildParseRoundTrip(t *testing.T) {
	from := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	to := "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	id := mail.NewPresenceID()
	want := Body{State: "online", Agent: 1, Human: 1, Boot: "0123456789abcdef", Seq: 3, Interval: 45, Epochs: map[string]int64{teamID(0): 5}}
	wire, err := Build(want, from, to, id, testNow)
	if err != nil {
		t.Fatal(err)
	}
	generic := roundTripJSON(t, wire)
	got, err := Parse(generic)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != want.State || got.Agent != want.Agent || got.Human != want.Human ||
		got.Boot != want.Boot || got.Seq != want.Seq || got.Interval != want.Interval ||
		got.Epochs[teamID(0)] != 5 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestParseRejectsBadBodies(t *testing.T) {
	base := func() map[string]any {
		return roundTripJSON(t, wireBody{
			Agent: 0, Boot: "0123456789abcdef", Epochs: map[string]int64{}, Human: 2,
			Interval: 30, Pad: "", Seq: 1, State: "offline",
		})
	}
	cases := map[string]func(m map[string]any){
		"bad state":        func(m map[string]any) { m["state"] = "away" },
		"agent on offline": func(m map[string]any) { m["agent"] = jsonNumber(1) },
		"human on offline": func(m map[string]any) { m["human"] = jsonNumber(1) },
		"bad boot":         func(m map[string]any) { m["boot"] = "not-hex" },
		"seq zero":         func(m map[string]any) { m["seq"] = jsonNumber(0) },
		"interval zero":    func(m map[string]any) { m["interval"] = jsonNumber(0) },
		"interval 301":     func(m map[string]any) { m["interval"] = jsonNumber(301) },
		"bad epoch key":    func(m map[string]any) { m["epochs"] = map[string]any{"nope": jsonNumber(1)} },
		"extra member":     func(m map[string]any) { m["extra"] = "x" },
		"missing member":   func(m map[string]any) { delete(m, "pad") },
		"bad pad char":     func(m map[string]any) { m["pad"] = "01" },
	}
	for name, mut := range cases {
		m := base()
		mut(m)
		if _, err := Parse(m); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	m := base()
	m["interval"] = jsonNumber(1)
	if _, err := Parse(m); err != nil {
		t.Fatalf("interval 1 should be accepted: %v", err)
	}
}
