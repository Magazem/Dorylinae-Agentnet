# `agentnetd`

The AgentNet daemon: the local coordination service that `agentnet` talks to. It also
registers itself as a per-user service ([agentnetd-install.md](agentnetd-install.md)).

```
agentnetd [run] [--home DIR] [--relay URL] [--log-file PATH] [--version]
agentnetd install   [--home DIR] [--relay URL] [--dry-run]
agentnetd uninstall [--home DIR] [--dry-run]
agentnetd stop      [--home DIR]
agentnetd version   [--json]
```

`run` is the default; `agentnetd --relay URL` and `agentnetd run --relay URL` are the same.

| Flag | Default | Meaning |
|------|---------|---------|
| `--home DIR` | `$DORYLINAE_HOME`, else the user config dir + `dorylinae` | Config directory: database, IPC endpoint, identity key file, mailbox keys |
| `--relay URL` | `$DORYLINAE_RELAY_URL`, else none | Relay to keep a persistent connection to, e.g. `ws://127.0.0.1:8787`. Empty runs the daemon without a relay ([relay.md](relay.md#daemon-side)) |
| `--log-file PATH` | none (log to stderr) | Append log lines to PATH instead of stderr. When the file would pass 1 MiB it is renamed to `PATH.1` (replacing an older one) and a new file is started, so the log stays under about 2 MiB. Created with owner-only permissions. The Windows scheduled task passes `--log-file <home>\agentnetd.log`; launchd redirects output to the same file itself, and systemd logs to the journal |
| `--version` | | Print the version and exit |
| `--help`, `-h` | | Print usage and exit |

On start, one line on stdout: `agentnetd listening on <endpoint> (db: <path>)`. Logs are `slog`
text lines and never contain message payloads, bodies or key material.

`stop` and `version` are also available as `agentnet stop` / `agentnet version`
([stop.md](stop.md)); `agentnetd stop`/`agentnetd version` are the same, for when only
`agentnetd` is on `PATH`.

## Two daemons, one home

Starting a second `agentnetd` for a home directory already in use (another `agentnetd`, or
a stuck listener) does not silently fail: it detects the endpoint is taken (the named pipe
on Windows, the Unix socket elsewhere) and prints

```
agentnetd: agentnetd is already running for <home> (pid <pid>)
```

exiting 3. The pid comes from asking the running daemon's own `status` method, not from a
separate pid file. If that call does not answer in time the pid is omitted but the message
and exit code are unchanged.

## Environment

| Variable | Effect |
|----------|--------|
| `DORYLINAE_HOME` | Config directory, as `--home` |
| `DORYLINAE_RELAY_URL` | Relay URL, as `--relay` |
| `DORYLINAE_KEYSTORE` | Where the identity key lives: `auto` (default: OS keychain, then file) or `file` ([../protocol/agent-card.md](../protocol/agent-card.md#key-storage)) |
| `DORYLINAE_DEBUG` | `1` registers the debug mail kind `note` ([mail.md](mail.md)). Not for production |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Stopped cleanly (SIGINT/SIGTERM), or `--help` / `--version` |
| 1 | Could not start or serve (for example `--log-file` cannot be opened) |
| 2 | Usage error |
| 3 | Another `agentnetd` is already running for this home (see above) |

`agentnetd stop`'s own exit codes (0 stopped, 1 error, 2 usage, 3 not running) are in
[stop.md](stop.md).
