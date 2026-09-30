# 81: R55-F13 security review

Reviewer: R55-F13sec-Opus · model claude-opus-5-5 · 2026-10-01 · worktree `AgentNet-wt/r55-f13`,
branch `p4/r55-f13`, ticket commit `a03bcfe` on `main` `840f5c4`. This is a report only. I added
two proof tests (listed at the end). The placeholder migration 23 was ignored as instructed.
Read against the spec `Docs/review/71-r55-f13-spec.md` (including Review 71b), D57, and
`Docs/review/80-r55-f14-security.md` (branch `p4/r55-f14`).

## Verdict

**PASS with one Medium finding. Nothing here blocks the merge on safety grounds.**
- Prune cannot remove unfinished items, `issued` grants of a live session, Decisions or audit
  rows.
- Prune cannot re-admit replays.
- No path removes data without a human-approved `data_prune` id.
- The `limit` refusal comes only after the mail is fully authenticated.

M1 is a design gap in the introducer caps: an honest member can be refused because of other
members. I recommend fixing it or recording it as an owner decision before merge. The Low
findings are hardening.

## Checked (no finding)

**Prune scope** (`internal/retention/retention.go`).
- `finishedRequests` (:92-111) requires all of these:
  - a final state and `updated < cutoff`;
  - every same-role work session `closed` and older than the cutoff;
  - every same-role debate `closed` or `broken` and older than the cutoff.
- Dependents are collected with the same role mapping (:142-155), so only closed, old sessions
  and debates are taken.
- Orphan sessions and debates (:164-180) also require `closed` / `closed|broken` and age.
- Grants (:182-185) follow Review 71b F1:
  - a `held` row goes when it is past `exp` or `revoked_at`;
  - an `issued` row goes only when no `work_sessions` row has its session.
  - The session's own grants go only with a pruned (closed) session. A `closed` session never
    reopens (`worksession/mirror.go:151`, `phase1.go:39,61`, `cancel.go:43`), so quarantine
    clause 1 cannot be lifted.
- `updated` is always the local `now` in every `UPDATE` of `requests`, `work_sessions` and
  `debates` (grep). A peer cannot back-date an item into range.
- No statement touches `audit_events` or `decisions`. The Decision re-use case (71b F8) is
  refused at `request/receive.go:187-197`.
- **Scope drift after approval.** An item can enter the set only if `updated < cutoff`, and
  that cutoff is fixed in the past. So the batches after approval remove the set the human
  saw, apart from `mail_seen`-driven `mail_inbox` rows at the same instant.

**Dedupe and replay.**
- `PruneSeenTx` runs before the `mail_inbox` deletes in the same transaction (:433-440). An
  inbox row is selected only when no `mail_seen` row newer than 35 d exists (:234-237).
- `Open` bounds `created` both ways (`mail/open.go:126`: 30 d back, `MaxSkew` forward).
  `ReceiveMaxAge` is 14 d. So an authentic mail whose dedupe rows are gone is always refused
  as too old. `keys` mail has no inbox row, and `mail_seen` (35 d) outlives its 30 d bound.
- An `in` request resent after prune fails the 30-day bound. A tombstone lasts ≤ 31 d.

**Approval gate** (`internal/daemon/prune.go`).
- Removal happens only in `runPrune` → `pruneBatch`, and only for an in-memory `pruneAuth`
  with `approved` set.
- `approved` is set only by `Perform`'s after-commit hook. `Perform` runs only from
  `approval.Store.confirm`, and confirm is reachable only from the window answer or the
  daemon's terminal. IPC exposes only `approval_list`, `approval_open` and `approval_reject`
  (`daemon/approval.go:118-141`), so a local agent can create, poll and reject, but not
  approve.
- The cutoff and `older_than` are bound to the id (:255). A different `older_than_s` →
  `bad_request`.
- An id of another kind → `bad_request`.
- These paths all give `unknown_approval`: a restart, `more:false`, or the 1 h lapse (:273).
  A replayed old id therefore removes nothing.
- An expired or rejected approval runs `OnReject` → `drop`
  (`approval/store.go:614,624,778`).
- Rebuild re-counts at the same cutoff and instant. It is stable: new mail inbox rows are
  blank and newer than the cutoff.
- The only race: a confirm before `auths.set` (:233). That calls `approve("")`, a no-op, so it
  fails closed.
- The summary is a fixed template of numbers (`approvaltext.BuildPrune`).
- **Audit.** `data.prune` holds counts plus `older_than_s`, `cutoff` and the approval id,
  written in the batch transaction (:299-315). There are no row ids and no content.

**F14 condition (`limit` only after step 7).**
- `ErrLimit` is produced only inside a kind's `Apply`: `request/receive.go:471-513` and
  `daemon/grant_kinds.go:93-105`.
- `Apply` runs in `Receiver.store` after `Opener.Open` has finished steps 1-10, and after the
  14 d age check (`mail/receiver.go:145-178`).
- The reject is reported as step 11 (`reject(11, reason, nil)`), so F14's `auditable` accepts
  it.
- A relay replay hits the marked `mail_seen` row → `seenDupBad`, which is not reported
  (:181-187). So there is one `mail.reject {limit}` per genuine peer-signed mail, as D58
  requires. See L2 for the one relay-timing aspect.

**Other items.**
- **Blank `mail_inbox`.** The only production read is the dedupe `COUNT(*)`
  (`mail/receiver.go:310`), confirmed by grep. Nothing re-reads `signed`.
- **Card size.**
  - `Verify` measures the received bytes before parsing (`agentcard.go:323`).
  - `New`/`Sign` measure the `MarshalIndent` form (71b F7).
  - `envelope.MaxCardBytes` now refers to the same constant.
  - Roster members are verified on their canonical value (`team/kinds.go:365`).
  - A stored oversize card fails `forwardCard` into F23's `errStoredCard` path.
  - `go run ./tools/verifyvectors`: P2 passes, N16 fails at 1, N17 fails at 5, all reproduced.
- **Migrations.**
  - Migration 24 adds indexes and a column only, with no rewrite.
  - Migration 25 copies every `approvals` column explicitly. It recreates `approvals_state`,
    and its CHECK equals F24's migration 23 plus `data_prune`.
  - Store tests, including the rewinds, pass.
- **Memory bounds.**
  - The mailbox key cache and its `failed` map are keyed only by own `mailbox_keys_own` rows
    and are cleared in `deleteKey`.
  - The identity key is one value, handed out as a copy.
  - The re-seal map holds at most the open outbox rows, dropped when final or after 1 h.
  - Fetch uses 25 buckets per grant, with `Flush` eviction.
  - Both queues charge the decoded payload before enqueueing and refund on dequeue or drop.
- **Held caps** apply after the duplicate and conflict checks. They affect only the sending
  grantor's own grants in that session.
- **`grant_list`.** The cursor is validated. Page bytes are bounded; only the first view is
  always taken, and it is local data.

## Findings

### M1 (Medium): one owner's introduced members share the introducer caps, so two members can shut out the rest

- **Where:** `internal/request/receive.go:471-513` (`checkDailyCaps`, `openCapReached`),
  constants at `internal/request/request.go:23-24`, and `Docs/protocol/request.md` §Per-peer
  caps (OD-F13-16).
- **Scenario.** Honest owner O introduces members H1, H2 and M. H1 and H2 are hostile or
  compromised, and each stays inside its own per-key caps.
  - **Open cap.** H1 and H2 each keep 100 requests open. M's **first** request is then
    auto-declined `inbox_full`.
  - **Daily cap.** H1 and H2 each send 200 requests a day, all auto-declined. Every request
    from M, and from every other O-introduced member, is then refused with `limit` for the
    whole 24 h window.
  - The recipient cannot clear the daily cap: it is time-based, and these rows are already
    declined.
- **Proof.** `internal/request/zz_sec81_introducer_test.go`
  (`TestSec81IntroducerCapStarvesHonestMember`) shows both results. It passes, which confirms
  the behaviour.
- **Why it matters.** "Honest peers wrongly refused": one paired peer's traffic (two keys, or
  one key plus normal team load) denies service to other, unrelated paired peers. The
  rationale in request.md ("room for an honest 32-member team") considers only honest load.
- **Fix direction** (any one of these):
  1. Count each key's contribution to the introducer total with a ceiling, for example
     `Σ min(count(key), 100/N)`. Or apply the introducer cap only to keys that are no longer in
     `peers` (the churned keys that 71b F2 is about), and let live keys be bounded by their
     per-key caps.
  2. Exclude content-free auto-declined rows (`body = '{}'`) from the **introducer** daily
     count, since those rows cost only metadata.
  3. At minimum, document the trade-off in request.md and OD-F13-16, and name `peers remove`
     of the offending member (which the recipient can find from `mail.reject {limit}` / the
     requests table) as the remedy.

### L1 (Low): the dry run is unbounded and also runs inside the confirm write transaction

- **Where:**
  - `internal/daemon/prune.go:168` (dry run), `:185` (approval creation), `:212-215` (Rebuild
    inside `approval.Store.confirm`'s transaction);
  - `internal/retention/retention.go:142-155` (two queries per request), `:199-207` (one per
    debate).
- **Scenario.** A database flooded before F13 (the case prune exists for) holds tens of
  thousands of finished rows.
  - Every `prune` counts with no bound.
  - `prune --yes` counts twice more: once at creation, and once in the Rebuild inside the
    confirm transaction, on the daemon's single connection (`SetMaxOpenConns(1)`), which
    blocks mail and IPC.
  - Measured: 40,000 finished requests with no dependents → **0.95 s** per count
    (`internal/retention/zz_sec81_dryrun_test.go`). With sessions and debates it is several
    times that.
  - Any local process can repeat the dry run. This is not a new class of problem (local IPC
    is trusted for availability), but it breaks retention.md's claim that the byte bounds
    keep the single connection free.
- **Fix direction:** in the unbounded count, replace the per-request `collectIDs` loops with
  set-based `COUNT`s (join on `(peer, request_id, role)`). Or let the Rebuild compare a
  cheaper fingerprint (for example `COUNT` and `MAX(rowid)` per table in range). Or state the
  cost in retention.md §IPC.

### L2 (Low): the daily cap uses receipt time, so a relay can bunch honest mail into a `limit` refusal

- **Where:** `internal/request/receive.go:471-490` (`received_at >= now − 24 h`).
- **Scenario.** An honest peer sends 15 requests a day. The relay holds them for 13 days
  (under the 14 d receive age) and then delivers all ~200 at once. Every request past the
  200th in that burst is refused with `limit` (`rejected` ack, sender row `failed`).
  - The refusal is still emitted only for genuine peer-signed mail, once per mail, so the F14
    audit contract holds.
  - But the refusal is caused by relay timing, not by the peer's own pace.
  - A relay can already turn delay into a permanent refusal through the 14 d age limit, so
    the relay gains little.
- **Fix direction:** count the daily cap by the request's `created` (bounded by `MaxSkew`),
  or by `max(created, received_at − 24 h)`. Or document that a relay-delayed burst can reach
  the cap, with `request resend` as the remedy.

### L3 (Low): an approved prune that is never driven stays in memory until restart

- **Where:** `internal/daemon/prune.go:79-85`, `:273-276`.
- **Problem.** `until` is checked only when the id is called again. If the CLI dies after
  approval, the entry is never dropped. It is bounded by the approval creation limit
  (20 an hour, so about 480 small structs a day) and is harmless in effect: after 1 h the id
  gives `unknown_approval`.
- **Fix direction:** sweep lapsed entries in `set` / `approve`.

### I1 (Info): doc and comment drift

- `internal/mail/receiver.go:48` still says "Inbox stores a mail_inbox row with the verified
  plaintext as proof", and `:50-52` names only `ErrBadBody` as the exception. It should say
  the row is blank since R55-F13 and name `ErrLimit` too.
- retention.md §IPC says "The daemon computes `cutoff` from its own clock at each call". With
  D57 the cutoff is fixed when the approval is created (as §Approval says). The sentence
  should be updated.

### I2 (Info): approval confusion

A local agent can open its own `data_prune` approval next to the human's (for example 35 d
against the human's 365 d). The window shows each approval's cutoff and "Confirm only if you
ran agentnet prune yourself". This is the same property as every other approval kind; noted
only because D57 is meant to stop an agent from pruning on its own.

## Tests run

All run in this worktree with `GOTMPDIR` under `%TEMP%\f13sec`.
- `go test` passed for `internal/{retention,mail,request,debate,capability,agentcard,mailbox,session,store,audit,peers}`.
- `go test ./cmd/agentnet -run Prune`: passed.
- `go run ./tools/verifyvectors`: all vectors reproduced.
- `go test ./internal/daemon -run 'Prune|Cap|Limit|Retention|Grant|AuditInventory'`: one
  failure, **`TestAuditInventoryDevices`**, with `device_scope_set: bad_scope …
  writable_by_others: "C:\\" can be changed by Authenticated Users`. This is the host ACL on
  `C:\`, not F13: the test and the scope code are unchanged by `a03bcfe`.
  `TestAuditInventory`, including F13's new prune and blank-inbox section, passes.
- The proof tests `TestSec81IntroducerCapStarvesHonestMember` and `TestSec81DryRunCost`
  pass.

## Files created

- `Docs/review/81-r55-f13-security.md` (this file)
- `internal/request/zz_sec81_introducer_test.go` (M1 proof)
- `internal/retention/zz_sec81_dryrun_test.go` (L1 measurement)
- Temp only: `%TEMP%\f13sec\` (`GOTMPDIR`, `tests.log`)
