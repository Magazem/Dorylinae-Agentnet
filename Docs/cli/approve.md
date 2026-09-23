# `agentnet approve`

Opens, rejects or lists a pending human approval (Docs/protocol/approval.md), used
by grants, sensitive-result release, own-device link and their policies.
Introduced by ticket 2.2a; the code entry form was replaced by the daemon-owned
approval window in ticket 2.2d (D19).

```
agentnet approve --open <a-id> [--json]   show the approval window again
agentnet approve --reject <a-id> [--json] reject; the waiting action is dropped
agentnet approve --list [--json]          list pending approvals (never codes)
```

The code is never given to this command, an IPC method, or any other CLI
argument. It is shown only on the desktop notification, and typed only into
the **AgentNet approval window** the daemon itself opens (or, on a headless
machine started with `DORYLINAE_APPROVAL=terminal`, on the daemon's own
terminal: the code goes to the daemon's stderr and is answered on the
daemon's stdin with `<tag> <code>` or `reject <tag>`). The code never appears
in this command's own output, an IPC result, the daemon log, the audit log,
a SQLite column, or any process's argument list. 3 wrong codes reject the
approval; an approval also expires 10 minutes after it was created, or
immediately if the daemon restarts meanwhile (the check value lives only in
the daemon's memory).

| Flag | Meaning |
|------|---------|
| `--open ID` | Reopen the approval window for a pending approval (a no-op, still audited, if one is already open) |
| `--reject ID` | Reject a pending approval instead of approving one |
| `--list` | List pending approvals |
| `--json` | Machine-readable output on stdout |

The old `agentnet approve <a-id> <code>` form put the code in this command's
own argv, readable by any local user through `/proc`/`ps` (review 26, L7). It
is a **usage error** (exit 2) that points at the approval window.

On approve (from the window, or the daemon's terminal in terminal mode), the
daemon re-checks every precondition of the waiting action against the
current state (for example: the session is still open, the peer is still
paired) before performing it. A precondition that no longer holds drops the
waiting action; the human sees this in the window or the terminal, not
through this command.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Opened, rejected, or listed |
| 1 | Unknown approval, expired, locked, or unavailable |
| 2 | Usage error (including the old `<a-id> <code>` form) |
| 3 | Daemon not running |

## Human output

```
Approval a-0123456789abcdef0123456789abcdef: window open
```

```
Rejected release (a-0123456789abcdef0123456789abcdef)
```

```
No pending approvals.
```

or a table of `ID`, `KIND`, `SUMMARY`, `EXPIRES`, `ATTEMPTS LEFT`.

## `--json` output

`--list`:

```json
{
  "ok": true,
  "approvals": [
    {"id": "a-0123456789abcdef0123456789abcdef", "kind": "grant",
     "summary": "approve grant git.read on agentnet (branch feat-x) to bob (2ED9 TGVE…) for 2h?",
     "created": "2026-01-01T00:00:00.000Z", "expires": "2026-01-01T00:10:00.000Z",
     "state": "pending", "attempts_left": 3, "window": "closed"}
  ]
}
```

`--open` / `--reject`:

```json
{"ok": true, "approval": {"id": "...", "kind": "release", "state": "pending", "window": "open", "...": "..."}}
```

`window` is `"open"` or `"closed"` in desktop mode, or `"terminal"` when the
daemon runs with `DORYLINAE_APPROVAL=terminal`.

Failures print `{"ok":false,"error":{"code","message"}}` with code
`unknown_approval`, `approval_expired`, `approval_limit`, `approval_locked`,
`approval_unavailable`, `daemon_not_running`, `timeout` or `usage`.
`bad_code` is no longer a CLI error: wrong codes are reported in the window
or on the daemon's terminal, never here.
