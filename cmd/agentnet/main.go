// Command agentnet - AgentNet CLI: talks to the local agentnetd daemon.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

const summary = "AgentNet CLI: talks to the local agentnetd daemon."

// Exit codes; documented in Docs/cli/status.md.
const (
	exitOK             = 0
	exitError          = 1
	exitUsage          = 2
	exitDaemonNotFound = 3
)

// statusTimeout bounds the whole status call (acceptance: under 2 s).
const statusTimeout = 1500 * time.Millisecond

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return exitOK
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(stdout)
		return exitOK
	case "--version", "-version":
		_, _ = fmt.Fprintln(stdout, version.String("agentnet"))
		return exitOK
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "identity":
		return runIdentity(args[1:], stdout, stderr)
	case "pair":
		return runPair(args[1:], stdout, stderr)
	case "peers":
		return runPeers(args[1:], stdout, stderr)
	case "team":
		return runTeam(args[1:], stdout, stderr)
	case "presence":
		return runPresence(args[1:], stdout, stderr)
	case "ping":
		return runPing(args[1:], stdout, stderr)
	case "request":
		return runRequest(args[1:], stdout, stderr)
	case "consult":
		return runConsult(args[1:], stdout, stderr)
	case "inbox":
		return runInbox(args[1:], stdout, stderr)
	case "accept":
		return runAccept(args[1:], stdout, stderr)
	case "decline":
		return runDecline(args[1:], stdout, stderr)
	case "defer":
		return runDefer(args[1:], stdout, stderr)
	case "complete":
		return runComplete(args[1:], stdout, stderr)
	case "sessions":
		return runSessions(args[1:], stdout, stderr)
	case "session":
		return runSession(args[1:], stdout, stderr)
	case "result":
		return runResult(args[1:], stdout, stderr)
	case "wait":
		return runWait(args[1:], stdout, stderr)
	case "accept-result":
		return runAcceptResult(args[1:], stdout, stderr)
	case "notify":
		return runNotify(args[1:], stdout, stderr)
	case "approve":
		return runApprove(args[1:], stdout, stderr)
	case "device":
		return runDevice(args[1:], stdout, stderr)
	case "mail":
		if debugEnabled() {
			return runMail(args[1:], stdout, stderr)
		}
		fallthrough
	default:
		_, _ = fmt.Fprintf(stderr, "agentnet: unknown command %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	debug := ""
	if debugEnabled() {
		debug = "  mail      Debug only: queue a mail to a paired agent (mail send)\n"
	}
	_, _ = fmt.Fprintf(w, `%s

Usage:
  agentnet <command> [flags]
  agentnet --version

Commands:
  status    Show whether the daemon is running, its PID, uptime and outbox
  identity  Print this agent's signed Agent Card
  pair      Pair with another machine using a one-time code
  peers     List paired agents
  ping      Round-trip an encrypted message to a paired agent
  team      Create and manage teams
  presence  Show or set who can see this machine's presence
  request   Send a teammate's agent a request
  consult   Ask a teammate's agent a question, with context files
  inbox     List the requests addressed to you
  accept    Accept a request from your inbox
  decline   Decline a request from your inbox
  defer     Defer a request from your inbox
  complete  Mark an accepted request complete, optionally with a result
  notify    Configure desktop notifications
  approve   Confirm or reject a pending human approval
  device    Link two of your own devices (controller and helper)
%s
Run 'agentnet <command> --help' for command flags.
`, summary, debug)
}

// errBody is the machine-readable error under --json.
type errBody struct {
	OK    bool       `json:"ok"`
	Error *ipc.Error `json:"error"`
}

// statusBody is the machine-readable success output of `status --json`.
type statusBody struct {
	OK bool `json:"ok"`
	daemon.StatusResult
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	teamRef := fs.String("team", "", "also list this team's members with their presence")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Show whether agentnetd is running, with its PID, uptime and outbox.

Usage:
  agentnet status [--team TEAM] [--json]

Flags:
  --team TEAM   also list the members of TEAM (id or unique name) with their presence
  --json        print machine-readable JSON on stdout

Exit codes: 0 running, 1 error, 2 usage, 3 daemon not running.
`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if fs.NArg() > 0 {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}

	p, err := paths.Default()
	if err != nil {
		return failJSON(*asJSON, stdout, stderr, exitError, "daemon_error", err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()

	var params any
	if *teamRef != "" {
		params = daemon.StatusParams{Team: *teamRef}
	}
	var res daemon.StatusResult
	if err := ipc.Call(ctx, p.Endpoint, "status", params, &res); err != nil {
		if errors.Is(err, ipc.ErrNotRunning) {
			return failJSON(*asJSON, stdout, stderr, exitDaemonNotFound, "daemon_not_running",
				fmt.Sprintf("agentnetd is not running (endpoint: %s)", p.Endpoint))
		}
		var ie *ipc.Error
		if errors.As(err, &ie) {
			return failJSON(*asJSON, stdout, stderr, exitError, ie.Code, ie.Message)
		}
		return failJSON(*asJSON, stdout, stderr, exitError, "daemon_error", err.Error())
	}

	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(statusBody{OK: true, StatusResult: res})
		return exitOK
	}
	up := time.Duration(res.UptimeSeconds * float64(time.Second)).Round(time.Second)
	_, _ = fmt.Fprintf(stdout, "agentnetd running\n  pid:     %d\n  uptime:  %s\n  version: %s\n  outbox:  %d queued, %d relayed, %d expired\n",
		res.PID, up, res.Version, res.Outbox.Queued, res.Outbox.Relayed, res.Outbox.Expired)
	_, _ = fmt.Fprintf(stdout, "  presence: %s, relay %s\n", res.Presence.Mode, res.Presence.Relay)
	if res.Team != nil {
		printStatusTeam(stdout, res.Team)
	}
	return exitOK
}

// printStatusTeam prints the human `--team` table of Docs/cli/status.md.
func printStatusTeam(w io.Writer, t *daemon.StatusTeamResult) {
	owner := t.Owner
	for _, m := range t.Members {
		if m.PublicKey == t.Owner && m.Name != "" {
			owner = m.Name
			break
		}
	}
	_, _ = fmt.Fprintf(w, "team %s (%s), owner %s\n", t.Name, shortTeamID(t.ID), owner)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tDAEMON\tAGENT\tHUMAN\tLAST SEEN")
	for _, m := range t.Members {
		name := m.Name
		if name == "" {
			name = m.PublicKey
		}
		daemonCol, agentCol, humanCol := "offline", "-", "-"
		if m.DaemonOnline {
			daemonCol = "online"
			agentCol = "idle"
			if m.AgentActive {
				agentCol = "active"
			}
			humanCol = "unknown"
			if m.HumanPresent != nil {
				humanCol = "away"
				if *m.HumanPresent {
					humanCol = "present"
				}
			}
		}
		lastSeen := "never"
		if m.LastSeen != nil {
			lastSeen = *m.LastSeen
		}
		if m.Self {
			lastSeen = "now"
		}
		suffix := ""
		if m.Self {
			suffix = "\t(you)"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s%s\n", name, daemonCol, agentCol, humanCol, lastSeen, suffix)
	}
	_ = tw.Flush()
}

// failJSON reports an error: JSON on stdout under --json, plain text on stderr otherwise.
func failJSON(asJSON bool, stdout, stderr io.Writer, code int, errCode, msg string) int {
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(errBody{Error: &ipc.Error{Code: errCode, Message: msg}})
	} else {
		_, _ = fmt.Fprintln(stderr, "agentnet: "+msg)
	}
	return code
}
