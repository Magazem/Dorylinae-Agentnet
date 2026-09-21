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

	"dorylinae/internal/daemon"
	"dorylinae/internal/paths"
	"dorylinae/internal/version"
)

const summary = "AgentNet daemon: local coordination service for agents."

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	name := "agentnetd"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	home := fs.String("home", "", "config directory (default: $"+paths.HomeEnv+" or the user config dir)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stdout, "%s\n\nUsage:\n  %s [--home DIR] [--version]\n\nFlags:\n", summary, name)
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

	var (
		p   paths.Paths
		err error
	)
	if *home != "" {
		p, err = paths.In(*home)
	} else {
		p, err = paths.Default()
	}
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
