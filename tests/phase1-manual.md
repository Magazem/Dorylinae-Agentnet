# Phase 1 manual tests

Mark each step `[x] PASS` or `[x] FAIL` and add notes.

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
