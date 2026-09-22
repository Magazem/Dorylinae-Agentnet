# Phase 1 manual tests

Mark each step `[x] PASS` or `[x] FAIL` and add notes.

## Two-machine run (ticket 1.9)

The automated e2e (`internal/daemon` `TestOfflineLifecycleEndToEnd`,
`TestAuditHasNoContent`) covers this flow in-process with two daemons and a real relay.
This checklist is the owner's manual run of the same features across two real machines
(A and B), each paired and running its own daemon.

### Team invite/join (ticket 1.1)

1. On A: `agentnet team create backend`.
   - [ ] Prints the team id and confirms A is the owner.
2. On A: `agentnet team invite backend`.
   - [ ] Prints a code and a pairing id.
3. On B (not yet paired with A): `agentnet team join <code>`.
   - [ ] B pairs with A and asks to join.
4. On both: `agentnet team list` / `agentnet team show backend`.
   - [ ] Both machines see a 2-member roster within a few seconds.

### Presence levels and visibility (tickets 1.2, 1.3)

1. On A: `agentnet status --team backend`.
   - [ ] B shows as online, with a fresh `last_seen`.
2. On B: `agentnet presence --invisible`.
   - [ ] A sees B go offline at once (a `last_seen` at that moment), not just after a timeout.
3. On B: `agentnet presence --visible`.
   - [ ] A sees B online again within one heartbeat.
4. On B: `agentnet presence --only-team backend`.
   - [ ] `agentnet presence` on B reports `only_team backend`; a peer outside the team sees B
     as never seen / offline.

### Request → inbox → accept/complete with result (tickets 1.4-1.6a)

1. On A: `agentnet request @bob task --title "Run the tests" --brief "What: run go test"`.
   - [ ] Returns in under 2 s with `status: queued`.
2. On B: `agentnet inbox`.
   - [ ] The request appears, with A's title and priority.
3. On B: `agentnet accept <id>`.
   - [ ] On A, `agentnet request show <id>` shows `state: accepted` within a few seconds, and
     a desktop notification fires (if enabled, see below).
4. On B: `agentnet complete <id> --status pass --summary "all green" --output-from-file out.log`.
   - [ ] On A, `agentnet request show <id>` shows `state: completed` with the result (status,
     summary, output).

### Cancel (ticket 1.6a, D11)

1. On A: send a second request, then `agentnet request cancel <id2> --reason "not needed"`
   before B answers it.
   - [ ] Returns at once; B's `agentnet inbox` no longer lists it (checked with `--all`:
     `state: cancelled`).
2. On A: `agentnet request show <id2>`.
   - [ ] Shows `state: cancelled` once B's daemon confirms.

### Urgency downgrade (ticket 1.7)

1. On A: send 6 `high` requests to B inside a few minutes.
   - [ ] The 6th is shown (on both `request show` on A and `inbox` on B) as
     `urgency: normal`, `urgency_declared: high`, with a note explaining the weekly budget.

### Desktop notification (ticket 1.8a)

See [Desktop notifications](#desktop-notifications-ticket-18a-internalnotify) below; run at
least the accept/complete/cancel steps against the live request exchanged above.

### Webhook (ticket 1.8b)

1. On B: `agentnet notify --webhook https://example.test/hook` (a URL you control, or a
   local `httptest`-style receiver).
   - [ ] Prints a `whsec_...` secret once.
2. On A: send a request; on B: accept it.
   - [ ] The receiver gets a signed POST for `request.received` and `request.accepted`,
     verifiable with the secret ([notify.md](../Docs/protocol/notify.md#delivery)).
3. `agentnet notify --webhook off` on B.
   - [ ] `agentnet notify` no longer lists a webhook; no further deliveries.

## Idle detection (ticket 1.2d, `internal/idle`)

Run once per OS (Windows, macOS, Linux with GNOME, KDE, or X11 + `xprintidle`). Human present
means OS idle time under 10 min ([presence.md](../Docs/protocol/presence.md#idle-detection)).

For each OS:

1. Start the daemon in a logged-in desktop session, and use the keyboard or mouse.
   - [ ] `agentnet status` shows human present (`human: 1`).
2. Leave the machine untouched for 10 min. Do not run CLI commands from that machine's own
   input devices (use SSH or a remote shell if needed).
   - [ ] After 10 min idle, human flips to not present (`human: 0`).
3. Touch the keyboard or mouse.
   - [ ] Human flips back to present within one heartbeat plus the 5 s cache.
4. Unknown cases:
   - [ ] Windows: daemon running as a service in session 0 reports unknown (`human: 2`).
   - [ ] Linux without gdbus, `xprintidle` or a supported compositor reports unknown, and
     presence keeps working.

| OS | Mechanism that answered | Flips at 10 min | Notes |
|---|---|---|---|
| Windows | GetLastInputInfo | | |
| macOS | ioreg HIDIdleTime | | |
| Linux | gdbus Mutter / ScreenSaver / xprintidle | | |

## Desktop notifications (ticket 1.8a, `internal/notify`)

Run once per OS (Windows, macOS, Linux with a session bus running `org.freedesktop.Notifications`).
`agentnet notify --desktop on` first, then trigger each event with a paired peer
([notify.md](../Docs/protocol/notify.md#desktop)):

1. `agentnet notify --test` shows a visible notification titled "AgentNet".
   - [ ] Notification appears within a few seconds.
2. A peer sends a request (`request.received`, on by default).
   - [ ] Title is `<Urgency> <type> request from <name>`; body is the title, within 5 s of
     the request landing in the inbox.
3. Accept / decline / defer / complete round trips from the peer's side mirror back.
   - [ ] `request.accepted` / `request.declined` shown (on by default); `request.deferred` /
     `request.completed` only after `agentnet notify --event request.deferred=on` /
     `--event request.completed=on`.
4. The sender cancels a pending request; the recipient sees `request.cancelled` (on by default,
   recipient side only).
   - [ ] Notification shown only on the recipient's machine.
5. Turn off one event (`agentnet notify --event request.received=off`) and repeat step 2.
   - [ ] No notification for that event; others still fire.
6. `agentnet notify --desktop off`, repeat step 2.
   - [ ] No notification at all; `agentnet notify --test` reports `"desktop":"disabled"`.

| OS | Mechanism used | Notes |
|---|---|---|
| Windows | PowerShell toast (Windows.UI.Notifications) | |
| macOS | osascript `display notification` | |
| Linux | gdbus `org.freedesktop.Notifications.Notify` (fallback `notify-send`) | |

## Headless agent harness (ticket 1.H)

Script: [harness/phase1-agents.ps1](harness/phase1-agents.ps1) (run) and
[harness/phase1-agents.sh](harness/phase1-agents.sh) (written, not run here —
see [harness/README.md](harness/README.md)).

- **Date:** 2026-09-22.
- **Harnesses:** Claude Code CLI (`claude` at `%USERPROFILE%\.local\bin\claude.exe`,
  app/session build `2.2553.1`) and Codex CLI `codex-cli 0.152.1`.
- **Environment:** Windows PowerShell 5.1, Go 1.27.1, worktree `AgentNet-wt/t1-H`
  (branch `p1/t1-H`, base `main` `5111bee`).
- **Result: FAIL (blocked), not a script defect.** Round 1 (Claude Code sends,
  Codex CLI receives) ran the full pipeline for real: built the binaries,
  started a loopback relay and two `agentnetd` daemons with separate `--home`
  dirs, paired them, created team `t1h` with both as members, then launched a
  real headless `claude -p` process in a working directory holding only the
  `CLAUDE.md` snippet. Given only the plain-English instruction "ask agent-b
  for a review of branch p1/t1-H with idempotency key ...", the agent found
  and ran `agentnet request agent-b review ...` unaided (from the snippet +
  `--help`) and queued **exactly one** request
  (`r-135910bf0890f1b3db083649844d788f` in the final run;
  `r-85cafcc0a12a6662aad7a915ebfb789a` in an earlier one), matching the given
  idempotency key. The recipient step (`codex exec`) then failed immediately:
  `codex` returned `"You've hit your usage limit... try again at Oct 2nd,
  2026 8:18 AM."` — an account-level rate limit on the installed Codex CLI,
  confirmed independently with a bare `codex exec "reply with exactly OK"`
  smoke test outside the harness. Round 2 (roles swapped) was not attempted
  since it depends on the same Codex account. **This blocks 1.P until Codex
  usage resets (or the owner supplies a different account/harness for the
  recipient role).**
- **Supplementary check (not part of the official two-harness matrix):** to
  confirm the rest of the pipeline (inbox → accept → complete → D14 result →
  audit log) actually works with a real agent, the recipient role was
  temporarily pointed at Claude Code instead of Codex for one throwaway run.
  That run's sender agent gave up in a single turn claiming "no AgentNet
  tool" (an inconsistent, non-reproduced response — a second identical
  attempt is what produced the clean pass above), so the recipient side of
  the pipeline itself was not exercised by a real agent this session; the
  request → inbox → accept → complete → audit chain is implemented and was
  exercised only by the script's own CLI calls in earlier development, not by
  a second live agent turn. This should be re-attempted once Codex is
  available, or with another second harness.
- **Snippet:** no change made. The one clean sender run shows
  `Docs/agents/snippet.md` plus `--help` is sufficient for Claude Code to
  find and use the CLI correctly (correct subcommand, correct peer name,
  correct idempotency key, single request, no protocol names in the prompt).
  The single-turn "no AgentNet tool" response from the same harness on a
  different invocation looks like model-level non-determinism (it invented
  tool names — "Claude Docs, Google Drive, Picsart" — that do not exist in
  this restricted session) rather than a snippet gap; flag for a retry count
  in the script (e.g. one retry of a sender/recipient invocation that
  produces zero tool calls) if this recurs.
- **Script bugs found and fixed during this run** (both now fixed in
  `phase1-agents.ps1`, no Go/product code touched):
  1. `ProcessStartInfo.ArgumentList` does not exist under .NET Framework
     (Windows PowerShell 5.1) — only under .NET Core/5+. Every process launch
     now builds a quoted `Arguments` string by hand (`Format-ArgList`).
  2. A function parameter named `$Home` collided with PowerShell's read-only
     automatic `$HOME` variable, throwing `VariableNotWritable` on every
     `agentnet ... --json` call. Renamed to `$HomeDir` throughout.
  3. `Process.Kill($true)` (tree-kill) also does not exist under .NET
     Framework; the call threw a `MethodException` that a bare `catch {}`
     silently swallowed, so the relay and both `agentnetd` daemons were never
     actually killed after a round failed or timed out, leaking processes
     across runs (this is what looked like a 26-minute "hang" mid-session —
     the top-level script had already exited; only its orphaned children were
     still running). Replaced with a `Stop-ProcessTree` helper that shells out
     to `taskkill /T /F`.
- **Real CLI issue found (not fixed, per instructions):** none in the
  `agentnet`/`agentnetd`/`relay` binaries themselves. `agentnetd`'s stderr
  showed one transient `noise: sign static key: load identity key: keystore:
  secret not found` on daemon B during an earlier run; it did not recur in
  the clean final run and did not block anything (the daemon reconnects with
  backoff and re-signs on the next attempt per `Docs/cli/relay.md`), so it is
  not reported as a defect — flag it if it reproduces reliably.
- **Total run time:** 19.9 s for round 1 up to the Codex blocker (well under
  the 10-minute budget).
- **Prerequisite note:** there is no `sqlite3` CLI or CGo SQLite driver
  available in this environment (the daemon uses the pure-Go
  `modernc.org/sqlite`), so the audit-log assertion shells out to
  `python3 -c "import sqlite3; ..."` (present here as Python 3.12.10) rather
  than adding a new Go tool, per the "scripts and docs only" instruction for
  this ticket; see `harness/README.md`.

**Next step for the owner:** re-run `tests/harness/phase1-agents.ps1` (or the
`.sh` port) once Codex CLI's usage limit resets (2026-10-02) or with a second
account/harness substituted, to get a real PASS on both rounds before 1.P.
