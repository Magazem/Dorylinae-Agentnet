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
