# Human approval

Status: **draft** for Phase 2 (tickets 2.2a and 2.2d, [../review/23-phase2-tickets.md](../review/23-phase2-tickets.md)).
2.2d (owner decision D19) replaces code entry through the CLI with the daemon-owned
[approval window](#the-approval-window).
Used by grants ([grant.md](grant.md)), the sensitive-grant release
([work-session.md](work-session.md#quarantine-24)), `accept-result --human`, and the
own-device link and scope ([device.md](device.md)). Change this document first.

## Why

The plan requires a "human approval prompt" for grants, and D13 requires the device link to
be "confirmed on BOTH devices". Both are decisions an **agent must not be able to take on
its own**, even though the agent runs `agentnet` with the user's full rights. The daemon
cannot tell a human from an agent over IPC: the caller is the same OS user either way. So an
approval needs a second channel that a harness agent normally does not see.

Phase 2 uses the **desktop notification** as that channel: the daemon shows a one-time code
only there. The human types the code into the **approval window**, which the daemon itself
opens and reads. No IPC method and no CLI command accepts a code, so an agent never handles
one, and a code phished through a fake window is useless ([Spoofing](#spoofing-and-stray-input)).
On a headless machine the daemon's own terminal takes the place of the window
([Headless machines](#headless-machines)).

## Threat model

| Attacker | Goal | Control |
|---|---|---|
| A prompt-injected local agent that uses AgentNet's interface (IPC, CLI) | Issue a grant, release a quarantine, link a device or widen a helper scope without the human | It cannot submit a code: no IPC method or CLI command takes one. Codes are read only from the daemon's own approval window (desktop) or the daemon's own stdin (terminal mode). It cannot see the code either: the code is never in an IPC result, a CLI output, the audit log, the daemon log, a webhook, SQLite (not even a hash: the check value lives only in the daemon's memory), any process's argument list or a file. Rate limits and the daily wrong-code cap still apply ([Object](#object)) |
| The same agent showing a **fake** approval window or chat prompt | Phish the code from the human | A captured code is useless: the agent has nowhere to submit it. The notification tells the human to type the code only into the AgentNet window with the matching id ([Spoofing](#spoofing-and-stray-input)) |
| Another local OS user | Read the code as it passes to the notifier | The code never appears in a process's argument list (world-readable through `/proc` and `ps`); it reaches the notifier in-process (Linux) or through the environment (macOS, Windows), which only the same user can read ([Delivering the code](#delivering-the-code)). On Linux the approval window's title and summary (never the code) are in the dialog's argv; this is accepted (OQ-2.2d-1, [The approval window](#the-approval-window)) |
| A remote peer | Trigger approvals to annoy or to phish the human | Only local IPC callers create approvals; a peer's mail never does. The notification names the **local** action and the **peer's name and fingerprint** |
| Malware running as the user | Anything | **Out of scope.** It can read the screen, the keystore file fallback and the database. The approval is a gate against confused-deputy agents, not a sandbox |
| A human approving without reading | A too-broad grant | The notification and the approval window state action, resource, peer and expiry in plain words |

**Boundary, stated plainly.** The approval stops an agent that acts through AgentNet's own
interface. It does **not** stop an agent that runs arbitrary programs as the user and sets
out to defeat it: such an agent can read the OS notification history (on Windows through the
documented toast-history API or `wpndatabase.db`; on macOS and Linux through the
notification daemon's store), take a screenshot, drive the UI (send keystrokes or clicks to
the approval window, or cover it with a window of its own), read or write the daemon's
SQLite database and the keystore file fallback, or stop the daemon and restart it with
`DORYLINAE_APPROVAL=terminal`, read the code from its stderr and type it on its stdin. Each of these is a
deliberate attack on the user's account, the same class as malware. The mitigation is
**harness confinement**, documented in `Docs/agents/snippet.md` and the CLI help: an agent
that works with AgentNet should have no read or write access to the config dir
(`DORYLINAE_HOME`), must not start, stop or configure `agentnetd`, and should not be given
screenshot, accessibility or notification-history tools (for Claude Code: deny rules for
those paths and commands). OD-P2-2 records this boundary. On a machine without a desktop,
see [Headless machines](#headless-machines).

## Object

```
approval = {id: "a-" + 32 hex, kind, subject, summary, created, expires,
            attempts, state: pending|approved|rejected|expired, decided}
```

- `kind`: `grant`, `grant_policy`, `release`, `accept_result`, `device_link`,
  `device_scope`. The action waits in the owning table in a `pending_approval` state that
  references the approval id.
- `code`: 6 decimal digits from `crypto/rand` (uniform, leading zeros kept). The daemon keeps
  only a check value **in memory**:
  `code_mac = HMAC-SHA256(approval_key, "dorylinae-approval-v2\n" ‖ id ‖ "\n" ‖ code)`, where
  `approval_key` is 32 bytes from `crypto/rand` generated at daemon start and never written
  anywhere. **No code material is stored in SQLite**: a 6-digit code under a stored hash is
  found by trying all 10⁶ codes in about a second (review 24, H1). The plaintext code lives
  only in the notification call and is then dropped. A daemon restart loses `approval_key`,
  so every `pending` approval becomes `expired` at start (audit `approval.reject {reason:
  "expired"}`).
- `expires` = `created + 10 min`. At most **3** attempts; the 3rd wrong code sets `rejected`.
- At most **5 pending approvals** per daemon, and at most **20 created per hour**. Beyond
  that `approval_limit`.
- At most **10 wrong codes per rolling 24 h** per daemon (persisted, so a restart does not
  reset it). The 10th sets every pending approval `rejected` and refuses new approvals with
  `approval_locked` until the window has room again, and shows a desktop notification
  ("AgentNet: approval codes were guessed wrongly 10 times; approvals are locked for up to
  24 h"). Without it, 20 approvals an hour × 3 guesses is about a 4 % chance a month for a
  looping agent; with it, under 0.4 % a year.

Vector (`approval_key` = bytes `00…1f`): `id = a-0123456789abcdef0123456789abcdef`,
`code = 482913` →
`code_mac = 8580a34986f6affcb96bbee33cc575eea83ac9b65c8e050b1ff64850680bb5ce`.

Comparison of the MAC is constant time.

### Delivering the code

The code is shown by the desktop notifier of [notify.md](notify.md#desktop). For approvals,
the text must never be in a child process's **argument list** (on Linux `/proc/<pid>/cmdline`
and on macOS `ps` show every process's arguments to every local user):

- **Linux:** the daemon calls `org.freedesktop.Notifications.Notify` in-process on the
  session bus with `github.com/godbus/dbus/v5` (pure Go, already in the module graph through
  `go-keyring`), not through `gdbus` or `notify-send`. No fallback for approvals: if the
  in-process call fails, the approval is `approval_unavailable`.
- **macOS:** title and body go through the environment of `osascript` (read with
  `system attribute`), as Windows already does; the script is fixed text.
- **Windows:** unchanged transport (environment, fixed script). The toast carries a `tag`
  (the approval id) and group `agentnet-approval`, and `ExpirationTime` = `expires`; when the
  approval is decided or expires, the daemon removes it from the notification history
  (`ToastNotificationHistory.Remove(tag, group, AUMID)`), which shortens, but does not close,
  the window in which a same-user program can read the history.

**Text of the notification (2.2d).** The code goes in the **title**, with the approval's
short tag (`a-` + the first 6 hex of the id):
`AgentNet code 482913 for approval a-012345`. The body is the summary followed by the fixed
sentence `Type this code only into the AgentNet approval window a-012345. AgentNet never asks
for it in a terminal, a chat or an agent.` Putting the code in the title keeps a decoy code in
peer text (review 26, N4) away from the place the real one is shown.

## The approval window

(Ticket 2.2d, D19.) For every approval in desktop mode the daemon starts a small dialog
process of its own on the user's desktop. The dialog shows the short tag, the kind, the
summary (action, resource, peer name and fingerprint, expiry) and a 6-digit input box, with
**Approve** and **Reject** buttons. The human's answer comes back to the daemon over the
dialog's **stdout pipe**, which only the daemon holds. Nothing else can submit a code.

Common rules, all platforms:

- **Fixed program, fixed script.** The dialog program is resolved from a fixed absolute path,
  never from `PATH`. The script (where there is one) is constant text in the binary. Summary,
  title and tag reach it through the **environment** (the same user can read it; other users
  cannot), never interpolated into the script. The code is **never** sent to the dialog.
  It exists only in the notification. No environment variable, flag, config key or
  `DORYLINAE_DEBUG` selects a different program, script or answer source: tests swap the
  runner only inside Go test code. A desktop-mode daemon **never reads its stdin**
  (review 29, M6).
- **Answer format.** The dialog writes one line to stdout and exits: `approve <digits>`,
  `reject` or `dismiss`. The daemon reads at most 256 bytes. Anything else counts as
  `dismiss`. An `approve` whose value is not exactly 6 ASCII digits is **not** counted as a
  wrong code: the daemon reopens the window once with "Enter the 6-digit code from the
  notification". A 6-digit value is checked like any code (3 per approval, 10 per 24 h,
  unchanged). An answer that arrives before the notification has been shown counts as
  `dismiss`. zenity and kdialog cannot print this line themselves, so on Linux the daemon
  builds it from the exit status: 0 with text → `approve <text>`; zenity's extra button
  (stdout `Reject`, exit 1) → `reject`; any other exit, including zenity's timeout (5) →
  `dismiss`. A typed text `Reject` + OK is therefore an `approve` with a malformed value
  (review 29, L6).
- **Ready check.** Creating an approval opens the window **before** the code is generated or
  the notification is shown. If the window does not become ready (below), nothing is stored,
  nothing is audited, and the method fails with `approval_unavailable`, as a failed notifier
  does today. The waiting row is dropped (review 26, N5). The same applies if the window is
  ready but **showing the notification then fails**: the daemon kills the window first, so
  there is never a window without a code or a code without a window (review 29, M4).
- **Lifetime.** The dialog carries its own timeout equal to `expires`. The daemon also kills
  it when the approval is decided, expires (a per-approval timer, not only the lazy sweep),
  hits the lockout, or when the daemon stops. At most one window per approval exists at a time,
  so there are at most 5 windows (the pending limit). Windows: the dialog runs in a Job
  object with `KILL_ON_JOB_CLOSE` (`golang.org/x/sys/windows`, no cgo), so it dies with the
  daemon. The process is assigned right after `Start`, and a failed assignment kills it and
  counts as not ready. It is started with `CREATE_NO_WINDOW`, so no console flashes up. Linux: `Pdeathsig = SIGKILL`. It fires
  when the starting OS **thread** exits, so the window is never started from a goroutine
  that calls `runtime.LockOSThread`. macOS: an orphan closes itself at `expires`. An
  orphan's answer goes nowhere, and after a restart every pending approval is `expired` anyway.
- **Locking.** The ready wait can take seconds, so it must not run while `Store.mu` is held
  (review 26, L1). Reserve the pending slot under the lock, open the window outside it, then
  finish or release the slot under the lock.
- **Outcome.** On `approve` with the right code the daemon confirms exactly as described in
  [Flow](#flow) step 3. After every answer it shows a short desktop notification without a
  code: "Approved: …", "Rejected: …", or the waiting action's error (for example "Not
  approved: the session is no longer open"). A wrong code reopens the window with "Wrong code,
  N attempts left". A `Perform` error, which leaves the approval pending (review 26, N1),
  reopens it with "Could not complete, try again or reject". `dismiss` (window closed)
  leaves the approval pending until it expires. `agentnet approve --open` shows the window again.

Per platform (no cgo, no admin):

| OS | Dialog | Ready when | Notes |
|---|---|---|---|
| Windows | `<system dir>\WindowsPowerShell\v1.0\powershell.exe -NoProfile -NonInteractive -STA -WindowStyle Hidden -Command <fixed script>`, where `<system dir>` comes from `GetSystemDirectory` (`x/sys/windows`), not from `%SystemRoot%` or `PATH`. The approval toast and its history removal switch to the same absolute path (today `powershell.exe` is found through `PATH`). PowerShell 5.1 with WinForms: a `Form` (`TopMost`, fixed size, no minimise, cascaded so that several windows do not stack exactly), `Label`s whose `.Text` is set from the environment (base64 UTF-16, like the toast), a `TextBox` limited to 6 digits and focused first, **Approve** (enabled only with 6 digits) and **Reject**. No `AcceptButton`/`CancelButton`, so Enter and Esc do not click them. The input box ignores keystrokes for **1 s** after each `Activated` event, not only after `Shown`: a background process usually cannot take the focus, so the window mostly gets it later from a click | The form's `Shown` event writes `ready` to stdout. Wait at most 10 s | Under Constrained Language Mode (AppLocker/WDAC) WinForms is blocked: the result is `approval_unavailable`. The toast has the same limit. A daemon outside an interactive session (session 0, an SSH logon) would fire `Shown` on a desktop nobody sees, so the check first requires `ProcessIdToSessionId` ≠ 0 and the process's window station to be `WinSta0`; otherwise the window is `missing` |
| macOS | `/usr/bin/osascript` with a fixed script: `activate` (osascript itself, no TCC permission; it brings the dialog to the front), then `display dialog (system attribute "AGENTNET_A_BODY") with title (system attribute "AGENTNET_A_TITLE") default answer "" buttons {"Reject", "Approve"} giving up after <from env>`, with no `default button` and no `cancel button`, so Return and Esc click nothing. The script turns the record into the one-line answer (`gave up` → `dismiss`) | Still running after 1.5 s, or already exited with a valid answer. An early non-zero exit is failure | `display dialog` inside osascript needs no Automation (TCC) permission. It cannot delay input. Only a daemon running in the user's GUI session (a LaunchAgent) can show it. The manual check includes a non-ASCII peer name (`system attribute` decoding) |
| Linux | `zenity --entry --title=… --text=… --extra-button=Reject --timeout=<s>`, else `kdialog --title=… --inputbox=…`. Every value is one `--opt=value` argument, and peer text is never a positional argument, so a summary starting with `-` cannot become an option. Text is markup-escaped (`& < >`) for both tools; zenity's `--no-markup` is used instead only if the manual check shows that it is accepted with `--entry` on the zenity versions of Ubuntu 22.04 and 24.04. The program is the first of `/usr/bin/<tool>`, `/bin/<tool>`, `/run/current-system/sw/bin/<tool>` (NixOS) that exists, is owned by root and is not group- or world-writable; never `PATH` (a Flatpak-only install is not found). `DISPLAY`, `WAYLAND_DISPLAY`, `XAUTHORITY` and `XDG_RUNTIME_DIR` are taken from the daemon's environment or, if missing, from the systemd user manager's environment, read in-process at each opening (property `Environment` of `org.freedesktop.systemd1.Manager` over godbus): a user unit started at `default.target` often starts before the desktop exports them | As macOS | **Title and summary are in argv** (neither tool reads its text from stdin or the environment), so another local user can read them through `/proc/<pid>/cmdline` unless `/proc` is mounted `hidepid=2`. The code is never in them. This is **accepted and documented** (owner, OQ-2.2d-1 = (a)): the summary is local metadata, not a secret. `Docs/cli/approve.md` and the Linux install notes say so and name `hidepid=2` as the fix on shared machines. kdialog has no Reject button: reject with `agentnet approve --reject` |

**No window helper on Linux** (neither zenity nor kdialog, or no display): approvals are
`approval_unavailable`, exactly as when the notifier fails. There is **no automatic
fallback**. The alternative is the install-time terminal mode ([Headless
machines](#headless-machines)), with its documented weaker security. In desktop mode `status`
also reports `approval_window: "ok" | "missing"`, a check that the helper program exists and
a display is set, without opening a window. That way the user learns about it before the
first grant. The `approval_unavailable` message and `status` name the fix in plain words:
"install zenity (or kdialog)", "no desktop session", or "PowerShell/WinForms blocked by policy".

The window also says where the code is: "The code is in the AgentNet notification for
a-012345. If notifications are silenced (Do Not Disturb, Focus Assist), open the
notification centre." (review 29, L5)

### Spoofing and stray input

- **Spoofing.** An agent can draw a window that looks exactly like this one. That is
  accepted: a code typed into a fake window cannot be submitted anywhere, and it is bound to
  its approval id by the MAC. The notification tells the human where the code belongs. An agent
  that covers the real window or types into it is using the UI directly, which is outside the
  boundary ([Threat model](#threat-model)).
- **Several pending approvals.** One window per approval, each titled with its short tag. A code
  typed into the wrong approval's window is a wrong code for that approval (attempt used),
  never a confirmation of the other one.
- **Focus stealing and stray keystrokes.** The window may appear while the human is typing
  elsewhere. A stray keystroke can at most dismiss the window, click Reject, or submit a wrong
  6-digit value. It can never approve, because approving needs the code from the notification.
  Windows also drops input for 1 s and has no default buttons. On macOS and Linux the dialogs
  cannot do this, and the code is the only guard.

## Flow

1. An IPC method that needs approval (for example `grant_create`) validates everything
   first, stores the action as `pending_approval` and creates the approval. Desktop mode
   opens the [approval window](#the-approval-window) and then shows the notification with the
   code. Terminal mode writes both to the daemon's stderr ([Headless machines](#headless-machines)).
   Peer-supplied text in the summary goes through `notify.Clean`
   ([notify.md](notify.md#text-and-sanitising)).
2. The method returns at once: `{"approval": {"id", "kind", "summary", "expires",
   "state": "pending"}}`. The CLI prints `Approval a-012345 pending: approve it in the
   AgentNet window on your desktop (code in the notification)`, or in terminal mode `… on
   the daemon's terminal`, and exits 0 with `state: pending`. It never prompts for a code.
   The outcome shows in the waiting object's own view (for example `agentnet grants`).
3. The human enters the code in the window, or on the daemon's terminal in terminal mode. The
   daemon checks state, expiry and the code. On success, in one transaction, it sets `approved`, **re-checks every
   precondition of the waiting action against the current state** (for a grant: the session
   is still `open` and the peer still paired and allowed by D5; for a release: still
   `quarantined` in the same round; for a device link: no conflicting link appeared), and
   performs the waiting action. A precondition that no longer holds sets the waiting object
   `rejected`/dropped (reason `precondition`). A wrong code uses one attempt. An expired
   approval cannot be confirmed. The human learns the outcome from the window or the terminal
   ([The approval window](#the-approval-window), Outcome).
4. Reject in the window, `reject <id>` on the daemon's terminal, or `agentnet approve
   --reject <a-id>` (no code needed) rejects it, and the waiting action is dropped. Any caller
   may reject: rejecting only removes access.

`approval_list` returns pending approvals (never codes) so a human can see what is waiting.

Approvals are never created by peer mail, never forwarded, and never cross machines. The two
confirmations of a device link are two local approvals, one on each device
([device.md](device.md#link-flow)).

## Headless machines

If the desktop notifier is disabled or fails (`notify_test` → `failed`), or the approval
window cannot be shown, approvals are
`approval_unavailable`, and the actions that need them fail unless a
[policy](grant.md#policies) covers them. A policy itself needs an approval, so a headless
machine needs a different channel. Phase 2 offers exactly one, chosen at install time:

- `agentnetd` started with the environment variable `DORYLINAE_APPROVAL=terminal` writes the
  approval summary and code to **its own stderr**, never to the log file. It is meant for a
  daemon run in the foreground in a terminal the human watches (`agentnetd` in tmux, for
  example). It cannot be set over IPC, and `status` reports `approval: "terminal"` so it is
  visible. This is weaker (anything that can read that terminal can read the code) and is
  documented as such. See OD-P2-3.
- **stderr must be a terminal.** In `terminal` mode the daemon refuses to start (exit 2,
  clear message) unless its stderr is a terminal, so a service manager can never route codes
  into a journal or log file. The one exception is `DORYLINAE_DEBUG=1` (test harnesses such as
  2.H read stderr through a pipe); `status` then reports `approval: "terminal-debug"`.
- **Visible switch.** A start in `terminal` mode writes audit `approval.mode {mode}` and, if a
  desktop notifier works on the machine, shows "AgentNet daemon started with terminal
  approvals", so a restart into this mode by someone else is noticed. It does not stop an
  agent that can restart the daemon ([Threat model](#threat-model), boundary).
- **Code entry on the daemon's own stdin (2.2d).** In terminal mode there is no window, and the
  code is typed back into the **daemon's own stdin**, never into a CLI argument or an IPC
  call. stdin must be a terminal too, under the same rule and the same `DORYLINAE_DEBUG=1`
  exception as stderr. For each approval the daemon writes to stderr:
  `AgentNet approval a-012345: <summary>. Code 482913. Type "a-012345 <code>" to approve or
  "reject a-012345" to reject.` It reads lines of at most 128 bytes. `<tag> <code>` confirms
  and `reject <tag>` rejects, where `<tag>` is the full id or a prefix of at least `a-` + 6
  hex (an ambiguous prefix is refused with a message and uses no attempt). The outcome ("approved", "wrong
  code, N attempts left", the waiting action's error) goes back on stderr. The code is shown
  in the same place it is typed, so terminal mode protects only against agents that cannot
  reach that terminal. That is the documented weaker level (OD-P2-3).
- **2.H harness.** The headless harness keeps working: it starts the daemons with
  `DORYLINAE_APPROVAL=terminal` and `DORYLINAE_DEBUG=1`, holds pipes to their stderr and
  stdin, reads each code from stderr and writes `<id> <code>` to stdin. The agent under test
  never sees either pipe.

## IPC and CLI

| Method | Params | Result |
|---|---|---|
| `approval_list` | none | `{"approvals": [<approval view>]}` (pending only) |
| `approval_open` | `{"id"}` | `{"approval": <view>}`. Desktop mode: opens the window again if none is open for this approval (no-op if one is). Terminal mode: `bad_request` ("answer on the daemon's terminal") |
| `approval_reject` | `{"id"}` | `{"approval": <view>}` |

`approval_confirm` (2.2a) is **removed** in 2.2d, together with the `approve <a-id> <code>`
form, which puts the code in argv (review 26, L7). No IPC method takes a code. The daemon
keeps no plaintext code, so `approval_open` cannot show the code again. A human who lost the
notification rejects the approval and starts the action again.

Approval view: `{"id", "kind", "summary", "created", "expires", "state", "attempts_left",
"window"}`. `window` is `open` or `closed` (desktop mode) or `terminal`.

CLI: `agentnet approve [--list | --open <a-id> | --reject <a-id>] [--json]`. It is the same on
every machine. An extra positional argument (the old code form) is a usage error (exit 2)
that says where to type the code. Any caller may run `--open`: it only puts a window in front
of the human, one per approval at a time.

Error codes: `unknown_approval`, `approval_expired`, `approval_limit`, `approval_locked`,
`approval_unavailable` (exit 1). `bad_code` is no longer an IPC error. Wrong codes are
reported in the window or on the terminal.

## Audit

`approval.create {id, kind, subject}`, `approval.approve {id, kind, subject}`,
`approval.reject {id, kind, subject, reason: "user"|"attempts"|"expired"|"locked"|"precondition"}`,
`approval.bad_code {id, attempts_left, via: "window"|"terminal"}`, `approval.locked {wrong_codes}`, `approval.mode
{mode}`, `approval.open {id}` (2.2d: a window reopened through `approval_open`, so repeated
reopening by an agent is visible). The window's first opening, a dismiss and a malformed
answer are not audited. `approval.approve` and `approval.reject` record whether the answer
came from the window, the terminal or IPC (reject only) as `via`. `subject` is the id of the waiting object (`g-…` grant, `p-…` policy, `s-…`
session, `i-…` device-link intent, `l-…` link for a scope). Never the code or its MAC.

## Tables

```sql
-- migration 15 (2.2a): approvals, grant_policies (policies: grant.md)
CREATE TABLE approvals (
    id        TEXT PRIMARY KEY,                 -- a-<32 hex>
    kind      TEXT NOT NULL CHECK (kind IN ('grant','grant_policy','release','accept_result','device_link','device_scope')),
    subject   TEXT NOT NULL,
    summary   TEXT NOT NULL,                    -- the text shown (local data); no code material
    created   TEXT NOT NULL,
    expires   TEXT NOT NULL,
    attempts  INTEGER NOT NULL DEFAULT 0,
    state     TEXT NOT NULL CHECK (state IN ('pending','approved','rejected','expired')),
    decided   TEXT
);
CREATE INDEX approvals_state ON approvals (state, expires);
```

The rolling wrong-code count lives in `settings` (key `approval.wrong_codes`: a JSON array
of at most 10 timestamps), so it survives restarts.

Decided rows are pruned after 30 days (the audit keeps the record). Both tables of migration
15 go into the DROP lists of both rewind tests in `internal/store/store_test.go`.
