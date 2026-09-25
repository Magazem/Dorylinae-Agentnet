# `agentnet stop`

Asks the running `agentnetd` to shut down cleanly over the local IPC endpoint
([../protocol/ipc.md](../protocol/ipc.md#shutdown)) and waits for it to exit. This is the
same shutdown path as Ctrl+C or SIGTERM: the outbox is flushed, the database is closed and
an audit row is written. Use it instead of killing the process.

```
agentnet stop [--json]
```

| Flag | Meaning |
|------|---------|
| `--json` | Machine-readable output on stdout |

`agentnetd stop [--home DIR]` does the same, for when only `agentnetd` is on `PATH`.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Stopped |
| 1 | Unexpected error, or the daemon did not exit within 10 s of being asked |
| 2 | Usage error |
| 3 | Daemon not running |

## Human output

```
agentnetd stopped
```

Not running (stderr, exit 3):

```
agentnet: agentnetd is not running (endpoint: <path or pipe name>)
```

## `--json` output

Stopped (exit 0): `{"ok": true}`.

Error (stdout, non-zero exit):

```json
{"ok": false, "error": {"code": "daemon_not_running", "message": "agentnetd is not running (endpoint: ...)"}}
```

Error codes: `daemon_not_running` (exit 3), `stop_timeout` (exit 1, the daemon did not
exit within 10 s), `daemon_error` (exit 1), `usage` (exit 2).

## Related

[status.md](status.md) reports whether the daemon is running; `agentnetd` also takes
`--home <dir>` and `$DORYLINAE_HOME` to select the config directory, same as `stop`.
