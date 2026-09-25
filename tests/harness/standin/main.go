// Command standin is the scripted stand-in agent for ticket 2.H (OD-P2-12).
//
// It issues exactly the `agentnet` CLI calls a real headless coding agent
// would make after reading only Docs/agents/snippet.md and `agentnet --help`,
// for the Phase 2 request -> accept -> grant -> fetch -> consult -> result ->
// accept-result round trip. It exists so tests/harness/phase2-agents.ps1/.sh
// can be exercised for free in the weekly CI job, without a paid model API
// key or a logged-in real harness (Claude Code, agy). Real-harness rounds
// still run manually; see tests/phase2-manual.md.
//
// Assertions about the outcome are made by the harness script itself, from
// `agentnet ... --json` output and the daemons' audit logs, never from this
// program's own stdout.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "standin: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	agentnet := flag.String("agentnet", "", "path to the agentnet executable")
	home := flag.String("home", "", "DORYLINAE_HOME for this agent")
	mode := flag.String("mode", "phase2", "phase2 (ticket 2.H) or debate (ticket 3.H)")
	role := flag.String("role", "", "a (requester/initiator) or b (worker/respondent)")
	peer := flag.String("peer", "", "role a: the AgentNet peer name of b")
	fixture := flag.String("fixture", "", "phase2 role a: absolute path of the fixture directory to grant fs.read on")
	context := flag.String("context", "", "phase2 role a: absolute path of the consult context file")
	branch := flag.String("branch", "phase2-harness", "phase2 role a: branch name named in the review request")
	reviewKey := flag.String("review-key", "", "phase2 role a: idempotency key for the review request")
	consultKey := flag.String("consult-key", "", "phase2 role a: idempotency key for the consult")
	timeoutSeconds := flag.Int("timeout", 300, "seconds to wait for each blocking step")
	pollMs := flag.Int("poll-ms", 500, "milliseconds between polls")
	topic := flag.String("topic", "", "debate role a: the debate's topic")
	claim := flag.String("claim", "", "debate mode: your opening position's claim")
	argument := flag.String("argument", "", "debate mode: your opening position's argument")
	rounds := flag.Int("rounds", 2, "debate role a: --rounds for the debate (1-5)")
	debateKey := flag.String("debate-key", "", "debate role a: idempotency key for the debate request")
	disagree := flag.Bool("disagree", false, "debate role b: answer accept:false to force an escalated outcome")
	flag.Parse()

	if *agentnet == "" || *home == "" {
		return fmt.Errorf("-agentnet and -home are required")
	}
	timeout := time.Duration(*timeoutSeconds) * time.Second
	poll := time.Duration(*pollMs) * time.Millisecond

	c := &client{agentnet: *agentnet, home: *home}

	switch *mode {
	case "debate":
		switch *role {
		case "a":
			if *peer == "" || *topic == "" || *claim == "" || *argument == "" || *debateKey == "" {
				return fmt.Errorf("debate role a needs -peer, -topic, -claim, -argument and -debate-key")
			}
			return debateRoleA(c, *peer, *topic, *claim, *argument, *rounds, *debateKey, timeout, poll)
		case "b":
			if *claim == "" || *argument == "" {
				return fmt.Errorf("debate role b needs -claim and -argument")
			}
			return debateRoleB(c, *claim, *argument, *disagree, timeout, poll)
		default:
			return fmt.Errorf("-role must be a or b, got %q", *role)
		}
	case "phase2":
		switch *role {
		case "a":
			if *peer == "" || *fixture == "" || *context == "" || *reviewKey == "" || *consultKey == "" {
				return fmt.Errorf("role a needs -peer, -fixture, -context, -review-key and -consult-key")
			}
			return roleA(c, *peer, *fixture, *context, *branch, *reviewKey, *consultKey, timeout, poll)
		case "b":
			return roleB(c, timeout, poll)
		default:
			return fmt.Errorf("-role must be a or b, got %q", *role)
		}
	default:
		return fmt.Errorf("-mode must be phase2 or debate, got %q", *mode)
	}
}

// client runs `agentnet` against one daemon's home directory.
type client struct {
	agentnet string
	home     string
}

// call runs `agentnet <args...> --json`, optionally with stdin, and returns
// the parsed JSON object. A non-"ok" result or a non-zero exit is an error.
func (c *client) call(stdin string, args ...string) (map[string]interface{}, error) {
	full := append(append([]string{}, args...), "--json")
	cmd := exec.Command(c.agentnet, full...) //nolint:gosec // c.agentnet is the harness-provided path to the agentnet binary under test
	cmd.Env = append(os.Environ(), "DORYLINAE_HOME="+c.home)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	var m map[string]interface{}
	if stdout.Len() > 0 {
		if jerr := json.Unmarshal(stdout.Bytes(), &m); jerr != nil {
			return nil, fmt.Errorf("agentnet %s: not JSON: %w (stdout=%q stderr=%q)",
				strings.Join(args, " "), jerr, stdout.String(), stderr.String())
		}
	}
	if runErr != nil {
		if m != nil {
			if e, ok := m["error"]; ok {
				return m, fmt.Errorf("agentnet %s: %v", strings.Join(args, " "), e)
			}
		}
		return m, fmt.Errorf("agentnet %s: %w (stderr=%q)", strings.Join(args, " "), runErr, stderr.String())
	}
	if ok, present := m["ok"].(bool); present && !ok {
		return m, fmt.Errorf("agentnet %s: %v", strings.Join(args, " "), m["error"])
	}
	return m, nil
}

// pollUntil calls fn every interval until it reports done, an error survives
// past the deadline, or timeout elapses.
func pollUntil(timeout, interval time.Duration, what string, fn func() (map[string]interface{}, bool, error)) (map[string]interface{}, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		m, done, err := fn()
		if err != nil {
			lastErr = err
		} else if done {
			return m, nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, fmt.Errorf("timed out waiting for %s: %w", what, lastErr)
			}
			return nil, fmt.Errorf("timed out waiting for %s", what)
		}
		time.Sleep(interval)
	}
}

func asString(m map[string]interface{}, keys ...string) string {
	var cur interface{} = m
	for _, k := range keys {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}

// roleA is the requester: sends a review with a grant, and a consult, then
// waits for both results and accepts them.
func roleA(c *client, peer, fixture, contextFile, branch, reviewKey, consultKey string, timeout, poll time.Duration) error {
	brief := "What: review the fixture directory shared with you for this task\n" +
		"Why: Phase 2 headless harness round trip (ticket 2.H)\n" +
		"Done when: you have listed and read the shared file and returned a result"
	reqResp, err := c.call(brief, "request", peer, "review", "--title", "Review "+branch,
		"--brief-from-file", "-", "--idempotency-key", reviewKey)
	if err != nil {
		return fmt.Errorf("send review request: %w", err)
	}
	reqID := asString(reqResp, "id")
	if reqID == "" {
		return fmt.Errorf("review request returned no id: %v", reqResp)
	}

	consultResp, err := c.call("", "consult", peer, "--question", "Is the fixture file readable and non-empty?",
		"--context-file", contextFile, "--idempotency-key", consultKey)
	if err != nil {
		return fmt.Errorf("send consult: %w", err)
	}
	consultSession := asString(consultResp, "session")
	if consultSession == "" {
		return fmt.Errorf("consult returned no session id: %v", consultResp)
	}

	var reviewSession string
	if _, err := pollUntil(timeout, poll, "review request to open a session", func() (map[string]interface{}, bool, error) {
		r, err := c.call("", "request", "show", reqID)
		if err != nil {
			return nil, false, err
		}
		id := asString(r, "request", "session", "id")
		if id == "" {
			return nil, false, nil
		}
		reviewSession = id
		return r, true, nil
	}); err != nil {
		return err
	}

	grantResp, err := c.call("", "grant", peer, "--session", reviewSession, "--action", "fs.read", "--resource", fixture)
	if err != nil {
		return fmt.Errorf("grant fs.read: %w", err)
	}
	grantID := asString(grantResp, "grant", "id")
	if grantID == "" {
		return fmt.Errorf("grant returned no grant id: %v", grantResp)
	}

	if _, err := pollUntil(timeout, poll, "grant to become active", func() (map[string]interface{}, bool, error) {
		r, err := c.call("", "grants", "--session", reviewSession, "--issued")
		if err != nil {
			return nil, false, err
		}
		grants, _ := r["grants"].([]interface{})
		for _, g := range grants {
			gm, _ := g.(map[string]interface{})
			if gm == nil {
				continue
			}
			if id, _ := gm["id"].(string); id == grantID {
				if state, _ := gm["state"].(string); state == "active" {
					return r, true, nil
				}
			}
		}
		return nil, false, nil
	}); err != nil {
		return err
	}

	if err := waitAndAccept(c, reviewSession, timeout, poll); err != nil {
		return fmt.Errorf("review session: %w", err)
	}
	if err := waitAndAccept(c, consultSession, timeout, poll); err != nil {
		return fmt.Errorf("consult session: %w", err)
	}

	summary, _ := json.Marshal(map[string]string{
		"role":            "a",
		"review_request":  reqID,
		"review_session":  reviewSession,
		"consult_session": consultSession,
	})
	fmt.Println(string(summary))
	return nil
}

func waitAndAccept(c *client, session string, timeout, poll time.Duration) error {
	released := false
	if _, err := pollUntil(timeout, poll, "session "+session+" to show a result", func() (map[string]interface{}, bool, error) {
		r, err := c.call("", "session", session)
		if err != nil {
			return nil, false, err
		}
		switch asString(r, "session", "state") {
		case "awaiting_result", "closed":
			return r, true, nil
		case "quarantined":
			// A sensitive grant quarantines the result until the human
			// releases it; the harness script answers the approval code.
			if !released {
				if _, err := c.call("", "session", session, "--release"); err != nil {
					return nil, false, err
				}
				released = true
			}
		}
		return nil, false, nil
	}); err != nil {
		return err
	}
	waitResp, err := c.call("", "wait", session, "--timeout", fmt.Sprintf("%d", int(timeout.Seconds())))
	if err != nil {
		return fmt.Errorf("wait: %w", err)
	}
	switch asString(waitResp, "wait") {
	case "result", "closed":
	default:
		return fmt.Errorf("wait on %s returned %q, want result or closed", session, asString(waitResp, "wait"))
	}
	if _, err := c.call("", "accept-result", session); err != nil {
		return fmt.Errorf("accept-result: %w", err)
	}
	return nil
}

// roleB is the worker: accepts the review, fetches the granted file through
// AgentNet, submits a result, and answers the consult.
func roleB(c *client, timeout, poll time.Duration) error {
	handled := map[string]bool{}
	reviewDone, consultDone := false, false
	var reviewSession, consultID string

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && (!reviewDone || !consultDone) {
		inbox, err := c.call("", "inbox")
		if err != nil {
			return fmt.Errorf("inbox: %w", err)
		}
		items, _ := inbox["requests"].([]interface{})
		for _, it := range items {
			m, _ := it.(map[string]interface{})
			if m == nil {
				continue
			}
			id, _ := m["id"].(string)
			typ, _ := m["type"].(string)
			if id == "" || handled[id] {
				continue
			}
			switch typ {
			case "review", "task":
				session, err := handleReview(c, id, timeout, poll)
				if err != nil {
					return fmt.Errorf("handle review %s: %w", id, err)
				}
				reviewSession = session
				reviewDone = true
				handled[id] = true
			case "question":
				if err := handleConsult(c, id); err != nil {
					return fmt.Errorf("handle consult %s: %w", id, err)
				}
				consultID = id
				consultDone = true
				handled[id] = true
			}
		}
		if !reviewDone || !consultDone {
			time.Sleep(poll)
		}
	}
	if !reviewDone || !consultDone {
		return fmt.Errorf("timed out: review done=%v consult done=%v", reviewDone, consultDone)
	}

	summary, _ := json.Marshal(map[string]string{
		"role":            "b",
		"review_session":  reviewSession,
		"consult_request": consultID,
	})
	fmt.Println(string(summary))
	return nil
}

func handleReview(c *client, requestID string, timeout, poll time.Duration) (string, error) {
	if _, err := c.call("", "accept", requestID); err != nil {
		return "", fmt.Errorf("accept: %w", err)
	}

	var session string
	if _, err := pollUntil(timeout, poll, "accepted request to show a session", func() (map[string]interface{}, bool, error) {
		r, err := c.call("", "request", "show", requestID)
		if err != nil {
			return nil, false, err
		}
		id := asString(r, "request", "session", "id")
		if id == "" {
			return nil, false, nil
		}
		session = id
		return r, true, nil
	}); err != nil {
		return "", err
	}

	var grantID string
	if _, err := pollUntil(timeout, poll, "a held grant to become active", func() (map[string]interface{}, bool, error) {
		r, err := c.call("", "grants", "--session", session, "--held")
		if err != nil {
			return nil, false, err
		}
		grants, _ := r["grants"].([]interface{})
		for _, g := range grants {
			gm, _ := g.(map[string]interface{})
			if gm == nil {
				continue
			}
			if state, _ := gm["state"].(string); state == "active" {
				grantID, _ = gm["id"].(string)
				return r, true, nil
			}
		}
		return nil, false, nil
	}); err != nil {
		return "", err
	}

	listResp, err := c.call("", "fetch", grantID, "--list")
	if err != nil {
		return "", fmt.Errorf("fetch --list: %w", err)
	}
	entries, _ := listResp["entries"].([]interface{})
	var fileName string
	for _, e := range entries {
		em, _ := e.(map[string]interface{})
		if em == nil {
			continue
		}
		if typ, _ := em["type"].(string); typ == "file" {
			fileName, _ = em["name"].(string)
			break
		}
	}
	if fileName == "" {
		return "", fmt.Errorf("granted directory listing has no file")
	}
	if _, err := c.call("", "fetch", grantID, fileName); err != nil {
		return "", fmt.Errorf("fetch %s: %w", fileName, err)
	}

	if _, err := c.call("", "result", session, "--status", "pass",
		"--summary", "Reviewed via AgentNet fetch",
		"--notes", "Listed and read "+fileName+" through the granted access."); err != nil {
		return "", fmt.Errorf("result: %w", err)
	}
	return session, nil
}

func handleConsult(c *client, requestID string) error {
	answerFile, err := os.CreateTemp("", "standin-answer-*.txt")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(answerFile.Name()) }()
	if _, err := answerFile.WriteString("Yes, the fixture file is readable and non-empty.\n"); err != nil {
		_ = answerFile.Close()
		return err
	}
	if err := answerFile.Close(); err != nil {
		return err
	}
	if _, err := c.call("", "result", requestID, "--file", answerFile.Name()); err != nil {
		return fmt.Errorf("result (consult answer): %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Ticket 3.H: debate mode. Issues exactly the `agentnet debate` CLI calls a
// real headless agent would make, driven only by `agentnet debate <id>
// --json`'s "turn"/"expect" fields (Docs/cli/debate.md), never by hardcoded
// slot numbers. The harness script (never this program) runs the
// `--constrain` step and confirms its approval code, so this file has no
// approval handling of its own.
// ---------------------------------------------------------------------------

// debateRoleA is the initiator: starts the debate with a committed opening
// position, then drives the turn loop until the debate closes.
func debateRoleA(c *client, peer, topic, claim, argument string, rounds int, idemKey string, timeout, poll time.Duration) error {
	position, err := json.Marshal(map[string]string{"claim": claim, "argument": argument})
	if err != nil {
		return err
	}
	resp, err := c.call(string(position), "debate", peer, "--topic", topic, "--position-file", "-",
		"--rounds", strconv.Itoa(rounds), "--idempotency-key", idemKey)
	if err != nil {
		return fmt.Errorf("start debate: %w", err)
	}
	session := asString(resp, "session")
	if session == "" {
		return fmt.Errorf("debate start returned no session id: %v", resp)
	}
	if err := debateLoop(c, session, timeout, poll, false); err != nil {
		return fmt.Errorf("debate loop (initiator): %w", err)
	}
	summary, _ := json.Marshal(map[string]string{"role": "a", "session": session})
	fmt.Println(string(summary))
	return nil
}

// debateRoleB is the respondent: finds the invited debate, accepts it and
// submits its opening position in one step, then drives the turn loop.
func debateRoleB(c *client, claim, argument string, disagree bool, timeout, poll time.Duration) error {
	var session string
	if _, err := pollUntil(timeout, poll, "an invited debate", func() (map[string]interface{}, bool, error) {
		r, err := c.call("", "debates", "--phase", "invited")
		if err != nil {
			return nil, false, err
		}
		debates, _ := r["debates"].([]interface{})
		for _, d := range debates {
			dm, _ := d.(map[string]interface{})
			if dm == nil {
				continue
			}
			if s, _ := dm["session"].(string); s != "" {
				session = s
				return r, true, nil
			}
		}
		return nil, false, nil
	}); err != nil {
		return fmt.Errorf("find invited debate: %w", err)
	}

	position, err := json.Marshal(map[string]string{"claim": claim, "argument": argument})
	if err != nil {
		return err
	}
	if _, err := c.call(string(position), "debate", session, "--position-file", "-"); err != nil {
		return fmt.Errorf("accept + submit position: %w", err)
	}
	if err := debateLoop(c, session, timeout, poll, disagree); err != nil {
		return fmt.Errorf("debate loop (respondent): %w", err)
	}
	summary, _ := json.Marshal(map[string]string{"role": "b", "session": session})
	fmt.Println(string(summary))
	return nil
}

// debateLoop polls `agentnet debate <session> --json` and, whenever it is
// this side's turn, submits the entry `expect` names, until the debate
// closes (or breaks). It never hardcodes slot numbers: the first move it
// submits is a real challenge (so the fixture's two designs are actually
// argued), every later move is a pass, which converges the debate in step
// with whatever --rounds the initiator chose.
func debateLoop(c *client, session string, timeout, poll time.Duration, disagree bool) error {
	deadline := time.Now().Add(timeout)
	myMoves := 0
	for {
		show, err := c.call("", "debate", session)
		if err != nil {
			return fmt.Errorf("debate show: %w", err)
		}
		phase := asString(show, "debate", "phase")
		if phase == "closed" || phase == "broken" {
			return nil
		}
		if asString(show, "debate", "turn") == "you" {
			expect := asString(show, "debate", "expect")
			var entry []byte
			var flagName string
			switch expect {
			case "move":
				flagName = "--move-file"
				if myMoves == 0 {
					entry, err = json.Marshal(map[string]interface{}{
						"challenges": []map[string]interface{}{{
							"targets":  []string{"argument"},
							"argument": "Consider the maintenance and readability cost of this approach over time.",
						}},
					})
				} else {
					entry, err = json.Marshal(map[string]interface{}{"challenges": []interface{}{}})
				}
				myMoves++
			case "proposal":
				flagName = "--propose-file"
				entry, err = json.Marshal(map[string]interface{}{
					"agreement": map[string]string{
						"decision": "Combine both designs: keep the simpler one and add the other's safety check.",
					},
				})
			case "answer":
				flagName = "--answer-file"
				if disagree {
					entry, err = json.Marshal(map[string]interface{}{
						"accept": false,
						"remaining_disagreement": []map[string]string{{
							"point":      "Which design to keep as the base",
							"initiator":  "prefers the first design for its simplicity",
							"respondent": "prefers the second design for its performance",
						}},
					})
				} else {
					entry, err = json.Marshal(map[string]interface{}{"accept": true})
				}
			default:
				return fmt.Errorf("debate %s: unexpected expect %q", session, expect)
			}
			if err != nil {
				return err
			}
			if _, err := c.call(string(entry), "debate", session, flagName, "-"); err != nil {
				return fmt.Errorf("submit %s: %w", expect, err)
			}
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for debate %s to close (phase=%s)", session, phase)
		}
		time.Sleep(poll)
	}
}
