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
	default:
		_, _ = fmt.Fprintf(stderr, "agentnet: unknown command %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, `%s

Usage:
  agentnet <command> [flags]
  agentnet --version

Commands:
  status    Show whether the daemon is running, its PID and uptime
  identity  Print this agent's signed Agent Card

Run 'agentnet <command> --help' for command flags.
`, summary)
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
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Show whether agentnetd is running, with its PID and uptime.

Usage:
  agentnet status [--json]

Flags:
  --json    print machine-readable JSON on stdout

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

	var res daemon.StatusResult
	if err := ipc.Call(ctx, p.Endpoint, "status", nil, &res); err != nil {
		if errors.Is(err, ipc.ErrNotRunning) {
			return failJSON(*asJSON, stdout, stderr, exitDaemonNotFound, "daemon_not_running",
				fmt.Sprintf("agentnetd is not running (endpoint: %s)", p.Endpoint))
		}
		return failJSON(*asJSON, stdout, stderr, exitError, "daemon_error", err.Error())
	}

	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(statusBody{OK: true, StatusResult: res})
		return exitOK
	}
	up := time.Duration(res.UptimeSeconds * float64(time.Second)).Round(time.Second)
	_, _ = fmt.Fprintf(stdout, "agentnetd running\n  pid:     %d\n  uptime:  %s\n  version: %s\n", res.PID, up, res.Version)
	return exitOK
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
