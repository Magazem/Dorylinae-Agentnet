# Orchestrator handoff

Read this first if you are a fresh Orchestrator instance. It is the single source of truth
for *where we are*. Update it after every merge and owner decision. The full per-wave
record up to the end of Phase 1 is archived in `Docs/orchestration/history.md`.

Last updated: 2026-09-23, Phase 2 wave P2-1.

## 0. Status and next steps

**Phase 0 and Phase 1 are code-complete, merged and pushed; CI is green on all 15 jobs.**
The repository is **public** (github.com/Magazem/Dorylinae-Agentnet), so GitHub Actions is
free. Phase 2 work is in flight: see the wave table below for workers, worktrees and branches.

**Open items (all owner-side, none blocks Phase 2 spec work):**
1. **Owner's two-machine run.** The top of `tests/phase1-manual.md` lists the only checks
   that need two real machines (reboot survival, cross-OS, a relay on a real network, idle
   detection, a visible desktop toast). Everything else is automated in
   `tests/phase1-smoke.ps1` (~65 steps, ~8 s, one machine, no admin). The Phase 0 two-machine
   run (`tests/phase0-manual.md`) is also still owed. The owner has one PC without admin
   rights at work; they will run it at home when they have time.
2. **Tags.** `phase-0` exists. A `phase-1` tag (and possibly `phase-0.1`) needs the owner's
   explicit OK, ideally after item 1.
3. **Codex CLI round of 1.H (optional).** 1.H passed with Claude Code + `agy` (Antigravity CLI)
   in both swapped rounds. Codex was blocked by the owner's account usage limit until
   2026-10-02; rerunning it then is optional.

**Phase 2 started 2026-09-23.** Step 1 done: specs committed on branch `p2/specs` (worktree
`AgentNet-wt/p2-specs`, commit d06c6d7): `Docs/protocol/{work-session,approval,grant,consult,device}.md`
+ `Docs/review/23-phase2-tickets.md` (15 tickets, migrations 14–17, OD-P2-1..15). Step 2 done:
`Docs/review/24-phase2-spec-review.md` (0 C, 3 H fixed, 11 M (10 fixed, M10 → OD-P2-6), 17 L),
commit faefa85. Step 3 done: owner approved all ODs (D16); OD-P2-6 (c) applied (4f28a2b);
specs merged to main. Self-hosted relays: D17.

**Wave P2-1 in progress** (Sonnet workers; each needs an Opus security review before merge):
| Ticket | Worker slot | Task | Worktree / branch | Migration |
|---|---|---|---|---|
| 2.1a work sessions | `01a0cd5e-eafe…` P2-WSCore | `01a0cd5f-0c95…` | `ws-core` / `p2/ws-core` | 14 |
| 2.2a approval | `01a0cd5e-ec75…` P2-Approval | `01a0cd5f-2990…` | `approval` / `p2/approval` | 15 (NO-OP placeholder 14 — replace at merge; merge after 2.1a) |
**2.2b merged** (0e1b64a + review 25 fixes 6b94bb1). Notes for **2.2c** (from review 25): store/compare `canonical(token)` never raw bytes; never audit `err.Error()` from capability (use `ReasonOf`); add a helper returning the canonical token; review-25 L9 (grant.md §Paths: CONIN$/CONOUT$, COM/LPT with superscript digits, C1/bidi chars) goes into the 2.3a fs ticket.
Next after these merge: 2.1b, 2.2c (then 2.D1 after 2.2c).

**Phase 2** (plan: grants, consult, sessions; see the build plan) plus the own-device
helper (D13). Follow the same flow as Phase 1:
1. An Opus worker writes the Phase 2 specs **first** (docs only): grants/capabilities (plan says
   Biscuit), consult, sessions, D13 device link + helper scope, and a ticket plan like
   `Docs/review/11-phase1-tickets.md` with pre-assigned migrations (next free: **14**) and
   review markers.
2. An Opus adversarial spec review, fixing in place.
3. **Owner approval** of the specs and any open decisions (OD list) before any code.
4. Then implementation tickets in dependency order, Opus security review on the sensitive ones.

**Backlog (not blocking):** review Lows in `Docs/review/07`–`22`; 05-review M1 (direct path
at-most-once); **M2 (relay abuse limits + TLS) is a beta gate (D17)**; relay pairing limits L1/L5 (08b)
before hosted relay; a dedicated `stale` ack status instead of `unsupported`; `status` cannot
split delivered vs failed counts; accept/decline/defer `--from` takes a raw key only;
team-invite table prune and `team_delete` not cancelling pending invites (18).

## 1. How this team runs

- **Roster.** Only the Orchestrator is standing. No Butler/Manager/Mailbox exists: **the user
  is the Manager** and makes every decision; talk to them directly.
- **Workers** (spawn yourself, one ticket each, retire after merge):
  | Template | assistant_id | Use for |
  |---|---|---|
  | Worker-Haiku | `custom-1789380226358-958d` | read-only summaries, mechanical work |
  | Worker-Sonnet | `custom-1789380226495-f6d5` | well-specified implementation |
  | Worker-Opus | `custom-1789460333299-bbb1` | specs, security reviews, hard debugging |
  Owner has approved Opus for specs and security reviews.
- **Dispatch** with `team_task_create owner=<slot>`. New workers often say "ready, no task"
  before the task arrives: send one short `team_send_message` pointing at the task id. If a
  worker still does nothing (empty worktree), see §5 on stuck workers.
- **Sequencing:** never tell a worker to wait for another. Dispatch when prerequisites merge.
  Building on an unmerged branch is fine; rebase later with `--onto` (§5).

## 2. Hard rules (do not break)

1. **Workers cannot run git** (their rules forbid it; read-only git for scans is the only
   exception). The Orchestrator commits, rebases, merges and pushes. Every task says
   "No git commands; list every file changed."
2. **One worktree per worker** under `C:\Users\ysuliman\Documents\AgentNet-wt\<name>`, branch
   `pN/<name>`. Workers stay inside their worktree.
3. **Specs before code.** Opus adversarial review, then explicit owner approval.
4. **Crypto/security tickets get an Opus security review before merge.**
5. **Merge gate:** worker reports + you run in the worktree: `go build ./...`, `go vet ./...`,
   `go test ./... -count=1` **×2**, `go run ./tools/verifyvectors`, and
   `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run ./...` (only
   CRLF gofmt noise allowed). Cross-vet for Unix when touching OS-specific code:
   `GOOS=linux GOARCH=amd64 go vet ./...`, `GOOS=darwin GOARCH=arm64 go vet ./...`.
6. **Safe commit sequence** (1.0f work was once lost): stage null-safely
   (`git diff --ignore-cr-at-eol --name-only -z | xargs -0 -r git add --` plus untracked via
   `git ls-files --others --exclude-standard -z | xargs -0 -r git add --`), commit, verify
   `git diff --ignore-cr-at-eol --name-only` and untracked are empty, **only then**
   `git checkout -- .` (EOL noise), rebase onto main, `git merge --ff-only`, remove the worktree.
   Chain with `&&` / `set -e`, never `;`.
7. **Authorized:** commit, merge and push `main`. **Not authorized:** tags, releases,
   force-push, repo settings. Ask first.
8. Commit messages end with the Co-Authored-By line given in the current system reminder
   (currently `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`).
9. **Owner decisions (§3) are final.** Reopen only for a concrete security/correctness flaw,
   and say so.
10. **Parking:** when the owner says park, do NOT send an abrupt STOP. Ask workers to finish
    the current step, make sure the tree builds, write WIP notes and report; then
    snapshot-commit their worktree and update §0.
11. **Watch CI after every push** (`gh run list --branch main --limit 3`; for failures
    `gh run view <id> --log-failed`). CI stayed red unnoticed for ~60 pushes once. Batch
    pushes (one per merged ticket, not per note).
12. **No admin on the owner's work PC.** Never run anything that installs services or needs
    elevation there (e.g. `agy --sandbox` raises a UAC prompt).
13. File bugs/to-dos/decisions on the Sticky Board (`sticky-board` skill); close them when done.

## 3. Owner decisions (final)

| # | Decision |
|---|---|
| D1 | Licence **PolyForm Shield 1.0.0** (public source for review, no competing products). |
| D2 | Durability via **sealed mail** (HPKE to rotating identity-signed mailbox key + inner Ed25519 sig), **sender outbox** until app ack, **receiver dedupe**. Noise XX only for ping/interactive. Persisted Noise state and IK/KK rejected. |
| D3 | Pairing **v2 code-bound MAC**: 15-char code (5 lookup + 10 secret), Argon2id, transcript over canonical cards + mailbox keys. PAKE deferred. Fingerprints + `peers verify/remove`. |
| D4 | 15-char code accepted now. |
| D5 | Refuse requests from `trust=relay` peers on a hosted (non-loopback) relay. |
| D6 | Non-repudiation accepted (signed mail). |
| D7 | Relay telemetry counts only `mail`; per-kind counts daemon-side. |
| D8 | Spec-writer choices for pairing/mail (canonical transcript, lookup lifetime, 60 s confirm timeout, key life 7+7 d / delete at 21 d, unknown kinds acked `unsupported`, outbox keeps signed plaintext). |
| D9 | Redeemer sends tag_R first; issuer counts attempts at `pair_peer` (max 3); used-code record persisted in SQLite. |
| D10 | Outbox `expired` = "delivery unknown"; receivers reject app mail older than 14 d; resubmits idempotent. |
| D11 | Phase 1 specs approved with OD-P1-1..13 as recommended **except** OD-P1-11 → `request.cancel` (from delivered/deferred, refused after accept). Single owner (OD-P1-1) and lost-owner-device (OD-P1-10) are documented limitations (`Docs/beta/known-limitations.md`). |
| D12 | Cancel gives **no** urgency-budget refund. |
| D13 | **Own-device helper (Phase 2):** separate `device` trust never created by team/roster/pairing; dedicated link flow confirmed on BOTH devices; only the obeying device can make itself a helper and holds the scope locally (request types, repos/paths, commands, expiry); off by default, audited, revocable from either side; out-of-scope requests go to the normal inbox; one-way hierarchy. |
| D14 | Optional size-capped **result payload on `request.complete`** (status, summary, exit_code, output ≤32 KiB, artifacts). Implemented in 1.6a. |
| D16 | **Phase 2 specs approved (2026-09-23)**, OD-P2-1..15 as recommended in `Docs/review/23-phase2-tickets.md`: in-house signed token (not Biscuit); desktop approval code with the stated boundary (agents with a raw shell need harness confinement; OS user-presence = Phase 3/4 hardening); DB grants deferred; requester-only grants; **OD-P2-6 = (c)** (from `quarantined`: discard, or request changes without release, result deleted unseen); every accept opens a work session; weekly CI with a scripted stand-in agent, real agents manual before releases. |
| D17 | **Self-hosted relays in the beta:** the relay stays untrusted whoever runs it; D5 "non-loopback" includes self-hosted relays; fetch limits are daemon-side caps (grant.md). Review-05 **M2 (relay TLS + abuse limits) is a GATE BEFORE THE BETA**, not a Phase 2 ticket. |
| D15 | Consumer onboarding = plan item **4.9** (install → one `agentnet setup` → one connect command; agent-runnable; clean machine on 3 OSes; no config files). Don't rush it. |

Still open (not urgent): relay hosting (Fly.io vs Hetzner) and account binding (4.1/4.2);
`Docs/` vs `docs/` casing; Windows code-signing timing (apply ~4–6 weeks before 4.4; an Apple
developer account is NOT needed for curl/Homebrew installs).

## 4. Phase 1 summary (what exists)

Specs: `Docs/protocol/{pairing,mail,team,presence,request,notify,ipc,envelope}.md`; tickets and
ODs: `Docs/review/11-phase1-tickets.md`; reviews 12–22 in `Docs/review/`.

| Area | Tickets | Notes |
|---|---|---|
| Mail stack | 1.0a–f | sealed mail, mailbox keys + rotation, dedupe/ack, outbox, 14-day limit |
| Pairing v2 | 0.8a–e | code-bound MAC; independent vector checker `make verify-vectors` |
| Teams | 1.1a–d | trust `team` + introductions, roster epochs, invite/join via pairing |
| Presence | 1.2a–d, 1.3 | ephemeral relay type, sealed fixed-size heartbeats, idle detection, visibility modes |
| Requests + inbox | 1.4a–c, 1.6a–b | schema + caps, submit/offline queued, lifecycle, cancel, result payload |
| Urgency | 1.7 | 5 high / 2 blocking per sender per rolling 7 d, weighted priority |
| Notifications | 1.8a–b | own per-OS desktop notifier (no beeep), signed webhook with dial-time SSRF checks |
| E2E + audit | 1.9 | offline lifecycle test, audit contains no content |
| Agents | 1.H | `tests/harness/phase1-agents.ps1/.sh`; Claude Code ↔ agy pass both rounds; snippet `Docs/agents/snippet.md` |
| Smoke | — | `tests/phase1-smoke.ps1/.sh` (one machine, no admin) |

Migrations on main: **1–13** (12 = requests_result, 13 = webhook_queue). Every new migration
must also add its tables to the DROP lists in BOTH rewind tests in `internal/store/store_test.go`.

## 5. Lessons (environment and process gotchas)

- **CRLF:** `core.autocrlf=true`; worktrees show many "modified" files with no content change.
  Use `git diff --ignore-cr-at-eol`. Local gofmt/lint noise is CRLF-only; check with
  `tr -d '\r' < f | gofmt -l`. `gofmt -w` rewrites line endings: only on changed files.
- **Go** at `%USERPROFILE%\tools\go\bin`; in Bash `export PATH="$USERPROFILE/tools/go/bin:$PATH"`.
  No cgo: `-race` runs only in CI. `python` fails (uv trampoline); use node or the Edit tool.
- **Windows vs Unix:** local tests pass on Windows (named pipes) while Unix sockets fail (e.g. a
  missing socket dir). Only CI catches it: watch it.
- **Windows PowerShell 5.1** lacks .NET Core APIs (`Process.Kill($true)`, `ArgumentList`); use
  `taskkill /T /F`. `$Home` is reserved. Wrap `Where-Object` results in `@()` before `.Count`.
- **Headless agents on Windows:** Claude Code's shell tool is **PowerShell**, not Bash; isolate
  connectors (`--strict-mcp-config`, empty `--mcp-config`, `--setting-sources project`).
- **Test flakiness patterns:** never read async state/audit once; poll with a deadline. Tests
  that write files use `internal/testutil.TempDir(t)` (Windows AV holds deleted files).
- **CLI:** base64url keys can start with `-`; `parseInterspersed` handles it (don't regress).
- **Workers:**
  - Paused after "could not process queued message after 3 delivery attempts": use
    `team_interrupt_agent` to resume from the files in its worktree; don't respawn.
  - Stuck (idle, empty worktree): interrupt once; if still idle, retire it, **wait for
    "Teammate X was removed"**, delete its task, then spawn a fresh worker with a NEW task in
    a NEW worktree (the board is visible to all workers; a "stopped" worker once picked up
    its successor's task).
  - Mark a worker's tasks completed before retiring it, or it declines shutdown.
  - Spec/docs workers read for a long time and write files late. `idle` notifications
    while the task is `in_progress` are not proof of being stuck: the P2 spec writer was
    retired as "stuck" yet delivered everything. Wait for its report (or a long silence)
    before retiring it.
  - Workers report a vanished worktree after you merge: expected.
  - Worker summaries are leads, not truth: verify key claims before telling the owner.
- **Branch on an unmerged branch:** after the base merges with review fixes, rebase with
  `git rebase --onto main <old-base-commit> <branch>`.
- **Migrations across parallel branches:** pre-assign numbers; a branch built early uses
  clearly marked NO-OP placeholders that you replace at merge (never merge a placeholder).
- **Merge conflicts** so far were always "both sides added independent code": keep both.
- **CI cost:** the full matrix on every push cost ~$18 in a day while the repo was private.
  It's free now (public). If it ever goes private again, apply a CI diet (paths-ignore docs,
  concurrency cancel, Linux-only per push, full matrix nightly/pre-tag).

## 6. Document index

| Path | What |
|---|---|
| `Docs/AgentNet Free Tier Build Plan.md` | The plan (phases 0–4, gates, 4.9 simple setup) |
| `Docs/protocol/*.md`, `Docs/cli/*.md` | Authoritative specs and per-command docs |
| `Docs/review/05-expert-review.md` | Phase 0 review (defects, backlog M1/M2) |
| `Docs/review/06-pairing-session-options.md` | Pairing/session design and Wave 1–4 tickets |
| `Docs/review/11-phase1-tickets.md` | Phase 1 ticket plan and OD-P1 decisions |
| `Docs/review/07`–`22` | Spec and code reviews (open Lows live here) |
| `Docs/agents/snippet.md` | CLAUDE.md / AGENTS.md snippet for agents |
| `Docs/beta/known-limitations.md` | Beta limitations (single owner, lost owner device) |
| `tests/phase0-manual.md`, `tests/phase1-manual.md` | Owner's manual checklists (two-machine parts at the top) |
| `tests/phase1-smoke.ps1`, `tests/harness/` | One-machine smoke; headless agent harness |
| `Docs/orchestration/history.md` | Archived full handoff (per-wave detail, commit ids) |
