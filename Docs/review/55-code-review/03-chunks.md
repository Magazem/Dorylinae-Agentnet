# 55 / 03: Chunk plan

Every production file at commit `6cc26a7` is in **exactly one** chunk (checked by script:
no file missing, none twice). "Lines" are `wc -l` physical lines, comments and blanks included.

| | Files | Lines |
|---|---|---|
| Go production (every non-`_test.go` file under `cmd/`, `internal/`, `tools/`, `tests/`) | 272 | **62,806** |
| Non-Go (scripts, workflows, deploy, packaging, build config, harness and smoke scripts) | 27 | **6,329** |
| **Total reviewed as chunks** | 299 | **69,135** |

**Reconciliation with the index** (`00-index.md`, CR-Index-Haiku): Go 62,726 + 80 = 62,806.
The index missed `internal/device/testdata/runnerhelper/main.go` (80 lines, a real
program the runner tests build and run; it is in C14). Non-Go 6,307 + 24 − 2 = 6,329: the
index missed `Makefile` (24; in C15) and counts `ci.yml` and `.golangci.yml` one line
higher. Also note that the index's §3.g is wrong: the daemon has migrations **1–21**
(`internal/store/store.go:21` is 1 = `audit_events`, `:522` is 21 = `experience_records`), and the relay has **R1–R2**
(`internal/relay/queue.go:114,128`).

**Not reviewed as chunks, and why:**
- `*_test.go` (58,331 lines): read as context by the chunk that owns the package. The two
  exceptions that ship or drive the release are in chunks: `tests/install/*.sh` (C15) and
  the harness stand-in and scripts (C30, C31). `internal/testutil` (C28) is test-only code
  but lives in the production tree, so it is reviewed.
- `go.mod`, `go.sum` (122 lines): given to C15 as context for the dependency questions.
- `Docs/**`, `README.md`, `SECURITY.md`, `scope.md`, `LICENSE`, `tests/*-manual.md`,
  `tests/harness/README.md`, `.claude/skills/**`, `.gitignore`: documentation. Specs are
  checked against the code by T6.
- Generated test data (`testdata/`, vectors, golden files): never hand-edited; checked by
  C29 and C25.

**Order = risk.** Dispatch in id order: internet-facing relay (C01–C04), crypto, identity,
pairing, mail and sessions (C05–C08), grants, approval, device and process execution
(C09–C14), installer, release, CI, deploy and local service (C15–C16), then the feature
layers (C17–C28), vectors (C29) and the test harness (C30–C31). **C03 has never been
security-reviewed** (4.2a accounts; R-4.2 is pending), and neither have 4.1a (backup,
journal, migrations: C02), `tools/binversion` (C15), 4.4c `doctor` (C28), 4.4d (C05/C16)
or 4.1p `deploy/early` (C15). Give those parts extra depth.

**Every chunk reviewer also** checks its package's tests for tests that cannot fail (a
tautology, a mocked-away invariant, a missing assert), and lists every "Assumption and
contract" (rubric §5.5) that crosses its boundary.

Specs are in `Docs/protocol/`, CLI docs in `Docs/cli/`, prior reviews in `Docs/review/`.

---

## C01 · Relay core: connections, auth, limits, ephemeral — 2,024 lines

**Files:** `internal/relay/relay.go` (977), `conn.go` (328), `auth.go` (98), `limits.go` (479),
`ephemeral.go` (98), `diskfree_other.go` (10), `diskfree_unix.go` (15), `diskfree_windows.go` (19).
**Specs:** envelope.md (auth, frames, forwarding, errors), relay-hosted.md §1–§2 (TLS, auth
v2, abuse limits), presence.md §Relay, pairing.md (relay side, limits).
**Prior reviews:** R04, R05 (M1, M2, L3, L6), R15, R50, R51, **R52** (H1 memory bound, M1).
**Entry points / trust boundaries:** `ServeHTTP` (`/v1/connect` WebSocket upgrade, `/healthz`),
`authenticate` (challenge → `auth` v1/v2), `route` (every frame from an authenticated key),
`directEphemeral`, per-prefix/per-key/relay-wide limiters, `clientAddr` (trusted proxy
header). Input: arbitrary bytes from anyone on the internet; unlimited keys.
**Questions:**
1. Before authentication, what can an unauthenticated client make the relay allocate, hold
   or compute (memory per connection, goroutines, timers, the SQLite connection)? Is every
   such cost bounded per prefix and relay-wide, including a client that never finishes a
   frame, never answers the challenge, or opens and closes in a loop?
2. Auth v1 and v2: can a signature for one relay, origin, challenge or time be replayed or
   forwarded to be accepted elsewhere? Is the no-downgrade rule (a public relay refuses v1)
   enforced on every path, including behind a proxy and on `--allow-non-loopback`?
3. After auth: can a key send as another key (`from` ≠ authenticated key on any frame type,
   including control ops and acks), or read, ack or delete another key's queued frames?
4. Do the R52 H1 fix and the in-flight byte budget hold on every read and write path
   (charge/uncharge on errors, panics, closes and the drain in `Close`)? Find any path that
   charges without uncharging or the reverse.
5. Replacement of an existing connection for the same key: any race that loses frames,
   double-delivers, leaks the old goroutines or lets the old connection keep sending?
6. Are the limiter maps bounded (eviction under Sybil load), and can the chosen prefix be
   spoofed when `--behind-proxy`/`--client-ip-header` is off or on?

## C02 · Relay persistence, pairing, backup, journal, binary — 1,828 lines

**Files:** `internal/relay/queue.go` (420), `pairing.go` (503), `backup.go` (105),
`journal.go` (126), `cmd/relay/main.go` (344), `transport.go` (150), `limits.go` (180).
**Specs:** envelope.md §Offline queue, pairing.md (relay side, `pair_*` frames, limits),
relay-hosted.md §1 (TLS, flags), §2 (caps), §3 (persistence, migrations, backup/restore,
security journal, drain), `Docs/cli/relay.md`, `Docs/ops/early-relay-deploy.md`.
**Prior reviews:** R05 (H2, L4), R08b, R15, R50 (M3, M6), R51 (L3, L4, L7), R52. **4.1a
(migrations, backup/restore, journal, SIGTERM drain, `--metrics-listen`) was never
security-reviewed.**
**Entry points / trust boundaries:** relay flags and env (operator), `pair_new/redeem/cancel`
frames (any key), queue enqueue/next/ack (any key, per recipient), `relay backup`/`restore`
subcommands (operator, files), journal file (append-only, off-host), metrics listener,
TLS config (`--tls-cert`, `--acme-domain`), SIGTERM.
**Questions:**
1. Queue: can one sender, or many fresh keys, exceed the per-sender, per-recipient and
   relay-wide caps, or make `SUM`/count bookkeeping drift from the table (restart, failed
   insert, expiry, ack of an unknown id)? Is disk exhaustion handled before SQLite fails?
2. Migrations R1/R2 and `restore`: atomic, idempotent on an old `queue.db`, safe against a
   crafted or truncated backup file (integrity check before use), and does `restore` refuse
   to clobber a live database? Any path traversal or symlink issue in `--db`, `backup`,
   `restore` or the journal path?
3. Pairing: lookup enumeration, card harvesting, the relay-wide code cap, v1 off on public
   relays (4.0c), the 3-redemption count vs D9: do they hold after the 4.0c/4.1a changes?
4. Journal: does anything that must be journaled (relay-hosted.md §3) skip it on an error,
   and can a journal line carry an email, a payload or a full key?
5. `cmd/relay`: are the safe-by-default rules right (non-loopback needs TLS; metrics only
   on loopback unless told otherwise; `/metrics` never on the public mux; HTTP timeouts on
   every server; SIGTERM drain bounded)? Do the flags in `deploy/early/agentnet-relay.service`
   parse and mean what the runbook says?

## C03 · Relay accounts (4.2a), admin CLI, test hooks — 1,661 lines · NEVER REVIEWED

**Files:** `internal/relay/accounts.go` (556), `accounts_store.go` (586), `admin.go` (128),
`internal/envelope/accounts.go` (60), `cmd/relay/admin.go` (227), `testhooks_on.go` (85),
`testhooks_off.go` (19).
**Specs:** accounts.md (all), relay-hosted.md §3 (journal), invites.md (quota group fields
only), envelope.md (`bind_*`, `unbind`, `account_changed` frames).
**Prior reviews:** R50 (H2, M5, M6, M7, L6, L15, L16) for the spec; **no code review exists.**
Read O-187 in 02: that is the 4.2a author's own must-check list; answer each item here.
**Entry points / trust boundaries:** `bind_start/poll/cancel`, `unbind` frames (any key),
`ConfirmBind` (Go API; the HTTP confirm page is 4.2b, not built), `watchAccounts`
(`PRAGMA data_version` polling), `accountsChanged` (closes live connections), bound-only
routing `eligible`, `relay admin account|group` (operator, same DB, concurrent with a live
relay), `testhooks` build tag (`/testhook/bind`).
**Questions:**
1. With `--accounts` on, can an unbound or suspended key send, receive or queue anything
   beyond what accounts.md allows (`pair.confirm` only, `bind_*`)? Check every routing path
   in `relay.go` that should call `eligible`, including queue drain on reconnect and
   ephemeral frames.
2. Bind flow: can a key be bound to an account it does not control (code guessing, code
   reuse, race between two `bind_start`, `already_bound` handling, a code confirmed after
   `bind_cancel`)? Are bind codes stored only hashed, with enough entropy and a TTL?
3. Revocation/suspension: after `relay admin` commits, is every live connection of that key
   closed within the watcher interval, including a connection that authenticated during the
   reload? What happens if the DB is unreachable (spec: fail closed)?
4. Is `ConfirmBind` or any bind path reachable in a release build without the browser step?
   Prove that `testhooks_on.go` cannot be compiled into release artefacts (build tags,
   `release.yml`, `TestReleaseBuildHasNoTestHooks`).
5. `relay admin`: output of display names/emails (terminal escapes), argument parsing
   (`parseInterspersed`, keys starting with `-`), and consistency when the relay and admin
   write concurrently.
6. Journal: every security event of accounts.md written; what happens when the DB change
   commits but the journal append fails (and the reverse)?

## C04 · Envelope wire format and relay client — 1,496 lines

**Files:** `internal/envelope/authv2.go` (185), `envelope.go` (160), `fingerprint.go` (87),
`frames.go` (246), `pairing.go` (124), `internal/relayclient/relayclient.go` (497),
`probe.go` (60), `seen.go` (37), `signer.go` (57), `wire.go` (43).
**Specs:** envelope.md, relay-hosted.md §1 (auth v2 origin form, URL rule, `--relay-ca`,
`min_client`), pairing.md (frames), mail.md §Receiving (seen-set bypass), presence.md §Relay.
**Prior reviews:** R07 (H2), R08 (L1, L8), R09 (L6), R15, R51 (M1, L1, L2, L6, L8), R52, R53
(`min_client`).
**Entry points / trust boundaries:** every frame from the (untrusted) relay: `Parse`,
`Classify`, `ParseHeader`, `ready` (features, `min_client`, account), `error`, `challenge`;
the client's dial (TLS roots, loopback-only dial, no redirects), the signer (identity key
via keystore), the seen-set, `Probe` (doctor).
**Questions:**
1. Parse every relay-supplied frame type: strictness (unknown members, duplicate keys,
   sizes, base64 strictness, key format), panics, and whether any relay-supplied string
   (error message, code, `min_client`, account display) reaches a log, the terminal or IPC
   without sanitising and bounding.
2. Auth v2: the signed bytes bind the relay origin exactly as the client dialled it; can a
   relay make the client compute a different origin (redirect, case, IDN, trailing dot, IPv6
   forms, default port)? Is the dial truly loopback-only when the URL is classified loopback
   (R51 M1 fix), including DNS rebinding?
3. Reconnect and backoff: can a hostile relay make the client spin (tight reconnect loop),
   leak goroutines, or grow memory (seen-set, pending sends, queued callbacks)?
4. Seen-set and dedupe: which types bypass it (mail, presence) and does anything else rely
   on it for security (replay) rather than convenience?
5. `Probe` never authenticates and cannot kick the daemon's connection; `signer` refuses a
   keystore seed that does not match the card.

## C05 · Identity, agent card, keystore, Noise wrapper, mailbox keys — 2,002 lines

**Files:** `internal/agentcard/agentcard.go` (215), `canonical.go` (191),
`internal/identity/identity.go` (207), `internal/keystore/file.go` (88), `keychain.go` (80),
`keystore.go` (106), `perm_unix.go` (24), `perm_windows.go` (74), `internal/noise/noise.go` (251),
`internal/mailbox/mailbox.go` (513), `tools/verifycard/main.go` (253).
**Specs:** agent-card.md, session.md (Noise binding), mail.md §Mailbox keys, §Lifecycle,
pairing.md §Domain separation.
**Prior reviews:** R03, R05 (L1, L2), R07, R08, R09 (L5, L6), R10 (rotation), 4.4d (keychain,
0600: not separately reviewed).
**Entry points / trust boundaries:** card parse/verify (peer and relay supplied cards),
canonical JSON (shared by mail, grants, decisions), keystore modes (`auto`/`keychain`/`file`,
`DORYLINAE_KEYSTORE`), file permissions (Unix mode, Windows DACL), Noise handshake inputs,
mailbox key rotation and deletion.
**Questions:**
1. Canonical JSON and strict parse: any input with two different byte forms that verify
   to the same signature, or one form that canonicalises differently here and in
   `tools/verifycard`/`verifyvectors` (numbers, escapes, UTF-16 ordering, duplicate keys,
   nesting depth)?
2. Keystore: are secrets written with owner-only permissions **before** the secret bytes
   (no window), atomically, on every OS; are permissions re-checked on read (see O-030);
   what does `auto` do when the keychain is locked or unavailable (silent fallback to file?)
   and does it ever log or return secret material in an error?
3. Noise: the static key is bound to the Ed25519 identity and both identities are in the
   prologue; is there any path that accepts a handshake whose static key is not the paired
   identity's, or reuses a nonce/counter?
4. Mailbox keys: is every private key recorded before it is announced, deleted on schedule
   (t + 21 d), never used after `not_after + 7 d`, and can a keystore read failure cause a
   silent rotation that orphans a key (R09 L5 follow-up)?
5. Identity: what happens on a missing/corrupt key or card (O-197), and is the seed ever
   exposed through the card, logs or IPC?

## C06 · Pairing v2 (daemon side), peers store, trust — 2,431 lines

**Files:** `internal/peers/pairing.go` (1090), `pairv2.go` (202), `store.go` (470),
`internal/daemon/pairing.go` (100), `trust.go` (128), `cmd/agentnet/pair.go` (256), `peers.go` (185).
**Specs:** pairing.md (all), team.md §Introduced peers, §Removing the owner, `Docs/cli/pair.md`,
`peers.md`.
**Prior reviews:** R06, R07, R09, R14, R16 (M3), R18, R33.
**Entry points / trust boundaries:** `pair_new`/`pair_redeem`/`pair_status` IPC (local agent),
`pair_code`/`pair_peer`/`error` frames and `pair.confirm` envelopes (relay), `peers_verify`,
`peers_remove`, trust ranks, `Introduce`/`GCIntroduced` (called from team).
**Questions:**
1. Can the relay make either side store a key without a verified tag, store a different key
   from the one the tag covers, or report "failed" while having stored a peer (R09 M1)?
   Trace every call to the store.
2. Timers, attempt counting and `checkMu`/`m.mu`: any race between confirm, timeout, relay
   error and cancel that breaks "nothing stored on the side that detects the failure"?
3. Is the code, lookup, secret, K or any tag ever written to a log, audit row, IPC error or
   status other than the pending issuer's `pair_status`? Are the `[]byte` secrets wiped on
   every exit path?
4. Trust never lowers; `introduced_by` invariants; `peers_remove` of a team owner cascades
   (R16 M3) and fails outbox rows atomically; `peers_verify` cannot be driven by a peer.
5. v1 pairing: only reachable with `--v1` and a 10-char code; a relay cannot downgrade.

## C07 · Sealed mail: seal/open, receiver, keys, outbox — 2,467 lines

**Files:** `internal/mail/announce.go` (80), `audit.go` (73), `body.go` (153), `keys.go` (242),
`mail.go` (251), `open.go` (292), `outbox.go` (594), `presence.go` (38), `receiver.go` (343),
`internal/daemon/mail.go` (288), `outbox.go` (113).
**Specs:** mail.md (all), envelope.md §Client behaviour, D2, D8, D10.
**Prior reviews:** R07, R08, R10, R13, R17 (openCore refactor), HO INV-4 / B-4.
**Entry points / trust boundaries:** every `mail` envelope from the relay (unauthenticated
until step 8), `keys` mail, key-miss triggers (unauthenticated), acks, `mail_submit` IPC
(local agent; kind allowlist), outbox worker, `OnReady`/`OnPeerOnline`.
**Questions:**
1. Verification order: before the signature is checked, what can an unauthenticated sender
   (the relay knows the mailbox pub) make the daemon do (CPU, memory, DB writes, audit rows,
   log lines, key-miss replies)? Is each bounded?
2. Exactly-once: find any path where a mail is acked without its effects committed, or its
   effects committed twice (concurrent `Handle`, restart between commit and ack, `ErrBadBody`
   re-marking, a kind's `After` failing).
3. Outbox state machine: every transition conditional, no resurrection of a final row, the
   INV-4 ready counter and B-4 fix intact; what remains of O-054/O-061?
4. `mail_submit`: can a local agent send any daemon-owned kind (grant, `ws.*`, `device.*`,
   `team.*`, `request.*`, `debate.*`, `keys`, `ack`) or a reserved prefix, bypassing its
   handler's checks and approvals?
5. Key rotation and key-miss: any downgrade to an older announcement, any loop or
   amplification, any re-seal of a row that became final (R10 M1)?

## C08 · Noise sessions, presence, idle, ping — 2,987 lines

**Files:** `internal/session/session.go` (929), `internal/presence/body.go` (249),
`presence.go` (40), `receive.go` (135), `seal.go` (33), `sender.go` (631), `settings.go` (112),
`store.go` (168), `internal/idle/idle.go` (91), `idle_darwin.go` (16), `idle_linux.go` (51),
`idle_other.go` (13), `idle_windows.go` (41), `internal/daemon/presence.go` (107), `ping.go` (145),
`cmd/agentnet/ping.go` (113), `presence.go` (113).
**Specs:** session.md, presence.md, grant.md §Transport (fetch runs over sessions),
`Docs/cli/ping.md`, `presence.md`.
**Prior reviews:** R03, R05 (H3, L5), R12 (M5–M8, L4, L10, L19), R15, R17.
**Entry points / trust boundaries:** `session.init/resp/data/fin` envelopes (relay-routed,
from paired and unpaired keys), counters and replay window, `presence` ephemeral envelopes,
the idle probe (`xprintidle`, OS APIs), `ping`/`presence_set` IPC.
**Questions:**
1. Session: are unpaired senders rejected before any expensive work; is every `session.data`
   bound to (from, to, sid, counter) in the AD; is replay and reordering rejected; can a
   relay force unbounded handshakes, sessions or buffered fragments per peer?
2. Concurrency in `session.Manager` (one worker under `m.mu`, fetch worker pool per R24 L14):
   deadlocks, a slow handler stalling pings, goroutine leaks on peer removal or shutdown.
3. Presence: freshness window, cross-boot rule, size handling before crypto (O-083),
   bounded drop limiter (R17 M2), no audit, no reply (no oracle); `interval` bounds.
4. Visibility modes (`invisible`, `away`) actually stop what the spec says they stop.
5. Idle: the external `xprintidle` call: resolved how (PATH?), bounded time, output parsed
   safely?

## C09 · Capability tokens, grant store, fs serving — 2,015 lines

**Files:** `internal/capability/doc.go` (7), `token.go` (380), `verify.go` (319), `fs.go` (399),
`fs_unix.go` (9), `fs_windows.go` (5), `store.go` (691), `internal/daemon/grant_kinds.go` (205).
**Specs:** grant.md (token, verification steps, paths, fs serving, sensitive grants,
revocation, threat model), work-session.md (quarantine rule, `QuarantineHolds`), OD-P2-1.
**Prior reviews:** R24, R25, R28, R34, R35, R39, R45 (L6).
**Entry points / trust boundaries:** `Verify` (grants from a peer's mail and every fetch
request), path grammar, `os.Root`-based fs `stat/list/read` serving files to a paired peer,
`grant`/`grant.revoke` mail kinds, grant store state machine.
**Questions:**
1. Path escape: can any grant path or fetch path reach outside the granted root (`..`,
   absolute, drive letters, UNC, `\\?\`, ADS `file:stream`, 8.3 short names, trailing dots
   and spaces, reserved names, case folding to `.git`, symlinks and junctions at any
   component, a root that is itself swapped)? Test it on Windows semantics too.
2. Verification: every grant.md step in order, fail closed on any error including a nil
   `SessionOpen` callback (R25 M1), canonical-token comparison (never raw bytes), `aud`
   bound to the Noise-authenticated identity.
3. Can a revoked, expired, session-closed or not-yet-approved grant still serve a byte
   (check on every fragment, not only at admission)?
4. Sensitive grants and quarantine: `QuarantineHolds` "ever active" rule (O-146 residual),
   and the edges from debates (OD-P3-4).
5. Does any error string from `capability` (which may quote attacker bytes) reach audit,
   logs or the peer (R25 N2: only `ReasonOf`)?

## C10 · Fetch server and client, git serving — 2,349 lines

**Files:** `internal/capability/fetch.go` (760), `git.go` (615), `internal/daemon/fetch.go` (53),
`fetch_client.go` (571), `cmd/agentnet/fetch.go` (350).
**Specs:** grant.md §Transport, §Serving git, §Limits, §Revocation, `Docs/cli/fetch.md`, D23.
**Prior reviews:** R34, R37, R38, R39, HO (audit-before-last-response trade-off, O-189).
**Entry points / trust boundaries:** `fetch.req` over a Noise session (paired peer holding a
grant), git subprocesses (`rev-parse`, `ls-tree`, `cat-file`, `for-each-ref`) with a scrubbed
environment, `fetch_start`/`fetch_status` IPC (local agent), the CLI writing fetched files
to local paths.
**Questions:**
1. Git: can a peer-controlled value (path, branch, ref, cursor) become an option, a revision
   expression, a pathspec with magic, or reach a hook, a config include, a filter, a lazy
   fetch or `core.fsmonitor`? Is every inherited `GIT_*` removed and is `git` resolved
   safely (PATH hijack on Windows: current directory)?
2. Limits: per-grant, per-peer and per-daemon in-flight slots, byte budgets and rate limits
   are released on every path (R39 fix), and a hostile holder cannot pin them.
3. Fetch client: every response member validated (R38 L1/L2), and the CLI's local write
   (`--out`): path traversal from a peer-supplied name, overwrite of existing files,
   symlink following, partial files, permissions.
4. Audit: fetches audited without content and before the last response (HO); any path
   where a served read is not audited?
5. Shutdown: `Close` ordering vs in-flight sends (O-144) — any deadlock rather than delay?

## C11 · Grant issuance, policies, revoke, quarantine release — 1,470 lines

**Files:** `internal/daemon/grant.go` (996), `quarantine.go` (63), `cmd/agentnet/grant.go` (411).
**Specs:** grant.md §Issuance, §Policies, §Session end, approval.md (callers' rules N1–N5),
work-session.md §Quarantine, OD-P2-5, D16, D18.
**Prior reviews:** R26 (N1–N5), R28, R35, R37 (L1).
**Entry points / trust boundaries:** `grant_create`, `grant_policy_add/list/remove`,
`grant_revoke`, `grant_list/show` IPC (local agent: every one of these must be approval-gated
or harmless), git issuance checks, policy matching (auto-issue without approval).
**Questions:**
1. Can a local agent get an active grant without a human approval: through a policy that is
   broader than the human approved, a policy match on a different branch/path/peer/expiry,
   a race between approval and Precondition, or a Perform that re-reads different state?
2. Forbidden resources at issuance (config dir, home, `/`, drive roots, `.git`): are they
   checked after resolving symlinks/junctions, case-insensitively where the FS is, and with
   component-wise containment (R28 L1)?
3. Every state check in Precondition, Perform writes only through its tx, hooks never call
   back into the approval Store (N1–N5).
4. `ws_release`/quarantine release: gated by approval bound to the current seq (R35 H1)?
5. CLI: output of peer-supplied names/paths (terminal safety), and argument parsing of keys
   starting with `-`.

## C12 · Human approval: store, window, terminal mode — 2,944 lines

**Files:** `internal/approval/approval.go` (396), `settings.go` (96), `store.go` (963),
`internal/daemon/approval.go` (152), `approval_terminal.go` (104), `cmd/agentnet/approve.go` (150),
`internal/notify/approval.go` (32), `approval_darwin.go` (32), `approval_linux.go` (120),
`approval_other.go` (15), `approval_windows.go` (64), `window.go` (123), `window_darwin.go` (103),
`window_linux.go` (281), `window_other.go` (16), `window_parse.go` (24), `window_windows.go` (273).
**Specs:** approval.md (all, especially Threat model, Object, Delivering the code, The
approval window, Headless machines), D19, D20, OQ-2.2d-1.
**Prior reviews:** R24 (H1, M1), R26, R29, R30, R31, R40 (M1 bidi), R46.
**Entry points / trust boundaries:** `approval_list/open/reject` IPC (local agent), the
daemon-spawned window process (PowerShell / osascript / zenity / kdialog) and its stdout,
terminal-mode stdin reader, the OS notification carrying the code, `approval_key` in memory.
**Questions:**
1. Is there any way for a local agent using IPC or the CLI to submit or learn a code
   (an IPC method, an error message, a log line, an audit detail, an argv, an env var
   readable by other processes, a file, a notification body it can read through the
   interface)? Trace the code from `crypto/rand` to its drop.
2. The window process: argv/env/script built only from sanitised text; paths resolved to
   fixed absolute locations (never PATH, `%SystemRoot%` from `GetSystemDirectory`); the child
   killed on decide/expiry/close (job object, Pdeathsig); output parsing exact and bounded.
3. Can a stale window or a buffered early answer approve a different or a newer approval
   (id binding, seq binding)?
4. Rate limits (5 pending, 20/h, 3 attempts, daily wrong-code cap, lockout) hold under
   concurrent calls; restart expires every pending approval.
5. Terminal mode: refuses without a TTY (exit 2), and `DORYLINAE_APPROVAL=terminal` cannot be
   set by an agent that only has IPC.
6. Summaries: every peer-supplied string passes `notify.Clean`/`DisplayQuote`, bidi and
   invisible characters removed or escaped, length-capped; decoy digits (O-129).

## C13 · Device link, helper scope, program ownership — 2,485 lines

**Files:** `internal/device/device.go` (635), `perm.go` (148), `perm_acl.go` (50),
`perm_acl_linux.go` (28), `perm_acl_other.go` (7), `perm_unix.go` (74), `perm_windows.go` (190),
`scope.go` (521), `internal/daemon/device.go` (583), `cmd/agentnet/device.go` (249).
**Specs:** device.md (all), D13, D22, D24, approval.md (kinds `device_link`, `device_scope`).
**Prior reviews:** R36, R40, R41.
**Entry points / trust boundaries:** `device_link/list/unlink` IPC, `device.link`/`device.unlink`
mail (paired peer), scope validation (`resolveProgram`, batch/script refusal),
`CheckProgramOwner` (Windows ACL walk, POSIX modes and Linux ACLs), link hierarchy (depth 1,
one-way).
**Questions:**
1. Can a peer, team owner, roster or pairing create or activate a `device` link without both
   humans' approvals (D13)? Offer freshness and unlink watermark (D22, R40 L7, O-158).
2. `resolveProgram`: can a scope end up running a shell, a script (`.bat`, `.cmd`, `.ps1`,
   extension-less, trailing dots/spaces, ADS), or a program found through PATH/current dir
   at run time rather than the absolute path approved?
3. Program ownership walk (D24): does it refuse any file or ancestor directory writable by
   non-owners/non-admins, including through junctions, symlinks, hard links, inherited ACEs,
   compound/object ACEs, deny-ACE ordering, and on Linux POSIX ACLs? Any TOCTOU between check
   and exec (see C14)?
4. Unlink from either side revokes authority immediately, including queued and running work.

## C14 · Helper runner and scope execution — 1,753 lines

**Files:** `internal/device/runner.go` (265), `runner_linux.go` (13), `runner_unix.go` (45),
`runner_unix_other.go` (9), `runner_windows.go` (91), `testdata/runnerhelper/main.go` (80),
`internal/daemon/device_run.go` (629), `device_scope.go` (329), `cmd/agentnet/device_scope.go` (292).
**Specs:** device.md §Scope, §Running, §Limits, §Unlink and expiry, §Program ownership, D24.
**Prior reviews:** R24 (L1, L12, L13), R40, R41.
**Entry points / trust boundaries:** `request` mail from the controller naming a command
(paired device), the process spawn (`exec.Cmd` with fixed argv/env/dir), process tree
control (job object; setpgid + Pdeathsig), output capture (32 KiB tail, control chars
stripped), `device_scope_set/show/clear` IPC (approval-gated).
**Questions:**
1. Is anything in the child's argv, env, working dir or stdin influenced by the request or
   the peer (it must not be: only the human's scope)? Is the environment minimal on every OS
   (R24 L1) and free of the daemon's secrets (`DORYLINAE_*`, keystore paths)?
2. Kill on revoke/clear/expiry/timeout (D24): whole tree on Windows (job created before or
   atomically with the child?), process group on Unix; races between the run finishing and
   the kill; restart recovery (R41 L1).
3. Re-check at run time (`CheckTarget`, program owner): TOCTOU between the check and
   `Start` (swap the binary or a directory in between)?
4. Output: bounded memory while reading (not only the stored tail), control/ANSI stripped
   before it reaches the controller's terminal, never audited.
5. `device_scope_set` summaries: full scope shown to the human before approval (O-167), and
   the stored scope equals what was approved.
6. `runnerhelper` (test program): is it built only in tests and never shipped?

## C15 · Supply chain: release, install, CI, deploy, versioning — 2,568 lines (912 Go + 1,656 other)

**Files:** `scripts/install.sh` (326), `.github/workflows/release.yml` (325), `ci.yml` (214),
`phase2-harness.yml` (41), `phase3-harness.yml` (43), `sensitive-paths.yml` (82),
`.github/dependabot.yml` (6), `tools/releasesign/main.go` (505), `tools/binversion/main.go` (200),
`internal/version/release.go` (66), `version.go` (141), `packaging/homebrew/agentnet.rb` (51),
`render.sh` (37), `tests/install/cases.sh` (118), `run.sh` (130), `.golangci.yml` (22),
`Makefile` (24), `.gitattributes` (3), `deploy/early/agentnet-relay.service` (66),
`Caddyfile` (35), `setup.sh` (133). Context (not counted): `go.mod`, `go.sum`.
**Specs:** `Docs/review/49-phase4-tickets.md` §install (4.4), §4.4a, §4.1p; D36 (OD-P4-19),
D39, D40, D42; `Docs/ops/release-signing.md`, `Docs/ops/early-relay-deploy.md`,
`Docs/cli/install.md`, relay-hosted.md §1 (for the Caddy/systemd config).
**Prior reviews:** **R53** (release, install.sh), HO (dry run, binversion 79b2445, L7/L8,
D42). `binversion`, `deploy/early/*` and the key embedding (447f08e) were **not reviewed**.
**Entry points / trust boundaries:** `curl | sh` of `install.sh` (network, mirrors, signature
over `SHA256SUMS`), GitHub Actions (PR code, `pull_request_target`?, tokens, permissions,
caches, third-party actions pinned by SHA), release draft creation, the embedded public key,
Homebrew formula rendering, the relay host (systemd sandbox, Caddy routes, `setup.sh` run as
root).
**Questions:**
1. `install.sh`: can any input (env var, mirror, redirect, archive member, version string,
   `uname` output, `$HOME` with spaces or quotes) make it install unsigned or unverified
   bytes, write outside the install dir, or run with sudo? Is the embedded key the owner's
   (447f08e) in both PEM and minisign encodings, and does the placeholder check still refuse?
2. Workflows: which triggers run PR-controlled code with secrets or `contents: write`? Any
   script injection through `${{ }}` of titles, branch names or inputs? Are all actions
   pinned by full SHA (L7) and `persist-credentials: false` where a token is not needed?
   Can a cache written by a PR or `main` reach a release build?
3. `binversion`: can a crafted binary make it read out of bounds, loop, or allocate a lot
   (it runs on build artefacts only, but it gates the release)? Does it read the right symbol
   on all 6 targets?
4. Release builds: `-trimpath`, `CGO_ENABLED=0`, no `testhooks` tag, goldmark (D32 test-only)
   not linked into any shipped binary; version string validation (`ParseRelease`).
5. `deploy/early`: systemd sandbox flags actually effective (ProtectSystem=strict with the
   DB/journal dirs writable only where needed), Caddy proxies only `/v1/connect` and
   `/healthz`, `/metrics` never public, `setup.sh` idempotent and safe to rerun (no lockout,
   no curl-to-bash, correct ufw rules, file permissions of the relay's state dir).
6. `go.mod`: any dependency that is unmaintained, has a known advisory at these versions,
   or pulls in network code not needed (quick check only; note, do not upgrade).

## C16 · Local daemon install, service, IPC transport, paths, log file — 1,629 lines

**Files:** `cmd/agentnetd/install.go` (197), `main.go` (143), `relayurl.go` (66), `stop.go` (54),
`internal/service/current_darwin.go` (6), `current_linux.go` (6), `current_other.go` (6),
`current_windows.go` (6), `env.go` (46), `launchd.go` (97), `schtasks.go` (142), `service.go` (225),
`systemd.go` (75), `internal/paths/paths.go` (75), `internal/logfile/logfile.go` (95),
`internal/config/doc.go` (3), `internal/ipc/ipc.go` (288), `transport_unix.go` (48),
`transport_windows.go` (51).
**Specs:** ipc.md (framing, limits, errors), `Docs/cli/agentnetd.md`, `agentnetd-install.md`,
relay-hosted.md §1 (daemon URL rule, `--relay-ca`), audit.md (install audit, D31).
**Prior reviews:** R02, R05 (M3), R43 (M9, L18), R44 (L4, L6), R51 (M2, L2), 4.4d (not reviewed).
**Entry points / trust boundaries:** the IPC endpoint (Unix socket 0600 in the config dir;
Windows named pipe with a user-only DACL) — the local-agent boundary; `agentnetd install`
writing a launchd plist / systemd unit / scheduled task (paths and flags baked in); service
manager command execution; config/data dir creation and permissions; the log file.
**Questions:**
1. IPC: can another OS user connect (socket dir and file mode, umask races, named pipe DACL
   and first-instance flag, `\\.\pipe\` squatting by a process that starts first)? Is the
   line/message size bounded before allocation, and are malformed requests unable to crash
   or block the server (one slow client stalling others)?
2. Service files: are values baked into plist/unit/task XML escaped for their format (a home
   path with spaces, quotes, `&`, `%`, newlines)? Can install be pointed at an attacker-chosen
   binary or relay URL by environment variables? Is the `service.go` exec argv fixed?
3. Paths: config dir permissions on first creation on each OS (0700 / private DACL);
   `DORYLINAE_HOME` handling; long socket paths (O-203).
4. Log file: size cap and rotation cannot be abused to delete other files (symlink at the
   log path), and file mode is owner-only.
5. Relay URL rule and `relay_ca.pem`: only https/wss except explicit loopback; persisted
   choices (O-018) — anything beyond what is listed?

## C17 · Requests: wire, decode, validate, receive, submit — 2,273 lines

**Files:** `internal/request/decode.go` (309), `validate.go` (352), `receive.go` (469),
`canonical.go` (119), `wire.go` (97), `artifact_spec.go` (89), `errors.go` (62),
`request.go` (170), `shared.go` (32), `urgency.go` (42), `priority.go` (33),
`internal/daemon/request.go` (499).
**Specs:** request.md (object, caps, receiving steps, invalid bodies, urgency, idempotency,
D5), consult.md (context files, caps), debate.md §Request type, D5, D12, D14.
**Prior reviews:** R12 (M9, M10, L1, L16, L17), R13, R19, R20, R32, R45 (L4).
**Entry points / trust boundaries:** `request` mail kind from any paired peer (including
`trust=relay` and introduced `team` peers), `request_submit` IPC (local agent), auto-decline
reply inside the Apply tx.
**Questions:**
1. Strictness and bounds of the request body (every string: UTF-8, controls including C1
   and bidi, lengths; arrays; nested context; artifacts; `deadline` bound O-073) and
   `ErrBadBody` vs retryable errors (O-059 rule).
2. D5: refuse requests from `trust=relay` peers on a non-loopback relay — fail closed on
   every read error (R19 #3), and "non-loopback" judged from the real connection (R51 M1),
   not the URL text.
3. Urgency budget (5 high / 2 blocking per sender per rolling 7 d): per key, correct window,
   not refunded by cancel (D12), not bypassable with duplicates or idempotency keys.
4. Idempotency: concurrent submits with one key, `params_hash` conflicts, and replay of an
   old request (`created` limits, M10's 21-day resend and 30-day receive bounds).
5. Can a request body ever reach the audit log, a log line or a notification body?

## C18 · Requests: lifecycle, mirror, cancel, result — 2,672 lines

**Files:** `internal/request/cancel.go` (407), `lifecycle.go` (397), `mirror.go` (440),
`result.go` (265), `session_hooks.go` (147), `submit.go` (265), `view.go` (199), `notify.go` (39),
`internal/daemon/request_lifecycle.go` (513).
**Specs:** request.md §Lifecycle, §Cancel, §Result payload (D14), §Result privacy,
work-session.md (every accept opens a session; early complete; quarantine), D11, D18.
**Prior reviews:** R20, R24 (H3, M6, L10, L11), R27, R35.
**Entry points / trust boundaries:** `request.accept/decline/defer/complete/cancel/cancelled`
mail (the other party of a request), `request_accept/decline/defer/complete/cancel/resend/
list/show` IPC (local agent), `inbox_list`.
**Questions:**
1. Only the authoritative side can move a request's state (`msg.from` + id lookup); `seq`
   stops stale and reordered updates; no transition outside the diagram.
2. Result payload (D14, ≤ 32 KiB): validated, and never visible to the requester while the
   work session is quarantined or discarded (R24 H3, D18); what does `request_show` return
   in each session state?
3. Cancel flows (D11): refused after accept, tombstones bounded (1000 cap), no reply loops,
   the throttled echo (O-103).
4. Every IPC method checks the caller's role (requester vs worker) for the request id, and
   ambiguous `r-` ids fail safely.
5. Transaction discipline: effects and outgoing mail in the Apply tx; `After` hooks only
   after commit; no DB use inside another tx on the single connection (R27 C1).

## C19 · Requests CLI, inbox, consult, query — 1,535 lines

**Files:** `internal/request/query.go` (166), `inbox.go` (116), `cmd/agentnet/request.go` (263),
`request_query.go` (256), `inbox.go` (418), `consult.go` (219), `internal/daemon/consult.go` (97).
**Specs:** request.md §Inbox order, consult.md, `Docs/cli/request.md`, `inbox.md`, `consult.md`.
**Prior reviews:** R19, R20 (L1, L8), R32, R48 (L2).
**Entry points / trust boundaries:** peer text printed to the terminal (titles, summaries,
outputs, notes, context names), CLI flags and files read by the CLI (`--brief-from-file`,
context files), `consult` accept-and-submit path, `wait`.
**Questions:**
1. Every peer-supplied string printed by the CLI (human and `--json`): control, C1, bidi,
   invisible characters, ANSI; multi-line indentation that can spoof a field (O-100 is the
   known part: look for anything beyond it).
2. Files the CLI reads: regular files only, bounded size, no FIFOs/devices (R32 L2), only
   the base name sent, symlinks acceptable?
3. Query/inbox SQL: parameterised, bounded result sizes vs the 1 MiB IPC line, sort order
   total.
4. `consult` + `wait`: timeouts, exit codes, and whether a hostile peer's answer can make the
   CLI hang or print unbounded output.

## C20 · Teams: store, kinds, invites — 2,508 lines

**Files:** `internal/team/invite.go` (73), `kinds.go` (583), `store.go` (752), `team.go` (77),
`internal/daemon/team.go` (483), `cmd/agentnet/team.go` (540).
**Specs:** team.md (all), pairing.md (tagged pairings), `Docs/cli/team.md`, OD-P1-2.
**Prior reviews:** R12 (M1–M4, L6–L9, L13, L18), R14, R16, R18.
**Entry points / trust boundaries:** `team.roster`, `team.join`, `team.leave` mail (owner /
members / anyone paired), `team_*` IPC (local agent), introductions of third-party keys into
`peers` with trust `team`.
**Questions:**
1. Only the team owner (trust `code`/`fingerprint`) can introduce keys; a member, an
   introduced peer or the relay cannot; epochs never roll back; re-entry after
   left/removed/dissolved needs a live pending join (R12 M1).
2. Roster size, card and announcement verification, and the order "teams/members before GC"
   in one tx (R14 L1); can a roster delete a directly paired peer?
3. `team.join`: invite lookup and single use, `team_full`, joins from existing members
   (R16 M2), deleted teams (O-091).
4. Owner operations (rename, delete, remove, leave) gated by state (O-078) and audited.
5. CLI output of team and member names (terminal safety).

## C21 · Work sessions: state machine, receive, results, cancel, IPC — 2,390 lines

**Files:** `internal/worksession/transitions.go` (491), `receive.go` (280), `result.go` (256),
`cancel.go` (231), `mirror.go` (278), `errors.go` (53), `wire.go` (134), `worksession.go` (87),
`internal/daemon/session.go` (580).
**Specs:** work-session.md (all: state machine, result object, accept-result, quarantine,
OD-P2-6 (c), security considerations), D16, D18, D25.
**Prior reviews:** R24 (H3, M6, M10, L10, L11), R27, R35, R43 (M3–M5).
**Entry points / trust boundaries:** `ws.result`, `ws.state`, `ws.cancel` mail (the other
party), `ws_*` IPC (local agent): `ws_accept_result`, `ws_release`, `ws_discard`,
`ws_request_changes`, `ws_cancel`, `ws_result`, `ws_show`, `ws_list`.
**Questions:**
1. Quarantine: can the requester's agent see any byte of a quarantined result (or a
   discarded one, D18) through any IPC method, `request_show`, the audit log, notifications,
   the experience record, or the `mail_inbox` row?
2. Release and accept-result are approval-gated and bound to the round/seq the human saw
   (R35 H1); discard and request-changes need no approval but delete the content.
3. Transition table: every method checks role and exact source state; a peer cannot move A's
   session; stale rounds ignored.
4. Mixed-version fallbacks (R24 M6, R27 M5) cannot be triggered by a current peer to close
   sessions.

## C22 · Work-session store, hooks, Phase 1 fallback, experience, session CLI — 2,056 lines

**Files:** `internal/worksession/store.go` (376), `hooks.go` (193), `phase1.go` (169),
`submit.go` (157), `experience.go` (133), `debate.go` (93), `internal/experience/experience.go` (236),
`cmd/agentnet/session.go` (699).
**Specs:** work-session.md, experience.md (private record, no read command, content rules),
request.md (early complete), debate.md (debate sessions), OD-P3-8, D25.
**Prior reviews:** R27, R35, R43 (L2, L3). 3.7 (experience) had no separate security review.
**Entry points / trust boundaries:** hook contracts with `request` and `debate`, the Phase 1
fallback trigger (outbox failures), experience rows written in every closing tx, the
`agentnet session`/`sessions`/`result`/`accept-result` CLI printing results.
**Questions:**
1. Experience record: written in the same tx as every close on both sides, contains only
   what experience.md allows, never exposed by IPC (no read command, D30).
2. Hook contracts: no audit or DB use inside a tx (R27 C1), callbacks after commit, and
   failure of a hook cannot leave a session open with live grants.
3. CLI: result output (≤ 32 KiB) printed with control/ANSI/bidi handling; quarantined
   results never printed; exit codes.
4. `phase1.go`: can a hostile or upgraded peer trigger the fallback to close a healthy
   session (R27 M5 matching)?

## C23 · Debates: engine, apply, turns, store, CLI — 2,881 lines

**Files:** `internal/debate/apply.go` (794), `engine.go` (260), `turns.go` (107), `commit.go` (48),
`sweep.go` (90), `store.go` (452), `submit.go` (136), `start.go` (206), `errors.go` (45),
`cmd/agentnet/debate.go` (709), `debate_constrain.go` (34).
**Specs:** debate.md (commit–reveal, turns and rounds, ordering, timeouts, close, security
considerations), decision.md §Signing, OD-P3-1..7, D30.
**Prior reviews:** R43, R45, R46 (L2, L4), R47 (L6–L8).
**Entry points / trust boundaries:** `debate.*` mail (the other party), `debate_submit`,
`debate_list`, `debate_show` IPC (local agent), the sweep (timeouts), the CLI printing peer
positions/arguments.
**Questions:**
1. Commit–reveal: can A learn B's position before committing, or change its own after B's
   opening; is the reveal nonce/format checked; can a replayed or reordered entry be
   accepted into another slot or round?
2. Turn engine: slot/round/deadline rules enforced on both sides; early entries bounded
   (O-168); a modified peer cannot make our side sign an outcome our transcript does not
   support (R45 H1).
3. Sweep and timeouts: one bad debate does not stop others; timeouts use the right clock.
4. `debate_submit`: position/argument size and content checks; `params_hash` covers
   position, rounds and timeout (R43 L11).
5. CLI: every peer string through `termSafe`/visible escaping (O-100 known), JSON output
   bounded.

## C24 · Debates: schemas, decode/validate, constraints, view, decision derivation — 2,722 lines

**Files:** `internal/debate/schema.go` (177), `decode.go` (399), `validate.go` (288), `text.go` (112),
`canonical.go` (111), `constraint.go` (352), `view.go` (185), `experience.go` (119), `decision.go` (497),
`internal/daemon/debate.go` (345), `debate_constrain.go` (137).
**Specs:** debate.md §Messages, §Human constraints (3.4), §Outcome, decision.md (derivation
rules), approval.md (`debate_constraint`), R46 H1 (invisible characters), D30.
**Prior reviews:** R43, R45, R46, R47.
**Entry points / trust boundaries:** every debate entry from the peer (decode/validate),
constraint text from the local agent (approval-gated), the derivation of Decision bytes on
both sides.
**Questions:**
1. Decode/validate: strict schemas, sizes, C0/C1/bidi/invisible-graphic refusal in all
   debate text (R43 L1, R46 H1), canonical form identical on both sides (Unicode version skew:
   R46 L4).
2. Constraints: nothing stored in debate tables before approval; the approval summary shows
   the full text; B holds a close for missing ids (O-165) — can that be abused to stall
   forever?
3. Derivation: both sides derive byte-identical Decisions from the same transcript; any
   input a modified peer controls that makes the honest side derive something the human
   did not agree to?
4. `debate_constrain` IPC: approval-gated, Perform writes only through its tx, id binding
   (O-147 known).

## C25 · Decision record: build, sign, verify, Markdown export — 2,396 lines

**Files:** `internal/decision/decision.go` (385), `markdown.go` (476), `verify.go` (575),
`visible.go` (81), `internal/daemon/decision.go` (175), `cmd/agentnet/decision.go` (397),
`tools/verifyvectors/decision.go` (307).
**Specs:** decision.md (all: canonical form, signatures, verification steps, exit codes,
inert Markdown), D30, D32, D33, OD-P3-14.
**Prior reviews:** R43 (H1, L12), R47, R48.
**Entry points / trust boundaries:** `decision verify` of an arbitrary file (anyone can hand
you a file), `decision show/export` (`--md`, `--json`, `--out`), `decision_list/show` IPC,
Markdown rendered by GitHub/Jekyll/Hugo (template-inert, R48 H1).
**Questions:**
1. Verify: can a single-signed or tampered file verify as confirmed (exit 0)? Every step of
   decision.md in order; strict base64; size cap before parse; derivation invariants (R43 L12).
2. Markdown: inert for CommonMark, GFM, Jekyll/Liquid, Hugo shortcodes, HTML, autolinks,
   link reference definitions and front matter, given hostile peer text; the goldmark test
   is test-only (D32).
3. `--out`: overwrite rules, symlinks (R48 L6), permissions, partial writes.
4. D33 revisit (O-177): state the facts the owner needs (what an unaudited export exposes;
   whether a content-free audit is feasible). Do not re-report the finding itself.

## C26 · Audit log, SQLite store, migrations, `agentnet log` — 2,111 lines

**Files:** `internal/audit/audit.go` (248), `chain.go` (376), `query.go` (348),
`internal/store/store.go` (624), `readonly.go` (28), `internal/daemon/audit.go` (94),
`cmd/agentnet/log.go` (393).
**Specs:** audit.md (all), D31, and every spec's §Audit table for the event list; HANDOFF §4
(migrations, rewind tests), §5 lessons.
**Prior reviews:** R31, R43 (M8, M9, L9, L18), R44, INV-5 (cancelled-context BEGIN).
**Entry points / trust boundaries:** `Append`/`AppendTx` (every package), migrations 1–21
(run by the daemon and by `agentnetd install`'s second opener), triggers (append-only,
chained), `audit_list/verify/head` IPC, read-only fallback when the daemon is down, anchors.
**Questions:**
1. Chain: can any code path (including `INSERT OR REPLACE`, gaps, concurrent appends from
   two processes, `SQLITE_BUSY_SNAPSHOT`) break or fork the chain without `--verify`
   noticing? Is `--verify` itself paged and non-blocking (R43 M8)?
2. Migrations: each atomic, version read inside the tx (R43 M9), safe when two processes
   open the DB at once; any migration that can lose data on an old DB?
3. Transactions: every `BeginTx` path rolls back on every error including a cancelled
   context (INV-5); single connection (`SetMaxOpenConns(1)`) never deadlocks through a
   nested use; `secure_delete` on (D30) and WAL handling.
4. `agentnet log` output: details printed safely (they are content-free, but peer ids and
   enums could still carry control characters if an upstream check slipped).
5. Read-only fallback: opens without migrating or writing; cannot be pointed at another
   user's DB by environment.

## C27 · Notifications: desktop, webhooks, triggers — 2,394 lines

**Files:** `internal/notify/desktop.go` (40), `desktop_darwin.go` (18), `desktop_linux.go` (73),
`desktop_other.go` (14), `desktop_windows.go` (58), `dial.go` (258), `notify.go` (65),
`payload.go` (182), `queue.go` (250), `settings.go` (197), `sign.go` (58), `trigger.go` (257),
`webhook.go` (293), `webhook_settings.go` (72), `internal/daemon/notify.go` (331),
`cmd/agentnet/notify.go` (228).
**Specs:** notify.md (all: desktop per OS, webhook signing, SSRF rules, payloads, privacy
summary), D25, OD-P1-13.
**Prior reviews:** R12 (M11–M14, L12), R21, R22.
**Entry points / trust boundaries:** events triggered by peer mail (names, titles), desktop
notifier processes (gdbus/notify-send argv as GVariant literals, PowerShell toast XML,
osascript), webhook URL settable by any local IPC client (SSRF), outbound HTTP, webhook
secret in the keystore.
**Questions:**
1. SSRF: the dial-time check on the real connected address for every connection the client
   makes (redirects off, proxies from env off, IPv6 forms, DNS rebinding between check and
   connect); O-118 is the known gap, look for others.
2. Injection: peer text in GVariant, XML toast templates, AppleScript strings, Slack/Discord
   markup (mentions, links), JSON payloads — each escaped for its sink?
3. Content: notifications and webhook payloads carry only what notify.md's privacy summary
   allows (titles are allowed; bodies and results are not).
4. Secrets: webhook secret and URL never logged, audited or printed in full (R22 L1/L2).
5. Bounds: queue sizes, retry schedule, response body size (O-119), goroutine per event?

## C28 · Daemon core, status, CLI entry, doctor, identity, test utilities — 2,235 lines

**Files:** `internal/daemon/daemon.go` (805), `status.go` (141), `stop.go` (56),
`cmd/agentnet/main.go` (327), `stop.go` (73), `doctor.go` (546), `identity.go` (96), `mail.go` (104),
`internal/testutil/private.go` (35), `tempdir.go` (46), `internal/protocol/doc.go` (3),
`internal/transport/doc.go` (3).
**Specs:** ipc.md (method table, errors), `Docs/cli/*.md` for `status`, `stop`, `doctor`,
`identity`, `mail`; 49 §CLI contracts (doctor); audit.md (`daemon.start`).
**Prior reviews:** R02, R05, R43 (M9), R44 (L2), R50 (M4), R53 (L4); 4.4c `doctor` not
reviewed.
**Entry points / trust boundaries:** daemon startup (store, keystore, relay, IPC server,
every subsystem wired in order), the IPC method registry (which methods exist; any debug or
test-only method reachable in release builds?), shutdown ordering, `status` (relay state,
`min_client`), `doctor` (dials the relay without auth, reads files, prints relay strings),
CLI argument parsing (`parseInterspersed`, exit codes).
**Questions:**
1. Method registry: list every IPC method registered at runtime and compare with ipc.md and
   the index (§3.a). Is any method undocumented, test-only, or more powerful than its doc?
2. Startup/shutdown: subsystems started in a safe order (no mail processed before the store
   and audit are ready; no IPC before approvals are wired), and stopped without losing
   committed-but-unsent work or deadlocking (`Close` ordering).
3. `doctor`: never authenticates (R50 M4), prints relay-supplied strings safely, never
   opens a URL, handles a hostile relay (slow, huge frames, bad TLS) with timeouts.
4. `status`/`identity`/`mail` CLI: output of relay and peer strings; no secret material.
5. `testutil`: `private.go` creates directories with the right ACL; nothing in testutil is
   imported by production code.

## C29 · Spec vectors: generator and independent verifier — 1,580 lines

**Files:** `tools/specvectors/main.go` (545), `tools/verifyvectors/main.go` (898), `relayauth.go` (137).
**Specs:** pairing.md, mail.md, grant.md, relay-hosted.md (auth v2) §Test vectors; HANDOFF §2
rule 5 (`go run ./tools/verifyvectors` is a merge gate).
**Prior reviews:** R07 (vector check), R25 (L7), R51.
**Entry points / trust boundaries:** none at runtime; these tools are the independent check
that the production code matches the specs.
**Questions:**
1. Independence: does `verifyvectors` share code with the production packages it checks
   (imports of `internal/...`)? Where it does, the check is not independent: list each.
2. Coverage: which spec vectors (pairing K/T/tags, mail HPKE, grants, auth v2, decisions)
   are recomputed from primitives vs only compared as strings?
3. Can the gate pass while the production code is wrong (e.g. it verifies the vectors file
   against itself)?

## C30 · Test harness: stand-in agent and POSIX scripts — 2,542 lines (600 Go + 1,942 shell)

**Files:** `tests/harness/standin/main.go` (600), `tests/harness/phase1-agents.sh` (375),
`phase2-agents.sh` (387), `phase3-agents.sh` (585), `tests/phase0-smoke.sh` (151),
`tests/phase1-smoke.sh` (444).
**Specs:** `tests/harness/README.md`, `.github/workflows/phase2-harness.yml`,
`phase3-harness.yml`, `Docs/agents/snippet.md`.
**Prior reviews:** none (2.H, 3.H, SH-FIX, INV-3 are recorded in HANDOFF only).
**Entry points / trust boundaries:** run weekly in CI and by hand with real agent CLIs
(Claude Code, agy) that may hold the owner's credentials; temp dirs; spawned daemons and a
relay.
**Questions:**
1. Can a harness run leak the owner's credentials, API keys or real config dir (it must use
   its own `DORYLINAE_HOME`), or touch the user's real daemon/keychain?
2. Quoting and temp-file handling in the shell scripts (spaces in paths, `set -e` gaps,
   `rm -rf` on an empty variable, predictable temp names).
3. Do the scripts' PASS conditions actually check the outcome (grep for a string that is
   always printed, exit codes ignored)?
4. Stand-in: parses agentnet output defensively; cannot be steered by peer text into running
   other commands.

## C31 · Test harness and smoke tests: PowerShell — 2,731 lines

**Files:** `tests/harness/phase1-agents.ps1` (600), `phase2-agents.ps1` (626), `phase3-agents.ps1` (830),
`tests/phase0-smoke.ps1` (214), `tests/phase1-smoke.ps1` (461).
**Specs:** as C30; HANDOFF §5 (PowerShell 5.1 pitfalls, headless agents on Windows,
connector isolation `--strict-mcp-config`).
**Prior reviews:** none.
**Questions:** the four C30 questions, for PowerShell 5.1: plus (5) are Claude Code runs
isolated as HANDOFF §5 requires (no MCP connectors, `--setting-sources project`, allowlist of
commands), and (6) are processes killed with `taskkill /T /F` on every exit path so no
daemon or relay is left running?
