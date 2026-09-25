# Ticket 1.H: headless agent harness

Runs two REAL headless coding-agent harnesses through a live AgentNet
request -> inbox -> accept -> complete round trip, using only the plain agent
snippet ([../../Docs/agents/snippet.md](../../Docs/agents/snippet.md)) as
instructions. Not in CI: it needs model API keys and installed harnesses. Run
it by hand before 1.P and record the result in
[../phase1-manual.md](../phase1-manual.md).

## What it does

1. Builds `agentnet`, `agentnetd` and `relay` (unless `--skip-build` /
   `-SkipBuild`).
2. For each of two rounds (harness roles swapped), in a fresh temp directory:
   - starts a loopback relay and two `agentnetd` daemons (`agent-a`,
     `agent-b`) with separate `--home` directories;
   - pairs them (`agentnet pair`) and creates a team `t1h` with both as
     members;
   - writes the snippet into a scratch working directory for each agent —
     `CLAUDE.md` for Claude Code, `AGENTS.md` for Codex CLI — and nothing
     else;
   - runs the **sender** agent headless, told in plain English to ask the
     other agent (by AgentNet peer name) for a review of a branch with a
     stated `--idempotency-key`. The prompt never names an `agentnet`
     subcommand: the agent must find it from the snippet and `--help`;
   - runs the **recipient** agent headless, told to check its inbox, accept
     what's there, do nothing else, then complete it with a note and a D14
     result (`status: n/a`, one-line summary);
   - asserts the outcome from `agentnet ... --json` output and the daemons'
     `audit_events` tables — never from agent prose.
3. Prints a pass/fail summary and exits non-zero if either round failed or
   the whole run took over 10 minutes.

Round 1: Claude Code sends, the second harness receives. Round 2: roles
swapped. Each harness is sender once and recipient once, per the ticket.
Default second harness is **agy** (Antigravity CLI) while Codex CLI's
account usage limit is in effect (resets 2026-10-02; see
`tests/phase1-manual.md`); pass `-SenderHarness codex -RecipientHarness
codex`/`--sender-harness codex --recipient-harness codex` (mixed with
`claude`) to use Codex instead once it is available again.

## Prerequisites

- Go 1.27+ on `PATH` (or pass `--skip-build`/`-SkipBuild` with binaries
  already in `<repo>/bin`).
- **Claude Code** (`claude`) logged in and able to call the API
  non-interactively.
- **agy** (Antigravity CLI, `%LOCALAPPDATA%\agy\bin\agy.exe`) logged in and
  able to call the API non-interactively (`agy -p "..." --output-format json
  --print-timeout 60s` should return `"status":"SUCCESS"`). If agy needs an
  interactive login, run `agy` once interactively yourself first — the
  harness scripts never attempt to log in.
- **Codex CLI** (`codex`) logged in and able to call the API
  non-interactively, if selected in place of agy via `-SenderHarness`/
  `-RecipientHarness` (`--sender-harness`/`--recipient-harness`) `codex`.
- **Python 3 with the stdlib `sqlite3` module**, used only to read the
  `audit_events` table from each daemon's `dorylinae.db`. There is no
  `sqlite3` CLI or CGo build in this environment (the daemon uses
  `modernc.org/sqlite`, a pure-Go driver with no CLI counterpart), so the
  script shells out to `python3 -c "import sqlite3; ..."` rather than adding
  a new Go tool (out of scope for this ticket: scripts and docs only). If
  Python is unavailable, every round fails at the audit-log assertion; the
  rest of the round (request/inbox/complete) still ran and is visible in the
  logs.
- Windows: **Windows PowerShell 5.1** (the version this script was written
  and run against; `pwsh` 7+ should also work but was not tested here).
  `System.Diagnostics.ProcessStartInfo.ArgumentList` does not exist under
  .NET Framework (only under .NET Core/5+), so the script builds a quoted
  `Arguments` string by hand — do not reintroduce `.ArgumentList`.

## Running

```powershell
# Windows
pwsh tests/harness/phase1-agents.ps1
# or, from an existing Windows PowerShell 5.1 prompt:
powershell -File tests/harness/phase1-agents.ps1
```

```bash
# Linux/macOS (untested in this environment; see tests/phase1-manual.md)
tests/harness/phase1-agents.sh
```

Useful flags (both scripts): `--skip-build`/`-SkipBuild`,
`--only-round 1|2`/`-OnlyRound`, `--branch NAME`/`-Branch`,
`--max-attempts N`/`-MaxAttempts` (retry a failed round up to N times and
report a pass rate; default 1, no retry).

Logs for every round (daemon stdout/stderr, agent stdout/stderr, relay log)
are written under a fresh temp directory printed at the start of the run;
nothing is cleaned up automatically so a failure can be inspected afterward.

## Why the assertions read `--json`, not agent text

The sender and recipient prompts are plain English and never name an
`agentnet` subcommand — the whole point of the run is to prove the snippet
plus `--help` is enough for a real agent to find and use the CLI correctly.
Because of that, the script cannot trust anything the agent *says* it did:
it re-derives the outcome independently from `agentnet request show --json`,
`agentnet inbox --all --json` and the audit log, exactly as the ticket
requires.

## Known limitations

- The two daemons run on one machine with two config directories; this
  exercises the full protocol (relay, pairing, team roster, mail, presence)
  but is not a two-machine test (that is `tests/phase0-manual.md` /
  `tests/phase1-manual.md`'s per-OS manual checks).
- Fingerprint verification (`agentnet peers verify`) is skipped: pairing
  alone is enough to establish `trust=code`, which is sufficient for the
  request path.
- Tool-allowlisting differs by harness. Claude Code's `--restricted --tools
  Bash --allowedTools "Bash(agentnet *)" --permission-prompts none` is a hard
  allowlist: only `agentnet ...` commands run, and anything else is denied
  automatically rather than prompting. Codex CLI (as of 0.152.1) has no
  equivalent per-binary command allowlist; the script confines it with
  `--sandbox workspace-write` and a working directory that holds only the
  snippet, which is weaker than Claude's allowlist. agy (Antigravity CLI
  v1.2.9) also has no confirmed working per-binary allowlist in headless
  print mode: it denies any unapproved tool call outright (there is no way to
  prompt in headless mode), and its own error message points at a
  `permissions.allow` / `command(<target>)` rule in
  `~/.gemini/antigravity-cli/settings.json`, but that is the operator's real,
  shared global config — this session could not confirm a per-run override of
  that path works (`HOME`/`USERPROFILE` env overrides had no effect; editing
  the real global file was avoided as out of scope and risky to shared
  state). The script therefore runs agy with
  `--dangerously-skip-permissions` (auto-approves *every* tool call, not
  just `agentnet`), compensated by keeping the agy working directory
  containing **only** the `AGENTS.md` snippet (no repo source, no other
  files); this is weaker containment than Claude's allowlist. **`--sandbox`
  is deliberately not used**: on the Windows machine this ticket was run on
  (no admin rights), it triggered a Windows UAC elevation prompt that
  headless/print mode cannot answer — confirmed while running this ticket.
  Do not add `--sandbox` back without confirming the target machine has
  admin rights, or that agy's sandbox no longer requires elevation. Document
  any stronger allowlist mechanism found later (e.g. a working per-project
  `settings.json` override, or an execpolicy `.rules` file) here.

---

# Ticket 2.H: Phase 2 headless harness

`phase2-agents.ps1` / `phase2-agents.sh` drive the whole Phase 2 loop through two
headless agents: request -> accept (session) -> grant (`fs.read`) -> fetch ->
result -> release (the grant makes the result quarantined) -> accept-result, plus
a consult (`agentnet consult` with one context file, answered with `agentnet
result`, then `wait` and `accept-result`). The prompts never name a subcommand;
the agent learns them from [the snippet](../../Docs/agents/snippet.md) and `--help`.

## Approvals are answered by the script, never the agent

Both daemons start with `DORYLINAE_APPROVAL=terminal` and `DORYLINAE_DEBUG=1`
(stdin and stderr are pipes). The script reads `AgentNet approval a-xxxxxx: ...
Code NNNNNN.` from each daemon's stderr and writes `a-xxxxxx NNNNNN` to that
daemon's stdin ([approval.md](../../Docs/protocol/approval.md#headless-machines)).
The agent under test has no pipe and no CLI form that takes a code. Windows uses
a small in-process C# thread (`Add-Type`); Linux/macOS use two named pipes per
daemon (a `<>` open keeps stdin from seeing EOF between codes).

## Harnesses

- `-Harness standin` (`--harness standin`, the default): the scripted Go program in
  [`standin/`](standin/main.go) runs the CLI calls a real agent would make, as two
  concurrent processes (A and B). Free; used by the weekly CI job
  ([`.github/workflows/phase2-harness.yml`](../../.github/workflows/phase2-harness.yml),
  Linux, Windows, macOS; OD-P2-12). About 10 s.
- `-Harness real` (`--harness real`): Claude Code and agy, round 1 Claude as A / agy
  as B, round 2 swapped. Needs logged-in CLIs; run by hand before a release and
  record the result in [`../phase2-manual.md`](../phase2-manual.md). Codex CLI via
  `-SenderHarness codex -RecipientHarness ...`. **Never add `agy --sandbox`** (UAC
  prompt, no admin). Tool confinement is as for 1.H (see above); the Claude prompt
  allowlist is `PowerShell(agentnet *)` on Windows and `Bash(agentnet *)` elsewhere.

Unlike 1.H, A and B run **concurrently**: A grants only after B accepts, and B
fetches only after A grants. Each agent gets up to 7 minutes (`-AgentTimeoutSeconds`);
the whole run has a 15-minute cap (`-TotalTimeoutSeconds`).

## Assertions (from `--json` and the audit log, not agent text)

On A: exactly one `review` and one `question` request; both sessions `closed` with
`outcome: accepted`; at least one issued grant, and it ends `revoked` after the
close; audit rows `request.submit`, `request.in`, `request.accept`, `ws.close`,
`grant.create` and `grant.fetch` across the two daemons.

## Running

```powershell
powershell -File tests/harness/phase2-agents.ps1 -RepoRoot <repo>            # stand-in
powershell -File tests/harness/phase2-agents.ps1 -RepoRoot <repo> -Harness real
```

```bash
tests/harness/phase2-agents.sh                  # stand-in
tests/harness/phase2-agents.sh --harness real
```

Pass `-RepoRoot` explicitly when running through a tool that leaves `$PSScriptRoot`
empty. The `.sh` port could not be run on the Windows machine it was written on
(Git Bash named pipes do not connect to native Windows daemons); the weekly CI job
is its first real run.

---

# Ticket 3.H: Phase 3 headless harness (debate)

`phase3-agents.ps1` / `phase3-agents.sh` drive a [debate](../../Docs/protocol/debate.md)
(request `debate` -> accept + opening position -> moves -> proposal/answer -> a signed
[Decision](../../Docs/cli/decision.md) on both sides) through two headless agents. The
fixture is a small repo with two plausible designs of one function
(`fixture/NOTES.md`, `design_a.go`, `design_b.go`) so the agents have something to argue.
Reuses the Phase 2 scaffolding unchanged: the loopback relay, two daemons with
`DORYLINAE_APPROVAL=terminal DORYLINAE_DEBUG=1`, pairing, a team (`t3h`), and the
script (never the agent) reading approval codes from a daemon's stderr and answering
them on its stdin.

## Turn-driven real agents (OD-P3-9)

Unlike 2.H's concurrent A/B agents, a real-agent round here is **turn-driven**: the
script polls `agentnet debate <id> --json` and, whenever it is a side's turn, runs
that side's agent headless **once** with a short prompt, then polls again. The first
prompt for the initiator names the question (from the fixture) and asks for a debate
of at most 2 rounds; the first prompt for the respondent says a teammate invited it to
a debate; every later prompt (either side) is the plain "Your AgentNet debate with your
teammate is waiting for you. Take your next step, then stop." Prompts never name a
subcommand — the agent learns them from [the snippet](../../Docs/agents/snippet.md)'s
debate paragraph and `--help`. This costs about 6-8 agent invocations per debate
(review 43 L7): both opening positions, up to 4 moves, the proposal and the answer.

## The human constraint is added by the script, never the agent

After both positions exist, **this script** (not an agent) runs `agentnet debate <id>
--constrain "..."` on the initiator's daemon, and that daemon's already-running approval
pump answers the code, exactly as a human would (OD-P3-3: an agent can never supply its
own approval code). This exercises 3.4's human-constraint path without needing either
agent to know about it.

## Harnesses

- `-Harness standin` (`--harness standin`, the default): the scripted Go program in
  [`standin/`](standin/main.go) (`-mode debate`) drives both sides through the full
  turn sequence itself, as two concurrent processes. Free; used by the weekly CI job
  (`.github/workflows/phase3-harness.yml`, Linux, Windows, macOS). Runs **two**
  scenarios: an agreed debate, and a forced escalation (`-disagree`, deterministic only
  with the stand-in — OD-P3-7/3.5 — never with real agents).
- `-Harness real` (`--harness real`): Claude Code and agy, each once as initiator and
  once as respondent. Needs logged-in CLIs; run by hand before a release and record the
  result in [`../phase3-manual.md`](../phase3-manual.md). Codex CLI via
  `-InitiatorHarness codex -RespondentHarness ...`. **Never add `agy --sandbox`** (UAC
  prompt, no admin). Tool confinement is as for 1.H/2.H (see above).

## Assertions (from `--json` and the audit log, not agent text)

Both debates end `closed` with outcome `agreed` or `escalated` (a real agent may
legitimately disagree) and `rounds.current <= 2`; exactly one `debate` request on the
initiator; the closed debate's Decision exports (`agentnet decision <id> --json --out`)
and `agentnet decision verify` on the exported file exits 0 with two signatures
(`complete: true`); `agentnet decision <id> --md --out` wrote a file; the human
constraint is in the exported Decision's `human_decisions`; `agentnet log --verify` is
`ok` on both daemons.

## Running

```powershell
powershell -File tests/harness/phase3-agents.ps1 -RepoRoot <repo>            # stand-in
powershell -File tests/harness/phase3-agents.ps1 -RepoRoot <repo> -Harness real
```

```bash
tests/harness/phase3-agents.sh                  # stand-in
tests/harness/phase3-agents.sh --harness real
```

Useful flags (both scripts): `-SkipBuild`/`--skip-build`, `-OnlyRound`/`--only-round`
(stand-in: 1 = agreed, 2 = escalated), `-InitiatorHarness`/`-RespondentHarness`
(`--initiator-harness`/`--respondent-harness`, mixed `claude`/`agy`/`codex`).
