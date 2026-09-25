# Phase 3 manual tests

Mark each step `[x] PASS` or `[x] FAIL` and add notes. Created by ticket 3.9. These are the
checks Phase 3's automated coverage cannot make: what a human sees in a browser, what a
tampered database looks like from the outside, and the long-text approval window path.

## A Decision viewed on GitHub

`agentnet decision --md` writes a Markdown file naming a sibling `d-….json` (OD-P3-10);
[decision.md](../Docs/protocol/decision.md) documents that both peers' free-text (topic,
positions, arguments, evidence notes, the proposal, the answer, any constraint) renders inert.

- [ ] Run a debate to a close (agreed or escalated), then `agentnet decision <id> --md --out
      out.md` and push `out.md` (and its `d-….json`) to a scratch GitHub repo, or open it in
      GitHub's Markdown preview (a gist works too).
- [ ] A peer's argument containing Markdown syntax (a `#` heading, a `[link](javascript:...)`,
      an image tag, a table, or raw HTML) renders as **plain text**, not as a heading, a
      clickable/`javascript:` link, an embedded image, or a table — matching the AST
      inertness test in `internal/decision` (or the renderer's package).
- [ ] The UNCONFIRMED banner (if the Decision is single-signed) is visible and not swallowed
      by GitHub's Markdown renderer (e.g. not inside an HTML comment).
- [ ] The file's JSON sibling is **not** required to render the Markdown — GitHub shows the
      `.md` file on its own with no missing-reference warnings.

## `agentnet log --verify` after a hand-edited database

Only run this if the owner has a SQLite browser/CLI available (e.g. `sqlite3`, DB Browser for
SQLite). This exercises the "what the chain proves" limits documented in
[audit.md §What the chain proves, and what it does not](../Docs/protocol/audit.md#what-the-chain-proves-and-what-it-does-not),
outside of the unit tests that drop the triggers programmatically.

- [ ] Stop `agentnetd`. Open `dorylinae.db` with `sqlite3` and run
      `UPDATE audit_events SET detail = detail WHERE id = (SELECT MAX(id) FROM audit_events)`
      or similar — confirm SQLite **refuses** it (the migration-1 update trigger), without
      needing `agentnet` at all.
- [ ] With the triggers dropped (`DROP TRIGGER audit_events_no_update` etc., as the automated
      tests do), change one `detail` field of a row, save, and restart the daemon (or run
      `agentnet log --verify` while it is stopped, which falls back to reading the file
      directly). Confirm the output is `{"verify": {"status": "broken", "reason":
      "hash_mismatch", "first_bad": <id>}}` and the exit code is **5**.
- [ ] Restore the row (or restore from a backup taken before this check) and confirm
      `agentnet log --verify` returns to `{"status": "ok"}`, exit 0.
- [ ] With the daemon stopped and no database file present, confirm `agentnet log --verify`
      exits **3** ("no daemon and no database file").
- [ ] Note here which SQLite tool was used and its version.

## The long approval-window summary for a debate constraint

Automated coverage never pops a real window (see
[phase2-manual.md](phase2-manual.md#the-approval-window-ticket-22d-approvalmdthe-approval-window));
this repeats that check specifically for `debate_constraint` (OD-P3-3, review 46 H2), whose
summary is a human-authored sentence that can run to several hundred characters.

- [ ] Start a debate on each OS (Windows, macOS, Linux) and run `agentnet debate <id>
      --constrain "<a sentence of at least 300 characters, plain visible text only>"`.
- [ ] Confirm the approval window's summary shows the **whole** constraint text: on Windows in
      the read-only, word-wrapped, scrollable box; on macOS and Linux, confirm the end of the
      text is visible or reachable (scrolling or resizing the dialog), matching the general
      long-summary check already logged in `phase2-manual.md`.
- [ ] Confirm every character shown matches what was typed — no control character, no format
      character, and nothing outside the visible-characters rule of
      [debate.md](../Docs/protocol/debate.md) (`DisplayQuote`) is silently dropped or rendered
      as something else.
- [ ] Approve with the code from the toast/terminal and confirm `agentnet decision <id>
      --json`'s `human_decisions` carries the constraint text unchanged.

## 3.H results

Ticket 3.H: `tests/harness/phase3-agents.ps1`/`.sh`, real-agent debate round (Claude Code
vs agy), OD-P3-9 turn-driven.

Real run 2026-09-25, `phase3-agents.ps1 -Harness real`, run dir
`%TEMP%\phase3-agents-d18955f4`, total 1,196.7 s (limit 1500 s). Assertions came from
`--json`/audit, not prose. Attempt 0 (`phase3-agents-91f922c3`) was a harness/setup failure
before any debate started: headless `claude -p` was denied the file write for
`position.json` (only `PowerShell(agentnet *)` was allowed), so no debate existed; fixed by
adding the `Write` tool to the claude args in `Start-Agent`, then re-run.

| Round | Result | Outcome | Duration | Turns |
|-------|--------|---------|----------|-------|
| 1 claude -> agy | PASS | agreed | 589.4 s | 9 |
| 2 agy -> claude | PASS | agreed | 603.3 s | 9 |

Cost: Claude Code (claude-opus-5-5) reported 1.06 USD (round 1) + 1.21 USD (round 2) = about
2.27 USD; agy reports no cost (not included).

Agent confusion / snippet wording problems seen:

- Claude re-runs a directory listing (`Get-ChildItem`, `..`) and file reads at the start of
  almost every turn; several of those (`Get-Content`, `Get-ChildItem`) were permission
  denials in the restricted headless mode (1-5 denials per claude turn), so it burned turns
  (up to 30) on orientation instead of the move.
- The move-file JSON shape is not in the snippet: claude guessed `challenges:[{}]`, `text`
  instead of `point`, and `targets:[0]` instead of `["0"]`, and fixed each after a CLI
  error. The snippet should show one move example (challenge with string `targets`, `point`).
- Claude tried piping JSON into `agentnet debate ... --position-file -` / `--move-file -`
  (denied by the allowlist); the snippet should say "write the file with the Write tool".
- Round 1 turn 5 and 6 were both claude's (turn 5 ended without moving), and round 2 turn 8
  and 9 were both claude's; the turn-driven loop simply re-ran the same agent, no failure.
- `Docs/agents/snippet.md` was changed after this run (exact move-file example, "write the
  file, do not pipe stdin", "no need to explore first"); not re-tested by a new real run —
  the stand-in and the next weekly run cover it.
- agy turns were single-shot (1 turn each) with no visible confusion.

### 3.H2 (after DX-2)

Three attempts. Attempts 1 and 2 were aborted by Claude before any `agentnet` command (the
restricted harness blocked the measurement); attempt 3 completed a debate and is the usable
result (verdict: improved, see the end of this subsection).

#### Attempt 1

One real round, 2026-09-25, `phase3-agents.ps1 -Harness real -OnlyRound 1` (claude -> agy), run dir
`%TEMP%\phase3-agents-ea340c45`. Run once, no retry: an agent did run, so it was not a harness
failure. Harness result: FAIL (`turn-driven loop exceeded its deadline`), 1,451 s wall clock, of
which the agent work was 5.3 s; the rest was the script polling for a debate that never existed.

| | 3.H round 1 (before DX-2) | 3.H2 round 1 (after DX-2) |
|---|---|---|
| claude invocations | several of 9 total turns (count not recorded) | 1 (turn 1), then the loop polled with nothing to run |
| claude turns (`num_turns`) | up to 30 per invocation | 2 |
| debate reached | agreed, 589 s | never started (0 moves, 0 agy turns) |
| claude cost | 1.06 USD | 0.031 USD |
| permission denials | 1-5 per invocation | 1 (the first call) |
| `bad_request` / `not_your_turn` | several JSON-shape errors | none (no agentnet command was ever run) |
| inline flags vs move files | move files, guessed shape | neither; never reached agentnet |
| read the `hint` field | n/a | no |

What happened: Claude's first and only tool call was `Get-Content NOTES.md; Get-Command *agentnet*
| Select Name,Source`, denied by the restricted headless mode (`--allowedTools` only allows
`PowerShell(agentnet *)`, `PowerShell(Start-Sleep *)`, `Write`). It then stopped with "I couldn't
start the debate", having read no file and run no agentnet command, so none of the DX-2 features
were exercised. Same failure mode as the earlier run (orientation via `Get-Content` denied), but
this time it ended the turn instead of pressing on.

Skill presence: NOT present. The harness builds `a-work` as an empty directory holding only
`CLAUDE.md` (the updated snippet, present) and `NOTES.md`; nothing copies
`.claude/skills/agentnet-debate` into it, and `--setting-sources project` only loads from that
directory. So the SKILL.md was not available to Claude in this run; only the snippet was.
Harness left unchanged. Also note the model differed: this run's claude was `claude-sonnet-5`
(modelUsage), the earlier one was recorded as claude-opus-5-5, so the comparison is confounded.

Verdict: INCONCLUSIVE, leaning worse for the harness. The 1 turn / 0.03 USD says nothing about
DX-2 (the flags, errors, and `hint` were never reached). The concrete finding is that the start
prompt tells Claude to "Read NOTES.md", the only read tool (`Get-Content`) is denied, and there is
no path to read it, so Claude can abort before its first agentnet call. Suggested follow-ups (not
done here): allow a read of NOTES.md (e.g. put its text in the prompt or allow
`PowerShell(Get-Content NOTES.md)`), copy `.claude/skills/agentnet-debate` into the fixture, then
re-run once.

#### Attempt 2 (after two harness fixes)

Harness changes in `phase3-agents.ps1` (mirrored in `phase3-agents.sh`): claude's
`--allowedTools` gained `PowerShell(Get-Content *)` (`Bash(cat *)` in the .sh) so NOTES.md can be
read; `.claude/skills/agentnet-debate/SKILL.md` is now copied into both `a-work` and `b-work`
(identical fixture for agy). So the skill file IS now present in the Claude agent's work dir
(verified in the run dir). Gates: PowerShell parse 0 errors, `bash -n` OK, `-Harness standin`
PASS (both rounds, 15 s). Run 2026-09-25 22:00, run dir `%TEMP%\phase3-agents-34ff9cca`,
`-Harness real -OnlyRound 1`. Model: `claude-sonnet-5` again (the first real run was
`claude-opus-5-5`, so still confounded).

| | 3.H round 1 (before DX-2) | 3.H2 attempt 2 |
|---|---|---|
| claude invocations | several of 9 turns | 1, then the run was stopped by hand |
| claude turns (`num_turns`) | up to 30 per invocation | 3 |
| debate reached | agreed, 589 s | never started (0 moves) |
| claude cost | 1.06 USD | 0.015 USD |
| permission denials | 1-5 per invocation | 1 |
| `bad_request` / `not_your_turn` | several | none (no agentnet command run) |
| inline flags / files / `hint` | move files, guessed shape | never reached |
| skill invoked | n/a | no evidence (see below) |

What happened: Claude read NOTES.md this time (fix 1 worked) and even formed a position (design
A), but to "start the debate" it ran `Get-Command *agentnet* ...; Get-ChildItem -Force | Select
Name`, which was denied, and it stopped saying it had "no AgentNet tool". It never tried
`agentnet --help` or `agentnet debate ...`, although CLAUDE.md (the snippet) says exactly that
the CLI is `agentnet` and to start with `agentnet --help`. I stopped the harness by hand after
turn 1 (it would only have polled ~20 more minutes for a debate that cannot start).

Likely blockers (not verified): the run passes `--tools "PowerShell,Write"`, which probably
excludes the Skill tool, so the skill file cannot be invoked even though it is on disk; and
`--allowedTools` permits only commands starting `agentnet *`, so any exploratory command is
denied and Claude gives up instead of trying `agentnet` directly. Suggested next step (not done,
needs a decision): say in the start prompt that the CLI is `agentnet` and to run
`agentnet --help` first, and add `Skill` to `--tools`.

Verdict: INCONCLUSIVE again. Cheap (0.015 USD), but nothing about DX-2 (flags, errors, `hint`,
skill) was exercised, so it is neither better nor worse than the first run.

#### Attempt 3 (last): CLI hint in the prompt, Skill tool, same model as the first run

Harness changes (`phase3-agents.ps1`, mirrored in `.sh`), Claude agents only: the start/join
prompt gets " The CLI is `agentnet`; start with `agentnet --help`." (no subcommand or flag named);
`--tools` gains `Skill` (`PowerShell,Write,Skill`; `Bash,Skill` in the .sh); `--model
claude-opus-5-5` (accepted by the CLI; the model of the first run; the run confirms
`modelUsage: claude-opus-5-5`). Gates: PowerShell parse 0 errors, `bash -n` OK, `-Harness standin`
PASS (16.5 s). Run 2026-09-25 22:02, run dir `%TEMP%\phase3-agents-7b3ca2bf`,
`-Harness real -OnlyRound 1`. Numbers come from the harness logs plus Claude's own session
transcripts (`~/.claude/projects/...phase3-agents-7b3ca2bf...-a-work/*.jsonl`).

| | 3.H round 1 (before DX-2) | 3.H2 attempt 3 (after DX-2) |
|---|---|---|
| result | PASS, agreed | PASS, agreed |
| elapsed (round) | 589.4 s | 211.2 s (script 214.4 s) |
| turns (all agents) | 9 | 6 (claude 3: start, pass, proposal; agy 3) |
| claude API turns (`num_turns`) | up to 30 per invocation | 8 + 13 + 12 = 33 (11 per invocation) |
| claude time / cost | ~1.06 USD | 111 s / 0.538 USD (0.192 + 0.185 + 0.161) |
| permission denials | 1-5 per invocation | 4 total (1 + 2 + 1) |
| agentnet errors | several JSON-shape errors | 1 `bad_request` (proposal decision over 280 code points), fixed on the next call; no `not_your_turn` |
| move style | move files, guessed shape | opening position: `--position-file` (file written with Write, correct shape first time); pass: inline `--pass`; proposal: inline `--agree "..."` |
| `hint` field | n/a | present in `agentnet debate <id> --json`; the two follow-up turns each ran `debate <id> --json` and then made exactly the move the hint named |
| skill invoked | n/a | No Skill-tool call. Claude read `.claude/skills/agentnet-debate/SKILL.md` itself with `Get-Content` in one turn (after a denied compound read); the other turns never opened it |

Remaining waste: on every follow-up turn Claude still spent 4-8 calls on orientation (`Get-ChildItem`,
reading CLAUDE.md/NOTES.md/position.json, `agentnet inbox`, `debate --help`, `sessions`) and had
compound `Get-ChildItem ...; ...` / multi-file `Get-Content a, b, c` commands denied (4 denials).
Both agents chose Design A, so no `--challenge` was needed and that flag was not exercised.

Verdict: IMPROVED, with caveats. Round time fell 589 s -> 211 s, total turns 9 -> 6, and the
JSON-shape guessing is gone (correct position-file shape first time, inline `--pass`/`--agree`,
the single error message named the limit and was fixed in one retry). Claude cost 0.54 USD for the
round vs 1.06 USD for the whole earlier run. Caveats: one run each; this debate had no
disagreement (fewer moves than a contested one); agy time is inside the totals; the earlier run
also lacked the prompt hint and the `Get-Content` allowance, so gains are not attributable to DX-2
alone; the skill file was barely used, so `hint` plus self-explaining errors (not the skill) carried
the gain. Orientation waste and denials persist and are a harness allowlist matter (compound and
multi-file commands are denied), not a DX-2 one. A future round should allow read-only listing
(`Get-ChildItem *`) or put the fixture contents in the prompt, and run several rounds (including a
contested one) before drawing quantitative conclusions.
