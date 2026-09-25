package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

// runStop implements `agentnetd stop`: the same shutdown-over-IPC as
// `agentnet stop` (Docs/cli/stop.md), for use when only agentnetd is on PATH.
func runStop(args []string, stdout, stderr io.Writer) int {
	name := "agentnetd stop"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "config directory (default: $"+paths.HomeEnv+" or the user config dir)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stdout, "Ask agentnetd to shut down cleanly and wait for it to exit.\n\nUsage:\n  %s [--home DIR]\n\nFlags:\n", name)
		fs.SetOutput(stdout)
		fs.PrintDefaults()
		fs.SetOutput(stderr)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "%s: unexpected argument %q\n", name, fs.Arg(0))
		return 2
	}

	p, err := resolvePaths(*home)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	if err := daemon.StopWait(context.Background(), p.Endpoint, stopTimeout); err != nil {
		if errors.Is(err, ipc.ErrNotRunning) {
			_, _ = fmt.Fprintf(stderr, "%s: agentnetd is not running (endpoint: %s)\n", name, p.Endpoint)
			return 3
		}
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "agentnetd stopped")
	return 0
}
