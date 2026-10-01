# `agentnet doctor`

Checks this machine's AgentNet setup: the CLI/daemon versions, the config directory's
ownership, where the identity key lives, whether the per-user service is installed and
running, whether the local IPC socket is reachable, the relay connection, git, the local
clock and the stored peer cards. Never needs `agentnetd` to be healthy to run: checks that do not need the daemon
(`config`, `keychain`, `git`, `relay`, `clock`, `peers`) still run with it stopped.

```
agentnet doctor [--json]
```

| Flag | Meaning |
|------|---------|
| `--json` | Machine-readable output on stdout |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | No check failed (a `warn` or `skip` still exits 0) |
| 1 | At least one check failed |
| 2 | Usage error |

## Checks

| id | What it checks | Needs the daemon? |
|----|-----------------|--------------------|
| `binary` | `agentnet` and `agentnetd` report the same version, and it is not older than the relay's `min_client` (4.4a; from the daemon's `status`, so only once it has connected). A dev build cannot be compared: `warn` | yes (`skip` otherwise) |
| `program` | The `agentnetd` binary next to `agentnet`, and its folder, cannot be changed by other users (same check as `agentnetd install`, which refuses otherwise); `warn` with a fix when they can; skipped when no sibling binary exists | no |
| `config` | The config directory exists and is owner-only (D24/L11); a drive-root ACL like `Authenticated Users:(M)` is a `warn` with a fix | no |
| `keychain` | The identity key is readable, and from where (OS keychain or the owner-only file fallback) | no |
| `service` | The per-user service (Task Scheduler task / systemd user unit / launchd agent) is installed and running | fails cleanly without it |
| `socket` | The local IPC endpoint answers within 1 second; on macOS, its path is under the 104-byte `AF_UNIX` limit | fails cleanly without it |
| `relay` | The relay URL follows the URL rule (loopback `ws://`, remote `wss://`); with the daemon up, its own reported connection state; with it down, an unauthenticated probe of the relay's challenge | no (probes directly if down) |
| `account` | Not implemented yet: always `skip`. Added by ticket 4.2c (bound/unbound/suspended, quota group, quota state) | — |
| `git` | Git is at least 2.32 (D23); older is a `warn`, not a `fail` (`git.read` grants are refused, but `fs` grants and everything else still works) | no |
| `clock` | The local clock is within 2 minutes of the relay's (estimated from the challenge's `expires`) | no (probes directly) |
| `peers` | Every stored peer Agent Card still verifies under the current card rules (review 68 OD-3). The daemon keeps a card that does not at start and logs it; this row names it by public key as `warn`, with the fix: re-pair, `agentnet peers remove <key>`, or `agentnet team remove <team> <key>`. A card that verifies only under the legacy text rule (a bidi control or a line separator in a text member, allowed before R55-F10, [agent-card.md](../protocol/agent-card.md#cards-stored-before-r55-f10)) is not a failure: the row stays `ok` with the detail `every stored peer card verifies (N with characters refused at new pairings since R55-F10; shown escaped)`. No database yet: `skip` | no (reads the database read-only) |

`relay` and `clock` never authenticate to the relay with the identity key: the relay keeps
one connection per key and replaces the older one, so a doctor login would kick the running
daemon's own connection, and any envelope in flight during that window could be lost or
misdirected. Instead, doctor dials the relay, reads its opening `challenge` frame (the auth
versions it offers and the nonce's expiry) and closes without ever answering it. With
`agentnetd` already connected, `relay` prefers the daemon's own reported state over dialling
again; `clock` always does its own dial, since the daemon does not keep the challenge frame
from its last handshake.

With no relay configured (no `--relay` given to `agentnetd install`, and
`$DORYLINAE_RELAY_URL` unset when the daemon is stopped), `relay` and `clock` report `skip`.

## Output

No path outside the config directory's own display is ever printed (paths inside it are
shown relative to `~`, per `config`'s row above), and no peer name appears in any check.

Human output, one line per check:

```
binary    ok   agentnet and agentnetd versions match
config    ok   ~/.config/dorylinae exists and is owner-only
keychain  ok   identity key readable from keychain
service   ok   installed and running
socket    ok   reachable
relay     ok   connected, auth v2
account   skip account support is added by a later ticket (4.2c)
git       ok   git 2.32 or newer
clock     ok   within 2 minutes of the relay
peers     ok   every stored peer card verifies
```

A `warn` or `fail` row carries a `(fix: ...)` suffix with a one-command or one-sentence fix.

With the daemon up but not connected, the `relay` row is `warn` with detail
`not connected: <last_error>`, the daemon's content-free `last_error` of
[status.md](status.md) (R55-F9). doctor applies `displayLine(…, 256)` to it again
([approval.md §Sanitising](../protocol/approval.md#sanitising-one-character-rule-two-renderings)),
in the human output and in `--json` `detail` alike. No relay-chosen text reaches a doctor row.
The probe path (daemon down) never shows the probe's error text: it reports `cannot reach the
relay`.

## `--json` output

```json
{"ok": true, "checks": [
  {"id": "binary", "state": "ok", "detail": "agentnet and agentnetd versions match"},
  {"id": "config", "state": "ok", "detail": "~/.config/dorylinae exists and is owner-only"},
  {"id": "keychain", "state": "ok", "detail": "identity key readable from keychain"},
  {"id": "service", "state": "ok", "detail": "installed and running"},
  {"id": "socket", "state": "ok", "detail": "reachable"},
  {"id": "relay", "state": "ok", "detail": "connected, auth v2"},
  {"id": "account", "state": "skip", "detail": "account support is added by a later ticket (4.2c)"},
  {"id": "git", "state": "ok", "detail": "git 2.32 or newer"},
  {"id": "clock", "state": "ok", "detail": "within 2 minutes of the relay"}
]}
```

Each check is `{"id": ..., "state": "ok"|"warn"|"fail"|"skip", "detail": ..., "fix": ...}`;
`fix` is present only on `warn` and `fail` rows. `ok` at the top level is `false` when any
check's `state` is `"fail"`.

## Related

`agentnet status` (`Docs/cli/status.md`) reports the same relay connection state (`relay`
object) whenever the daemon is running, without the extra checks here. `agentnet setup`
(ticket 4.9a) runs `doctor` as its last step.
