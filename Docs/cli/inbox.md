# `agentnet inbox`, `accept`, `decline`, `defer`, `complete`

Status: draft (Phase 1, 1.6, 1.7; the `complete` result: 1.6a, D14). Protocol: [../protocol/request.md](../protocol/request.md).

Lists the requests addressed to you and answers them.

```
agentnet inbox [--team TEAM] [--all] [--json]
agentnet accept <id> [--from <peer>] [--json]
agentnet decline <id> --reason R [--from <peer>] [--json]
agentnet defer <id> --until T [--from <peer>] [--json]
agentnet complete <id> [--note N] [--status pass|fail|partial|n/a [--summary S]
                  [--exit-code N] [--output-from-file F] [--artifact SPEC]...]
                  [--from <peer>] [--json]
```

| Flag | Meaning |
|---|---|
| `--team TEAM` | `inbox`: only requests in that team |
| `--all` | `inbox`: every received request, including answered, cancelled and deferred ones not yet due |
| `--reason R` | Required for `decline`. 1–500 characters, sent to the requester |
| `--until T` | Required for `defer`. RFC 3339 time or a duration (`2h`, `3d`), at most 90 days ahead |
| `--note N` | `complete`: optional, up to 2000 characters, sent to the requester |
| `--status S` | `complete`: attach a [result](#result) with status `pass`, `fail`, `partial` or `n/a`. Required when any of the result flags below is given |
| `--summary S` | `complete`: one line, up to 280 characters |
| `--exit-code N` | `complete`: an integer from −2147483648 to 4294967295 |
| `--output-from-file F` | `complete`: attach a text output such as a test log from file `F` (`-` = stdin), up to 32 KiB |
| `--artifact SPEC` | `complete`: repeatable, up to 20. The same `SPEC` as [`agentnet request --artifact`](request.md) |
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
| `complete` | `accepted` | `completed` (final), or (ticket 2.1b) stays `accepted` and submits a work session result if the request's session is `open` — see below |

The sender can **cancel** a `pending` or `deferred` request (`agentnet request cancel`,
[../protocol/request.md](../protocol/request.md#cancel-od-p1-11)). It then becomes `cancelled` (final), leaves `inbox`, shows under
`inbox --all` with the sender's reason, and a `request.cancelled` notification fires. A
request you already accepted cannot be cancelled by the sender.

Anything else fails with `bad_state` and changes nothing (so answering a `cancelled`
request is `bad_state`). Each answer is queued to the
requester at once (under 2 s, even if the requester is offline) and logged with a timestamp.

## Work sessions (ticket 2.1b)

Since every `accept` opens a work session (Docs/protocol/work-session.md), `complete` on
a request whose session is `open` is a **shorthand for `agentnet result`**: the given
`--note` becomes `--notes`, the D14 result becomes the session result with
`--verification none`, and the request itself stays `accepted` — it only reaches
`completed` once the requester runs `agentnet accept-result`. In any other session state
`complete` is `bad_state`. See [session.md](session.md) for `sessions`, `session`,
`result`, `wait` and `accept-result`.

## Result

`complete` can return a result to the requester (owner decision D14,
[../protocol/request.md](../protocol/request.md#result-payload-d14)): a status, a one-line
summary, an exit code, a text output and artifact pointers. For example, after running tests:

```
$ go test ./... > test.log 2>&1; agentnet complete r-0123… --status fail --exit-code 1 \
      --summary "3 of 212 tests failed" --output-from-file test.log \
      --artifact "branch=fix/retry commit=1a2b3c4"
```

- `--summary`, `--exit-code`, `--output-from-file` and `--artifact` without `--status` are a
  usage error (exit 2).
- `--output-from-file` reads the file as UTF-8, turns CRLF into LF, and removes ANSI colour
  and cursor sequences (`ESC [` … a final byte `@`–`~`). Any other control character except
  tab, or invalid UTF-8, is `bad_request` (field `result.output`). An output over 32768 bytes
  is `bad_request`; nothing is truncated for you, so send the end of a long log
  (for example `tail -n 300 test.log`) and put the full log behind an `--artifact`.
- The whole completion (note and result) is at most 64 KiB once encoded. Over that gives
  `result_too_large`: shorten the output or send fewer artifacts.
- The result is sent to the requester only. It is never audited (only its sizes), never in a
  webhook, and a desktop notification shows at most the status.

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
  A completed request you returned a result for carries `result` **without `output`**, with
  `output_bytes` (`request show <id> --json` gives the full result).
- `accept`, `decline`, `defer`, `complete`: `{"ok": true, "request": <in view>, "mail_id"}`.

Error codes: `unknown_request`, `ambiguous_request`, `bad_state`, `result_too_large`, `unknown_team`,
`ambiguous_team`, `bad_request`, `daemon_not_running` (exit 3) and `usage` (exit 2).

## Audit

`request.accept`, `request.decline`, `request.defer` and `request.complete`, with `age_s`
([../protocol/request.md](../protocol/request.md#audit-and-metrics)). `request.complete`
with a result also logs `result_bytes`, `output_bytes` and the artifact count, never the
result itself.
