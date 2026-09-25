package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

// stopTimeout bounds how long `agentnet stop` waits for agentnetd to exit
// after asking it to shut down (Docs/cli/stop.md).
const stopTimeout = 10 * time.Second

func runStop(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stdout, `Ask the running agentnetd to shut down cleanly (the same path as Ctrl+C or
SIGTERM: outbox flushed, database closed, audit row written) and wait for it
to exit.

Usage:
  agentnet stop [--json]

Flags:
  --json   print machine-readable JSON on stdout

Exit codes: 0 stopped, 1 error, 2 usage, 3 daemon not running.
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

	err = daemon.StopWait(context.Background(), p.Endpoint, stopTimeout)
	switch {
	case err == nil:
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(struct {
				OK bool `json:"ok"`
			}{true})
		} else {
			_, _ = fmt.Fprintln(stdout, "agentnetd stopped")
		}
		return exitOK
	case errors.Is(err, ipc.ErrNotRunning):
		return failJSON(*asJSON, stdout, stderr, exitDaemonNotFound, "daemon_not_running",
			fmt.Sprintf("agentnetd is not running (endpoint: %s)", p.Endpoint))
	case errors.Is(err, daemon.ErrStopTimeout):
		return failJSON(*asJSON, stdout, stderr, exitError, "stop_timeout", "agentnetd did not stop within 10s")
	default:
		return failJSON(*asJSON, stdout, stderr, exitError, "daemon_error", err.Error())
	}
}
