# Human approval

Status: **draft** for Phase 2 (tickets 2.2a and 2.2d, [../review/23-phase2-tickets.md](../review/23-phase2-tickets.md)).
2.2d (owner decision D19) replaces code entry through the CLI with the daemon-owned
[approval window](#the-approval-window).
Used by grants ([grant.md](grant.md)), the sensitive-grant release
([work-session.md](work-session.md#quarantine-24)), `accept-result --human`, and the
own-device link and scope ([device.md](device.md)). Change this document first.
Review 55 ticket **R55-F5** (draft, pending the adversarial review and the owner's approval of
OD-R55F5-1..10) adds [Approval summaries](#approval-summaries-r55-f5): one builder, the contents per
kind, one sanitising rule and a hard length limit.

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
| A remote peer | Trigger approvals to annoy or to phish the human, or pass as another peer (a twin name, a decoy code in its name) | Only local IPC callers create approvals; a peer's mail never does. The notification and the window name the **local** action, the **peer's cleaned name and its fingerprint**, computed by the daemon from the key the action binds to ([Approval summaries](#approval-summaries-r55-f5)) |
| Malware running as the user | Anything | **Out of scope.** It can read the screen, the keystore file fallback and the database. The approval is a gate against confused-deputy agents, not a sandbox |
| A human approving without reading | A too-broad grant | The notification and the approval window state action, resource, peer and expiry in plain words |
| A prompt-injected local agent that asks for more than the human meant (a whole repository instead of a folder, `--public`, a 90-day policy, a twin-named peer, the wrong session, a command hidden after a long argument) | Get the broad action approved under a text that looks like the benign one (review 55 R55-005, R55-007) | Every summary is built by one builder from the object that will be performed and states every field that decides what is approved, the fingerprint included; a summary that does not fit the window is refused, never cut; the confirm transaction re-derives the text and rejects the approval if it no longer matches ([Approval summaries](#approval-summaries-r55-f5)) |

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
  `device_scope`, `debate_constraint` (3.4, [debate.md](debate.md#human-constraints-34)). The
  action waits in the owning table in a `pending_approval` state that references the approval
  id, except `debate_constraint`: the constraint waits only in the approval's in-memory action
  and is stored by its Perform, so a restart (which expires every pending approval) or a
  rejection leaves nothing in the debate tables.
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
- **macOS:** title and body go through the environment of `osascript` as base64 of their
  UTF-8 bytes (read with `system attribute`, decoded as UTF-8 with Foundation, R55-203), as
  Windows already does; the script is fixed text.
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
summary **in full** (built as in [Approval summaries](#approval-summaries-r55-f5): action,
resource, peer name and fingerprint, expiry, and the other fields of its kind) and a 6-digit
input box, with
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
- **The summary is shown as built (R55-F5).** The window adds only the fixed sentence about
  where the code is. It never cleans, collapses, escapes (beyond what its own toolkit needs to
  show the text literally, [Display-safe output](#display-safe-output)) or cuts the summary.
  Today's `windowText` runs `notify.Clean(summary, 4096)`, which both cuts (C14-02) and
  collapses the double spaces inside a quoted argv; that is removed. Instead the window checks
  that the summary is display-safe and at most `MaxWindowSummary` code points, and if not it
  does not open (not ready, so `approval_unavailable`), a fail-closed backstop that a correct
  handler never reaches.
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
| macOS | `/usr/bin/osascript` with a fixed script: `activate` (osascript itself, no TCC permission; it brings the dialog to the front), then `display dialog (k & ": " & s) with title ("AgentNet approval " & t) default answer "" buttons {"Reject", "Approve"} giving up after <from env>`, with no `default button` and no `cancel button`, so Return and Esc click nothing. The script turns the record into the one-line answer (`gave up` → `dismiss`). Tag, kind and summary (`t`, `k`, `s`) come from the environment as base64 of their UTF-8 bytes, read with `system attribute` and decoded as UTF-8 with Foundation (`use framework "Foundation"`), so non-ASCII text is not decoded in a legacy encoding (R55-203); a value that does not decode is an error, so osascript exits non-zero | Still running after 1.5 s, or already exited with a valid answer. An early non-zero exit is failure | `display dialog` inside osascript needs no Automation (TCC) permission. It cannot delay input. Only a daemon running in the user's GUI session (a LaunchAgent) can show it. The manual check includes a non-ASCII peer name (UTF-8 decoding) |
| Linux | `zenity --entry --title=… --text=… --extra-button=Reject --timeout=<s>`, else `kdialog --title=… --inputbox=…`. Every value is one `--opt=value` argument, and peer text is never a positional argument, so a summary starting with `-` cannot become an option. The title is shown literally by both tools. The text is escaped for each tool's real parser, so the window shows exactly the builder's text (R55-F6; review 55 R55-025, which found the former "markup-escaped for both tools" wrong): zenity passes `--text` through GLib `g_strcompress` (decodes `\` escapes, cuts at a decoded NUL) and sets it as a GTK **mnemonic** label, not markup, so every `_` is doubled and then every `\` is doubled, and `& < >` are left alone. kdialog decodes `\\` and `\n` (`Utils::parseString`) and shows the result in a `QLabel` with a buddy, whose format is guessed and in which `&` marks a mnemonic, so the text is forced rich text (`<qt><p style="white-space:pre-wrap">…</p></qt>`), with `& < >` as entities, each `&` doubled, and every `\` doubled. A value that is not one line of valid UTF-8 (a NUL, any C0/C1 control, U+2028/U+2029) makes the window fail closed (not ready); `windowText` never produces one. The program is the first of `/usr/bin/<tool>`, `/bin/<tool>`, `/run/current-system/sw/bin/<tool>` (NixOS) that exists, is owned by root and is not group- or world-writable; never `PATH` (a Flatpak-only install is not found). `DISPLAY`, `WAYLAND_DISPLAY`, `XAUTHORITY` and `XDG_RUNTIME_DIR` are taken from the daemon's environment or, if missing, from the systemd user manager's environment, read in-process at each opening (property `Environment` of `org.freedesktop.systemd1.Manager` over godbus): a user unit started at `default.target` often starts before the desktop exports them | As macOS | **Title and summary are in argv** (neither tool reads its text from stdin or the environment), so another local user can read them through `/proc/<pid>/cmdline` unless `/proc` is mounted `hidepid=2`. The code is never in them. This is **accepted and documented** (owner, OQ-2.2d-1 = (a)): the summary is local metadata, not a secret. `Docs/cli/approve.md` and the Linux install notes say so and name `hidepid=2` as the fix on shared machines. kdialog has no Reject button: reject with `agentnet approve --reject` |

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

## Approval summaries (R55-F5)

(Review 55: R55-005 = C11-01, C11-06, C12-02, T8-01, T8-02; R55-007 = C14-02; R55-066 = T11-02;
R55-122 = T6b-02; R55-148 = T6b-11. Owner decision D44, review-55 §6 D9.) The window and the
notification are the only channel a local agent cannot write to, so the summary is the whole
of what the human approves. Review 55 found that most kinds named only a random label, an
opaque session id or the peer's self-chosen name, never its key; that grants and policies left
out scope, branch, `public`, `until` and the fingerprint; that grant summaries carried the raw
card name; that a decoy code split by a zero-width character survived; and that a scope summary
of up to 16384 bytes was cut at 4096 code points in every window, so a command placed last was
approved unseen. This section is normative for all seven kinds and replaces every per-handler
summary.

### One builder

- **One builder.** Every summary is produced by one builder (for example
  `approvaltext.Build(facts)`; the package and names are the implementer's choice) from a
  typed **facts** value per kind ([Contents per kind](#contents-per-kind)). No handler formats a
  summary itself. The builder is a pure function: the same facts give the same bytes. It reads
  no clock, locale, time zone, environment or database.
- **Facts come from the object that will be performed, never from IPC params.** A handler
  first builds what it will store or perform (the grant record, the policy, the link intent,
  the resolved scope, the prepared constraint, the session row), then derives the facts from
  that object and from the `peers` row of its key. So the text cannot describe something other
  than what Perform does. (Today `grant_create` formats `p.Action` and `label` next to a
  separately built token; the grant summary is the one place where they could drift.)
  **For `grant`, what Perform sends is the token signed at Create** (grant.md §Issuance step
  6), not the row. So the grant's facts are taken from that signed token, decoded back from
  the exact bytes Perform will send (action, label, branch, scope, `sensitive`, `nbf`, `exp`,
  audience, session), plus the one local-only field, the resolved path, from the row. The
  Precondition first requires the stored row to agree with the token on every shared field
  (review 58a, M3).
- **The path is the identity-resolved path.** A grant's or policy's path fact is the path
  that grant.md §Issuance step 3 resolves and stores (R55-F7: symbolic links, and on Windows
  junctions, mount points, `subst` and 8.3 names, resolved from an open handle), never the
  spelling the agent passed. The existing `recheckResource` at confirm requires the path to
  still resolve to it, so a folder swapped for a link while the approval waits is rejected.
- **Precondition compares.** At confirm, inside the confirm transaction, each kind's
  Precondition, after its existing state checks:
  1. re-derives the facts from the tables of record: the stored row (grant, policy, link
     intent, session); for the two kinds whose waiting object lives only in memory
     (`device_scope`, `debate_constraint`), the captured object plus the current `peers` row;
  2. requires them equal to the facts captured at Create, field by field (times compared as
     instants);
  3. requires `Build(facts)` to equal `approvals.summary`, read in the same transaction, byte
     for byte.

  Any difference rejects the approval, reason `precondition`, with the message "the request
  changed after it was shown; start it again". This turns review 46 H2's "the human approves
  what the window shows" into a checked property. Facts that can change while an approval
  waits: the peer's name (a roster update or re-pairing; OD-R55F5-5) and, for release and
  accept-result, the round, which the `seq` check already binds. A fact that depends on time
  (the release's rule-2 reason, "a sensitive grant in another session in the last 7 days") is
  evaluated against the approval's stored `created` instant, both at Create and at confirm, so
  the comparison stays deterministic and the builder stays free of any clock.
- **The summary holds no secret and no quarantined content.** `approvals.summary` is returned
  by `approval_list` to any local caller, the agent included. So no summary ever contains a
  code, a token, or the content of a quarantined (or not yet accepted) result: only its
  status and sizes.

### Length

- The builder's output is at most **`MaxWindowSummary` = 4096 code points**
  (`notify.MaxWindowSummary`, unchanged). The window's fixed sentence about where the code is
  comes on top and is not counted. 4096 code points are at most 16 KiB of UTF-8, which fits the
  Windows window's environment variable (base64 of UTF-16, at most about 22 000 characters,
  under the 32 767-character limit) and a Linux argv string (128 KiB).
- **Refused at the IPC layer, never cut.** A handler whose summary would be longer refuses the
  call before anything is stored and before any approval exists: `bad_scope` (field `scope`)
  for `device_scope_set`, which replaces device.md's 16384-byte limit, and `bad_request`
  naming the field that made it long for every other kind (for example "resource: too long to
  show in full in the approval window"). Nothing is cut anywhere: not by the builder, the
  approval store, the window, terminal mode or `approval_list`. The OS may shorten a
  notification body. The window is the authoritative view, and the notification says so
  ("Type this code only into the AgentNet approval window …").
- **Backstops that fail closed.** `approval.Store.Create` refuses a summary that is longer or
  not [display-safe](#display-safe-output) (an internal error, so the method fails and nothing
  is stored), and the window refuses to open for such a text ([The approval
  window](#the-approval-window)). A correct handler never reaches either. They exist so that a
  missed check refuses instead of cutting (C14-02).
- **Not cut is not the same as seen (review 58a, M1).** The Windows window shows about 1000
  characters in its 490×200 box and scrolls for the rest, so a command placed after a
  3000-character argument is still below the fold. And `display dialog` and zenity `--entry`
  may not scroll at all. So:
  - every summary puts what decides the approval **before** any long, agent-chosen field. For
    `device_scope`, the count and the names of all commands come first
    ([Contents per kind](#contents-per-kind)), so a command hidden below the fold is still
    named at the top;
  - every summary ends with its fixed "Confirm only if …" sentence, so a text whose end the
    human cannot see is visibly unfinished;
  - the manual check (review 58 M1, M2) confirms on Windows, macOS and Linux that a
    4096-code-point summary can be read to its end and that the buttons and the code box stay
    on screen. If a platform fails, F6 changes that platform's window (OD-R55F5-10). The limit
    stays one number for all platforms, because the text must be the same everywhere.

### Sanitising: one character rule, two renderings

One predicate, `hidden(r)`, shared by the builder, `device.DisplayQuote` and
`decision.Visible`. The decision output does not change: this is exactly its set, and review 46
H1's. It is true for:

- C0 and C1 controls (U+0000–U+001F, U+007F–U+009F);
- format characters (`unicode.Cf`): the bidi controls (U+200E, U+200F, U+202A–U+202E,
  U+2066–U+2069), the zero-width characters U+200B–U+200D, the word joiner U+2060, U+FEFF and
  the tag characters U+E0000–U+E007F, among others;
- U+2028 and U+2029;
- variation selectors (`unicode.Variation_Selector`);
- the other default-ignorable code points (`unicode.Other_Default_Ignorable_Code_Point`: U+034F,
  the Hangul fillers U+115F, U+1160, U+3164, U+FFA0, …);
- every space separator (`Zs`) other than U+0020, and U+2800;
- any rune that is not `unicode.IsGraphic`, except U+0020.

Invalid UTF-8 is decoded as U+FFFD before anything else.

**Name rendering, `displayName(s)`**, for text that only identifies: the peer's card name and a
team name. The binding is the fingerprint, not the name, so the name may be changed to make it
safe.
1. Every hidden rune that renders as space (controls, U+2028/U+2029, `Zs`, U+2800) becomes
   U+0020. Every other hidden rune is **removed**, so that `4​8​2​9​1​3` (zero-width spaces)
   reads as `482913` for step 3 (R55-066).
2. Runs of spaces collapse to one, and the text is trimmed.
3. **Stacked marks.** After each base character at most **2** combining marks (`Mn`, `Me`)
   are kept; the rest are removed. A long stack of marks is drawn above or below its line and
   can cover the neighbouring text, the fingerprint included (review 58a, M4).
4. **Long digit runs.** A digit run is a maximal sequence of runes of category `N` (`Nd`, `Nl`,
   `No`: ASCII, fullwidth, superscript, circled and mathematical digits). Between two of its
   numbers there may be **up to 8** separator runes (spaces, `P*` or `S*`), so `4 - 8 - 2 - 9 -
   1 - 3` and `4 - - 8 - - 2 …` count as one run (review 58a, L3; review 64 F5S-2). Combining
   marks of any kind (`Mn`, `Mc`, `Me`) anywhere in the run, on a number or on a separator,
   are part of it and do not count as separators (F5S-2). A run of **6 or more** numbers is replaced by one `…`. This is today's
   `stripLongDigits` (review 26 N4, review 36 L2), made to see through invisible characters,
   combining marks, non-decimal digits and spaced-out digits. The zenity octal form
   `\064\070\062\071\061\063` is caught as well, because `\` is a separator.
5. **Fingerprint-shaped text** (OD-R55F5-9). A run of **2 or more** groups of 4 characters of
   the fingerprint alphabet (case-insensitive), joined by 1 to 3 separator runes (spaces, `P*`
   or `S*`: `-`, `.`, `_`, `/`, `—`, `--` …), and any run of 8 or more such characters with no
   separator that holds both a digit and a letter, are replaced by one `…`. Characters are
   matched after folding look-alikes onto the alphabet: fullwidth forms, and the Cyrillic and
   Greek letters that look like one of its letters or digits (`Е`, `М`, `С`, `Ε`, `З` → `3` …);
   combining marks inside a group are part of it (review 64 F5S-1). A peer cannot then put the fingerprint of the device it
   impersonates into its own name (review 58a, H1).
6. An empty result becomes `(no name)`.

The name is **not cut**: the card rules already cap it at 128 code points (OD-R55F5-4). The
40-code-point cut of notify.md stays for ordinary notifications only.

**Exact rendering, `displayQuote(s)`**, for text the human must be able to check exactly: paths,
branch, scope, the label, argv, env names, the request title and the constraint text. This is
review 40's `DisplayQuote`: a JSON string in double quotes in which every hidden rune (and U+FFFD
from invalid bytes) is escaped as `\uXXXX`, as a surrogate pair above U+FFFF. It now uses the
full `hidden` set; today it misses the variation selectors, the default-ignorable code points,
`Zs` and U+2800. It also escapes every combining mark after the 2nd in a row on one base
character (the stacked-marks rule of `displayName`, but escaped rather than removed, so the
value stays exact). It does **not** HTML-escape: `<`, `>` and `&` are shown as themselves, not
as `<`, `>` and `&` as `json.Marshal` writes them today (an encoder with
`SetEscapeHTML(false)`; review 58a, L1). Digits are **kept**: removing them would show a
different path from the one approved. A decoy code in such a field is the terminal-mode
residual of OD-R55F5-6. Letters of right-to-left scripts are kept too (they are real names);
they can visually reorder the digits and punctuation next to them inside the quotes, but
cannot hide or add a character.

**One-line rendering, `displayLine(s, max)`** (R55-F9; not an approval rendering), for
untrusted diagnostic text that ends up on one terminal line: the relay's `error` message and
the daemon's relay `last_error` ([envelope.md §Relay-supplied text](envelope.md#relay-supplied-text-daemon)).
It is steps 1–3 of `displayName` (hidden runes that render as space become one space, the other
hidden runes are removed, spaces collapse, text is trimmed, at most 2 combining marks per base).
Then the text is cut to at most `max` bytes on a rune boundary, and `…` is appended when
anything was cut. Digit runs and fingerprint-shaped text are **not** blanked (steps 4–5):
this text is diagnostic, and addresses such as `127.0.0.1:8787` must stay readable. It sits
on one line after a fixed prefix (`agentnet: pairing failed:`, `last error:`), so it cannot
pose as a separate line. An empty result stays empty. It is `displaytext.Line` in code; the
CLI applies it again at every print site listed there (defence in depth: an older daemon may
serve raw text).

**The fingerprint** is `fp(key)` of [pairing.md](pairing.md#fingerprints), computed by the
daemon from the key the action binds to. It is never taken from an IPC parameter or a card.
It is shown **in full: 20 characters (100 bits) in the grouped form of `agentnet identity`**,
five groups of four separated by single spaces, 24 characters in all, for example `2ED9 TGVE
R471 63MC C451` (OD-R55F5-1). It does not go through `displayName`. Its alphabet cannot
carry a hidden rune. Running `displayName` over it would turn digit groups such as `R471 6385`
into `…` and destroy the value being compared. A peer can generate keys until its fingerprint
holds digit groups, but that decoy is only a random guess at a 6-digit code (the window never
shows the code). Its cost is at most wasted attempts in terminal mode, the residual of
OD-R55F5-6. A peer always appears as

`peer XXXX XXXX XXXX XXXX XXXX named <displayQuote(displayName(name))>`

written `<peer>` below. A key that is no longer in `peers` (the release of a session whose peer
was removed) shows as `peer XXXX XXXX XXXX XXXX XXXX (no longer paired)`.

**Why the fingerprint comes first and the name is quoted (review 58a, H1, M2, L2).**
- *Fingerprint in the name.* With the name first and unquoted, a peer named
  `Desktop (fingerprint 2ED9 TGVE R471 63MC C451)`, which is the real desktop's fingerprint,
  would show the right fingerprint before its own. A human who compares "the fingerprint"
  would then link the twin. Now the real fingerprint is always the first one, right after the
  fixed word `peer`. The name is inside quotes that it cannot close (`displayQuote` escapes
  `"`), and fingerprint-shaped text in it is blanked (`displayName` step 5).
- *Right-to-left names.* A Hebrew or Arabic letter just before the fingerprint would make the
  bidi algorithm move its leading digits. Fixed English text (strong left-to-right) now
  precedes the fingerprint, and the name only follows it.
- *zenity before F6.* zenity decodes `\` escapes in its text (R55-025), so a name such as
  `Bob\0` would cut the Linux window right after the name, fingerprint included.
  `displayQuote` writes `\` as `\\`, which zenity decodes back to `\`, so no name could cut the
  window even before F6. F6 now escapes the whole text for zenity, so it shows `\\` as typed.

**Fixed text** in the builder is constant ASCII English.

### Display-safe output

The builder's output is **display-safe**:
- it is valid UTF-8;
- it contains no `hidden` rune except U+0020, and so no line break;
- it is one line of at most `MaxWindowSummary` code points.

`Store.Create` and the window check this ([Length](#length)). The output may contain characters
that mean something to a renderer, such as `\ " & < > _ % $`. Every renderer must show them
**literally**:
- the Windows window sets `Label.Text` and the scrolling box's `.Text`, which is literal;
- the macOS window and code notification read the text from the environment as base64 of
  its UTF-8 bytes, decoded as UTF-8 (R55-F6: `system attribute` alone may decode it in a
  legacy encoding and garble non-ASCII text, R55-203), and `display dialog` is literal;
- the terminal line and `agentnet approve --list` are literal;
- **Linux zenity and kdialog** escape the text for each tool's real parser (R55-F6, see the
  Linux row of [The approval window](#the-approval-window)): zenity passes `--text` through
  GLib `g_strcompress` (a `\` escape would become a NUL, a newline or any byte) and `--entry`
  uses a mnemonic label (`_` would be consumed), so `_` and then `\` are doubled; kdialog
  decodes `\` escapes and guesses rich text with `&` mnemonics, so its text is forced rich
  text with entities, doubled `&` and doubled `\`. The builder gives the window a fixed
  contract: one display-safe line in which `\` and `_` can occur (in `displayQuote` output,
  paths and argv).

### Contents per kind

Common notation:
- `<peer>`: as above.
- `<q(x)>`: `displayQuote(x)`.
- `<utc(t)>`: the instant `t` in UTC, to the minute, as `2026-09-29 18:45 UTC` (OD-R55F5-3).
- `<dur(d)>`: a duration in whole units, largest first, with at most two units: `30 min`,
  `2 h`, `1 h 30 min`, `7 d`, `2 d 4 h`. It is computed from two stored instants, never from
  "now", so the text does not change while the approval waits.

Every field listed as a fact is in the summary. The templates below are the exact wording
(`[…]` = only when the fact is present).

| Kind | Facts (from the object of record) | Summary |
|---|---|---|
| `grant` | grant id; action; peer key, name; resolved path; label; branch (`git.read`); scope; sensitive; `nbf`, `exp`; session id; the session's request type and title | `Grant <action> to <peer> on <q(path)> (label <q(label)>)[, branch <q(branch)>], <scope part>, for <dur(exp−nbf)> until <utc(exp)>, in session <sid> (your <type> request <q(title)>). <sensitivity part> Confirm only if you asked for exactly this.` Scope part: `only <q(scope)> inside it`, or `the whole folder` (`fs.read`), or `the whole repository` (`git.read`). Sensitivity part: sensitive → `Sensitive: results of this session stay quarantined until you release them.`; public → `PUBLIC: you state this repository is public; results of this session are NOT quarantined.` |
| `grant_policy` | policy id; action; peer key, name; resolved path; branch; scope; public; `max_expires_s`; `created`, `until` | `Allow grants with no further question until <utc(until)> (<dur(until−created)>): <action> to <peer> on <q(path)>[, branch <q(branch)>], <scope part>, each grant for at most <dur(max)>, <sensitivity part>. Confirm only if you asked for exactly this.` Scope part: `only paths inside <q(scope)>`, or `any path in it`. Sensitivity part: not public → `sensitive grants only (results quarantined)`; public → `PUBLIC grants only: results are NOT quarantined`. |
| `release` | session id; peer key, name; request id, type, title; round; `seq`; result `status`, `result_bytes`, `output_bytes`, artifact count; the number K of sensitive grants ever active in the session | `Release the quarantined result of <peer> for your <type> request <q(title)> (session <sid>, round <n>): status <status>, <result_bytes> bytes, <output_bytes> bytes of output, <a> artifact(s). Your agent will then be able to read it. <reason> Confirm only if you mean to release this result.` Reason: K > 0 → `This session had K sensitive grant(s).`; K = 0 → `This peer held a sensitive grant in another session in the last 7 days.` Never the result's summary, output, notes or artifacts ([One builder](#one-builder)) |
| `accept_result` | as `release`, without K | `Accept the result of <peer> for your <type> request <q(title)> (session <sid>, round <n>): status <status>, <result_bytes> bytes, <output_bytes> bytes of output, <a> artifact(s). This closes the request as accepted. Confirm only if you checked this result.` (OD-R55F5-8) |
| `device_link` | link intent id; peer key, name; role | `Link this device as the <role> of <peer>. <role part> Compare all five groups of this fingerprint with what 'agentnet identity' shows on the other device. Confirm only if all of them match and you started this on both devices.` Role part: helper → `That device will be able to run, on this device, the commands of a scope you set later.`; controller → `This device will be able to ask that device to run the commands of its scope.` (D9 as decided in D44: the human **compares**; nothing is typed.) |
| `device_scope` | peer key, name; the resolved scope (types, repos, commands with resolved argv, env names, timeouts, expires) | `Let <peer> run <N> command(s) (<name>, <name>, …) on this device until <utc(expires)>, for <types> requests:` then per command ` [<name>] in <q(dir)> runs <argv as a JSON array of q()> (timeout <n> s[, env <names>]);`, then ` Confirm only if you set this scope yourself.` (today's text, with the command count and names first (review 58a, M1), the fingerprint added and the expiry in UTC). Command names, types and env names are ASCII by device.md's rules and need no quoting |
| `debate_constraint` | debate session id; peer key, name; constraint id; text | `Add a human constraint to the debate <sid> with <peer>: <q(text)>. It is signed into the Decision as a human decision. Confirm only if you wrote this constraint yourself.` (today's text, with the fingerprint added) |

Notes:
- **Grant with a matching policy.** No approval, so no summary. The policy's own summary
  stated everything it allows.
- **Where the text goes.** The builder's output is the window's summary, the notification body
  (followed by the fixed sentence), the terminal-mode line, `approvals.summary`, and so
  `approval_list` and `agentnet approve --list`. It is the same text everywhere.
- **`release` and the session view.** The session view's `grants` is filled
  ([work-session.md §IPC](work-session.md#ipc), R55-122), so a human who runs `agentnet session
  <id>` before releasing sees which grants made the result quarantined.
- **Confusable names.** A name made of look-alike letters (Cyrillic `а` in `desktop`) passes
  `displayName`. The fingerprint is the control, which is why every kind shows it, first.
- **Residual: partial comparison.** A peer can generate keys until the first and last groups
  of its fingerprint match the target's (8 characters, about 2⁴⁰ tries, hours on one GPU). Only
  a human who compares all five groups defeats that, so the `device_link` text says "all five
  groups" (OD-R55F5-1).

## Flow

1. An IPC method that needs approval (for example `grant_create`) validates everything
   first, stores the action as `pending_approval` and creates the approval. Desktop mode
   opens the [approval window](#the-approval-window) and then shows the notification with the
   code. Terminal mode writes both to the daemon's stderr ([Headless machines](#headless-machines)).
   The summary comes from the one builder, and peer- or agent-supplied text in it follows
   [Sanitising](#sanitising-one-character-rule-two-renderings) (R55-F5; this replaces the plain
   `notify.Clean` rule, which missed zero-width decoys and was not applied to grants). A
   summary longer than `MaxWindowSummary` is refused here, before anything is stored.
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
   `rejected`/dropped (reason `precondition`). Every kind's precondition also re-derives the
   summary's facts and text and requires them unchanged
   ([Precondition compares](#one-builder)). A wrong code uses one attempt. An expired
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
  hex (an ambiguous prefix is refused with a message and uses no attempt). A `<code>` that
  is not exactly 6 ASCII digits is **not** checked and uses no attempt, as in the window's
  answer format: the daemon answers "enter the 6-digit code" (R55-F5, review 55 R55-148). The
  summary on this line is the builder's output, which holds no control or line-break
  character, so it stays on one line. The outcome ("approved", "wrong
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
-- (migration 19, 3.4, rebuilds approvals to add 'debate_constraint' to the kind CHECK)
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

## Open decisions (R55-F5)

For the owner, after the adversarial review. Each has a recommendation; the section above is
written as if every recommendation is taken.

- **OD-R55F5-1: fingerprint form in the window.** (a) The full 20 characters, grouped
  4-4-4-4-4 (24 characters with spaces), as `agentnet identity` prints them. (b) A prefix,
  e.g. the first 8 characters (40 bits). (c) The full fingerprint plus a word list.
  **Recommend (a).** A 40-bit prefix can be matched by generating keys (about 2⁴⁰ hashes, hours
  on one GPU), which defeats D9. The full form is what the human compares against, and it is
  what `agentnet identity` already shows. Residual (review 58a, L4): a human who checks only
  the first and last groups compares 40 bits, which key generation can match; the
  `device_link` text therefore asks for all five groups. (c) would close that, at the cost of a
  second encoding to keep in sync with `agentnet identity`.
- **OD-R55F5-2: the `device_link` `fingerprint` parameter after D9.** (a) Keep it required and
  still compared, documented as a check against the caller's typing mistakes only, not as the
  binding control. (b) Make it optional. (c) Remove it. **Recommend (a).** Nothing changes for
  the CLI, the harness or the docs' examples, and it still catches a wrong `@peer`. The binding
  now comes from the window (device.md §Link flow).
- **OD-R55F5-3: time format.** (a) UTC to the minute, plus a duration computed from stored
  instants (`for 2 h until 2026-09-29 18:45 UTC`). (b) Local time with its offset, with the
  time zone captured at Create so that the confirm-time comparison stays deterministic.
  **Recommend (a).** The builder stays pure (no time zone input), the text cannot change
  while the approval waits, and the duration is what the human reasons with.
- **OD-R55F5-4: peer names in summaries.** (a) The full cleaned name, with no length cut (the
  card caps it at 128 code points). (b) Keep today's 40-code-point cut with `…`. **Recommend
  (a).** "Never truncated" should hold for every part of the summary, and a cut name hides the
  tail where a twin name differs.
- **OD-R55F5-5: the peer's name changes while an approval waits.** (a) Reject the approval
  (`precondition`), since the text shown no longer matches. (b) Compare only the facts bound
  to the key and ignore a name change. **Recommend (a).** It is rare (a roster update or
  re-pairing in a 10-minute window), the human simply starts again, and a mid-approval rename
  is exactly the twin-name move.
- **OD-R55F5-6: digits in exact fields (paths, argv, branch, title, constraint text).** (a)
  Keep them, and accept that in terminal mode (off by default) an agent-chosen path such as
  `…\code 482913` can sit on the same stderr line as the real code. (b) Also print the code on
  its own line after the summary in terminal mode. This changes the stderr format that the 2.H
  and 3.H harnesses parse. **Recommend (a).** Stripping digits would show a path other than
  the approved one. The residual costs at most wrong attempts (3 per approval, 10 per day, a
  nuisance), only in terminal mode, and it is the same for a decoy typed into the request's
  own title today.
- **OD-R55F5-7: what the `release` summary says about grants.** (a) The count of sensitive
  grants that were ever active in the session, or the cross-session reason (rule 2). (b) List
  them (action and path), which needs a cap and a "… and N more" or a refusal, since a session
  has no grant limit. (c) Nothing. **Recommend (a).** It is bounded, needs no cut, and the
  full list is in `agentnet session <id>` (R55-122 fills `grants`).
- **OD-R55F5-8: `accept_result` contents.** (a) Status and sizes only, as for `release`. (b)
  Also show the result's own summary (at most 280 code points, quoted). **Recommend (a).**
  `approvals.summary` is readable through `approval_list`, and one rule for both
  result-approval kinds ("no result content in a summary") is simpler to keep correct. For an
  accept-result the content is already visible to the agent and the human through `agentnet
  session`, so (b) adds little.
- **OD-R55F5-9: fingerprint-shaped text in names** (review 58a, H1). (a) `displayName` blanks
  2 or more groups of 4 fingerprint-alphabet characters, and 8 or more such characters in a
  row that hold a digit and a letter (step 5), on top of putting the real fingerprint first
  and quoting the name. (b) Only the order and the quotes. **Recommend (a).** The order and the
  quotes make the real fingerprint the first one, but a human looking for "the fingerprint"
  can still read the one inside the name. The cost is that a name such as `WS2022DEV` shows as
  `…`, which is harmless because the name binds nothing. A copy written with look-alike
  letters (Cyrillic `Е` for `E`) is not caught by (a); against that only the order holds.
- **OD-R55F5-10: a window that cannot show 4096 code points** (review 58a, M1). If the manual
  check shows that macOS `display dialog` or zenity `--entry` cannot show a 4096-code-point
  summary to its end with the buttons on screen: (a) F6 changes that platform's window (for
  example a scrolling text view, zenity `--text-info` or a larger macOS dialog) and keeps the
  one limit; (b) lower `MaxWindowSummary` for every platform to what the weakest window shows.
  **Recommend (a).** (b) would make ordinary scopes impossible to set, and the text must be the
  same on every platform for the confirm-time comparison.
