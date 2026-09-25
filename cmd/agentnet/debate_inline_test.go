package main

// DX-2: unit tests for the inline entry flag builders (Docs/cli/debate.md,
// Docs/protocol/debate.md §Messages) and the self-explaining error hint, plus
// one e2e round trip that submits every entry with inline flags only (after
// A's start, which still needs --position-file).

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

func TestInlineDebateKind(t *testing.T) {
	cases := []struct {
		name    string
		set     map[string]bool
		want    string
		wantErr bool
	}{
		{"none", map[string]bool{}, "", false},
		{"position by claim", map[string]bool{"claim": true}, "position", false},
		{"position by assumption", map[string]bool{"assumption": true}, "position", false},
		{"move by pass", map[string]bool{"pass": true}, "move", false},
		{"move by challenge", map[string]bool{"challenge": true}, "move", false},
		{"move by revise", map[string]bool{"revise-claim": true, "revise-argument": true}, "move", false},
		{"proposal", map[string]bool{"agree": true}, "proposal", false},
		{"answer accept", map[string]bool{"accept": true}, "answer", false},
		{"answer reject", map[string]bool{"reject": true}, "answer", false},
		{"remaining alone", map[string]bool{"remaining": true}, "", true},
		{"two kinds", map[string]bool{"claim": true, "agree": true}, "", true},
		{"move and answer", map[string]bool{"pass": true, "accept": true}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := inlineDebateKind(c.set)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got kind %q", got)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got (%q, %v), want (%q, nil)", got, err, c.want)
			}
		})
	}
}

func TestBuildInlinePosition(t *testing.T) {
	entry, err := buildInlinePosition("Use capped backoff", "Keeps retries bounded.", []string{"Clock skew is under 5s"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(entry, &got); err != nil {
		t.Fatal(err)
	}
	if got["claim"] != "Use capped backoff" || got["argument"] != "Keeps retries bounded." {
		t.Fatalf("entry = %s", entry)
	}
	if a, ok := got["assumptions"].([]any); !ok || len(a) != 1 || a[0] != "Clock skew is under 5s" {
		t.Fatalf("assumptions = %v", got["assumptions"])
	}

	if _, err := buildInlinePosition("", "x", nil); err == nil {
		t.Fatal("want error for missing --claim")
	}
	if _, err := buildInlinePosition("x", "", nil); err == nil {
		t.Fatal("want error for missing --argument")
	}
}

func TestBuildInlineMove(t *testing.T) {
	// Pass.
	entry, err := buildInlineMove(true, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(entry, &got); err != nil {
		t.Fatal(err)
	}
	if ch, ok := got["challenges"].([]any); !ok || len(ch) != 0 {
		t.Fatalf("pass challenges = %v", got["challenges"])
	}

	// A challenge and a revision.
	entry, err = buildInlineMove(false, []string{`claim=Why not use jitter?`}, "New claim", "New argument")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(entry, &got); err != nil {
		t.Fatal(err)
	}
	ch, ok := got["challenges"].([]any)
	if !ok || len(ch) != 1 {
		t.Fatalf("challenges = %v", got["challenges"])
	}
	c0 := ch[0].(map[string]any)
	if targets, ok := c0["targets"].([]any); !ok || len(targets) != 1 || targets[0] != "claim" {
		t.Fatalf("targets = %v", c0["targets"])
	}
	if c0["argument"] != "Why not use jitter?" {
		t.Fatalf("argument = %v", c0["argument"])
	}
	rev, ok := got["revision"].(map[string]any)
	if !ok || rev["claim"] != "New claim" || rev["argument"] != "New argument" {
		t.Fatalf("revision = %v", got["revision"])
	}

	// Mixed-form refusals within a move.
	if _, err := buildInlineMove(true, []string{"claim=x"}, "", ""); err == nil {
		t.Fatal("want error: --pass with --challenge")
	}
	if _, err := buildInlineMove(false, nil, "New claim", ""); err == nil {
		t.Fatal("want error: --revise-claim without --revise-argument")
	}
	if _, err := buildInlineMove(false, nil, "", ""); err == nil {
		t.Fatal("want error: nothing given")
	}
	// Bad target string: no "=".
	if _, err := buildInlineMove(false, []string{"claim without equals"}, "", ""); err == nil {
		t.Fatal("want error for a --challenge value with no TARGET=ARGUMENT split")
	}
	// Bad target string: empty target.
	if _, err := buildInlineMove(false, []string{"=argument only"}, "", ""); err == nil {
		t.Fatal("want error for a --challenge value with an empty target")
	}
}

func TestBuildInlineProposalAndAnswer(t *testing.T) {
	entry, err := buildInlineProposal("Capped backoff with jitter", "")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(entry, &got); err != nil {
		t.Fatal(err)
	}
	agreement, ok := got["agreement"].(map[string]any)
	if !ok || agreement["decision"] != "Capped backoff with jitter" {
		t.Fatalf("agreement = %v", got["agreement"])
	}
	if _, ok := got["remaining_disagreement"]; ok {
		t.Fatalf("remaining_disagreement present without --remaining: %v", got)
	}
	if _, err := buildInlineProposal("", ""); err == nil {
		t.Fatal("want error for missing --agree")
	}

	entry, err = buildInlineProposal("Decision", "Still unsure about timeout")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(entry, &got); err != nil {
		t.Fatal(err)
	}
	rd, ok := got["remaining_disagreement"].([]any)
	if !ok || len(rd) != 1 {
		t.Fatalf("remaining_disagreement = %v", got["remaining_disagreement"])
	}
	item := rd[0].(map[string]any)
	if item["point"] != "Still unsure about timeout" || item["initiator"] != "Still unsure about timeout" || item["respondent"] != "Still unsure about timeout" {
		t.Fatalf("remaining_disagreement item = %v", item)
	}

	answerEntry, err := buildInlineAnswer(true, false, "")
	if err != nil {
		t.Fatal(err)
	}
	var gotAnswer map[string]any
	if err := json.Unmarshal(answerEntry, &gotAnswer); err != nil {
		t.Fatal(err)
	}
	if gotAnswer["accept"] != true {
		t.Fatalf("accept = %v", gotAnswer["accept"])
	}
	if _, err := buildInlineAnswer(false, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := buildInlineAnswer(true, true, ""); err == nil {
		t.Fatal("want error: both --accept and --reject")
	}
	if _, err := buildInlineAnswer(false, false, ""); err == nil {
		t.Fatal("want error: neither --accept nor --reject")
	}
}

// TestDebateMixedFormRefusal: giving a file flag and inline flags together,
// or inline flags of two kinds together, is a usage error, not silently
// picking one.
func TestDebateMixedFormRefusal(t *testing.T) {
	shortHome(t)
	const sid = "s-0123456789abcdef0123456789abcdef"
	for name, args := range map[string][]string{
		"file and inline claim":  {"debate", sid, "--move-file", "f", "--claim", "x", "--argument", "y"},
		"file and inline pass":   {"debate", sid, "--propose-file", "f", "--pass"},
		"position and move kind": {"debate", sid, "--claim", "x", "--argument", "y", "--pass"},
		"move and answer kind":   {"debate", sid, "--pass", "--accept"},
		"remaining without kind": {"debate", sid, "--remaining", "still unsure"},
		"inline flags to a peer": {"debate", "@bob", "--topic", "t", "--claim", "x", "--argument", "y"},
	} {
		var out, errb bytes.Buffer
		if c := run(args, &out, &errb); c != exitUsage {
			t.Errorf("%s: code %d (stdout %q stderr %q)", name, c, out.String(), errb.String())
		}
	}
}

// TestDebateInlineSubmitCLI drives runDebateSubmit against a fake daemon to
// check the inline flags reach debate_submit with the right kind and entry,
// and that a bad_request/not_your_turn error gets the shape+example hint
// appended.
func TestDebateInlineSubmitCLI(t *testing.T) {
	p := shortHome(t)
	var mu sync.Mutex
	var gotKind string
	var gotEntry json.RawMessage
	nextErr := ""
	startFakeDaemon(t, p, map[string]ipc.HandlerFunc{
		"debate_submit": func(_ context.Context, raw json.RawMessage) (any, error) {
			var params struct {
				ID    string
				Kind  string
				Entry json.RawMessage
			}
			if err := json.Unmarshal(raw, &params); err != nil {
				return nil, err
			}
			mu.Lock()
			gotKind, gotEntry = params.Kind, params.Entry
			e := nextErr
			mu.Unlock()
			switch e {
			case "bad_request":
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "entry.claim: must hold 1-280 code points"}
			case "not_your_turn":
				return nil, &ipc.Error{Code: daemon.CodeNotYourTurn, Message: "not your turn: slot 3 expects a move from the respondent"}
			}
			return daemon.DebateSubmitResult{Debate: daemon.DebateView{Session: params.ID, Phase: "rounds", Turn: "peer"}}, nil
		},
		"debate_show": func(_ context.Context, _ json.RawMessage) (any, error) {
			return daemon.DebateShowResult{Debate: daemon.DebateView{Expect: "move"}}, nil
		},
	})
	const sid = "s-0123456789abcdef0123456789abcdef"

	var out, errb bytes.Buffer
	if c := run([]string{"debate", sid, "--claim", "Use capped backoff", "--argument", "Keeps retries bounded.", "--json"}, &out, &errb); c != exitOK {
		t.Fatalf("code %d: %s %s", c, out.String(), errb.String())
	}
	mu.Lock()
	kind, entry := gotKind, gotEntry
	mu.Unlock()
	if kind != "position" {
		t.Fatalf("kind = %q", kind)
	}
	var entryBody map[string]any
	if err := json.Unmarshal(entry, &entryBody); err != nil || entryBody["claim"] != "Use capped backoff" {
		t.Fatalf("entry = %s (%v)", entry, err)
	}

	mu.Lock()
	nextErr = "bad_request"
	mu.Unlock()
	out.Reset()
	c := run([]string{"debate", sid, "--pass", "--json"}, &out, &errb)
	if c != exitError || !strings.Contains(out.String(), "bad_request") {
		t.Fatalf("bad_request: code %d, out %q", c, out.String())
	}
	if !strings.Contains(out.String(), "challenges") || !strings.Contains(out.String(), "--pass") {
		t.Fatalf("bad_request hint missing the move shape/example: %q", out.String())
	}

	mu.Lock()
	nextErr = "not_your_turn"
	mu.Unlock()
	out.Reset()
	c = run([]string{"debate", sid, "--pass", "--json"}, &out, &errb)
	if c != exitError || !strings.Contains(out.String(), "not_your_turn") {
		t.Fatalf("not_your_turn: code %d, out %q", c, out.String())
	}
	if !strings.Contains(out.String(), "challenges") {
		t.Fatalf("not_your_turn hint missing the expected (move) shape: %q", out.String())
	}
}

// TestDebateInlineRoundTrip: the same round trip as TestDebateRoundTrip, but
// every submission after A's start (which still needs --position-file) uses
// inline flags only, through two real daemons and a relay.
func TestDebateInlineRoundTrip(t *testing.T) {
	oldInterval := waitPollInterval
	waitPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = oldInterval })
	a, b := consultTeam(t)
	dir := t.TempDir()

	posA := writeJSONFile(t, dir, "posA.json", `{"claim":"Use capped backoff","argument":"Keeps retries bounded after an outage."}`)
	code, out, errs := cli(t, a, "debate", "@bob", "--topic", "How should the outbox retry?",
		"--position-file", posA, "--rounds", "1", "--json")
	if code != exitOK {
		t.Fatalf("debate start: %d %s %s", code, out, errs)
	}
	var sub requestBody
	if err := json.Unmarshal([]byte(out), &sub); err != nil || !sub.OK || sub.Session == "" {
		t.Fatalf("debate start --json = %q: %v", out, err)
	}
	pollCLI(t, b, "the debate invitation", func(o string) bool { return strings.Contains(o, sub.ID) }, "inbox", "--json")

	// B's one-step accept + position, inline.
	code, out, errs = cli(t, b, "debate", sub.Session, "--claim", "Use a fixed retry interval",
		"--argument", "Simpler to reason about.", "--json")
	if code != exitOK {
		t.Fatalf("B one-step accept + position (inline): %d %s %s", code, out, errs)
	}

	waitCode, w := debateWaitOn(t, a, sub.Session, "10")
	if waitCode != exitOK || w.Wait != "turn" || w.Debate == nil || w.Debate.Expect != "move" {
		t.Fatalf("A wait after B's position = %d %+v", waitCode, w)
	}
	if w.Debate.Hint == "" {
		t.Fatalf("debate view has no hint for A's turn: %+v", w.Debate)
	}

	// A passes (inline).
	if code, out, errs := cli(t, a, "debate", sub.Session, "--pass", "--json"); code != exitOK {
		t.Fatalf("A pass: %d %s %s", code, out, errs)
	}
	if waitCode, w := debateWaitOn(t, b, sub.Session, "10"); waitCode != exitOK || w.Wait != "turn" || w.Debate.Expect != "move" {
		t.Fatalf("B wait after A's move = %d %+v", waitCode, w)
	}
	// B passes (inline).
	if code, out, errs := cli(t, b, "debate", sub.Session, "--pass", "--json"); code != exitOK {
		t.Fatalf("B pass: %d %s %s", code, out, errs)
	}

	// Round 1 of 1 used up: A's proposal, inline.
	waitCode, w = debateWaitOn(t, a, sub.Session, "10")
	if waitCode != exitOK || w.Wait != "turn" || w.Debate.Expect != "proposal" {
		t.Fatalf("A wait at converge = %d %+v", waitCode, w)
	}
	if code, out, errs := cli(t, a, "debate", sub.Session, "--agree", "Use capped backoff with jitter", "--json"); code != exitOK {
		t.Fatalf("A propose (inline): %d %s %s", code, out, errs)
	}
	waitCode, w = debateWaitOn(t, b, sub.Session, "10")
	if waitCode != exitOK || w.Wait != "turn" || w.Debate.Expect != "answer" {
		t.Fatalf("B wait for the proposal = %d %+v", waitCode, w)
	}
	// B accepts, inline.
	if code, out, errs := cli(t, b, "debate", sub.Session, "--accept", "--json"); code != exitOK {
		t.Fatalf("B answer (inline): %d %s %s", code, out, errs)
	}

	waitCode, w = debateWaitOn(t, b, sub.Session, "10")
	if waitCode != exitOK || w.Wait != "closed" || w.Debate == nil || w.Debate.Phase != "closed" || w.Debate.Outcome != "agreed" {
		t.Fatalf("B wait at the end = %d %+v", waitCode, w)
	}
	pollCLI(t, a, "A's debate to close (B signed)", func(o string) bool {
		return strings.Contains(o, `"phase":"closed"`) && strings.Contains(o, `"outcome":"agreed"`)
	}, "debate", sub.Session, "--json")

	for name, n := range map[string]*testNode{"alice": a, "bob": b} {
		text := auditText(t, n)
		for _, secret := range []string{"capped backoff", "Capped backoff", "fixed retry interval", "jitter"} {
			if strings.Contains(text, secret) {
				t.Errorf("%s audit holds debate content %q", name, secret)
			}
		}
	}
}
