# 43: Phase 3 spec review (adversarial)

Reviewer: P3-SpecReviewer (Opus, `claude-opus-5-5`), 2026-09-25. Target: branch `p3/specs` at
`bc3c45f`, worktree `AgentNet-wt/p3-specs`. Scope: `Docs/protocol/{debate,decision,audit,experience}.md`
(new), the Phase 3 edits to `README.md`, `request.md`, `consult.md` and `ipc.md`, and the ticket
plan `42-phase3-tickets.md`. Checked against plan Phase 3 (steps 3.1–3.7), HANDOFF §3 (D1–D29),
the Phase 2 specs (`work-session.md`, `approval.md`, `grant.md`, `consult.md`), and the code
where a claim depends on it (`internal/audit`, `internal/store`, `internal/ipc`,
`internal/notify`, `internal/agentcard/canonical.go`, `internal/capability`,
`internal/device/scope.go`, `internal/worksession`, `cmd/agentnetd/install.go`).

## Verdict

**Ready with changes, and one new owner decision.** The changes are applied in this
worktree. There are no Critical findings. There are 3 High findings, all fixed in the docs:
a single-signed Decision verified as "valid" with exit 0, so the initiator alone could
present a fabricated agreement (H1); an ordinary race between a constraint mail and the
close made an honest pair refuse each other's Decision (H2); and a "human-approved"
constraint could carry invisible characters that the human never saw but the peer's agent
reads (H3). There are 10 Medium findings, all fixed in the docs. There are 19 Lows: 13 fixed and
6 listed. No D1–D29 decision is reopened.

| Severity | Found | Fixed in docs |
|---|---|---|
| Critical | 0 | — |
| High | 3 | 3 |
| Medium | 10 | 10 |
| Low | 19 | 13 |

**OD changes:** OD-P3-1 confirmed with analysis; OD-P3-3 amended (visible-only text; the gate
protects the local side only); OD-P3-5 amended (only a two-signature Decision proves anything
about the respondent); OD-P3-9 cost corrected; new **OD-P3-14** (exporting a single-signed
Decision). The other ODs are confirmed (below).

## Vectors (recomputed independently)

A throwaway Go program in `%TEMP%` (stdlib `crypto/ed25519`, `crypto/sha256`,
`encoding/json` with an RFC 8785-style key sort; deleted afterwards) reproduced every vector
byte for byte:

| Vector | Spec value | Result |
|---|---|---|
| Keys from seeds `00…1f` / `20…3f` | `A6EHv_…Mbg` / `Kay64U…bdc` | match |
| Session id | `s-36375782ceb6baea9cee4d4273dfb035` | match |
| Commitment position is canonical | — | yes |
| Commitment | `a24d1e03…6cb81ed2` | match |
| Decision text is canonical (1316 bytes) | — | yes |
| Decision id | `d-7eaeb0b78e6bd96981357b500af94044` | match |
| `decision_hash` | `6ec367cd…36094ede` | match |
| Signature initiator / respondent | `oU23UZtZ…iDBnAw` / `xCdaoO0E…PTJv-Cg` | match / match |
| Audit genesis | `fa430260…6a979dd` | match |
| Audit rows 1 (virtual), 2, 3 | `554876a6…`, `c2878d54…`, `3daa4dae…` | match, and all three row texts are canonical |

Negatives: `outcome` changed to `escalated` fails signature verification under the original
signature; the respondent's signature does not verify under the initiator's key. The vector
Decision is consistent with the derivation rules (one round, two passes → converge rule 1,
`agreed`/`accepted`, `final_agreement` = the proposal's agreement, no optional member empty).

## What holds (checked, no finding)

- **Commitment binding and hiding.** SHA-256 over a domain string, then `sid`, `A` and the
  nonce, which have fixed lengths (34, 43 and 64 ASCII characters) and come before the one
  variable-length field, so the preimage parses one way only. The 256-bit nonce makes it
  hiding against "yes"/"no" guessing. `sid` covers both keys and the request id, so a
  commitment or reveal cannot be replayed into another debate, and B cannot claim A's
  commitment. The daemon, not the agent, draws the nonce and sends the reveal automatically
  in the transaction that applies B's position, so neither A's agent nor A's human can change
  the position after seeing B's (any change fails B's check). B learns nothing but the
  commitment before its own position has left its daemon.
- **One-sided commit (OD-P3-1).** A placeholder opening followed by a revision is possible
  for both sides, in (a) and (b) alike, and is visible in the record (`initial`/`final`).
  (b) adds no property.
- **Identical bytes on both sides.** `internal/agentcard/canonical.go` sorts keys by UTF-16
  code units, escapes only what RFC 8785 requires, and allows integers only. Entries are
  stored canonical and copied byte for byte; no normalisation (NFC) is applied anywhere, which
  is right: both sides hold the same bytes, and a third-party verifier must not normalise.
  Derivation uses A's `at` and A's `entries`, and no local name or clock.
- **Signature scheme.** Ed25519 over `"dorylinae-decision-v1\n" ‖ canonical(decision)`, a
  domain string distinct from mail, card, grant and audit; the signed content contains both
  keys, the session and the request, so a signature cannot move to another debate or pair.
- **Truncation by A.** A cannot cut its own applied entries (B checks), and dropping B's late
  `accept: true` yields `escalated`/`timeout`, which never passes as agreement.
- **Hash-chained audit.** The trigger `audit_events_chained` makes an unchained row
  impossible for any writer that does not drop triggers; the migration-1 update/delete
  triggers stay. The explicit `id = head.id + 1` is the primary key, so two writers cannot
  fork the chain. `row_c` hashes the stored `detail` text, so verification does not depend on
  Go's map marshalling. The legacy-row design (virtual hashes, `audit.chain_start` with counts)
  protects old rows from the migration on and says so. What `--verify` does not detect
  (whole-chain rewrite, tail truncation, never-written rows, wrong clocks) is stated plainly,
  and anchors close the first two up to the anchor.
- **Migration-18 rewind issue.** The writer's analysis is right: both rewind tests re-run
  every migration above 7 (or 2), migration 18 is the first to alter a migration-1 table, and
  `DROP TABLE audit_events` removes its index, triggers and `sqlite_sequence` row, so
  recreating the migration-1 form gives an exact version-7 (or version-2) schema. Migrations
  11–17 have no `FOREIGN KEY` and no trigger on `requests` or `approvals`, so the migration-19
  rebuilds are safe with `foreign_keys(1)` (M10 fixes the column copy).
- **No content in audit.** The new actions carry ids, enums, counts, sizes and hashes; the
  Decision hash covers far more than a guessable value. `audit.chain_start` carries counts.
- **Experience record read paths.** There is no backup, dump or export command in `cmd/`,
  no IPC method or mail kind touches the table, and daemon logs and debug mode
  (`DORYLINAE_DEBUG`) print no table content (spot-checked, not exhaustive). Only the accepted result's `status` and
  `summary` are copied, so D18-deleted bytes cannot come back.
- **Sizes of a single mail.** A 32 KiB entry, a reveal with a position, and a close
  (no Decision inside, only hash and signature) are all far under `MaxMailPlaintext`.

## High (all fixed)

| # | Area | Finding | Fix (file) |
|---|---|---|---|
| H1 | Decision / verify | A single-signed file (`awaiting_peer`) passed `decision verify` as **valid, exit 0**. With one signature, every respondent entry in the file, including `accept: true`, is only the initiator's claim. A modified initiator daemon, or anyone holding A's key, can produce an "agreed" Decision for a debate that never happened, and a script that tests the exit code accepts it. The signed file also carries no local state, so an exported `peer_refused` Decision looks the same | verify gains step 5 (derivation invariants) and `complete`. **Exit 0 only with both signatures, exit 6 = valid but unconfirmed** (3 is "daemon not running", 5 is audit "broken"). The Markdown of a single-signed or refused Decision starts with a fixed UNCONFIRMED banner and says "Outcome claimed by the initiator". New negative vectors (decision.md §Signing, §Signed file, §Vector, §Markdown, §Security; 42 §3.3a/3.3b; OD-P3-5 amended, new OD-P3-14) |
| H2 | Decision / constraints | B derived its Decision from the constraint ids in A's close, but an A-authored `debate.constraint` mail can be **overtaken by the close** (the spec itself says mail overtakes mail). B then lacks the text, derives different bytes and refuses: `peer_refused` and a `debate.broken` alarm on both sides of an honest debate. The same happened when both sides added their 10th constraint at once (each receiver dropped the other's as the 11th, but A's close listed its own). Separately, B would wait **forever** for a B-authored slot that a modified A counted but B never sent | B holds the close until it has every listed **A-authored** entry and constraint; anything B-authored that is counted or listed but that B never sent is refused at once as a mismatch. A is authoritative for the constraint set; receivers keep over-limit constraints (`excess` on A, `active` on B, at most 20 stored) instead of dropping them (debate.md §Human constraints, `debate_constraints.state`; decision.md §Derivation rule 9, §Signing step 2; 42 §3.3a/3.4) |
| H3 | Constraints (OD-P3-3) | The approval-gate claim "an agent cannot forge a human decision" failed for **what the human sees**. Constraint text only excluded C0 controls, and the approval summary went through `notify.Clean`, which removes controls and bidi marks but **not** zero-width characters, U+FEFF or the invisible tag characters U+E0000–U+E007F (checked in `internal/notify/notify.go`). A local agent could run `--constrain "No new dependency"` with an invisible tag-character payload. The human approves the visible text, and the peer's agent receives the hidden instruction inside a signed "human decision" | Constraint text is **visible characters only** (`unicode.IsGraphic` or space, no `unicode.Cf`), refused at the sender and receiver. The approval summary uses the review-40 `DisplayQuote` rule (`internal/device/scope.go`). The Linux argv exposure of the summary (D20) is stated (debate.md §Human constraints; 42 §3.4; OD-P3-3 amended) |

## Medium (all fixed)

| # | Area | Finding | Fix (file) |
|---|---|---|---|
| M1 | Markdown | The inertness rule relied on `notify.Clean`, which does not do what the spec said. It turns `\n` into spaces and collapses white space (so multi-line arguments would be destroyed), it truncates, it replaces with a space rather than `?`, and it leaves zero-width and tag characters in place | A dedicated `decision.Visible`: keeps `\n`/`\t` in multi-line text and renders every non-graphic, format (Cf) or C0/C1 rune as visible `\u{XXXX}`; fence and span lengths are computed after it (decision.md §Markdown; 42 §3.3b) |
| M2 | Markdown | The layout put per-side arguments under list structure. A fenced block **inside a list item** ends when a content line is indented less than the item's content column, and the rest of the peer text is then parsed as Markdown (headings, HTML, links): a fence breakout without any backtick | Every fence starts at column 0 outside any container, the per-side structure uses headings, and lists carry only single-line code spans. The adversarial golden test gains column-0 `# `, `- `, `<div>` and reference-definition lines (decision.md §Markdown; 42 §3.3b) |
| M3 | Debate turns | With the one-step accept + position, B sends `request.accept` and the slot-1 entry together. If the entry reaches A first, A "ignored" it (no open session). B never re-sends an entry it considers applied, and A's echo only helps B, so B's position was lost and the debate timed out `cancelled` | A slot-1 entry for an `invited` debate opens the session as if the accept had arrived first, as an early `ws.result` does in Phase 2 (work-session.md §Ordering) (debate.md §Submitting entries; 42 §3.1a) |
| M4 | Debate / request | Two gaps in a debate request. First, `agentnet complete`, which the snippet teaches, is the Phase 2 shorthand for `ws_result`, which debate sessions refuse, and the spec did not say what happens. Second, a `request.complete` from B while the debate is open (B's own abandon path sends one) was the Phase 2 early complete: it stored a result on a type that has none and left the debate open on A | `request_complete` on an open debate is `bad_state`. On A, an early complete on a debate always drops the result and note (inbox copy blank, D18) and closes an open debate `cancelled`; in `closing` it changes nothing (debate.md §Cancel and abandon; 42 §3.1a/3.1b) |
| M5 | Quarantine edges | `debate_open` covered only `positions`, `rounds` and `converge`. A could invite, then issue a sensitive grant to B in another session while the debate is `invited`, and B's accept (which checks B's grants only) opened the debate while rule 2 held on A: the side door OD-P3-4 closes. Pending-approval sensitive grants and the policy path were not covered either | `debate_open` also covers `invited`, is a precondition at `approval_confirm`, and applies on the policy path (debate.md §Quarantine interplay; 42 §3.1a) |
| M6 | Decision size | `MaxDecision` = 524288 ignored the duplicated content: the final positions, `final_agreement`, `remaining_disagreement` and `affected_artifacts` repeat entry text, and escaping enlarges the topic and constraints. A maximal legitimate debate (about 740 KB) would fail derivation, which the spec calls "a bug", so the close would fail | The bound is recomputed and `MaxDecision` = 786432, with a worst-case derivation test (decision.md §Size; 42 §3.3a) |
| M7 | IPC size | `internal/ipc/ipc.go` encodes results with `json.Marshal`, which writes `<`, `>`, `&` as six-byte escapes. A 448 KiB transcript of `<`-heavy text (for example code, or a peer doing it on purpose) becomes 2.7 MB, over the 1 MiB line, so `debate_show`, `wait` and `decision_show` break (a denial of service against the debate and its record). U+2028/U+2029 double too. Phase 2 `request_show` of `<`-heavy question context has the same issue | The IPC server encodes with `SetEscapeHTML(false)` (ipc.md, 42 §3.1b). Debate text refuses U+2028/U+2029 and C1. The view's size is then its canonical size, and a test covers the maximal case (debate.md §Messages, §IPC) |
| M8 | Audit verify | The store has one connection (`SetMaxOpenConns(1)`, `internal/store/store.go`). A streaming `audit_verify` over 10⁶ rows, with a 120 s timeout, would hold it and stall mail, sweeps and every IPC call | Verify reads the head, then walks pages of 2000 rows, each its own short query; a test checks that other work proceeds during a verify (audit.md §Verification; 42 §3.6a) |
| M9 | Migrations / second writer | `agentnetd install` starts the daemon and then calls `store.Open` (`cmd/agentnetd/install.go` `recordAudit`), which runs migrations. `store.migrate` reads the version **outside** the per-migration transaction, so after an upgrade both processes can apply migration 18. The loser fails with "duplicate column", and if the loser is the daemon it exits at start | `store.apply` runs each migration under `BEGIN IMMEDIATE`, re-reads the version inside, and skips it if already applied. There is a two-opener test (audit.md §Appending; 42 migration table, §3.6a) |
| M10 | Migration 19 | `INSERT INTO requests_new SELECT * FROM requests` is positional. Migration 12 appended `result` as the last physical column, so a rebuilt table declared in schema order would shift data silently (the `json_valid` checks might not catch it) | Explicit column lists on both sides for `requests` and `approvals`. The rebuild test compares every column of every row and the indexes (debate.md §Persistence; 42 migration table) |

## Low

Fixed in the docs:

- **L1** "No control characters" meant C0 and DEL only. C1 (U+0080–U+009F, which includes
  the 8-bit CSI U+009B) was allowed in every debate string that reaches terminals. Now refused
  in debate text (debate.md §Messages).
- **L2** The experience record's `acceptance.decision.signed_by` on A was always "initiator"
  (A writes at `closing`, before B signs, and never updates). Removed (experience.md).
- **L3** B's `broken` state left B's work-session mirror, request and experience record
  unspecified. They now all close in the `broken` transaction (debate.md §Commit–reveal,
  experience.md).
- **L4** "Silence … or a Phase 2 daemon": a Phase 2 daemon can never accept a debate, so it
  cannot be silent at close (decision.md).
- **L5** Context `sha256` was over "the bytes after CRLF → LF (as sent)", which is ambiguous.
  It is now exactly the `text` member of the stored canonical request (decision.md rule 2).
- **L6** 3.H's acceptance runs `log --verify` but 3.H did not depend on 3.6b (42).
- **L7** The 3.H cost said "about 6 invocations" and "1–2 USD per Claude round". A 2-round
  debate takes 6–8 invocations including A's start, and the run has two debates, so about
  1.5–3.5 USD of Claude per full 3.H run (42 §3.H, OD-P3-9).
- **L8** Models per D26/D28: 3.4 (approval gate, parses peer input) → Opus; 3.7 (a privacy
  invariant in every closing transaction, not routine) → normal Sonnet instead of Lite; 3.3b
  review from "light" to full (Markdown inertness is its security property) (42).
- **L9** audit.md said `Append` "uses `BEGIN IMMEDIATE`" and "retries once after 100 ms".
  database/sql's `BeginTx` issues a deferred `BEGIN`, and the DSN's `busy_timeout(5000)`
  already waits. Now: a dedicated `sql.Conn` with an explicit `BEGIN IMMEDIATE`, no extra
  retry, and the `SQLITE_BUSY_SNAPSHOT` case of `AppendTx` stated (audit.md).
- **L10** A constraint's `at` was undefined. It is now the approval time, stored exactly as
  sent, so both sides order constraints identically (debate.md).
- **L11** An idempotent `debate` retry with the same key and a different position would
  have returned the first request, whose committed position the agent no longer has in mind.
  `params_hash` now covers position, rounds and timeout (debate.md §Request type).
- **L12** Verify did not check derivation invariants (outcome vs answer, `final_agreement`,
  duplicates, final positions); step 5 does now (decision.md; overlaps H1).
- **L16** Peer-authored "human decisions" cannot be checked by the other side; the Markdown
  now labels each with the machine that approved it (debate.md, decision.md).

Open (backlog or implementer's choice; none blocks approval):

- **L13** Reveal nonce format and a non-canonical position in the reveal: now stated as
  `broken`, but the "held early entries" path on B could keep up to 12 `early` rows from a
  misbehaving A indefinitely; bounded (≤ 14 × 32 KiB), so no fix.
- **L14** 3.1a is sized **L** "at the one-day limit" but carries the request type, two table
  rebuilds, commit–reveal, the turn engine and mirror, quarantine edges and the M3/M4/M5
  additions. The Orchestrator may split it (schema + request type + commitment/reveal, then
  the turn engine and mirror) without changing migration 19.
- **L15** 3.2 is reviewed "with 3.1a", so it cannot merge before 3.1a's review, while 3.1a
  depends on 3.2. Build 3.1a on the 3.2 branch and merge both after the joint review.
- **L17** 3.6b ∥ 3.1a both edit `internal/capability` (review-28 L9 and `debate_open`): a
  small merge conflict to expect.
- **L18** `agentnetd install`'s audit row after starting the daemon can make a concurrent
  daemon `AppendTx` fail with `SQLITE_BUSY_SNAPSHOT` (stated in audit.md). Moving the
  install audit before the service start would remove the window; it is rare.
- **L19** The `debates.reason` CHECK has no value for the M4 early-complete close; it uses
  `cancelled`. If the owner wants the cause visible, add `early_complete` in 3.1a.

## OD list: completeness and soundness

| OD | Assessment |
|---|---|
| OD-P3-1 one-sided commit | **Confirmed**, with the analysis added to its row: binding, hiding, replay and "what B learns early" all hold; placeholder-then-revise is equally possible in (b) |
| OD-P3-2 type `debate` | Sound. The rebuild is safe once M10 is applied (no FKs or triggers reference `requests`) |
| OD-P3-3 approval-gated constraints | **Amended** (H3, L16): visible-only text; the gate protects the local side, not against a modified peer |
| OD-P3-4 no grants, edge checks | Sound with M5 (`invited`, approval precondition, policy path) |
| OD-P3-5 daemon signs | **Amended** (H1): a single-signed Decision is unconfirmed |
| OD-P3-6 manual anchors | Sound. (b) would also leak the peer's row count, as stated |
| OD-P3-7 turn parameters | Sound |
| OD-P3-8 experience (a) | Sound for Phase 3. Note that `debates`, `decisions` and `experience_records` all keep content indefinitely; decide (c) for all content tables together before the beta |
| OD-P3-9 turn-driven 3.H | Sound; cost corrected (L7) |
| OD-P3-10 two files | Sound |
| OD-P3-11, -12 | Sound (defer) |
| OD-P3-13 `secure_delete` | Sound. It narrows but does not close the gap (WAL frames until checkpoint, file-system copies), as stated |
| **OD-P3-14** (new) | Exporting a single-signed Decision: (a) export with banner and exit 6; (b) also refuse `--md --out` without `--unconfirmed`; (c) no export. Recommendation (a) |

Missing and not needed: an OD for the IPC encoder change (M7) is not needed, because the
change is invisible to JSON clients.

## Implementability

After these fixes each ticket can be built from the docs alone. The implementer still
chooses two small things, both named in the docs: the exact `\u{XXXX}` escape form in
`decision.Visible` (it must add no backtick), and the page size of the verify walk (2000
suggested). The migration order 18 → 21 matches the dependency graph: 3.6a merges before 3.1a,
3.3a before 3.7, and no ∥ pair adds migrations that could merge out of order.

## Files changed by this review

- `Docs/protocol/debate.md`
- `Docs/protocol/decision.md`
- `Docs/protocol/audit.md`
- `Docs/protocol/experience.md`
- `Docs/protocol/ipc.md`
- `Docs/review/42-phase3-tickets.md`
- `Docs/review/43-phase3-spec-review.md` (new)
