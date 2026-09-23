# 24: Phase 2 spec review (adversarial)

Reviewer: P2-SpecReviewer (Opus), 2026-09-23. Target: branch `p2/specs` at `d06c6d7`,
worktree `AgentNet-wt/p2-specs`. Scope: `Docs/protocol/{work-session,approval,grant,consult,device}.md`
(new), the Phase 2 edits to `README.md`, `request.md`, `mail.md`, `ipc.md` and `session.md`,
and the ticket plan `23-phase2-tickets.md`. Checked against plan Phase 2 (steps 2.1–2.7 and
the state diagram), HANDOFF §3 (D1–D15), the Phase 1 specs, and the code where a claim
depends on it (`internal/session`, `internal/relay`, `internal/ipc`, `internal/notify`,
`internal/request`, `internal/store`, `internal/mail`).

## Verdict

**Ready with changes, and two owner decisions.** The changes are applied in this worktree.
There are no Critical findings. There are 3 High findings, all fixed in the docs: the
approval code could be recovered from SQLite in about a second (H1), it passed through
world-readable argv (H2), and the quarantine could be bypassed through the request's own
`request.complete` (H3). There are 11 Medium findings: 9 fixed in the docs, 1 moved to an OD
(M10 → OD-P2-6 amended), and M1 fixed in the docs with its boundary put to the owner (OD-P2-2
amended). There are 17 Lows: 9 fixed and 8 listed. No D1–D15 decision is reopened.

| Severity | Found | Fixed in docs |
|---|---|---|
| Critical | 0 | — |
| High | 3 | 3 |
| Medium | 11 | 10 (M10 is an OD) |
| Low | 17 | 9 |

**OD changes:** OD-P2-2 amended (the approval's boundary and a later OS user-presence
option), OD-P2-3 amended (terminal-mode hardening), OD-P2-6 **recommendation changed from
(a) to (c)**, and new **OD-P2-15** (worker content outside the result). The other ODs are
confirmed (below).

## Vectors (recomputed independently)

A throwaway Go program in `%TEMP%` (stdlib `crypto/ed25519`, `crypto/sha256`,
`crypto/hmac`; deleted afterwards) reproduced every vector byte for byte:

| Vector | Spec value | Result |
|---|---|---|
| Keys from seeds `00…1f` / `20…3f` | `A6EHv_…Mbg` / `Kay64U…bdc` | match |
| Work-session id | `s-36375782ceb6baea9cee4d4273dfb035` | match |
| Canonical grant SHA-256 | `78da9339…abe239d` | match (keys in byte order, nested too) |
| Grant signature | `l3c5wKLJ…Ca_CCg` | match |
| Approval `code_hash` (old) | `d0260c13…25db4bda` | match, **and brute-forced back to `482913` in under 1 s** (H1) |
| Device link id | `l-ee417e25d0625afad79d54cd3539dd64` | match |
| Approval `code_mac` (new, H1) | `8580a349…680bb5ce` | computed here and recorded in approval.md |

The negative checks in grant.md fail at the stated steps (step 1 for `"write":true` and
`"sensitive":1`, step 4 for the widened `exp` and the removed `scope`, step 5 for a third
key, step 6 at `exp`).

## What holds (checked, no finding)

- **Token forgery and widening.** Ed25519 over a domain-prefixed canonical grant, verified
  over the generic parse, plus the grantor's byte comparison with its stored row (step 9).
  A widened member fails step 4 on both sides. A validly signed but different token (for
  example a second token the grantor signed for the same id) fails step 9.
- **Audience confusion.** The grantor checks `aud` against the identity the Noise session
  authenticated (static-key binding plus prologue, `session.md`), not against anything in
  the message. A stolen token is useless without the holder's identity key. Step 7 binds the
  token to a session whose derived id covers both keys and the request.
- **Replay.** Noise counters and in-memory keys stop replays within and across daemon runs.
  `ts` and `req` bound only relay-queued delivery (L2 aligns the dedupe window).
- **Revocation latency.** The grantor is the enforcer and checks its own row on every
  message and before every fragment, so revocation needs no propagation. At most the
  fragments already handed to the relay (now ≤ 8) still arrive (clarified in grant.md).
- **Work-session state machine.** The table matches the diagram edge for edge. Accept and
  open share one transaction, so `Accepted` is never observable alone. A single writer (A)
  plus `seq` gives the mirror no races. The derived-id check on every kind stops a body
  being moved to another session. D11 is intact: `request.cancel` still ends at accept, and
  after accept there is only the session's `Open → Closed: cancel` edge.
- **Consult sizing.** 327680 bytes plus mail framing is well under `MaxMailPlaintext`
  (716800, `internal/mail/mail.go`). `request_show` with a full context stays under the
  1 MiB IPC line, even with worst-case JSON escaping.
- **Noise fragment size.** 32768 raw bytes is about 43.7 KB of base64 in the plaintext, under
  the 65535-byte Noise limit. The envelope is about 59 KB, under `MaxFrameBytes` (1 MiB).
- **Device trust separation (D13).** A separate table that only the link handlers write,
  with both sides confirming through a local approval and a typed fingerprint, a one-way
  depth-1 hierarchy checked locally on each side, a scope held only on the helper, `run`
  as a name only, and `argv[0]` pinned at set time. It meets every clause of D13.
- **Migrations 14–17.** None references `peers` or `requests`, so no rebuild is needed. The
  DROP-list note names the right tests (`TestMigration8PreservesPeers`,
  `TestMigrationAddsPeerTrust` in `internal/store/store_test.go`), and both tests rewind by
  dropping tables, so the new tables must be listed.

## High (all fixed)

| # | Area | Finding | Fix (file) |
|---|---|---|---|
| H1 | Approval | `approvals.code_hash` = SHA-256 of a public id and a **6-digit** code, stored in SQLite. Anything that can read the database file (for example an agent with a shell, which runs as the same user) tries all 10⁶ codes in about a second (reproduced) and approves itself. The spec presented the hash as the protection | The check value is `HMAC-SHA256(approval_key, …)`, kept only in daemon memory. `approval_key` is random per daemon start and never written. **No code material in SQLite.** A restart expires pending approvals. New vector (approval.md §Object; ticket 2.2a acceptance searches every table for code material) |
| H2 | Approval / notifier | The code goes to the notifier the same way as other notifications. On **Linux** that is `gdbus`/`notify-send` **argv**, and on **macOS** `osascript` argv (`internal/notify/desktop_linux.go`, `desktop_darwin.go`). `/proc/<pid>/cmdline` and `ps` show argv to **every local user**, so a polling loop reads the code. On Windows the toast stays in a notification history that same-user programs can read | New §Delivering the code: Linux uses an in-process D-Bus call (`godbus/dbus/v5`, already in the module graph through `go-keyring`) with no argv fallback for approvals. macOS passes the text through the environment (`system attribute`). Windows tags the toast, sets `ExpirationTime` and removes it from history on decision. Ticket 2.2a asserts that no argv of any child process contains the code (approval.md, 23 §2.2a) |
| H3 | Quarantine (2.4) | B is authoritative for the **request**, so B can send `request.complete` with a D14 result (output ≤ 32 KiB, artifacts) at any time after accept. A's mirror applies it, and `request_show` shows it: **the quarantine is bypassed inside the same request**. The same holds for the text of a `ws.cancel` reason, and for returning the data in a second, grant-less session or a consult with the same peer | New §Early complete: a `request.complete` on A while the session is not `closed` has its result and note **dropped unread** when the quarantine rule holds, and an `open` session is then closed `cancelled` (the existing `Open → Closed` edge, grants ended). The `ws.cancel` reason is not stored while quarantined. The quarantine rule now also holds when A gave the same peer a sensitive grant whose `exp` is less than 7 d ago. The overclaim ("cannot consume anything the worker produced") is corrected. The remaining channel (B's **new** mail) is OD-P2-15 (work-session.md, 23 §2.1a, §2.4) |

## Medium

| # | Area | Finding | Fix (file) |
|---|---|---|---|
| M1 | Approval threat model | The model said the gate stops "a prompt-injected local agent" with the user's rights. It cannot stop one that deliberately attacks the account: reading the notification history (Windows toast-history API or `wpndatabase.db`), writing the SQLite database directly, or stopping the daemon and restarting it with `DORYLINAE_APPROVAL=terminal` to read the code. Terminal mode under a service manager would also put codes into the journal | The boundary is stated plainly, with **harness confinement** (no config-dir access, no control of `agentnetd`, no screenshot or notification-history tools) documented in the snippet (2.2a files). Terminal mode refuses to start unless stderr is a terminal (except with `DORYLINAE_DEBUG=1`, for 2.H), audits `approval.mode`, and announces itself on the desktop. **OD-P2-2 and OD-P2-3 amended** (approval.md) |
| M2 | Approval | Guessing budget: 20 approvals/h × 3 attempts = 60 guesses/h, so a looping agent had about a **4 % chance a month** | At most 10 wrong codes per rolling 24 h, persisted across restarts, then `approval_locked` plus a desktop notification: under 0.4 % a year (approval.md) |
| M3 | Fetch / IPC / relay | A 1 MiB `read` returns about 1.4 MiB of base64 through `fetch_status`, over the **1 MiB IPC line limit** (`internal/ipc/ipc.go` `maxLine`), so it could never work. Also, 32 fragments × 4–8 reads in flight exceed the relay's 64-frame per-connection buffer. The overflow goes to the recipient's **persistent** relay queue (`directBusy` → `enqueue`, 1000 envelopes / 32 MiB), where it competes with the holder's mail (`queue_full`) | `read` ≤ 256 KiB (8 fragments). 2 in flight per grant and per holder, so at most 16 fragments travel towards one holder. Fetches run on a worker pool, off the session goroutine (L14) (grant.md, 23 §2.3a/2.3c) |
| M4 | `fs` serving | The `.git` exclusion was a plain name check. On NTFS, APFS and HFS+ `.GIT/config` reaches the same file, and on Windows so does the 8.3 alias `GIT~1`. A **junction** is not `ModeSymlink` on Go ≥ 1.23 (it is `ModeIrregular`), so "symlink anywhere → refuse" missed it. `.git/config` can hold credentials (remote URLs with tokens) | Case-insensitive `.git`. On Windows, 8.3-shaped segments → `bad_path`. Any intermediate component that is not a plain directory → `symlink`. `list` hides `.git` (grant.md §Serving `fs`, 23 §2.3a) |
| M5 | `git` serving | The fixed variables were *added to* the daemon's environment. An inherited `GIT_DIR`, `GIT_WORK_TREE`, `GIT_OBJECT_DIRECTORY`, `GIT_CONFIG_PARAMETERS` or `GIT_CONFIG_COUNT`/`KEY_n`/`VALUE_n` redirects the repository or injects configuration. Peer paths were also pathspecs (`*`, `[`) | Every inherited `GIT_*` is removed first. Added: `GIT_LITERAL_PATHSPECS=1`, `GIT_NO_REPLACE_OBJECTS=1`, `GIT_ATTR_NOSYSTEM=1`. `git` is resolved once at start (grant.md, 23 §2.3b) |
| M6 | Mixed versions | Phase 2 B with a Phase 1 A: `complete` became a shorthand for `ws.result`, and A acks that `unsupported`, so **B can never complete a Phase 1 requester's request**. Phase 1 B with a Phase 2 A: B's `request.complete` left A's session `open` with **live grants** until `exp` | Fallbacks: B completes through the Phase 1 path when a `ws.*` mail fails `unsupported_kind`. A treats `request.complete` before `closed` as an early complete (H3) (work-session.md, 23 §2.1a) |
| M7 | Approval / grants | An approval confirmed up to 10 min later performed the action with no re-check. A grant could be activated and mailed after its session closed or the peer was removed. Closing a session did not touch `pending_approval` grants | `approval_confirm` re-checks every precondition in its transaction. Session close revokes pending grants too (approval.md §Flow, grant.md) |
| M8 | Grants / device | `grant.revoke` and `device.unlink` were applied **by id**, without checking that `msg.from` is the grantor or the linked peer. Any paired peer that learned an id could revoke another peer's grant on the holder, or unlink another device | Both are applied only to rows whose peer is `msg.from` (grant.md §Kinds, device.md §Unlink) |
| M9 | Device (D13) | Activation is independent on each side, so one side can be `active` while the other's intent expired before the peer's offer arrived. The inactive side then got `unknown_link` from `device unlink` and **could not revoke**, which D13 requires | `device_unlink` always sends `device.unlink`. On arrival it revokes every link, intent and offer with `msg.from`. `link` is optional (device.md, 23 §2.D1) |
| M10 | Session state machine | In `quarantined` the only exit is `release`. A human who distrusts a result can get rid of it only by **exposing it to the agent the quarantine protects**. The session can't be cancelled or sent back without release | **OD-P2-6 amended**, recommendation (c): from `quarantined`, *discard* (→ `closed`, `cancelled`) and *request-changes without release*, neither needing an approval (both reduce exposure). This adds diagram edges, so it is the owner's call; the specs still describe (a) (23 §OD) |
| M11 | Ticket plan | 2.D1 (migration 17) was marked ∥ 2.2c (migration 16) with only "2.2a" as its dependency, so it could merge before 16. Missing review markers: 2.3c parses peer-controlled fragments and writes files, and 2.5 changes the request decode caps and adds an accept-and-submit path | 2.D1 "merge after 2.2c". 2.3c is **yes** (batched with 2.3b), 2.5 is **yes** (light, with 2.4) (23) |

## Low

Fixed in the docs:

- **L1** The helper runner's Windows environment lacked `LOCALAPPDATA`/`APPDATA`/`SystemDrive`/
  `windir`, so the doc's own example (`go test`) fails ("GOCACHE is not defined") (device.md;
  2.D2 test).
- **L2** The `req` dedupe (60 s) was shorter than the `ts` window (+10 min); it is now 11 min
  (grant.md).
- **L3** The quarantine rule counted grants "ever issued". A grant that was never approved
  gave no access; the rule is now "ever active" (work-session.md).
- **L4** Policies had no end of their own, and matching on branch and `--public` was not
  specified. Policies now have `until` (default 30 d, max 90 d), an exact branch, and
  `public` matching; columns were added (grant.md).
- **L5** The approval subject list missed `p-` (policy) and `i-` (device intent), and the
  audit reasons missed `locked` and `precondition` (approval.md).
- **L6** The scope approval and the CLI now show each command's resolved `argv[0]` and full
  argv, so the human approves what will run (device.md).
- **L7** The worker's mirror did not copy `verification` from `ws.state` (work-session.md).
- **L8** Plan 2.2's `--resource github.com/org/repo#branch` became a local clone path
  without saying so. Recorded as interpretation 11 (23).
- **L14** `internal/session` handles `session.data` on one worker goroutine under `m.mu`, so
  a 10 s `git` call would stall pings and every session. Fetches now run on their own worker
  pool (grant.md §Transport; 2.3a test).

Open (backlog or implementer's choice; none blocks approval):

- **L9** grant.md issuance step 4 says "1 min to 7 d, and additionally capped at 7 d": the
  second part is redundant.
- **L10** If `request.cancel` and accept cross, the request ends `accepted` with an `open`
  session that the requester wanted gone. Consider auto-applying a session cancel when the
  accept arrives for a request whose cancel is pending.
- **L11** A modified B can make A create a session with a `ws.result` without ever
  accepting (step 3 creates the row while A's mirror is `pending`). It is harmless, because
  A still decides, but it leaves the request `pending` on A.
- **L12** Unix runner: a daemon crash leaves the process group running (Windows job objects
  kill on close). Use `Pdeathsig` on Linux, or record the pgid and kill it at start.
- **L13** A command already running is allowed to finish after unlink, for up to
  `timeout_s` (≤ 1 h). Consider `device unlink --kill`.
- **L15** `peers remove` of the other device does not send `device.unlink`. If the same key
  is paired again, the remote side's old link is still `active`.
- **L16** In-scope check 5 compares the controller's `created` clock with the helper's
  `activated_at`, so skew can push the first requests after linking out of scope. That is
  safe but confusing; the CLI should show the `created` check.
- **L17** Fragments for an offline holder are kept (as ciphertext) in the relay's queue for
  the queue TTL, although they are useless after 10 s. Consider an envelope TTL for
  `session.data` when M2 of review 05 (relay abuse limits) is done.

## OD list: completeness and soundness

| OD | Assessment |
|---|---|
| OD-P2-1 in-house token | Sound. No Biscuit feature is needed while grants are audience-bound and grantor-enforced; `v` leaves room. Confirmed |
| OD-P2-2 approval | **Amended** (M1, H1, H2): (a) with the hardening; the confinement boundary is stated; (d) OS user-presence is added as later hardening. The owner should confirm the boundary |
| OD-P2-3 headless | **Amended** (M1): the TTY requirement, the debug exception for 2.H, and the audit and announcement on start |
| OD-P2-4 database deferred | Sound. (b) fails "revocable" |
| OD-P2-5 requester grants only | Sound |
| OD-P2-6 exits from quarantine | **Recommendation changed to (c)** (M10) |
| OD-P2-7 release on metadata | Sound for Phase 2; interacts with OD-P2-6 (c), which gives a way out without viewing |
| OD-P2-8 `device_links` table | Sound, and an interpretation of D13, not a change of it |
| OD-P2-9 depth 1 | Sound |
| OD-P2-10 helper daemon runs allowlisted argv | Sound. It needs L1 on Windows |
| OD-P2-11 every accept opens a session | Sound with the M6 fallbacks. Without them it broke mixed teams |
| OD-P2-12 weekly CI stand-in | Sound. The real-agent run stays the release gate |
| OD-P2-13 sensitive by default | Sound |
| OD-P2-14 branch tip per call | Sound. `commit` makes the snapshot visible |
| **OD-P2-15** (new) | Worker content **outside** the session result (new requests, consults, notes) while a sensitive grant is live: (a) document, or (b) hold all of B's content on A until release. Recommendation (a) for Phase 2 |

## Implementability

After these fixes each ticket can be built from the docs alone. Two choices are left to the
implementer and are named: the Windows toast-history removal call (PowerShell script,
fixed text, tag/group through the environment) and the Linux D-Bus call signature (the
same `Notify` arguments as the current `gdbus` call). The migration order (14 → 17) now
matches the dependency graph, and no ∥ pair adds migrations that could merge out of order.

## Files changed by this review

- `Docs/protocol/approval.md`
- `Docs/protocol/grant.md`
- `Docs/protocol/work-session.md`
- `Docs/protocol/device.md`
- `Docs/review/23-phase2-tickets.md`
- `Docs/review/24-phase2-spec-review.md` (new)
