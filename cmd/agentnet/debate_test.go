package main

// Ticket 3.1b acceptance, CLI side (Docs/protocol/debate.md, Docs/cli/debate.md):
// a full round trip through two real daemons and a relay, driven only through
// the CLI: start, the one-step accept + position, moves converging after one
// round, the proposal and an accepting answer, `agentnet wait` reporting
// "turn" at each step and "closed" once B's mirror closes, and a cancel in
// "invited".

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

func writeJSONFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// debateWaitOn runs `agentnet wait <id>` against n directly (not through cli(),
// which asserts a 2 s ceiling): the wait loop legitimately polls for longer.
func debateWaitOn(t *testing.T, n *testNode, id string, timeout string) (int, debateWaitBody) {
	t.Helper()
	t.Setenv(paths.HomeEnv, n.p.Dir)
	var wout, werr bytes.Buffer
	code := run([]string{"wait", id, "--timeout", timeout, "--json"}, &wout, &werr)
	var w debateWaitBody
	if err := json.Unmarshal(wout.Bytes(), &w); err != nil {
		t.Fatalf("wait --json = %q (%s): %v", wout.String(), werr.String(), err)
	}
	return code, w
}

func TestDebateRoundTrip(t *testing.T) {
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
	if !strings.HasPrefix(sub.Session, "s-") {
		t.Fatalf("session = %q, want an s- id", sub.Session)
	}

	// A's committed position never reaches B before B's own position leaves
	// B's daemon (Docs/protocol/debate.md §Commit-reveal).
	pollCLI(t, b, "the debate invitation", func(o string) bool { return strings.Contains(o, sub.ID) }, "inbox", "--json")
	_, out, _ = cli(t, b, "debate", sub.Session, "--json")
	if strings.Contains(out, "capped backoff") || strings.Contains(out, "Capped backoff") {
		t.Fatalf("B's debate_show holds A's position before B's own position is sent: %q", out)
	}

	// B's one-step accept + position.
	posB := writeJSONFile(t, dir, "posB.json", `{"claim":"Use a fixed retry interval","argument":"Simpler to reason about."}`)
	code, out, errs = cli(t, b, "debate", sub.Session, "--position-file", posB, "--json")
	if code != exitOK {
		t.Fatalf("B one-step accept + position: %d %s %s", code, out, errs)
	}

	// A's turn: the automatic reveal already ran, next slot is A's move.
	waitCode, w := debateWaitOn(t, a, sub.Session, "10")
	if waitCode != exitOK || w.Wait != "turn" || w.Debate == nil || w.Debate.Expect != "move" {
		t.Fatalf("A wait after B's position = %d %+v", waitCode, w)
	}

	moveEmpty := writeJSONFile(t, dir, "move.json", `{"challenges":[]}`)
	if code, out, errs := cli(t, a, "debate", sub.Session, "--move-file", moveEmpty, "--json"); code != exitOK {
		t.Fatalf("A move: %d %s %s", code, out, errs)
	}
	if waitCode, w := debateWaitOn(t, b, sub.Session, "10"); waitCode != exitOK || w.Wait != "turn" || w.Debate.Expect != "move" {
		t.Fatalf("B wait after A's move = %d %+v", waitCode, w)
	}
	if code, out, errs := cli(t, b, "debate", sub.Session, "--move-file", moveEmpty, "--json"); code != exitOK {
		t.Fatalf("B move: %d %s %s", code, out, errs)
	}

	// Round 1 of 1 is used up: converge, A's proposal next.
	waitCode, w = debateWaitOn(t, a, sub.Session, "10")
	if waitCode != exitOK || w.Wait != "turn" || w.Debate.Expect != "proposal" {
		t.Fatalf("A wait at converge = %d %+v", waitCode, w)
	}
	propose := writeJSONFile(t, dir, "propose.json", `{"agreement":{"decision":"Use capped backoff with jitter"}}`)
	if code, out, errs := cli(t, a, "debate", sub.Session, "--propose-file", propose, "--json"); code != exitOK {
		t.Fatalf("A propose: %d %s %s", code, out, errs)
	}
	waitCode, w = debateWaitOn(t, b, sub.Session, "10")
	if waitCode != exitOK || w.Wait != "turn" || w.Debate.Expect != "answer" {
		t.Fatalf("B wait for the proposal = %d %+v", waitCode, w)
	}
	answer := writeJSONFile(t, dir, "answer.json", `{"accept":true}`)
	if code, out, errs := cli(t, b, "debate", sub.Session, "--answer-file", answer, "--json"); code != exitOK {
		t.Fatalf("B answer: %d %s %s", code, out, errs)
	}

	// B checks A's close, signs the Decision and closes its mirror; A is
	// "closing" until B's debate.sign arrives, then closed (3.3a).
	waitCode, w = debateWaitOn(t, b, sub.Session, "10")
	if waitCode != exitOK || w.Wait != "closed" || w.Debate == nil || w.Debate.Phase != "closed" || w.Debate.Outcome != "agreed" {
		t.Fatalf("B wait at the end = %d %+v", waitCode, w)
	}
	pollCLI(t, a, "A's debate to close (B signed)", func(o string) bool {
		return strings.Contains(o, `"phase":"closed"`) && strings.Contains(o, `"outcome":"agreed"`)
	}, "debate", sub.Session, "--json")

	// agentnet debates lists it on both sides.
	_, out, _ = cli(t, a, "debates", "--json")
	var list struct {
		OK bool `json:"ok"`
		daemon.DebateListResult
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Debates) != 1 || list.Debates[0].Session != sub.Session {
		t.Fatalf("debates --json = %q: %v", out, err)
	}

	// No debate content (topic, positions, arguments) is ever audited.
	for name, n := range map[string]*testNode{"alice": a, "bob": b} {
		text := auditText(t, n)
		for _, secret := range []string{"capped backoff", "Capped backoff", "fixed retry interval", "jitter"} {
			if strings.Contains(text, secret) {
				t.Errorf("%s audit holds debate content %q", name, secret)
			}
		}
	}
}

// TestDebateCancelInInvited: A cancels before B ever accepts (a Phase 1
// request.cancel); B sees the request cancelled.
func TestDebateCancelInInvited(t *testing.T) {
	oldInterval := waitPollInterval
	waitPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = oldInterval })
	a, b := consultTeam(t)
	dir := t.TempDir()

	posA := writeJSONFile(t, dir, "pos.json", `{"claim":"x","argument":"y"}`)
	_, out, _ := cli(t, a, "debate", "@bob", "--topic", "Cancel me", "--position-file", posA, "--json")
	var sub requestBody
	if err := json.Unmarshal([]byte(out), &sub); err != nil || sub.ID == "" {
		t.Fatalf("debate start --json = %q: %v", out, err)
	}
	pollCLI(t, b, "the invitation", func(o string) bool { return strings.Contains(o, sub.ID) }, "inbox", "--json")

	code, out, errs := cli(t, a, "debate", sub.Session, "--cancel", "--json")
	if code != exitOK {
		t.Fatalf("A cancel: %d %s %s", code, out, errs)
	}
	pollCLI(t, b, "B to see the cancellation", func(o string) bool { return strings.Contains(o, `"state":"cancelled"`) }, "request", "show", sub.ID, "--json")
}

// TestDebateAbandon: once B has accepted, B has no ws.state to learn A's
// answer from, so B's own --cancel also closes B's mirror at once
// (Docs/protocol/debate.md §Cancel and abandon, "B abandon").
func TestDebateAbandon(t *testing.T) {
	oldInterval := waitPollInterval
	waitPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = oldInterval })
	a, b := consultTeam(t)
	dir := t.TempDir()

	posA := writeJSONFile(t, dir, "posA.json", `{"claim":"x","argument":"y"}`)
	_, out, _ := cli(t, a, "debate", "@bob", "--topic", "Abandon me", "--position-file", posA, "--json")
	var sub requestBody
	if err := json.Unmarshal([]byte(out), &sub); err != nil || sub.Session == "" {
		t.Fatalf("debate start --json = %q: %v", out, err)
	}
	pollCLI(t, b, "the invitation", func(o string) bool { return strings.Contains(o, sub.ID) }, "inbox", "--json")

	posB := writeJSONFile(t, dir, "posB.json", `{"claim":"z","argument":"w"}`)
	if code, out, errs := cli(t, b, "debate", sub.Session, "--position-file", posB, "--json"); code != exitOK {
		t.Fatalf("B one-step accept + position: %d %s %s", code, out, errs)
	}

	code, out, errs := cli(t, b, "debate", sub.Session, "--cancel", "--json")
	if code != exitOK {
		t.Fatalf("B abandon: %d %s %s", code, out, errs)
	}
	_, out, _ = cli(t, b, "debate", sub.Session, "--json")
	var shown struct {
		OK     bool `json:"ok"`
		Debate daemon.DebateView
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil || shown.Debate.Phase != "closed" ||
		shown.Debate.Outcome != "cancelled" || shown.Debate.Reason != "abandoned" {
		t.Fatalf("B's debate after abandon = %q: %v", out, err)
	}
	text := auditText(t, b)
	if !strings.Contains(text, "debate.abandon") {
		t.Errorf("B's audit has no debate.abandon: %s", text)
	}
}

func TestDebateUsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no peer or id":         {"debate"},
		"extra argument":        {"debate", "@bob", "x", "--topic", "t", "--position-file", "p"},
		"both topics":           {"debate", "@bob", "--topic", "t", "--topic-from-file", "f", "--position-file", "p"},
		"no position file":      {"debate", "@bob", "--topic", "t"},
		"reason without cancel": {"debate", "s-00000000000000000000000000000000", "--reason", "x"},
		"multiple entry files":  {"debate", "s-00000000000000000000000000000000", "--move-file", "a", "--propose-file", "b"},
	} {
		var out, errb bytes.Buffer
		code := run(args, &out, &errb)
		if code != exitUsage {
			t.Errorf("%s: code = %d, want usage (stdout %q stderr %q)", name, code, out.String(), errb.String())
		}
	}
	var help bytes.Buffer
	var errb bytes.Buffer
	if code := run([]string{"debate", "--help"}, &help, &errb); code != exitOK || !strings.Contains(help.String(), "--position-file") {
		t.Fatalf("debate --help: %d %q", code, help.String())
	}
}
