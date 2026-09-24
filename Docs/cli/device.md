# `agentnet device`

Links two of your own devices, a **controller** and a **helper**
([Docs/protocol/device.md](../protocol/device.md), owner decision D13). Introduced by
ticket 2.D1: link, list and unlink. `device scope` and helper runs come with 2.D2.

```
agentnet device link <peer> --as controller|helper --fingerprint FP [--json]
agentnet device list [--json]
agentnet device unlink <peer> [--json]
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
lapses (`revoked`) and can be repeated.

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

## Audit

`device.link_intent`, `device.link_active`, `device.unlink` (with `side` `local` or
`remote`), and `peer.verify` on confirmation. Only ids and enums are recorded, never
names or paths.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Approval created / listed / unlinked |
| 1 | Error (see the codes above) |
| 2 | Usage error |
| 3 | Daemon not running |
