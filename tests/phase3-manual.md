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
