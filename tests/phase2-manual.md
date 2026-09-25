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

## Own-device helper runs (ticket 2.D2, [device.md](../Docs/protocol/device.md#running-in-scope-requests))

Automated coverage (`internal/device/runner_test.go`,
`internal/daemon/device_run_e2e_test.go`) runs a test program built with `go build`.
These checks run a real repository's tests between two real machines of one person.
Setup on each OS pair: pair the two devices, share a team, run `agentnet device link` on
both (`--as controller` on the laptop, `--as helper` on the desktop) and confirm both
approval windows. Then, on the **helper**:

```
agentnet device scope @laptop --types task --repo agentnet=<abs path of a Go repo> \
    --command 'test=agentnet:["go","test","./...","-count=1"]' --env test=GOFLAGS --expires 1d
```

and on the **controller**: `agentnet request @desktop task --title t --brief b --run test`,
then `agentnet wait <session>`.

### Windows 11 helper (PowerShell 5.1, no admin)

- [ ] The scope approval window lists `test`, the repo path and the argv with `go`
      resolved to its absolute path (e.g. `C:\Program Files\Go\bin\go.exe`), JSON-quoted.
- [ ] The run needs no agent on the helper: no console window flashes up while it runs
      (`CREATE_NO_WINDOW`), and Task Manager shows `go.exe` (and its test binaries) as
      children of `agentnetd`, never of `cmd.exe`.
- [ ] The controller's `wait` gets `pass` with `exit_code` 0 and the test output tail; `go
      env GOCACHE` works with the minimal environment (no "GOCACHE is not defined").
- [ ] A command with `--timeout test=5` on a slow suite: the result is `fail`,
      `test: timed out after 5 s`, and Task Manager shows no leftover `go.exe` or test
      process (the job object killed the tree).
- [ ] A `--command` naming a `.bat` file is refused with `bad_scope`.
- [ ] Set a user environment variable `SECRET_TOKEN` before starting `agentnetd`; a command
      that prints its environment (`["powershell.exe","-NoProfile","-Command","Get-ChildItem Env:"]`)
      does not show it.

### macOS helper

- [ ] Same flow: `go` resolved to `/usr/local/go/bin/go` (or the Homebrew path), `pass`
      result on the controller.
- [ ] Timeout: `ps -ax | grep go` after the timeout result shows no leftover process
      (process group killed).
- [ ] A daemon started by launchd (not a terminal) runs the command: the minimal `PATH`
      of a launchd agent may lack `/usr/local/bin`; the absolute `argv[0]` stored at scope
      time still starts it. Note here whether the test suite itself needed `--env` names.

### Linux helper

- [ ] Same flow under a systemd user unit; `pass` result on the controller.
- [ ] Timeout on a suite that starts a background process (`sleep 600 &` in a test helper):
      `ps -eo pid,pgid,cmd` shows none left after the timeout result.
- [ ] `ps`/`/proc/<pid>/environ` of the running test shows only the minimal environment.

### Either OS pair

- [ ] `agentnet device unlink @laptop` on the helper while a long run executes: the run
      finishes and its result arrives; the next `--run` request lands in the helper's
      inbox (`agentnet inbox`) and nothing runs.
- [ ] `agentnet device scope @laptop --clear` while runs are queued: the queued ones end
      `cancelled` on the controller, the running one finishes.
- [ ] Stop `agentnetd` on the helper during a run and start it again: the controller gets
      `fail`, `test: interrupted`; queued runs end `cancelled`.
- [ ] `agentnet audit` (or the `audit_events` table) on both devices holds no command name,
      argv, repo path or output.

## 2.H headless harness run (2026-09-25, Windows 11, PowerShell 5.1, no admin)

Script: `tests/harness/phase2-agents.ps1 -Harness real` (see `tests/harness/README.md`). Each
round: A grants `fs.read` on a one-file fixture, B fetches it and returns a result (quarantined,
then released and accepted), plus a consult with one context file. Approval codes were read from
the daemons' stderr and typed on their stdin by the script only.

| Round | A (requester) | B (worker) | Result | Duration | Notes |
|---|---|---|---|---|---|
| Stand-in | Go stand-in | Go stand-in | PASS | 5-6 s | Weekly-CI harness; Linux/macOS run only in CI (the `.sh` could not run locally: no WSL, Git Bash fifos do not reach native daemons) |
| 1 | Claude Code (`claude -p`) | agy | PASS | 157 s | Claude used 39 turns, about 0.37 USD; no permission denials |
| 2 | agy | Claude Code | PASS | 163 s | Claude (B) used 37 turns, about 0.20 USD; no permission denials |

Asserted from `--json`: one review and one question request on A; both sessions `closed` /
`accepted`; the grant issued and then `revoked`; audit rows `grant.create`, `grant.fetch`,
`ws.close` and the request rows.

Agent confusion (behaviour, not AgentNet): in round 1 Claude (A) ended its summary with "agent-b
never got the read access, as far as I can tell" although B fetched the file and the round passed
(A only sees the grant as `active`, not B's fetches). Two harness fixes were needed first, both
in the script, not AgentNet: a job-based launch left `claude -p` waiting on an open stdin (now
processes are started directly and stdin is closed), and the `PowerShell(agentnet *)` allowlist
also needs `PowerShell(Start-Sleep *)` so an agent can wait. A sensitive grant makes B's result
quarantined, so A has to run `agentnet session <id> --release` (the snippet now says so).
No AgentNet bug found.

## 2.H headless harness note

The 2.H script drives this through pipes with `DORYLINAE_DEBUG=1` and is
covered by an automated e2e test
(`TestTerminalStdinApprovesAndRejects`, `internal/daemon/approval_terminal_test.go`,
ticket 2.2d); no manual step is needed for it.
