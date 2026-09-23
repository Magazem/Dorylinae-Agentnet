# Human approval

Status: **draft** for Phase 2 (ticket 2.2a, [../review/23-phase2-tickets.md](../review/23-phase2-tickets.md)).
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
only there, and the approving command must quote it.

## Threat model

| Attacker | Goal | Control |
|---|---|---|
| A prompt-injected local agent that uses AgentNet's interface (IPC, CLI) | Issue a grant, release a quarantine, link a device or widen a helper scope without the human | It cannot see the code: the code is never in an IPC result, a CLI output, the audit log, the daemon log, a webhook, SQLite (not even a hash: the check value lives only in the daemon's memory), a process argument list or a file. It must guess: 3 attempts per approval, 6 digits → ≤ 3 × 10⁻⁶ per approval; approvals are rate-limited and wrong codes are capped per day ([Object](#object)) |
| Another local OS user | Read the code as it passes to the notifier | The code never appears in a process's argument list (world-readable through `/proc` and `ps`); it reaches the notifier in-process (Linux) or through the environment (macOS, Windows), which only the same user can read ([Delivering the code](#delivering-the-code)) |
| A remote peer | Trigger approvals to annoy or to phish the human | Only local IPC callers create approvals; a peer's mail never does. The notification names the **local** action and the **peer's name and fingerprint** |
| Malware running as the user | Anything | **Out of scope.** It can read the screen, the keystore file fallback and the database. The approval is a gate against confused-deputy agents, not a sandbox |
| A human approving without reading | A too-broad grant | The notification states action, resource, peer and expiry in plain words; the CLI prints the same before asking |

**Boundary, stated plainly.** The approval stops an agent that acts through AgentNet's own
interface. It does **not** stop an agent that runs arbitrary programs as the user and sets
out to defeat it: such an agent can read the OS notification history (on Windows through the
documented toast-history API or `wpndatabase.db`; on macOS and Linux through the
notification daemon's store), take a screenshot, drive the UI, read or write the daemon's
SQLite database and the keystore file fallback, or stop the daemon and restart it with
`DORYLINAE_APPROVAL=terminal` and read the code from its stderr. Each of these is a
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

## Flow

1. An IPC method that needs approval (for example `grant_create`) validates everything
   first, stores the action as `pending_approval`, creates the approval and shows the
   desktop notification:
   `AgentNet: approve grant git.read on agentnet (branch feat-x) to bob (2ED9 TGVE…) for 2h? Code 482913`.
   Peer-supplied text in it goes through `notify.Clean` ([notify.md](notify.md#text-and-sanitising)).
2. The method returns at once: `{"approval": {"id", "kind", "summary", "expires",
   "state": "pending"}}`. The CLI, when its stdin is a terminal, prints the summary and
   prompts `Code from the desktop notification:`; otherwise it prints the approval id and
   exits 0 with `state: pending`.
3. `agentnet approve <a-id> <code>` (IPC `approval_confirm {id, code}`) checks state,
   expiry and the code. On success, in one transaction, it sets `approved`, **re-checks every
   precondition of the waiting action against the current state** (for a grant: the session
   is still `open` and the peer still paired and allowed by D5; for a release: still
   `quarantined` in the same round; for a device link: no conflicting link appeared), and
   performs the waiting action; the result is the action's result. A precondition that no
   longer holds sets the waiting object `rejected`/dropped and returns its usual error (for
   example `bad_state`). A wrong code → `bad_code`
   (attempts left in the message); expired → `approval_expired`.
4. `agentnet approve --reject <a-id>` (no code needed) rejects it; the waiting action is
   dropped.

`approval_list` returns pending approvals (never codes) so a human can see what is waiting.

Approvals are never created by peer mail, never forwarded, and never cross machines. The two
confirmations of a device link are two local approvals, one on each device
([device.md](device.md#link-flow)).

## Headless machines

If the desktop notifier is disabled or fails (`notify_test` → `failed`), approvals are
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

## IPC and CLI

| Method | Params | Result |
|---|---|---|
| `approval_list` | none | `{"approvals": [<approval view>]}` (pending only) |
| `approval_confirm` | `{"id", "code"}` | The waiting action's result, plus `"approval": <view>` |
| `approval_reject` | `{"id"}` | `{"approval": <view>}` |

Approval view: `{"id", "kind", "summary", "created", "expires", "state", "attempts_left"}`.

CLI: `agentnet approve [<a-id> <code> | --reject <a-id> | --list] [--json]`.

Error codes: `unknown_approval`, `bad_code`, `approval_expired`, `approval_limit`,
`approval_locked`, `approval_unavailable` (exit 1).

## Audit

`approval.create {id, kind, subject}`, `approval.approve {id, kind, subject}`,
`approval.reject {id, kind, subject, reason: "user"|"attempts"|"expired"|"locked"|"precondition"}`,
`approval.bad_code {id, attempts_left}`, `approval.locked {wrong_codes}`, `approval.mode
{mode}`. `subject` is the id of the waiting object (`g-…` grant, `p-…` policy, `s-…`
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
