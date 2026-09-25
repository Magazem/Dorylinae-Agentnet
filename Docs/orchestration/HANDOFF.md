# Orchestrator handoff

Read this first if you are a fresh Orchestrator instance. It is the single source of truth
for *where we are*. Update it after every merge and owner decision. The full per-wave
record up to the end of Phase 1 is archived in `Docs/orchestration/history.md`.

Last updated: 2026-09-23, Phase 2 wave P2-2.

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

**Wave P2-1 merged:** 2.2b (0e1b64a, 6b94bb1), 2.1a (01b50c0 + review 27 fixes 0313404), 2.2a (efc62fa + review 26 fixes 80724c0; placeholder 14 replaced).
**Wave P2-2 status:**
- 2.1b **merged** (f566c83 + D18 rework 543bc2e: `mail.Opened.Withhold` → the receiver stores signed='' at receipt). Not security-reviewed (not required by 23); **the 2.4 security review must also cover the D18/withhold path** in internal/mail/receiver.go and worksession receive/cancel.
- 2.2c **merged** (1ceb40e, 9503c6c review 28, bb46694 integration: approvals via window/Store.Confirm; the grant Perform returns an AfterCommitter that wakes the outbox). Review-28 leftovers: L8 (→ 2.4), L9 (grant.orphan audit lacks `grant`), L6/L7 notes.
- 2.5 **merged** (7a68d33 + review 32 fixes a4b1093).
- 2.3a **merged** (fetch server + fs; review 34: 0/0/3 fixed; open Lows L3–L6, L6 = COM0/LPT0 needs a vector change, owner call later).
- 2.4 **merged** (quarantine + L8; review 35: H1 stale release approval now bound to seq, H2 D18 gaps closed). Noted Lows: pre-2.4 revoked rows have NULL approval; SQLite secure_delete off/WAL keeps discarded bytes (→ backlog); session.quarantined text vs work-session.md.
- 2.D1 **merged** (device link, migration 17; review 36: M1 mail_submit could forge device.* kinds, fixed). Open from review 36: L4 (spec: offer freshness + ignore offers older than the last unlink), L5 (notify on activation: implement or amend the spec), L6 (link ids may differ per side, so 2.D2 must not key on it), L7 (mail_submit allowlist for all daemon-owned kinds), L8 (approval OnReject hook, needed by 2.D2).
- 2.3b **merged** (git serving; review 37: M1 exact branch ref via for-each-ref, M2 real git.exe on Windows). Open: L2 → D23 (enforce in 2.9); L3 include/alternates can read local paths (the grantor's own config).
- FIX-CI **merged** (review 39): stat/list/read refuse ANY symlink (final component too); in-flight slots are freed before the last response (a real bug for sequential holders). No security impact.
- 2.D2 helper runner (+ review-36 L7 mail_submit allowlist, L8 OnReject, D22): **Opus trial** P2-Opus-HelperRunner slot `01a0d2ee-b28c…`, task `01a0d2ee-ea1d…`, worktree `helper-runner`.
- 2.3c **merged** (fetch client + CLI; review 38: 0/0/4 fixed). Open: L3 fs `changed` can't detect same-size rewrites (protocol change → owner, Phase 3). 
- 2.D2 **merged** (helper runner + mail_submit allowlist + OnReject + D22; review 40: M1 bidi/zero-width in approval text, M2 Linux Pdeathsig (macOS gap documented)). Runner output control chars become "?" (not U+FFFD). L1/L11 → D24 (2.D3).
**Final wave (P2-4):**
| Ticket | Model | Slot | Task | Worktree |
|---|---|---|---|---|
| 2.D3 (D24) | Opus | **merged** (review 41: link-hop walk, Linux POSIX ACLs, watch expiry). Open: L5 UNC/NFS paths trust the file server admins (owner call later); L4 macOS ACLs not read. | | |
| 2.9 | Sonnet | **merged** (whole-loop e2e, TestPhase2AuditHasNoContent, D23 git version check, status `git` field, docs fixes) | | |
| 2.N (D25) | Sonnet | **merged** (session.result on A, session.changes on B; content-free, after commit) | | |
| 2.H | Sonnet | **merged** (stand-in PASS; real rounds Claude Code↔agy PASS both ways, 157 s / 163 s; ~0.2–0.4 USD per Claude run). The .sh and Linux/macOS legs are first exercised by the weekly workflow `.github/workflows/phase2-harness.yml` (manually dispatched once at merge). | | |
Then 2.D3 security review, 2.P push (a `phase-2` tag only with the owner's OK).
- 2.2d **merged** (2dc1b3d, aeb1680 AfterCommitter, 1664499 review 30 fixes). Review-30 notes L6/L7/L10 on the Sticky Board.
- CI race failures after 2.2d: test-only races in approval/daemon fakes, fixed in 71db65d (review 31); store.go was fine.
- 2.5 consult: P2-Consult slot `01a0ce43-22be…`, task `01a0ce43-450c…`, worktree `consult`.
`tests/phase2-manual.md` exists (created by 2.2d; includes the 2.2a toast-history check).
Review-26 caller rules for 2.2c/2.4/2.D1 (N1–N5 in Docs/review/26): all state checks in Precondition (a Perform error leaves the approval pending); Perform writes only through tx; hooks return *ipc.Error; hooks run under Store.mu and must never call back into the Store; drop the pending row if Create fails; peer text can still show a decoy code. Review-26 L7 is resolved by D19 (2.2d).
**2.2b merged** (0e1b64a + review 25 fixes 6b94bb1). Notes for **2.2c** (from review 25): store/compare `canonical(token)` never raw bytes; never audit `err.Error()` from capability (use `ReasonOf`); add a helper returning the canonical token; review-25 L9 (grant.md §Paths: CONIN$/CONOUT$, COM/LPT with superscript digits, C1/bidi chars) goes into the 2.3a fs ticket.
**Wave P2-3 in progress** (Sonnet; each gets an Opus security review):
| Ticket | Slot | Task | Worktree | Migration |
|---|---|---|---|---|
Then: 2.3b (git serving), 2.3c (fetch client + e2e), 2.D2 (helper runner), 2.9, 2.H, 2.P.

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
before hosted relay; a dedicated `stale` ack status instead of `unsupported`; `accept/decline/defer `--from` takes a raw key only;
team-invite table prune and `team_delete` not cancelling pending invites (18).

## 1. How this team runs

- **Roster.** Only the Orchestrator is standing. No Butler/Manager/Mailbox exists: **the user
  is the Manager** and makes every decision; talk to them directly.
- **Workers** (spawn yourself, one ticket each, retire after merge):
  | Template | assistant_id | Use for |
  |---|---|---|
  | Worker-Haiku | `custom-1789380226358-958d` | read-only summaries, mechanical work |
  | Worker-Sonnet | `custom-1789380226495-f6d5` | well-specified implementation |
  | Worker-Sonnet-Lite | `custom-1790318917893-ab35` | trial (D27): Sonnet, thinking off, routine well-scoped tickets |
  | Worker-Opus | `custom-1789460333299-bbb1` | specs, security reviews, investigations/hard debugging, security-critical or OS-level code (D26) |
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
   `GOOS=linux GOARCH=amd64 go vet ./...`, `GOOS=darwin GOARCH=arm64 go vet ./...`. **Also lint per OS:** install the linter once
   (`GOBIN="$TEMP/gl" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2`), then run
   `GOOS=linux "$TEMP/gl/golangci-lint.exe" run ./...` and the same with `GOOS=darwin`. Windows lint
   skips `_linux.go`/`_darwin.go`, and CI lints on Linux. `GOOS=linux go run …golangci-lint…` does
   NOT work: it builds a Linux binary that can't run here, and the error is easy to miss.
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
| D18 | Review-27 H1: a discarded / not-released quarantined result (and dropped early-complete content) is also blanked or deleted from `mail_inbox` in the same tx; ack/dedupe metadata stays. Implemented in 2.1b. |
| D19 | Ticket **2.2d**: a daemon-owned approval window. The code is typed only there, so agents never handle it; CLI code entry is removed on desktop machines, and headless machines keep terminal mode (OD-P2-3). The spec comes first, then the build and an Opus security review. **Merges before 2.2c.** Replaces review-26 L7. |
| D20 | OQ-2.2d-1: the Linux approval window (zenity/kdialog) gets the summary text via argv. Accept and document it: other local users can see the summary (never the code); hidepid=2 hides it. |
| D21 | **Model trial (2026-09-24):** gradually move complex implementation and investigations to Opus 5.5 workers and measure against Sonnet (`Docs/orchestration/model-trial.md`: minutes, follow-ups, gate failures, review findings, CI after merge). Sonnet stays the default for well-specified tickets. Plan: INV-1 flake investigation (running), then 2.3b git serving and 2.D2 helper runner on Opus, with 2.3c on Sonnet as a control. |
| D22 | Review-36 device link: **L4** tighten offer freshness (now < offer.at + 10 min; ignore offers older than the last unlink from that peer); **L5** content-free desktop notification when a device link becomes active. Both folded into 2.D2. |
| D23 | Enforce **Git ≥ 2.32** at daemon start. If the git found is older, git.read grants are refused (`unsupported`) with a clear message; fs serving is unaffected. Folded into 2.9. |
| D24 | Review-40 helper runner: **L1** clearing the scope, unlinking, or scope expiry **kills a running command's process tree** (reported as cancelled). **L11** refuse a program (or its folder) that users other than the owner (or admins) can write, checked at scope-set and at every run. Ticket 2.D3. |
| D25 | Add content-free notifications **session.result** (A: a result waits for your accept; not sent when quarantined, which has its own event) and **session.changes** (B: the requester asked for changes). No session.closed (request.completed/cancelled cover it). Ticket 2.N. |
| D26 | **Model policy (after the D21 trial, `Docs/orchestration/model-trial.md`):** Sonnet (Sonnet 5) is the default for well-specified implementation tickets. Opus (Opus 5.5) is for investigations and hard debugging, security-critical or OS-level code (process control, permissions, crypto, parsing input from peers), specs, and security reviews. |
| D27 | **Trial: Sonnet with thinking off** for routine, well-scoped tickets. New template **Worker-Sonnet-Lite** `custom-1790318917893-ab35` (Sonnet, thought_level=off, same worker rules as Worker-Sonnet). First pair: B-1 (Lite) vs B-2 (normal Sonnet control), both small backlog items. Results in `Docs/orchestration/model-trial.md`. |
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
- **Machine load:** with 4+ workers each running `go test ./...` at the same time, IPC tests time out (i/o timeout in pairing/ping/mail/e2e). Keep at most ~3 implementation workers at once, and re-run failing packages alone before blaming a change.
- **Symlink tests are skipped on this Windows machine** (no symlink privilege), so symlink behaviour is only tested in CI. After merging fs/fetch code, check that CI is green before building on top. TestFetchSymlinkEscapes was red on every OS from the 2.3a merge until FIX-CI.
- **GitHub API rate limit:** 5000/h shared by me and every worker. Poll CI sparingly (one `gh run list` per merge), and tell workers to avoid gh unless needed.
- **-race runs only in CI** (no cgo here). Every implementation task must say: guard test variables written by goroutines (Perform/AfterCommit hooks, window/notifier fakes) with a mutex or an atomic (review 31). After merging concurrency code, watch the CI `race` job.
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
