// Command agentnetd - AgentNet daemon: local coordination service for agents.
package main

import (
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/idle"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/logfile"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/service"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

const summary = "AgentNet daemon: local coordination service for agents."

// RelayEnv supplies the relay URL when --relay is not given.
const RelayEnv = "DORYLINAE_RELAY_URL"

// Exit codes beyond the plain 0/1/2 of version.Main-style binaries.
const exitAlreadyRunning = 3

// stopTimeout bounds how long `agentnetd stop` waits for the daemon to exit.
const stopTimeout = 10 * time.Second

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
		case "stop":
			return runStop(args[1:], stdout, stderr)
		case "version":
			return version.Command("agentnetd", args[1:], stdout, stderr)
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
	relayCA := fs.String("relay-ca", "", "PEM CA certificate(s) trusted for a wss:// relay, besides the system roots (default: "+relayCAFile+" in the config directory, if install stored one)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stdout, "%s\n\nUsage:\n  %[2]s [run] [--home DIR] [--relay URL] [--relay-ca FILE] [--log-file PATH] [--version]\n  %[2]s install [--home DIR] [--relay URL] [--relay-ca FILE] [--allow-writable-program] [--dry-run]\n  %[2]s uninstall [--home DIR] [--dry-run]\n  %[2]s stop [--home DIR]\n  %[2]s version [--json]\n\nFlags (run):\n", summary, name)
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

	warning, err := checkRelayURL(*relayURL, os.Getenv(InsecureRelayEnv) == "1")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 2
	}
	p, err := resolvePaths(*home)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	rotateOutLog(stderr, p.Dir)
	roots, err := relayRoots(p.Dir, *relayCA)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 2
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
	if warning != "" {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, warning)
		logger.Warn(warning, "event", "relay_insecure")
	}
	if err := daemon.RunWithOptions(ctx, p, ready, daemonOptions(*relayURL, roots, logger)); err != nil {
		if errors.Is(err, ipc.ErrAlreadyRunning) {
			if pid := runningPID(p.Endpoint); pid > 0 {
				_, _ = fmt.Fprintf(stderr, "%s: agentnetd is already running for %s (pid %d)\n", name, p.Dir, pid)
			} else {
				_, _ = fmt.Fprintf(stderr, "%s: agentnetd is already running for %s\n", name, p.Dir)
			}
			return exitAlreadyRunning
		}
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		if errors.Is(err, daemon.ErrApprovalRequiresTerminal) {
			return 2
		}
		return 1
	}
	return 0
}

// rotateOutLog bounds launchd's stdout/stderr file (OD-F14-3 (e), R55-F14):
// when stderr is <dir>/agentnetd.out.log and it is over logfile.MaxSize, it
// is renamed to agentnetd.out.log.1, replacing an older one. launchd opens a
// fresh file at the next start; this run keeps writing to the renamed one,
// so the latest crash report is kept. Best effort: errors are ignored. No
// other platform uses that name, so elsewhere this does nothing.
func rotateOutLog(stderr io.Writer, dir string) {
	f, ok := stderr.(*os.File)
	if !ok {
		return
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() <= logfile.MaxSize {
		return
	}
	path := filepath.Join(dir, service.LaunchdOutFileName)
	pi, err := os.Stat(path)
	if err != nil || !os.SameFile(fi, pi) {
		return
	}
	_ = os.Rename(path, path+".1") // replaces an older .1
}

// daemonOptions is the production daemon.Options. Idle is the OS input-idle
// probe behind a 5 s cache, for the human-present level (R55-033,
// Docs/protocol/presence.md Idle detection).
func daemonOptions(relayURL string, roots *x509.CertPool, logger *slog.Logger) daemon.Options {
	return daemon.Options{
		RelayURL: relayURL, RelayRoots: roots, Logger: logger,
		Idle: idle.Cached(idle.CacheTTL, time.Now, idle.Idle),
	}
}

// runningPID asks the daemon already listening on endpoint for its PID, so
// the "already running" message is useful without inventing a pid file
// (Docs/cli/agentnetd.md). It returns 0 if status does not answer in time.
func runningPID(endpoint string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var res daemon.StatusResult
	if err := ipc.Call(ctx, endpoint, "status", nil, &res); err != nil {
		return 0
	}
	return res.PID
}
