package main

// Ticket 3.3b: `agentnet decisions` / `agentnet decision`
// (Docs/protocol/decision.md, Docs/cli/decision.md). `decision verify` runs
// offline, without a daemon: it parses and checks the file itself with
// internal/decision.Verify and the debate message schema
// (internal/debate.DecisionSchema).

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/decision"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// exitUnconfirmed is `decision verify`'s exit code for a valid file signed
// by the initiator only (Docs/protocol/decision.md §Signed file).
const exitUnconfirmed = 6

const decisionUsage = `Shows, exports or verifies a Decision, the signed artifact of a closed
debate (Docs/protocol/decision.md).

Usage:
  agentnet decision <id> [--json]
  agentnet decision <id> --md [--out FILE [--force]]
  agentnet decision verify FILE [--md] [--json]

<id> is a Decision's own id (d-...), or the debate's session (s-...) or
request (r-...) id.

Flags:
  --json    print the signed file {"decision","hash","signatures"} on stdout
  --md      render Markdown suitable for a repository's decisions folder
  --out F   with --md: write to F instead of stdout
  --force   with --out: overwrite an existing file

'agentnet decision verify' reads a signed file (as written by --json or
--out) without contacting a daemon: it recomputes the hash and checks both
signatures, the id and the derivation invariants.

Exit codes (decision <id>): 0 done, 1 error (unknown_decision, ...), 2
usage, 3 daemon not running.
Exit codes (decision verify): 0 valid with both signatures, 6 valid but
signed by the initiator only (unconfirmed: the respondent has not signed),
1 invalid, 2 usage.
`

func runDecision(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "verify" {
		return runDecisionVerify(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("agentnet decision", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the signed file on stdout")
	md := fs.Bool("md", false, "render Markdown suitable for a repository")
	out := fs.String("out", "", "with --md: write to FILE instead of stdout")
	force := fs.Bool("force", false, "with --out: overwrite an existing file")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, decisionUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one Decision id (see 'agentnet decision --help')")
	}
	if *md && *asJSON {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--md and --json exclude each other")
	}
	if *out != "" && !*md {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--out is only valid with --md")
	}
	if *force && *out == "" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--force is only valid with --out")
	}

	var res daemon.DecisionShowResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "decision_show", map[string]any{"id": pos[0]}, &res); code != exitOK {
		return code
	}
	if *md {
		return runDecisionMarkdown(stdout, stderr, res, *out, *force)
	}
	if *asJSON {
		return printDecisionSignedFile(stdout, res)
	}
	printDecisionHuman(stdout, res)
	return exitOK
}

// decisionSignedFile is the file of decision.md §Signed file: what
// `--json`/`--out` writes and `decision verify` reads. Decision is kept as
// raw bytes (the canonical form as stored), so re-marshalling never changes
// them.
type decisionSignedFile struct {
	Decision   json.RawMessage `json:"decision"`
	Hash       string          `json:"hash"`
	Signatures struct {
		Initiator  string `json:"initiator,omitempty"`
		Respondent string `json:"respondent,omitempty"`
	} `json:"signatures"`
}

func signedFileOf(res daemon.DecisionShowResult) decisionSignedFile {
	var f decisionSignedFile
	f.Decision = res.Decision
	f.Hash = res.Hash
	f.Signatures.Initiator = res.Signatures.Initiator
	f.Signatures.Respondent = res.Signatures.Respondent
	return f
}

func printDecisionSignedFile(stdout io.Writer, res daemon.DecisionShowResult) int {
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(signedFileOf(res)); err != nil {
		_, _ = fmt.Fprintf(stdout, "error: %v\n", err)
		return exitError
	}
	return exitOK
}

func printDecisionHuman(w io.Writer, res daemon.DecisionShowResult) {
	var d struct {
		ID      string `json:"id"`
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	}
	_ = json.Unmarshal(res.Decision, &d)
	signedBy := "initiator only (unconfirmed)"
	if res.Signatures.Initiator != "" && res.Signatures.Respondent != "" {
		signedBy = "initiator and respondent"
	}
	_, _ = fmt.Fprintf(w, "%s  outcome %s (%s)  state %s  signed by %s\n", d.ID, d.Outcome, d.Reason, res.State, signedBy)
	_, _ = fmt.Fprintf(w, "  initiator: %s (fingerprint %s)\n", res.PeerNames[debate.RoleInitiator], res.PeerFPs[debate.RoleInitiator])
	_, _ = fmt.Fprintf(w, "  respondent: %s (fingerprint %s)\n", res.PeerNames[debate.RoleRespondent], res.PeerFPs[debate.RoleRespondent])
	_, _ = fmt.Fprintf(w, "  hash: %s\n", res.Hash)
}

func runDecisionMarkdown(stdout, stderr io.Writer, res daemon.DecisionShowResult, out string, force bool) int {
	var d map[string]any
	dec := json.NewDecoder(bytes.NewReader(res.Decision))
	dec.UseNumber()
	if err := dec.Decode(&d); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: malformed decision from the daemon: %v\n", err)
		return exitError
	}
	refused := res.State == "peer_refused"
	md, err := decision.Render(d, res.Hash, res.Signatures.Initiator, res.Signatures.Respondent, refused, res.PeerNames, res.PeerFPs)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return exitError
	}
	return writeDecisionOutput(stdout, stderr, md, out, force)
}

func writeDecisionOutput(stdout, stderr io.Writer, md []byte, out string, force bool) int {
	if out == "" {
		_, _ = stdout.Write(md)
		return exitOK
	}
	if !force {
		if _, err := os.Stat(out); err == nil {
			_, _ = fmt.Fprintf(stderr, "error: %s already exists (use --force to overwrite)\n", out)
			return exitError
		}
	}
	if err := writeFileAtomic(out, md); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: cannot write %s: %v\n", out, err)
		return exitError
	}
	_, _ = fmt.Fprintf(stdout, "Wrote %s\n", out)
	return exitOK
}

const decisionsUsage = `Lists your Decisions, the signed artifacts of closed debates
(Docs/protocol/decision.md).

Usage:
  agentnet decisions [--state S] [--peer PEER] [--json]

Flags:
  --state S    awaiting_peer, signed or peer_refused
  --peer PEER  a peer name or public key, with an optional "@"
  --json       print machine-readable JSON on stdout

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
`

func runDecisions(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet decisions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	state := fs.String("state", "", "awaiting_peer, signed or peer_refused")
	peer := fs.String("peer", "", "a peer name or public key")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, decisionsUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) > 0 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", pos[0]))
	}
	var res daemon.DecisionListResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "decision_list", map[string]any{"state": *state, "peer": *peer}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK bool `json:"ok"`
			daemon.DecisionListResult
		}{OK: true, DecisionListResult: res})
		return exitOK
	}
	if len(res.Decisions) == 0 {
		_, _ = fmt.Fprintln(stdout, "No decisions.")
		return exitOK
	}
	for _, d := range res.Decisions {
		_, _ = fmt.Fprintf(stdout, "%s  %s  outcome %s  state %s  %s\n", d.ID, d.Peer, d.Outcome, d.State, d.Title)
	}
	return exitOK
}

const decisionVerifyUsage = `Verifies a Decision's signed file without a daemon (Docs/protocol/decision.md
§Signed file): recomputes the hash, checks each present signature, the id
and the derivation invariants.

Usage:
  agentnet decision verify FILE [--md] [--json]

Flags:
  --md    also render Markdown to stdout (offline: names are the keys'
          fingerprints, not petnames)
  --json  print {"valid","complete","signed_by","hash","id","participants",...}

Exit codes: 0 valid with both signatures, 6 valid but signed by the
initiator only (unconfirmed), 1 invalid (see the printed step), 2 usage.
`

type decisionVerifyParticipant struct {
	Key         string `json:"key,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type decisionVerifyResult struct {
	Valid        bool                                 `json:"valid"`
	Complete     bool                                 `json:"complete"`
	Step         int                                  `json:"step,omitempty"`
	Reason       string                               `json:"reason,omitempty"`
	SignedBy     []string                             `json:"signed_by,omitempty"`
	Hash         string                               `json:"hash,omitempty"`
	ID           string                               `json:"id,omitempty"`
	Participants map[string]decisionVerifyParticipant `json:"participants,omitempty"`
}

func runDecisionVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet decision verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	md := fs.Bool("md", false, "also render Markdown to stdout")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, decisionVerifyUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one FILE (see 'agentnet decision verify --help')")
	}
	data, err := os.ReadFile(pos[0]) //nolint:gosec // the path is a user-supplied CLI argument, as intended
	if err != nil {
		return failJSON(*asJSON, stdout, stderr, exitError, "io_error", err.Error())
	}
	res := decision.Verify(data, debate.DecisionSchema())

	exit := exitOK
	switch {
	case !res.Valid:
		exit = exitError
	case !res.Complete:
		exit = exitUnconfirmed
	}

	if *asJSON {
		out := decisionVerifyResult{
			Valid: res.Valid, Complete: res.Complete, Step: res.Step, Reason: res.Reason,
			SignedBy: res.SignedBy, Hash: res.Hash, ID: res.ID,
		}
		if res.Valid {
			out.Participants = map[string]decisionVerifyParticipant{
				debate.RoleInitiator:  {Key: res.Initiator, Fingerprint: fingerprintOf(res.Initiator)},
				debate.RoleRespondent: {Key: res.Respondent, Fingerprint: fingerprintOf(res.Respondent)},
			}
		}
		_ = json.NewEncoder(stdout).Encode(out)
	} else {
		printDecisionVerifyHuman(stdout, res)
	}
	if *md && res.Valid {
		names := map[string]string{
			debate.RoleInitiator:  fingerprintOf(res.Initiator),
			debate.RoleRespondent: fingerprintOf(res.Respondent),
		}
		var d map[string]any
		dec := json.NewDecoder(bytes.NewReader(res.Decision))
		dec.UseNumber()
		if derr := dec.Decode(&d); derr == nil {
			out, rerr := decision.Render(d, res.Hash, res.SigInitiator, res.SigRespondent, false, names, names)
			if rerr == nil {
				_, _ = stdout.Write(out)
			}
		}
	}
	return exit
}

func fingerprintOf(key string) string {
	fp, err := envelope.KeyFingerprint(key)
	if err != nil {
		return ""
	}
	return fp
}

func printDecisionVerifyHuman(w io.Writer, res decision.Result) {
	if !res.Valid {
		_, _ = fmt.Fprintf(w, "invalid at step %d: %s\n", res.Step, res.Reason)
		return
	}
	status := "valid, signed by " + strings.Join(res.SignedBy, " and ")
	if !res.Complete {
		status = "unconfirmed: signed by the initiator only; its entries are the initiator's claim"
	}
	_, _ = fmt.Fprintf(w, "%s  hash %s  %s\n", res.ID, res.Hash, status)
	_, _ = fmt.Fprintf(w, "  initiator fingerprint: %s\n", fingerprintOf(res.Initiator))
	_, _ = fmt.Fprintf(w, "  respondent fingerprint: %s\n", fingerprintOf(res.Respondent))
}
