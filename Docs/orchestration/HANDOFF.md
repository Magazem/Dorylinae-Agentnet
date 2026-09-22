# Orchestrator handoff

Read this first if you are a fresh Orchestrator instance taking over this project.
It is the single source of truth for *where we are*. Update it after every merge,
every owner decision, and before you expect a context reset.

Last updated: 2026-09-21, Phase 1 wave A in flight.

## 1. How this team runs

- **Roster reality.** The AionUi team has only the Orchestrator (you) as a standing
  member. There is no Butler, Manager or Mailbox on the roster. **The user acts as
  Manager**: they make every decision, and you talk to them directly.
- **Workers.** You spawn them yourself with `team_spawn_agent` from these templates,
  one ticket per worker, and retire each (`team_shutdown_agent`) once its work is
  merged:
  | Template | assistant_id | Use for |
  |---|---|---|
  | Worker-Haiku | `custom-1789380226358-958d` | read-only summaries, mechanical work |
  | Worker-Sonnet | `custom-1789380226495-f6d5` | well-specified implementation |
  | Worker-Opus | `custom-1789460333299-bbb1` | specs, security review, design research |
  The owner has approved Opus for specs and security reviews (about 4 per wave).
- **Dispatch.** `team_task_create` with `owner=<slot_id>` wakes the worker, so no
  separate message is needed. A new worker's first "ready, standing by" message was
  sent before the task reached it; ignore it.
- **Sequencing.** Never dispatch dependent work with a "wait until X" instruction.
  Dispatch the next wave only after the previous one is merged.

## 2. Hard rules (owner-set or learned; do not break)

1. **Workers cannot run git.** Their rules forbid it. The Orchestrator commits, rebases
   and merges. Every task says: "No git commands; leave changes uncommitted; list
   every file changed."
2. **One git worktree per worker**, under `C:\Users\ysuliman\Documents\AgentNet-wt\<name>`,
   on branch `wN/<name>`. A worker must work only in its own worktree.
3. **Specs before code.** Protocol specs need an Opus adversarial review, then the
   **owner's explicit approval**, before any implementation starts.
4. **Crypto and security tickets need an Opus security review before they merge**
   (0.8c, 0.8d, 1.0c, and 1.0e's crypto paths).
5. **Merge gate:** `go build ./...`, `go vet ./...` and `go test ./... -count=1` pass
   in the worktree, the acceptance tests exist as automated tests, and any required
   review is clean. Then commit on the branch, rebase onto `main`, and `git merge --ff-only`.
6. **Owner-authorized:** committing and merging to `main` after each wave passes review, **and pushing `main` to origin after merges** (authorized 2026-09-21).
   **Not authorized: tags, releases, force-push.** Ask first.
7. Every commit message ends with `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`.
8. **Owner decisions below are final.** Reopen one only for a concrete security or
   correctness flaw, and flag it to the owner explicitly.
9. File bugs, to-dos and decisions on the Sticky Board (`sticky-board` skill). Close
   items when they are fixed.
10. The user's context resets are expected. Keep this file current.
11. **Never discard a worktree until its commit is verified.** On 2026-09-21 the 1.0f work was LOST: `git add $(...)` split a path with spaces ("Docs/AgentNet Free Tier Build Plan.md"), the commit failed, and the next `;`-chained `git checkout -- .` plus `git worktree remove --force` destroyed it. Now: stage with `git add -A -- <dirs>` or quoted paths; chain the whole sequence with `&&` (never `;`); confirm `git log -1` shows the new commit and `git status` shows no real changes (`git diff --ignore-cr-at-eol --stat` empty) BEFORE any checkout, reset or worktree removal. Only discard EOL noise after that check.

## 3. Owner decisions (final)

| # | Decision | Where recorded |
|---|---|---|
| D1 | Licence: **PolyForm Shield 1.0.0**. Free to use, and the source is public for review; no competing products. | `LICENSE`, README |
| D2 | Session durability: **sealed mail** (HPKE to a rotating, identity-signed mailbox key, plus an inner Ed25519 signature), a **sender outbox** that holds each message until app-level ack, and **receiver dedupe**. Noise XX is kept for ping and interactive traffic only. Persisting Noise state was **rejected** (nonce reuse on rollback). IK/KK was **rejected**. | `Docs/review/06-...md` §B, §7 |
| D3 | Pairing: **v2 code-bound MAC**. 15-char code (5 lookup + 10 secret), Argon2id, transcript over canonical cards and mailbox keys. PAKE deferred to v3 (Go libraries unaudited). Fingerprints plus `peers verify/remove`. | `Docs/protocol/pairing.md` |
| D4 | Accept the 15-char code now. | 06 §7 |
| D5 | Refuse requests from `trust=relay` peers on a hosted relay, once a re-pair path exists. | 06 §7 |
| D6 | Accept non-repudiation (signed mail). | 06 §7 |
| D7 | Relay telemetry counts only `mail`; per-kind counts are kept daemon-side. | 06 §7 |
| D8 | The spec-writer's 5 choices: canonical-JSON transcript; lookup kept until `pair_cancel`, TTL or 3 redemptions; errors `pair_lookup_taken` and `pair_v1_disabled`; 60 s confirm timeout counts as a failure; no check char; 0.8c creates the first mailbox key; mail key life 7 d current + 7 d retired, deleted at t+21 d; unknown kinds acked `unsupported`; outbox keeps signed plaintext for re-seal. | pairing.md, mail.md |
| D9 | From the spec review: **the redeemer sends tag_R first**; the issuer counts attempts at `pair_peer` (max 3); the redeemer's 24 h used-code record is **persisted in SQLite** (`pair_used_codes`). | 06 §7, pairing.md |
| D10 | Review 10 M3: outbox `expired` means **"delivery unknown"**; receivers reject app mail older than 14 d; Phase 1 resubmits must be idempotent. Being implemented in 1.0f. | mail.md (after 1.0f) |
| D11 | Phase 1 specs APPROVED (2026-09-21) with OD-P1-1..13 as recommended EXCEPT **OD-P1-11 → (b): add `request.cancel`** — allowed from delivered and deferred, refused after accept, counts against nothing. OD-P1-1 (single owner) and OD-P1-10 (lost owner device kills the team) stay, but must be written up as **known limitations in the beta docs**. Additions: **ticket 1.H** (scripted headless run in Claude Code + one other harness: request → inbox → accept → complete, plus a minimal CLAUDE.md/AGENTS.md snippet; 1.P depends on it); **request body caps** (brief length, artifact count, title length) in request.md and covered by 1.4a table test; **request.cancel** in the 1.9 audit list and `TestAuditHasNoContent`; 1.7 test derives the priority vector from the request.md formula (no magic 2500). Process: 1.2a, 1.2d, 1.4b (and 1.1a) start now; **1.4a may be built in parallel with 1.1 but merges only in migration order**. Owner expects Phase 1 to take 6–8 weeks at ~10 h/week; the Opus review queue is the bottleneck. | 11-phase1-tickets.md, request.md |
| D12 | `request.cancel` does **not** refund urgency budget (prevents high+cancel resetting the weekly count). | request.md |

Still **open** (not urgent): relay hosting (Fly.io vs Hetzner) and account binding
(needed by 4.1/4.2); `Docs/` vs `docs/` casing; work rhythm (part-time vs full-time).

## 4. Where things are

### Done and on `main`
| Commit | What |
|---|---|
| `2078f1f` and earlier | Phase 0 as built by the original harness (tag `phase-0`) |
| `0785aa6` | Review docs 01–06, LICENSE, README licence line |
| `395ee48` | H2: relay `--queue-db` / `--queue-ttl`, persistent queue, restart test |
| `322acd9` | CI: linux/macos/windows matrix, `-race` job, identity test flake fixed |
| `a4fa53a`, `18cd191`, `b9dba62` | Specs 0.8a (pairing v2) and 1.0a (mail), review fixes, owner approvals |
| `51334e6` | This handoff file + `CLAUDE.md` pointer |
| `8dd7297` | 0.8e `tools/verifyvectors` (`make verify-vectors`), all vectors reproduce |

### Wave 2: done (2026-09-21)
0.8b `a8cb0a3`; 0.8d `099054e` + `6c78619`; 0.8e `8dd7297`; 1.0c `9e3ddf3` + review fixes `eb7d39e` (strict timestamps, fuzz). Reviews: `Docs/review/08-wave2-security-review.md`, `08b-relay-v2-review.md`.

### Wave 3: done (2026-09-21)
0.8c `96ae3ac` + `e530aa3`; 1.0d `6835a6c`; 1.0b `663eb76` (migration 6 mailbox_keys_own; rotation 7/7/21 d; `keys` kind; key-miss reply; mail receiver ON by default; peers.mailbox_keys keeps max 2 per spec). 1.0b and 1.0d were reviewed together with 1.0e in `Docs/review/10-mail-delivery-review.md`.

### Wave 4: done (2026-09-21)
1.0e `cdb9047` + `0052764`; M4 `bfee756`; Windows flakes `a01c877`; CI fixes `8dd7100` + `be50cc2` (**CI fully green** on all jobs from `be50cc2`); 1.0f (redo) `13b4a6b` (install --relay, Windows service log, `agentnet mail send` + debug `note` kind under `DORYLINAE_DEBUG=1`, D10 14-day receive limit, docs/plan reconciled). Everything is pushed to origin/main.

### Next
1. **Owner** runs `tests/phase0-manual.md` on two real machines and sends back the results table. Fix whatever it finds.
2. Then ask the owner whether to re-tag Phase 0 (e.g. `phase-0.1`). Tags need explicit OK.
3. **Phase 1 in flight** (specs merged `b6e824b`, D11). Ticket plan: `Docs/review/11-phase1-tickets.md`. Branch names `p1/<x>`, worktrees under `AgentNet-wt/`.
   | Work | Worker | Worktree | Review |
   |---|---|---|---|
   | ~~Spec amendment per D11~~ | merged `e973b45` (cancel + tombstone + `request.cancelled` confirm kind; caps 64 KiB total `request_too_large`; 1.H; known limitations). Owner confirmed (D12): cancel gives **no** urgency-budget refund. | — | — |
   | 1.1a peers trust team (migration 8) | committed `da9b4d3` on `p1/t1-1a`; in Opus review (R-1.1a → `Docs/review/14-1.1a-review.md`) | `t1-1a` | **Opus** |
   | 1.2a relay ephemeral envelopes | committed `0984bca` on `p1/t1-2a`; in Opus review (R-1.2a → `Docs/review/15-1.2a-review.md`) | `t1-2a` | **Opus** |
   | ~~1.2d internal/idle~~ | merged `4324c4b` | — | — |
   | ~~1.4b mail.ErrBadBody~~ | merged `704efdb` + review fix `33682ff` (review 13; `!bad_body` suffix confirmed safe). Rule for 1.1b/1.4c: return `ErrBadBody` only for failures a resend cannot fix. | — | — |
   Next after these: 1.1b (needs 1.1a + 1.4b merged, migration 9); 1.4a may be built once the amendment merges (merge after 1.2b, migration 11). Every implementation task includes the golangci-lint `go run` command in its acceptance.
4. Backlog: 05-review M1 (direct path at-most-once) and M2 (relay abuse limits + TLS, before 4.1); relay pairing limits L1/L5 (08b); Lows in reviews 07, 08, 08b, 09, 10; a dedicated `stale` ack status instead of `unsupported` (1.0f compromise); `status` cannot distinguish delivered vs failed counts.

To see live status: `team_members`, `team_task_list`, `git worktree list`.

Ticket definitions and acceptance tests: `Docs/review/06-pairing-session-options.md` §5.

## 5. Lessons learned (environment gotchas)

- **CRLF noise.** `core.autocrlf=true`. A new worktree often shows ~80 "modified" files
  with no content change. Use `git diff --ignore-cr-at-eol --stat` to see real changes.
  `git add` **only the files the worker listed**. Remove merged worktrees with
  `git worktree remove --force`.
- **Go** is at `%USERPROFILE%\tools\go\bin`, and is not on PATH by default in Bash.
  Use `export PATH="$USERPROFILE/tools/go/bin:$PATH"`.
- **No cgo or gcc locally**, so `-race` only runs in CI. First CI run of the matrix (run 35601167726, `e3488f0`) FAILED: lint (~27 findings), race (identity e2e test buffer), Windows harness audit-row timing. Fixed in `8dd7100` (pushed); CI re-run pending verification. Check CI with `gh run list --branch main` after every push.

- **Python via `python` fails** (uv trampoline error). Use the Edit tool or Go.
- **Paused workers.** On 2026-09-22 all five Phase 1 workers were paused by "could not process queued message after 3 delivery attempts" (runtime restart). Their partial work survived in the worktrees. Recovery: `team_interrupt_agent` each one with "resume task X from the files already in your worktree; do not start over". Never respawn or clear a worktree for this.
- **Lint before merge.** The installed golangci-lint cannot load the config, but `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run ./...` works. Put it in every implementation task's acceptance criteria and run it yourself before merging; gofmt complaints that only exist because of CRLF are noise (check with `tr -d '\r' < f | gofmt -l`). CI failed on lint three times in a row on 2026-09-21 because this was skipped.
  CRLF artefact, not a real issue.
- The `internal/service` test "hang" on the board was not reproducible; it was closed as stale.
- Windows TempDir-cleanup flakes (AV holds deleted files) are fixed by `internal/testutil.TempDir` (`a01c877`). New tests must use it.
- `gofmt -w` converts CRLF to LF across a whole worktree. Tell workers to run gofmt only on files they changed.
- Merge order matters: rebase each remaining branch onto `main` after every merge.
- Haiku summarizers were good enough for fact-gathering. Spot-check line counts and
  headings before trusting them (one reported "2000 lines" and wrote 464).
- Workers' summaries are leads, not truth: verify key claims in code before reporting
  them to the owner.

## 6. Document index

| Path | What |
|---|---|
| `Docs/AgentNet Free Tier Build Plan.md` | Original plan (phases 0–4, gates) |
| `Docs/review/01-plan-checklist.md` | Plan as a per-phase checklist, doc contradictions |
| `Docs/review/02..04-*.md` | Code summaries (daemon, crypto, relay and build) |
| `Docs/review/05-expert-review.md` | Phase 0 review: ticket status, defects H1–H3, M1–M4, Lows |
| `Docs/review/06-pairing-session-options.md` | Pairing and session design, **ticket list §5**, owner decisions §7 |
| `Docs/review/07-spec-review.md` | Adversarial spec review findings |
| `Docs/protocol/pairing.md`, `mail.md` | Authoritative specs for Waves 2–4 |
| `Docs/orchestration/HANDOFF.md` | This file |
