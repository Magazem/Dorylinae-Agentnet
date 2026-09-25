package main

// Ticket 3.3b acceptance, CLI side (Docs/protocol/decision.md,
// Docs/cli/decision.md): `agentnet decisions`, `agentnet decision <id>
// [--json|--md]`, `--out`/`--force`, and `agentnet decision verify` reading
// the written file back offline.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// closedDebate runs a full debate to a signed Decision through the CLI, as
// TestDebateRoundTrip does, and returns the session id.
func closedDebate(t *testing.T, a, b *testNode) string {
	t.Helper()
	oldInterval := waitPollInterval
	waitPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = oldInterval })
	dir := t.TempDir()

	posA := writeJSONFile(t, dir, "posA.json", `{"claim":"Use capped backoff","argument":"Keeps retries bounded after an outage."}`)
	_, out, errs := cli(t, a, "debate", "@bob", "--topic", "How should the outbox retry?",
		"--position-file", posA, "--rounds", "1", "--json")
	var sub requestBody
	if err := json.Unmarshal([]byte(out), &sub); err != nil || !sub.OK || sub.Session == "" {
		t.Fatalf("debate start --json = %q (%s): %v", out, errs, err)
	}
	pollCLI(t, b, "the debate invitation", func(o string) bool { return strings.Contains(o, sub.ID) }, "inbox", "--json")

	posB := writeJSONFile(t, dir, "posB.json", `{"claim":"Use a fixed retry interval","argument":"Simpler to reason about."}`)
	if code, out, errs := cli(t, b, "debate", sub.Session, "--position-file", posB, "--json"); code != exitOK {
		t.Fatalf("B one-step accept + position: %d %s %s", code, out, errs)
	}
	if waitCode, w := debateWaitOn(t, a, sub.Session, "10"); waitCode != exitOK || w.Wait != "turn" {
		t.Fatalf("A wait after B's position = %d %+v", waitCode, w)
	}
	moveEmpty := writeJSONFile(t, dir, "move.json", `{"challenges":[]}`)
	if code, out, errs := cli(t, a, "debate", sub.Session, "--move-file", moveEmpty, "--json"); code != exitOK {
		t.Fatalf("A move: %d %s %s", code, out, errs)
	}
	if waitCode, w := debateWaitOn(t, b, sub.Session, "10"); waitCode != exitOK || w.Wait != "turn" {
		t.Fatalf("B wait after A's move = %d %+v", waitCode, w)
	}
	if code, out, errs := cli(t, b, "debate", sub.Session, "--move-file", moveEmpty, "--json"); code != exitOK {
		t.Fatalf("B move: %d %s %s", code, out, errs)
	}
	if waitCode, w := debateWaitOn(t, a, sub.Session, "10"); waitCode != exitOK || w.Wait != "turn" {
		t.Fatalf("A wait at converge = %d %+v", waitCode, w)
	}
	propose := writeJSONFile(t, dir, "propose.json", `{"agreement":{"decision":"Use capped backoff with jitter"}}`)
	if code, out, errs := cli(t, a, "debate", sub.Session, "--propose-file", propose, "--json"); code != exitOK {
		t.Fatalf("A propose: %d %s %s", code, out, errs)
	}
	if waitCode, w := debateWaitOn(t, b, sub.Session, "10"); waitCode != exitOK || w.Wait != "turn" {
		t.Fatalf("B wait for the proposal = %d %+v", waitCode, w)
	}
	answer := writeJSONFile(t, dir, "answer.json", `{"accept":true}`)
	if code, out, errs := cli(t, b, "debate", sub.Session, "--answer-file", answer, "--json"); code != exitOK {
		t.Fatalf("B answer: %d %s %s", code, out, errs)
	}
	if waitCode, w := debateWaitOn(t, b, sub.Session, "10"); waitCode != exitOK || w.Wait != "closed" {
		t.Fatalf("B wait at the end = %d %+v", waitCode, w)
	}
	pollCLI(t, a, "A's debate to close (B signed)", func(o string) bool {
		return strings.Contains(o, `"phase":"closed"`) && strings.Contains(o, `"outcome":"agreed"`)
	}, "debate", sub.Session, "--json")
	return sub.Session
}

func TestDecisionCLIRoundTrip(t *testing.T) {
	a, b := consultTeam(t)
	sid := closedDebate(t, a, b)

	// `agentnet decisions --json` on both sides.
	for _, n := range []*testNode{a, b} {
		code, out, errs := cli(t, n, "decisions", "--json")
		if code != exitOK {
			t.Fatalf("decisions --json: %d %s %s", code, out, errs)
		}
		var list struct {
			OK bool `json:"ok"`
			daemon.DecisionListResult
		}
		if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Decisions) != 1 || list.Decisions[0].Session != sid {
			t.Fatalf("decisions --json = %q: %v", out, err)
		}
	}

	// `agentnet decisions` human output.
	if code, out, _ := cli(t, a, "decisions"); code != exitOK || !strings.Contains(out, "outbox retry") {
		t.Fatalf("decisions (human) = %d %q", code, out)
	}

	// `agentnet decision <id> --json` prints the signed file.
	code, out, errs := cli(t, a, "decision", sid, "--json")
	if code != exitOK {
		t.Fatalf("decision --json: %d %s %s", code, out, errs)
	}
	var file struct {
		Decision   json.RawMessage `json:"decision"`
		Hash       string          `json:"hash"`
		Signatures struct {
			Initiator  string `json:"initiator"`
			Respondent string `json:"respondent"`
		} `json:"signatures"`
	}
	if err := json.Unmarshal([]byte(out), &file); err != nil || file.Hash == "" || file.Signatures.Initiator == "" || file.Signatures.Respondent == "" {
		t.Fatalf("decision --json = %q: %v", out, err)
	}

	// `agentnet decision <id>` human output.
	if code, out, _ := cli(t, a, "decision", sid); code != exitOK || !strings.Contains(out, "agreed") {
		t.Fatalf("decision (human) = %d %q", code, out)
	}

	// `agentnet decision <id> --md` renders Markdown to stdout.
	code, mdOut, errs := cli(t, a, "decision", sid, "--md")
	if code != exitOK {
		t.Fatalf("decision --md: %d %s %s", code, mdOut, errs)
	}
	if !strings.Contains(mdOut, "# Decision d-") || !strings.Contains(mdOut, file.Hash) || !strings.Contains(mdOut, "agentnet decision verify") {
		t.Fatalf("decision --md output missing expected content: %q", mdOut)
	}
	if strings.Contains(mdOut, "\r") {
		t.Error("decision --md output must be LF only")
	}

	// `--out`: writes the file, refuses to overwrite without --force.
	dir := t.TempDir()
	out1 := filepath.Join(dir, "decision.md")
	if code, o, errs := cli(t, a, "decision", sid, "--md", "--out", out1); code != exitOK {
		t.Fatalf("decision --md --out: %d %s %s", code, o, errs)
	}
	got, err := os.ReadFile(out1) //nolint:gosec // test fixture path
	if err != nil || len(got) == 0 {
		t.Fatalf("--out did not write %s: %v", out1, err)
	}
	if code, _, _ := cli(t, a, "decision", sid, "--md", "--out", out1); code == exitOK {
		t.Fatal("decision --md --out over an existing file without --force must fail")
	}
	if code, o, errs := cli(t, a, "decision", sid, "--md", "--out", out1, "--force"); code != exitOK {
		t.Fatalf("decision --md --out --force: %d %s %s", code, o, errs)
	}

	// `agentnet decision verify FILE` reads the written --json file back,
	// offline: exit 0, complete, two signatures.
	// Review 48 M3: `--json --out` writes the sidecar itself (the same bytes
	// as stdout), refusing to overwrite without --force.
	jsonFile := filepath.Join(dir, "decision.json")
	if code, o, errs := cli(t, a, "decision", sid, "--json", "--out", jsonFile); code != exitOK {
		t.Fatalf("decision --json --out: %d %s %s", code, o, errs)
	}
	if got, err := os.ReadFile(jsonFile); err != nil || string(got) != out { //nolint:gosec // test fixture path
		t.Fatalf("--json --out wrote %q (%v), want the --json stdout bytes %q", got, err, out)
	}
	if code, _, _ := cli(t, a, "decision", sid, "--json", "--out", jsonFile); code == exitOK {
		t.Fatal("decision --json --out over an existing file without --force must fail")
	}
	// Review 48 L5: verify --md --json would mix two formats on stdout.
	if code, _, _ := cli(t, a, "decision", "verify", jsonFile, "--md", "--json"); code != exitUsage {
		t.Fatalf("decision verify --md --json: exit %d, want %d", code, exitUsage)
	}
	vcode, vout, verrs := cli(t, a, "decision", "verify", jsonFile, "--json")
	if vcode != exitOK {
		t.Fatalf("decision verify: %d %s %s", vcode, vout, verrs)
	}
	var vres struct {
		Valid    bool     `json:"valid"`
		Complete bool     `json:"complete"`
		SignedBy []string `json:"signed_by"`
		Hash     string   `json:"hash"`
	}
	if err := json.Unmarshal([]byte(vout), &vres); err != nil || !vres.Valid || !vres.Complete || len(vres.SignedBy) != 2 || vres.Hash != file.Hash {
		t.Fatalf("decision verify --json = %q: %v", vout, err)
	}

	// verify --md renders without contacting the daemon: it still succeeds
	// against the offline environment (blank paths.Default) because it never
	// calls callDaemon.
	if vcode, vout, verrs := cli(t, a, "decision", "verify", jsonFile, "--md"); vcode != exitOK || !strings.Contains(vout, "# Decision d-") {
		t.Fatalf("decision verify --md: %d %s %s", vcode, vout, verrs)
	}

	// A single-signed file (the respondent's signature dropped) verifies
	// exit 6, unconfirmed.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	var sigs map[string]json.RawMessage
	if err := json.Unmarshal(raw["signatures"], &sigs); err != nil {
		t.Fatal(err)
	}
	delete(sigs, "respondent")
	sigsRaw, _ := json.Marshal(sigs)
	raw["signatures"] = sigsRaw
	rawBytes, _ := json.Marshal(raw)
	singleFile := filepath.Join(dir, "single.json")
	if err := os.WriteFile(singleFile, rawBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if vcode, vout, _ := cli(t, a, "decision", "verify", singleFile); vcode != exitUnconfirmed {
		t.Fatalf("decision verify of a single-signed file: exit %d, want %d (%q)", vcode, exitUnconfirmed, vout)
	}

	// An unknown Decision id.
	if code, _, _ := cli(t, a, "decision", "d-00000000000000000000000000000000"); code == exitOK {
		t.Fatal("decision of an unknown id must fail")
	}
}
