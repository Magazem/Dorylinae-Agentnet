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
| `--yes` | Actually remove, **after your approval** in the AgentNet approval window (below). Without it the command only counts what it would remove (a dry run) and changes nothing |
| `--json` | Machine-readable output on stdout |

**What counts as finished:** a request in a final state (`declined`, `completed`,
`cancelled`), last changed before the cutoff, whose work session is closed and whose debate
is closed or broken, both also before the cutoff. Its session, the session's grants, its
experience records and its debate go with it. Expired or revoked grants, closed sessions and
debates left without a request, and inbox records (which keep no content) older than the
cutoff are removed too. A grant you issued is removed only with its session, because the
quarantine rule still needs it while the session exists. Anything still open is kept, however
old; so is a request of yours that was never delivered (it stays pending: resend it or leave
it).

**Inbox copies from older versions.** Before R55-F13 the daemon kept a second, signed copy of
every mail it applied. Each run with `--yes` also blanks those old copies, whatever their age
(`old inbox copies` in the output, `inbox_blanked` in `--json`). Nothing reads them; this only frees space.

**Never removed:** the audit log (append-only; the `agentnet log` rows of a pruned request
stay), Decisions (`agentnet decision`), peers, teams and keys.

The minimum of 35 days keeps every pruned item older than every window in which a peer's
mail about it would count as new ([retention.md §Why 35 days](../protocol/retention.md#why-35-days)).

The database file does not shrink: SQLite reuses the freed space for new data. Removed rows
are overwritten on disk (`secure_delete`).

The daemon must be running. The command calls IPC `data_prune` repeatedly (at most 500
requests and about 8 MiB of content per call, so mail and other commands are not held up)
until nothing is left, then prints the totals. Each call that removed or blanked something
writes one `data.prune` audit row with the counts.

**Your approval (owner decision D57).** With `--yes`, nothing is removed until you approve it,
like a grant: the daemon counts what it will remove, opens the AgentNet approval window with
those numbers and the cutoff, and shows the code in a desktop notification (on a headless
machine started with `DORYLINAE_APPROVAL=terminal`, the text and code are on the daemon's
terminal instead; see [approve.md](approve.md) and
[../protocol/approval.md](../protocol/approval.md)). The command prints `Approval a-012345
pending: approve it in …` on stderr and waits (up to the approval's 10 minutes). Once you
approve, it removes in batches against the cutoff you saw; if you reject it or it expires,
nothing is removed and the command exits 1 (`approval_rejected`, `approval_expired`). If the
counts changed between the command and your answer (another prune ran, a request changed),
the approval is rejected; run the command again. When there is nothing to remove, no
approval is asked.

**Removal cannot be undone.** A local program that can run `agentnet`, your agent included,
can start a prune, but only you can approve it. It never touches anything unfinished, younger
than 35 days, a Decision or the audit log, and every run is in `agentnet log`.

**Full disk.** Removing rows briefly needs free space of about what one call removes (the
removed data is overwritten through the database's write-ahead log). If the disk is
completely full, free a few tens of MB first; a failed call removes nothing.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Done (or, without `--yes`, counted) |
| 1 | Error (the approval was rejected or expired, or the database write failed, for example on a full disk) |
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
  old inbox copies    830
run again with --yes to remove them
```

With `--yes`, the approval line on stderr while it waits, then the same table under
`removed (finished before …):`, and no last line. When nothing matches: `nothing to remove
(finished before …)`.

## `--json` output

```json
{"ok": true, "dry_run": false, "cutoff": "2026-08-26T10:00:00Z", "approval": "a-0123…",
 "counts": {"requests": 412, "work_sessions": 130, "grants": 57, "debates": 3,
            "debate_entries": 41, "debate_constraints": 2, "experience_records": 133,
            "mail_inbox": 5210, "inbox_blanked": 830}}
```

`counts` are totals over every call. If a later call fails, the output is the error with
`counts` of what was already removed (`{"ok": false, "error": {...}, "counts": {...}}`, exit 1);
running the command again continues.

`approval` is present with `--yes` when an approval was asked.

Error codes: `usage` (exit 2), `daemon_not_running` (exit 3), `bad_request`, `io_error`,
`approval_rejected`, `approval_expired`, `approval_limit`, `approval_locked`,
`approval_unavailable`, `unknown_approval`, `daemon_error` (exit 1).
