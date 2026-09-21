// Command agentnetd - AgentNet daemon: local coordination service for agents.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

const summary = "AgentNet daemon: local coordination service for agents."

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "install", "uninstall":
			return runService(ctx, args[0], args[1:], stdout, stderr)
		case "run":
			args = args[1:]
		}
	}
	name := "agentnetd"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	home := fs.String("home", "", "config directory (default: $"+paths.HomeEnv+" or the user config dir)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stdout, "%s\n\nUsage:\n  %[2]s [run] [--home DIR] [--version]\n  %[2]s install [--home DIR] [--dry-run]\n  %[2]s uninstall [--home DIR] [--dry-run]\n\nFlags (run):\n", summary, name)
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
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, version.String(name))
		return 0
	}

	p, err := resolvePaths(*home)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}

	ready := make(chan struct{})
	go func() {
		<-ready
		_, _ = fmt.Fprintf(stdout, "%s listening on %s (db: %s)\n", name, p.Endpoint, p.DB)
	}()
	if err := daemon.Run(ctx, p, ready); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	return 0
}
