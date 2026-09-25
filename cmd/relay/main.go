// Command relay - AgentNet relay: forwards encrypted traffic between daemons.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

const (
	name            = "relay"
	summary         = "AgentNet relay: forwards encrypted traffic between daemons."
	defaultListen   = "127.0.0.1:8787"
	shutdownWindow  = 5 * time.Second
	defaultQueueTTL = 7 * 24 * time.Hour
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "version" {
		return version.Command(name, args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	listen := fs.String("listen", defaultListen, "address to listen on, host:port (port 0 picks a free port)")
	allowPublic := fs.Bool("allow-non-loopback", false, "allow --listen on a non-loopback address; Phase 0 relay has no TLS, so this is plaintext")
	verbose := fs.Bool("verbose", false, "also log every routed envelope (metadata only, never payloads)")
	queueDB := fs.String("queue-db", "", "SQLite file holding envelopes queued for offline peers (default: relay-queue.db in the config directory)")
	queueTTL := fs.Duration("queue-ttl", defaultQueueTTL, "how long a queued envelope waits for its recipient")
	allowV1 := fs.Bool("allow-pairing-v1", false, "accept v1 pairing frames (relay-generated 10-character codes); default on when listening on loopback, off otherwise")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stdout, "%s\n\nUsage:\n  %s [--listen HOST:PORT] [--allow-non-loopback] [--queue-db PATH] [--queue-ttl DURATION] [--allow-pairing-v1[=false]] [--verbose] [--version]\n  %s version [--json]\n\nDaemons connect to ws://HOST:PORT%s.\nThe relay never reads or logs envelope payloads.\n\nFlags:\n", summary, name, name, envelope.ConnectPath)
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
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, version.String(name))
		return 0
	}

	if !*allowPublic {
		if err := requireLoopback(*listen); err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return 2
		}
	}
	v1 := *allowV1
	explicit := false
	fs.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "allow-pairing-v1" })
	if !explicit {
		v1 = requireLoopback(*listen) == nil
	}
	if *queueTTL <= 0 {
		_, _ = fmt.Fprintf(stderr, "%s: --queue-ttl must be positive\n", name)
		return 2
	}
	dbPath := *queueDB
	if dbPath == "" {
		p, err := paths.Default()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return 1
		}
		dbPath = p.RelayQueueDB
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: create queue directory: %v\n", name, err)
		return 1
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	rs, err := relay.Open(relay.Options{Logger: logger, QueuePath: dbPath, QueueTTL: *queueTTL, DisablePairingV1: !v1})
	if err != nil {
		_ = ln.Close()
		_, _ = fmt.Fprintf(stderr, "%s: cannot open offline queue %s: %v\n", name, dbPath, err)
		return 1
	}
	srv := &http.Server{
		Handler:           rs,
		ReadHeaderTimeout: 10 * time.Second,
	}
	_, _ = fmt.Fprintf(stderr, "%s listening on %s; Ctrl+C to stop\n", name, ln.Addr())

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownWindow)
	defer cancel()
	_ = srv.Shutdown(sctx)
	rs.Close() // hijacked WebSocket connections are not covered by Shutdown
	return 0
}

// requireLoopback rejects listen addresses that are reachable from other machines.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--listen %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("--listen %q is not a loopback address; the Phase 0 relay has no TLS. Use 127.0.0.1 or pass --allow-non-loopback", addr)
}
