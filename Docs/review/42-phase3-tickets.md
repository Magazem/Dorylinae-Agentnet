# 42: Phase 3 tickets (3.1–3.7: debate, decision records, audit chain, experience record)

Status: **approved by the owner 2026-09-25 (D30 in HANDOFF), all OD-P3-1..14 as recommended; adversarially reviewed ([43](43-phase3-spec-review.md), fixes applied).** No code starts
before approval (HANDOFF rule 3). Specs: [debate.md](../protocol/debate.md),
[decision.md](../protocol/decision.md), [audit.md](../protocol/audit.md),
[experience.md](../protocol/experience.md), and small additions to
[request.md](../protocol/request.md) (type `debate`, member `debate`),
[consult.md](../protocol/consult.md) (`context` also on `debate`) and
[ipc.md](../protocol/ipc.md) (audit note).

Every ticket follows the plan's exit criteria, as in Phases 1 and 2: acceptance tests are
automated Go tests; `go vet` and the linter pass; `--help` and `--json` output are
documented in `Docs/cli/`; audit events exist; new tests use `internal/testutil.TempDir`;
async state and audit are polled with a deadline, never read once; test variables written
by goroutines are guarded by a mutex or an atomic (review 31). "e2e" means the in-process
two-daemon + relay harness. "Fake clock" means injecting `Now`.

## Scope rule

Only what the Phase 3 acceptance tests (plan 3.1–3.7) need. Deferred, with the reason in the
spec: grants inside a debate (OD-P3-4), quarantining debate entries (OD-P3-4), three-way
debates and external judges (plan), early `--escalate` (OD-P3-7), human-approved signing
(OD-P3-5), signed audit checkpoints and audit heads in Decisions (OD-P3-6), audit pruning
(audit.md), a read command and agent notes for experience records (OD-P3-8), the JSON
embedded in the Markdown (OD-P3-10), mixed Phase 2/Phase 3 debates (documented limitation),
and the Phase 2 leftovers listed in [Phase 2 leftovers](#phase-2-leftovers).

## Migrations (pre-assigned)

`internal/store` requires consecutive versions, so tickets that add a migration **merge in
migration order**; a branch built early uses a clearly marked NO-OP placeholder that the
Orchestrator replaces at merge (never merge a placeholder). **Every migration's tables must
also be added to the DROP lists of BOTH rewind tests in `internal/store/store_test.go`**
(`TestMigration8PreservesPeers`, `TestMigrationAddsPeerTrust`), and each ticket's
acceptance includes that both still pass.

**New in Phase 3:** migrations 18 and 19 **alter or rebuild existing tables**. The rewind
tests reopen the database and re-run every migration above 7 (or 2), so a migration that
alters a table the rewind does not drop fails on the second run. Each such ticket makes the
rewind tests drop **and recreate the table in its pre-migration form** (noted per row).

| # | Name | Tables | Ticket | Rewind-test note |
|---|---|---|---|---|
| 18 | `audit_chain` | `audit_events` + column `hash`, trigger `audit_events_chained` | 3.6a | both tests: `DROP TABLE audit_events`, then the migration-1 `CREATE TABLE`, index and two triggers. 3.6a also makes `store.apply` re-read the version under `BEGIN IMMEDIATE` (review 43 M9: `agentnetd install` and the daemon migrate concurrently after an upgrade) |
| 19 | `debates` | rebuild `requests` (type `debate`), rebuild `approvals` (kind `debate_constraint`), `work_sessions` + column `kind`, new `debates`, `debate_entries`, `debate_constraints` | 3.1a | `requests`, `approvals`, `work_sessions` are already dropped by both tests; add the three new tables. Both rebuilds use **explicit column lists** (review 43 M10). A separate test fills every column of `requests` and `approvals` with distinct values in rows of every state, migrates, and compares **every column of every row**, the `result` column, and all indexes by `sqlite_master` (including the unique partial idempotency index, then proven by a duplicate insert) |
| 20 | `decisions` | `decisions` | 3.3a | add `decisions` |
| 21 | `experience_records` | `experience_records` | 3.7 | add `experience_records` |

## Tickets

"Review" marks tickets that need an **Opus security review before merge** (HANDOFF rule 4).
Sizes: **S** ≈ half a day of agent work, **M** ≈ one day, **L** = at the one-day limit.
"Model" is the suggested worker per D26/D28: **Opus** for security-critical code (crypto,
signing, integrity, parsing peer input), **Sonnet** (normal, thinking on) for feature work
with design choices, **Lite** (Worker-Sonnet-Lite, thinking off) for routine, well-scoped
work. "∥" lists tickets that can run in parallel once dependencies merge (at most ~3
implementation workers at once, HANDOFF §5).

| ID | Title | Depends on | Migration | Size | Review | Model | ∥ |
|---|---|---|---|---|---|---|---|
| 3.6a | Audit chain: migration 18, chained `Append`/`AppendTx`, `audit.chain_start`, `Verify`, vectors, rewind tests | specs approved | 18 | M | **yes** | Opus | 3.2 |
| 3.2 | Debate message schemas: `internal/debate` validators (position, evidence, challenge, targets, move, proposal, answer, disagreement), the free-text rule, caps, fuzz | specs approved | — | S | **yes** (with 3.1a) | Opus | 3.6a |
| 3.1a | Debate core: request type `debate` (+ requests rebuild), commitment and reveal, kinds `debate.*`, slot/turn engine on A and mirror on B, quarantine edges, `reservedKindPrefixes`, work-session `kind`, `approvals` rebuild | 3.2; **merge** after 3.6a | 19 | L | **yes** | Opus | 3.6b |
| 3.6b | `agentnet log`: `audit_list`, `audit_verify`, `--since/--until/--session/--action`, `--verify`, `--head`, `--anchor`, `TestAuditInventory`, review-28 L9 | 3.6a | — | M | — | Sonnet | 3.1a, 3.1b |
| 3.1b | Debate IPC and CLI: `debate`, `debates`, `debate_submit` (incl. one-step accept + position), `wait` for debates, the timeout sweep wiring (the rule itself is 3.1a), cancel/abandon, notifications, `Docs/cli/debate.md` | 3.1a | — | M | — | Sonnet | 3.4, 3.6b |
| 3.4 | Human constraints: `debate_constrain`, approval kind `debate_constraint`, kind `debate.constraint`, visible-only text, limits (`excess`), late constraints | 3.1a | — | S | **yes** | Opus (review 43 L8: approval gate + peer input, D26) | 3.1b, 3.6b |
| 3.3a | Decision: derivation, canonical form, signing exchange (`debate.close`/`debate.sign`), refusal and silence, `decisions` table, vectors in `tools/specvectors` and `tools/verifyvectors` | 3.1b, 3.4 | 20 | M | **yes** | Opus | — |
| 3.3b | Decision output: `decision_list/show`, `agentnet decisions`, `decision <id> [--json\|--md]`, `decision verify` (offline), the inert Markdown renderer with golden files | 3.3a | — | M | **yes** (with 3.3a; review 43 L8: Markdown inertness is the security property) | Sonnet | 3.7 |
| 3.7 | Experience record: builder, migration 21, write in every closing transaction, the "never in the record" tests | 3.3a | 21 | S | — | Sonnet (review 43 L8: privacy invariant in every closing transaction, not routine per D28) | 3.3b |
| 3.9 | Phase 3 e2e, `TestPhase3AuditHasNoContent`, docs reconciliation, known limitations | 3.3b, 3.6b, 3.7 | — | M | — | Sonnet | 3.H |
| 3.H | Headless harness run (3.1 acceptance): Claude Code + agy, both rounds; stand-in debate mode; weekly workflow | 3.3b, 3.4, 3.6b | — | M | — | Sonnet | 3.9 |
| 3.P | Phase 3 push (`main`; a `phase-3` tag **only with the owner's OK**) | all above | — | — | — | — | — |

Plan step **3.5 (escalation)** has no ticket of its own: the `escalated` outcome is in 3.1a
(the turn engine and timeouts), its notification in 3.1b, and the signed escalated record in
3.3a; the forced-disagreement acceptance test is in 3.3a (Go e2e) and 3.H (stand-in).

Critical path: 3.2 → 3.1a → 3.1b/3.4 → 3.3a → 3.3b → 3.9/3.H. 3.6a and 3.2 start at approval;
3.6a merges first (migration 18 before 19). Opus reviews: **four batches**: 3.6a alone;
3.2 + 3.1a; 3.4 (approval gate); 3.3a + 3.3b (signing and Markdown inertness).

## Ticket details

### 3.6a Audit chain (review)

- Files: `internal/audit` (chained append, chain start, `Verify`, streaming walk), migration
  18 in `internal/store/store.go`, `internal/store/store_test.go` (both rewind tests: drop and
  recreate `audit_events` in its migration-1 form), `cmd/agentnetd/install.go` (nothing
  changes, but its separate-process append is tested), `tools/specvectors`,
  `tools/verifyvectors`, `Docs/protocol/ipc.md` (audit note), and tests.
- Acceptance: the chain vector of [audit.md](../protocol/audit.md#vector) byte for byte,
  recomputed independently by `go run ./tools/verifyvectors`; a database with legacy rows
  migrates, the first append writes `audit.chain_start` with the right counts, and `Verify`
  is `ok`; **3.6: changing one field of one row (with the triggers dropped in the test) makes
  `Verify` report `hash_mismatch` at that row**; so do a deleted middle row (`gap` or
  `hash_mismatch`), a swapped pair, a changed legacy row (`chain_start`/`hash_mismatch`), an
  inserted unchained row (`unchained`; the trigger normally refuses it: asserted too); a
  truncated tail still verifies (documented limit); `--anchor` of a rewritten prefix fails;
  `AppendTx` rolled back leaves no row and no gap; an `Append` from a second process (a
  second `sql.DB` on the same file) while the daemon's handle holds a write transaction
  succeeds after the lock is released, and the chain stays valid; `TestAppendListAndAppendOnly`
  still passes; both rewind tests pass. **Review 43:** two concurrent `store.Open` calls on one
  file at schema 17 both succeed and apply each migration once (M9, `store.apply` under
  `BEGIN IMMEDIATE` with the version re-read); `Verify` walks in pages and an IPC call and a
  mail apply complete while it verifies 10⁵ rows (M8); an anchor on a legacy row is
  `bad_request`.

### 3.2 Debate message schemas (review, with 3.1a)

- Files: new `internal/debate` (schema types, `ValidatePosition`, `ValidateMove`,
  `ValidateProposal`, `ValidateAnswer`, target parsing and resolution against a position,
  `MaxDebateEntry`), and tests.
- Acceptance: **3.2: every entry kind validates against its schema; free text only in
  `argument`** (a table test: a `\n` in every non-`argument` string is refused, in
  `argument` it is accepted; `\t` likewise; ESC and U+007F refused everywhere); each field at
  its limit and one over; missing and extra members; `null` refused; targets: every form,
  out-of-range, leading zero, duplicates, 6 targets; canonical entry 32768 / 32769 →
  `entry_too_large`; a fuzz test that the validators never panic and never accept a
  non-canonical re-encoding.

### 3.1a Debate core (review)

- Files: `internal/debate` (store, slots and turns, commitment, reveal, apply on A, mirror
  on B, early entries, echo, close decision without the Decision itself: 3.3a adds it),
  `internal/request` (type `debate`, member `debate`, `context` on `debate`), migration 19,
  `internal/worksession` (`kind`, refuse result/accept-result/changes/release/discard on a
  debate, `ws.ignored {reason: "kind"}` with a blank inbox copy), `internal/capability`
  (`debate_open` on sensitive grants), `internal/daemon/mail.go` (register kinds),
  `internal/daemon/outbox.go` (`reservedKindPrefixes` += `debate.`, `decision.`),
  `internal/store/store_test.go`, `tools/specvectors` and `tools/verifyvectors` (the
  commitment vector), `Docs/beta/known-limitations.md` (Phase 2 peers cannot debate), and
  tests.
- Acceptance: the commitment vector; the requests rebuild keeps every row, the `result`
  column and all indexes (a test with rows in every state, including an idempotency key);
  a Phase 2-shaped request body with `type: "debate"` or a stray `debate` member is
  `bad_body`; **turn engine** (`TestDebateTurns`, a table over `phase × slot × author`):
  only the listed next slot is accepted, everything else is `not_your_turn`/`bad_state` at
  IPC and `debate.ignored` on receipt; the converge rules (two empty moves in a row, and
  round `rounds` used up) on both sides give the same next slot; A reveals only after
  applying B's position, in the same transaction; B never has A's position before its own
  position is committed to its outbox (asserted by inspecting B's tables at each step); a
  reveal with a changed position, a changed nonce, another session's commitment or the
  commitment claimed by B → `broken` on B, `debate.reveal_bad`, a `ws.cancel` to A, no
  Decision; entries overtaking each other are held and applied in order; a body moved to
  another session fails the derived-id check; timeouts (fake clock): missing slot 1 →
  `cancelled`/`timeout`, a later slot → `escalated`/`timeout`; **quarantine edges**:
  `quarantine_active` on start (A) and on accept (B) under the rule-2 test, `debate_open`
  on a sensitive `grant_create` to a peer in an open debate; `mail_submit` refuses
  `debate.x` and `decision.x`; the work session of a debate refuses `ws_result`,
  `ws_accept_result`, `ws_request_changes`, `ws_release`, `ws_discard` and ignores a
  `ws.result`; both rewind tests pass. **Review 43:** a slot-1 entry that reaches A before the
  `request.accept` opens the session and is applied, and the reveal is sent (M3); a
  `request.complete` from B during an open debate closes it `cancelled` on A with result and
  note dropped and the inbox copy blank (M4); `debate_open` also while `invited`, at
  `approval_confirm` of a pending sensitive grant, and on the policy path (M5); an
  idempotent `debate` retry with a different position is `idempotency_conflict` (L11); C1
  characters and U+2028/U+2029 are refused in every debate string (L1); a reveal with a
  malformed nonce or non-canonical position → `broken`; on `broken`, B's mirror, request and
  experience record close in the same transaction (L3).

### 3.6b `agentnet log`

- Files: `internal/audit` (queries, session resolution), `internal/daemon` (`audit_list`,
  `audit_verify`), `cmd/agentnet/log.go`, `Docs/cli/log.md` (including exit code 5),
  `internal/capability` (review 28 L9: `grant.orphan` gets `grant`), and tests.
- Acceptance: **3.6: `agentnet log --since 24h --json` and `agentnet log --session <id>`**
  (a session's request, grant, approval, decision and `ws.*` rows are all in the session
  view, and rows of another session are not); `--verify` exits 0 on a clean log and 5 after
  a tampered row, naming the row; `--head` and `--anchor`; paging with `after_id` over 2500
  rows; the human output of a detail containing ESC shows no escape; `TestAuditInventory`:
  every non-read IPC method and every registered mail kind produces its documented audit
  action in an e2e run (read-only methods listed as exempt).

### 3.1b Debate IPC and CLI

- Files: `internal/daemon` (handlers, `wait` support, timeout sweep), `internal/notify`
  (events `debate.constraint`, `debate.agreed`, `debate.escalated`, `debate.broken`),
  `internal/ipc/ipc.go` (result encoding with `SetEscapeHTML(false)`, review 43 M7),
  `cmd/agentnet/debate.go`, `cmd/agentnet/session.go` (`wait`), `Docs/cli/debate.md`,
  `Docs/cli/request.md`, and tests.
- Review 43 additions: `debate_show` of a maximal transcript made of `<`, `&` and non-ASCII
  text stays under the 1 MiB line (M7); `request_complete` on a debate request whose session
  is open is `bad_state` (M4).
- Acceptance: CLI tests for every form with human and `--json` output and exit codes;
  `--position-file` on a pending debate accepts and submits in one transaction (an injected
  failure leaves neither); `wait` returns `turn` when it becomes the caller's turn and
  `closed` at the end, 4 on timeout; `--help` shows one example of each entry kind;
  **3.5 (notification part)**: `debate.escalated` fires once on each side; webhooks carry no
  title unless `title: true` and never entry text; A's cancel in `invited` is a
  `request.cancel`; B's `--cancel` after a silent A abandons locally.

### 3.4 Human constraints (review)

- Files: `internal/debate` (constraints), `internal/approval` (kind `debate_constraint`,
  precondition), `internal/daemon`, `cmd/agentnet/debate.go`, `Docs/cli/debate.md`, and
  tests.
- Acceptance: **3.4: a constraint added with `--constrain` and approved (fake window) shows
  in both sides' Decisions under human decisions** (with 3.3a; until then, in both
  `debate_show` views); **without approval it appears nowhere** (pending, then rejected or
  expired: no row `active`, no mail, no Decision entry); the approval summary shows the
  full text and escapes bidi/zero-width characters; a constraint after the debate left
  `converge` is `bad_state`, and one confirmed after that is `rejected`/`precondition`;
  the 11th is `constraint_limit` (sender) / ignored (receiver); a constraint reaching A
  after the close is not in either Decision and is `late` on B; constraint text is in no
  audit row. **Review 43:** a constraint containing a zero-width character, a bidi control,
  U+FEFF or a tag character (U+E0041) is `bad_request` at `debate_constrain` and `bad_body` on
  receipt (H3); the approval summary uses the `DisplayQuote` rule; both sides adding their
  10th constraint at the same time end with identical Decisions (the receiver keeps the
  over-limit one, A's close decides; H2).

### 3.3a Decision object and signatures (review)

- Files: `internal/decision` (derivation, canonical, sign, verify), `internal/debate`
  (close, sign exchange, hold a close for missing entries, drop late own entries), migration
  20, `tools/specvectors`, `tools/verifyvectors`, and tests.
- Acceptance: the Decision vector (id, hash, both signatures) byte for byte, and every
  negative check of [decision.md §Vector](../protocol/decision.md#vector) fails at the stated
  step, recomputed independently by `verifyvectors`; **3.3: an e2e debate ends with the same
  Decision bytes on both sides and both signatures stored on both sides**; **3.5: a forced
  disagreement (`accept: false`) ends `escalated`, and the Decision is produced and signed by
  both**; a timeout after positions ends `escalated`/`timeout`, signed by both; a
  `cancelled` debate has no Decision; B refuses (`peer_refused` on both) when A's close
  claims `agreed` without B's accept, carries a wrong hash, a bad signature, cuts an A entry
  B holds, or claims `timeout` although B's answer was applied on A; a close overtaking the
  last entry is held and applied when the entry arrives; B offline → A `awaiting_peer`,
  then `signed` when B returns; derivation uses no local name or clock (two daemons with
  different names and skewed clocks produce identical bytes); both rewind tests pass.
  **Review 43:** a close that overtakes an A-authored **constraint** is held and applied when
  the constraint arrives, and both sides sign (H2); a close that counts a B slot or lists a B
  constraint B never sent is refused at once, not held (H2); a `debate.sign` with a wrong hash
  or bad signature makes A `peer_refused`; a worst-case Decision (every entry at 32768 bytes,
  two revisions, maximal proposal and answer, 10 constraints) derives under `MaxDecision` =
  786432 (M6); the vector without the respondent signature verifies with exit 6 and the
  step-5 negative fails at step 5.

### 3.3b Decision output (review, with 3.3a)

- Files: `internal/decision` (Markdown renderer), `internal/daemon` (`decision_list`,
  `decision_show`), `cmd/agentnet/decision.go`, `Docs/cli/decision.md`, golden files under
  `internal/decision/testdata`, and tests.
- Acceptance: **3.3: `agentnet decision <id> --md` writes a Markdown file** that matches the
  golden file for an agreed and an escalated debate; the output is identical on repeated
  runs and across OSes (LF only); an adversarial Decision (text containing `<script>`,
  `<img src=x onerror=…>`, `[x](javascript:…)`, `https://evil.example`, backtick runs of 1–10,
  `|`, `#`, `---`, a leading `>`, bidi overrides and zero-width characters, and a `~~~`
  fence) renders with **no raw HTML, no link, no autolink, no heading or table from peer
  text** (asserted by rendering the output with a CommonMark + GFM renderer in the test,
  e.g. `github.com/yuin/goldmark` as a **test-only** dependency (or, if the owner prefers no new module, a hand-written checker of the few constructs the renderer emits), and checking the HTML has no
  `<a`, `<img`, `<script`, `<table`, `<h` from peer text); `decision verify` works without a
  daemon, exits 0/6/1, and names the failing step; `--out` refuses to overwrite without
  `--force`. **Review 43:** the adversarial set also has a multi-line argument whose lines
  start at column 0 with `# `, `- `, `<div>` and `[x]: http://e` (no fence breakout, M2; every
  fence at column 0), zero-width and tag characters (rendered as `\u{…}`, M1), and a long
  argument full of newlines and tabs (kept, not collapsed); an `awaiting_peer` and a
  `peer_refused` Decision render the UNCONFIRMED banner and "Outcome claimed by the
  initiator" (H1); every human decision carries its "approved on … machine" label.

### 3.7 Experience record

- Files: `internal/experience` (builder), migration 21, the closing transactions in
  `internal/worksession` and `internal/debate`, `internal/store/store_test.go`, and tests.
- Acceptance: **3.7: a record exists per closed session and side** (accepted, cancelled,
  discarded-from-quarantine, debate agreed, debate escalated, debate cancelled); a marker in a
  quarantined-then-discarded result, a result replaced by request-changes without release, a
  dropped early complete and a withheld cancel reason appears in **no** record; the size cap
  truncates in the stated order; **no IPC method** (the test lists every registered method)
  **and no outbox row** contains record-only markers; a rolled-back close leaves no record;
  `experience.write` has no content; both rewind tests pass.

### 3.9 End-to-end and audit

- Files: `internal/daemon` e2e, `Docs/beta/known-limitations.md`, `tests/phase3-manual.md`
  (new: the Markdown viewed on GitHub, `log --verify` after a hand edit of the database with a
  SQLite tool, if the owner has one), docs reconciliation, the review-35 `secure_delete`
  item if OD-P3-13 (a) is chosen.
- Acceptance: one e2e test runs a debate with 2 rounds, a constraint from each side, a
  revision, a proposal and an accepted answer; then a second debate forced to escalate; both
  Decisions verify offline with two signatures; `log --verify` is `ok` on both sides and the
  `--session` view shows the debate's rows; experience records exist. **Audit has no
  content** (`TestPhase3AuditHasNoContent`): markers in topics, titles, context files,
  positions, arguments, evidence refs and notes, challenges, proposals, answers,
  disagreements, constraints and experience records appear in no `audit_events` row on either
  side. The Phase 2 test still passes. The docs match the behaviour.

### 3.H Headless harness run (3.1)

- Files: `tests/harness/phase3-agents.ps1` and `.sh`, `tests/harness/standin` (a `debate`
  mode), `tests/harness/README.md`, `Docs/agents/snippet.md` (a short debate paragraph),
  `.github/workflows/phase2-harness.yml` (a Phase 3 job, or a new `phase3-harness.yml`),
  and the record in `tests/phase3-manual.md`.
- **Reuse** the Phase 2 scaffolding unchanged: loopback relay, two daemons with
  `DORYLINAE_APPROVAL=terminal` and `DORYLINAE_DEBUG=1`, pairing, a team, the script (never
  the agent) reading approval codes from stderr and confirming on stdin, assertions from
  `--json` and the audit table, never from agent prose.
- **Turn-driven agents** (OD-P3-9): the script polls `agentnet debate <id> --json` and, when
  it is a side's turn, runs that side's agent headless once with a plain prompt ("Your
  AgentNet debate with <peer> is waiting for you. Take your next step, then stop."). The
  first prompt for A names the question and asks for a debate with at most 2 rounds; the
  first prompt for B says a teammate invited it to a debate. Prompts never name subcommands;
  agents learn them from the snippet and `--help`. The fixture is a small repository with
  two plausible designs of one function, so the agents have something to argue.
- **Constraint:** after both positions exist, the script runs `agentnet debate <id>
  --constrain "…"` on A and confirms the approval code on A's stdin, as a human would.
- Harnesses: Claude Code and agy, each once as initiator and once as respondent, like 2.H.
- Acceptance (from `--json`): both debates `closed` with `agreed` or `escalated` (a real
  agent may legitimately disagree), `rounds.current ≤ 2`; `agentnet decision verify` on the
  exported JSON is valid with **two** signatures; `decision --md` wrote a file; the
  constraint is in `human_decisions`; `log --verify` is `ok` on both daemons; exactly one
  debate request per round; the run finishes within 25 minutes.
- **Forced disagreement** is deterministic only with the stand-in (`--disagree` answers
  `accept: false`), so it runs in the **weekly CI job** (Linux, Windows, macOS) and in the
  3.3a e2e test, not with real agents.
- Cost note (corrected by review 43 L7): a 2-round debate takes **6–8** agent invocations
  (A's start with its position, B's position, up to 4 moves, the proposal and the answer;
  6 when both pass in round 1), about half of them Claude when Claude is one side. The run has
  two debates (Claude as initiator, then as respondent), so about **7–8 Claude invocations**:
  at the 2.H rate of ~0.2–0.4 USD each, roughly **1.5–3.5 USD per full 3.H run**, more if a
  turn is retried. Later turns re-read a growing transcript, so the upper end is likelier.

### 3.P Phase 3 push

`main` to origin after 3.9 and 3.H; the `phase-3` tag only with the owner's OK; HANDOFF
updated. Gate 1 of the plan (10 consecutive headless runs of request, grant, consult and
debate across two machines) is an owner activity after 3.P, recorded in
`tests/phase3-manual.md`.

## Owner decisions needed

| # | Decision | Options | Recommendation |
|---|---|---|---|
| OD-P3-1 | Commit–reveal shape (plan: "each side commits a position hash, then reveals") | (a) **one-sided**: the initiator commits inside the request; the respondent's position goes in the clear; the initiator's daemon reveals only after applying it; (b) symmetric: both commit after accept, both reveal after both commitments are in | **(a)**. Same property (neither opening position can be influenced by the other), two fewer mails and one fewer phase. The initiator's agent must write its position before sending, which is natural for the side that asks. *Review 43: confirmed.* Binding and hiding hold (256-bit nonce, domain-separated preimage with fixed-length fields before the one variable one, session and author bound, so no replay across debates); B learns only the commitment before its own position leaves its daemon. Either side can still open with a placeholder and revise after seeing the other's; that is equally true of (b), and the Decision shows `initial` and `final` |
| OD-P3-2 | How a debate is typed | (a) a new request type `debate` (rebuild `requests` for its CHECK); (b) type `task` plus a `debate` member (no rebuild) | **(a)**. Clean views and filters (`inbox` shows a debate as one), and Phase 2 daemons refuse it clearly. The rebuild is one migration with its own test |
| OD-P3-3 | Human constraint injection (3.4): can an agent forge a "human decision"? | (a) approval-gated (kind `debate_constraint`, window code); (b) no gate, recorded as "added through the CLI on <side>"; (c) gated only with `--human` | **(a)**. The plan puts it in a signed record as a *human* decision; without a gate any agent could write one. It costs the human one code per constraint, and the harness confirms it through terminal mode. *Amended by review 43 (H3, L16):* the gate only works if the human sees every character, so constraint text is **visible characters only** (no format or invisible characters), shown with the `DisplayQuote` rule. The gate protects the **local** side only: a modified peer daemon can add unapproved constraints under its own side's name, and the Markdown labels each one "approved on the X machine; the other side cannot check this" |
| OD-P3-4 | Grants and quarantine in debates | (a) no grants in debates; refuse debate start/accept while the peer-wide quarantine clause holds; refuse sensitive grants to a peer during an open debate; (b) allow grants and quarantine debate entries like results; (c) ignore (debate entries are "new mail" under OD-P2-15 (a)) | **(a)**. It closes the side door with three simple checks and no new quarantine states. (c) would let a worker return sensitive data through a debate one day after reading it. Revisit (b) if users want evidence fetched during debates |
| OD-P3-5 | Who signs the Decision | (a) the daemon, automatically, as an attestation of the transcript; agreement is B's `answer`; (b) each human approves the signature | **(a)**. A signature that waits for a human makes the escalated case (3.5) hang, and the human already expressed agreement (or not) through the answer. The Markdown states what a signature means. *Amended by review 43 (H1):* only a **two-signature** Decision proves anything about the respondent; a single-signed one is "unconfirmed" (verify exit 6, banner in the Markdown). See OD-P3-14 |
| OD-P3-6 | Audit anchor | (a) `log --head` and `log --verify --anchor ID:HASH` (manual anchors, free); (b) also send each side's head inside its Decision signature; (c) periodic signed checkpoints | **(a)**. (c) adds nothing against a same-user attacker who can read the key file fallback; (b) is cheap but leaks row counts to the peer and couples two features; add it if the beta shows a need |
| OD-P3-7 | Turn parameters | rounds 1–5 (default 2); `turn_timeout_s` 300–86400 (default 3600); the initiator proposes; converge after two empty moves in a row or when rounds run out; early `--escalate` deferred | **As specified** |
| OD-P3-8 | Experience record | (a) daemon-assembled snapshot, no read command, no agent notes, kept indefinitely; (b) add `agentnet experience <id>` (local only); (c) a retention limit (e.g. 365 d) | **(a) for Phase 3** (the plan says nothing reads it). Decide (c) before the beta, together with the other content tables |
| OD-P3-9 | 3.H agent driving | (a) turn-driven: the script invokes the agent whose turn it is, once per turn; (b) both agents run concurrently and loop on `wait` | **(a)**. Deterministic, no long-lived agent sessions, and a stuck agent is a clear per-turn failure. Costs 6–8 invocations per debate, about 1.5–3.5 USD of Claude per full 3.H run (review 43 L7) |
| OD-P3-10 | Decision file in a repo | (a) Markdown plus a separate `d-….json` for verification; (b) the JSON embedded in the Markdown | **(a)**. Embedding needs a fence around peer bytes inside a document humans read; two files are simpler and the Markdown names the JSON |
| OD-P3-11 | Review 38 L3: `changed` cannot detect a same-size `fs.read` rewrite between two reads | (a) defer (documented in `Docs/cli/fetch.md`); (b) add a version (size + mtime) to fs read responses (a grant.md protocol change, small ticket) | **(a)**. Not needed for Phase 3 acceptance; rare (files > 256 KiB rewritten mid-fetch). Do (b) in the 4.8 hardening pass |
| OD-P3-12 | OD-P2-2 (d): OS user-presence (Windows Hello `UserConsentVerifier`, macOS LocalAuthentication) on top of approvals | (a) defer to Phase 4 hardening (before 4.8); (b) Phase 3 ticket | **(a)**. It needs per-OS work and a manual test on each OS, and no Phase 3 acceptance test needs it. Constraint approvals (OD-P3-3) use the existing window |
| OD-P3-13 | Review 35: deleted quarantined bytes can survive in SQLite free pages and the WAL | (a) turn on `PRAGMA secure_delete=ON` in the store DSN (one line, in 3.9) and document that WAL frames persist until a checkpoint; (b) defer | **(a)**. Cheap, narrows the D18 promise gap, no protocol change |
| OD-P3-14 (new, review 43 H1) | Exporting a single-signed Decision (`awaiting_peer` or `peer_refused`) | (a) allow `decision --md`/`--json` with the UNCONFIRMED banner, "Outcome claimed by the initiator", and `decision verify` exit 6 (as now specified); (b) as (a), and also refuse `--md --out` of an unconfirmed Decision unless `--unconfirmed` is given; (c) never export until both signatures exist | **(a)**. The banner and the exit code already stop the file passing as agreed, and (c) would hide a record the initiator's human may need when the peer vanished. Choose (b) if the owner expects Decision files to be committed by agents without a human look |

### Phase 2 leftovers

Also deferred explicitly (none blocks Phase 3): review 24 L10 (cancel and accept crossing),
L15 (`peers remove` does not unlink a device); review 32 L4 (bidi characters in context file
names: the Decision stores names only as data and the Markdown renders them inert, so the
debate path is covered); review 34 L6 (COM0/LPT0, owner call); review 37 L3 (git include and
alternates); review 41 L4 (macOS ACLs) and L5 (UNC/NFS paths); OD-P2-7 (human-only preview of
quarantined results) and OD-P2-15 (worker content outside the result). Folded into Phase 3:
review 28 L9 (`grant.orphan` without `grant`, in 3.6b).

## Conflicts and interpretations found while writing

1. **CHECK constraints need rebuilds.** `requests.type` and `approvals.kind` are CHECK
   lists (migrations 11 and 15). SQLite cannot alter a CHECK, so migration 19 rebuilds both
   tables. `work_sessions.outcome` stays `accepted`/`cancelled`; the debate's own outcome
   (`agreed`/`escalated`/`cancelled`) lives in `debates`.
2. **Migration 18 alters a migration-1 table,** which the rewind tests never drop; they must
   now recreate `audit_events` (audit.md §Migration 18).
3. **The audit ts is the wall clock** (`time.Now()` in `internal/audit`), with no injected
   clock; 3.6b adds an optional `Now` for the `--since` tests. `agentnetd install` appends from
   a second process, which the chain must serialise (`BEGIN IMMEDIATE`).
4. **`agentnet log` does not exist yet**; `ipc.md` says "ticket 3.6 adds a chain column", which
   this spec keeps.
5. **The plan's `agentnet debate @peer --topic … --context-file …`** gains a required
   `--position-file` (the initiator's committed position, OD-P3-1).
6. **"Constraints" and "human decisions"** are two plan fields but one thing in Phase 3
   (decision.md §Object).
7. **Plan 3.6 "every daemon action".** `grant.fetch` rows are summarised beyond 60 a minute
   (Phase 2 design), so not literally every fetch has a row; the summary row counts them.
8. **Plan 3.7 fields** ("what worked", "what failed") cannot be written by a daemon without a
   model; they map to stored data (experience.md). `work_sessions` keeps only the **last**
   changes text, so earlier rounds' texts are not in the record.
9. **The IPC 2-second rule** is relaxed for `audit_verify` (like `wait`, a long local walk).
10. **`mail_submit`** must refuse the new kinds: `debate.` and `decision.` join
    `reservedKindPrefixes` (review 36 L7).
11. **`context` on `debate`**: consult.md allowed it only on `question`; it now also applies to
    `debate`, with the same caps.
12. **Plan 3.1 "each side commits"**: see OD-P3-1.
