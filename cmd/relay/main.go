// Command relay - AgentNet relay: forwards encrypted traffic between daemons.
package main

import (
	"context"
	"crypto/tls"
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
	"strings"
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
	if len(args) > 0 {
		switch args[0] {
		case "version":
			return version.Command(name, args[1:], stdout, stderr)
		case "backup":
			return runBackup(args[1:], stdout, stderr)
		case "restore":
			return runRestore(args[1:], stdout, stderr)
		case "admin":
			return runAdmin(args[1:], stdout, stderr)
		}
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	listen := fs.String("listen", defaultListen, "address to listen on, host:port (port 0 picks a free port)")
	allowPublic := fs.Bool("allow-non-loopback", false, "allow --listen on a non-loopback address; needs --tls-cert/--tls-key, --acme-domain or --behind-proxy")
	verbose := fs.Bool("verbose", false, "also log every routed envelope (metadata only, never payloads)")
	db := fs.String("db", "", "SQLite file holding the relay database (default: relay-queue.db in the config directory)")
	queueDB := fs.String("queue-db", "", "deprecated alias for --db")
	queueTTL := fs.Duration("queue-ttl", defaultQueueTTL, "how long a queued envelope waits for its recipient")
	allowV1 := fs.Bool("allow-pairing-v1", false, "accept v1 pairing frames (relay-generated 10-character codes); default on for a relay that is not public, off for a public one")
	var tf transportFlags
	tf.register(fs)
	var lf limitFlags
	lf.register(fs)
	metricsListen := fs.String("metrics-listen", "", "address for the operator metrics listener (Prometheus text on /metrics); empty disables it. Never the same listener as --listen")
	securityJournal := fs.String("security-journal", "", "append-only file of content-free security events, for a later --replay-journal restore")
	accountsMode := fs.String("accounts", relay.AccountsOff, "require a bound account to use the relay: off, github, email or both (Docs/protocol/accounts.md)")
	hooks := registerTestHooks(fs)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(stdout, "%s\n\nUsage:\n  %s [--listen HOST:PORT] [--allow-non-loopback] [--tls-cert FILE --tls-key FILE | --acme-domain NAME | --behind-proxy --client-ip-header NAME --trusted-proxy CIDR...] [--public-origin URL]... [--allow-auth-v1] [--db PATH] [--queue-ttl DURATION] [--allow-pairing-v1[=false]] [--accounts off|github|email|both] [--metrics-listen HOST:PORT] [--security-journal PATH] [abuse limit flags, below] [--verbose] [--version]\n  %s version [--json]\n  %s backup --db PATH --out FILE\n  %s restore --from FILE --db PATH [--force] [--replay-journal PATH]\n  %s admin account|group ... (%s admin for details)\n\nDaemons connect to ws://HOST:PORT%s (wss:// with TLS); %s is the unauthenticated health check.\nThe relay never reads or logs envelope payloads.\n\nFlags:\n", summary, name, name, name, name, name, name, envelope.ConnectPath, relay.HealthPath)
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

	loopErr := requireLoopback(*listen)
	if loopErr != nil && !*allowPublic {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, loopErr)
		return 2
	}
	public, err := tf.validate(loopErr == nil)
	if err == nil {
		err = lf.validate(tf.behindProxy)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 2
	}
	v1 := *allowV1
	explicit := false
	fs.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "allow-pairing-v1" })
	if !explicit {
		v1 = !public // review 50 H1: "public", not the listen address
	}
	switch *accountsMode {
	case relay.AccountsOff, relay.AccountsGitHub, relay.AccountsEmail, relay.AccountsBoth:
	default:
		_, _ = fmt.Fprintf(stderr, "%s: --accounts must be off, github, email or both\n", name)
		return 2
	}
	if *queueTTL <= 0 {
		_, _ = fmt.Fprintf(stderr, "%s: --queue-ttl must be positive\n", name)
		return 2
	}
	dbPath := *db
	if dbPath == "" {
		dbPath = *queueDB
	}
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
	tlsConf, err := tf.tlsConfig(filepath.Dir(dbPath))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	origins := tf.origins
	if len(origins) == 0 { // not public: the loopback names of the listen port
		origins = loopbackOrigins(ln.Addr())
	}
	if tlsConf != nil {
		ln = tls.NewListener(ln, tlsConf)
	}

	var metricsLn net.Listener
	if *metricsListen != "" {
		metricsLn, err = net.Listen("tcp", *metricsListen)
		if err != nil {
			_ = ln.Close()
			_, _ = fmt.Fprintf(stderr, "%s: --metrics-listen: %v\n", name, err)
			return 1
		}
	}

	var journal *relay.JournalWriter
	var journalFile *os.File
	if *securityJournal != "" {
		journalFile, err = os.OpenFile(*securityJournal, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			_ = ln.Close()
			if metricsLn != nil {
				_ = metricsLn.Close()
			}
			_, _ = fmt.Fprintf(stderr, "%s: --security-journal: %v\n", name, err)
			return 1
		}
		defer func() { _ = journalFile.Close() }()
		journal = relay.NewJournalWriter(journalFile)
	}

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	opts := relay.Options{Logger: logger, QueuePath: dbPath, QueueTTL: *queueTTL, AllowPairingV1: v1,
		Public: public, AllowAuthV1: tf.allowAuthV1, Origins: origins, Journal: journal,
		Accounts: *accountsMode, LoginURL: loginURL(origins)}
	lf.apply(&opts)
	rs, err := relay.Open(opts)
	if err != nil {
		_ = ln.Close()
		if metricsLn != nil {
			_ = metricsLn.Close()
		}
		_, _ = fmt.Fprintf(stderr, "%s: cannot open offline queue %s: %v\n", name, dbPath, err)
		return 1
	}
	srv := newHTTPServer(hooks.wrap(rs, rs, stderr))
	_, _ = fmt.Fprintf(stderr, "%s listening on %s; Ctrl+C to stop\n", name, ln.Addr())
	_, _ = fmt.Fprintf(stderr, "public: %s; origins: %s; accounts: %s\n", yesNo(public), strings.Join(origins, " "), *accountsMode)
	if public && tf.allowAuthV1 {
		_, _ = fmt.Fprintf(stderr, "%s: warning: --allow-auth-v1: this public relay accepts relay auth v1, which does not name the relay; remove it once every daemon is updated\n", name)
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	var metricsSrv *http.Server
	if metricsLn != nil {
		// A separate listener: /metrics is never reachable through ln, the
		// public listener (Docs/protocol/relay-hosted.md §5 acceptance item).
		metricsSrv = &http.Server{Handler: metricsHandler(rs), ReadHeaderTimeout: 10 * time.Second}
		_, _ = fmt.Fprintf(stderr, "%s metrics listening on %s\n", name, metricsLn.Addr())
		go func() { errc <- metricsSrv.Serve(metricsLn) }()
	}
	select {
	case err := <-errc:
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownWindow)
	defer cancel()
	_ = srv.Shutdown(sctx)
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(sctx)
	}
	rs.Close() // hijacked WebSocket connections are not covered by Shutdown; this is the SIGTERM drain
	return 0
}

// metricsHandler serves Prometheus text format on /metrics only, for the
// operator listener bound by --metrics-listen (Docs/protocol/relay-hosted.md
// §5). No per-key or per-account labels.
func metricsHandler(rs *relay.Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		st, err := rs.Stats()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(w, "# HELP relay_connections Authenticated connections currently open.\n# TYPE relay_connections gauge\nrelay_connections %d\n", st.Connections)
		_, _ = fmt.Fprintf(w, "# HELP relay_queue_rows Envelopes waiting in the offline queue.\n# TYPE relay_queue_rows gauge\nrelay_queue_rows %d\n", st.QueueRows)
		_, _ = fmt.Fprintf(w, "# HELP relay_queue_bytes Bytes waiting in the offline queue.\n# TYPE relay_queue_bytes gauge\nrelay_queue_bytes %d\n", st.QueueBytes)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	return mux
}

// runBackup implements `relay backup --db PATH --out FILE`.
func runBackup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name+" backup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "SQLite file holding the relay database to back up (required)")
	out := fs.String("out", "", "path to write the backup file to (required; must not already exist)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *dbPath == "" || *out == "" {
		_, _ = fmt.Fprintf(stderr, "%s backup: --db and --out are required\n", name)
		return 2
	}
	if err := relay.Backup(*dbPath, *out); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s backup: %v\n", name, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "backup written to %s\n", *out)
	return 0
}

// runRestore implements `relay restore --from FILE --db PATH [--force] [--replay-journal PATH]`.
func runRestore(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name+" restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "backup file to restore from (required)")
	dbPath := fs.String("db", "", "SQLite file to restore into (required)")
	force := fs.Bool("force", false, "overwrite a non-empty existing database at --db")
	replayJournal := fs.String("replay-journal", "", "security journal file whose entries after --from's backup time are replayed into the restored database")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *from == "" || *dbPath == "" {
		_, _ = fmt.Fprintf(stderr, "%s restore: --from and --db are required\n", name)
		return 2
	}
	if err := relay.Restore(*from, *dbPath, *force); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s restore: %v\n", name, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "restored %s from %s\n", *dbPath, *from)
	if *replayJournal != "" {
		info, err := os.Stat(*from)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s restore: --replay-journal: stat %s: %v\n", name, *from, err)
			return 1
		}
		applied, skipped, err := relay.ReplayJournal(*dbPath, *replayJournal, info.ModTime())
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s restore: --replay-journal: %v\n", name, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "replayed %d journal entries (%d skipped, no handler yet)\n", applied, skipped)
	}
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
	return fmt.Errorf("--listen %q is not a loopback address; use 127.0.0.1, or pass --allow-non-loopback with TLS (--tls-cert/--tls-key, --acme-domain or --behind-proxy)", addr)
}

// loginURL is the fixed login page bind_pending names: the first origin's
// https:// (http:// for ws://) address plus /login.
func loginURL(origins []string) string {
	if len(origins) == 0 {
		return ""
	}
	return relay.LoginURLFromOrigin(origins[0])
}
