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
	"syscall"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

const (
	name           = "relay"
	summary        = "AgentNet relay: forwards encrypted traffic between daemons."
	defaultListen  = "127.0.0.1:8787"
	shutdownWindow = 5 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	listen := fs.String("listen", defaultListen, "address to listen on, host:port (port 0 picks a free port)")
	allowPublic := fs.Bool("allow-non-loopback", false, "allow --listen on a non-loopback address; Phase 0 relay has no TLS, so this is plaintext")
	verbose := fs.Bool("verbose", false, "also log every routed envelope (metadata only, never payloads)")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stdout, "%s\n\nUsage:\n  %s [--listen HOST:PORT] [--allow-non-loopback] [--verbose] [--version]\n\nDaemons connect to ws://HOST:PORT%s.\nThe relay never reads or logs envelope payloads.\n\nFlags:\n", summary, name, envelope.ConnectPath)
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
	rs := relay.New(relay.Options{Logger: logger})
	srv := &http.Server{
		Handler:           rs,
		ReadHeaderTimeout: 10 * time.Second,
	}
	_, _ = fmt.Fprintf(stdout, "%s listening on %s\n", name, ln.Addr())

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
