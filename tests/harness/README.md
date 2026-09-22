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

Round 1: Claude Code sends, Codex CLI receives. Round 2: roles swapped. Each
harness is sender once and recipient once, per the ticket.

## Prerequisites

- Go 1.27+ on `PATH` (or pass `--skip-build`/`-SkipBuild` with binaries
  already in `<repo>/bin`).
- **Claude Code** (`claude`) logged in and able to call the API
  non-interactively.
- **Codex CLI** (`codex`) logged in and able to call the API
  non-interactively. If Codex is unavailable, substitute another headless
  harness (edit the `$rounds` array / `ROUNDS` list at the bottom of the
  script) and name it in `tests/phase1-manual.md`.
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
`--only-round 1|2`/`-OnlyRound`, `--branch NAME`/`-Branch`.

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
  snippet, which is weaker than Claude's allowlist. Document any stronger
  mechanism found later (e.g. an execpolicy `.rules` file) here.
