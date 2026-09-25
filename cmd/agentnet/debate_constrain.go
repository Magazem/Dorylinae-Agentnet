package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// debateConstrainBody is the machine-readable output of
// `debate <id> --constrain TEXT --json`.
type debateConstrainBody struct {
	OK       bool          `json:"ok"`
	Approval approval.View `json:"approval"`
}

// runDebateConstrain is `agentnet debate <id> --constrain TEXT`
// (Docs/protocol/debate.md §Human constraints, ticket 3.4): it asks the
// daemon for a debate_constraint approval and prints its id. The constraint
// exists only once the human types the code into the approval window.
func runDebateConstrain(asJSON bool, stdout, stderr io.Writer, id, text string) int {
	var res daemon.DebateConstrainResult
	if code := callDaemon(asJSON, stdout, stderr, approveTimeout, "debate_constrain", daemon.DebateConstrainParams{ID: id, Text: text}, &res); code != exitOK {
		return code
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(debateConstrainBody{OK: true, Approval: res.Approval})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Approval %s: the constraint is added once you type the code into the AgentNet approval window.\n", res.Approval.ID)
	return exitOK
}
