# WIP: 3.H headless harness (parked)

Model: Claude Sonnet 5. Parked at the owner's request before the real Claude Code / agy
round; that round moves to the next session.

## Done

- `tests/harness/standin/main.go`: added `-mode debate` (kept `-mode phase2` as the
  default, unchanged behaviour for 2.H). `debateRoleA`/`debateRoleB`/`debateLoop` drive
  the whole turn sequence purely from `agentnet debate <id> --json`'s `turn`/`expect`
  fields (no hardcoded slot numbers): the first own move is a real challenge, every later
  one a pass, which converges the debate however many rounds `-rounds` sets. `-disagree`
  on B answers `accept:false` to force `escalated` (deterministic only with the stand-in,
  OD-P3-7/3.5). `go build ./...`, `go vet ./...` pass; `go test ./... -count=1` all green
  (no internal packages touched, only tests/harness/standin).
- `tests/harness/phase3-agents.ps1` (new): reuses the Phase 2 scaffolding unchanged
  (relay, two daemons with `DORYLINAE_APPROVAL=terminal DORYLINAE_DEBUG=1`, the approval
  pump, pairing, team `t3h`, `Invoke-CliJson`/`Wait-Until`/`Start-Agent`/`Wait-Agent`
  copied verbatim). New `Invoke-Round`: for `-Harness standin`, runs the two stand-ins
  concurrently (`agreed` and `escalated` scenarios as separate rounds) and drives the
  human-constraint step itself (`agentnet debate <id> --constrain` on A, answered by A's
  running approval pump — the script, not an agent, per OD-P3-3). For `-Harness real`,
  turn-driven per OD-P3-9: polls `agentnet debate <id> --json` and runs the agent whose
  turn it is, once, with a short prompt (first prompt names the question/asks for a
  debate ≤2 rounds for the initiator, "a teammate invited you" for the respondent, else
  the plain "take your next step" text); never names a subcommand. Assertions are all
  from `--json`/audit/`decision verify`, never agent prose: exactly one debate request,
  `closed`/`agreed|escalated`, `rounds.current<=2`, decision exported + `decision verify`
  exits 0 with `complete:true` (two signatures), `decision --md` wrote a file, the
  constraint is in the exported Decision's `human_decisions`, `log --verify` ok on both
  daemons.
- `tests/harness/phase3-agents.sh` (new): Linux/macOS port of the above, same structure
  as `phase2-agents.sh` (named-pipe approval pump, fixed fd 7/8, `/tmp` run dir for the
  Unix socket path limit). `bash -n` passes. **Not run on this Windows machine** (Git Bash
  named pipes don't connect to native Windows daemons here — the same limitation noted for
  `phase2-agents.sh`); its first real exercise is the weekly CI job.
- `tests/harness/standin`'s fixture (repo with two `Debounce` designs, `NOTES.md` +
  `design_a.go`/`design_b.go`) is written inline by both scripts so the agents have
  something to argue.
- `Docs/agents/snippet.md`: added a debate paragraph (start/answer/wait via
  `agentnet debate`, and "a human constraint needs your human's approval; never ask for a
  code" reusing the existing approval-code rule).
- `tests/harness/README.md`: new "Ticket 3.H" section (turn-driven agents, the
  script-runs-the-constraint note, harnesses, assertions, running).
- `.github/workflows/phase3-harness.yml` (new): weekly (Mon 06:00 UTC) + `workflow_dispatch`,
  Linux/Windows/macOS matrix, stand-in only (both scenarios), 25 min timeout, log upload on
  failure. Modelled on `phase2-harness.yml`.
- `tests/phase3-manual.md` (new — did not exist yet; created with a clearly separate
  "## 3.H results" section for the Orchestrator to merge against 3.9's own section if it
  lands first).

## Stand-in run: how far it got

Ran locally (`pwsh tests/harness/phase3-agents.ps1 -SkipBuild -RepoRoot <worktree>`),
**both rounds PASS**:

```
round 1: standin -> standin (agreed): PASS (outcome=agreed, 4.6s)
round 2: standin -> standin (escalated): PASS (outcome=escalated, 4.9s)
elapsed: 11.5s (limit 1500s)
```

Both hit every assertion (single debate request, closed/agreed|escalated, decision
exported + verified with two signatures, `decision --md` written, constraint present in
`human_decisions`, `log --verify` ok on both daemons).

Gate so far (in the worktree): `go build ./...` ok, `go vet ./...` ok (host + `GOOS=linux`
+ `GOOS=darwin`), `go test ./... -count=1` all green, `golangci-lint run` on host/linux/darwin
shows only pre-existing CRLF gofmt noise in files this ticket never touched (agentcard,
specvectors, relay, cmd/agentnet e2e tests, cmd/relay) — nothing in `tests/harness/standin`
or the new scripts. `bash -n tests/harness/phase3-agents.sh` passes.

## What's left

1. **The real Claude Code ↔ agy run** (both directions, per the ticket): a background
   attempt at round 1 (Claude as initiator, agy as respondent) was started, got as far as
   "turn 1: initiator (claude) starts the debate" (one `claude -p ...` invocation in
   flight), then was stopped on request before it produced a debate id or any further
   turns. No daemons, relay, or agent processes were left running afterward (checked with
   `Get-Process`); the aborted attempt's temp run directory is
   `%TEMP%\phase3-agents-a074a58e` (safe to delete, nothing else depends on it). This
   still needs: round 1 (claude→agy) and round 2 (agy→claude), recorded pass/fail,
   duration, turn count and any agent confusion in `tests/phase3-manual.md`'s "3.H
   results" section.
2. Not yet exercised in this session: `-Harness real -InitiatorHarness codex
   -RespondentHarness ...` path (present in the script, mirrors 2.H, untested here — not
   required by the ticket, which only asks for Claude Code + agy).
3. The weekly CI job (`phase3-harness.yml`) has not had its first real CI run yet (added
   this session, not pushed).

## Files changed/created this session

- `tests/harness/standin/main.go` (edited: `-mode` flag, debate role/loop functions)
- `tests/harness/phase3-agents.ps1` (new)
- `tests/harness/phase3-agents.sh` (new)
- `tests/harness/README.md` (edited: added the 3.H section)
- `Docs/agents/snippet.md` (edited: debate paragraph, status line)
- `.github/workflows/phase3-harness.yml` (new)
- `tests/phase3-manual.md` (new, "## 3.H results" section only)
- `WIP-3.H.md` (this file)
