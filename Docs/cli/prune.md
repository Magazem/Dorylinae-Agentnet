# `agentnet prune`

Removes **finished** requests, work sessions, debates, grants and inbox records older than a
cutoff from the daemon's database ([../protocol/retention.md](../protocol/retention.md)). The
daemon never deletes this content on its own (owner decision D50); this command is how you
reclaim space. Introduced by ticket R55-F13.

```
agentnet prune --older-than DURATION [--yes] [--json]
```

| Flag | Meaning |
|------|---------|
| `--older-than D` | Required. Only items finished before `now − D`. `D` is a number with a unit, `d` (days) or `w` (weeks): `35d`, `90d`, `12w`. The minimum is `35d` |
| `--yes` | Actually remove. Without it the command only counts what it would remove (a dry run) and changes nothing |
| `--json` | Machine-readable output on stdout |

**What counts as finished:** a request in a final state (`declined`, `completed`,
`cancelled`), last changed before the cutoff, whose work session is closed and whose debate
is closed or broken, both also before the cutoff. Its session, the session's grants, its
experience records and its debate go with it. Expired or revoked grants, closed sessions and
debates left without a request, and inbox records (which keep no content) older than the
cutoff are removed too. Anything still open is kept, however old.

**Never removed:** the audit log (append-only; the `agentnet log` rows of a pruned request
stay), Decisions (`agentnet decision`), peers, teams and keys.

The minimum of 35 days keeps every pruned item older than every window in which a peer's
mail about it would count as new ([retention.md §Why 35 days](../protocol/retention.md#why-35-days)).

The database file does not shrink: SQLite reuses the freed space for new data. Removed rows
are overwritten on disk (`secure_delete`).

The daemon must be running. The command calls IPC `data_prune` repeatedly (at most 500
requests per call) until nothing is left, then prints the totals. Each call that removed
something writes one `data.prune` audit row with the counts.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Done (or, without `--yes`, counted) |
| 1 | Error (the database write failed, for example on a full disk) |
| 2 | Usage error, including a missing `--older-than`, a bad duration or one under `35d` |
| 3 | Daemon not running |

## Human output

Dry run (no `--yes`):

```
would remove (finished before 2026-08-26T10:00:00Z):
  requests            412
  work sessions       130
  grants               57
  debates               3  (+ 41 entries, 2 constraints)
  experience records  133
  inbox records      5210
run again with --yes to remove them
```

With `--yes`, the same table under `removed (finished before …):`, and no last line. When
nothing matches: `nothing to remove (finished before …)`.

## `--json` output

```json
{"ok": true, "dry_run": false, "cutoff": "2026-08-26T10:00:00Z",
 "counts": {"requests": 412, "work_sessions": 130, "grants": 57, "debates": 3,
            "debate_entries": 41, "debate_constraints": 2, "experience_records": 133,
            "mail_inbox": 5210}}
```

`counts` are totals over every call. If a later call fails, the output is the error with
`counts` of what was already removed (`{"ok": false, "error": {...}, "counts": {...}}`, exit 1);
running the command again continues.

Error codes: `usage` (exit 2), `daemon_not_running` (exit 3), `bad_request`, `io_error`,
`daemon_error` (exit 1).
