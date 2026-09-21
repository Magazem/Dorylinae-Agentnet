# `agentnetd install` / `agentnetd uninstall`

Register `agentnetd` as a **per-user** service that starts at login, and remove it again.
No administrator rights or `sudo` are needed on any platform.

```
agentnetd install   [--home DIR] [--relay URL] [--dry-run]
agentnetd uninstall [--home DIR] [--dry-run]
```

| Flag | Meaning |
|---|---|
| `--home DIR` | Config directory the service runs against (default `$DORYLINAE_HOME`, else the user config dir + `dorylinae`). It is baked into the service definition as `run --home DIR`, so the service does not depend on environment variables it will not inherit. |
| `--relay URL` | (`install` only) Relay the service connects to, e.g. `wss://relay.example.com`. Default `$DORYLINAE_RELAY_URL`; empty means no relay. Baked into the definition as `run ... --relay URL`, for the same reason as `--home`: a service started at login does not inherit your shell's `DORYLINAE_RELAY_URL`. `--relay ""` overrides the variable. To change the relay, re-run `install`. |
| `--dry-run` | Print every file that would be written and every command that would be run, then exit 0 without touching the machine (no audit event, no config directory created). |

`install` also starts the daemon immediately; it does not wait for the next login. Re-running
`install` replaces the definition (idempotent). `uninstall` stops the daemon, removes the
definition, and succeeds when nothing is installed ("nothing to do").

The service runs the binary that ran `install` (symlinks resolved): `agentnetd install` records
that path, so move or rebuild the binary and re-run `install`.

Exit codes: 0 success, 1 failure, 2 usage error.

## What each platform gets

| OS | Mechanism | Definition | Started with |
|---|---|---|---|
| macOS | launchd agent `dev.dorylinae.agentnetd` | `~/Library/LaunchAgents/dev.dorylinae.agentnetd.plist` (`RunAtLoad`, `KeepAlive` on failure, log to `<home>/agentnetd.log`) | `launchctl bootstrap gui/<uid>` |
| Linux | systemd user unit `agentnetd.service` | `$XDG_CONFIG_HOME` (default `~/.config`)`/systemd/user/agentnetd.service` (`Restart=on-failure`, `WantedBy=default.target`) | `systemctl --user enable --now` |
| Windows | Task Scheduler task `Dorylinae agentnetd` | registered from an XML definition (written to `<home>\agentnetd-task.xml` for the `schtasks /Create` call, then deleted) | `schtasks /Run` |

### Logs

| OS | Where the daemon's log goes |
|---|---|
| macOS | launchd redirects stdout and stderr to `<home>/agentnetd.log` (not rotated) |
| Linux | The systemd journal: `journalctl --user -u agentnetd` |
| Windows | Task Scheduler keeps no output, so the task runs `agentnetd run --home <home> [--relay URL] --log-file <home>\agentnetd.log`. The daemon rotates that file at 1 MiB to `agentnetd.log.1` (one generation), see [agentnetd.md](agentnetd.md) |

Only the Windows task passes `--log-file`.

Run `agentnetd install --dry-run` to see the exact plist, unit or task XML for your machine.

### Why a scheduled task on Windows, not a Windows service

A Windows service can only be registered by an administrator, and one running as `LocalSystem`
would not be the user whose config directory, named pipe DACL and SQLite database the daemon
uses. A Task Scheduler task with a logon trigger, whose principal is the current user with
`InteractiveToken` / `LeastPrivilege`, can be created without elevation and runs as that user.
Settings: one instance at a time (`IgnoreNew`), no execution time limit, no battery
restrictions, restart up to 3 times at 1 minute intervals on failure.

### Linux note

A systemd user unit runs while the user has a session. To start it at boot without logging in,
the user must run `loginctl enable-linger` themselves; `install` does not do this.

## Behaviour to know about

- **Audit:** a successful `install` / `uninstall` (not `--dry-run`) appends `service.install` /
  `service.uninstall` to the audit log (actor `cli`, detail: platform, home, and for install the
  executable; for uninstall `changed` says whether anything was removed). A failed run writes none.
- **Stopping is a hard stop.** On Windows, `uninstall` ends the task, which terminates the daemon
  without a graceful shutdown, so no `daemon.stop` audit row is written for that run (see the
  0.2a notes). launchd and systemd send SIGTERM, which the daemon handles gracefully.
- **Windows console.** The daemon is a console program, so the task gives it a console host
  (`conhost.exe`) in the user's session; a visible window may appear at logon, and closing it
  would end the daemon. A windowless launch is future work.
- Re-running `install` while the daemon is already running updates the stored definition but
  keeps the running process; it takes effect at the next start.
- `agentnet status` works exactly as in [status.md](status.md) whichever way the daemon started.

## Verifying

```
agentnetd install
agentnet status          # running, with PID and uptime
# log off and on again (or reboot)
agentnet status          # still running, new PID, small uptime
agentnetd uninstall
agentnetd uninstall      # "nothing to do", exit 0
```
