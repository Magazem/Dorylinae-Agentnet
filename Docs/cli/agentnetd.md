# `agentnetd`

The AgentNet daemon: the local coordination service that `agentnet` talks to. It also
registers itself as a per-user service ([agentnetd-install.md](agentnetd-install.md)).

```
agentnetd [run] [--home DIR] [--relay URL] [--relay-ca FILE] [--log-file PATH] [--version]
agentnetd install   [--home DIR] [--relay URL] [--relay-ca FILE] [--dry-run]
agentnetd uninstall [--home DIR] [--dry-run]
agentnetd stop      [--home DIR]
agentnetd version   [--json]
```

`run` is the default; `agentnetd --relay URL` and `agentnetd run --relay URL` are the same.

| Flag | Default | Meaning |
|------|---------|---------|
| `--home DIR` | `$DORYLINAE_HOME`, else the user config dir + `dorylinae` | Config directory: database, IPC endpoint, identity key file, mailbox keys |
| `--relay URL` | `$DORYLINAE_RELAY_URL`, else none | Relay to keep a persistent connection to, e.g. `ws://127.0.0.1:8787` or `wss://relay.example.com`. Empty runs the daemon without a relay ([relay.md](relay.md#daemon-side)). A remote relay must use `wss://` (see [Relay URL rule](#relay-url-rule)) |
| `--relay-ca FILE` | `relay_ca.pem` in the config directory, if `install --relay-ca` stored one; else the system roots only | PEM CA certificate(s) trusted for the relay connection only, in addition to the system roots: for a self-hosted relay with a private or self-signed certificate. Go ignores `SSL_CERT_FILE` on Windows and macOS, so this is the supported way in. A file with no certificate is a usage error |
| `--log-file PATH` | none (log to stderr) | Append log lines to PATH instead of stderr. When the file would pass 1 MiB it is renamed to `PATH.1` (replacing an older one) and a new file is started, so the log stays under about 2 MiB. Created with owner-only permissions. The Windows scheduled task passes `--log-file <home>\agentnetd.log`; launchd redirects output to the same file itself, and systemd logs to the journal |
| `--version` | | Print the version and exit |
| `--help`, `-h` | | Print usage and exit |

On start, one line on stdout: `agentnetd listening on <endpoint> (db: <path>)`. Logs are `slog`
text lines and never contain message payloads, bodies or key material.

## Relay URL rule

`agentnetd` refuses, at start and at `install`, a relay URL with scheme `ws://` whose host is
not loopback (`localhost`, `127.0.0.0/8`, `::1`), exiting 2 with
`a remote relay must use wss:// (got "<url>")`. A URL with user info is refused too.

For LAN tests only, `DORYLINAE_ALLOW_INSECURE_RELAY=1` lets `agentnetd run` use a remote
`ws://` relay; it prints a warning on stderr and in the log at every start. `install` does
not honour it (the service would not inherit the variable).

Whatever the URL, the daemon never follows an HTTP redirect from it (a redirect is a
connection error), and for a host that is not loopback it signs only relay auth v2, which
names the relay: a relay that does not offer v2 gets no signature and the connection fails
with "relay does not support auth v2" ([../protocol/envelope.md](../protocol/envelope.md#auth-daemon---relay)).

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
| `DORYLINAE_ALLOW_INSECURE_RELAY` | `1` lets `run` use a remote `ws://` relay (LAN tests; warned at every start). See [Relay URL rule](#relay-url-rule) |
| `DORYLINAE_KEYSTORE` | Where the identity key lives: `auto` (default: OS keychain, then file) or `file` ([../protocol/agent-card.md](../protocol/agent-card.md#key-storage)) |
| `DORYLINAE_DEBUG` | `1` registers the debug mail kind `note` ([mail.md](mail.md)). Not for production |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Stopped cleanly (SIGINT/SIGTERM), or `--help` / `--version` |
| 1 | Could not start or serve (for example `--log-file` cannot be opened) |
| 2 | Usage error, including a remote `ws://` relay URL and an unreadable or certificate-less `--relay-ca` |
| 3 | Another `agentnetd` is already running for this home (see above) |

`agentnetd stop`'s own exit codes (0 stopped, 1 error, 2 usage, 3 not running) are in
[stop.md](stop.md).
