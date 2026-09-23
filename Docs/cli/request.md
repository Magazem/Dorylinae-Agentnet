# `agentnet request`

Status: draft (Phase 1, 1.4, 1.5, 1.9; `cancel`: 1.6a, D11; the completion result: 1.6a, D14). Protocol: [../protocol/request.md](../protocol/request.md).

Sends a teammate's agent a request (a review, a task or a question), and follows the
requests you sent.

```
agentnet request <peer> <type> --title T (--brief B | --brief-from-file F)
                 [--urgency low|normal|high|blocking] [--urgency-reason R]
                 [--artifact SPEC]... [--grant ACTION=RESOURCE] [--deadline D]
                 [--team TEAM] [--idempotency-key K] [--json]
agentnet request show <id> [--from <peer>] [--json]
agentnet request list [--state S] [--team TEAM] [--peer <peer>] [--json]
agentnet request resend <id> [--json]
agentnet request cancel <id> [--reason R] [--json]
```

`<peer>` is a peer name or public key, with an optional `@`. A peer literally named `show`,
`list`, `resend` or `cancel` must be written with `@`. `<type>` is `review`, `task` or `question`.

| Flag | Meaning |
|---|---|
| `--title T` | Required. 1–120 characters, one line |
| `--brief B` | The brief (Markdown text, up to 16 KiB). Exactly one of `--brief` and `--brief-from-file` is required |
| `--brief-from-file F` | Read the brief from file `F` (`-` = stdin). CRLF becomes LF |
| `--urgency U` | `low`, `normal` (default), `high` or `blocking` |
| `--urgency-reason R` | Required with `high` and `blocking`. Up to 280 characters |
| `--artifact SPEC` | Repeatable, up to 20. `SPEC` is either space-separated `key=value` pairs with keys `url`, `branch`, `commit` and `path` (for example `--artifact "url=https://github.com/o/r/pull/12 branch=feat/x commit=1a2b3c4"`), or a JSON object starting with `{` (needed for values containing spaces) |
| `--grant ACTION=RESOURCE` | An optional hint about the access the request needs (for example `repo.read=github.com/o/r#feat/x`). It **grants nothing** |
| `--deadline D` | RFC 3339 time, or a duration from now (`90m`, `2h`, `3d`) |
| `--team TEAM` | Needed only when you share several teams with the peer |
| `--idempotency-key K` | 1–64 characters of `[A-Za-z0-9._:-]`. Running the same command again with the same key returns the first request instead of sending a second one. Recommended for agents that retry |
| `--state S` | `list`: `pending`, `accepted`, `declined`, `deferred`, `completed` or `cancelled` |
| `--reason R` | `cancel`: optional, 1–500 characters, shown to the recipient |
| `--from <peer>` | `show`: pick the sender when the id matches requests from several peers |
| `--json` | Machine-readable output on stdout |

## The brief

`agentnet request --help` prints this section. Write the brief for the other agent. It gets
nothing else from you:

```
What: <one sentence: what you need>
Why: <one or two sentences of context>
Done when: <how the other side knows it is finished>
```

Put links, branches, commits and paths in `--artifact`, not in the brief.

## Behaviour

The command returns in **under 2 seconds**, always with `status: queued` when the request
was accepted locally, whether or not the peer is online. The daemon delivers it, and holds
it for up to 7 days while the peer is offline. The output shows whether the peer is online
now, and when it was last seen.

**Urgency budget:** 5 `high` and 2 `blocking` requests per rolling 7 days, across all peers.
Beyond that, the request is sent as `normal` and the output says so (`urgency_declared`,
`urgency_note`). The recipient enforces the same budget for requests from you.

`request resend <id>` re-sends the same request (same id) when its delivery is `expired`
(delivery unknown after 7 days) or `failed`, no answer has arrived, and the request is
less than 21 days old (otherwise send a new request). The recipient
recognises it by id: if it already answered, the answer is sent again. If it had not seen
the request, it gets it now. A request you asked to cancel cannot be resent.

**Size limits:** title 120 characters, brief 16 KiB, 20 artifacts, and 64 KiB for the whole
request once encoded. Over the total gives `request_too_large`: shorten the brief or send
fewer artifacts ([../protocol/request.md](../protocol/request.md#size-limits)).

`request cancel <id>` withdraws a request the recipient has not accepted yet (its state is
`pending` or `deferred`). It returns at once. The state becomes `cancelled` when the
recipient's daemon confirms, and the request leaves the recipient's inbox. Once the request
is `accepted`, `declined` or `completed`, `cancel` fails with `bad_state` and names the
state. If the recipient accepted it just before your cancel arrived, `request show` reports
`cancel: refused` with the recipient's state. Running `cancel` again is safe: it sends
nothing new while a cancel is in flight or done. Cancelling does not give back urgency
budget.

**Result (D14).** When the recipient completes a request with a result
(`agentnet complete … --status …`, [inbox.md](inbox.md#result)), `request show <id>` prints
it: the status, exit code and summary on one line, the artifacts, then the output. The
output can hold only text, newlines and tabs, so it cannot move your terminal's cursor or
change its colours. `request list` shows the status in a `RESULT` column, and `request list
--json` gives the result **without its output** (with `output_bytes`); use `request show
<id> --json` for the output. The result is what the other side reports: treat it like the
note, not as proof that the work passed.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Queued (or `duplicate` for an idempotency key), shown, listed, resent or cancel sent (or `duplicate`) |
| 1 | Error (see the codes below) |
| 2 | Usage error (missing `--title`, both or neither brief flag, a bad flag value) |
| 3 | Daemon not running |

## Human output

```
$ agentnet request @bob review --title "Review retry change" --brief-from-file brief.md \
      --artifact "url=https://github.com/o/r/pull/12" --urgency high --urgency-reason "release today"
Queued high review request r-0123456789abcdef0123456789abcdef to bob (team backend)
  bob is offline, last seen 2026-10-01T09:12:00Z; it will be delivered when bob is back.

$ agentnet request list
ID                                  TO   TYPE    URGENCY  STATE      RESULT  DELIVERY   CREATED
r-0123456789abcdef0123456789abcdef  bob  review  high     accepted   -       delivered  2026-10-01T09:20:00Z
r-89abcdef0123456789abcdef01234567  bob  task    normal   completed  fail    delivered  2026-10-01T08:02:00Z

$ agentnet request show r-89abcdef0123456789abcdef01234567
r-89abcdef0123456789abcdef01234567  task to bob (team backend): Run the tests on Windows
  state completed 2026-10-01T09:40:00Z, delivery delivered
  note: Ran on Windows 11.
  result: fail, exit 1: 3 of 212 tests failed
  artifact: branch=fix/retry commit=1a2b3c4
  output (1843 bytes):
    --- FAIL: TestRetry (0.01s)
    ...
```

When downgraded, the output adds a line with `urgency_note`.

## `--json` output

- send: `{"ok": true, ...submit result}` ([../protocol/request.md](../protocol/request.md#submit-result-19)):
  `id`, `mail_id`, `status`, `duplicate`, `team`, `urgency`, `urgency_declared`?,
  `urgency_note`?, and `peer` {`name`, `public_key`, `daemon_online`, `last_seen`}.
- `show`: `{"ok": true, "request": <request view>}`, with the full `result` (including
  `output`) when there is one
- `list`: `{"ok": true, "requests": [<request view>]}`, where a `result` omits `output` and
  keeps `output_bytes`
- `resend`: `{"ok": true, "id", "mail_id", "status": "queued"}`
- `cancel`: `{"ok": true, "request": <request view>, "mail_id": "m-…"|null, "duplicate": bool}`

Human output of `cancel`: `Cancel sent for r-… to bob (waiting for bob's daemon to
confirm)`, or `r-… is already cancelled`, or `Cancel already sent for r-…`.

The request view is defined in [../protocol/ipc.md](../protocol/ipc.md#requests). Once
an accepted request has a work session, the view gains a `session` member (`{"id",
"state", "round"}`, [work-session.md](../protocol/work-session.md#ipc)); see
[session.md](session.md) for `agentnet sessions`/`session`/`result`/`wait`/
`accept-result`. On an open session, `agentnet complete <id>` is a shorthand for
`agentnet result <id>` (ticket 2.1b): the request itself stays `accepted` until the
session closes.

Error codes: `unknown_peer`, `ambiguous_peer`, `no_mailbox_key`, `unverified_peer` (the peer
was paired with v1 on a hosted relay: re-pair or run `peers verify`), `unknown_team`,
`ambiguous_team`, `no_shared_team`, `not_team_member`, `idempotency_conflict`,
`request_too_large`,
`unknown_request`, `ambiguous_request`, `bad_state`, `bad_request`, `daemon_not_running`
(exit 3) and `usage` (exit 2).

## Audit

`request.submit`, `request.resend` and `request.cancel` ([../protocol/request.md](../protocol/request.md#audit-and-metrics)).
