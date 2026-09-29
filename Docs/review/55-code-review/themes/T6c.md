# T6c: Spec-versus-code drift, Phases 3–4

Reviewer: TH-T6c · model claude-opus-5-5 · elapsed 243 min wall clock (includes a usage-limit pause of unknown length; five read-only helper sub-agents built the first-pass drift tables, and every candidate below was re-read by me at the cited lines) · commit 6cc26a7 (worktree HEAD ef03d14; only `Docs/review/55-code-review/*` differs)

Scope: `debate.md`, `decision.md`, `audit.md`, `experience.md`, `relay-hosted.md`,
`accounts.md` (4.2a parts), `invites.md`/`telemetry.md`/`feedback.md` (partial-code check only),
`Docs/cli/relay.md`, `doctor.md`, `status.md`, `install.md`, `log.md`, and the IPC method list
of `00-index.md` §3.a against `ipc.md`.

## Summary

| Critical | High | Medium | Low | Info |
|---|---|---|---|---|
| 0 | 0 | 1 | 13 | 8 |

**Verdict.** The Phase 3 protocol code follows its specs closely. Every debate and decision
audit row has the documented action, actor and detail keys. The commitment, the derivation
rules 1–12, the signing and hold flow, the verification steps and the Markdown rules are
implemented as written. The audit chain, the paging `Verify` and the read-only fallback
match `audit.md`. The relay's flag defaults match `Docs/cli/relay.md` and `relay-hosted.md`
§2 exactly. The accounts 4.2a state table, frames and eligibility rules match `accounts.md`.
No partial invites, telemetry or feedback code exists that a later ticket could take for
finished.

The one real drift with security weight is **T6c-01 (Medium)**. `ipc.md` §Framing and
`debate.md` §IPC "Size" (review 43 M7) say the IPC server writes results with HTML
escaping off. `marshalResult` does turn it off, but `serveConn` then writes the `Response`
with a default `json.Encoder`, which escapes `<`, `>` and `&` inside the `json.RawMessage`
again. A paired peer that fills its debate entries with `<` pushes `debate_show`,
`debate_submit`, `wait` and `decision_show`/export past the 1 MiB line, for that debate and
its signed Decision, permanently. A test confirms it.

The Lows are:
- The relay's `Close` does not wait for connection goroutines, although the spec says it does.
- A misleading `debate.broken` text on a Decision refusal.
- An audit inventory test that cannot see the debate mail kinds.
- Error mapping and CLI edge cases in debate, decision and log.
- Doctor and install diagnostics that disagree with their docs, including the missing
  `agentnet setup` command that install.sh and `install.md` point to.

The Infos are doc drift.

## Questions (04-themes.md §T6 method)

1. **Drift table per spec.** See "Drift tables" below. Every normative statement was traced.
   Matching rows are kept terse; differing rows point to a finding id or a known id.
2. **Code with no spec.** See "Undocumented behaviour".
3. **IPC method list: `00-index.md` §3.a against `ipc.md`.**
   - The daemon registers **71** methods (`srv.Handle` in `internal/daemon/*.go`). The index
     lists 70: it omits `notify_test` (`internal/daemon/notify.go:182`). That is an index
     error, not code drift; C28 counted 71 correctly.
   - Every method is named in `ipc.md`. The Phase 3 list (`ipc.md:388-401`) names
     `debate_submit/show/list/constrain`, `decision_show/list` and `audit_list/verify/head`,
     all registered. `ws_discard` is documented in `work-session.md` only (T6b's area).
   - No debug-only or test-only method is registered.
4. **invites / telemetry / feedback.** No code that a later ticket might assume is finished:
   - `quota_groups` and `quota_group_members` exist, created in relay migration R2 as
     `invites.md` §Relay storage says (`internal/relay/accounts_store.go:65-80`). Column sets
     and CHECKs match. No code creates groups outside the `testhooks` hook.
   - `relay admin group suspend/unsuspend` operates on them (`accounts_store.go:500-511`).
   - The journal names `invite_redeem`, `invite_revoke` and `team_remove`, but only in
     comments (`internal/relay/journal.go:15-16,67-68`). Replay counts such entries as
     "skipped, no handler yet" (`cmd/relay/main.go:317`), so nothing is silently treated as
     applied.
   - No `pair_admit`, `beta_invites`, `group_full`, `telemetry_*`, `feedback*` or
     `quota_exceeded` exists in the code (grep). An unknown control op still closes 1008
     (`internal/relay/pairing.go:141-164`), or gets `account_required` for an unbound or
     group-less key (`accounts.go:269-276`).
   - `relay-hosted.md` §4 quotas (4.1c) are not built. `/metrics` is not the telemetry of
     4.6a.
   - One reference to unbuilt code does exist: `agentnet setup` (4.9a). See T6c-12.

## Findings

### T6c-01 · Medium · confirmed-test
- **Where:** `internal/ipc/ipc.go:140-155` (`serveConn`: `enc := json.NewEncoder(c)` with the
  default HTML escaping, then `enc.Encode(resp)`); the intended fix is at `:194-203`
  (`marshalResult`, `SetEscapeHTML(false)`) and is undone by `:153`.
- **What goes wrong:** `marshalResult` encodes a result without HTML escaping into a
  `json.RawMessage`. The outer encoder then compacts that RawMessage with `escapeHTML=true`,
  which is `encoding/json`'s behaviour for `Marshaler` values, so every `<`, `>` and `&` in
  every IPC result is still sent as a 6-byte `<` escape. No result size is checked
  against the client's 1 MiB `maxLine` (`ipc.go:28`, `:258`).
- **Scenario:** a paired peer takes part in a debate with the user (as either side). Each of
  its up to 7 entries is filled to about 32 KiB with `<` in `argument`, `evidence.ref`,
  `note` and the like, all valid under the debate text rule. That is about 224 KiB raw and
  about 1.3 MB as sent over IPC.
  - From then on, `agentnet debate <id>`, `wait <id>` and the agent's `debate_show` fail with
    "line too long".
  - Every `debate_submit` on that debate commits and then returns an error, so the user's
    agent believes its move failed and retries into `not_your_turn`.
  - After the close, `decision_show` of the signed Decision fails the same way. `agentnet
    decision <id> --md/--json/--out` cannot export it, for good. `decision.md` §Size puts the
    worst case at about 740 KB "under the 1 MiB IPC line", which holds only with escaping
    off.
  - Recourse: `--cancel` still works; the Decision is reachable only by reading the DB.
  - The same root cause also multiplies C28-01/C04-01's relay `last_error` 6× and O-178's
    list sizes.
  - Test: `internal/ipc/zz_review55_T6c-01_test.go`. A 200 000 × `a` result is delivered,
    while a 200 000 × `<` result (200 KB) fails with
    `ipc: read response: ipc: line too long`.
- **Spec:** `Docs/protocol/ipc.md:26-33`: "The server encodes results with HTML escaping
  **off** (`json.Encoder.SetEscapeHTML(false)`). With the default … a debate or Decision view
  full of such characters would exceed the 1 MiB line"; `debate.md` §IPC "Size" (ticket 3.1b);
  `decision.md` §Size.
- **Fix direction:** create the connection encoder with `SetEscapeHTML(false)` as well (or
  write `Response` bytes built by `marshalResult` directly). Add an end-to-end test through
  `ipc.Call` with a `<`-heavy result near the limit.
- **Related:** C28-01, C04-01 (same inflation for `status`), O-178, O-179 (no worst-case
  `decision_show` IPC test: this is the defect such a test would have caught), review 43 M7.

### T6c-02 · Low · confirmed-read
- **Where:** `internal/relay/relay.go:429-439` (`Close` starts `go c.drainClose(...)` per
  connection, then immediately `s.q.close()` and returns); `cmd/relay/main.go:237-238`
  (`rs.Close()` then `return 0`: the process exits and the `drainClose` goroutines die).
- **What goes wrong:** on SIGTERM, frames already accepted into connections' outbound
  buffers are dropped when the process exits. Read loops still running after `q.close()` get
  "database is closed" on `add`/`ack`: the sender receives `internal`, and an ack is lost, so
  the frame is redelivered later.
- **Scenario:** the operator restarts the hosted relay (deploy, upgrade) while peers
  exchange traffic. Direct-forwarded non-mail envelopes in flight (`pair.confirm`,
  `session.*`, presence) are lost silently. Mail recovers through the outbox, so it is
  delayed, not lost. Pairing can fail and must be retried.
- **Spec:** `relay-hosted.md:302`: "SIGTERM waits up to 10 s for writes, and `Close` waits for
  connection goroutines (review 05 L6) before closing the database". The doc comment at
  `relay.go:403-409` claims the same.
- **Fix direction:** wait (bounded) for the `drainClose` goroutines and the read loops (a
  WaitGroup) before `q.close()`, within the unit's `TimeoutStopSec`.
- **Related:** O-001 (at-most-once direct path; this is a certain trigger at every
  restart), C02 lead and unchecked assumption (`relay.go:439`), resolved here.

### T6c-03 · Low · confirmed-read
- **Where:** `internal/debate/decision.go:283` (`refuseOnB` → `closeMirrorTx(…, EventBroken, …)`),
  `:395` (A on a refused or bad signature), `internal/notify/trigger.go:229-230`
- **What goes wrong:** every Decision refusal fires `debate.broken`, whose only text is
  "Debate with <name> stopped: the opening position did not match its commitment". A
  refusal is caused by a hash, signature, outcome or time mismatch, never by the reveal, and
  it fires on A as well.
- **Scenario:** a paired peer with a buggy or modified daemon sends a `debate.close` whose
  hash B does not derive. B's human is told that the peer cheated at commit–reveal, and A's
  human gets the same text. The humans then look in the wrong place. The actual reason is in
  the audit (`decision.refuse {reason}`).
- **Spec:** `debate.md` §Notifications, "`debate.broken` (B): … the opening position did not
  match its commitment"; `decision.md:166-176` (the refusal notifies `debate.broken` on both
  sides); `Docs/cli/debate.md:243` ("the respondent, a bad reveal"). The two specs disagree,
  and the code takes the event from one and the text from the other.
- **Fix direction:** a separate event (or text) for a refused Decision; align the three docs.
- **Related:** none

### T6c-04 · Low · confirmed-read
- **Where:** `internal/daemon/audit_inventory_test.go:145-147` (`kindsRE` matches only
  `kinds["literal"]`; `kindConsRE` matches only `Kind…` constants), `:178-181` (it scans only
  `../request`, `../worksession`, `../device`), against `internal/daemon/mail.go:272-276`
  (`kinds[debate.MailEntry]` … `kinds[debate.MailSign]`, constants named `Mail…` in
  `internal/debate`)
- **What goes wrong:** `TestAuditInventoryIsComplete` cannot see the five debate mail kinds.
  They have no inventory row, and the test still passes. So no e2e step asserts
  `debate.entry_in`, `debate.reveal_in`, `debate.close_in`, `debate.constraint_in` or
  `decision.sign_in`, and a new debate kind added without an audit would not fail the
  build. Package tests do cover some of these rows today.
- **Scenario:** a later change drops the receiver-side audit when a paired peer's
  `debate.close` is applied. CI stays green and the owner's log silently misses
  peer-caused state changes.
- **Spec:** `audit.md:266-275`: "for each IPC method and each registered mail kind, one e2e
  step asserts …; a second test compares the table with the … mail-kind registrations in the
  source, so a method added without an entry fails".
- **Fix direction:** match `kinds\[[A-Za-z.]+\]` and resolve the constants (or build the
  kind set from `daemonKinds` as `ownedKinds` does, `internal/daemon/outbox.go:29-40`); add
  the debate rows.
- **Related:** C30-01, C31-07 (other audit tests that cannot fail)

### T6c-05 · Low · confirmed-read
- **Where:** `cmd/agentnet/debate.go:582-585` (`runDebateCancel`, `invited` branch)
- **What goes wrong:** `--reason R` is accepted but not passed to `request_cancel` on an
  invited debate. It is passed only in the `ws_cancel` branch (`:597-600`), and nothing
  reports the drop.
- **Scenario:** the user runs `agentnet debate r-… --cancel --reason "wrong topic"` before
  the peer accepts. The peer never sees the reason. `request_cancel` supports one
  (`request.md` §Cancel).
- **Spec:** `debate.md` §CLI, "`agentnet debate <id> --cancel [--reason R]`"; §Cancel ("in
  `invited`: a Phase 1 `request.cancel`").
- **Fix direction:** pass `reason` in the invited branch too.
- **Related:** C23-01 (the same branch also calls the wrong method on the respondent)

### T6c-06 · Low · confirmed-read
- **Where:** `internal/daemon/decision.go:61-66` (`decisionError` maps only
  `ErrUnknownDecision`), `internal/debate/decision.go:425-428` → `internal/debate/store.go:216-224`
  (`resolve` returns `ErrUnknownDebate` or `request.ErrAmbiguousRequest`),
  `internal/ipc/ipc.go:179-181`. Also `internal/daemon/debate.go:227-249` (`debateError`)
  for request-layer errors of the one-step accept (`internal/debate/submit.go:63-66`).
- **What goes wrong:** `decision_show` with an unknown `s-` id, or an ambiguous `r-` id,
  returns `internal` / "internal error" instead of `unknown_decision` or `ambiguous_request`.
  The same applies to `debate_submit`'s one-step accept when the request layer returns
  `BadStateError`, `ErrUnknownRequest` or `mail.ErrUnpaired`. That part is suspected: its
  paths were not all enumerated.
- **Scenario:** the user or agent runs `agentnet decision s-<typo>`, or passes an `r-` id
  that two peers used (review 45 L1). The CLI reports an internal error, and an agent may
  retry or escalate instead of fixing the id.
- **Spec:** `decision.md` §IPC: "`decision_show` | `{"id"}` (`d-`, `s-` or `r-`) | … `unknown_decision`";
  `debate.md` §IPC error list for `debate_submit`.
- **Fix direction:** map `ErrUnknownDebate` → `unknown_decision` and `ErrAmbiguousRequest` →
  `ambiguous_request` in `decisionError`; map the request-layer errors in `debateError`.
- **Related:** C23 lead (`debateError` does not map `request.BadStateError`), resolved here

### T6c-07 · Low · confirmed-read
- **Where:** `internal/audit/query.go:230-231` (`r-` → `SELECT … FROM work_sessions WHERE request_id = ?`,
  every match), `:259-262` (no session: `detail.request = ?` without a peer), `:295-297`
- **What goes wrong:** `agentnet log --session r-X` collects the sessions of **every** peer
  whose request has id `r-X`, and their rows. Request ids are unique per sender only.
- **Scenario:** the user sent `r-X` to peer D. D (or any other peer) sends the user its own
  request with id `r-X`, and it is accepted. `log --session r-X` then mixes both sessions'
  grants, approvals and events into one view. It is a view only; the chain is unaffected.
- **Spec:** `Docs/cli/log.md:18`: "Rows of another session are not shown. An `r-` id resolves
  to its session".
- **Fix direction:** return `ambiguous_request` when an `r-` id maps to sessions with more
  than one peer or role, and accept an optional peer, as the request commands do.
- **Related:** C22-03 (same ambiguity in `GetByRequestID`), C23-01, R45 L1

### T6c-08 · Low · confirmed-read
- **Where:** `cmd/agentnet/log.go:249-251` (`printVerify`)
- **What goes wrong:** `log --verify` prints "head N H (keep it as an anchor: --anchor N:H)"
  on stdout even when the chain is **broken**, so it recommends anchoring a head that may
  have been rewritten. On a log whose chain never started (read-only fallback), it prints
  `--anchor N:` with an empty hash, which `ParseAnchor` then rejects with exit 2.
- **Scenario:** the user sees "BROKEN at row 512" on stderr and, on stdout, advice to keep
  the current head as a trusted anchor. If they store it, a later `--verify` against a
  rewritten chain checks out against that anchor.
- **Spec:** `Docs/cli/log.md:60-75`: the anchor line only in the intact example; exit 5 shows
  only the stderr line.
- **Fix direction:** print the anchor advice only when `status = ok` and the head is chained.
- **Related:** none

### T6c-09 · Low · suspected
- **Where:** `cmd/agentnet/log.go:30`, `:151-156` (`logListTimeout` 15 s for the whole list
  or `--head` paging loop; `--timeout` only applies with `--verify`), `:388-389`
- **What goes wrong:** `agentnet log` (list mode, default "every match") must page through the
  whole log within one fixed 15 s. `--timeout` is accepted but ignored outside `--verify`,
  and the timeout message suggests raising it.
- **Scenario:** the owner's log grows to a few hundred thousand rows, or they use a sparse
  `--session`/`--action` filter (C26-02 measured 162 ms per 200 k-row statement). `agentnet
  log --json > all.json` then exits 1 with no remedy other than `--limit`/`--since`. Not
  measured at 10⁶ rows, hence suspected.
- **Spec:** `Docs/cli/log.md:20` (`--limit` default "every match"), `:21-23` (`--timeout`).
- **Fix direction:** apply the timeout per page, or honour `--timeout` in list mode.
- **Related:** C26-02

### T6c-10 · Low · confirmed-read
- **Where:** `cmd/agentnet/doctor.go:152-157` (daemon down: the URL comes only from
  `$DORYLINAE_RELAY_URL`), `:165` (probe roots)
- **What goes wrong:** with the daemon stopped, doctor ignores the relay URL that `agentnetd
  install --relay` baked into the service definition. So `relay` and `clock` report `skip`
  "no relay configured" exactly when the user needs them (a daemon that will not stay up).
- **Scenario:** the user installed with `--relay wss://relay.dorylinae.net`. The daemon
  crashes at start because the relay is unreachable or a TLS failure keeps killing it, and
  doctor says no relay is configured.
- **Spec:** `Docs/cli/doctor.md:48-49`: "With no relay configured (no `--relay` given to
  `agentnetd install`, and `$DORYLINAE_RELAY_URL` unset when the daemon is stopped), `relay`
  and `clock` report `skip`". That implies an installed `--relay` counts.
- **Fix direction:** read the installed service's relay argument (as `probeService*` already
  reads the definition) or a small config record written by `install`.
- **Related:** O-016 (baked-in relay URL), C28-05

### T6c-11 · Low · confirmed-read
- **Where:** `cmd/agentnet/doctor.go:259-268` (`checkConfigWith`), `internal/device/perm.go:43-61`
  (`CheckProgramOwner` checks only who can **change** the path)
- **What goes wrong:**
  - Any "writable by others" result is a `warn` (overall `ok: true`, exit 0), including a
    directory owned by another uid or a 0777 one, not only the drive-root ACL the doc names.
  - Read access is never checked. A 0755 or Users-readable config dir prints "… exists and
    is owner-only".
  - On Unix the daemon re-chmods to 0700 at start (`internal/paths/paths.go:57-64`). On
    Windows nothing tightens a readable `DORYLINAE_HOME`.
- **Scenario:** the user points `DORYLINAE_HOME` at a shared, Users-readable folder on
  Windows. Doctor says "owner-only", while another local user can read `dorylinae.db`
  (requests, results, debates, Decisions). This needs the owner's own choice of path, so it
  is at most Low (rubric §2).
- **Spec:** `Docs/cli/doctor.md:30`: "The config directory exists and is owner-only (D24/L11);
  a drive-root ACL like `Authenticated Users:(M)` is a `warn`".
- **Fix direction:** check read access too (DACL read rights and Unix group/other `r`), and
  make a non-owner or world-writable result a `fail`.
- **Related:** C16-08 (the drive-root case)

### T6c-12 · Low · confirmed-read
- **Where:** `cmd/agentnet/main.go:43-127` (no `setup` command); referenced by
  `scripts/install.sh:323` ("Next: agentnet setup"), `Docs/cli/install.md:23`,
  `packaging/homebrew/agentnet.rb:43`, and doctor fixes `cmd/agentnet/doctor.go:257,309`
- **What goes wrong:** `agentnet setup` (ticket 4.9a) is not built, but the installer, the
  Homebrew caveat, the install doc and two doctor fix texts tell the user to run it. It gives
  "unknown command", exit 2.
- **Scenario:** a new beta user follows the installer's last line and gets a usage error.
  Doctor's fix for "no identity key yet" sends them to the same missing command.
- **Spec:** `Docs/cli/install.md:23` ("Then run `agentnet setup`").
- **Fix direction:** point to `agentnetd install` until 4.9a lands, or gate the texts.
- **Related:** C15-06 (another stale install.sh text)

### T6c-13 · Low · confirmed-read
- **Where:** `scripts/install.sh:311-315` (the two binaries are replaced one at a time),
  `:67-71` (`die` always prints "nothing was installed")
- **What goes wrong:** if `agentnet` was already moved into place and the `cp`/`mv` of
  `agentnetd` then fails (disk full, permissions), the script says "nothing was installed".
  It leaves a new CLI next to an old daemon, plus a stray `.agentnetd.new.$$` file (the EXIT
  trap removes only `$tmp`). A `chmod` failure at `:313` exits through `set -e` with no
  message at all.
- **Scenario:** the user upgrades on a nearly full disk and is told nothing changed. Then
  `doctor` reports `binary fail` (version mismatch), and the daemon keeps running the old
  version.
- **Spec:** `Docs/cli/install.md` (failure paragraph: "Any failure exits 1 with 'nothing was
  installed'").
- **Fix direction:** stage both `.new` files first, then rename both; make `die` after the
  first rename say what was replaced; clean up the `.new` files in the trap.
- **Related:** O-183 (predictable `.new.$$` names)

### T6c-14 · Low · confirmed-read
- **Where:** `cmd/agentnet/doctor.go:347` (with the daemon up, the daemon's URL is checked
  against `CheckURL` using **doctor's own** `DORYLINAE_ALLOW_INSECURE_RELAY`, `:418`)
- **What goes wrong:** a daemon started with `DORYLINAE_ALLOW_INSECURE_RELAY=1` (a LAN `ws://`
  relay) is connected, but doctor, run from a normal shell, fails `relay` ("a remote relay
  must use wss://") with exit 1. The reverse also happens: doctor with the variable set passes
  a URL the daemon was not started with.
- **Scenario:** a LAN or test user gets a false `fail` from doctor while everything works.
- **Spec:** `Docs/cli/doctor.md:34`: "with the daemon up, its own reported connection state".
- **Fix direction:** with the daemon up, trust the daemon's accepted URL (or have `status`
  report whether the insecure escape hatch is in use).
- **Related:** O-023, C04 note (probe dials before `CheckURL`)

### T6c-15 · Info · confirmed-read
- **Where:** `internal/worksession/cancel.go:57-68` → `internal/debate/engine.go:220-240`
  (`AbandonTx` checks only `r.open()`)
- **What goes wrong:** `debate.md` §Cancel limits B's local abandon to "when A's daemon is
  silent … past the deadline B displays". The code abandons on every B `--cancel` while the
  debate is open, including after B's own `answer`, when B shows no deadline. A may already
  be `closing`: A refuses the `ws.cancel`, B never signs, and A's Decision stays
  `awaiting_peer` with `wait` never ending. `decision.md` §"Silence" ("or B abandoned … An
  abandoning B does not sign") and `Docs/cli/debate.md:169-174` describe the unconditional
  behaviour, so the specs disagree. A modified B could withhold the signature anyway, so
  there is no security gain from the stricter reading.
- **Scenario:** B's agent answers `accept: true` and then runs `--cancel`. A's human got
  `debate.agreed`, but the Decision is never countersigned.
- **Spec:** `debate.md` §Cancel and abandon, against `decision.md` §If B refuses or never signs.
- **Fix direction:** owner decides; align `debate.md` with the other two, or refuse the
  abandon after B's answer.
- **Related:** O-176, O-175

### T6c-16 · Info · confirmed-read
- **Where:** `internal/debate/store.go:409-414` (`audit` returns an after-commit
  `_ = s.Audit.Append(…)`), used by every `debate.*`/`decision.*` action except
  `debate.constraint` (`internal/daemon/debate_constrain.go:116`, in the tx)
- **What goes wrong:** all debate and decision audit rows are appended after the commit and
  their errors are ignored. A crash, or `SQLITE_BUSY_SNAPSHOT` (O-169), between commit and
  append leaves, for example, an applied peer close with no `debate.close_in`, or a signed
  Decision with no `decision.create`.
- **Scenario:** none attacker-driven. `audit.md` §What the chain proves already names "rows
  that were never written".
- **Spec:** `debate.md` §Human constraints (in-tx audit, which the code meets); `experience.md`
  §Audit ("in the closing transaction"). This is the same pattern as C22-05 and the R27 C1
  rule.
- **Fix direction:** owner decision: state "after commit, best effort" in `audit.md` for these
  actions, or move them to `AppendTx`.
- **Related:** C22-05, O-169

### T6c-17 · Info · confirmed-read
- **Where:** `internal/experience/experience.go:183-186` with `internal/debate/experience.go:86-94`,
  `internal/worksession/transitions.go:240`, `phase1.go:73`, `internal/debate/apply.go:443`,
  `internal/debate/decision.go:283`, `experience.go:105-218`
- **What goes wrong:** the experience record drifts from `experience.md` §Record:
  - `verification` is `""` (outside the enum) for every debate and every cancelled work
    session, and `verification_by` is missing.
  - `team` is never set for debate records.
  - B's own broken-reveal or refused-Decision close is recorded with
    `failed.cancelled_by: "initiator"` and without `acceptance.decision`.
  - Level-3 truncation returns an over-cap record silently. This last point is suspected:
    `approach.grants` has no cap here, and whether grants per session are bounded elsewhere
    was not traced.
- **Scenario:** none; nothing reads the record in Phase 3. The later learning feature
  inherits out-of-schema data.
- **Spec:** `experience.md` §Record.
- **Fix direction:** fill `verification` for every close (`none` for cancelled), set `team`,
  pass the right `cancelled_by`, and fail or cap the grants list at level 3.
- **Related:** C22-06, C24-03

### T6c-18 · Info · confirmed-read
- **Where:** `internal/decision/verify.go:466-494` (context `{name, bytes, sha256}`)
- **What goes wrong:** third-party `decision verify` checks context entries more loosely than
  the daemon's request validator (`internal/request/validate.go:159-175`):
  - `bytes` up to 1 MiB against 65 536;
  - no limit of 8 files;
  - `.` and `..` accepted as names.
  A file no daemon could derive can still verify, if both keys signed it.
- **Scenario:** two colluding keys produce such a file. It proves nothing extra (they could
  sign anything), so there is no impact.
- **Spec:** `decision.md` §Signed file step 1 ("the same validators as the daemon").
- **Fix direction:** reuse the request context validator.
- **Related:** C29-02

### T6c-19 · Info · confirmed-read
- **Where:** `cmd/relay/main.go:56-72,208-228`, `internal/relay/accounts.go:364-368`
- **What goes wrong:** `Docs/cli/relay.md` lags the code:
  - Undocumented: `--db`, `--accounts`, `--metrics-listen`, `--security-journal` and the
    `backup`/`restore`/`admin` subcommands.
  - `--queue-db` is presented as primary but is a deprecated alias of `--db` (`main.go:61`).
  - The startup line appends `; accounts: MODE`, and a metrics line is printed.
  - `relay-hosted.md` §5's "`relay --version` printed at start" does not happen.
  - The exit-1 cases are incomplete: a metrics-listener failure, a journal open failure and
    a `Serve` error from the metrics server all exit the whole relay, via the shared `errc`
    at `:214-228`.
  - `accounts.md`'s `bind_poll` "unchanged" reply carries no `user_code`, because only the
    hash is stored.
  - An invalid device/os label gets `bad_envelope`, a code the spec does not name.
- **Scenario:** an operator scripting against the doc (for example checking the startup line
  or relying on `--queue-db`).
- **Spec:** `Docs/cli/relay.md` §Synopsis, §Output, §Exit codes; `relay-hosted.md` §5;
  `accounts.md` §Frames.
- **Fix direction:** update the docs.
- **Related:** C02-07, O-190

### T6c-20 · Info · confirmed-read
- **Where:** `internal/daemon/daemon.go:44-94` (status result), `cmd/agentnet/main.go:228-235`
- **What goes wrong:** status docs lag the code:
  - `relay.last_error` is omitted when empty (`omitempty`), not `""` as `status.md:91,95`
    says.
  - `ipc.md` §status (`:79-87`) lists neither `relay`, `approval` nor `git`.
  - `status --json` passes through daemon codes `internal` and `bad_request`, which
    `status.md:115-116` does not list.
  - `min_client` persists after a disconnect (`internal/relayclient/relayclient.go:94-97`).
- **Scenario:** a client coded to the doc.
- **Spec:** `Docs/cli/status.md`, `Docs/protocol/ipc.md` §`status`.
- **Fix direction:** update the docs.
- **Related:** C28-01, C04-01

### T6c-21 · Info · confirmed-read
- **Where:** docs only.
- **What goes wrong:** Phase 3 doc drift:
  - `audit.md:228` synopsis lacks `--timeout`; `audit.md:10` "about 75 call sites" is stale
    (about 120).
  - `decision.md:167` gives `decision.refuse {session, peer, reason}`, but the audit table
    (`:388`) and the code (`internal/debate/decision.go:280,392`) also carry `id`.
  - B's unsigned `peer_refused` row gets no `decision.create`, although `decision.md` §Audit
    says "both" sides write it.
  - `debate.md` §State calls `broken` "B only: a bad reveal", while B also goes `broken` on
    a refused close (`decision.md:168-177`).
  - `Docs/cli/debate.md:6-7` still says the Decision and `agentnet decision` are not yet
    implemented.
  - `debate.md` §CLI requires `--help` to show one example per entry kind; it shows shapes
    only (`cmd/agentnet/debate.go:67-153`).
  - The debate view's `decision` member is `{id, state, outcome, signed_by}` with no `hash`
    (`internal/daemon/debate.go:30-35`), against the spec's `{id, hash, state}`.
  - `debate_submit` on a non-debate session returns `unknown_session`, not `bad_state`
    (`internal/debate/store.go:196-236`).
  - `decision verify --md` writes the human status lines and then the Markdown to the same
    stdout (`cmd/agentnet/decision.go:334-349`), so `> d.md` does not produce a clean file.
- **Scenario:** none.
- **Spec:** as cited.
- **Fix direction:** update the docs, or add the `hash`.
- **Related:** O-177

### T6c-22 · Info · confirmed-read
- **Where:** `internal/daemon/notify.go:124-131` (`debateNotifyAdapter` looks the title up as
  `out` first, then `in`)
- **What goes wrong:** when a peer's debate request reuses the id of a request this daemon
  sent to it, the `debate.agreed`/`escalated`/`broken` body shows our own unrelated request's
  title. The view path picks the direction from the role (`internal/daemon/debate.go:126-131`).
- **Scenario:** it needs the id reuse of review 45 L1. The effect is display only; the title
  is cleaned.
- **Spec:** `debate.md` §Notifications ("body = the request title").
- **Fix direction:** take the direction from the debate row's role.
- **Related:** C22-03, C23-01, T6c-07 (same id-collision class)

## Drift tables

Legend: ✓ matches · ≠ differs (finding id or known id) · ∅ not implemented (ticket).

### debate.md
| § | Statement | Code | |
|---|---|---|---|
| Model | accept opens a `debate` session in the same tx | `internal/worksession/hooks.go:30-43` | ✓ |
| What not | `grant_create` on a debate: `bad_state`; ws.result/accept/changes/release/discard `bad_state`; ws.result ignored `kind` | `internal/daemon/grant.go:408-410`; `worksession/submit.go:53-56`, `transitions.go:98,151,212,338,401`; `receive.go:101-106` | ✓ |
| Request type | `debate` member exact; rounds 1–5; timeout 300–86400; iff type; no grant/run; context | `internal/request/decode.go:250-275`, `validate.go:121-157` | ✓ (0 = default: C23-03) |
| Request type | rows created in submit / receive tx; decline/cancel/auto-decline close `cancelled` | `internal/debate/start.go:81-160`; `request/receive.go:242-246`, `lifecycle.go:207`, `cancel.go:281`, `mirror.go:322` | ✓ |
| Idempotency | params_hash covers position, rounds, timeout | `internal/daemon/request.go:431-440` | ✓ |
| Commit–reveal | nonce crypto/rand; commitment preimage; reveal in slot-1 tx; B checks | `start.go:72-78`, `commit.go:385-415`, `engine.go:67-91`, `apply.go:377-389` | ✓ |
| Commit–reveal | bad reveal → broken, audit, notify, ws.cancel, mirror, note, experience | `apply.go:390-392,428-476` | ✓ |
| Turns | slots, converge rules 1–2, outcome from answer | `turns.go:57-86`, `apply.go:208-221` | ✓ |
| State | phases / CHECKs; session closes in same tx | `internal/store/store.go:457-477`; `engine.go:138-145` | ✓ (`broken` wording: T6c-21) |
| Messages | strict schemas, field caps, text rules, targets | `decode.go`, `validate.go`, `text.go:23-64`, `schema.go:26-46` | ✓ |
| Size | 32768 `entry_too_large`/`bad_body`; ≤ 14 entries | `schema.go:26`, `decode.go:42`, `daemon/debate.go:241-242` | ✓ |
| Constraints | approval kind, visible-only text, DisplayQuote, 4096 summary, id/at, one-tx store+audit+send, precondition, limits 10/11th excess/20, hold, late | `internal/daemon/debate_constrain.go:38-136`, `constraint.go:60-311`, `text.go:75-96` | ✓ (C24-01, C24-02) |
| Timeouts | deadline; sweep ≤ 1 min; every IPC call; table | `engine.go:18-31`, `sweep.go:57-73`, `daemon/debate.go:326` | ≠ C23-05 (show/list/constrain do not sweep) |
| Cancel/abandon | A invited request.cancel; after accept cancelled; B ws.cancel; abandon | `cmd/agentnet/debate.go:577-615`, `engine.go:177-240`, `worksession/cancel.go:57-68` | ≠ C23-01, T6c-05, T6c-15 |
| request_complete / early complete | B `bad_state`; A drops content, closes cancelled; closing unchanged | `worksession/submit.go:53-56`, `hooks.go:76-175`, `engine.go:247-260` | ✓ (O-170) |
| Submitting | resolve; kind debate else `bad_state` | `store.go:196-236` | ≠ T6c-21 (`unknown_session`) |
| Submitting | not_your_turn; A/B tx; one-step accept | `submit.go:56-131` | ✓ (errors: T6c-06) |
| Applying | A ignore+audit+echo; early slot 1; B holds early; dup ignored | `apply.go:151-319` | ✓ |
| Echo | ≤ once per 10 min | `store.go:61`, `apply.go:718-748` | ✓ |
| Quarantine | submit/accept `quarantine_active`; sensitive grant `debate_open` incl. confirm and policy | `start.go:81-124,186-190`; `capability/store.go:346-358`; `daemon/grant.go:444-452,513-525,567-571` | ✓ |
| Kinds | reserved prefixes; bodies; derived session; strict; ws.state not sent; B completion notes | `daemon/outbox.go:25`; `apply.go:31-98,330,494`; `worksession/mirror.go:130-135`; `apply.go:670-686` | ✓ |
| Persistence | migration 19 | `store.go:385-501` | ✓ (extra indexes, undocumented) |
| IPC | SetEscapeHTML(false) | `ipc/ipc.go:194-203`, `:140-155` | ≠ **T6c-01** |
| IPC | request_submit `debate`; list filters; show `unknown_session`; submit result; constrain result | `daemon/request.go:88-100,289`; `daemon/debate.go:227-321` | ✓ (O-178) |
| IPC | view shape | `daemon/debate.go:82-104` | ≠ T6c-21 (`decision.hash`) |
| CLI | commands, flags, wait turn/closed/exit 4 | `cmd/agentnet/debate.go`, `session.go:535-540,662-699` | ✓ (help examples: T6c-21) |
| Notifications | four events, on by default, content-free | `notify/settings.go:47-79`, `trigger.go:223-230` | ≠ T6c-03, T6c-22 (C27-02, C27-05) |
| Audit | all 12 actions, actors, detail keys | `start.go:101-107`, `submit.go:133`, `apply.go:62,197,263,317,398,437-438,667`, `engine.go:87,156,239`, `constraint.go:225`, `debate_constrain.go:116` | ✓ (timing: T6c-16) |

### decision.md
| § | Statement | Code | |
|---|---|---|---|
| Object | members, patterns, enums, caps | `internal/decision/verify.go:259-494` | ✓ (context: T6c-18) |
| Derivation | id; rules 1–12; absent-not-empty; MaxDecision 786432 | `decision.go:30-35,63-66,162-366`; `debate/decision.go:57-84,156-163` | ✓ (C24-01) |
| Signing | domain tag, hash, Ed25519; A step 1; B checks/hold/refuse-at-once; B signs in one tx; A checks sig | `decision.go:29,69-83`; `engine.go:99-172`; `apply.go:603-686`; `debate/decision.go:187-403` | ✓ |
| Refuse | B unsigned own derivation, peer_hash, broken, sign `refused: mismatch`; empty-hash case | `debate/decision.go:238-284` | ✓ (notify text: T6c-03) |
| Silence | lost sign not re-sent | — | ∅ O-176 |
| Signed file | steps 1–6, exit 0/6/1 | `verify.go:60-148,499-575`; `cmd/agentnet/decision.go:276-352` | ✓ (C25-01, C25-02) |
| Markdown | fences, spans, Visible, TemplateInert, banner, per-constraint sentence, layout | `markdown.go`, `visible.go` | ✓ (O-179) |
| Storage | migration 20 | `internal/store/store.go:503-518` | ✓ |
| IPC | decision_list, decision_show | `internal/daemon/decision.go:61-174` | ≠ T6c-06; ✓ otherwise (O-178; `state` filter not validated, Info) |
| IPC | HTML escaping off | `ipc/ipc.go:140-155` | ≠ **T6c-01** |
| CLI | flags, `--out/--force`, exits | `cmd/agentnet/decision.go:61-201` | ✓ (O-179; `--md` stdout: T6c-21) |
| Audit | create, sign_in, refuse; export not audited | `debate/decision.go:135-140,280,392,402` | ✓ (O-177; T6c-21 on B's refused row) |

### experience.md
| § | Statement | Code | |
|---|---|---|---|
| Record | members and sources | `internal/experience/experience.go:96-218`, `debate/experience.go`, `worksession/experience.go` | ≠ T6c-17 (C22-06, C24-03) |
| Never in record | no quarantined/discarded/superseded result, no output/paths | `worksession/experience.go:102-118`, `transitions.go:236-240` | ✓ |
| When | written in every closing tx; one per (session, role) | `engine.go:167`, `apply.go:443,470,686`, `debate/decision.go:283`, `engine.go:236`, `store.go:522-530` | ✓ (C22-01) |
| Who can read | nothing reads it | only `INSERT` at `experience.go:231` | ✓ |
| Audit | `experience.write {session, role, bytes, truncated?}` in the tx | `debate/experience.go:113-119`, `worksession/experience.go:124-133` | ≠ C22-05 (after commit) |

### audit.md and Docs/cli/log.md
| § | Statement | Code | |
|---|---|---|---|
| What exists | table, triggers | `internal/store/store.go:21-33` | ✓ |
| Chain | genesis, tag, row canonical form, raw prev hash, lowercase hex | `internal/audit/chain.go:23-68` | ✓ |
| Appending | BEGIN IMMEDIATE on own Conn; head; chain_start; id + 1; AppendTx fails caller | `audit.go:58-219` | ✓ (O-169) |
| Migrations | IMMEDIATE, re-read, concurrent test | `store.go:589-624`, `store_test.go:234` | ✓ (C26-03, C28-03) |
| Migration 18 | column, CHECK, trigger | `store.go:371-377` | ✓ |
| Verification | reasons, order, result shape, exit 5, paging 2000, 120 s | `chain.go:128-331`; `cmd/agentnet/log.go:27,151-155,253-256` | ✓ (`head` omitempty: Info) |
| Fallback | read-only open, stderr line, exit 3 | `internal/store/readonly.go:13-28`; `log.go:306-326` | ✓ (C26-01) |
| Anchors | legacy → mismatch after walk; malformed → bad_request | `chain.go:85-103,236-241,287-292,348-361` | ✓ (advice: T6c-08) |
| agentnet log | params, oldest first, 1000/5000, next_after_id, bad_request, --since/--until/--action | `query.go:17-233`; `daemon/audit.go:49-94`; `log.go:131-235` | ✓ |
| agentnet log | `--session` resolution | `query.go:219-309` | ≠ T6c-07 |
| agentnet log | list timeout | `log.go:30,151` | ≠ T6c-09 |
| agentnet log | human output through `notify.Clean`; `--json` | `log.go:206-293` | ✓ |
| Scope | TestAuditInventory covers every registered kind | `internal/daemon/audit_inventory_test.go:145-187` | ≠ T6c-04 |
| No content | Phase 3 actions: ids, enums, counts, sizes, hashes | see the debate/decision audit rows above | ✓ |
| ipc.md lifecycle | daemon.start/stop `{pid, version}`; stop_requested; chain_start | `daemon.go:134-137,290,298,613`; `audit.go:125-128,200` | ✓ |
| agentnetd-install | service.install/uninstall `{platform, custom_home, changed}` | `cmd/agentnetd/install.go:50-54,159-163,189` | ✓ |

### relay-hosted.md, accounts.md (4.2a), Docs/cli/relay.md
| § | Statement | Code | |
|---|---|---|---|
| §1 / CLI | loopback default, `--allow-non-loopback` rules, TLS 1.2 / HTTP/1.1, ACME, proxy flags, public origin, Public definition, v2 only when public, v1 refused and not counted, nonce 32 B / TTL 10 s | `cmd/relay/main.go:94-124,155-158,208-212`; `transport.go:23-129,268-323`; `internal/relay/relay.go:32,477-488,584-611`; `auth.go:383-435` | ✓ (O-002, O-007, O-019–O-022) |
| §1 / CLI | `/healthz` 200/503, own rate bucket, HTTP limits | `auth.go:439-457`; `limits.go:164,245-250`; `transport.go:23-41` | ✓ (C02-02 can hold the connection) |
| §2 / CLI | every abuse-limit flag and default (30/60, 64, 10/10 min, 256, 5000, 64, 600, 64 MiB, 120/240, 32 MiB, 60, 20, 4 MiB, 256 MiB, 300/8 MiB, 2000/64 MiB, 4 GiB, 1 GiB); positive; suffixes | `cmd/relay/limits.go:34-180`; `internal/relay/limits.go:19-39`; `queue.go:20-36` | ✓ (O-024) |
| §2 | refusal behaviour and log line | `limits.go:183-240`; `relay.go:492-504,768-819`; `queue.go:283-309` | ✓ (C01-01..06, O-009, O-025, O-028) |
| §2 L5 | pairing limits; per-account 30/day, 20 outstanding; 10 000 codes | `pairing.go:19,114,223-290,367-376`; `accounts.go:36-38` | ✓ (C02-05; v1 skips the per-prefix rate, Info; O-004–O-008) |
| §3 | one DB, WAL, FULL, secure_delete; R1/R2; newer schema refused | `queue.go:113-129,208-254` | ✓ (C26 lead DSN) |
| §3 | backup / restore / journal / replay | `backup.go:20-105`; `journal.go`; `main.go:260-318`; `accounts_store.go:407-447` | ≠ C02-01, C02-03, C02-04, C03-04, C03-06 |
| §3 | SIGTERM drain; Close waits for connection goroutines | `relay.go:410-441`; `main.go:231-238` | ≠ **T6c-02** |
| §4 | quotas, `quota_exceeded` | — | ∅ 4.1c |
| §5 | metrics; version at start | `main.go:163-171,208-257` | ≠ C02-06, C02-07, T6c-19 |
| accounts | `--accounts` off by default; 4 keys; ready.account; state table; 16 unbound/prefix; who may send to whom; bind frames and codes; revocation; delete; suspend; R2 tables; no email in logs; test hooks | `accounts.go:27-536`; `accounts_store.go:23-553`; `relay.go:630-661,940-970`; `envelope/accounts.go:16-56`; `testhooks_*.go` | ✓ (C03-01..09, C04-04, O-187) |
| accounts | outage fails closed | `auth.go:439-457` only | ≠ C03-03 |
| accounts | web pages, OAuth, sessions, CSRF, wrong-code limits | — | ∅ 4.2b |

### Docs/cli/doctor.md, status.md, install.md
| Doc | Statement | Code | |
|---|---|---|---|
| doctor | checks and states; never authenticates; exits 0/1/2; JSON shape; 1 s socket | `cmd/agentnet/doctor.go:41-493`; `internal/relayclient/probe.go:30-60` | ✓ |
| doctor | `config` owner-only | `doctor.go:254-268` | ≠ T6c-11 (C16-08) |
| doctor | `relay`/`clock` configured-relay rule; URL rule | `doctor.go:152-170,344-373,395-416` | ≠ T6c-10, T6c-14 (C28-01, O-023) |
| doctor | `service` installed and running | `doctor.go:467-493` | ≠ Info: with the daemon up it is `ok` without looking at the service; English-only (C28-05) |
| doctor | no path outside the config dir printed | `doctor.go:263,268,284-293` | ≠ Info: git's exec error and a `CheckURL` error can echo an absolute path or the raw URL (`capability/git.go:168`, `relayclient.go:410-419`) |
| doctor | macOS 104-byte socket rule | `doctor.go:320` (`> 104`) | suspected off-by-one (the limit includes the NUL), Info |
| status | exits, human lines, team table, JSON fields, relay object | `cmd/agentnet/main.go:189-327`; `internal/daemon/daemon.go:44-131`; `status.go:31-141` | ✓ (C28-01, C28-04, T6c-20) |
| install | archives, no testhooks, `~/.local/bin`, no sudo, verifier choice, one version, min version, archive hash, env vars, Homebrew render | `scripts/install.sh:55-321`; `release.yml:143-186`; `packaging/homebrew/*` | ✓ (C15-01..07, O-181–O-183) |
| install | "Then run `agentnet setup`" | — | ≠ T6c-12 (∅ 4.9a) |
| install | failure message | `install.sh:67-71,311-315` | ≠ T6c-13 |

## Undocumented behaviour (code with no spec)

- **Relay:**
  - `/healthz` accepts HEAD and answers 405 otherwise.
  - 429 responses carry `Retry-After: 60` (`relay.go:446-463`, `auth.go:440-456`).
  - Limit names `health_per_prefix`, `max_inflight_read`, `control_frames_close`,
    `unbound_per_prefix` and `queue_*`, and the log fields `relay=all` and
    `suppressed_before` (`internal/relay/limits.go:55-80,206`).
  - Unbound connections evicted with close 1013 and no error frame (`relay.go:641-646`).
  - Unknown ops from unbound or group-less keys get `account_required` instead of 1008
    (`accounts.go:269-276`).
  - `relay admin` verbs, output and exit codes (`cmd/relay/admin.go:18-227`).
  - `bind_*` log events (`accounts.go:183,213,341,391,469`).
  - Size suffixes `K/M/G/B`, case-insensitive (`cmd/relay/limits.go:168`).
- **Debate:**
  - `debate.ignored` reasons `unknown`, `state`, `turn`, `duplicate`, `kind`, `targets`,
    `late` (`apply.go`, `submit.go:106`, `constraint.go:167-207`, `decision.go:354-367`).
  - `debate.reveal_in {ok:false}` alongside `reveal_bad` (`apply.go:437`).
  - `ws.ignored {kind:"ws.state"}` on debate sessions (`worksession/mirror.go:269-272`).
  - The view's `hint`, list `entries`/`constraint_count`, and `decision.outcome`/`signed_by`
    (`daemon/debate.go:94-171`).
  - Inline CLI entry flags `--claim/--argument/--pass/--challenge/--agree/--accept/--reject/…`
    (`cmd/agentnet/debate.go:180-405`); `debates --peer`.
  - A 20 s sweep interval (`daemon/debate.go:326`); `debates_request`/`debates_peer_phase`
    indexes (`store.go:478-479`).
- **Decision:**
  - `decision_show.peer_fingerprints` (`daemon/decision.go:57,172`).
  - `decision.refuse` reason values `outcome`, `entries`, `cut`, `unsent`, `time`, `hash`,
    `signature`, `mismatch`.
  - An 8 MiB verify file cap (`cmd/agentnet/decision.go:359-375`).
- **Audit and log:**
  - Unknown `audit_*` params ignored (`daemon/audit.go:64,78,88`).
  - Rows with an unparsable `ts` dropped under `--since/--until` (`query.go:166-168`).
  - No cap on the number of anchors (`daemon/audit.go:33-47`).
  - `audit.SetPageHook`, a test hook exported from production code (`chain.go:165-178`).
  - Usage-error combinations of `log` flags (`log.go:116-129`).
- **Doctor and status:**
  - `doctor_error`; the 9 s / 3 s budgets; `-h` exit 0 (`doctor.go:45,86-117`).
  - Status fields `approval`, `team.epoch/state/owner`, `presence.team`
    (`daemon.go:61-131`).
- **install.sh:** Rosetta detection forces arm64 (`:157-160`); BusyBox wget has no TLS
  pinning (`:213-235`).

## Assumptions resolved

| Chunk | Assumption (theme area) | Result |
|---|---|---|
| C02 | `Server.Close` waits for connection goroutines before `q.close()` (spec §3) | **finding T6c-02** |
| C02 | `ReplayJournal` runs on a DB already migrated by `Restore` | holds (`cmd/relay/main.go:301-312`), as C02 checked |
| C03 | `ConfirmBind`/`PendingBind`/`DenyBind` rate limits and authentication live in the 4.2b web layer | holds as "not built": the only callers are `cmd/relay/testhooks_on.go:54-76` (build tag), `accounts.md` web part ∅ 4.2b |
| C03 | `EnsureAccount` callers validate `subject`/`display` | not built (4.2b); only the test hook calls it; C03-05 stands |
| C03 | Journal replay "since" = backup time | finding C02-01 (known) |
| C03 | `OpenAdmin` migrating the live DB under an older relay is safe | holds today: R1/R2 are `IF NOT EXISTS` (`queue.go:114-127`, `accounts_store.go:23-81`), and a relay refuses a newer schema only at open (`queue.go:240-242`). A future R3 run by a newer `relay admin` under an older live relay would hit the same issue as C28-03; no R3 exists |
| C03 | `LoginURLFromOrigin` must be validated by the daemon | holds vacuously: the daemon ignores `ready.account` (C04-04; `relayclient.go:257-263`) |
| C23 | `debateError` maps the request-layer errors of the one-step accept | **finding T6c-06** (suspected part) |
| C23 | `PeerQuarantine` is wired in the daemon | holds: the debate quarantine checks are traced end to end (`start.go:81-124,186-190`, `daemon/request.go:349`, `daemon/debate.go:237`) |
| C24 | `deriveTx` assumes the stored request `body` is byte-identical on A and B | holds: both sides store the canonical re-encoding of the same validated request (`internal/request/submit.go`, `receive.go`), and derivation reads it (`debate/decision.go:62-65`, `decision.go:249-276`). Cross-checked by `TestDecisionVector` |
| C24 | `ValidateConstraintText` uses the same Unicode tables on both daemons | unchanged; a skew leads to O-165 (not a drift item) |
| C25 | `Render` on the `decision <id> --md` path assumes the daemon's Decision came from validated entries | holds (`debate/decision.go:57-84` reads only `applied`/`sent` rows stored after `DecodeEntry`) |
| C26 | `AppendTx` callers pass the store's tx and fail with it | holds for Phase 3: the only `AppendTx`-style debate audit is `debate.constraint` in the approval's tx (`daemon/debate_constrain.go:109-125`); every other debate/decision row is after commit (T6c-16) |
| C26 | callers put no content in `detail` (Phase 3–4 actions) | holds: every `debate.*`, `decision.*`, `experience.write`, `audit.chain_start`, `daemon.*` and `service.*` row carries ids, enums, counts, sizes or hashes only (table above); peer-supplied enums are validated before use (`apply.go:517-520`) |
| C22 | `experience_records` is in both rewind DROP lists | holds (`internal/store/store_test.go:109,190,246`) |
| C28 | `status` assumes `LastError` is content-free | finding C28-01/C04-01 (known); T6c-01 multiplies it |

Spec fields of chunk findings in this area, treated as known drift and not re-reported:
C01-01..06, C02-01..07, C03-01..09, C04-01, C04-02, C04-04, C15-01..07, C22-01, C22-02,
C22-05, C22-06, C23-01..05, C24-01..03, C25-01, C25-02, C26-01..03, C27-02, C27-05, C28-01..05,
C29-02, C30-01, C31-07.

## Checked and fine (open findings)

- O-134 appears **fixed**: `grant.orphan` now carries `{grant, peer}`
  (`internal/daemon/grant_kinds.go:113`), as `audit.md` §Scope says.
- O-170 still open (`engine.go:255` closes early complete as `cancelled`). O-176 still open (no
  `debate.sign` re-send). O-177 still open by decision (D33). O-178 still open, and worse under
  T6c-01.
- O-187: every item checked against `accounts.md` in the table above. Fail-closed is only on
  `/healthz` (C03-03). `ConfirmBind` is exported but reachable only under `testhooks`.

## Leads for other chunks

- `cmd/relay/main.go:214-228`: a metrics-server failure goes to the shared `errc` and stops the
  public relay (T2/T10).
- `internal/relay/pairing.go:238-253`: v1 `pair_new` skips the per-prefix rate (v1 is off on a
  public relay unless `--allow-pairing-v1`).
- `deploy/early/agentnet-relay.service:19-31`: no `--security-journal`. That is harmless
  while accounts are off, but must be set before `--accounts` is enabled (C03-06).
- `00-index.md` §3.a omits `notify_test` (71 methods, not 70).

## Notes

- The helper sub-agent for debate.md listed `SetEscapeHTML(false)` as "matches": it looked only
  at `marshalResult`. The test shows the defect; always test the full encoder path.
- `agentnetd` and doctor use two different `CheckURL` environments (T6c-14). One shared source
  of truth for "is the insecure escape hatch on" would fix both.

## Commands run

- `go test ./internal/ipc -run TestReview55T6c01 -count=1 -v` → PASS (demonstrates T6c-01):
  `200000 x '<' (raw 200 KB, under the 1 MiB line): err = ipc: read response: ipc: line too long`.
  The temporary test file `internal/ipc/zz_review55_T6c-01_test.go` is kept.
- `grep`/`comm` over `srv.Handle(` against `ipc.md` and `00-index.md` §3.a: 71 registered, index
  70 (missing `notify_test`).
- Read-only: `git diff --stat 6cc26a7 HEAD -- internal cmd tools` (empty).
