# Phase 2 manual tests

Mark each step `[x] PASS` or `[x] FAIL` and add notes. Created by ticket 2.2d
(the approval window); later Phase 2 tickets add their own sections here
(2.2a's toast-history check moves here from its own ticket).

Automated coverage for 2.2d never pops a real window: every test drives a
fake `approval.WindowRunner` that records argv/env/stdin and answers in
process (Docs/review/23-phase2-tickets.md §2.2d). The checks below are the
only place a human actually sees the dialog.

## The approval window (ticket 2.2d, [approval.md](../Docs/protocol/approval.md#the-approval-window))

Run `agentnetd` in the foreground (desktop mode, no `DORYLINAE_APPROVAL`) on
each OS below, trigger an approval (once 2.2c/2.4/2.D1 exist, `agentnet grant
create ...`; until then, any build with a small Go program that calls
`approval.Store.Create` directly is enough), and check by eye:

### Windows 11 (PowerShell 5.1, no admin)

- [ ] A window titled `AgentNet approval a-xxxxxx` appears, `TopMost`, with the
      kind and summary text, a 6-digit input box (focused), **Approve**
      (disabled until 6 digits) and **Reject**.
- [ ] The desktop toast shows a code in its **title** (`AgentNet code NNNNNN
      for approval a-xxxxxx`) and the summary in the body.
- [ ] Typing the code from the toast and clicking Approve performs the
      action; the window closes and a new toast without a code says
      "Approved: …".
- [ ] A wrong 6-digit code reopens the window with "Wrong code, N attempts
      left"; the 3rd wrong code rejects instead of reopening.
- [ ] Clicking Reject rejects immediately (no code needed).
- [ ] Pressing Enter or Esc in the window does nothing (no default/cancel
      button).
- [ ] Typing immediately after the window appears (within ~1 s) does not
      reach the input box (the focus/input guard).
- [ ] `agentnet approve --open <id>` on an already-open window does nothing
      new (no second window); on a dismissed one, it reopens the window.
- [ ] Stopping `agentnetd` (Ctrl+C) closes any open approval window.
- [ ] **Toast history removal (moved from ticket 2.2a):** after the approval
      is decided (or expires), open Windows' Notification Center and confirm
      the AgentNet approval toast is gone from the history, not just
      dismissed from the screen.
- [ ] Running `agentnetd` from a Task Scheduler task in session 0, or over an
      RDP/SSH logon with no interactive desktop, reports `approval_window:
      "missing"` in `status` and the approval fails `approval_unavailable`
      rather than silently doing nothing.

### macOS

- [ ] `osascript`'s `display dialog` appears in front of other windows (the
      `activate` call), with the kind/summary text and an input field, and
      **Reject**/**Approve** buttons (no default/cancel button, so
      Return/Esc do nothing).
- [ ] A non-ASCII peer name in the summary (e.g. `Böb Müller`) renders
      correctly (checks `system attribute` UTF-8 decoding).
- [ ] The code is shown in Notification Center, never in the dialog itself.
- [ ] Approve/Reject/dismiss (closing the dialog) all behave as on Windows.
- [ ] `giving up after <seconds>` closes the dialog unanswered at `expires`
      and the approval expires.

### Linux (zenity, then kdialog if installed)

- [ ] `zenity --entry` appears with the title and text set, an **OK** and an
      extra **Reject** button.
- [ ] The code is shown only in the desktop notification (GNOME/KDE
      notification popup), never in the zenity window.
- [ ] A summary starting with `-` (e.g. a peer named `--evil`) is shown as
      literal text, not treated as an option (review 29 M3: every value is a
      single `--opt=value` argument).
- [ ] A summary containing `<b>bold</b>` or `&amp;` markup is shown escaped
      (literal angle brackets/ampersand), not rendered — unless the manual
      check separately confirms `--no-markup` works with `--entry` on this
      zenity version, in which case escaping may be dropped later.
- [ ] With `kdialog` only (zenity absent): an `--inputbox` appears; Reject is
      not available in the dialog, use `agentnet approve --reject` instead.
- [ ] `ps aux` (or `/proc/<pid>/cmdline`) while the dialog is open shows the
      title/summary in argv (accepted, OQ-2.2d-1) but never the code.
- [ ] With `/proc` mounted `hidepid=2`, another local user cannot see the
      summary in `ps`/`/proc` at all.
- [ ] A daemon started as a systemd user unit (`WantedBy=default.target`,
      no `DISPLAY` at start) still opens the window once the desktop session
      exports `DISPLAY`/`WAYLAND_DISPLAY` (review 29 M1: read from the
      systemd user manager's `Environment`).
- [ ] No `zenity`/`kdialog` installed, or no `$DISPLAY`: `status` reports
      `approval_window: "missing"` with a message naming the fix (install
      zenity/kdialog, or use terminal mode).

## Headless / terminal mode (ticket 2.2d, [approval.md](../Docs/protocol/approval.md#headless-machines))

- [ ] `agentnetd` started in a real terminal (not through `DORYLINAE_DEBUG`)
      with `DORYLINAE_APPROVAL=terminal` writes
      `AgentNet approval a-xxxxxx: <summary>. Code NNNNNN. Type "a-xxxxxx
      NNNNNN" to approve or "reject a-xxxxxx" to reject.` to its stderr, and
      typing that line on its stdin approves.
- [ ] Redirecting `agentnetd`'s stdin or stderr to a file (no
      `DORYLINAE_DEBUG`) refuses to start with a clear exit-2 message.
- [ ] `agentnet approve --open <id>` against a terminal-mode daemon fails
      `bad_request` ("answer on the daemon's terminal").

## 2.H headless harness note

The 2.H script drives this through pipes with `DORYLINAE_DEBUG=1` and is
covered by an automated e2e test
(`TestTerminalStdinApprovesAndRejects`, `internal/daemon/approval_terminal_test.go`,
ticket 2.2d); no manual step is needed for it.
