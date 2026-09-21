# `agentnet inbox`, `accept`, `decline`, `defer`, `complete`

Status: draft (Phase 1, 1.6, 1.7). Protocol: [../protocol/request.md](../protocol/request.md).

Lists the requests addressed to you and answers them.

```
agentnet inbox [--team TEAM] [--all] [--json]
agentnet accept <id> [--from <peer>] [--json]
agentnet decline <id> --reason R [--from <peer>] [--json]
agentnet defer <id> --until T [--from <peer>] [--json]
agentnet complete <id> [--note N] [--from <peer>] [--json]
```

| Flag | Meaning |
|---|---|
| `--team TEAM` | `inbox`: only requests in that team |
| `--all` | `inbox`: every received request, including answered ones and deferred ones not yet due |
| `--reason R` | Required for `decline`. 1–500 characters, sent to the requester |
| `--until T` | Required for `defer`. RFC 3339 time or a duration (`2h`, `3d`), at most 90 days ahead |
| `--note N` | `complete`: optional, up to 2000 characters, sent to the requester |
| `--from <peer>` | Needed only when the id matches requests from several peers |
| `--json` | Machine-readable output on stdout |

## Order

`inbox` lists `pending` requests, and `deferred` ones whose `--until` has passed
(marked `due`). They are sorted by **effective priority**, highest first, then by age,
oldest first. Effective priority is the urgency weighted by how often this sender's urgent
requests were actually accepted by you over the last 30 days
([../protocol/request.md](../protocol/request.md#effective-priority)): `low` 1000,
`normal` 2000, `high` up to 3000, and `blocking` up to 4000. A sender whose urgent requests
you usually defer or decline sinks toward `normal`.

A request over its sender's weekly budget (5 `high`, 2 `blocking`) is shown as `normal`,
with `urgency_declared` and a note.

## Answering

| Command | Allowed when the request is | Result state |
|---|---|---|
| `accept` | `pending` or `deferred` | `accepted` |
| `decline` | `pending` or `deferred` | `declined` (final) |
| `defer` | `pending` or `deferred` | `deferred` until `T` |
| `complete` | `accepted` | `completed` (final) |

Anything else fails with `bad_state` and changes nothing. Each answer is queued to the
requester at once (under 2 s, even if the requester is offline) and logged with a timestamp.

## Exit codes

0 done, 1 error, 2 usage, 3 daemon not running.

## Human output

```
$ agentnet inbox
ID                                  FROM   TYPE    URGENCY  PRIO  AGE  TITLE
r-0123456789abcdef0123456789abcdef  alice  review  high     3000  4m   Review retry change
r-89abcdef0123456789abcdef01234567  carol  task    normal   2000  2h   Update the runbook
```

With nothing pending: `Inbox empty.` `accept` prints `Accepted r-… from alice`, and the
other commands print the same form.

## `--json` output

- `inbox`: `{"ok": true, "requests": [<in view>]}`. Each view carries the full `brief`,
  `artifacts`, `priority`, `state`, `due`?, `urgency`, `urgency_declared`,
  `downgraded_by` and `urgency_note`? ([../protocol/ipc.md](../protocol/ipc.md#requests)).
- `accept`, `decline`, `defer`, `complete`: `{"ok": true, "request": <in view>, "mail_id"}`.

Error codes: `unknown_request`, `ambiguous_request`, `bad_state`, `unknown_team`,
`ambiguous_team`, `bad_request`, `daemon_not_running` (exit 3) and `usage` (exit 2).

## Audit

`request.accept`, `request.decline`, `request.defer` and `request.complete`, with `age_s`
([../protocol/request.md](../protocol/request.md#audit-and-metrics)).
