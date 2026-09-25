package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// Ticket 1.6b: `agentnet inbox`, `accept`, `decline`, `defer`, `complete`
// (Docs/cli/inbox.md, Docs/protocol/request.md §Inbox (1.6)).

// inboxBody is `inbox --json`'s output.
type inboxBody struct {
	OK bool `json:"ok"`
	daemon.RequestListResult
}

func runInbox(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet inbox", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	team := fs.String("team", "", "only requests in this team")
	all := fs.Bool("all", false, "every received request, including answered, cancelled and not-yet-due deferred ones")
	fs.Usage = func() { inboxUsage(stdout) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 0 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "inbox takes no positional arguments")
	}

	params := map[string]any{}
	if *team != "" {
		params["team"] = *team
	}
	if *all {
		params["all"] = true
	}
	var res daemon.RequestListResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "inbox_list", params, &res); code != exitOK {
		return code
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(inboxBody{OK: true, RequestListResult: res})
		return exitOK
	}
	printInbox(stdout, res.Requests)
	return exitOK
}

func inboxUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Lists the requests addressed to you.

Usage:
  agentnet inbox [--team TEAM] [--all] [--json]

Flags:
  --team TEAM   only requests in that team
  --all         every received request, including answered, cancelled and
                not-yet-due deferred ones
  --json        print machine-readable JSON on stdout

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
`)
}

func printInbox(w io.Writer, requests []daemon.RequestView) {
	if len(requests) == 0 {
		_, _ = fmt.Fprintln(w, "Inbox empty.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tFROM\tTYPE\tURGENCY\tPRIO\tAGE\tTITLE")
	now := time.Now()
	for _, v := range requests {
		prio := "-"
		if v.Priority != nil {
			prio = fmt.Sprintf("%d", *v.Priority)
		}
		age := "-"
		if v.ReceivedAt != "" {
			if t, err := time.Parse(time.RFC3339, v.ReceivedAt); err == nil {
				age = formatAge(now.Sub(t))
			}
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", v.ID, v.Peer.Name, v.Type, v.Urgency, prio, age, v.Title)
	}
	_ = tw.Flush()
}

// formatAge renders a compact age like "4m", "2h" or "3d" (Docs/cli/inbox.md
// §Human output).
func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "0m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// lifecycleBody is the machine-readable output of accept, decline, defer and
// complete (Docs/cli/inbox.md §--json output).
type lifecycleBody struct {
	OK bool `json:"ok"`
	daemon.RequestLifecycleResult
}

func runAccept(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet accept", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	from := fs.String("from", "", "pick the sender (peer name or public key) when the id matches requests from several peers")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Accepts a request from your inbox.

Usage:
  agentnet accept <id> [--from <peer>] [--json]

  --from <peer>  the sender's name or public key, when the id matches requests
                 from several peers

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
`)
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <id> (see 'agentnet accept --help')")
	}

	params := idFromParam(pos[0], *from)
	var res daemon.RequestLifecycleResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "request_accept", params, &res); code != exitOK {
		return code
	}
	return printLifecycleResult(*asJSON, stdout, "Accepted", res)
}

func runDecline(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet decline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	from := fs.String("from", "", "pick the sender (peer name or public key) when the id matches requests from several peers")
	reason := fs.String("reason", "", "required. 1-500 characters, sent to the requester")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Declines a request from your inbox.

Usage:
  agentnet decline <id> --reason R [--from <peer>] [--json]

  --from <peer>  the sender's name or public key, when the id matches requests
                 from several peers

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
`)
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <id> (see 'agentnet decline --help')")
	}
	if *reason == "" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--reason is required")
	}

	params := idFromParam(pos[0], *from)
	params["reason"] = *reason
	var res daemon.RequestLifecycleResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "request_decline", params, &res); code != exitOK {
		return code
	}
	return printLifecycleResult(*asJSON, stdout, "Declined", res)
}

func runDefer(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet defer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	from := fs.String("from", "", "pick the sender (peer name or public key) when the id matches requests from several peers")
	until := fs.String("until", "", "required. RFC 3339 time or a duration (2h, 3d), at most 90 days ahead")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Defers a request from your inbox.

Usage:
  agentnet defer <id> --until T [--from <peer>] [--json]

  --from <peer>  the sender's name or public key, when the id matches requests
                 from several peers

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
`)
	}
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <id> (see 'agentnet defer --help')")
	}
	if *until == "" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "--until is required")
	}

	params := idFromParam(pos[0], *from)
	params["until"] = *until
	var res daemon.RequestLifecycleResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "request_defer", params, &res); code != exitOK {
		return code
	}
	return printLifecycleResult(*asJSON, stdout, "Deferred", res)
}

// completeResultParam is request_complete's "result" param
// (Docs/protocol/ipc.md §Requests).
type completeResultParam struct {
	Status    string                 `json:"status"`
	Summary   string                 `json:"summary,omitempty"`
	ExitCode  *int64                 `json:"exit_code,omitempty"`
	Output    string                 `json:"output,omitempty"`
	Artifacts []daemon.ArtifactParam `json:"artifacts,omitempty"`
}

// completeParams is request_complete's params.
type completeParams struct {
	ID     string               `json:"id"`
	From   string               `json:"from,omitempty"`
	Note   string               `json:"note,omitempty"`
	Result *completeResultParam `json:"result,omitempty"`
}

func runComplete(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet complete", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	from := fs.String("from", "", "pick the sender (peer name or public key) when the id matches requests from several peers")
	note := fs.String("note", "", "optional, up to 2000 characters, sent to the requester")
	status := fs.String("status", "", "attach a result: pass, fail, partial or n/a")
	summary := fs.String("summary", "", "result: one line, up to 280 characters")
	exitCode := fs.Int64("exit-code", 0, "result: an integer from -2147483648 to 4294967295")
	outputFile := fs.String("output-from-file", "", "result: attach a text output from file F (- = stdin), up to 32 KiB")
	var artifacts []string
	fs.Func("artifact", "result: an artifact pointer (repeatable, up to 20)", func(v string) error {
		artifacts = append(artifacts, v)
		return nil
	})
	fs.Usage = func() { completeUsage(stdout) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(pos) != 1 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", "give exactly one <id> (see 'agentnet complete --help')")
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	resultFlagGiven := set["summary"] || set["exit-code"] || set["output-from-file"] || set["artifact"]
	if resultFlagGiven && *status == "" {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage",
			"--status is required with --summary, --exit-code, --output-from-file or --artifact")
	}

	var result *completeResultParam
	if *status != "" {
		result = &completeResultParam{Status: *status}
		if set["summary"] {
			result.Summary = *summary
		}
		if set["exit-code"] {
			ec := *exitCode
			result.ExitCode = &ec
		}
		if set["output-from-file"] {
			out, err := readOutputFile(*outputFile)
			if err != nil {
				return failJSON(*asJSON, stdout, stderr, exitError, "bad_request", err.Error())
			}
			result.Output = out
		}
		for _, spec := range artifacts {
			a, err := parseArtifactFlag(spec)
			if err != nil {
				return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
			}
			result.Artifacts = append(result.Artifacts, a)
		}
	}

	params := completeParams{ID: pos[0], From: *from, Note: *note, Result: result}
	var res daemon.RequestLifecycleResult
	if code := callDaemon(*asJSON, stdout, stderr, statusTimeout, "request_complete", params, &res); code != exitOK {
		return code
	}
	return printLifecycleResult(*asJSON, stdout, "Completed", res)
}

func completeUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Marks an accepted request complete, optionally with a result.

Usage:
  agentnet complete <id> [--note N] [--status pass|fail|partial|n/a [--summary S]
                    [--exit-code N] [--output-from-file F] [--artifact SPEC]...]
                    [--from <peer>] [--json]

Flags:
  --note N               optional, up to 2000 characters, sent to the requester
  --status S              attach a result: pass, fail, partial or n/a. Required
                         when any other result flag is given
  --summary S             one line, up to 280 characters
  --exit-code N           an integer from -2147483648 to 4294967295
  --output-from-file F    a text output such as a test log from file F ("-" =
                         stdin), up to 32768 bytes. CRLF becomes LF, and ANSI
                         colour and cursor sequences are removed
  --artifact SPEC         repeatable, up to 20. The same SPEC as
                         'agentnet request --artifact'
  --from <peer>           the sender's name or public key, when the id matches
                         requests from several peers
  --json                  print machine-readable JSON on stdout

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running.
`)
}

func idFromParam(id, from string) map[string]any {
	p := map[string]any{"id": id}
	if from != "" {
		p["from"] = from
	}
	return p
}

func printLifecycleResult(asJSON bool, stdout io.Writer, verb string, res daemon.RequestLifecycleResult) int {
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(lifecycleBody{OK: true, RequestLifecycleResult: res})
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "%s %s from %s\n", verb, res.Request.ID, res.Request.Peer.Name)
	return exitOK
}

// completeStdin is where --output-from-file - reads from; tests replace it.
var completeStdin io.Reader = os.Stdin

// csiPattern matches an ANSI CSI escape sequence: ESC "[" then parameter and
// intermediate bytes, then a final byte in '@'-'~' (Docs/cli/inbox.md
// §Result).
var csiPattern = regexp.MustCompile("\x1b\\[[0-9:;<=>?]*[ -/]*[@-~]")

// readOutputFile reads F ("-" = stdin), turns CRLF into LF, strips ANSI CSI
// sequences, and rejects any other control character (except tab) or invalid
// UTF-8 (Docs/cli/inbox.md §Result).
func readOutputFile(path string) (string, error) {
	name := path
	var r io.Reader
	if path == "-" {
		name, r = "stdin", completeStdin
	} else {
		f, err := os.Open(path) //nolint:gosec // the path is a user-supplied CLI flag, as intended
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = csiPattern.ReplaceAllString(s, "")
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("read %s: not valid UTF-8", name)
	}
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("read %s: contains a control character other than tab", name)
		}
	}
	return s, nil
}
