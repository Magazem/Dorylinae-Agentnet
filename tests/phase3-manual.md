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

<!-- filled in after the real run; see the worker's report for the run this section
     summarises -->
