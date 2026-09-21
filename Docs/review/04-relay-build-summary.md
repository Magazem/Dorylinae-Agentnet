# 04 – Relay code summary and build verification

Facts only. Source was read, not edited. Go go1.27.1 windows/amd64, CGO_ENABLED=0 (no gcc on PATH).

## B. Verification results

| Command | Result |
|---|---|
| `go build ./...` | exit 0, no output |
| `go vet ./...` | exit 0, no output |
| `go test ./... -count=1` | exit 1: **1 package failed on first run** (see below); all others ok |
| `go test ./internal/identity -count=5` (rerun) | ok (passed 5/5) |
| `go test ./internal/identity -count=1` (rerun) | ok |
| `go test -race ./internal/relay` | not run: `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1` (no C toolchain) |
| `golangci-lint run ./...` (installed at ~/tools/bin) | `0 issues.` exit 0 |

Per package (`go test ./... -count=1`): ok – cmd/agentnet, cmd/agentnetd, cmd/relay, internal/agentcard, audit, daemon, envelope, ipc, keystore, noise, paths, peers, relay, relayclient, service, session, store, version, tools/verifycard. No test files – internal/capability, config, protocol, transport. **FAIL – internal/identity.**

Full failure text (first run only, not reproduced on 6 reruns):
```
--- FAIL: TestMissingCardIsRecreatedForSameKey (0.02s)
    testing.go:1617: TempDir RemoveAll cleanup: unlinkat C:\Users\ysuliman\AppData\Local\Temp\TestMissingCardIsRecreatedForSameKey668194232\001: The directory is not empty.
FAIL
FAIL	github.com/Magazem/Dorylinae-Agentnet/internal/identity	1.923s
```
It is a temp-dir cleanup error on Windows (file still open/locked at cleanup), outside relay scope; looks like a flake. Not investigated further.

Relay-scope coverage (`-cover`): internal/relay 90.5%, internal/relayclient 80.9%, cmd/relay 87.1%. 36 tests pass across these three packages.

## A. Summary

### Files (lines)
internal/relay: relay.go 412, pairing.go 278, queue.go 163, conn.go 95 (+ tests 286/366/475). internal/relayclient: relayclient.go 265, seen.go 37, signer.go 57, wire.go 43 (+ test 227). cmd/relay/main.go 116 (+ test 99).

### Purpose
- `relay`: WebSocket relay server. Authenticates daemons by signed challenge, keeps an in-memory registry `conns` keyed by wire public key, forwards envelopes by `to`, queues for offline/slow peers in SQLite, brokers pairing codes. Never decodes payloads; forwards frames byte for byte (relay.go:1-8).
- `relayclient`: daemon side; one persistent WS, challenge auth, reconnect with jittered exponential backoff, ack + dedupe of inbound envelopes.
- `cmd/relay`: HTTP server wrapper; flags `--listen` (default 127.0.0.1:8787), `--allow-non-loopback`, `--verbose`, `--version`. Refuses non-loopback listen without flag because there is no TLS (main.go:41,64,115). Graceful shutdown 5s then `rs.Close()` (main.go:96-99). Uses `relay.New` with zero Options, so the queue is **in-memory only** (main.go:81) – there is no flag to set `QueuePath`, TTL or limits.

### Exported API
relay: `Options{Logger, ChallengeTTL, SendQueue, Now, PairTTL, PairFailLimit, PairFailWindow, QueuePath, QueueTTL, QueueMaxEnvelopes, QueueMaxBytes, SweepInterval}`; `Server` with `New` (panics on queue open error, relay.go:78-86), `Open`, `ServeHTTP`, `Sweep`, `Queued(key)`, `Connected(key)`, `Close`, `PairStats()`; type `PairStats{Issued, Redeemed, Invalid, RateLimited}`.
relayclient: `Config{URL, Signer, OnEnvelope, OnError, OnQueued, OnControl, Logger, MinBackoff, MaxBackoff}`, `Client` (`New`, `Connected`, `Send`, `SendControl`, `Run`), `ErrNotConnected`, `Signer` interface, `NewKeySigner` (tests), `NewKeystoreSigner`.

### Protocol (server side)
1. Path must equal `envelope.ConnectPath` else 404 (relay.go:183).
2. Server sends `challenge` (random nonce, version, expiry). Client answers with signed `auth` as first text frame within ChallengeTTL (default 10s); `envelope.VerifyAuth` yields the pubkey (relay.go:206-240). Failure → `error auth_failed` + close StatusPolicyViolation. Read limit during auth `MaxAuthFrameBytes`, then `MaxFrameBytes`.
3. `register` connection (a second connection with the same key replaces the old; old kicked "replaced"), send `ready`, start writeLoop and initial queue drain.
4. Read loop: binary frames → kick. Text frames classified: control ops permitted after auth are `pair_new`, `pair_redeem`, `ack`; any other control → close (pairing.go:90-103). Envelopes: header parsed, `from` must equal authenticated key else `bad_sender` (relay.go:293); bad envelope → `bad_envelope` error (connection kept).
5. Routing: if recipient connected and not draining, `direct` send into its bounded `out` channel (64 frames default). If recipient offline, draining, or buffer full → `enqueue`, sender gets `queued{ref}`; `queue_full` or `internal` errors otherwise.
6. Client `ack{from, ref}` deletes the queued row where `to_key = <acking conn's key>` – only the recipient's own key can ack (pairing.go:97, queue.go:141).
7. Pairing: `pair_new` (card + ref) → `pair_code`; `pair_redeem` delivers `pair_peer` to both sides. Codes are stored only as SHA-256 with domain prefix (pairing.go:20,74); 10 min TTL; max 5 outstanding per issuer (`pair_limit`); cannot redeem own code; redemption is refused if issuer is offline or its buffer full without consuming the code (pairing.go:213-228).

### Offline queue (queue.go)
- Storage: SQLite via modernc.org/sqlite, table `queue(seq AUTOINCREMENT, to_key, from_key, id, enqueued ms, frame BLOB)`; unique index (to,from,id); indexes by recipient and by age. `QueuePath==""` → private in-memory DB. File mode: WAL, synchronous=FULL, busy_timeout 5s. Single connection (`SetMaxOpenConns(1)`).
- Limits (per recipient): 1000 envelopes and 32 MiB (`defaultQueueMaxEnvelope`, `defaultQueueMaxBytes`); counted over unexpired rows only (queue.go:103-108). Exceeding → `queue_full` to sender.
- TTL: 7 days default; expired rows are excluded from `next`/`count` and deleted by `sweep`, run every 1 min by `sweepLoop` and via `Server.Sweep()`.
- Dedupe: re-adding same (to, from, id) is a silent success. The dedupe lookup (queue.go:94) does not filter by expiry, so an expired-but-unswept row makes a resend a no-op until the sweeper removes it (max ≈ sweep interval).
- Sender auth: `from` in the header must match the authenticated key (relay.go:293). The relay does not verify signatures/encryption on the envelope itself; it treats payload as opaque.
- Delivery: `drainStep` reads batches of 64 with per-conn `cursor`; while `draining`, live traffic for that peer is also queued so order is preserved (conn.go:41-54, relay.go:360). Rows stay in DB until the recipient acks; unacked rows are redelivered on next connect; the client dedupes (seen ring of 8192 (from,id)).
- Frames delivered via `direct` (recipient online, not draining) are not stored; if the connection drops after the frame is placed in `out` but before write, nothing retains it (no ack tracking on the direct path). `route` logs "routed" once handed to `out`.

### Rate limiting / abuse controls
- Pair redemption failures: fixed-window limiter per key, 5 failures/min default; lockout returns `pair_rate_limited`; bucket map swept when ≥1024 entries (pairing.go:237-278). Keyed by public key, not IP.
- Pair codes: max 5 outstanding/issuer; TTL 10 min; card validated (`CheckCard`), ref ≤ `MaxRefLen`.
- Per-recipient queue caps (above). Per-connection send buffer 64 frames; write timeout 10s; read limit `MaxFrameBytes`.
- HTTP: `ReadHeaderTimeout` 10s only (main.go:84).
- **Not present in code:** no rate limit on envelope sends, on connection attempts/auth failures, per-sender queue quota, cap on total connections or total queue size across recipients, TLS, or IP-based limits. Any keypair can connect (no allowlist) and queue for any recipient key up to that recipient's caps.

### Stubs / TODO / panics / ignored errors / hardcoded values
- No TODO/FIXME/XXX in relay, relayclient, cmd/relay (grep).
- `panic(err)` in `relay.New` (relay.go:83) – documented; other panics only in test helpers (queue_test.go:363, relay_test.go:472).
- Ignored errors (all `_ =`): `ws.CloseNow`, `ws.Close` (relay.go:191,198,174, conn.go:68), `writeControl` on auth failure (relay.go:197), `s.Sweep()` result in loop (relay.go:136; failures are logged inside Sweep), `json.Marshal` in `control()` (conn.go:89, commented as infallible), `tx.Rollback` (queue.go:91), `rows.Close`, `srv.Shutdown` (main.go:98), `q.close()` (relay.go:177).
- `authenticate` uses `time.Now()` instead of injected `s.now` for challenge issue/expiry (relay.go:211,228).
- `Close` kicks connections in goroutines and does not wait for them (relay.go:173-175).
- Hardcoded: challenge TTL 10s, send queue 64, write timeout 10s (relay.go:26-28); queue defaults 7d/1000/32MiB/1m/batch 64/op timeout 10s (queue.go:17-22); pair TTL 10m, fail limit 5/1m, max outstanding 5, sweep threshold 1024 (pairing.go:15-19); `defaultListen` 127.0.0.1:8787, `shutdownWindow` 5s (main.go:26-27). Client: backoff 500ms–30s, handshake 15s, write 10s, seen capacity 8192.
- `ack` handler ignores `ctl.From`/`ctl.Ref` validity beyond the DB match (no length checks on these in `handleControl`, pairing.go:96-99).
- gosec suppression: `//nolint:gosec` on jitter (relayclient.go:264).
- Client `dispatch` drops malformed frames with a log line; `OnEnvelope` runs on the read loop (must not block).

### Tests
Relay (relay_test.go): two-daemon exchange, byte-for-byte forwarding, auth rejections, queued ack, client error frames, bad envelope/sender mismatch, control after auth closes, connection replacement, 404 path, logs contain no payload. Queue (queue_test.go): offline arrive once and in order, survives restart (file DB), expiry, reconnect mid-flush redelivers only unacked, backlog ordering vs live traffic, limits and duplicates, only recipient can ack, opaque payload. Pairing (pairing_test.go): issue/redeem, single use, expiry, brute-force limit, malformed code, issuer offline doesn't consume, bad requests, outstanding limit, other control frames close, logs hold no codes/cards. Client: URL/signer validation, send while disconnected, reconnect with backoff, keystore signer, duplicate deliveries acked but delivered once. cmd/relay: help/version, usage errors, serves until cancelled.
Untested/lightly tested (from reading test names and coverage; not a line-by-line coverage audit): no `-race` run possible here; no test named for `requireLoopback` beyond usage errors; no test for cmd/relay with `--allow-non-loopback`; no test for total-connection or per-sender abuse (features absent); limiter bucket sweep at 1024 entries; slow-consumer `directBusy` path and `peer_busy` pairing path have no dedicated test names; `Close` with connected peers; `NewKeystoreSigner` key-mismatch branch. Relay is only ever run with `QueuePath` set in tests, not via the binary.

### CI (.github/workflows/ci.yml)
Triggers: push to main, pull_request. Job `check` (ubuntu): `make vet`, `make test` (`go test ./...` — no `-race`, no `-count=1`), golangci-lint-action v8 with v2.13.2. Job `build` (needs check; ubuntu/macos/windows): `make build` (three binaries, trimpath, version ldflags) + `--version` smoke on each binary, upload artifacts. Job `cross` (ubuntu): `go build -trimpath ./cmd/...` for darwin/linux/windows × amd64/arm64 with CGO_ENABLED=0. Tests run only on ubuntu; no Windows/macOS test run, no `-race`, no coverage.

### Config files
- Makefile: targets build, test, vet, lint; binaries agentnet, agentnetd, relay; VERSION default 0.0.0-dev.
- .golangci.yml (v2): default standard + errcheck, govet, ineffassign, staticcheck, unused, misspell, revive, gosec, bodyclose, errorlint; formatters gofmt, goimports; timeout 5m.
- go.mod: module `github.com/Magazem/Dorylinae-Agentnet`, `go 1.27`; direct deps go-winio 0.6.2, coder/websocket 1.8.15, flynn/noise 1.1.0, zalando/go-keyring 0.2.8, x/sys 0.48.0, modernc.org/sqlite 1.59.0.
