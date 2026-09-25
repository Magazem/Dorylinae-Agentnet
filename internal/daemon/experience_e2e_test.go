package daemon_test

// Ticket 3.7 (Docs/protocol/experience.md): e2e coverage of the private
// experience record through two real daemons over a relay, for one
// work-session close and one debate close, plus a structural check that
// nothing outside internal/worksession, internal/debate, internal/store and
// internal/experience itself ever reads the experience_records table (no IPC
// method, CLI command or mail kind exposes it).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestExperienceRecordNotReferencedOutsideItsOwnPackages: the same style of
// source scan TestAuditInventoryIsComplete uses. internal/experience is a
// leaf package; only its writers may import it, and nothing outside
// internal/store's migration and rewind tests may name its table.
func TestExperienceRecordNotReferencedOutsideItsOwnPackages(t *testing.T) {
	allowedImporters := map[string]bool{
		filepath.FromSlash("internal/experience"):  true,
		filepath.FromSlash("internal/worksession"): true,
		filepath.FromSlash("internal/debate"):      true,
	}
	allowedTableReferences := map[string]bool{
		filepath.FromSlash("internal/experience"):  true,
		filepath.FromSlash("internal/worksession"): true,
		filepath.FromSlash("internal/debate"):      true,
		filepath.FromSlash("internal/store"):       true, // migration 21 and the rewind tests
	}
	importRE := regexp.MustCompile(`"github\.com/Magazem/Dorylinae-Agentnet/internal/experience"`)
	tableRE := regexp.MustCompile(`experience_records`)

	root := filepath.Join("..", "..") // repo root from internal/daemon
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil // test files legitimately inspect the table to verify it; production code may not
		}
		b, rerr := os.ReadFile(path) //nolint:gosec // scanning this repo's own sources
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		pkgDir := filepath.FromSlash(dir)
		if importRE.Match(b) && !allowedImporters[pkgDir] {
			t.Errorf("%s imports internal/experience but is not a writer package", rel)
		}
		if tableRE.Match(b) && !allowedTableReferences[pkgDir] {
			t.Errorf("%s references experience_records but is not an allowed package", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestExperienceRecordE2E_WorkSession: an accepted work session closes on
// both real daemons and each stores its own experience record.
func TestExperienceRecordE2E_WorkSession(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "experience record test")

	b.call("ws_result", map[string]any{
		"id":     sid,
		"result": map[string]any{"status": "pass", "summary": "all good", "verification": "tests_passed"},
	}, new(any))
	harnessWait(t, "A to see awaiting_result", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'awaiting_result'`) == 1
	})
	a.call("ws_accept_result", map[string]any{"id": sid}, new(any))

	harnessWait(t, "A's experience record", func() bool {
		return a.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid+`' AND role = 'requester'`) == 1
	})
	harnessWait(t, "B's session to close", func() bool {
		return b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed'`) == 1
	})
	harnessWait(t, "B's experience record", func() bool {
		return b.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid+`' AND role = 'worker'`) == 1
	})
	if a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'experience.write'`) == 0 {
		t.Error("A: no experience.write audit row")
	}
}

// TestExperienceRecordE2E_Debate: a single-round debate agrees on both real
// daemons and each stores its own experience record.
func TestExperienceRecordE2E_Debate(t *testing.T) {
	a, b, teamID, aDS, bDS := debatePair(t)
	var res struct {
		ID, Session string
	}
	a.call("request_submit", map[string]any{
		"to": b.key, "type": "debate", "team": teamID, "title": "Retries", "brief": "How should the outbox retry?",
		"debate": map[string]any{"position": e2ePosition("Capped backoff"), "rounds": 1},
	}, &res)
	sid := res.Session
	harnessWait(t, "B to store the debate", phaseIs(b, sid, "invited"))

	dsSubmit(t, bDS, sid, "position", `{"argument":"Simple.","claim":"Fixed retry"}`)
	harnessWait(t, "A to reveal", phaseIs(a, sid, "rounds"))
	dsSubmit(t, aDS, sid, "move", `{"challenges":[]}`)
	harnessWait(t, "B to apply A's move", nextIs(b, sid, 3))
	dsSubmit(t, bDS, sid, "move", `{"challenges":[]}`)
	harnessWait(t, "A to converge", phaseIs(a, sid, "converge"))
	dsSubmit(t, aDS, sid, "proposal", `{"agreement":{"decision":"Capped backoff with jitter"}}`)
	harnessWait(t, "B to apply the proposal", nextIs(b, sid, 5))
	dsSubmit(t, bDS, sid, "answer", `{"accept":true}`)
	harnessWait(t, "A to close", phaseIs(a, sid, "closed"))
	harnessWait(t, "B to close", phaseIs(b, sid, "closed"))

	harnessWait(t, "A's experience record", func() bool {
		return a.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid+`' AND role = 'initiator'`) == 1
	})
	harnessWait(t, "B's experience record", func() bool {
		return b.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid+`' AND role = 'respondent'`) == 1
	})
}
