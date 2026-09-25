# `agentnet log`

Shows the audit log and checks its hash chain ([../protocol/audit.md](../protocol/audit.md)).
The log is content-free by construction: ids, keys, enums, counts and sizes, never titles,
briefs, results, notes, reasons, paths or commands.

```
agentnet log [--since DURATION|TIME] [--until TIME] [--session ID] [--action PREFIX]
             [--limit N] [--json]
agentnet log --verify [--anchor ID:HASH]... [--timeout SECONDS] [--json]
agentnet log --head [--json]
```

| Flag | Meaning |
|------|---------|
| `--since D\|T` | only rows at or after this time: a duration back from now (`24h`, `90m`, `7d`) or an RFC 3339 time |
| `--until T` | only rows at or before this RFC 3339 time |
| `--session ID` | only the rows of one work session (`s-…`) or request (`r-…`): the rows that name the session, its request (with its peer), its grants, its approvals and its decision. Rows of another session are not shown. An `r-` id resolves to its session; a request without one shows its request rows |
| `--action PREFIX` | only actions starting with `PREFIX` (`grant.`) |
| `--limit N` | print at most `N` rows. Default: every match (the CLI pages through the daemon, 1000 rows a call) |
| `--verify` | check the whole hash chain (it takes no filters: they are views, `--verify` always checks everything) |
| `--anchor ID:HASH` | with `--verify`: the row `ID` must still have hash `HASH`. Repeatable |
| `--head` | print the newest row's `id`, `hash` and time, to keep as an anchor |
| `--timeout S` | with `--verify`: seconds to wait for the walk (default 120; `audit_verify` is exempt from the 2-second rule) |
| `--json` | machine-readable output on stdout |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | done; with `--verify`, the chain is intact |
| 1 | error (the daemon or the database failed, timeout) |
| 2 | usage error, including a malformed `--anchor` |
| 3 | the daemon is not running **and** there is no database file |
| 5 | `--verify` found the chain **broken** (tampering or a bypassed writer), naming the first bad row |

Exit 5 is separate from 1 so a script can tell tampering from an error.

## Without the daemon

The command reads through the daemon (IPC). When nothing listens on the endpoint it opens the
database file **read-only** and does the same work itself, saying so on stderr:

```
agentnet: agentnetd is not running; reading the database directly (read-only)
```

This is what makes `--verify` usable when the daemon refuses to start because of an unchained
row. No daemon and no database is exit 3.

## Human output

One line per row, `TIME ACTOR ACTION key=value …`, keys sorted, values as JSON:

```
2026-10-01T09:00:05.5Z daemon audit.chain_start legacy_last_id=0 legacy_rows=0
2026-10-01T09:00:05.6Z daemon daemon.start pid=4242 version="0.3.0"
```

Every part goes through the control-character cleaner used for notifications, so a key or
name that came from a peer cannot inject terminal escapes.

`--verify`:

```
The audit log is intact: 1043 rows checked, 210 of them written before the chain existed (their history before then cannot be checked).
head 1043 c2878d54…f24d85 (keep it as an anchor: --anchor 1043:c2878d54…f24d85)
```

and, on stderr with exit 5:

```
agentnet: the audit log is BROKEN at row 512: hash_mismatch
```

`reason` is one of `gap`, `unchained`, `chain_start`, `hash_mismatch`, `malformed`,
`anchor_mismatch`, `anchor_missing` ([audit.md](../protocol/audit.md#verification)). An anchor
on a row written before the chain existed is `anchor_mismatch` (tampering, not a usage error).

## `--json`

```
{"ok": true, "events": [{"id", "ts", "actor", "action", "detail": {…}, "hash"}]}
{"ok": true, "verify": {"status": "ok"|"broken", "rows", "legacy_rows", "chained_from",
                         "head": {"id", "hash", "ts"}, "first_bad"?, "reason"?}}
{"ok": true, "head": {"id", "hash", "ts"}}
```

`hash` is absent on a row written before the chain. A `--verify` result with
`"status": "broken"` still has `"ok": true` (the call worked) and exits 5. Errors are
`{"ok": false, "error": {"code", "message"}}`.

## What the chain does not prove

It detects a changed, deleted, inserted or reordered row before the head, but not a rewrite of
the whole chain by someone who can write the database file, nor truncation of the newest
rows. Keep a head somewhere they cannot reach (`--head`, a commit message, a ticket) and pass
it back with `--verify --anchor` to close both gaps for everything before it.
