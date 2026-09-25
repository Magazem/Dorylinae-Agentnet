# `agentnet status`

Reports whether the local daemon (`agentnetd`) is running, its PID, uptime and how much
mail is waiting in its outbox.
Returns within 2 seconds even when the daemon is unresponsive.

```
agentnet status [--team TEAM] [--json]
```

| Flag | Meaning |
|------|---------|
| `--team TEAM` | Phase 1 (1.2): also list the members of `TEAM` (id or unique name) with their presence ([../protocol/presence.md](../protocol/presence.md)) |
| `--json` | Machine-readable output on stdout |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Daemon running |
| 1 | Unexpected error (daemon reachable but failed, or bad response) |
| 2 | Usage error |
| 3 | Daemon not running |

## Human output

```
agentnetd running
  pid:     4242
  uptime:  1m2s
  version: 0.0.0-dev
  outbox:  0 queued, 0 relayed, 0 expired
  presence: visible, relay connected
```

`--json` also carries `git`: `"ok"` when `git.read` grants can be issued, or
`"unsupported: <reason>"` when the resolved git is missing or older than 2.32 (D23,
[../protocol/grant.md](../protocol/grant.md#serving-git)); `fs` grants are unaffected either
way.

With `--team backend` (Phase 1), a member table follows:

```
team backend (t-0123…cdef), owner alice
NAME   DAEMON   AGENT   HUMAN    LAST SEEN
alice  online   active  present  now            (you)
bob    offline  -       -        2026-10-01T09:12:00Z
carol  online   idle    unknown  2026-10-01T09:40:31Z
```

`DAEMON` is `online` or `offline`. `AGENT` is `active` or `idle` (`-` when offline).
`HUMAN` is `present`, `away` or `unknown` (`-` when offline). A peer that is invisible to
you shows `offline` with the last time it was seen.

`outbox` counts the sender's mail by state ([../protocol/mail.md](../protocol/mail.md#outbox)):
`queued` (not yet handed to the relay), `relayed` (handed over, no ack yet) and `expired`
(no ack within 7 days: delivery unknown). Delivered and failed mail is not counted.

Not running (stderr, exit 3):

```
agentnet: agentnetd is not running (endpoint: <path or pipe name>)
```

## `--json` output

Running (exit 0):

```json
{"ok": true, "pid": 4242, "started_at": "2026-09-21T10:00:00Z", "uptime_seconds": 62.4, "version": "0.0.0-dev", "outbox": {"queued": 0, "relayed": 0, "expired": 0}}
```

From Phase 1 the object also has `presence` (own mode, relay state, own agent and human
flags), and with `--team` a `team` object whose `members[]` each carry `name`,
`public_key`, `fingerprint`, `self`, `owner`, `trust`, `daemon_online`, `agent_active`,
`human_present` (`true`, `false` or `null`), `last_seen`, `agent_last_active` and
`human_last_present`. The exact shape is in [../protocol/ipc.md](../protocol/ipc.md#status).

Error (stdout, non-zero exit):

```json
{"ok": false, "error": {"code": "daemon_not_running", "message": "agentnetd is not running (endpoint: ...)"}}
```

Error codes: `daemon_not_running` (exit 3), `daemon_error` (exit 1), `usage` (exit 2),
and with `--team`: `unknown_team`, `ambiguous_team` (exit 1).

## Related

`agentnetd` (the daemon) takes `--home <dir>` to override the config directory;
both binaries also honour the `DORYLINAE_HOME` environment variable. See
`Docs/protocol/ipc.md`.
