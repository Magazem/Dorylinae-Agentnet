# `agentnet status`

Reports whether the local daemon (`agentnetd`) is running, its PID, uptime and how much
mail is waiting in its outbox.
Returns within 2 seconds even when the daemon is unresponsive.

```
agentnet status [--json]
```

| Flag | Meaning |
|------|---------|
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
```

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

Error (stdout, non-zero exit):

```json
{"ok": false, "error": {"code": "daemon_not_running", "message": "agentnetd is not running (endpoint: ...)"}}
```

Error codes: `daemon_not_running` (exit 3), `daemon_error` (exit 1), `usage` (exit 2).

## Related

`agentnetd` (the daemon) takes `--home <dir>` to override the config directory;
both binaries also honour the `DORYLINAE_HOME` environment variable. See
`Docs/protocol/ipc.md`.
