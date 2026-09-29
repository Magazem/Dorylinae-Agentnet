# T6b: Spec-versus-code drift, Phase 2

Reviewer: TH-T6b · model claude-opus-5-5 · elapsed ~45 min active (shell clock 06:01–10:05, including a usage-limit pause of about 3 h) · commit 6cc26a7

Scope: `Docs/protocol/work-session.md`, `approval.md`, `grant.md`, `consult.md`, `device.md`,
and `Docs/cli/approve.md`, `session.md`, `grant.md`, `fetch.md`, `device.md`, `consult.md`.
The worktree HEAD only adds review-55 docs on top of 6cc26a7.

## Summary

Critical 0 · High 0 · Medium 0 · Low 5 · Info 6

Verdict: the Phase 2 code follows its specs closely. Every limit and constant I checked
matches: approval TTL, attempts, pending, hourly and daily caps; grant expiry bounds, token
size, fetch windows, in-flight limits, fragment, read, file and list caps; device queue,
daily run cap, helpers per controller, scope TTL and summary cap; consult body and
context caps. The same holds for every IPC method and error code, the work-session
transition table, the `ws.*`/`grant*`/`device.*` wire kinds and the device `check` enum.
The serious drift in this area was already found by the chunk reviewers and is not repeated
here:
- C11-01/C12-02: approval summaries lack the fingerprint, scope and branch, and are not cleaned.
- C11-04/C11-02/C11-05: forbidden-resource bypasses.
- C14-02: a scope summary is cut off in the window.
- C21-01: B completes with a result A never accepted.
- C21-05: `mail_id` is missing.
- C21-06: `ws.orphan` has no `session`.
- C11-09: policy audit shape.

What this pass adds is mostly on the **audit side** (`ws.open` never written, per-grant revokes
at session close or peer removal not audited, daemon-caused approval rejections recorded as
`reason: "user"`), the **session view** (`grants` is always empty), the **status field**
`approval_window` (not built), plus six documentation drifts.

## Questions (04-themes.md §T6)

1. **Drift table per spec.** See [Drift tables](#drift-tables) below. Every normative
   statement I checked has a `file:line` and a verdict.
2. **Code behaviour with no spec.** These have no spec text:
   - the audit action `approval.limit {reason}` (T6b-07);
   - `approval.reject` `via` values `superseded`, `unlinked` and `scope_cleared` (T6b-03);
   - the CLI form `agentnet session <id> --release`, which the specs call `agentnet release` (T6b-08).

   I found no undocumented IPC method in the Phase 2 namespaces. Every `srv.Handle` for
   `approval_*`, `ws_*`, `grant_*`, `fetch_*` and `device_*` is in its spec. `ws_discard` is in
   work-session.md but missing from ipc.md's method list (T6b-09).
3. **IPC list cross-check (00-index §3.a vs ipc.md).** All 26 Phase 2 methods registered in
   `internal/daemon/*.go` appear in 00-index. All but `ws_discard` appear in
   `Docs/protocol/ipc.md:375-398`.
4. **Chunk "Spec" fields used as known drift.** Not re-reported: C09-01, C09-02, C09-03,
   C10-01, C10-02, C11-01…09, C12-02, C12-03, C12-05, C13-01, C13-02, C14-01…05, C18-01,
   C19-01, C21-01…06, C22-01, C22-02, C22-05, C22-06. The verify reports confirm C11-01,
   C11-04 and C14-02 as High.

## Findings

### T6b-01 · Low · confirmed-read
- **Where:** `internal/worksession/hooks.go:23-45` (`OpenSession`), `internal/worksession/receive.go` (session created by a `ws.result` that arrives first); no `"ws.open"` string exists anywhere in `internal/` or `cmd/`
- **What goes wrong:** The spec's first audit row, `ws.open {session, request, peer, role}`
  on both sides, is never written. Session creation is visible only indirectly: through
  `request.accept` on B, and on A through `request.state` or `ws.result_in`.
- **Scenario:** An auditor or a `log --verify` user looks for when a session opened (for
  example to tie a later `grant.create` or `ws.release` to its session) and finds no row. On A,
  when the `ws.result` overtakes the accept, nothing records the creation at all. It carries
  no content and bypasses nothing: the audit trail is incomplete against its spec.
  `TestAuditInventory` does not catch it, because it maps `request_accept` to
  `request.accept` only (`internal/daemon/audit_inventory_test.go`).
- **Spec:** `Docs/protocol/work-session.md` §Audit: "`ws.open` | both / `daemon` (B: `cli`) | `{session, request, peer, role}`"
- **Fix direction:** Return an after-commit audit from `OpenSession`, and from the receive path that creates the row. Or drop the row from the spec.
- **Related:** C21-06 (other `ws.*` audit-shape drift)

### T6b-02 · Low · confirmed-read
- **Where:** `internal/daemon/session.go:178` (`Grants: []any{}`); nothing else writes `SessionView.Grants`
- **What goes wrong:** The session view always returns `"grants": []`, although the spec
  makes it the list of the session's grants. `Docs/cli/session.md:47,107` show `[]` too, so the
  CLI page hides the gap.
- **Scenario:** A's human runs `agentnet session s-… --json` before deciding on a release, to
  see what the worker could read. The view says there are no grants even when a sensitive
  `fs.read` grant was active, and that grant is why the result is quarantined. The human
  has to cross-check with `agentnet grants --session`. That is a misleading view at the
  point of an approval decision, not a bypass.
- **Spec:** work-session.md §IPC, Session view: `"grants": [<grant summary>]`; §Grants in a session.
- **Fix direction:** Fill `grants` from `capStore.List(ListFilter{Session: id})`, with id, action, sensitive, state and exp, and no path or label on list views.
- **Related:** C11-01 (what the human sees at approval time)

### T6b-03 · Low · confirmed-read
- **Where:** `internal/daemon/device.go:389` (`"superseded"`), `:560,566` and `internal/daemon/daemon.go:506` (`"unlinked"`), `internal/daemon/device_scope.go:215` (`"superseded"`), `:305` (`"scope_cleared"`) → `internal/approval/store.go:654` (`rejectRow(ctx, id, "user", via)`)
- **What goes wrong:** Several approvals are rejected by the daemon, not by a person: a
  superseded link intent or scope, an unlink, or a scope clear. They go through
  `Store.Reject`, which always records `reason: "user"` and puts the daemon's cause into
  `via`. The spec allows only `user | attempts | expired | locked | precondition` for the
  reason, and only `window | terminal | ipc` for `via`.
- **Scenario:** A local agent runs `device unlink @laptop` while the human's scope approval
  is pending. The audit shows `approval.reject {reason: "user", via: "unlinked"}`, so the
  record says a human rejected it. Tools or reviewers that filter on `reason = user`, or
  trust `via` to name a human channel, draw the wrong conclusion. Nothing is bypassed.
- **Spec:** `Docs/protocol/approval.md` §Audit: `approval.reject {…, reason: "user"|"attempts"|"expired"|"locked"|"precondition"}` … "`approval.approve` and `approval.reject` record whether the answer came from the window, the terminal or IPC (reject only) as `via`".
- **Fix direction:** Add a `RejectFor(ctx, id, reason)` used by daemon-side callers with `reason: "precondition"` (as `RejectSubjects` already does) and no `via`. Or extend the spec's enums.
- **Related:** O-128 (the reason for a lapsed Reject), C14-04

### T6b-04 · Low · confirmed-read
- **Where:** `internal/daemon/daemon.go:486-488` (`RevokeGrants`: the ids returned by `RevokeForSessionTx` are discarded), `internal/daemon/grant.go:796-802` (`revokeForRemovedPeer`: same), `internal/capability/store.go:378-432`
- **What goes wrong:** Only the user's `grant_revoke` writes a `grant.revoke` audit row
  (`grant.go:715`). The spec also names the `daemon` actor and the reasons `session_closed`
  and `peer_removed`, but those revocations leave no per-grant row. The only trace is
  `ws.close` (session) or `peer.remove`. Neither names the grants, and `grant.create` rows
  cannot be tied to their end.
- **Scenario:** A's human closes a session with three active sensitive grants, or removes
  the peer. The grantor's audit then shows the three `grant.create`/`grant.issue` rows and
  no matching `grant.revoke`. An auditor who asks "when did this grant stop working?" has to
  infer it from the session. On the holder, B, the ws.state mirror revoke is also silent,
  which is expected there: the spec gives `grant.revoked_in` only for `grant.revoke` mail.
- **Spec:** `Docs/protocol/grant.md` §Audit: "`grant.revoke` | grantor / `cli` or `daemon` | `{grant, peer, reason}`"; §Session end.
- **Fix direction:** Carry the returned ids to after-commit and append one `grant.revoke {grant, peer, reason}` per id with actor `daemon`.
- **Related:** O-134 (similar grant audit-shape drift, now fixed; see Checked and fine)

### T6b-05 · Low · confirmed-read
- **Where:** `internal/daemon/daemon.go:61` (status has `approval` only); no `approval_window` anywhere in `internal/` or `cmd/`; `internal/daemon/approval.go:98` (`approval_unavailable` message "the desktop notifier is unavailable")
- **What goes wrong:** approval.md specifies a `status` field
  `approval_window: "ok" | "missing"`. It checks the helper program and the display without
  opening a window, "so the user learns about it before the first grant". It also says the
  `approval_unavailable` message and `status` name the fix ("install zenity (or kdialog)",
  "no desktop session", "PowerShell/WinForms blocked by policy"). Neither is built. Every
  cause maps to one generic message.
- **Scenario:** A Linux user without zenity or kdialog, or a Windows daemon in session 0 or
  under WDAC, installs AgentNet. `agentnet status` and `doctor` report nothing. The first
  `grant_create`, `release` or `device link` fails with "the desktop notifier is
  unavailable", which names the wrong component. The user must diagnose it by hand. This
  fails closed; it is not a security issue.
- **Spec:** `Docs/protocol/approval.md` §The approval window, "No window helper on Linux" paragraph.
- **Fix direction:** Have `ApprovalWindow` expose a `Check()` that returns a reason, report it in `status` (and `doctor`), and map it into the `approval_unavailable` message.
- **Related:** none

### T6b-06 · Info · confirmed-read
- **Where:** `Docs/cli/approve.md` (whole page); Linux install notes (`Docs/cli/install.md`, `agentnetd-install.md`)
- **What goes wrong:** Owner decision D20 (OQ-2.2d-1 = (a)) accepted that on Linux the
  approval summary is in zenity's or kdialog's argv, on the condition that it is
  documented. approval.md says "`Docs/cli/approve.md` and the Linux install notes say so
  and name `hidepid=2` as the fix on shared machines". No page under `Docs/cli`, `Docs/beta`
  or `Docs/agents` mentions it. `hidepid` appears only in HANDOFF, approval.md and old
  reviews.
- **Scenario:** An operator of a shared Linux machine does not learn that other local
  users can read grant summaries (action, label, peer name) through `/proc/<pid>/cmdline`.
- **Spec:** approval.md §The approval window, Linux row; HANDOFF D20.
- **Fix direction:** Add the paragraph to `Docs/cli/approve.md` and the Linux install page.
- **Related:** D20

### T6b-07 · Info · confirmed-read
- **Where:** `internal/approval/store.go:221-225` (`auditLimit` → `approval.limit {reason: "pending"|"hourly"}`), called at `:75,85`
- **What goes wrong:** The audit action `approval.limit` is not in approval.md §Audit, nor
  in audit.md. It is harmless and content-free, but undocumented code behaviour.
- **Scenario:** none; inventory drift only.
- **Spec:** approval.md §Audit (list of actions).
- **Fix direction:** Add `approval.limit {reason}` to the spec.
- **Related:** none

### T6b-08 · Info · confirmed-read
- **Where:** `cmd/agentnet/main.go:44-118` (no `release` command), `cmd/agentnet/session.go:139` (`--release`); `Docs/cli/session.md:59,69` documents `session <id> --release`
- **What goes wrong:** work-session.md §Quarantine and §CLI ("`agentnet release <id>
  [--json]` | `ws_release`") and grant.md §Sensitive grants ("`agentnet release` needs a human
  approval") name a top-level `agentnet release` command. The code and the CLI page have only
  `agentnet session <id> --release`. An agent that follows the protocol document gets an
  unknown-command usage error.
- **Scenario:** none beyond the usage error.
- **Spec:** work-session.md §CLI table; grant.md §Sensitive grants.
- **Fix direction:** Align the protocol docs with `session --release`, or add the alias.
- **Related:** none

### T6b-09 · Info · confirmed-read
- **Where:** `Docs/protocol/ipc.md:375-376` (the method list omits `ws_discard`); handler `internal/daemon/session.go:457`
- **What goes wrong:** `ws_discard` (OD-P2-6 (c)) is specified in work-session.md §IPC and
  registered, but ipc.md's list of work-session methods leaves it out.
- **Scenario:** none; doc drift.
- **Spec:** ipc.md §method list vs work-session.md §IPC.
- **Fix direction:** Add `ws_discard` to ipc.md.
- **Related:** none

### T6b-10 · Info · confirmed-read
- **Where:** `internal/store/store.go:285-296` (migration 15 `grant_policies`), `:329-331` (migration 16 `ADD COLUMN path/branch/approval`) vs `Docs/protocol/grant.md` §Tables
- **What goes wrong:** The documented `grant_policies` schema is not the one the code
  creates:
  - spec `max_expires_s INTEGER NOT NULL`; code `max_ttl INTEGER` (nullable);
  - spec `until TEXT NOT NULL`; code `until TEXT` (nullable);
  - spec `scope TEXT` (nullable); code `scope TEXT NOT NULL`;
  - spec `public … DEFAULT 0` with no CHECK; code adds `CHECK (public IN (0, 1))`;
  - `path` and `approval` are `NOT NULL DEFAULT ''` in the code.

  The code comment at `store.go:298-304` acknowledges part of this. Behaviour is enforced in
  `internal/capability` (non-NULL values always written), so there is no functional
  difference today.
- **Scenario:** none; a reader or a later migration that trusts the spec's column names or
  NULL rules would be wrong.
- **Spec:** grant.md §Tables.
- **Fix direction:** Update grant.md §Tables to the real schema (as it does for `approvals`).
- **Related:** O-133 (`grant_policies.approval` always `''`)

### T6b-11 · Info · suspected
- **Where:** `internal/daemon/approval_terminal.go:73-85` (`Confirm` with any `<code>` string), `internal/approval/store.go:279-283`
- **What goes wrong:** In terminal mode, a line `<tag> <anything>` whose second field is not
  6 digits (a typo such as `a-0123ab 48291`) is checked as a code and uses one of the 3
  attempts and the 10-per-24 h budget. The window path refuses such a value without counting
  it (`isSixDigits`, `store.go:841`). approval.md states the "not counted" rule only for the
  window's answer format, so this is not strictly a contradiction.
- **Scenario:** a human mistypes twice on the daemon's terminal. The approval then has one
  attempt left, and the daily lockout comes closer.
- **Spec:** approval.md §The approval window, "Answer format" (window only); §Headless machines (silent).
- **Fix direction:** Apply `isSixDigits` in `handleTerminalLine` before `Confirm`, or state in the spec that terminal mode counts every value.
- **Related:** O-128

## Drift tables

Legend: ✓ matches · ✗ differs (finding) · — not implemented.

### approval.md

| § | Statement | Code | Verdict |
|---|---|---|---|
| Object | id `a-` + 32 hex, crypto/rand | `internal/approval/approval.go:351-357` | ✓ |
| Object | kinds incl. `debate_constraint` | `approval.go:22-31`; migration 19 `internal/store/store.go:437-439` | ✓ |
| Object | 6-digit uniform code | `approval.go:362-376` | ✓ (C12) |
| Object | MAC `dorylinae-approval-v2\n…`, key in memory only | `approval.go:380`, `store.go:130,182` | ✓ (C12) |
| Object | pending → expired at start, audit `approval.reject {reason: expired}` | `approval.go:317-348` | ✓ |
| Object | TTL 10 min, 3 attempts, 5 pending, 20/h, 10 wrong/24 h | `approval.go:50-55` | ✓ |
| Object | lockout rejects all pending, `approval_locked`, notification text | `store.go:296-316,573-588`, `Create` `:56-64` | ✓ |
| Delivering | title `AgentNet code N for approval a-xxxxxx`, fixed body sentence | `store.go:201-206` | ✓ |
| Delivering | Windows toast/history via `GetSystemDirectory` path | `internal/notify/approval_windows.go:86,95` | ✗ C12-03 |
| Window | ready check before code; kill on notifier failure | `store.go:106-149` | ✓ |
| Window | answer ≤ 256 bytes; non-6-digit approve reopens, not counted | `internal/notify/window_parse.go`, `store.go:841-843` | ✓ |
| Window | answer before notification shown = dismiss | — | ✗ O-137 |
| Window | locking: reserve under `mu`, window outside | `store.go:93-120` | ✓ |
| Window | per-approval timer, kill on decide/expiry/lockout/stop | `store.go:184,524-558`, `approval.go:291` | ✓ |
| Window | outcome notifications; wrong-code / Perform-error reopen messages | `store.go:835-864` | ✓ |
| Window | summary shown in full (device scope ≤ 16384 bytes) | `internal/notify/window.go:27,39` | ✗ C14-02 / O-167 |
| No helper | `status.approval_window: ok\|missing`; message names the fix | — | ✗ T6b-05 |
| Flow 1 | peer text in summary through `notify.Clean`; name + fingerprint | `internal/daemon/grant.go:616,960` | ✗ C12-02 / C11-01 / C11-06 |
| Flow 2 | method returns `{approval}` at once, CLI never prompts | `cmd/agentnet/approve.go:76`, `grant.go` | ✓ |
| Flow 3 | one tx: approved + Precondition + Perform; `precondition` reject | `store.go:320-395` | ✓ |
| Flow 4 | any caller may reject | `internal/daemon/approval.go:141-150` | ✓ |
| Headless | `DORYLINAE_APPROVAL=terminal`; stderr and stdin must be terminals unless `DORYLINAE_DEBUG=1`; exit 2 | `internal/daemon/approval.go:43-54`, `cmd/agentnetd/main.go:124` | ✓ |
| Headless | `status.approval` = `terminal`/`terminal-debug`; audit `approval.mode`; start notification | `daemon.go:61,435-443` | ✓ |
| Headless | stderr line format; 128-byte lines; tag prefix ≥ `a-`+6, ambiguous uses no attempt | `store.go:208-209`, `approval_terminal.go:17-100`, `store.go:18-41` | ✓ (T6b-11 note) |
| IPC | `approval_list/open/reject`; `approval_open` terminal → `bad_request` | `internal/daemon/approval.go:117-151` | ✓ |
| IPC | view fields incl. `attempts_left`, `window` | `approval.go:207-218` | ✓ |
| IPC | error codes | `internal/daemon/approval.go:89-110` | ✓ |
| CLI | `approve [--list\|--open\|--reject] [--json]`; positional = exit 2 | `cmd/agentnet/approve.go:67-87` | ✓ |
| Audit | create/approve/reject/bad_code/locked/mode/open shapes | `store.go:187,386,448-450,582,598-602,723` | ✓ except T6b-03; extra `approval.limit` T6b-07 |
| Tables | migration 15/19 schema; `approval.wrong_codes` setting; 30-day prune | `store.go:273-283,437-439`; `settings.go:15`; `store.go:560-568` | ✓ |
| IPC/CLI docs | argv exposure and `hidepid=2` documented (D20) | — | ✗ T6b-06 |

### work-session.md

| § | Statement | Code | Verdict |
|---|---|---|---|
| Session id | derived SHA-256 id, vector | `internal/worksession` `DeriveID`; vectors (C29) | ✓ |
| State machine | accept creates the session row in the same tx | `internal/worksession/hooks.go:23-45` | ✓ |
| Transitions | each A transition checks role and exact state; one tx with `ws.state` + `SubmitTx` | `transitions.go:95-102,148-157,209-217,280-285,335-343` | ✓ (C21) |
| Transitions | close ends every grant in the same tx | `daemon.go:486-488`, `transitions.go:54-70` | ✓ |
| Mirror | steps 1–5, `ws.orphan {session,…}` | `mirror.go:73-212,263` | ✓ except C21-06; O-131/C22-01 |
| Closing | B completes with the **accepted** result | `mirror.go:212` | ✗ C21-01 |
| Early complete | drop content under quarantine rule; close if `open` | `hooks.go:70-146`, `request/mirror.go:229` | ✓ (O-130 fixed; C18-01 for decline/cancel) |
| Phase 1 requester | fallback on `unsupported_kind` | `phase1.go:29-161`, `daemon.go:473-481` | ✗ C21-04, C22-02 |
| `request_complete` with session | shorthand for `ws_result` | `internal/daemon/request_lifecycle.go` (C18) | ✓ |
| Result object | D14 + `verification`, `notes`; 65536-byte body | `result.go:111-116`, `receive.go:80-86` | ✓ |
| Accept-result | `--human` → approval kind `accept_result`, seq-bound | `internal/daemon/session.go:396-431` | ✓ |
| Request changes / Discard | from quarantined without release; result deleted in tx | `transitions.go:168-170,226-241` | ✓ |
| Quarantine | both clauses (ever active; 7 d peer-wide) | `internal/capability/store.go:300-325` | ✓ (C09) |
| Quarantine | views show sizes only; inbox copy blank (D18) | `store.go:235`, `session.go:199-203`, `receive.go:94-162` | ✓ |
| Quarantine | release: approval kind `release`, audit `ws.release {…, approval}` | `session.go:507-578` | ✓ (approval id closure O-147) |
| Cancel | A in `open`; B sends `ws.cancel`; `cancel: requested\|refused` | `transitions.go:264-309`, `cancel.go` | ✗ C21-02 (`refused` never set) |
| Kinds | body rules for `ws.result`, `ws.state`, `ws.cancel` | `receive.go:66`, `mirror.go:80-120`, `cancel.go:133` | ✓ |
| Kinds | `ws.result` step 4 re-send `last_state` (10-min rule) | `receive.go:242-280` | ✓ |
| Persistence | migration 14 schema (+ `kind` column, later migration) | `internal/store/store.go:244-268` | ✓ |
| IPC | method set, params, errors | `internal/daemon/session.go:22-29,237-579` | ✓ |
| IPC | A-side results carry `mail_id` | `session.go:388,454,477,498` | ✗ C21-05 |
| IPC | `r-` id resolved through its session | `internal/worksession/store.go:268-278` | ✗ C21-03 / C22-03 (ambiguity) |
| IPC | list views omit `output`, `notes`, `changes` | `session.go:183-197` | ✓ |
| IPC | session view `grants: [<grant summary>]` | `session.go:178` | ✗ T6b-02 |
| IPC | `ws_list` newest `state_at` first | `worksession/store.go:335` | ✓ |
| CLI | `sessions`, `session`, `result`, `wait`, `accept-result` flags | `cmd/agentnet/session.go:55-502` | ✓ |
| CLI | `agentnet release <id>` | `session --release` only | ✗ T6b-08 |
| CLI | `wait` timeout default 300 / max 3600, exit 4 | `cmd/agentnet/session.go:493,502` | ✓ |
| Notifications | `session.quarantined` text | `internal/daemon/quarantine.go:47` | ✗ O-149 |
| Notifications | `session.result`, `session.changes` events | `internal/notify/settings.go:34-42` | ✓ (C27) |
| Audit | `ws.open` | — | ✗ T6b-01 |
| Audit | `ws.result`, `ws.result_in`, `ws.release`, `ws.accept_result`, `ws.request_changes`, `ws.discard`, `ws.cancel`, `ws.cancel_in`, `ws.state`, `ws.close`, `ws.ignored` | `submit.go:88`, `receive.go:225-232`, `transitions.go:118,185,249,309,379,429-438,488`, `cancel.go:75,223`, `mirror.go:270-277`, `hooks.go:189` | ✓ |

### grant.md

| § | Statement | Code | Verdict |
|---|---|---|---|
| Grant object / Token | members, `exp` 1 min–7 d, 2048-byte token, domain-prefixed sig | `internal/capability/token.go:24-28`, `verify.go` | ✓ (C09-01 holder re-marshal) |
| Verification | steps 1–10 in order, fail closed | `verify.go:110-240` | ✓ (C09) |
| Issuance 1–2 | session, role, state, D5 | `internal/daemon/grant.go:400-450` | ✓ |
| Issuance 3 | forbidden config dir, home, root; `.git` | `grant.go:133-216` | ✗ C11-04, C11-02, C11-03, C11-05 |
| Issuance 4–5 | default 2 h, cap 7 d; sensitive unless `--public` git | `grant.go:39`, `token.go:27-28` | ✓ |
| Issuance 7 | policy → `grant.auto`; else approval kind `grant`; recheck 1–3 in tx | `grant.go:488-630` | ✓ (C11-08 race) |
| Policies | ≤ 50, `until` 30 d default / 90 d max, match rules, removal with peer | `capability/store.go:470`, `grant.go:44-45`, `grant.go:796-802` | ✓ |
| Kinds | `grant`, `grant.revoke` bodies; holder apply, orphan, conflict, revoke only from `msg.from` | `internal/daemon/grant_kinds.go:30-190` | ✓ (C09-02) |
| Transport | `ts` −30 s…+10 min, `req` 11 min, per-holder store, ≤ 2 rejects | `internal/capability/fetch.go:30-44` | ✓ |
| Transport | fragments 32768; read ≤ 262144; list ≤ 1000 / ~48 KiB | `internal/capability/fs.go:21-32` | ✓ |
| Revocation | one tx: revoke + `grant.revoke` mail; next fetch fails | `grant.go:666-720` | ✓ |
| Session end | all grants revoked in close tx; pending approvals rejected `precondition` | `daemon.go:486-488`, `approval/store.go:665-705` | ✓ |
| Serving fs | `os.OpenRoot`, per-component `Lstat`, `.git` any case, 8.3, 8 MiB | `fs.go` | ✓ (C09-04 local TOCTOU note) |
| Serving git | env scrub, `-c` overrides, 10 s, 32 MiB / 100 000 entries, D23 | `internal/capability/git.go:24-31` | ✓ (C10) |
| Limits | 2/grant, 2/peer, 32/daemon, 20/s, 256 MiB/24 h | `fetch.go:30-34` | ✓ (C10-02 fixed window) |
| IPC | methods, params, error codes; `grant_revoke` duplicate | `grant.go:630-720`, `fetch_client.go` | ✓ |
| IPC | `fetch_status` keep 60 s / 64 | `fetch_client.go:36-38` | ✓ |
| CLI | `grant`, `grants`, `revoke`, `grant policy …`, `fetch` flags | `cmd/agentnet/grant.go:34-40,119-122,293-299`, `fetch.go:90-94` | ✓ |
| Sensitive | `agentnet release` | `session --release` | ✗ T6b-08 |
| Audit | `grant.create/auto/issue/in/revoked_in/fetch/fetch_summary/orphan/conflict/refused` | `grant.go:448,520,543-547,607,623`, `grant_kinds.go:113-119,187`, `fetch.go:717-724` | ✓ |
| Audit | `grant.revoke` for daemon reasons | — | ✗ T6b-04 |
| Audit | `grant.policy_add/remove` actor and detail | `grant.go:951,992` | ✗ C11-09 |
| Audit | fetch rows flushed on close | `fetch.go:187-197` | ✗ C10-01 |
| Tables | `grants` schema | `store.go:305-328` | ✓ |
| Tables | `grant_policies` schema | `store.go:285-296,329-331` | ✗ T6b-10 |

### consult.md

| § | Statement | Code | Verdict |
|---|---|---|---|
| Request additions | `context` only for `question`/`debate`; 1–8 files | `internal/request/validate.go:153-175` | ✓ |
| Context files | name ≤ 255 bytes, text ≤ 65536, no controls but `\n\t` | `internal/request/request.go:16-21`, `validate.go:163-175` | ✓ (O-142) |
| Size | `MaxQuestionBody` 327680 for question with context | `request.go:16`, `canonical.go:96-103` | ✓ |
| Audit | only `context_files`, `context_bytes` | `internal/daemon/request.go:267-268`, `receive.go:464-465` | ✓ |
| consult CLI | flags; default title 120 code points with `…` | `cmd/agentnet/consult.go:63-73,159-175` | ✓ |
| Submit result | gains `session` for every type | `internal/daemon/request.go:131` | ✓ |
| Daemon CRLF → LF on context | `internal/daemon/request.go:218` | ✓ |
| `request_show` accepts a derived `s-` id | `internal/daemon/consult.go:52`, `query.go:42-61` | ✓ (C19-05 cost) |
| Answering | one-step accept + open + `ws.result` on pending/deferred question; defaults `n/a`/`none` | `internal/daemon/session.go:303-341`, `consult.go:61-76` | ✓ (C19-04 id collision) |
| Inbox | `inbox_list` shows counts not context | `request_lifecycle.go:116-117` | ✓ (C19-01 brief size) |

### device.md

| § | Statement | Code | Verdict |
|---|---|---|---|
| Model | `device_links` written only by link handlers | `internal/device/device.go` (C13) | ✓ |
| Link flow 1–4 | constant-time fp compare, D5, hierarchy, approval kind `device_link`, trust→fingerprint, intent 10 min, offer rules (D22), link id | `internal/daemon/device.go:358-498`, `internal/device/device.go` | ✓ (C13-02 note) |
| Scope | approval kind `device_scope`, 16384-byte summary cap, newer rejects older | `internal/daemon/device_scope.go:31,191-278` | ✗ C14-02 (window cut), C14-04 (race) |
| Scope members | types, repos 1–16, commands 1–32, argv, env, timeout 1–3600, expires ≤ 30 d | `internal/device/scope.go:31-43` | ✓ (C13) |
| Program ownership | set time and start time, Unix/Windows rules | `internal/device/perm*.go` | ✓ except C13-01 (`#!` interpreter) |
| Running | in-scope checks 1–6 in order; check enum | `internal/daemon/device_run.go:232-275`, `scope.go:37-43` | ✓ |
| Running | repo re-check refuses junction | `internal/device/runner.go:180-190` | ✗ C14-01 |
| Running | env allowlist, 32 KiB tail, sanitising, summary formats | `runner.go:207-249`, `device_run.go` | ✓ (C14-05 note) |
| Limits | 1 running, 8 queued, 60/controller/24 h, state in `device.runs` | `device_run.go:29-30,254` | ✓ (C14-03 wedge) |
| Hierarchy | `device_cycle`, depth 1, ≤ 8 helpers, 1 controller | `internal/device/device.go:51-53,297` | ✓ |
| Unlink | one tx, mail always sent; remote revokes only `msg.from` | `internal/daemon/device.go:300,511-570` | ✓ |
| Unlink | `peers remove` sends `device.unlink` | — | ✗ O-122 (spec says "revokes locally" only) |
| Kinds | `mail_submit` refuses `device.*` | `internal/daemon/outbox.go:25` | ✓ |
| IPC | six methods and results | `device.go:358-570`, `device_scope.go:191-330` | ✓ |
| CLI | `device link/list/unlink/scope` flags, default timeout 900 | `cmd/agentnet/device.go:67-69`, `device_scope.go:19,198-203` | ✓ |
| Audit | `link_intent`, `link_active`, `unlink {side}`, `scope_set`, `scope_clear`, `run`, `out_of_scope` | `device.go:243,300-306,338,441,465,547`, `device_scope.go:250,296`, `device_run.go:261,527,549` | ✓ |
| Tables | migration 17 | `internal/store/store.go:335-365` | ✓ |
| Approval reject on unlink / clear / supersede | frees at once | `device.go:389,560,566`, `device_scope.go:215,305` | ✓ behaviour, ✗ audit reason T6b-03 |

### Docs/cli pages

| Page | Checked against | Verdict |
|---|---|---|
| `approve.md` | `cmd/agentnet/approve.go` flags, exit codes, JSON | ✓ (missing D20 note: T6b-06) |
| `session.md` | `cmd/agentnet/session.go` (`sessions`, `session`, `result`, `wait`, `accept-result`, `--release`) | ✓ (shows `grants: []`, see T6b-02) |
| `grant.md` | `cmd/agentnet/grant.go` flags | ✓ |
| `fetch.md` | `cmd/agentnet/fetch.go` (`--out`, `--list`, `--stat`, `--timeout`) | ✓ |
| `device.md` | `cmd/agentnet/device.go`, `device_scope.go` | ✓ |
| `consult.md` | `cmd/agentnet/consult.go` | ✓ |

## Assumptions and contracts

- `approval.Store.Reject` is used only for human rejections. **Checked:** it is not. Daemon
  callers reuse it (`device.go:389,560,566`, `device_scope.go:215,305`, `daemon.go:506`),
  which causes T6b-03.
- `worksession.Store.RevokeGrants` callers audit the revoked ids. **Checked:** they do not
  (`daemon.go:487`); see T6b-04.
- `sessionView` receives grant data from a caller. **Checked:** nothing passes it
  (`session.go:154-212`); see T6b-02.
- `capability.GrantIDOf(verr)` returns the token's id even when Verify fails at step 7
  (`grant_kinds.go:73`). **Unchecked** for malformed-but-signed tokens; used only for the
  orphan audit.
- The chunk reports' reading of each area is taken as known drift. **Checked** for the
  Spec fields listed under Questions 4.

## Checked and fine

- O-134 appears fixed: `grant.orphan` now carries `{grant, peer}` (`internal/daemon/grant_kinds.go:73,113`).
- O-130 appears fixed (per C22): early-complete close audited (`hooks.go:125-146,189`).
- Every Phase 2 limit and constant in the drift tables matches its spec value.
- Every IPC method in the five specs is registered, and no Phase 2 method is registered without a spec.
- Every `device.*` audit row matches device.md's detail shape, including `side`, and the `out_of_scope` check enum.
- Every `ws.*` audit row except `ws.open` and `ws.orphan` matches work-session.md, and none carries content.
- The approval error-code mapping matches approval.md, and `bad_code` is never returned over IPC from a code path, because no IPC method takes a code.
- The Consult CRLF normalisation is daemon-side, and the default title is always ≤ 120 code points.
- The approvals and device table schemas match their specs (migrations 15, 17 and 19).

## Leads for other chunks

- `internal/approval/store.go:635-657`: `Reject` is also the entry for daemon rejections; the T7/T9 audit themes may want the reason semantics (T6b-03).
- `Docs/protocol/audit.md`: its action inventory should gain `approval.limit` and confirm whether `ws.open` is still intended (T6c).
- `internal/daemon/audit_inventory_test.go`: `request_accept` is not required to produce `ws.open`, so the inventory test cannot catch T6b-01.

## Notes

- `internal/approval/store.go:397-399`: the comment on `checkExpiryLocked` says the lock is released around the write; it is not (also C12 Notes).
- `approval_open` audits a reopen even when the window is already open. That matches Docs/cli/approve.md ("still audited") and approval.md.

## Commands run

- Read-only: `grep`/`sed`/`cat` over `Docs/protocol`, `Docs/cli`, `internal/{approval,worksession,capability,device,request,store,daemon}`, `cmd/agentnet`; a loop comparing each registered Phase 2 IPC method against `Docs/protocol/ipc.md` and `00-index.md` (26 methods; only `ws_discard` missing from ipc.md).
- `git log -1 --oneline` → `ef03d14` (docs only on top of 6cc26a7).
- No `go test` was run: every finding is a read of a missing call or string. The packages involved are `internal/daemon` and `internal/device`, which cannot run on this PC.
