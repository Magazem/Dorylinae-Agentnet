// Command agentnetd - AgentNet daemon: local coordination service for agents.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/logfile"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

const summary = "AgentNet daemon: local coordination service for agents."

// RelayEnv supplies the relay URL when --relay is not given.
const RelayEnv = "DORYLINAE_RELAY_URL"

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
	relayURL := fs.String("relay", os.Getenv(RelayEnv), "relay WebSocket URL, e.g. ws://127.0.0.1:8787 (default: $"+RelayEnv+"; empty = no relay)")
	logFile := fs.String("log-file", "", "append log output to this file instead of stderr, rotating at 1 MiB to PATH.1")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stdout, "%s\n\nUsage:\n  %[2]s [run] [--home DIR] [--relay URL] [--log-file PATH] [--version]\n  %[2]s install [--home DIR] [--relay URL] [--dry-run]\n  %[2]s uninstall [--home DIR] [--dry-run]\n\nFlags (run):\n", summary, name)
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
	logOut := stderr
	if *logFile != "" {
		lf, err := logfile.Open(*logFile, logfile.MaxSize)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return 1
		}
		defer func() { _ = lf.Close() }()
		logOut = lf
	}
	logger := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := daemon.RunWithOptions(ctx, p, ready, daemon.Options{RelayURL: *relayURL, Logger: logger}); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	return 0
}
