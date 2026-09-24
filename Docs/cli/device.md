# `agentnet device`

Links two of your own devices, a **controller** and a **helper**
([Docs/protocol/device.md](../protocol/device.md), owner decision D13). Introduced by
ticket 2.D1 (link, list, unlink); `device scope` and helper runs by 2.D2.

```
agentnet device link <peer> --as controller|helper --fingerprint FP [--json]
agentnet device list [--json]
agentnet device unlink <peer> [--json]
agentnet device scope <controller> --types T[,T] --repo LABEL=PATH... --command 'NAME=REPO:ARGV-JSON'...
                      --expires D [--timeout NAME=SECONDS]... [--env NAME=VAR]... [--json]
agentnet device scope <controller> --from-file scope.json [--json]
agentnet device scope <controller> --clear|--show [--json]
```

A link never comes from pairing, a team, a roster or `peers verify`. It exists only
after `device link` was run and **confirmed by a human on both devices**, each
typing the code of its own [approval window](../protocol/approval.md) and giving the
**other** device's fingerprint. A link by itself does nothing: no request runs on a
helper until its owner sets a scope on the helper (2.D2). Nothing is on by default.

`<peer>` is a public key, a unique peer name or `@name`; the device must already be
paired.

## `device link`

Run it **on both devices**: on the helper with `--as helper`, on the controller with
`--as controller`, each with the other device's fingerprint (`agentnet identity` on
the other machine, in any case, with or without spaces).

| Flag | Meaning |
|------|---------|
| `--as ROLE` | This device's role: `controller` or `helper` |
| `--fingerprint FP` | The other device's 20-character fingerprint |
| `--json` | Machine-readable output |

The fingerprint is compared in constant time. A mismatch exits 1 with
`fingerprint_mismatch` and changes nothing. A match, once the approval is confirmed,
sets the peer's trust to `fingerprint` (exactly `peers verify`). The command creates
an approval (kind `device_link`) and returns at once; confirm it in the AgentNet
approval window (reopen it with `agentnet approve --open <a-id>`). After the
confirmation this device is `waiting`; the link is `active` on each device once it
holds its own confirmation and the other's, in either order, within 10 minutes. Both
devices then show the same link id. An attempt that is not completed within 10 minutes
lapses (`revoked`) and can be repeated. Each device shows a desktop notification when its link becomes active, `<name> is now linked as your helper` (or `controller`); switch it off with `agentnet notify --event device.linked=off`. A confirmation that reaches the other device more than 10 minutes after it was made, by its sender's clock, or that was made before that device's last unlink, is ignored.

Hierarchy: the link is one-way and one level deep. A reverse link (the controller
linking as helper of its own helper) and a chain (a helper controlling a third device, a
controller being controlled) are refused with `device_cycle`. A helper has at most one
controller and a controller at most 8 helpers (`bad_state` beyond that).

Human output:

```
Approval a-0123456789abcdef0123456789abcdef pending to link as helper with laptop. Type the code in the AgentNet approval window (reopen it with 'agentnet approve --open a-0123456789abcdef0123456789abcdef').
The link is active once the other device has confirmed too.
```

`--json`:

```json
{"ok": true,
 "approval": {"id": "a-0123456789abcdef0123456789abcdef", "kind": "device_link", "state": "pending", "...": "..."},
 "link": {"id": "i-0123456789abcdef0123456789abcdef", "peer": {"name": "laptop", "public_key": "..."},
          "role": "helper", "state": "pending_approval"}}
```

Failures: `fingerprint_mismatch`, `bad_fingerprint`, `device_cycle`, `bad_state` (a link or
attempt with this peer already exists, or the controller/helper limit), `unverified_peer`
(trust `relay` on a non-loopback relay), `unknown_peer`, `approval_limit`,
`approval_locked`, `approval_unavailable`, `daemon_not_running`, `timeout`, `usage`.

## `device list`

Lists this device's links and attempts (never the other device's).

```
ID                                  PEER    ROLE    STATE   ACTIVATED
l-ee417e25d0625afad79d54cd3539dd64  laptop  helper  active  2026-10-01T09:00:03Z
```

`--json`:

```json
{"ok": true, "links": [
  {"id": "l-ee417e25d0625afad79d54cd3539dd64", "peer": {"name": "laptop", "public_key": "..."},
   "role": "helper", "state": "active", "activated_at": "2026-10-01T09:00:03Z"}]}
```

`role` is this device's role. `state` is `pending_approval` (the approval is not yet
confirmed; the id is `i-…`), `waiting` (this device confirmed, the other has not, id
`i-…`), `active` (id `l-…`) or `revoked`. `links` is an empty array when there are none.

## `device unlink`

Ends the link with a peer, on **either** device. No approval is needed: removing
authority is always allowed. Every link, attempt and kept offer with that peer ends
here, a helper's scope for it is deleted, and the peer is told with a `device.unlink`
mail, which ends its side too. The mail is sent **even if this device holds no active
link** with the peer, because each side activates on its own: one side can be `active`
while the other's attempt lapsed before the peer's confirmation arrived. A peer can only
ever end its own links; a `device.unlink` from anyone else changes nothing. A run that
is already executing on a helper finishes; queued runs are dropped (2.D2).
`agentnet peers remove` of the other device also ends the link on this device.

Human output:

```
Unlinked laptop (l-ee417e25d0625afad79d54cd3539dd64); the other device was told.
```

or `No link with that device here; it was told to end any link on its side.`

`--json`: `{"ok": true, "link": {...}, "mail_id": "..."}`. `link` is absent when this
device held no link or attempt with the peer.

Failures: `unknown_peer`, `daemon_not_running`, `timeout`, `usage`.

## `device scope`

Run it on the **helper**. It sets, clears or shows what the controller may run here
([device.md §Scope](../protocol/device.md#scope-held-by-the-helper-only)). The scope is
stored only on this device and is never sent anywhere.

| Flag | Meaning |
|------|---------|
| `--types T[,T]` | Request types that may run: `review`, `task`, `question` (1–3) |
| `--repo LABEL=PATH` | Repeatable, 1–16. `LABEL` 1–64 of `[a-z0-9._-]`; `PATH` an existing absolute directory, not the home directory, the AgentNet config dir (nor inside or containing it) or a filesystem root |
| `--command 'NAME=REPO:ARGV-JSON'` | Repeatable, 1–32. `NAME` 1–64 of `[a-z0-9._-]`, `REPO` one of the labels, `ARGV-JSON` a JSON array of 1–64 strings (each 1–4096 bytes), e.g. `'test=agentnet:["go","test","./..."]'` |
| `--timeout NAME=SECONDS` | Repeatable: the command's timeout, 1–3600 (default 900) |
| `--env NAME=VAR` | Repeatable: pass the daemon's `VAR` to command `NAME` too (up to 32 per command, never `DORYLINAE_*`) |
| `--expires D` | Required: an RFC 3339 time or a duration from now (`90m`, `12h`, `7d`), at most 30 days |
| `--from-file F` | The whole scope as JSON (the object of device.md §Scope) instead of the flags |
| `--clear` | Remove the scope. No approval; queued runs are dropped, a running one finishes |
| `--show` | Print the stored scope |
| `--json` | Machine-readable output |

Setting a scope creates an approval (kind `device_scope`). The program (`argv[0]`) is looked
up **now**, on this device's `PATH`, and stored as an absolute path, so a later `PATH`
change cannot swap it; a `.bat` or `.cmd` file is refused on Windows (it would run through
`cmd.exe`). The CLI prints the scope as it will be stored, and the approval window shows
every command with its repo path and full resolved argv: approve only what you set yourself.
A new scope replaces the old one and rejects an older one still waiting for its code.

A request from the controller runs only if it names one of the commands
(`agentnet request <helper> task --run NAME ...`, see [request.md](request.md)), its type is
in `--types`, it was sent after the link became active, the scope has not expired, and the
limits allow it (one run at a time, 8 queued, 60 per day). It is accepted by the daemon, the
command runs with no shell, in its repo, stdin empty, with only `PATH`, `HOME`/`USERPROFILE`,
`TMP`/`TEMP`/`TMPDIR`, `LANG`, `LC_ALL`, the Windows system variables (`SystemRoot`,
`SystemDrive`, `windir`, `ComSpec`, `PATHEXT`, `LOCALAPPDATA`, `APPDATA`) and the `--env`
names, and is killed with its whole process tree at its timeout. The controller gets a
[result](../protocol/work-session.md#result-object-26) with `pass` (exit 0) or `fail`, the
exit code and the last 32 KiB of output (ANSI sequences removed, other control characters
shown as `?`); it reads it with `agentnet wait` and closes it with `accept-result`. Anything
else lands in this device's normal inbox, as any request does.

Only name repositories you trust: running a repository's tests runs its code, just as if
you ran them by hand.

Human output (set):

```
This scope will be stored once you approve it:
Types: task
Expires: 2026-10-08T09:00:00Z
  test  in agentnet (/home/alice/src/agentnet)
      runs ["/usr/local/go/bin/go","test","./..."], timeout 900 s, env GOFLAGS
Approval a-0123456789abcdef0123456789abcdef pending. Type the code in the AgentNet approval window (reopen it with 'agentnet approve --open a-0123456789abcdef0123456789abcdef').
```

`--json`: set `{"ok": true, "approval": {...}, "scope": {...}}` (the scope as it will be
stored); clear `{"ok": true, "link": {...}}`; show `{"ok": true, "scope": {...}}`.

Failures: `unknown_link` (no active link with that device), `not_helper` (this device is
the controller), `bad_scope` (the message names the field, e.g.
`commands[0].argv[0]: program not found`), `forbidden_resource` (a repo path that may not
be used), `bad_state` (`--show` with no scope), `unknown_peer`, `approval_limit`,
`approval_locked`, `approval_unavailable`, `daemon_not_running`, `timeout`, `usage`.

`device list --json` on the helper adds `"scope": {"expires", "types", "commands": [names]}`
to an active link that has a scope.

## Audit

`device.link_intent`, `device.link_active`, `device.unlink` (with `side` `local` or
`remote`), and `peer.verify` on confirmation; `device.scope_set` (counts, types, expiry
in seconds, approval id), `device.scope_clear`, `device.run` (ids, duration, output size,
timed out) and `device.out_of_scope` (request, peer, check). Only ids, enums, counts and
sizes are recorded, never names, command names, argv, paths, environment or output.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Approval created / listed / unlinked / scope cleared or shown |
| 1 | Error (see the codes above) |
| 2 | Usage error |
| 3 | Daemon not running |
