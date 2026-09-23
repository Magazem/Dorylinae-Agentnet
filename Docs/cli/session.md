# `agentnet sessions`, `session`, `result`, `wait`, `accept-result`

Work sessions (Docs/protocol/work-session.md): the bounded, persisted piece of work
an accepted request becomes. Introduced by ticket 2.1b. `<id>` throughout is a
session id (`s-...`) or the request id it belongs to (`r-...`), resolved through
its session.

## `agentnet sessions`

Lists work sessions.

```
agentnet sessions [--state S] [--role requester|worker] [--json]
```

| Flag | Meaning |
|------|---------|
| `--state S` | `open`, `awaiting_result`, `quarantined` or `closed` |
| `--role R` | `requester` or `worker` |
| `--json` | Machine-readable output on stdout |

Newest `state_at` first. List views omit `result.output`, `result.notes` and
`changes`, keeping their sizes (`result.output_bytes`, `result.result_bytes`)
and, while quarantined, only the sizes and status.

### Human output

```
ID                                 ROLE       STATE            PEER  REQUEST         ROUND
s-36375782ceb6baea9cee4d4273dfb035 requester  awaiting_result  bob   review this PR  1
```

or `No sessions.`

### `--json` output

```json
{"ok": true, "sessions": [{"id": "s-...", "role": "requester", "peer": {...},
  "team": {...}, "request": {"id": "r-...", "type": "task", "title": "..."},
  "state": "awaiting_result", "round": 1, "seq": 2, "opened": "...", "state_at": "...",
  "result": {"status": "pass", "output_bytes": 42, "result_bytes": 120}, "grants": []}]}
```

## `agentnet session <id>`

Shows one session, or changes it.

```
agentnet session <id> [--json]
agentnet session <id> --request-changes TEXT | --changes-from-file F [--json]
agentnet session <id> --discard [--json]
agentnet session <id> --cancel [--reason R] [--json]
agentnet session <id> --release [--json]
```

| Flag | Meaning |
|------|---------|
| `--request-changes TEXT` | `awaiting_result → open`, round + 1 (1-4000 characters). Also allowed straight from a `quarantined` result, without releasing it first (OD-P2-6 (c)) |
| `--changes-from-file F` | Read the changes text from file `F` (`-` = stdin) |
| `--discard` | `quarantined` only: close the session, cancelled, without ever seeing the result (OD-P2-6 (c)); no approval |
| `--cancel` | Close an `open` session |
| `--reason R` | Optional, with `--cancel`, 1-500 characters, never audited |
| `--release` | Release a `quarantined` result; needs a human approval (kind `release`). Prints the approval id; confirm with `agentnet approve <id> <code>` |
| `--json` | Machine-readable output on stdout |

At most one action flag may be given; with none, the command shows the
session. Every action flag is requester-only except a worker's `--cancel`,
which sends `ws.cancel`; A applies it automatically while `open`, and
otherwise refuses it (the worker's `session <id>` then shows `cancel:
"requested"` until A's next state arrives).

### Human output

```
s-36375782ceb6baea9cee4d4273dfb035  role requester  state awaiting_result  round 1
  peer:    bob
  request: review this PR (r-0123456789abcdef0123456789abcdef)
  result: status pass
    summary: looks good
```

A quarantined session instead prints:

```
s-...  role requester  state quarantined  round 1
  peer:    bob
  request: review this PR (r-...)
  quarantined: status pass, 812 result bytes, 40 output bytes, 1 artifacts (run 'agentnet session s-... --release')
```

Action commands print `<Verb> <id> (now <state>)`, e.g. `Requested changes on
s-... (now open)`, `Discarded s-... (now closed)`, `Cancelled s-...`.

### `--json` output

Show:

```json
{"ok": true, "session": {"id": "s-...", "role": "requester", "peer": {...},
  "team": {...}, "request": {...}, "state": "awaiting_result", "round": 1, "seq": 2,
  "opened": "...", "state_at": "...", "result": {...}, "grants": []}}
```

`--request-changes`, `--discard`:

```json
{"ok": true, "session": {...}, "mail_id": "m-..."}
```

`--cancel`:

```json
{"ok": true, "session": {...}, "mail_id": "m-...", "duplicate": false}
```

`--release`:

```json
{"ok": true, "approval": {"id": "a-...", "kind": "release", "state": "pending", "...": "..."}}
```

Failures print `{"ok":false,"error":{"code","message"}}` with code
`unknown_session`, `not_requester`, `not_worker`, `bad_state`, `bad_request`,
`not_available` (human approval not built into this daemon), `daemon_not_running`,
`timeout` or `usage`.

## `agentnet result <id>`

Submits a work session's result (worker only): Docs/protocol/work-session.md
§Result object (2.6), the D14 result extended by `verification` and `notes`.

```
agentnet result <id> --status pass|fail|partial|n/a
                [--summary T] [--file F | --output-from-file F]
                [--exit-code N] [--artifact SPEC]...
                [--verification none|tests_passed] [--notes T] [--json]
```

| Flag | Meaning |
|------|---------|
| `--status S` | Required: `pass`, `fail`, `partial` or `n/a` |
| `--summary T` | One line, up to 280 characters |
| `--file F` | Result output from file `F` (`-` = stdin), up to 32768 bytes. CRLF becomes LF, ANSI colour and cursor sequences are removed, other control characters are refused (same processing as `agentnet complete`). Same as `--output-from-file` |
| `--output-from-file F` | Alias for `--file` |
| `--exit-code N` | An integer from -2147483648 to 4294967295 |
| `--artifact SPEC` | Repeatable, up to 20. Same `SPEC` as `agentnet request --artifact` |
| `--verification V` | `none` (default) or `tests_passed`: your own claim, not proof |
| `--notes T` | Up to 2000 characters, sent to the requester |
| `--json` | Machine-readable output on stdout |

The result is capped at 65536 bytes of canonical JSON in total
(`result_too_large`). `agentnet complete <id>` on a request whose session is
`open` is a shorthand for this command (verification `none`, `status: "n/a"`
without `--status`).

### Human output

```
Submitted result for s-36375782ceb6baea9cee4d4273dfb035 (open)
```

The session's own mirror stays as it was until the requester's `ws.state`
confirms the new round; `agentnet session <id>` shows the update once it
arrives.

### `--json` output

```json
{"ok": true, "session": {...}, "mail_id": "m-..."}
```

Failures: `bad_state` (the mirror is not `open`, or a cancel is pending),
`not_worker`, `bad_request` (naming the field), `result_too_large`,
`unknown_session`.

## `agentnet wait <id>`

Polls once a second until the session (or, before it exists, the request)
changes (Docs/protocol/work-session.md §CLI, "wait"). The one command that
deliberately blocks beyond 2 seconds; every IPC call inside the loop still
returns in under 2 seconds.

```
agentnet wait <id> [--timeout SECONDS] [--json]
```

| Condition (as seen by the caller's side) | Exit | `--json` `"wait"` |
|---|---|---|
| Requester: `awaiting_result` (result visible) or `closed` | 0 | `"result"` / `"closed"` |
| Worker: the state changed from the one at the start (e.g. `open` after changes requested, or `closed`) | 0 | `"changed"` / `"closed"` |
| The request was `declined` or `cancelled` (before a session exists) | 0 | `"declined"` / `"cancelled"` |
| Timeout (default 300s, max 3600s) | 4 | `"timeout"` |

| Flag | Meaning |
|------|---------|
| `--timeout SECONDS` | Default 300, max 3600 |
| `--json` | Machine-readable output on stdout |

### Human output

```
result: s-36375782ceb6baea9cee4d4273dfb035 (awaiting_result)
```

or, on timeout, `timeout: s-... (quarantined)` naming the state found at the
deadline, or bare `timeout`/`declined`/`cancelled` when there is no session
view to show.

### `--json` output

```json
{"ok": true, "wait": "result", "session": {...}}
```

The session, when present, is the full show view (with `output` when
visible), so a consulting agent needs one command.

Exit codes: 0 the wait resolved, 1 error, 2 usage, 3 daemon not running, 4
timeout.

## `agentnet accept-result <id>`

Accepts a work session's result, closing the session on both sides and
completing the request on both sides (requester only;
Docs/protocol/work-session.md §Accept-result). This is the plan's 2.6
acceptance path.

```
agentnet accept-result <id> [--human] [--json]
```

| Flag | Meaning |
|------|---------|
| `--human` | Require a human approval first (kind `accept_result`); on confirm, the stored `verification` becomes `human_accepted` (`verification_by: "requester"`). Prints the approval id; confirm with `agentnet approve <id> <code>` |
| `--json` | Machine-readable output on stdout |

Without `--human`, the worker's claimed `verification` stays. Without a human
approval subsystem in the daemon build, `--human` returns `not_available`; the
plain path is unaffected.

### Human output

```
Accepted result for s-36375782ceb6baea9cee4d4273dfb035 (now closed)
```

or, with `--human`:

```
Approval a-0123456789abcdef0123456789abcdef pending; confirm with 'agentnet approve a-0123456789abcdef0123456789abcdef <code>' (the code arrives by desktop notification).
```

### `--json` output

```json
{"ok": true, "session": {...}, "mail_id": "m-..."}
```

or, with `--human`:

```json
{"ok": true, "approval": {"id": "a-...", "kind": "accept_result", "state": "pending", "...": "..."}}
```

Failures: `bad_state` (not `awaiting_result`), `not_requester`,
`unknown_session`, `not_available` (`--human` only).

## Exit codes (every command above except `wait`)

| Code | Meaning |
|------|---------|
| 0 | Done |
| 1 | Error (see each command's failure codes) |
| 2 | Usage error |
| 3 | Daemon not running |
