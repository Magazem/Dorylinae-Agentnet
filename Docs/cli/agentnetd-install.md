# `agentnetd install` / `agentnetd uninstall`

Register `agentnetd` as a **per-user** service that starts at login, and remove it again.
No administrator rights or `sudo` are needed on any platform.

```
agentnetd install   [--home DIR] [--relay URL] [--relay-ca FILE] [--dry-run]
agentnetd uninstall [--home DIR] [--dry-run]
```

| Flag | Meaning |
|---|---|
| `--home DIR` | Config directory the service runs against (default `$DORYLINAE_HOME`, else the user config dir + `dorylinae`). It is baked into the service definition as `run --home DIR`, so the service does not depend on environment variables it will not inherit. |
| `--relay URL` | (`install` only) Relay the service connects to, e.g. `wss://relay.example.com`. Default `$DORYLINAE_RELAY_URL`; empty means no relay. Baked into the definition as `run ... --relay URL`, for the same reason as `--home`: a service started at login does not inherit your shell's `DORYLINAE_RELAY_URL`. `--relay ""` overrides the variable. To change the relay, re-run `install`. The [relay URL rule](agentnetd.md#relay-url-rule) applies: `ws://` to a host that is not loopback is refused (exit 2), and `DORYLINAE_ALLOW_INSECURE_RELAY` is **not** honoured here, because the service would not inherit it. |
| `--relay-ca FILE` | (`install` only) PEM CA certificate(s) for a `wss://` relay with a private or self-signed certificate. Checked (it must hold a certificate, else exit 2) and copied to `relay_ca.pem` in the config directory (mode 0600) before the service starts; `agentnetd run` uses that file whenever `--relay-ca` is not given, for the relay connection only, in addition to the system roots. Re-running `install` without `--relay-ca` keeps an existing `relay_ca.pem`; delete it to stop trusting that CA. `--dry-run` lists the copy without making it. |
| `--dry-run` | Print every file that would be written and every command that would be run, then exit 0 without touching the machine (no audit event, no config directory created). |

`install` also starts the daemon immediately; it does not wait for the next login. Re-running
`install` replaces the definition (idempotent). `uninstall` stops the daemon, removes the
definition, and succeeds when nothing is installed ("nothing to do").

The service runs the binary that ran `install` (symlinks resolved): `agentnetd install` records
that path, so move or rebuild the binary and re-run `install`.

Exit codes: 0 success, 1 failure, 2 usage error (including a remote `ws://` relay and a bad `--relay-ca`).

## What each platform gets

| OS | Mechanism | Definition | Started with |
|---|---|---|---|
| macOS | launchd agent `dev.dorylinae.agentnetd` | `~/Library/LaunchAgents/dev.dorylinae.agentnetd.plist` (`RunAtLoad`, `KeepAlive` on failure, `--log-file <home>/agentnetd.log`, stdout and stderr to `<home>/agentnetd.out.log`) | `launchctl bootstrap gui/<uid>` |
| Linux | systemd user unit `agentnetd.service` | `$XDG_CONFIG_HOME` (default `~/.config`)`/systemd/user/agentnetd.service` (`Restart=on-failure`, `WantedBy=default.target`) | `systemctl --user enable --now` |
| Windows | Task Scheduler task `Dorylinae agentnetd` | registered from an XML definition (written to `<home>\agentnetd-task.xml` for the `schtasks /Create` call, then deleted) | `schtasks /Run` |

### Logs

| OS | Where the daemon's log goes |
|---|---|
| macOS | The plist runs `agentnetd run --home <home> [--relay URL] --log-file <home>/agentnetd.log`, rotated at 1 MiB to `agentnetd.log.1` as on Windows. launchd sends stdout and stderr to a separate `<home>/agentnetd.out.log` (see below) |
| Linux | The systemd journal: `journalctl --user -u agentnetd` |
| Windows | Task Scheduler keeps no output, so the task runs `agentnetd run --home <home> [--relay URL] --log-file <home>\agentnetd.log`. The daemon rotates that file at 1 MiB to `agentnetd.log.1` (one generation), see [agentnetd.md](agentnetd.md) |

The macOS plist and the Windows task pass `--log-file`; the systemd unit does not.

**Why macOS rotates (R55-F14, D49; review 55 R55-016).** Before R55-F14, launchd wrote the
whole log to `<home>/agentnetd.log` and nothing rotated it. That was chosen with only the
user's own activity in mind. But a relay can make the daemon log, so the file could grow
until the disk was full. The daemon now writes its log itself, rotated, and it
[limits relay-driven lines](../protocol/envelope.md#relay-driven-log-lines-daemon).

`agentnetd.out.log` gets only what the daemon writes to stdout and stderr outside its log:
the `listening on` line at each start, the insecure-relay notice, a fatal start error and a
Go runtime crash report. None of it is written per frame. The one way it grows fast is a
crash loop: launchd restarts the daemon at most every 10 s, and each Go crash report can be
tens of KB. That needs a daemon bug, but a relay that finds a panic in the receive path
could trigger it at will, so (OD-F14-3 (e), recommended in review 72b) the daemon bounds
the file too: at start, if its stderr is a regular file named `agentnetd.out.log` in the
config directory and that file is over 1 MiB, it renames it to `agentnetd.out.log.1`
(replacing an older one). launchd opens `StandardOutPath` and `StandardErrorPath` again
each time it starts the job, so the next start writes a fresh file; the current run keeps
writing to `.1`. The latest crash report is therefore always in one of the two files, and
together they stay near 2 MiB plus one run's output. (The implementer confirms the
reopen-per-start behaviour on a Mac once; if launchd kept the descriptor, the rename would
still be harmless and only the bound would be lost.)

It gets its own file because the daemon's stdout and stderr are descriptors that launchd
opened on that path at start, which the log rotation cannot move. If both wrote one file,
the rotation would leave stdout and stderr writing into `agentnetd.log.1`, which the next
rotation deletes, and the size count would miss those writes.

**Existing installs.** A plist written before R55-F14 keeps the old behaviour until
`agentnetd install` is run again (see [Behaviour to know about](#behaviour-to-know-about)).
Until then the file is still unrotated, but the new binary already
[limits relay-driven lines](../protocol/envelope.md#relay-driven-log-lines-daemon), so a
flood adds a few MB a day, not GBs an hour. After the re-install, the daemon's first log
line moves the old, possibly large `agentnetd.log` to `agentnetd.log.1` if it is over
1 MiB. The next rotation deletes it; that may take weeks at a normal rate, so delete
`agentnetd.log.1` by hand to reclaim the space at once.

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
  `service.uninstall` to the audit log (actor `cli`, detail `{"platform", "custom_home",
  "changed"}`: no paths, because the audit log never carries them; `custom_home` says whether
  `--home` was given, and for uninstall `changed` says whether anything was removed). A failed
  run writes none. (Before 3.6b the detail also carried `home` and `executable`.)
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
