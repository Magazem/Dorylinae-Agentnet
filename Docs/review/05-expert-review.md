# 05 – Expert review: Phase 0 as built vs plan

Date: 2026-09-21. Reviewer: Expert Reviewer (Dorylinae team). No source edited.
Inputs: `Docs/AgentNet Free Tier Build Plan.md`, `scope.md`, `Docs/review/01..04`, `.orchestrator/result.json`, and direct reading of the code cited below.

## 1. Executive summary

- **Phase:** end of Phase 0 (weeks 1–4). Tickets 0.1–0.7 are implemented. According to `.orchestrator/result.json`, commit `2078f1f` was pushed and tagged `phase-0`. Git was not run to check this, per the worker rules. Phases 1–4 are not started.
- **Health: good code, with gaps against the plan.** Build, vet and lint are clean. All tests pass, apart from one Windows temp-dir flake.
- **Crypto is sound.** Noise XX with the Ed25519-bound static key and prologue, the explicit-counter AEAD with anti-replay, strict signed Agent Cards, hashed single-use pairing codes, and a relay that only sees ciphertext were all verified in code and tests.
- **Pairing trusts the relay.** The relay issues the code and forwards both cards, and nothing is checked out of band, so a hostile or compromised relay can intercept (MITM) every pairing. That is acceptable for a local relay, but it must be fixed before a hosted relay.
- **0.7 deviates.** The relay binary queues only in memory (the SQLite queue exists but is not wired in). Offline delivery is proven only at the relay layer. Between daemons it does not survive a stopped peer, because Noise sessions live only in memory and handshakes time out after 10 s.
- **The Phase 0 exit rules are not fully met.** There is no evidence of the manual reboot or two-machine test, and no Phase 0 harness test. CI runs tests on Linux only, without `-race`.
- **Defects found:** 0 Critical, 3 High, 4 Medium, 7 Low.

## 2. Ticket-by-ticket status

Legend: **Done** = deliverable present and its acceptance test automated and passing. **Partial** = deliverable present but acceptance not fully shown. **Deviates** = built differently from the plan. **Not started**.

### Phase 0

| Ticket | Status | Evidence | Acceptance test met? |
|---|---|---|---|
| 0.1 Repo and skeleton | Done | `Makefile` (build/test/vet/lint), `.github/workflows/ci.yml` build matrix ubuntu/macos/windows plus cross job | Yes if CI ran on push (local `make build` works; the CI run itself was not inspected) |
| 0.1b Module path (not in plan table) | Done | `go.mod` module `github.com/Magazem/Dorylinae-Agentnet` | n/a (ticket missing from plan table, see §4) |
| 0.2(a) Daemon lifecycle | Partial | `internal/daemon/daemon.go`, `internal/store/store.go` (SQLite + migrations, append-only audit triggers), `internal/ipc/*` (unix socket / named pipe), `cmd/agentnetd/install.go`, `internal/service/{launchd,systemd,schtasks}.go` | PID and uptime: yes (`cmd/agentnet/main_test.go:60` `TestStatusRunning`, `cmd/agentnet/e2e_test.go:29`). "Survives reboot": **not shown**. This is manual only, and install plans are unit-tested but never applied (`internal/service/service_test.go`) |
| 0.3 Identity | Done | `internal/identity/identity.go`, `internal/keystore/{keychain,file,perm_windows,perm_unix}.go`, `internal/agentcard/*`, standalone `tools/verifycard/main.go` (stdlib only) | Yes (`cmd/agentnet/identity_e2e_test.go:52`). One Windows flake (L1) |
| 0.4 Relay minimum | Done | Signed-challenge auth `internal/relay/relay.go:206-240`, registry `relay.go:391-407`, forwarding by `to` with `from == authenticated key` enforced at `relay.go:293` | Yes (`internal/relay/relay_test.go:156` two daemons, `:196` byte-for-byte, `:415` no payload in logs) |
| 0.5a Pairing, relay half | Done | Codes stored only as a domain-prefixed SHA-256 (`internal/relay/pairing.go:74`), 10-min TTL (`:15`), consumed on redeem (`:229`), 5 fails/min/key (`:176`), max 5 outstanding (`:137`) | Yes (`internal/relay/pairing_test.go:112` single use, `:129` expiry, `:148` brute-force limit) |
| 0.5b Pairing, daemon half | Done (security caveat H1) | `internal/peers/pairing.go:325-327` verifies the card signature and that the card key equals the relay-authenticated key before `Store.Add` | Yes (`cmd/agentnet/pair_test.go:208`: both list peer; `:269` same code fails a second time, from two machines) |
| 0.6 Encrypted session | Done | `internal/noise/noise.go:29` (25519/ChaChaPoly/SHA256), `:131` `HandshakeXX`, `:124` prologue binds both identities, `:84-101` Ed25519 binding of the static key, `:235-244` counter replay rejection; `internal/session/session.go:423` AD binds from/to/sid/counter; unpaired senders rejected at `session.go:484-491` | Yes (`cmd/agentnet/ping_test.go:203`: round trip, in-transit payloads and relay log have no plaintext at `:238-260`, tampered byte rejected and audited at `:262`; `internal/session/session_test.go:166` tamper and replay) |
| 0.7 Offline queue | **Deviates** | SQLite queue with a 7-day TTL, per-recipient caps and ack/redeliver in `internal/relay/queue.go`. The binary does `relay.New(relay.Options{Logger: logger})` at `cmd/relay/main.go:81`, so `QueuePath` is empty and the queue is in memory | Relay layer only: `internal/relay/queue_test.go:105` (arrives once, in order), `:180` (survives restart **with a file DB, which the binary never uses**). **Not met end to end**: no daemon-level "stop B, send from A, start B" test, and by design it would fail (H3) |
| Phase-0 push ticket (scope.md) | Done, with deviation | `.orchestrator/result.json`: pushed `2078f1f` and annotated tag `phase-0`. The tree was dirty at start and 0.3–0.7 were committed as one commit. Known gaps were carried over | Build/vet/lint/test passed per result.json. Not independently checked (no git) |
| Demo script ("second ticket" per plan §10) | Not started | No demo script in repo | – |

### Phases 1–4

All **Not started**. No code, no protocol docs. `internal/capability`, `internal/protocol`, `internal/transport` and `internal/config` are empty doc-only placeholders, and `biscuit-go` is not in `go.mod`.

| Phase | Tickets | Status |
|---|---|---|
| 1 Presence, requests, inbox | 1.1 Teams, 1.2 Presence, 1.3 Visibility, 1.4 Request object, 1.5 Brief, 1.6 Inbox, 1.7 Urgency guards, 1.8 Notifications, 1.9 Offline handling | Not started. Blocked on Phase 0 harness test and the Phase 1 protocol docs |
| 2 Grants, consult, sessions | 2.1–2.7 | Not started |
| 3 Debate, decisions, audit | 3.1–3.7 (3.6 hash chain; `internal/audit/audit.go:5` notes it is deferred) | Not started |
| 4 Private beta ops | 4.1–4.8 | Not started |

## 3. Defects and risks (ranked)

### Critical
None.

### High

**H1 – Pairing can be intercepted by the relay.** `internal/relay/pairing.go:142-166` (relay generates the code), `:224` and `:234` (relay forwards the cards), `internal/peers/pairing.go:325-327` (daemon accepts any validly self-signed card whose key matches what the relay says). The relay knows the code and supplies both cards. A compromised or hostile relay can pair each side with its own key and then sit in the middle of every Noise session, because the Noise binding only proves the key the relay chose. `Docs/protocol/agent-card.md:86` says "Trust in a key comes from pairing", but pairing gives no independent proof. That undermines the plan's claim that the relay has "nothing readable inside envelopes" once the relay is hosted (4.1).
*Fix:* bind the code to the card exchange so the relay cannot know the secret. For example, the issuer daemon generates the code as `lookup-id || secret`, only the lookup part goes to the relay, and each side sends `HMAC(secret, own card)` (or runs a PAKE such as CPace/SPAKE2) and verifies the peer's MAC. A cheaper interim fix is to show a short fingerprint or SAS of both keys in `pair` / `peers` output for manual comparison. Either way, update `pairing.md` first.

**H2 – The relay binary drops the 7-day offline queue on restart.** `cmd/relay/main.go:81`: no `--queue-db` flag, so `QueuePath` is empty. Plan 0.7 says the relay "stores envelopes … up to 7 days". A relay restart silently loses everything queued. Also `relay.New` panics on a queue open error (`internal/relay/relay.go:78-86`).
*Fix:* add `--queue-db PATH` (default `<config dir>/relay-queue.db`), plus optional `--queue-ttl`. Build with `relay.Open` (which returns an error) instead of `New`. Add a `cmd/relay` test that restarts the binary and still delivers. Update `Docs/cli/relay.md`.

**H3 – Offline delivery does not work between daemons.** `internal/session/session.go:70` (`handshakeTTL = 10s`), `:771-782` (ping timeout drops the session), `Docs/protocol/session.md:137-138` (sessions live only in memory). When B is stopped: if A has no session, A's `session.init` is queued at the relay, but A drops its handshake after 10 s, so B's later `session.resp` is `unknown_session`. If A had a session, the queued `session.data` reaches a restarted B, which rejects it as `unknown_session`. So 0.7's acceptance ("Stop B, send from A, start B: the message arrives once") holds only for raw envelopes. It is the foundation for 1.9 (send to a stopped daemon returns `queued`) and every Phase 1 request.
*Fix (decide before 1.4/1.9):* either (a) persist Noise transport state per peer in SQLite, encrypted with a key held in the keystore, so sessions survive restarts, or (b) add a daemon-side outbox: the app message stays in the sender's daemon until a session with the peer is open and an app-level ack arrives, with handshakes allowed to wait as long as the queue TTL. Add a two-daemon test: stop B, send from A, start B, message processed exactly once.

### Medium

**M1 – The direct path delivers at most once.** `internal/relay/relay.go:300-306`: a frame handed to a live connection's `out` channel is not stored. If the socket dies before the write, the frame is lost, and the sender got no `queued` and no error. This is documented at `Docs/protocol/envelope.md:188-190`, but Phase 1 requests need at-least-once delivery.
*Fix:* always store, then forward. Delete on the recipient's `ack`. The client already dedupes (`internal/relayclient/seen.go`).

**M2 – The relay has no abuse controls beyond pairing.** Any keypair can authenticate (no allowlist). The pairing limiter is keyed by public key (`internal/relay/pairing.go:176`, `:253`), so fresh keys bypass it. Any stranger can fill a victim's 1000-envelope / 32 MiB queue (`internal/relay/queue.go:18-19`, `:103-108`), which denies the victim legitimate mail. There are no send-rate, connection-rate or total-capacity limits, and `--allow-non-loopback` exposes all of this over plaintext `ws://` (`cmd/relay/main.go:64`).
*Fix (before any non-loopback or hosted use):* per-IP connection and auth-failure limits, a per-sender-per-recipient queue quota, a global queue cap, TLS or a documented reverse proxy, and making `--allow-non-loopback` require TLS config.

**M3 – CI does not test the platforms that carry the risk.** `.github/workflows/ci.yml` runs `make test` on ubuntu only, with no `-race` and no `-count=1`. Windows-only code (DACL `internal/keystore/perm_windows.go`, named pipe `internal/ipc/transport_windows.go`, `internal/service/schtasks.go`) and macOS launchd/keychain are never tested. `-race` cannot run locally (no cgo), so the concurrency-heavy `session`/`relay`/`relayclient` code has never run under the race detector.
*Fix:* a test matrix on all three OSes, plus a `go test -race -count=1 ./...` job on ubuntu.

**M4 – The Phase 0 exit criteria are only partly evidenced.** Plan exit criteria need "you ran it by hand once" and "service survives reboot". Order rules say "never start a phase's first ticket before the previous phase's harness test passes". Nothing in the repo shows a manual two-machine run or reboot check, and there is no `tests/` folder or Phase 0 harness script.
*Fix:* see §5 steps 4–5.

### Low

- **L1 – Windows test flake.** `internal/identity/identity_test.go:147` `TestMissingCardIsRecreatedForSameKey`: `TempDir RemoveAll … directory is not empty` on the first run, 0/6 on reruns. It is likely AV or indexer holding the freshly written key/card file. *Fix:* retry cleanup in a test helper, or use `os.MkdirTemp` plus a best-effort remove on Windows. Confirm no file handle leaks in `keystore.WriteOwnerOnly` (`internal/keystore/file.go:54-80` closes on all paths).
- **L2 – Windows never re-checks key file permissions on read.** `internal/keystore/perm_windows.go:35-38`: `checkOwnerOnly` only checks that the file exists. Unix refuses group- or world-readable keys; Windows never re-verifies the DACL. *Fix:* call `OwnerOnly` in `Get` and refuse (or warn) like Unix.
- **L3 – Relay auth ignores the injected clock.** `internal/relay/relay.go:211` and `:228` use `time.Now()`/`time.Since` instead of `s.now`, so challenge expiry cannot be tested deterministically.
- **L4 – Queue dedupe ignores expiry.** `internal/relay/queue.go:94`: an expired but unswept row turns a resend into a no-op for up to one sweep interval. *Fix:* add `AND enqueued >= cutoff`, or delete the expired row inside the transaction.
- **L5 – A failed `fin` send leaves a half-open session.** `internal/session/session.go:577-579`: if sending `session.fin` fails, the initiator keeps a half-open "current" session and drops the flushed messages. Pings then time out after 10 s instead of failing fast. *Fix:* on a `fin` send error, `dropLocked` and `failPeerLocked(FailSend)`.
- **L6 – `Close` does not wait for connections.** `internal/relay/relay.go:173-174` kicks connections in goroutines and never waits. Shutdown can close the queue DB while drains still run.
- **L7 – Dead placeholder packages.** `internal/transport`, `internal/protocol`, `internal/capability` and `internal/config` are empty. `transport`'s stated purpose duplicates `session` + `relayclient`. Delete them, or re-scope them in the plan.

## 4. Doc / plan drift to reconcile

1. **The README is stale.** `README.md:8-9` says "Phase 0, ticket 0.1: a skeleton", and the layout table says `internal/` holds "placeholders for now".
2. **`Docs/cli/relay.md:7` is stale.** It says "no queue for offline peers, no persistence", but the queue exists. After H2 it must document `--queue-db`.
3. **`Docs/cli/ping.md` lists `peer_offline`.** result.json says the code never returns it.
4. **Ticket IDs outside the plan table.** 0.1b, 0.2a (no 0.2b), 0.5a/0.5b and the Phase-0 push ticket are not in the plan's Phase 0 table, which violates plan §8 ("never open a ticket not in a phase table without adding it to the table first").
5. **Service type differs.** The plan says "Windows service"; the code installs a per-user Task Scheduler logon task (`internal/service/schtasks.go:12`). That is a reasonable choice, but record it in the plan.
6. **0.7 wording vs reality.** The plan says 7-day storage; `Docs/protocol/envelope.md:191-193` documents in-memory as an option; the binary only offers in-memory (H2).
7. **Missing `tests/` folder.** Plan entry criteria allow "shell script in `tests/`" and 2.7 expects `tests/harness/`; neither exists.
8. **`Docs/` vs `docs/` casing** is still unresolved (README note).
9. **Decisions: `01-plan-checklist.md` overstates what is confirmed.** In the repo, only name (Dorylinae), language/stack and module path are confirmed. The licence is explicitly "not decided" (no LICENSE file). Relay hosting and account binding are not recorded anywhere.
10. **Summarizer error.** `02-daemon-summary.md:83` says "Phase 0 has no authentication … at the relay level". That is wrong: the relay authenticates by a signed challenge (`internal/relay/relay.go:206-240`).
11. **Pairing trust statement.** `Docs/protocol/agent-card.md:86` implies pairing establishes trust. It does not against a malicious relay (H1). Rewrite `pairing.md`'s threat model.

## 5. Recommended next steps (in order)

**A. Before Phase 0 can honestly be called done**
1. **H2:** add `--queue-db` to `cmd/relay` and use `relay.Open`, with a restart test. Update `Docs/cli/relay.md`.
2. **H3:** choose the session/offline design ((a) persisted sessions or (b) daemon outbox). Write it into `Docs/protocol/session.md`, implement it, and add the two-daemon "stop B, send, start B, arrives once" test.
3. **M3:** CI test matrix on linux/macos/windows plus a `-race` job. Fix the L1 flake so the Windows job is green.
4. **M4:** write `tests/phase0-manual.md` (or a script): two machines, pair, ping, stop/start B, reboot; `agentnet status` shows the new PID. Run it once and record the result.
5. **Docs:** fix README, `Docs/cli/relay.md` and `ping.md`, and add 0.1b/0.2a/0.5a/0.5b/push to the plan table.
6. **Re-tag** (`phase-0.1` or move to `phase-0` per owner) through a new push ticket once 1–5 pass.

**B. Security work to schedule (must land before 4.1 hosted relay, ideally before Phase 1 requests carry real content)**
7. **H1:** pairing bound to a code secret the relay never sees (MAC over cards, or PAKE), or at minimum a fingerprint/SAS display. Spec first in `pairing.md`.
8. **M1** store-then-forward with ack. **M2** relay abuse limits and TLS.

**C. First Phase 1 tickets (after A)**
9. Write the Phase 1 protocol docs first: `Docs/protocol/team.md`, `presence.md`, `request.md`, and `ipc.md` additions. The entry criteria forbid starting code before them.
10. **1.1 Teams**, then **1.2 Presence** (30 s heartbeat, 90 s offline), then **1.4 Request object**. 1.4 depends on the H3 decision and M1.
11. Add the Phase 1 "metrics from day one" audit events (accept/decline/defer/complete with timestamps) to the audit schema at 1.6.

**Open "Decisions to make now" the owner must answer**
- **D1 Licence:** Apache-2.0 for daemon/CLI; BSL or AGPL for the relay? README says undecided, and there is no LICENSE file.
- **D2 Session durability:** persist Noise sessions, or use a daemon outbox with re-handshake (H3)? This shapes 1.4/1.9.
- **D3 Pairing trust model:** accept a relay-trusted pairing for the beta, or require code-bound MAC/PAKE or SAS confirmation (H1)?
- **D4 Relay hosting (Fly.io vs Hetzner) and account binding (GitHub OAuth):** not recorded in the repo. Needed by 4.1/4.2, not urgent.
- **D5 Docs casing:** rename `Docs/` to `docs/`?
- **D6 Rhythm:** part-time at 10 h/week or a full-time block (plan §10). This affects the Gate 1 week-20 date.
