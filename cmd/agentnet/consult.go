package main

// Ticket 2.5: `agentnet consult` (Docs/protocol/consult.md, Docs/cli/consult.md).

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

const consultUsage = `Asks a teammate's agent a question, with optional context files
(Docs/protocol/consult.md). It is a request of type question; the answer
arrives as a work session result.

Usage:
  agentnet consult <peer> (--question TEXT | --question-from-file F)
                   [--context-file F]... [--title T] [--team TEAM]
                   [--urgency low|normal|high|blocking] [--urgency-reason R]
                   [--deadline D] [--idempotency-key K] [--json]

<peer> is a peer name or public key, with an optional "@".

Flags:
  --question TEXT          the question (up to 16 KiB). Exactly one of
                           --question and --question-from-file is required
  --question-from-file F   read the question from file F ("-" = stdin)
  --context-file F         repeatable, up to 8. A text file of up to 65536
                           bytes (UTF-8, CRLF becomes LF, no control
                           characters except tab and newline). Only its base
                           name and text are sent. A binary file is refused
  --title T                default: the first line of the question, cut to 120
                           characters
  --team TEAM              needed only when you share several teams with the peer
  --urgency U              low, normal (default), high or blocking
  --urgency-reason R       required with high and blocking
  --deadline D             RFC 3339 time, or a duration from now (90m, 2h, 3d)
  --idempotency-key K      1-64 characters of [A-Za-z0-9._:-]
  --json                   print machine-readable JSON on stdout

The command returns at once with status: queued and the session id "session".
Wait for the answer with 'agentnet wait <session> --timeout 300'.

Exit codes: 0 queued (or duplicate), 1 error (bad_request, request_too_large,
no_shared_team, ...), 2 usage, 3 daemon not running.
`

// maxContextFileRead bounds what --context-file reads: a text is at most
// 65536 bytes after CRLF -> LF, so a valid file is at most twice that. Anything
// longer is refused without reading it all.
const maxContextFileRead = 2 * 65536

func runConsult(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet consult", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	question := fs.String("question", "", "the question")
	questionFile := fs.String("question-from-file", "", "read the question from a file (- for stdin)")
	title := fs.String("title", "", "request title, default the first line of the question")
	urgency := fs.String("urgency", "", "low, normal (default), high or blocking")
	urgencyReason := fs.String("urgency-reason", "", "required with high and blocking")
	deadline := fs.String("deadline", "", "RFC 3339 time, or a duration from now (90m, 2h, 3d)")
	team := fs.String("team", "", "team ref, needed only when you share several teams with the peer")
	idemKey := fs.String("idempotency-key", "", "1-64 characters of [A-Za-z0-9._:-]")
	var contextFiles []string
	fs.Func("context-file", "a context file (repeatable, up to 8)", func(v string) error {
		contextFiles = append(contextFiles, v)
		return nil
	})
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, consultUsage) }

	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	switch {
	case len(pos) < 1:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give <peer> (see 'agentnet consult --help')")
	case len(pos) > 1:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", pos[1]))
	case set["question"] == set["question-from-file"]:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one of --question or --question-from-file")
	}

	text := *question
	if set["question-from-file"] {
		t, err := readBriefFile(*questionFile)
		if err != nil {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", strings.Replace(err.Error(), "the brief", "the question", 1))
		}
		text = t
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "the question is empty")
	}
	t := *title
	if t == "" {
		t = consultTitle(text)
	}

	params := daemon.RequestSubmitParams{
		To: pos[0], Type: "question", Team: *team, Title: t, Brief: text,
		Urgency: *urgency, UrgencyReason: *urgencyReason, Deadline: *deadline, IdempotencyKey: *idemKey,
	}
	for _, path := range contextFiles {
		c, err := readContextFile(path)
		if err != nil {
			return failJSON(*asJSON, stdout, stderr, exitError, "bad_request", err.Error())
		}
		params.Context = append(params.Context, c)
	}
	if !*asJSON {
		for _, c := range params.Context {
			_, _ = fmt.Fprintf(stderr, "context: sending %s (%d bytes)\n", c.Name, len(c.Text))
		}
	}

	var res daemon.RequestSubmitResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "request_submit", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(requestBody{OK: true, RequestSubmitResult: res})
		return exitOK
	}
	verb := "Queued"
	if res.Duplicate {
		verb = "Already queued (duplicate)"
	}
	_, _ = fmt.Fprintf(stdout, "%s %s consult %s to %s (team %s)\n  session: %s (wait with 'agentnet wait %s')\n",
		verb, res.Urgency, res.ID, res.Peer.Name, res.Team.Name, res.Session, res.Session)
	if !res.Peer.DaemonOnline {
		lastSeen := "never"
		if res.Peer.LastSeen != nil {
			lastSeen = *res.Peer.LastSeen
		}
		_, _ = fmt.Fprintf(stdout, "  %s is offline, last seen %s; it will be delivered when %s is back.\n", res.Peer.Name, lastSeen, res.Peer.Name)
	}
	if res.UrgencyNote != "" {
		_, _ = fmt.Fprintf(stdout, "  %s\n", res.UrgencyNote)
	}
	return exitOK
}

// consultTitle is the default title: the first non-blank line of the question,
// cut to 120 code points in total, the last being "…" when it was cut
// (Docs/protocol/consult.md §agentnet consult).
func consultTitle(question string) string {
	line := ""
	for _, l := range strings.Split(question, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
			break
		}
	}
	runes := []rune(line)
	if len(runes) <= 120 {
		return line
	}
	return strings.TrimSpace(string(runes[:119])) + "…"
}

// readContextFile reads one --context-file: its base name and its text, with
// CRLF turned into LF. A file that is not UTF-8 text is refused
// (Docs/protocol/consult.md §Context files).
func readContextFile(path string) (daemon.ContextParam, error) {
	if path == "-" {
		return daemon.ContextParam{}, errors.New("--context-file needs a file name, not stdin")
	}
	f, err := os.Open(path) //nolint:gosec // the path is a user-supplied CLI flag, as intended
	if err != nil {
		return daemon.ContextParam{}, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxContextFileRead+1))
	if err != nil {
		return daemon.ContextParam{}, fmt.Errorf("read %s: %w", path, err)
	}
	if len(b) > maxContextFileRead {
		return daemon.ContextParam{}, fmt.Errorf("context file %s is over %d bytes; a context text is limited to 65536 bytes", path, maxContextFileRead)
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !utf8.ValidString(s) {
		return daemon.ContextParam{}, fmt.Errorf("context file %s is not text", path)
	}
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return daemon.ContextParam{}, fmt.Errorf("context file %s is not text", path)
		}
	}
	return daemon.ContextParam{Name: filepath.Base(path), Text: s}, nil
}
