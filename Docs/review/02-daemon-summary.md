# Daemon/CLI/Service Code Review — Phase 0

This document summarizes the daemon, CLI, relay, and service lifecycle code in AgentNet as of Phase 0. Each section documents purpose, exports, commands, database schema, IPC methods, audit events, known limitations, and test coverage.

---

## cmd/agentnet

**Purpose:** CLI tool for interacting with the local agentnetd daemon; implements user-facing commands for identity, pairing, peer listing, and encrypted messaging.

**Exported Types/Functions:** None (package main).

**CLI Commands:**
- `agentnet status [--json]` — Check daemon running status, PID, uptime, version. Exit codes: 0 (running), 1 (error), 2 (usage), 3 (daemon not found).
- `agentnet identity [--json]` — Print this agent's signed Agent Card with name, public key, harness, skills. Private key stays in daemon.
- `agentnet pair --new [--json]` — Issue a one-time pairing code (valid 10 minutes).
- `agentnet pair <code> [--json]` — Redeem a pairing code from another machine.
- `agentnet pair --status <id> [--json]` — Poll a pairing that was still pending.
- `agentnet peers [--json]` — List paired agents with their names, harness, skills, paired timestamp.
- `agentnet ping @peer [--json]` — Send end-to-end encrypted ping through relay; reports round-trip time.
- `agentnet ping --status <id> [--json]` — Poll a ping still in flight.

**IPC Methods Called:** status, identity, pair_new, pair_redeem, pair_status, peers, ping, ping_status (all served by daemon).

**Timeouts:**
- statusTimeout = 1500ms for status/identity/peers commands (cmd/agentnet/main.go:31)
- pairTimeout = 1900ms for pair/ping commands (cmd/agentnet/pair.go:20, ping.go:79)

**JSON Output:** Each command with --json flag outputs {"ok":true/false, ...} with machine-readable result or {"error":{"code","message"}}.

**Tests (cmd/agentnet):**
- identity_e2e_test.go — end-to-end identity card fetch
- pair_test.go — pair_new, pair_redeem, pair_status workflows
- ping_test.go — ping, ping_status workflows with encrypted session
- e2e_test.go — end-to-end CLI scenarios
- main_test.go — CLI argument parsing

**Untested:** CLI error output formatting (plain text vs JSON branches in each command).

---

## cmd/agentnetd

**Purpose:** Daemon entry point and service lifecycle (install/uninstall). Parses flags, creates config dir, opens database, listens on IPC socket, and runs the daemon.

**Exported Types/Functions:** None (package main).

**CLI Commands:**
- `agentnetd [run] [--home DIR] [--relay URL] [--version]` — Start the daemon. Connects to relay if --relay is set or DORYLINAE_RELAY_URL env var is present.
- `agentnetd install [--home DIR] [--dry-run]` — Register daemon as per-user service (launchd/systemd/Task Scheduler).
- `agentnetd uninstall [--home DIR] [--dry-run]` — Unregister daemon service.

**Flags:**
- --home: config directory (default: $DORYLINAE_HOME or user config dir)
- --relay: WebSocket URL to relay, e.g. ws://127.0.0.1:8787 (default: $DORYLINAE_RELAY_URL; empty = no relay)
- --dry-run: for install/uninstall, print what would happen without changing system

**Relay Environment:**
- RelayEnv = "DORYLINAE_RELAY_URL" (cmd/agentnetd/main.go:23)

**Tests (cmd/agentnetd):**
- install_test.go — service install/uninstall plan generation, audit recording

**Untested:** --relay flag parsing and relay connection; --version flag.

---

## cmd/relay

**Purpose (Entrypoint Only):** WebSocket relay for forwarding encrypted traffic between daemons. Phase 0 has no TLS; plaintext relay is loopback-only by default.

**CLI Commands:**
- `relay [--listen HOST:PORT] [--allow-non-loopback] [--verbose] [--version]` — Start the relay. Listens on 127.0.0.1:8787 by default.

**Flags:**
- --listen: address and port (default: 127.0.0.1:8787; port 0 picks a free port)
- --allow-non-loopback: permit non-loopback addresses (warning: no TLS in Phase 0)
- --verbose: log routed envelope metadata (never payloads)
- --version: print version

**IPC Path:** envelope.ConnectPath (defined in envelope package, not in scope)

**Security Note:** Relay validates only that connections are loopback; Phase 0 has no authentication or encryption at the relay level. Envelopes inside are encrypted end-to-end by daemons.

**Tests:** main_test.go

**Untested:** --allow-non-loopback flag, verbose logging, --version flag.

---

## internal/daemon

**Purpose:** Daemon lifecycle: open store, serve IPC socket, manage pairing and ping sessions, connect to relay, record audit log events.

**Exported Types:**
- StatusResult — {PID, StartedAt, UptimeSeconds, Version}
- IdentityResult — {Card, Signature, KeyBackend}
- PairStatus — alias to peers.Status
- PeersResult — {Peers: []peers.Peer}
- PingStatus — alias to session.PingStatus
- PairRedeemParams, PairStatusParams, PingParams, PingStatusParams
- Options — daemon configuration (Keystore, Identity, RelayURL, Logger)

**Exported Functions:**
- Run(ctx, paths, ready) — Run daemon with default options
- RunWithOptions(ctx, paths, ready, opts) — Run daemon with custom options

**IPC Methods Registered:**
- status — StatusResult (daemon/daemon.go:143)
- identity — IdentityResult (daemon/daemon.go:139)
- pair_new — PairStatus (daemon/pairing.go:41)
- pair_redeem — PairStatus (daemon/pairing.go:48)
- pair_status — PairStatus (daemon/pairing.go:59)
- peers — PeersResult (daemon/pairing.go:70)
- ping — PingStatus (daemon/ping.go:74)
- ping_status — PingStatus (daemon/ping.go not fully read, but referenced in ping.go)

**IPC Error Codes:**
- no_relay — daemon not configured with --relay (daemon/pairing.go:84)
- relay_unavailable — daemon not connected to relay (daemon/pairing.go:86)
- bad_code — invalid pairing code format (daemon/pairing.go:88)
- unknown_pairing — pairing_id not found (daemon/pairing.go:90)
- too_many_pairings — too many pairings in flight (daemon/pairing.go:92)
- unknown_peer — peer name/key not paired (daemon/ping.go, not read)
- ambiguous_peer — multiple peers match name (daemon/ping.go, not read)
- unknown_ping — ping_id not found (daemon/ping.go, not read)
- too_many_pings — too many pings in flight (daemon/ping.go, not read)

**Audit Events Recorded:**
- daemon.start — detail: {PID, Version} (daemon/daemon.go:90)
- daemon.stop — detail: {PID, Version} (daemon/daemon.go:98)
- identity.create — detail: from identity.LoadOrCreate (daemon/daemon.go:176)
- service.install — detail: {Platform, Executable, Home, Changed} (cmd/agentnetd/install.go:124)
- service.uninstall — detail: {Platform, Home, Changed} (cmd/agentnetd/install.go:122)

**Relay Connection:**
- Managed by startRelay() (daemon/daemon.go:185) with relayclient.New()
- Sets daemon as handler for control frames and envelopes from relay
- Runs in background, cancellable via context

**Database:** Opened via store.Open(ctx, p.DB) with migrations applied; store.DB() exposes *sql.DB to audit package.

**Tests (internal/daemon):**
- daemon_test.go — lifecycle (start, second instance fails, status IPC call, shutdown)
- relay_test.go — relay connection and offline queueing (not in scope detail)

**Untested:** pairing workflows, ping workflows, identity creation, relay connection errors, relay reconnection.

---

## internal/ipc

**Purpose:** Local socket API between CLI and daemon. Unix domain socket (Linux/macOS) or named pipe (Windows). JSON-encoded request/response with line framing.

**Exported Types:**
- Request — {ID, Method, Params}
- Response — {ID, OK, Result, Error}
- Error — {Code, Message}
- HandlerFunc — (ctx, params) → (result, error)
- Server — dispatches requests to registered handlers
- ErrNotRunning — constant sentinel for daemon not listening

**Error Codes:**
- bad_request (ipc.CodeBadRequest)
- unknown_method (ipc.CodeUnknownMethod)
- internal (ipc.CodeInternal)

**Exported Functions:**
- NewServer() → *Server
- (s *Server) Handle(method, HandlerFunc)
- (s *Server) Serve(ctx, listener) error
- Call(ctx, endpoint, method, params, result) error — dial, send one request, decode response
- Dial(ctx, endpoint) — returns net.Conn to IPC endpoint (platform-specific; internal)
- Listen(endpoint) — returns net.Listener on IPC endpoint (platform-specific; internal)

**Protocol:**
- Framing: newline-terminated lines, up to 1 MiB per line (ipc/ipc.go:27–28)
- JSON request on client side, JSON response on server side
- Idle timeout: 30 seconds per connection (ipc/ipc.go:28)
- Server handles multiple concurrent connections with buffered I/O

**Error Handling:**
- Requests with non-*Error errors are reported as CodeInternal without detail (ipc.go:156–160)
- Connection errors during Serve are logged and stop accepting new connections

**Tests (internal/ipc):**
- ipc_test.go — roundtrip, error codes, timeout, line length limit, endpoint not found

**Untested:** concurrent requests, large payloads near 1 MiB limit, idle timeout behavior.

---

## internal/store

**Purpose:** SQLite persistence layer with schema migrations. All migrations are immutable after apply; append-only design.

**Exported Types:**
- Store — wrapper around *sql.DB with migration tracking
- migration — {version, name, sql} (internal)

**Exported Functions:**
- Open(ctx, path) → (*Store, error) — Open or create database, apply pending migrations
- (s *Store) DB() → *sql.DB — Access raw database handle
- (s *Store) Close() error

**Schema Migrations:**
1. audit_events — table id, ts, actor, action, detail (JSON); append-only with triggers; indexed on ts (internal/store/store.go:20–34)
2. peers — table public_key (primary), name, harness, skills (JSON), card (JSON), paired_at (internal/store/store.go:35–44)

**Database Configuration:**
- DSN: SQLite3 pure-Go driver (modernc.org/sqlite)
- pragmas: busy_timeout(5000ms), journal_mode(WAL), foreign_keys(1)
- MaxOpenConns: 1 (single writer for simple access pattern; internal/store/store.go:60)

**Migration Table:**
- Tracks version, name, applied_at for each migration
- Rejects databases with schema newer than binary (internal/store/store.go:87–88)

**Tests (internal/store):**
- store_test.go — migrations applied once, idempotent; schema version forward-compatibility check

**Untested:** concurrent access under heavy load, WAL mode edge cases, foreign key constraint enforcement.

---

## internal/audit

**Purpose:** Append-only audit log of daemon actions. Rows are immutable by database trigger.

**Exported Types:**
- Event — {ID, TS, Actor, Action, Detail}
- Log — reader/writer for audit log

**Audit Actors:**
- ActorDaemon = "daemon"
- ActorCLI = "cli"

**Audit Actions:**
- ActionDaemonStart = "daemon.start"
- ActionDaemonStop = "daemon.stop"
- ActionServiceInstall = "service.install"
- ActionServiceUninstall = "service.uninstall"
- identity.create (from identity package)

**Exported Functions:**
- New(db) → *Log
- (l *Log) Append(ctx, actor, action, detail) error — Marshal detail to JSON, insert row
- (l *Log) List(ctx) → ([]Event, error) — All events in insertion order

**Detail Field:** JSON-marshalled per action; nil becomes "{}". Custom details per action (e.g., daemon.start detail has {PID, Version}).

**Database:** Insertures via audit_events table created by store migrations. Append-only by trigger.

**Tests (internal/audit):**
- audit_test.go — append, list, JSON marshalling

**Untested:** audit after failed append, detail parsing/validation, trigger enforcement.

---

## internal/config

**Purpose:** Placeholder package for per-user configuration loading. No behavior yet (ticket not yet implemented).

**Exported Types/Functions:** None (doc.go only).

**Status:** Future work.

---

## internal/paths

**Purpose:** Locate per-user config directory and derive file/endpoint paths. Cross-platform: respects $DORYLINAE_HOME, user config dir, Unix socket vs Windows named pipe.

**Exported Types:**
- Paths — {Dir, DB, Endpoint}

**Exported Constants:**
- HomeEnv = "DORYLINAE_HOME"

**Exported Functions:**
- Default() → (Paths, error) — Resolve from $DORYLINAE_HOME or OS user config dir
- In(dir) → (Paths, error) — Resolve paths for explicit directory
- (p Paths) Ensure() error — Create dir with owner-only permissions (0o700)

**Path Derivation:**
- Dir: absolute path to config directory
- DB: {Dir}/dorylinae.db
- Endpoint:
  - Windows: `\\.\pipe\dorylinae-<8-byte-sha256-hash-of-dir>` (internal/paths/paths.go:68–69)
  - Unix: {Dir}/agentnetd.sock

**Security:** Directory created with mode 0o700 (owner-only) on Unix; Windows directory access inherits user ACL.

**Tests (internal/paths):**
- paths_test.go (not read in detail)

**Untested:** Ensure on existing directory with wrong permissions, symlink resolution edge cases.

---

## internal/service

**Purpose:** Register agentnetd as per-user service (launchd on macOS, systemd on Linux, Task Scheduler on Windows). Pure functions from (Spec, Env) to Plans. Nothing touches the machine until Plan.Apply.

**Exported Types:**
- Spec — {Executable, Home}
- Env — {HomeDir, ConfigHome, UID, User}
- Platform interface — Install(Spec, Env) → Plan; Uninstall(Spec, Env) → Plan
- Plan — {Platform, Steps}
- Step — {Op, Path, Content, Mode, UTF16, Args, Ignore, Probe, Cleanup}
- Op — OpWrite, OpRun, OpRemove (enum)
- Result — {Changed}
- Runner interface — Run(ctx, []string) → ([]byte, error)
- ExecRunner — implements Runner with os/exec

**Exported Functions:**
- (p Plan) Apply(ctx, Runner) → (Result, error) — Execute plan; first failure stops, cleanup removals still run
- (p Plan) Describe(w) — Dry-run output for human inspection
- Current() → (Platform, error) — Platform-specific backend (platform-dependent, internal)
- DefaultEnv() → (Env, error) — Current user environment (platform-dependent, internal)

**Platforms (not in scope detail, but referenced):**
- Launchd (macOS) — launchd.go
- Systemd (Linux) — systemd.go
- Task Scheduler (Windows) — schtasks.go
- Unsupported other OSes — current_other.go

**Step Operations:**
- OpWrite: write Content to Path with Mode; UTF16 encodes as UTF-16LE with BOM (for Windows scripts)
- OpRun: execute Args; Probe=true marks "not installed" on failure; Ignore=true tolerates failure
- OpRemove: delete Path; Cleanup=true runs even after earlier failure

**Tests (internal/service):**
- service_test.go — launchd plist generation, systemd unit generation, Task Scheduler XML generation

**Untested:** plan execution on real systems, error handling during Apply, file permission edge cases.

---

## internal/version

**Purpose:** Build metadata and shared --help/--version handling for all binaries.

**Exported Variables:**
- Version = "0.0.0-dev" (overrideable at build time via -ldflags)

**Exported Functions:**
- String(name) → "<name> <version>"
- Main(name, summary, args, stdout, stderr) → exit code — Parse --help/--version, print usage or version

**Hardcoded Defaults:**
- Version string defaults to "0.0.0-dev" for local builds (internal/version/version.go:14)

**Tests (internal/version):**
- version_test.go (not read in detail)

---

## Summary of Known Limitations and TODOs

### No Explicit TODOs/FIXMEs in Scope:
- audit: Comment mentions ticket 3.6 for hash-chaining (internal/audit/audit.go:5)

### Panics (All in relay, not in scope):
- internal/relay/relay.go:83, relay_test.go:472, queue_test.go:363

### Intentional Error Ignores (Defensive):
- Close() errors ignored throughout (IPC, daemon, audit, store) — standard Go pattern
- Shutdown timeouts and context cancellation errors ignored — expected during graceful shutdown

### Untested Scenarios:
1. **CLI:** JSON/plain-text error output branches; all flag combinations
2. **Daemon:** Relay connection failures/reconnection; pairing race conditions; multiple concurrent pairings; session timeout
3. **IPC:** Concurrent requests; large payloads; connection idle timeout
4. **Service:** Real system integration (launchd/systemd/Task Scheduler); permission errors during install
5. **Database:** Concurrent access patterns under load; WAL mode edge cases; migration forward-compatibility on downgrade
6. **Audit:** Append failures; detail validation; trigger enforcement

### Phase 0 Security Notes:
- Relay has no TLS; enforced loopback-only by default (cmd/relay/main.go:64–115)
- Pairing uses relay for code exchange; code is 10-char alphanumeric, single-use
- Ping messages encrypted end-to-end via Noise protocol inside relay envelopes
- Private keys never leave daemon; stored via keystore package (env-configurable backend: file or system keychain)

---

## Test Coverage Summary

| Package | Test Files | Coverage Notes |
|---------|-----------|-----------------|
| cmd/agentnet | identity_e2e_test.go, pair_test.go, ping_test.go, e2e_test.go, main_test.go | Full e2e workflows; error branches not tested |
| cmd/agentnetd | install_test.go | Service install/uninstall plans; audit recording; no real systemd/launchd/Task Scheduler |
| cmd/relay | main_test.go | Listener and shutdown; --version and verbose flags untested |
| internal/daemon | daemon_test.go, relay_test.go | Lifecycle and IPC; pairing/ping workflows untested; relay reconnection untested |
| internal/ipc | ipc_test.go | Roundtrip, errors, timeout, line length; concurrent requests untested |
| internal/store | store_test.go | Migration idempotency; schema forward-compatibility; concurrency untested |
| internal/audit | audit_test.go | Append, list, JSON marshal; detail validation untested |
| internal/service | service_test.go | Platform-specific plan generation; no real Apply tested |
| internal/paths | paths_test.go | (not read in detail) |
| internal/version | version_test.go | (not read in detail) |
