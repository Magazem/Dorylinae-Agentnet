# 23: Phase 2 tickets (2.1–2.7 and the own-device helper, D13)

Status: **draft, reviewed (adversarial spec review: [24-phase2-spec-review.md](24-phase2-spec-review.md), fixes applied), awaiting owner approval.** No code
starts before approval (HANDOFF rule 3). Specs:
[work-session.md](../protocol/work-session.md), [approval.md](../protocol/approval.md),
[grant.md](../protocol/grant.md), [consult.md](../protocol/consult.md),
[device.md](../protocol/device.md), and additions to [request.md](../protocol/request.md),
[mail.md](../protocol/mail.md), [ipc.md](../protocol/ipc.md) and
[session.md](../protocol/session.md) (a naming note only).

Every ticket follows the plan's exit criteria, as in Phase 1: acceptance tests are automated
Go tests; `go vet` and the linter pass; `--help` and `--json` output are documented in
`Docs/cli/`; audit events exist; new tests use `internal/testutil.TempDir`; async state and
audit are polled with a deadline, never read once. "e2e" means the in-process two-daemon +
relay harness (`internal/daemon/outbox_harness_test.go` pattern). "Fake clock" means
injecting `Now`.

## Scope rule

Only what the Phase 2 acceptance tests (plan 2.1–2.7) and D13 need. Deferred, with the
reason in the spec: read-only **database** grants (OD-P2-4), holder attenuation and
Biscuit (OD-P2-1), write grants, a human-only preview of quarantined results (OD-P2-7),
cancel from `awaiting_result` (OD-P2-6), multi-part consult answers, arguments in helper
commands, helpers of helpers, git history or bundles, mixed Phase 1/Phase 2 teams beyond
the request-level fallback ([work-session.md](../protocol/work-session.md#early-complete-and-phase-1-workers)), fetch
resume across restarts, a Phase 2 section in the one-machine smoke script (after 2.P, if the
owner wants it).

## Migrations (pre-assigned)

`internal/store` requires consecutive versions, so tickets that add a migration **merge in
migration order**; a branch built early uses a clearly marked NO-OP placeholder that the
Orchestrator replaces at merge (never merge a placeholder). **Every migration's tables must
also be added to the DROP lists of BOTH rewind tests in `internal/store/store_test.go`**
(`TestMigration8PreservesPeers` and `TestMigrationAddsPeerTrust`); each ticket's acceptance
includes that both tests still pass.

| # | Name | Tables | Ticket |
|---|---|---|---|
| 14 | `work_sessions` | `work_sessions` | 2.1a |
| 15 | `approvals` | `approvals`, `grant_policies` | 2.2a |
| 16 | `grants` | `grants` | 2.2c |
| 17 | `device_links` | `device_links`, `device_scopes`, `device_offers` | 2.D1 |

No migration changes `peers` (no rebuild: the `device` relation is its own table, OD-P2-8),
and none changes `requests` (`context` and `run` live in the canonical body).

## Tickets

"Review" marks tickets that need an **Opus security review before merge** (HANDOFF rule 4).
Sizes: **S** ≈ half a day of agent work, **M** ≈ one day, **L** = at the one-day limit (split
further if it runs over). "∥" lists tickets that can run in parallel once dependencies merge.

| ID | Title | Depends on | Migration | Size | Review | ∥ |
|---|---|---|---|---|---|---|
| 2.1a | `internal/worksession`: derived id, store, state machine on the requester, mirror on the worker, kinds `ws.*`, open on accept, close → complete, result object (2.6) | specs approved | 14 | L | **yes** | 2.2a, 2.2b |
| 2.1b | Session IPC + CLI: `sessions`, `session`, `result`, `wait`, `accept-result`, request changes, cancel; `complete` shorthand; request view `session` | 2.1a | — | M | — | 2.2a, 2.2b, 2.D1 |
| 2.2a | Human approval: `approvals`, notifier code, `approve`, `DORYLINAE_APPROVAL=terminal`, `grant_policies` table | specs approved; **merge** after 2.1a (migration order) | 15 | M | **yes** | 2.1a, 2.1b, 2.2b |
| 2.2b | `internal/capability`: token, canonical, sign, `Verify` steps 1–8, vectors in `tools/specvectors` and `tools/verifyvectors` | specs approved | — | S | **yes** | 2.1a, 2.2a |
| 2.2c | Grant issuance: `grants`, `grant_create/list/show/revoke`, policies, kinds `grant`/`grant.revoke`, holder apply, session-end revocation | 2.1a, 2.2a, 2.2b | 16 | M | **yes** | 2.1b, 2.D1 |
| 2.3a | Fetch server: `fetch.req`/`fetch.resp` on Noise sessions, fragments, token check per message, `fs` serving with `os.Root`, limits, audit rate limit | 2.2c | — | L | **yes** | 2.4, 2.5 |
| 2.3b | `git` serving (plumbing commands, environment, path grammar) | 2.3a | — | M | **yes** | 2.3c, 2.4 |
| 2.3c | Fetch client: `fetch_start/status`, CLI `fetch`, `grant`, `grants`, `revoke`; the 2.2/2.3 acceptance e2e | 2.3a | — | M | **yes** (with 2.3b) | 2.3b, 2.4 |
| 2.4 | Sensitive grants: quarantine rule, `release`, views without content | 2.2c, 2.1b | — | S | **yes** | 2.3b, 2.3c, 2.5 |
| 2.5 | Consult: request `context`, `MaxQuestionBody`, `consult`, `result` on a pending question, submit result `session` | 2.1b | — | M | **yes** (light, with 2.4) | 2.3x, 2.4, 2.D1 |
| 2.D1 | Device link: `device_links`, flow, fingerprint, offers, unlink, hierarchy | 2.2a; **merge** after 2.2c (migration order) | 17 | M | **yes** | 2.2c, 2.3x, 2.5 |
| 2.D2 | Helper scope and runner: `device scope`, request `run`, in-scope checks, runner (no shell, env, process-tree kill, output sanitiser) | 2.D1, 2.1b | — | L | **yes** | 2.4, 2.5 |
| 2.9 | End-to-end loop, audit-has-no-content, docs reconciliation | 2.3c, 2.4, 2.5, 2.D2 | — | M | — | 2.H |
| 2.H | Headless harness run (2.7): Claude Code + agy, both rounds | 2.3c, 2.4, 2.5 | — | M | — | 2.9 |
| 2.P | Phase 2 push (`main`; a `phase-2` tag **only with the owner's OK**) | all above | — | — | — | — |

Critical path: 2.1a → 2.2c → 2.3a → 2.3c → 2.H/2.9 (with 2.2a and 2.2b beside 2.1a).
2.1a, 2.2a and 2.2b start at approval; 2.2a merges after 2.1a (migration 15 after 14).
Eleven tickets carry an Opus review; 2.2a and 2.2b are small and can go to one reviewer
together, 2.3a + 2.3b + 2.3c to another, and 2.4 + 2.5 to a third (2.5 changes the request
decode caps and adds an accept-and-submit path; 2.3c parses peer fragments and writes files).

## Ticket details

### 2.1a Work sessions core (review)

- Files: new `internal/worksession` (id, store, transitions, mirror, kinds, result
  validation reusing `request.ValidateComplete`), migration 14, `internal/request`
  (hooks: open on accept, complete on close, `request_complete` shorthand),
  `internal/daemon/mail.go` (register kinds), `internal/store/store_test.go` (DROP lists),
  `Docs/beta/known-limitations.md` (mixed versions), and tests.
- Acceptance:
  - the session-id vector of [work-session.md](../protocol/work-session.md#session-id);
  - **2.1: every transition of the diagram, and nothing else** (table test over all
    `state × event` pairs: allowed ones reach the listed state, every other pair is
    `bad_state` at IPC-level functions or `ws.ignored` for peer mail) (`TestSessionStateMachine`),
    including the two OD-P2-6 (c) edges from `quarantined` — **discard** (→ `closed`,
    `cancelled`, no approval, `ws_discard`, CLI `agentnet session <id> --discard`) and
    **request-changes without release** (→ `open`, `round += 1`, no approval, `ws_request_changes`
    now also accepted from `quarantined`) — and that in both the stored result is deleted and
    never surfaced through any IPC view or mail to B;
  - accept opens the session in the same transaction (an injected failure after the accept
    leaves neither); A creates its row on `request.accept`, or on a `ws.result` that
    overtakes it;
  - `ws.result` with a wrong round, in a wrong state, or with a session id not derived from
    `(A, B, request)` is ignored/`bad_body`; a body moved to another session fails;
  - mirror `seq` rules: out-of-order `ws.state` ends in the highest `seq`;
  - on close, B completes the request and A's request mirror ends `completed` with the D14
    part of the result (`outcome = accepted`) or with the fixed note (`cancelled`);
  - `request_complete` on an open session submits a result; in other session states it is
    `bad_state`;
  - result caps: every member at its limit and one over, `verification` values
    (`human_accepted` from B → `bad_body`), total 65536 / 65537 → `result_too_large`;
  - **early complete** (review 24, H3/M6): a `request.complete` applied on A while the
    session is `open` completes the request and closes the session `cancelled` (grants
    ended); with the quarantine rule holding, its `result` and `note` are stored nowhere
    (search every table for a marker); in `awaiting_result` the session is unchanged;
  - **Phase 1 requester:** a `ws.result` whose outbox row ends `failed`/`unsupported_kind`
    completes B's request with the D14 part and closes B's mirror (a fake peer that acks
    `unsupported`);
  - both rewind tests pass with `work_sessions` in their DROP lists.

### 2.1b Session IPC and CLI

- Files: `internal/daemon`, `cmd/agentnet/session.go`, `cmd/agentnet/request.go`,
  `Docs/cli/session.md`, `Docs/cli/request.md`, `Docs/cli/inbox.md`, and tests.
- Acceptance: CLI tests for each command's human and `--json` output and exit codes;
  `wait` exits 0 on a result, 4 on timeout (fake time via a short `--timeout`), and works on a
  derived id before the session exists; list views omit `output`, `notes` and `changes`;
  `accept-result --human` returns an approval and, once confirmed, stores `human_accepted`
  (**2.6 acceptance:** the requester accepts with `accept-result` and the session closes on
  both sides, `TestAcceptResultCloses`).

### 2.2a Human approval (review)

- Files: new `internal/approval`, migration 15, `internal/notify` (an approval notification
  that bypasses the event toggles but never goes to the webhook; the code never in argv:
  in-process D-Bus on Linux via `godbus/dbus/v5`, environment on macOS, tagged toast removed
  from history on Windows, [approval.md §Delivering the code](../protocol/approval.md#delivering-the-code)),
  `internal/daemon`, `cmd/agentnet/approve.go`, `Docs/cli/approve.md`, the harness
  confinement note in `Docs/agents/snippet.md` ([approval.md §Threat model](../protocol/approval.md#threat-model)),
  and tests.
- Acceptance: the `code_mac` vector; a code appears in the fake desktop notifier's call and
  in **no** IPC result, CLI output, `audit_events` row, daemon log line, webhook queue row or
  SQLite column, and **no code material** either (search every table for the code string
  and for any 64-hex value computable from it; the `approvals` table has no hash column);
  the code is in **no argv** of any child process the notifier starts (the runner fake
  records argv and environment; Linux uses the in-process D-Bus call, macOS the
  environment); a daemon restart turns pending approvals `expired`; 3 wrong codes →
  `rejected`; the 10th wrong code in 24 h → `approval_locked` for new approvals, surviving a
  restart; expiry after 10 min (fake clock); `approval_limit` at 6 pending and at 21 per
  hour; notifier disabled → `approval_unavailable`; a confirmed approval whose precondition
  no longer holds (session closed meanwhile) performs nothing and returns `bad_state`;
  `DORYLINAE_APPROVAL=terminal` writes the code to the daemon's stderr only, refuses to start
  when stderr is not a terminal unless `DORYLINAE_DEBUG=1`, audits `approval.mode`, and
  `status` reports it; Windows: the approval toast is removed from history on decision
  (manual check in `tests/phase2-manual.md`); both rewind tests pass.

### 2.2b Capability tokens (review)

- Files: `internal/capability` (replaces the placeholder), `tools/specvectors`,
  `tools/verifyvectors`, and tests.
- Acceptance: the grant vector reproduces byte for byte (canonical grant, hash, signature);
  `go run ./tools/verifyvectors` recomputes it independently; every negative check of
  [grant.md §Test vectors](../protocol/grant.md#test-vectors) fails at the stated step,
  including the **widened caveat** (plan 2.2); a fuzz test: `Verify` never accepts a
  mutation of a valid token.

### 2.2c Grant issuance (review)

- Files: `internal/capability` (store), migration 16, `internal/daemon` (handlers, kinds),
  `internal/worksession` (end grants on close), `internal/peers` hook (`peers remove`), and
  tests.
- Acceptance: **2.2: a token issued by A verifies offline on B** (holder steps 1–8 with the
  relay stopped) **and a token with a widened caveat is rejected** (holder and grantor);
  issuance refused for: a worker (`not_requester`), a closed session, a `trust=relay` peer on
  a non-loopback relay, a relative path, the config dir, the home dir, `/` or a drive root,
  a missing branch, `--expires 8d`; `--public` on `fs.read` still sensitive; approval flow
  (pending until confirmed; rejected → no mail); a matching policy issues at once and a
  non-matching one does not (scope outside the policy's, longer expiry, other peer, other
  branch, a sensitive grant under a `--public` policy, a policy past its `until`); adding a
  policy needs an approval; closing the session revokes every grant in the closing
  transaction, including `pending_approval` ones; a grant approved after its session left
  `open` is dropped and sends no mail; a `grant.revoke` from a peer other than the grantor
  changes nothing; `peers remove` revokes; both rewind tests pass.

### 2.3a Fetch server and `fs` (review)

- Files: `internal/capability` (server), `internal/session` (register the `fetch.*`
  plaintext types), `internal/daemon`, and tests.
- Acceptance: every path-grammar rule (a table of accepted and `bad_path` inputs, including
  `..`, `a//b`, `\`, `C:`, `CON.txt`, trailing dot, NUL); **escape attempts fail**: a symlink
  to outside, a symlink to inside, a directory swapped for a symlink between two calls, `.git`
  reads **in any case** (`.GIT/config`, `.Git/HEAD`), `GIT~1` on Windows (`bad_path`), a
  Windows junction inside the directory (`symlink`), `list` hiding `.git`, a FIFO/device
  where the OS supports it (`not_regular`), a 8 MiB + 1 file (`too_large`); `scope` checked
  by segments (`internal/mail` does not match `internal/mailbox`); a fetch whose Noise
  identity differs from `aud` → `wrong_audience`; `ts` 31 s old → `stale`; a repeated `req`
  10 min later → `stale`; `length` 262145 → `malformed`; the limits (3rd in-flight op per
  grant or per holder, 21st op in a second) → `rate_limited`; a revoke between two fragments
  stops the read at the next fragment; a 10 s fetch does not delay a concurrent ping (served
  off the session goroutine); `grant.fetch` audit rows carry no path and are summarised
  beyond 60 per minute.

### 2.3b `git` serving (review)

- Files: `internal/capability` (git), and tests (a temporary repository built with the
  `git` binary; skipped with a clear message if `git` is missing, but CI has it).
- Acceptance: list/stat/read at the branch tip, with `commit` reported and changing after a
  new commit; other branches and tags unreachable; mode 120000 and 160000 entries typed
  `symlink`/`other` and not served; a path starting with `-` is passed after `--` and treated
  as a path; a repository with a hostile `core.fsmonitor`, `core.hooksPath` and a `diff`
  driver in `.git/config` runs none of them (marker files never appear); a daemon started
  with `GIT_DIR`, `GIT_CONFIG_PARAMETERS` and `GIT_CONFIG_COUNT=1`/`KEY_0`/`VALUE_0` pointing
  elsewhere still serves the granted repository and none of that configuration applies; a
  path `*.go` or `:(glob)x` is `bad_path` or matches literally; a 10 s timeout with a fake
  slow `git`; the environment variables are set and no inherited `GIT_*` survives (asserted
  through a fake `git` that prints its environment).

### 2.3c Fetch client and CLI

- Files: `internal/daemon` (`fetch_start/status`), `cmd/agentnet/grant.go`,
  `cmd/agentnet/fetch.go`, `Docs/cli/grant.md`, `Docs/cli/fetch.md`, and tests.
- Acceptance (e2e): B fetches a file of 3 MiB (fragments, several 256 KiB reads) byte-identical;
  **2.3: after `agentnet revoke <grant-id>`, the next fetch fails within one second**
  (`TestRevokeNextFetchFails`, measured from the revoke IPC return to the failing
  `fetch_status`); a fetch with the grantor stopped → `timeout` after `--timeout`; an expired
  grant fails locally without network traffic; CLI output and exit codes.

### 2.4 Sensitive grants and quarantine (review)

- Files: `internal/worksession`, `internal/daemon`, `cmd/agentnet/session.go`
  (`release`), notify event `session.quarantined`, and tests.
- Acceptance: **2.4: a session with a sensitive grant cannot deliver a result until
  released, and the audit log records the release** (`TestSensitiveQuarantine`): A's views,
  `wait` and `request show` show only sizes and status while quarantined (marker strings in
  B's summary, output, artifacts and notes appear in no A-side IPC result); `release` needs
  an approval; after it the result is visible and `ws.release` is audited; a grant revoked
  or expired before the result still quarantines it; a `--public` git grant does not; a
  sensitive grant that was never approved does not; a result in a **second, grant-less
  session** with the same peer is quarantined while that peer's sensitive grant from the
  first session is within 7 d of its `exp`; an early `request.complete` carrying marker
  strings and a `ws.cancel` reason with a marker leave no marker on A (review 24, H3); after
  `request-changes` the next round is quarantined again.

### 2.5 Consult

- Files: `internal/request` (`context`, `MaxQuestionBody`), `internal/worksession`
  (accept + result in one transaction), `cmd/agentnet/consult.go`, `Docs/cli/consult.md`,
  and tests.
- Acceptance: caps (8/9 files, 65536/65537 bytes per text, a control character, a binary
  file refused by the CLI, `context` on a `review` → `bad_request`, total 327680/327681 →
  `request_too_large`, and `bad_body` on the receive path); the submit result carries the
  derived `session`; `result` on a pending question accepts, opens and submits atomically;
  **2.5 (in-process part):** `wait <session> --timeout 300` returns the answer
  (`TestConsultRoundTrip`); context text appears in no audit row (sizes only).

### 2.D1 Device link (review)

- Files: new `internal/device` (link, offers, hierarchy), migration 17, `internal/daemon`,
  `cmd/agentnet/device.go`, `Docs/cli/device.md`, and tests.
- Acceptance: the link-id vector; e2e: both sides confirm (fake approvals) in either order →
  both `active` with the same `link_id`; only one side confirms → nothing after 10 min;
  a wrong fingerprint → `fingerprint_mismatch` and nothing changes; an offer from a peer with
  no local intent creates nothing; **no team, roster, pairing, `team.join` or `peers verify`
  path creates a `device_links` row** (`TestDeviceTrustOnlyFromLinkFlow`, driving each of
  those flows between two daemons and asserting the table stays empty); reverse link and
  chains → `device_cycle`; unlink from either side ends it on both; **asymmetric activation**
  (the controller's intent expires before the helper's offer arrives, so only the helper is
  `active`): `device unlink` on the controller still sends `device.unlink` and ends the
  helper's link; a `device.unlink` from a third peer naming the link id changes nothing;
  both rewind tests pass.

### 2.D2 Helper scope and runner (review)

- Files: `internal/device` (scope, in-scope checks, runner with `runner_windows.go` /
  `runner_unix.go`), `internal/request` (`run` member), `internal/daemon`,
  `cmd/agentnet/device.go`, and tests.
- Acceptance: scope validation (each field at its limit and one over; `argv[0]` resolved
  to an absolute path at set time); every out-of-scope check sends the request to the normal
  inbox with the right `device.out_of_scope` check, and nothing runs; in scope: auto-accept,
  the command runs in the repo with only the listed environment (a test program prints its
  environment; a `SECRET_TOKEN` in the daemon's environment does not appear; on Windows a
  `go env GOCACHE` command succeeds with the minimal environment), exit 0 →
  `pass`, exit 3 → `fail` with `exit_code` 3, the output tail is ≤ 32768 bytes with ANSI
  sequences removed; the timeout kills a child-of-child process (process tree); a request
  created before the link was activated does not run; unlink on the helper → the next
  request goes to the inbox; the command name, argv and output appear in no audit row.
  A manual check on each OS goes into `tests/phase2-manual.md` (created here).

### 2.9 End-to-end and audit

- Files: `internal/daemon` e2e, `tests/phase2-manual.md`, docs reconciliation.
- Acceptance: one e2e test runs the whole Phase 2 loop: A requests → B accepts (session
  opens) → A grants `git.read` (policy) and `fs.read` (approval) → B lists and reads → A
  revokes one grant → B's next fetch fails → B submits a result → quarantined → A releases
  → A requests changes → B submits again → quarantined again → release → accept-result →
  both sides `closed` and the request `completed` on both. Then a consult round trip and one
  helper run. **Audit has no content** (`TestPhase2AuditHasNoContent`): marker strings placed
  in file contents, paths, labels, branch names, scopes, results, notes, changes, cancel
  reasons, context files, helper command names, argv and output appear in no `audit_events`
  row on either side. The docs match the behaviour.

### 2.H Headless harness run (2.7)

- Files: `tests/harness/phase2-agents.ps1` and `.sh`, `tests/harness/README.md`,
  `Docs/agents/snippet.md` (Phase 2 commands), the record in `tests/phase2-manual.md`.
- The script starts a loopback relay and two daemons with `DORYLINAE_APPROVAL=terminal`, so
  the **script** (never the agent) reads the approval codes from the daemons' stderr and
  confirms them. It drives real agents headless through **request → accept → grant → fetch
  → consult → result → accept-result**:
  - agent A is told in plain words to ask B to review a directory of a small fixture repo
    and to give B read access to it, then to wait for the result and accept it; separately to
    consult B with one context file;
  - agent B is told to check its inbox, accept, read the granted files through AgentNet,
    and return a result; and to answer the consult.
  Prompts do not name subcommands; agents learn them from the snippet and `--help`.
- Harnesses: **Claude Code** (`claude -p`) and **agy** (Antigravity CLI), each once as A and
  once as B, like 1.H (both work locally on the owner's machine without admin; never
  `agy --sandbox`, which raises a UAC prompt). **Codex CLI** is optional (its account limit
  lifts 2026-10-02). **Hermes** from the plan is not installed and is dropped for Phase 2
  unless the owner adds it.
- Acceptance (asserted from `--json`, not agent text): both sessions `closed` with
  `outcome: accepted`; the grants were used (`grant.fetch` rows on A) and are revoked after
  close; the consult's `wait` returned the answer; exactly one request per task; the run
  finishes within 15 minutes.
- **CI weekly (plan 2.7):** real harnesses need model API keys and logged-in CLIs, which are
  paid secrets; the owner has not approved spending them in CI. Proposal (OD-P2-12): a weekly
  CI job runs the **same script with a scripted stand-in agent** (a small Go program that
  issues the CLI calls a real agent would, from the snippet's documented commands) on
  Linux, Windows and macOS runners, which is free on the public repository; the real-harness
  run stays manual before every release and before 2.P, recorded in
  `tests/phase2-manual.md`.

### 2.P Phase 2 push

`main` to origin after 2.9 and 2.H; the `phase-2` tag only with the owner's OK; HANDOFF
updated.

## Owner decisions needed

**Decided.** All of OD-P2-1..15 were approved by the owner on 2026-09-23 as recommended:
OD-P2-6 = **(c)** (the specs now describe (c) throughout), OD-P2-2's confinement boundary is
accepted, and (d) (an OS user-presence check) is deferred to Phase 3/4 hardening. D1–D15 are
not reopened; OD-P2-8 is an interpretation of D13's wording, not a change of it.

| # | Decision | Options | Recommendation |
|---|---|---|---|
| OD-P2-1 | Capability token format (plan: Biscuit) | (a) in-house Ed25519-signed canonical JSON, versioned; (b) `biscuit-go` v2 | **(a)**. No new dependency (Biscuit adds protobuf and a Datalog evaluator on attacker input), vectors checkable by `verifyvectors`, and the one Biscuit feature we would not have (holder attenuation) is not needed while grants are audience-bound and enforced by the grantor. `v` leaves room to switch later |
| OD-P2-2 | How the "human approval prompt" is made agent-proof (**amended by review 24**) | (a) a 6-digit code shown only in the desktop notification, quoted by `agentnet approve`, with the review-24 hardening (check value only in daemon memory, never in argv, 10 wrong codes per 24 h, precondition re-check); (b) a plain `approve` command (any agent can run it); (c) no approval, policies only; (d) later: an OS user-presence check (Windows Hello `UserConsentVerifier`, macOS LocalAuthentication) on top of (a) | **(a) now, (d) as a Phase 3/4 hardening item.** (a) stops a prompt-injected agent that uses AgentNet's interface. It does **not** stop an agent that runs arbitrary programs as the user and attacks the account (reads the notification history or the database, restarts the daemon): that needs **harness confinement** (no access to the config dir, no control of `agentnetd`, no screenshot/notification-history tools), documented in the snippet. The owner should confirm this boundary is acceptable for the beta |
| OD-P2-3 | Approval on machines without a desktop | (a) `DORYLINAE_APPROVAL=terminal` at daemon start (code on the daemon's stderr; review 24: stderr must be a terminal unless `DORYLINAE_DEBUG=1`, the start is audited and announced on the desktop if there is one); (b) no approvals there (policies set elsewhere cannot exist either, so no grants) | **(a)**, documented as weaker. It is also what the 2.H harness uses (with `DORYLINAE_DEBUG=1`) |
| OD-P2-4 | Read-only database URL grants (plan 2.2 example) | (a) defer; (b) sealed hand-over of a URL (not revocable); (c) a query proxy | **(a)**. (b) fails "revocable", (c) is a large new attack surface, and no Phase 2 acceptance test needs a database |
| OD-P2-5 | Who may grant in a session | (a) the requester only; (b) either party | **(a)**. The plan's wording; one direction keeps quarantine semantics simple |
| OD-P2-6 | Leaving `awaiting_result` / `quarantined` other than by accept-result or request-changes (**amended by review 24, decided by the owner 2026-09-23**) | (a) not allowed (the diagram exactly); (b) cancel from `awaiting_result` and `quarantined` (→ `closed`, `cancelled`); (c) from `quarantined` only: **discard** (→ `closed`, `cancelled`) and **request-changes without release** (→ `open`, `round + 1`), both without an approval and with the result deleted unseen | **Decided: (c)** (changed from (a)). Under (a) a human who distrusts a quarantined result can only get rid of it by releasing it, which exposes it to the agent the quarantine protects: the one exit adds exposure. Both (c) moves reduce exposure, so they need no approval. It adds two edges to the diagram; 2.1a adds them to `TestSessionStateMachine` and work-session.md has them in its transition table |
| OD-P2-7 | Deciding a quarantine release | (a) on metadata (status, sizes) and out-of-band knowledge; (b) add a human-only preview behind an approval | **(a)** for Phase 2; (b) if beta users ask |
| OD-P2-8 | Representation of D13's `device` trust | (a) its own table `device_links`, not a `peers.trust` value; (b) add `device` to `peers.trust` (a peers rebuild) | **(a)**. `peers.trust` ranks key authenticity and is written by pairing, teams and `peers verify`; a separate table cannot be raised by any of those paths, which is D13's point. It is still called "device trust" in the docs |
| OD-P2-9 | Depth of the one-way hierarchy | (a) depth 1: a device is only helper or only controller, no reverse links; (b) allow chains without cycles | **(a)**. Simplest, and nothing in Phase 2 needs chains |
| OD-P2-10 | What an own-device helper does with in-scope requests | (a) the helper **daemon** runs an allowlisted command (argv fixed on the helper) and returns exit code + output (the D14 result); (b) auto-accept only, and a local agent on the helper does the work | **(a)**. It needs no agent on the helper and uses D14 as designed; the request carries only a command **name** |
| OD-P2-11 | Does every accept open a session | (a) yes (plan: "created on accept"), `complete` becomes a result shorthand; (b) opt-in sessions | **(a)**. One path; Phase 1 commands keep working through the shorthand |
| OD-P2-12 | 2.7 "all harnesses pass in CI weekly" | (a) weekly CI with a scripted stand-in agent (free), real harnesses (Claude Code + agy, Codex optional) manual before releases; (b) put model API keys into CI secrets (paid, per run); (c) no CI job | **(a)**. It keeps the loop tested weekly at no cost; the real-agent run is the release gate |
| OD-P2-13 | Default sensitivity | `fs.read` always sensitive; `git.read` sensitive unless `--public` | **As specified**. The daemon cannot tell a private repo from a public one |
| OD-P2-14 | Which git state a grant serves | (a) the branch tip at each call, with `commit` reported; (b) a commit pinned at grant time | **(a)**. A reviewer after "changes requested" sees the new commits without a new grant |
| OD-P2-15 | Content from a worker **outside** the session result while a sensitive grant is live (**new, review 24**) | (a) not quarantined: B's new requests, consults and reply notes to A are ordinary untrusted peer text, as in Phase 1 (documented); (b) while the [quarantine rule](../protocol/work-session.md#quarantine-24) holds for any session with B, A withholds every content member of B's mail (briefs, titles, context, notes, reasons) from IPC views, showing sizes only, until a human runs `agentnet release --peer B` | **(a) for Phase 2**, with the limit stated in work-session.md (done). Any peer can already send A a brief, so (b) protects only against a worker that first read sensitive data; revisit if beta users rely on quarantine as containment. Review 24 already closed the side doors **inside** the request (early `request.complete`, `ws.cancel` reason) and across sessions with the same peer (7-day rule) |

## Conflicts and interpretations found while writing

1. **"Session" is taken.** `session.md`, envelope types `session.*`, audit `session.open` /
   `session.reject` and `internal/session` are the Noise transport session. Phase 2 uses
   "work session" with `s-` ids, `ws.*` kinds and audit, `ws_*` IPC, a new document, and a
   naming note in `session.md`; the CLI keeps `sessions`/`session`.
2. **Who enforces a grant.** The plan says "the peer's daemon exposes granted resources
   only through `agentnet fetch`". The resource lives on the grantor, so the **grantor's**
   daemon enforces; the holder's `fetch` is a client. This is also what makes revocation
   immediate.
3. **Plan 2.1 lists three states** (`open, awaiting_result, closed`) while the diagram also
   has `Quarantined` and changes-requested; the spec follows the diagram.
4. **2.6 result vs D14 result** are merged into one object (D14 members + `verification` +
   `notes`), validated by the same code.
5. **Request object is strict.** The new optional members `context` and `run` are rejected
   (`bad_body`) by Phase 1 daemons; mixed versions are a documented beta limitation.
6. **CLI 2-second rule vs `wait` and `fetch`.** Both are CLI-side loops of sub-2-second IPC
   calls, like `ping`'s pending/poll behaviour.
7. **Noise message limit.** `session.data` plaintexts are at most 65535 bytes, so fetch
   content is fragmented (32 KiB per message).
8. **D13 wording "separate `device` trust"** is implemented as a separate table, not a
   `peers.trust` value (OD-P2-8).
9. **`internal/capability`** already exists as an empty placeholder; 2.2b fills it.
10. **Plan 2.7** names Codex and Hermes; the harnesses that run on the owner's machine today
    are Claude Code and agy (1.H), Codex is limited until 2026-10-02, and Hermes is not
    installed.
11. **Plan 2.2 names a remote resource** (`--resource github.com/org/repo#branch`). Grants
    serve what the grantor's daemon can read, so the resource is a **local clone path**
    (`--resource C:\src\repo#branch`); the grantor never fetches from a remote on the
    holder's behalf.
