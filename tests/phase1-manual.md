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
