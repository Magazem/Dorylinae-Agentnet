# `agentnet approve`

Confirms or rejects a pending human approval (Docs/protocol/approval.md), used by
grants, sensitive-result release, own-device link and their policies. Introduced
by ticket 2.2a.

```
agentnet approve <a-id> <code> [--json]   confirm with the code shown on the
                                           desktop notification
agentnet approve --reject <a-id> [--json] reject; the waiting action is dropped
agentnet approve --list [--json]          list pending approvals (never codes)
```

The code is shown only on the desktop notification (or, on a headless machine
started with `DORYLINAE_APPROVAL=terminal`, on the daemon's own stderr): it is
never in this command's own output, an IPC result, the daemon log, the audit
log or a SQLite column. 3 wrong codes reject the approval; an approval also
expires 10 minutes after it was created, or immediately if the daemon
restarts meanwhile (the check value lives only in the daemon's memory).

| Flag | Meaning |
|------|---------|
| `--reject ID` | Reject a pending approval instead of confirming one |
| `--list` | List pending approvals |
| `--json` | Machine-readable output on stdout |

On confirm, the daemon re-checks every precondition of the waiting action
against the current state (for example: the session is still open, the peer
is still paired) before performing it. A precondition that no longer holds
drops the waiting action and returns its usual error, such as `bad_state`.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Confirmed, rejected, or listed |
| 1 | Wrong code, expired, locked, unavailable, unknown approval, or the waiting action's own error |
| 2 | Usage error |
| 3 | Daemon not running |

## Human output

```
Approved grant (a-0123456789abcdef0123456789abcdef)
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
     "state": "pending", "attempts_left": 3}
  ]
}
```

`--reject`:

```json
{"ok": true, "approval": {"id": "...", "kind": "release", "state": "rejected", "...": "..."}}
```

Confirm prints the waiting action's own result fields with an `"approval"`
field merged in (Docs/protocol/approval.md §IPC and CLI):

```json
{"ok": true, "approval": {"id": "...", "state": "approved", "...": "..."}, "...action-specific fields...": "..."}
```

Failures print `{"ok":false,"error":{"code","message"}}` with code
`unknown_approval`, `bad_code` (message names the attempts left),
`approval_expired`, `approval_limit`, `approval_locked`,
`approval_unavailable`, `daemon_not_running`, `timeout` or `usage`, or the
waiting action's own error code (for example `bad_state`).
