package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// requestBody is the machine-readable output of `request --json` (submit).
type requestBody struct {
	OK bool `json:"ok"`
	daemon.RequestSubmitResult
}

const briefTemplate = `What: <one sentence: what you need>
Why: <one or two sentences of context>
Done when: <how the other side knows it is finished>
`

func runRequest(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		requestUsage(stdout)
		return exitOK
	}
	return runRequestSubmit(args, stdout, stderr)
}

func requestUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Sends a teammate's agent a request (a review, a task or a question).

Usage:
  agentnet request <peer> <type> --title T (--brief B | --brief-from-file F)
                   [--urgency low|normal|high|blocking] [--urgency-reason R]
                   [--artifact SPEC]... [--grant ACTION=RESOURCE] [--deadline D]
                   [--team TEAM] [--idempotency-key K] [--json]

<peer> is a peer name or public key, with an optional "@". <type> is review,
task or question.

Flags:
  --title T             required. 1-120 characters, one line
  --brief B              the brief (Markdown text, up to 16 KiB). Exactly one
                         of --brief and --brief-from-file is required
  --brief-from-file F    read the brief from file F ("-" = stdin). CRLF becomes LF
  --urgency U            low, normal (default), high or blocking
  --urgency-reason R     required with high and blocking. Up to 280 characters
  --artifact SPEC        repeatable, up to 20. SPEC is either space-separated
                         key=value pairs with keys url, branch, commit and
                         path, or a JSON object starting with "{"
  --grant ACTION=RESOURCE  an optional hint about the access the request
                         needs. It grants nothing
  --deadline D           RFC 3339 time, or a duration from now (90m, 2h, 3d)
  --team TEAM            needed only when you share several teams with the peer
  --idempotency-key K    1-64 characters of [A-Za-z0-9._:-]. Running the same
                         command again with the same key returns the first
                         request instead of sending a second one
  --json                 print machine-readable JSON on stdout

The brief. Write the brief for the other agent; it gets nothing else from you:

`+briefTemplate+`
Put links, branches, commits and paths in --artifact, not in the brief.

Behaviour: the command returns in under 2 seconds, always with status: queued
when the request was accepted locally, whether or not the peer is online. The
daemon delivers it, and holds it for up to 7 days while the peer is offline.

Exit codes: 0 queued (or duplicate for an idempotency key), 1 error, 2 usage,
3 daemon not running.
`)
}

func runRequestSubmit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet request", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	title := fs.String("title", "", "request title, 1-120 characters")
	brief := fs.String("brief", "", "the brief")
	briefFile := fs.String("brief-from-file", "", "read the brief from a file (- for stdin)")
	urgency := fs.String("urgency", "", "low, normal (default), high or blocking")
	urgencyReason := fs.String("urgency-reason", "", "required with high and blocking")
	deadline := fs.String("deadline", "", "RFC 3339 time, or a duration from now (90m, 2h, 3d)")
	team := fs.String("team", "", "team ref, needed only when you share several teams with the peer")
	idemKey := fs.String("idempotency-key", "", "1-64 characters of [A-Za-z0-9._:-]")
	grant := fs.String("grant", "", "ACTION=RESOURCE: an optional hint about the access needed")
	var artifacts []string
	fs.Func("artifact", "an artifact pointer (repeatable, up to 20)", func(v string) error {
		artifacts = append(artifacts, v)
		return nil
	})
	fs.Usage = func() { requestUsage(stdout) }

	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	switch {
	case len(pos) < 2:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give <peer> <type> (see 'agentnet request --help')")
	case len(pos) > 2:
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", pos[2]))
	case *title == "":
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--title is required")
	case (*brief == "") == (*briefFile == ""):
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one of --brief or --brief-from-file")
	}

	briefText := *brief
	if *briefFile != "" {
		text, err := readBriefFile(*briefFile)
		if err != nil {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
		}
		briefText = text
	}
	briefText = strings.ReplaceAll(briefText, "\r\n", "\n")

	params := daemon.RequestSubmitParams{
		To: pos[0], Type: pos[1], Team: *team, Title: *title, Brief: briefText,
		Urgency: *urgency, UrgencyReason: *urgencyReason, Deadline: *deadline, IdempotencyKey: *idemKey,
	}
	for _, spec := range artifacts {
		a, err := parseArtifactFlag(spec)
		if err != nil {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
		}
		params.Artifacts = append(params.Artifacts, a)
	}
	if *grant != "" {
		action, resource, ok := strings.Cut(*grant, "=")
		if !ok || action == "" || resource == "" {
			return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--grant must be ACTION=RESOURCE")
		}
		params.RequestedGrant = &daemon.GrantParam{Action: action, Resource: resource}
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
	_, _ = fmt.Fprintf(stdout, "%s %s %s request %s to %s (team %s)\n", verb, res.Urgency, pos[1], res.ID, res.Peer.Name, res.Team.Name)
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

// requestStdin is where --brief-from-file - reads from; tests replace it.
var requestStdin io.Reader = os.Stdin

// maxBriefFileBytes bounds what --brief-from-file reads: a brief is at most
// 16384 bytes after CRLF → LF (Docs/protocol/request.md §Size limits), so a
// valid input is at most twice that. Anything longer is refused without
// reading it all (a pipe or device may never end).
const maxBriefFileBytes = 2 * 16384

func readBriefFile(path string) (string, error) {
	name := path
	var r io.Reader
	if path == "-" {
		name, r = "stdin", requestStdin
	} else {
		f, err := os.Open(path) //nolint:gosec // the path is a user-supplied CLI flag, as intended
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxBriefFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	if len(b) > maxBriefFileBytes {
		return "", fmt.Errorf("read %s: over %d bytes; the brief is limited to 16384 bytes", name, maxBriefFileBytes)
	}
	// Checked here: JSON-encoding the IPC params would silently turn invalid
	// bytes into U+FFFD, so the daemon's UTF-8 check would never see them
	// (Docs/protocol/request.md §The brief: "It must be valid UTF-8").
	if !utf8.Valid(b) {
		return "", fmt.Errorf("read %s: the brief is not valid UTF-8", name)
	}
	return string(b), nil
}

func parseArtifactFlag(spec string) (daemon.ArtifactParam, error) {
	trimmed := strings.TrimSpace(spec)
	if strings.HasPrefix(trimmed, "{") {
		var a daemon.ArtifactParam
		if err := json.Unmarshal([]byte(trimmed), &a); err != nil {
			return daemon.ArtifactParam{}, fmt.Errorf("--artifact: invalid JSON: %w", err)
		}
		return a, nil
	}
	var a daemon.ArtifactParam
	for _, tok := range strings.Fields(trimmed) {
		key, val, ok := strings.Cut(tok, "=")
		if !ok {
			return daemon.ArtifactParam{}, fmt.Errorf("--artifact: %q is not key=value", tok)
		}
		switch key {
		case "url":
			a.URL = val
		case "branch":
			a.Branch = val
		case "commit":
			a.Commit = val
		case "path":
			a.Path = val
		default:
			return daemon.ArtifactParam{}, fmt.Errorf("--artifact: unknown key %q", key)
		}
	}
	return a, nil
}
