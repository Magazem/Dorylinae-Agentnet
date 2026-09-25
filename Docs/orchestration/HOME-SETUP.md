# Home setup: replicate the AgentNet team on another PC

For the **AionUi Butler on the owner's home PC** (and for the owner). Goal: continue the
Dorylinae/AgentNet work at home with exactly the same team, logic and workflow as on the work PC.

Read this whole file once, do the setup in §2–§4, then hand over to the Orchestrator with the
prompt in §6. After that, `Docs/orchestration/HANDOFF.md` is the single source of truth, as on
the work PC.

## 1. How the team really works now

The AionUi assistant rules on the work PC still describe an older structure (a separate
Manager, the Butler creating workers, a Mailbox). **That structure is retired.** What actually
runs:

| Role | Who | What it does |
|---|---|---|
| **Manager** | **the owner (the human)** | The only decision-maker. Approves specs, open decisions (OD lists), tags, spending. |
| **Orchestrator** | one standing assistant (Claude Code, **Opus**), team lead | Talks to the owner directly. Breaks approved work into tickets, **spawns and retires workers itself**, writes task descriptions, runs the merge gate, commits, merges, pushes, watches CI, keeps HANDOFF.md current. Never decides strategy or scope. |
| **Workers** | short-lived assistants spawned per ticket | One task each, in their own git worktree, **no git**, report back, then retired. |
| Butler | AionUi's built-in assistant | Only used for **setup** (this file). Not part of the day-to-day team flow. |

There is no Mailbox and no separate Manager assistant. Don't create them.

## 2. Machine prerequisites (home PC)

1. **AionUi** with the built-in **Claude Code** agent enabled (the assistants below all run on
   it).
2. **Git**: `git config --global core.autocrlf true` (Windows; the repo relies on it).
3. **Go**: the version in the repo's `go.mod` or newer. On the work PC Go lives at
   `%USERPROFILE%\tools\go\bin` (no admin rights there); at home any install is fine, but put it on
   PATH. In Git Bash: `export PATH="$USERPROFILE/tools/go/bin:$PATH"` if you use the same layout.
4. **GitHub CLI** `gh`, logged in to an account with push access to
   `github.com/Magazem/Dorylinae-Agentnet` (`gh auth login`).
5. **golangci-lint v2.13.2**, installed once as a binary (needed for per-OS lint; see §5.5):
   `GOBIN="$TEMP/gl" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2`
6. **Clone** next to where worktrees will live:
   `C:\Users\<you>\Documents\AgentNet` for the repo, with worktrees in
   `C:\Users\<you>\Documents\AgentNet-wt\<name>`. (If your user name differs from the work PC's
   `ysuliman`, the Orchestrator must use your real paths. Tell it in the §6 prompt.)
7. Optional: `agy` (Antigravity CLI) for the real-agent harness runs (never `agy --sandbox`
   on the work PC: it needs admin), and the **Sticky Board** app + its `sticky-board` skill (the
   owner's global Claude instructions ask agents to file bugs/decisions there).

## 3. Create the five assistant templates

Create each assistant in AionUi (Butler: use `config assistants create`, then `config
assistants rule write`, then read both back). All use the **Claude Code** agent. Names matter
less than the settings, but keep them so HANDOFF.md reads the same.

The model values are AionUi aliases: `opus` resolves to **Opus 5.5** (`claude-opus-5-5`),
`sonnet` to **Sonnet 5** (`claude-sonnet-5`), `haiku` to Haiku 4.5. Verify by asking a
spawned worker for its model id; the Orchestrator does this in every report.

| Name | Model | Thinking (`thought_level`) | Permission | Use (model policy D26/D28) |
|---|---|---|---|---|
| **Orchestrator** | opus | medium | bypassPermissions | The standing team lead (§4). |
| **Worker-Sonnet** | sonnet | medium | bypassPermissions | Feature tickets with real design choices. |
| **Worker-Sonnet-Lite** | sonnet | **off** | bypassPermissions | **Default for routine, well-scoped tickets** (backlog items, fixes, portability, integration of already-designed pieces). "Under watch": log every Lite ticket in `Docs/orchestration/model-trial.md`. |
| **Worker-Opus** | opus | auto | auto | Specs, adversarial spec reviews, security reviews, investigations/hard debugging, security-critical or OS-level code (process control, permissions, crypto/signing, parsing peer input). |
| **Worker-Haiku** | haiku | auto | bypassPermissions | Mechanical bulk work, read-only lookups (rarely used). |

Descriptions (optional, short): "Short-lived worker, one task, then retired." Skills: none.
MCP: default.

### 3.1 Worker rule (Worker-Sonnet, Worker-Sonnet-Lite, Worker-Haiku)

```markdown
# Worker

You are a short-lived worker on this team. You get one task from the task board with acceptance criteria. Do exactly that task, verify the result against the criteria, report, and stop. You will be retired after reporting unless another task is queued for you immediately.

Rules:
- Follow the task description exactly; do not widen scope.
- No git commands of any kind, including stash, checkout and reset; use a backup copy to revert.
- Never hand-edit generated files (e.g. schema-manifest.json); edit the generator if asked.
- If your task is to render, build or prepare artefacts for an evidence gate: you do not judge or score anything; judging is done by other workers. Write the artefacts and the manifest, report, and stop. Any judging or verdict you produce will be discarded.
- Report to the slot_id named in your task using team_send_message. Cite file paths. Stay within the word limit given.
- If blocked or the task is ambiguous, say so in your report with your best-effort partial result. Do not wait or stand by.
- Do not contact the user directly.
```

(Each task description overrides "no git of any kind" with "read-only git is OK", and the
Orchestrator names itself as the report target.)

### 3.2 Worker-Opus rule

```markdown
# Worker (Opus)

You are a short-lived worker on this team. You get one task from the task board with acceptance criteria. Do exactly that task, verify the result against the criteria, report, and stop. You will be retired after reporting unless another task is queued for you immediately.

Rules:
- Follow the task description exactly; do not widen scope.
- No git commands of any kind, including stash, checkout and reset; use a backup copy to revert.
- Never hand-edit generated files (e.g. schema-manifest.json); edit the generator if asked.
- Report to the slot_id named in your task using team_send_message. Cite file paths. Stay within the word limit given.
- If blocked or the task is ambiguous, say so in your report with your best-effort partial result. Do not wait or stand by.
- Do not contact the user directly.
```

(The work-PC version also has a "blind evidence panel judge" section from an older project; it
isn't used for AgentNet and can be left out.)

## 4. The Orchestrator: corrected rule

Use this rule, **not** the old work-PC text (which still says "You are NOT the manager… ask the
Butler for workers… route questions via Mailbox"). This version matches how the team actually
works.

```markdown
# Orchestrator

You are the Orchestrator and team lead for the owner's project. The owner (the human) is the Manager and the only decision-maker; talk to them directly and concisely. You never set strategy or scope; you turn approved decisions into work, run it, and report.

## First thing, every session
Read `Docs/orchestration/HANDOFF.md` in the repo before doing anything. It records the current phase, running tickets, owner decisions (final), hard rules, lessons and the worker-template ids. Keep it updated after every merge and every owner decision.

## Workers
- You spawn and retire workers yourself (team_spawn_agent / team_shutdown_agent), one ticket each, using the templates listed in HANDOFF.md §1 and the model policy in HANDOFF.md §3 (D26, D28).
- Every worker gets its own git worktree under `<Documents>/AgentNet-wt/<name>` on branch `pN/<name>`, made by you. Workers never run git (read-only is fine); you commit, rebase, merge and push.
- Dispatch with team_task_create (owner = slot_id), then one short team_send_message pointing at the task id. New workers often report "ready, no task" before the task arrives; that is harmless.
- Never tell a worker to wait for another. Dispatch dependent work only when its prerequisite is merged (or build on the unmerged branch and rebase later).
- At most about 3 workers running full test suites at once (the machine overloads and IPC tests time out).
- Retire a worker once its work is committed. Mark its task completed first.

## Decisions
Specs before code: an Opus worker writes specs, an Opus adversarial review fixes them in place, then the owner approves the open decisions (OD list). Ask the owner with AskUserQuestion, with a recommended option first. Owner decisions are final; record them as D-numbers in HANDOFF.md §3.

## Quality
Run the full merge gate yourself before every merge (HANDOFF.md §2 rule 5, including per-OS lint). Security-sensitive tickets get an Opus security review before merge. Watch CI after every push. File bugs and decisions on the Sticky Board when the skill is available.

## Parking
When the owner says "park", ask workers to finish their current step safely (tree builds, WIP notes, report); never send an abrupt STOP. Then commit each worktree's state and update HANDOFF.md §0.
```

## 5. Workflow and lessons that are not in any assistant rule

Most of this is also in HANDOFF.md (§2 hard rules, §5 lessons). It's collected here so a fresh
Orchestrator at home can't miss it.

### 5.1 The standard task description

Every task the Orchestrator creates follows this shape (copy it):

```
Worktree: C:\Users\<you>\Documents\AgentNet-wt\<name> (branch pN/<name>, from main). Stay inside it.
NO git commands except read-only; avoid gh. List every file changed/created.
Start your report with your model id and elapsed time.

TICKET: <id> in Docs/review/<NN>-phaseN-tickets.md (its Files + Acceptance are the contract).
Spec: Docs/protocol/<x>.md (authoritative; owner decisions Dnn in HANDOFF §3). <review notes to apply>
<scope limits; what parallel tickets touch>
Rules: no DB/audit inside a tx via another connection (after-commit callbacks); CI runs -race
(not available locally): guard test variables written by goroutines with a mutex or an atomic;
testutil.TempDir, poll with deadlines, injected Now; e2e with the two-daemon + relay harness.
Tests that roll the schema back must undo every later migration that ALTERs a table.
The machine is shared: if an unrelated IPC test times out, re-run that package alone.
GATE (Go on PATH): go build ./... ; go vet ./... ; go test ./... -count=1 twice ;
go run ./tools/verifyvectors ; GOOS=windows|linux|darwin "$TEMP/gl/golangci-lint.exe" run
--allow-parallel-runners ./... (CRLF gofmt noise only) ; GOOS=linux/darwin go vet.
Report: model id, elapsed, files, tests per acceptance item, gate.
```

Security reviews use the same header plus "Review <ticket>: <files>. Against: <spec>. Attack:
<list>. OUTPUT: Docs/review/<NN>-<ticket>-review.md (Critical/High/Medium/Low); FIX
Critical/High/Medium in code with tests; Lows if cheap."

### 5.2 The ticket flow per phase

1. Opus spec writer (docs only) → specs + ticket plan with migrations, review markers,
   suggested model per ticket, OD list.
2. Opus adversarial spec review, fixing in place.
3. Owner approves the ODs (AskUserQuestion). Record as a D-number. Merge the specs.
4. Tickets in dependency order, max ~3 at a time. Each: build → Orchestrator gate → (Opus
   security review if marked) → Orchestrator gate again → rebase on main → merge → push → CI.
5. Harness run with real agents near the end (owner-approved cost), then the phase push. Tags
   only with the owner's explicit OK.

### 5.3 Merge procedure (safe sequence)

In the worktree: stage null-safely
(`git diff --ignore-cr-at-eol --name-only -z | xargs -0 -r git add --` plus untracked via
`git ls-files --others --exclude-standard -z | xargs -0 -r git add --`), commit, verify nothing
is left, then `git checkout -- .` (CRLF noise), `git rebase main`, full gate, then in the main
checkout `git merge --ff-only <branch>`, remove the worktree and branch, push, check CI once.
Commit messages end with the `Co-Authored-By` line given in the session's system reminder.
Conflicts so far were always "both sides added independent code": keep both, and check
migration placeholders (never merge a NO-OP placeholder migration).

### 5.4 Worker behaviour to expect

- "Ready, no task yet" arrives before the task: harmless. But most new workers then go idle without
  starting; if the worktree is still empty at the next check, interrupt with "start task <id> now".
- Idle notifications are normal. A worker is stuck only if it stays idle with an empty
  worktree after a pointer message; then `team_interrupt_agent` with "start task <id> now".
  Spec and review workers read for a long time before writing; don't retire them early.
- "Could not process queued messages … paused" = usage limit or delivery failure: interrupt
  the worker with "resume task <id> from your worktree" once the limit resets.
- After `team_shutdown_agent`, wait for "Teammate X was removed". A worker once approved its
  shutdown but stayed on the team idle; check `team_members` and re-issue the shutdown.
- Worker reports are leads: re-run the gate yourself; they sometimes miss per-OS lint or a new
  test that landed on main meanwhile.

### 5.5 Environment traps

- CRLF: use `git diff --ignore-cr-at-eol`; gofmt/lint noise on untouched files is CRLF-only
  (check with `tr -d '\r' < f | gofmt -l`).
- Per-OS lint: `GOOS=linux go run …golangci-lint…` does **not** work (builds a Linux binary);
  use the installed `$TEMP/gl/golangci-lint.exe` with `GOOS=linux` / `GOOS=darwin`. CI lints on
  Linux.
- `-race` and symlink tests run only in CI (no cgo, no symlink privilege on Windows): after
  merging concurrency or file-serving code, check CI before building on it.
- GitHub API limit is 5000/h shared by you and all workers: poll CI once per merge, and tell
  workers to avoid `gh`.
- No `python` on the work PC (use node or the Edit tool); PowerShell 5.1 there.
- Owner is cost-sensitive: no paid CI secrets; the weekly harness uses a scripted stand-in.

### 5.6 The Orchestrator's private memory notes (work PC)

These lived in Claude Code's per-project memory, not in the repo. At home, either recreate them
as memories or just rely on HANDOFF.md (it contains the same facts):

- **Orchestrator handoff:** all orchestration state is in `Docs/orchestration/HANDOFF.md`;
  read it first on resume, update it after every merge/decision and before a context reset.
- **Park gracefully:** on "park", ask workers to finish their step safely; never an abrupt
  STOP (an abrupt stop once left a build-breaking half edit).
- **Owner environment:** the work PC has no admin rights; two-machine tests happen at home;
  the owner is cost-sensitive; tools: Claude Code, agy, Codex (account-limited until
  2026-10-02).

## 6. Handing over at home

1. Owner: `git pull` in the repo on the home PC (this file and HANDOFF.md come with it).
2. **Unfinished worker branches live only on the work PC** (worktrees are local). When parking
   on the work PC, the Orchestrator commits every worktree; to continue those tickets at home,
   the owner must OK pushing those `pN/*` branches (pushing anything but `main` needs the owner's
   OK, HANDOFF §2 rule 7). Otherwise the home Orchestrator restarts those tickets from `main`.
3. Butler: create the five templates (§3, §4) and tell the owner their new assistant ids.
4. Owner: start a team with the new **Orchestrator** as lead and send:

   > You are the Orchestrator. Read `Docs/orchestration/HANDOFF.md` and
   > `Docs/orchestration/HOME-SETUP.md` first. On this PC the repo is at `<path>`, worktrees go in
   > `<path>-wt`, Go is at `<path>`, and the worker template ids are: Worker-Sonnet `<id>`,
   > Worker-Sonnet-Lite `<id>`, Worker-Opus `<id>`, Worker-Haiku `<id>`. Update HANDOFF.md §1 with
   > these ids (keep the work-PC ids too, labelled), then continue from §0.

5. The home Orchestrator updates HANDOFF.md §1 with a "Home PC" template table, and from then on
   everything proceeds exactly as on the work PC.
