package main

// Ticket 3.1b: `agentnet debate` / `agentnet debates` (Docs/protocol/debate.md,
// Docs/cli/debate.md). `--constrain` (ticket 3.4) routes to runDebateConstrain
// in debate_constrain.go.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// maxEntryFileRead bounds what --position-file/--move-file/--propose-file/
// --answer-file read: a canonical entry is at most 32768 bytes
// (debate.MaxDebateEntry), so a pretty-printed or padded file is generously
// bounded at several times that.
const maxEntryFileRead = 8 * 32768

// debateEntryStdin is where an entry file "-" reads from; tests replace it.
var debateEntryStdin io.Reader = os.Stdin

// readEntryFile reads one JSON entry file ("-" = stdin): the raw bytes,
// unvalidated (the daemon parses, canonicalises and validates it).
func readEntryFile(path string) (json.RawMessage, error) {
	name := path
	var r io.Reader
	if path == "-" {
		name, r = "stdin", debateEntryStdin
	} else {
		f, err := os.Open(path) //nolint:gosec // the path is a user-supplied CLI flag, as intended
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxEntryFileRead+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if len(b) > maxEntryFileRead {
		return nil, fmt.Errorf("read %s: over %d bytes", name, maxEntryFileRead)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, fmt.Errorf("read %s: empty", name)
	}
	return json.RawMessage(b), nil
}

// isDebateID reports whether s is a debate's session (s-...) or request
// (r-...) id, as opposed to a peer reference (name, @name or public key).
func isDebateID(s string) bool {
	return worksession.ValidID(s) || request.ValidID(s)
}

const debateUsage = `Argues a question with a teammate's agent in a fixed structure, ending in a
signed Decision (Docs/protocol/debate.md). It is a request of type debate.

Usage:
  agentnet debate <peer> (--topic TEXT | --topic-from-file F) --position-file P
                  [--context-file F]... [--rounds N] [--turn-timeout D]
                  [--title T] [--team T] [--urgency U --urgency-reason R]
                  [--idempotency-key K] [--json]
  agentnet debate <id> [--json]
  agentnet debate <id> --position-file F | --move-file F | --propose-file F | --answer-file F [--json]
  agentnet debate <id> --cancel [--reason R] [--json]
  agentnet debate <id> --constrain TEXT [--json]

<peer> is a peer name or public key, with an optional "@". <id> is a debate's
session id (s-...) or the request id it belongs to (r-...).

Flags:
  --topic TEXT              the debate's topic (up to 16 KiB). Exactly one of
                            --topic and --topic-from-file is required to start
  --topic-from-file F       read the topic from file F ("-" = stdin)
  --position-file P         starting a debate: your opening position (a JSON
                            object, "-" = stdin), committed and never sent
                            before the peer's own position is applied. On a
                            pending debate: accepts it and submits your
                            position in one step
  --context-file F          repeatable, up to 8; same rules as 'agentnet consult'
  --rounds N                1-5, default 2
  --turn-timeout D          a Go duration (e.g. 90m, 2h) or Nd, default 1h
  --title T                 default: the first line of the topic, cut to 120
                            characters
  --team TEAM               needed only when you share several teams with the peer
  --urgency U                low, normal (default), high or blocking
  --urgency-reason R         required with high and blocking
  --idempotency-key K        1-64 characters of [A-Za-z0-9._:-]
  --move-file F              a move: {"challenges", "revision"?} ("-" = stdin)
  --propose-file F           a proposal: {"agreement", ...} ("-" = stdin)
  --answer-file F            an answer: {"accept", ...} ("-" = stdin)
  --cancel                   close an open or invited debate (no Decision)
  --reason R                 optional, with --cancel, 1-500 characters
  --constrain TEXT           add a human constraint, approval-gated: the
                            AgentNet approval window shows the peer, the
                            session and TEXT in full, and only your code
                            stores and sends it (Docs/protocol/debate.md
                            §Human constraints)
  --json                     print machine-readable JSON on stdout

Each entry file is a JSON object of the given kind (Docs/protocol/debate.md
§Messages): position {"claim","argument",...}, move {"challenges","revision"?},
proposal {"agreement",...}, answer {"accept",...}.

Wait for your turn or the close with 'agentnet wait <id>'.

Exit codes: 0 done, 1 error (bad_request, bad_state, not_your_turn,
entry_too_large, quarantine_active, ...), 2 usage, 3 daemon not running.
`

func runDebate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet debate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	topic := fs.String("topic", "", "the debate's topic")
	topicFile := fs.String("topic-from-file", "", "read the topic from a file (- = stdin)")
	positionFile := fs.String("position-file", "", "your opening position, or the one-step accept + position (- = stdin)")
	moveFile := fs.String("move-file", "", "a move (- = stdin)")
	proposeFile := fs.String("propose-file", "", "a proposal (- = stdin)")
	answerFile := fs.String("answer-file", "", "an answer (- = stdin)")
	rounds := fs.Int("rounds", 0, "1-5, default 2")
	turnTimeout := fs.String("turn-timeout", "", "a Go duration (e.g. 90m, 2h) or Nd, default 1h")
	title := fs.String("title", "", "default: the first line of the topic")
	team := fs.String("team", "", "team ref, needed only when you share several teams with the peer")
	urgency := fs.String("urgency", "", "low, normal (default), high or blocking")
	urgencyReason := fs.String("urgency-reason", "", "required with high and blocking")
	idemKey := fs.String("idempotency-key", "", "1-64 characters of [A-Za-z0-9._:-]")
	cancel := fs.Bool("cancel", false, "close an open or invited debate")
	reason := fs.String("reason", "", "optional, with --cancel")
	constrain := fs.String("constrain", "", "add a human constraint, approval-gated")
	var contextFiles []string
	fs.Func("context-file", "a context file (repeatable, up to 8)", func(v string) error {
		contextFiles = append(contextFiles, v)
		return nil
	})
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, debateUsage) }

	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <peer> or <id> (see 'agentnet debate --help')")
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *reason != "" && !*cancel {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--reason is only valid with --cancel")
	}

	entryActions := 0
	for _, a := range []bool{set["position-file"], set["move-file"], set["propose-file"], set["answer-file"]} {
		if a {
			entryActions++
		}
	}

	if isDebateID(pos[0]) {
		id := pos[0]
		switch {
		case entryActions > 1:
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give at most one of --position-file, --move-file, --propose-file or --answer-file")
		case *cancel && (entryActions > 0 || set["constrain"]):
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--cancel cannot be combined with an entry file or --constrain")
		case set["constrain"] && entryActions > 0:
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--constrain cannot be combined with an entry file")
		case *cancel:
			return runDebateCancel(*asJSON, stdout, stderr, id, *reason)
		case set["constrain"]:
			return runDebateConstrain(*asJSON, stdout, stderr, id, *constrain)
		case entryActions == 1:
			kind, path := debateEntryKind(set, *positionFile, *moveFile, *proposeFile, *answerFile)
			return runDebateSubmit(*asJSON, stdout, stderr, id, kind, path)
		case set["topic"] || set["topic-from-file"] || set["rounds"] || set["turn-timeout"]:
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("%q looks like a debate id, not a peer; drop --topic/--rounds/--turn-timeout to show it", id))
		default:
			return runDebateShow(*asJSON, stdout, stderr, id)
		}
	}

	// Starting a new debate: --move-file/--propose-file/--answer-file/--constrain
	// only make sense against an existing debate id, never a peer.
	if *cancel || set["move-file"] || set["propose-file"] || set["answer-file"] || set["constrain"] {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give a debate id (s-... or r-...) with --cancel, --move-file, --propose-file, --answer-file or --constrain")
	}
	if !set["position-file"] {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--position-file is required to start a debate")
	}
	if set["topic"] == set["topic-from-file"] {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one of --topic or --topic-from-file")
	}
	return runDebateStart(*asJSON, stdout, stderr, pos[0], debateStartFlags{
		topic: *topic, topicFile: *topicFile, positionFile: *positionFile,
		rounds: *rounds, turnTimeout: *turnTimeout, title: *title, team: *team,
		urgency: *urgency, urgencyReason: *urgencyReason, idemKey: *idemKey,
		contextFiles: contextFiles,
	})
}

func debateEntryKind(set map[string]bool, positionFile, moveFile, proposeFile, answerFile string) (kind, path string) {
	switch {
	case set["position-file"]:
		return "position", positionFile
	case set["move-file"]:
		return "move", moveFile
	case set["propose-file"]:
		return "proposal", proposeFile
	default:
		return "answer", answerFile
	}
}

type debateStartFlags struct {
	topic, topicFile, positionFile string
	rounds                         int
	turnTimeout                    string
	title, team                    string
	urgency, urgencyReason         string
	idemKey                        string
	contextFiles                   []string
}

func runDebateStart(asJSON bool, stdout, stderr io.Writer, peer string, f debateStartFlags) int {
	topic := f.topic
	if f.topicFile != "" {
		t, err := readBriefFile(f.topicFile)
		if err != nil {
			return failJSON(asJSON, stdout, stderr, exitUsage, "usage", strings.Replace(err.Error(), "the brief", "the topic", 1))
		}
		topic = t
	}
	topic = strings.ReplaceAll(topic, "\r\n", "\n")
	if strings.TrimSpace(topic) == "" {
		return failJSON(asJSON, stdout, stderr, exitUsage, "usage", "the topic is empty")
	}
	position, err := readEntryFile(f.positionFile)
	if err != nil {
		return failJSON(asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	turnTimeoutS := 0
	if f.turnTimeout != "" {
		d, err := parseDebateDuration(f.turnTimeout)
		if err != nil {
			return failJSON(asJSON, stdout, stderr, exitUsage, "usage", "--turn-timeout: "+err.Error())
		}
		turnTimeoutS = int(d.Seconds())
	}
	title := f.title
	if title == "" {
		title = consultTitle(topic)
	}
	params := daemon.RequestSubmitParams{
		To: peer, Type: "debate", Team: f.team, Title: title, Brief: topic,
		Urgency: f.urgency, UrgencyReason: f.urgencyReason, IdempotencyKey: f.idemKey,
		Debate: &daemon.DebateParam{Position: position, Rounds: f.rounds, TurnTimeoutS: turnTimeoutS},
	}
	for _, path := range f.contextFiles {
		c, err := readContextFile(path)
		if err != nil {
			return failJSON(asJSON, stdout, stderr, exitError, "bad_request", err.Error())
		}
		params.Context = append(params.Context, c)
	}

	var res daemon.RequestSubmitResult
	if code := callDaemon(asJSON, stdout, stderr, statusTimeout, "request_submit", params, &res); code != exitOK {
		return code
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(requestBody{OK: true, RequestSubmitResult: res})
		return exitOK
	}
	verb := "Started"
	if res.Duplicate {
		verb = "Already started (duplicate)"
	}
	_, _ = fmt.Fprintf(stdout, "%s debate %s with %s (team %s)\n  session: %s (wait with 'agentnet wait %s')\n",
		verb, res.ID, res.Peer.Name, res.Team.Name, res.Session, res.Session)
	return exitOK
}

func runDebateShow(asJSON bool, stdout, stderr io.Writer, id string) int {
	var res daemon.DebateShowResult
	if code := callDaemon(asJSON, stdout, stderr, statusTimeout, "debate_show", map[string]any{"id": id}, &res); code != exitOK {
		return code
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK bool `json:"ok"`
			daemon.DebateShowResult
		}{OK: true, DebateShowResult: res})
		return exitOK
	}
	printDebateHuman(stdout, res.Debate)
	return exitOK
}

func runDebateSubmit(asJSON bool, stdout, stderr io.Writer, id, kind, path string) int {
	entry, err := readEntryFile(path)
	if err != nil {
		return failJSON(asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	var res daemon.DebateSubmitResult
	if code := callDaemon(asJSON, stdout, stderr, statusTimeout, "debate_submit", map[string]any{"id": id, "kind": kind, "entry": entry}, &res); code != exitOK {
		return code
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK bool `json:"ok"`
			daemon.DebateSubmitResult
		}{OK: true, DebateSubmitResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Submitted %s to %s (now %s, turn: %s)\n", kind, res.Debate.Session, res.Debate.Phase, res.Debate.Turn)
	return exitOK
}

// runDebateCancel cancels a debate (Docs/protocol/debate.md §Cancel and
// abandon): in "invited" (before B ever accepted) it is a Phase 1
// request.cancel, since no work session exists yet to send ws_cancel to;
// afterwards it is ws_cancel, which also runs B's local abandon
// (internal/worksession's AbandonTx) when B is the caller.
func runDebateCancel(asJSON bool, stdout, stderr io.Writer, id, reason string) int {
	var shown daemon.DebateShowResult
	if code := callDaemon(asJSON, stdout, stderr, statusTimeout, "debate_show", map[string]any{"id": id}, &shown); code != exitOK {
		return code
	}
	if shown.Debate.Phase == "invited" {
		var res daemon.RequestCancelResult
		if code := callDaemon(asJSON, stdout, stderr, statusTimeout, "request_cancel", map[string]any{"id": shown.Debate.Request.ID}, &res); code != exitOK {
			return code
		}
		if asJSON {
			_ = json.NewEncoder(stdout).Encode(struct {
				OK bool `json:"ok"`
				daemon.RequestCancelResult
			}{OK: true, RequestCancelResult: res})
			return exitOK
		}
		_, _ = fmt.Fprintf(stdout, "Cancelled %s\n", shown.Debate.Request.ID)
		return exitOK
	}
	params := map[string]any{"id": shown.Debate.Session}
	if reason != "" {
		params["reason"] = reason
	}
	var raw map[string]json.RawMessage
	if code := callDaemon(asJSON, stdout, stderr, statusTimeout, "ws_cancel", params, &raw); code != exitOK {
		return code
	}
	if asJSON {
		out := map[string]any{"ok": true}
		for k, v := range raw {
			out[k] = v
		}
		_ = json.NewEncoder(stdout).Encode(out)
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "Cancelled %s\n", shown.Debate.Session)
	return exitOK
}

func printDebateHuman(w io.Writer, d daemon.DebateView) {
	_, _ = fmt.Fprintf(w, "%s  role %s  phase %s  turn %s\n", d.Session, d.Role, d.Phase, d.Turn)
	_, _ = fmt.Fprintf(w, "  peer:    %s\n", d.Peer.Name)
	_, _ = fmt.Fprintf(w, "  request: %s (%s)\n", d.Request.Title, d.Request.ID)
	_, _ = fmt.Fprintf(w, "  rounds:  %d/%d\n", d.Rounds.Current, d.Rounds.Max)
	if d.Expect != "" {
		_, _ = fmt.Fprintf(w, "  expect:  %s\n", d.Expect)
	}
	if d.Waiting != "" {
		_, _ = fmt.Fprintf(w, "  waiting: %s\n", d.Waiting)
	}
	if d.Deadline != "" {
		_, _ = fmt.Fprintf(w, "  deadline: %s\n", d.Deadline)
	}
	if d.Outcome != "" {
		_, _ = fmt.Fprintf(w, "  outcome: %s (%s)\n", d.Outcome, d.Reason)
	}
	if d.Topic != "" {
		_, _ = fmt.Fprintf(w, "  topic:   %s\n", d.Topic)
	}
	for _, e := range d.Transcript {
		_, _ = fmt.Fprintf(w, "  [%d] %s %s at %s\n", e.Slot, e.Author, e.Kind, e.At)
	}
}

// parseDebateDuration parses --turn-timeout: a Go duration (90m, 2h) or a
// bare number of days with a "d" suffix (Docs/protocol/request.md §deadline
// uses the same rule for consistency).
func parseDebateDuration(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil {
			return 0, fmt.Errorf("must be a duration like 90m, 2h, 3d")
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("must be a duration like 90m, 2h, 3d")
	}
	return d, nil
}

const debatesUsage = `Lists your debates (Docs/protocol/debate.md).

Usage:
  agentnet debates [--phase P] [--peer PEER] [--json]

Flags:
  --phase P    invited, positions, rounds, converge, closing, closed or broken
  --peer PEER  a peer name or public key, with an optional "@"
  --json       print machine-readable JSON on stdout

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
`

func runDebates(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet debates", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	phase := fs.String("phase", "", "invited, positions, rounds, converge, closing, closed or broken")
	peer := fs.String("peer", "", "a peer name or public key")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, debatesUsage) }
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
	var res daemon.DebateListResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "debate_list", map[string]any{"phase": *phase, "peer": *peer}, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK bool `json:"ok"`
			daemon.DebateListResult
		}{OK: true, DebateListResult: res})
		return exitOK
	}
	if len(res.Debates) == 0 {
		_, _ = fmt.Fprintln(stdout, "No debates.")
		return exitOK
	}
	for _, d := range res.Debates {
		_, _ = fmt.Fprintf(stdout, "%s  %s  role %s  phase %s  turn %s  %s\n", d.Session, d.Peer.Name, d.Role, d.Phase, d.Turn, d.Request.Title)
	}
	return exitOK
}
