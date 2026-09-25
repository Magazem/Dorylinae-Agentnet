---
name: agentnet-debate
description: Use when a teammate invited you to an AgentNet debate, or you must start or answer one — argues a question with a teammate's agent through the `agentnet debate` CLI, ending in a signed Decision.
---

# AgentNet debate

A debate is `agentnet` CLI only — there is no separate tool or connector. No exploration
needed: run `agentnet debate <id> --json` (or `agentnet wait <id> --json`) and follow the
result's `turn`, `expect` and `hint` fields.

## The loop

1. `agentnet debate <id> --json` (or `agentnet wait <id> --json` to block until it changes).
   `turn: "you"` means it is your move; `expect` names the entry kind; `hint` (present only
   when it is your turn) already gives the exact command to run.
2. Submit that kind with inline flags — no file needed:
   - `position` (opening, or accepting an invitation): `agentnet debate <id> --claim "..."
     --argument "..." [--assumption "..."]...`
   - `move`: `agentnet debate <id> --pass` (nothing to challenge), or `agentnet debate <id>
     --challenge TARGET=ARGUMENT` (repeatable up to 3, e.g. `--challenge claim="Why not use
     jitter?"`; `TARGET` names an item of the *other* side's current position, e.g. `claim`,
     `argument`, `evidence/0`), with an optional `--revise-claim "..." --revise-argument
     "..."` to replace your own position
   - `proposal` (converge step): `agentnet debate <id> --agree "..."`
   - `answer` (converge step): `agentnet debate <id> --accept` or `--reject`
3. Repeat step 1 until `phase` is `closed` (or `broken`). A closed debate has a signed
   Decision on both sides: `agentnet decisions`, `agentnet decision <id> --md`.

If you'd rather write an entry as a file (evidence, rejected alternatives, several
remaining-disagreement points, or any text you don't want to pass as a flag), use
`--position-file`/`--move-file`/`--propose-file`/`--answer-file F` (`-` = stdin) instead —
give exactly one of a file or the inline flags per call, never both. `agentnet debate --help`
shows the exact JSON shape of every kind with a worked example. If a submission is refused
(`bad_request`, `not_your_turn`), the error message already names the expected shape and a
working example — retry with that shape rather than guessing again.

A debate carries no grants; closing one (before or after an answer) is `agentnet debate <id>
--cancel`, never `accept-result`/`release`/`discard`.

## Human constraint rule

A debate may gain a **human constraint** partway through (a rule a human adds, like "no new
dependency"): it always needs a human's approval in the AgentNet window, exactly like a
grant. **Never ask the user for a code, and never claim to enter one yourself** — no IPC
method and no CLI form ever takes a code; only a human, at the daemon's own window (or its
terminal on a headless machine), can approve it.

See `Docs/cli/debate.md` and `Docs/protocol/debate.md` for the full protocol if you need more
than this loop.
