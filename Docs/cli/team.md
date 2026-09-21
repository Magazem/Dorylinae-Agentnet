# `agentnet team`

Status: draft (Phase 1, 1.1). Protocol: [../protocol/team.md](../protocol/team.md).

Creates and manages teams: named sets of paired agents that share presence and requests.

```
agentnet team create <name> [--json]
agentnet team list [--all] [--json]
agentnet team show <team> [--json]
agentnet team invite <team> [--json]          owner: print a one-time invite code (10 min)
agentnet team join <code> [--json]            join the team whose owner showed <code>
agentnet team remove <team> <peer> [--json]   owner
agentnet team rename <team> <new-name> [--json]  owner
agentnet team leave <team> [--json]           member (not the owner)
agentnet team delete <team> [--json]          owner: dissolve the team
```

`<team>` is a team id (`t-…`) or a team name that is unique on this machine. `<name>` must
match `^[a-z0-9][a-z0-9-]{0,31}$`. `<peer>` is a peer name or public key, with an optional
leading `@`. `<code>` is typed like a pairing code (case-insensitive, `-` and spaces ignored).

| Flag | Meaning |
|---|---|
| `--all` | `list`: also list teams that were left, removed or dissolved |
| `--json` | Machine-readable output on stdout |

## Invite and join

The invite code **is** a pairing v2 code ([pair.md](pair.md)), so the same rules apply: both
daemons must be connected to the relay, the code is valid for 10 minutes, and each code can
be redeemed once. `team invite` and `team join` follow the 2-second rule of `pair`. They
return `pending` with a `pairing_id` if the exchange is not finished within 1 s. Poll it
with `agentnet pair --status <id>`.

When the join completes, the joiner is paired with the owner (`trust=code`) and asks to
join. The owner's daemon adds the joiner and sends the new member list to everyone. The
joiner then sees the other members as peers with `trust=team`. Redeeming an invite code with
plain `agentnet pair <code>` pairs the two daemons without joining the team.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Done (for `invite` and `join`, the state is `pending` or `complete`) |
| 1 | Error (see the codes below), or the invite or join pairing `failed` |
| 2 | Usage error |
| 3 | Daemon not running |

## Human output

```
$ agentnet team create backend
Created team backend (t-0123456789abcdef0123456789abcdef). You are the owner.

$ agentnet team invite backend
Invite code:  7KQ2M-9XHF4-TRW8N
Expires:      2026-10-01T09:22:00Z
Pairing ID:   pair-0123456789abcdef
On the other machine run: agentnet team join 7KQ2M-9XHF4-TRW8N

$ agentnet team join 7KQ2M-9XHF4-TRW8N
Paired with alice (fingerprint 2ED9 TGVE R471 63MC C451); asked to join alice's team.
Run 'agentnet team list' in a few seconds.

$ agentnet team list
NAME     ID                                  ROLE    MEMBERS  STATE
backend  t-0123456789abcdef0123456789abcdef  owner   3        active

$ agentnet team show backend
backend (t-0123…cdef), owner alice, epoch 3
NAME   ROLE    ADDED                 FINGERPRINT
alice  owner   2026-10-01T09:00:00Z  2ED9 TGVE R471 63MC C451
bob    member  2026-10-01T09:12:00Z  …
```

With no teams: `No teams yet. Run 'agentnet team create <name>' to start.`

## `--json` output

- `create`, `remove`, `rename`, `leave`, `delete`: `{"ok": true, "team": <team summary>}`
- `list`: `{"ok": true, "teams": [<team summary>]}`
- `show`: `{"ok": true, "team": {...summary, "members": [{"name","public_key","fingerprint","added","owner","self"}]}}`
- `invite`: `{"ok": true, "pairing_id", "role": "issuer", "state", "code"?, "expires"?, "team": {"id","name"}}`
- `join`: `{"ok": true, "pairing_id", "role": "redeemer", "state", "peer"?}`

A team summary is `{"id","name","owner","epoch","state","role","members"}`, see
[../protocol/ipc.md](../protocol/ipc.md#teams). `ok` is `false` for a `failed` pairing.

Errors print `{"ok":false,"error":{"code","message"}}`. The codes are `unknown_team`,
`ambiguous_team`, `team_exists`, `bad_team_name`, `not_owner`, `owner_cannot_leave`,
`not_member`, `team_inactive`, `team_full`, `unknown_peer`, `ambiguous_peer`, the pairing
setup codes of [pair.md](pair.md) (`no_relay`, `relay_unavailable`, `bad_code`, …),
`daemon_not_running` (exit 3) and `usage` (exit 2).

## Audit

`team.*` events, see [../protocol/team.md](../protocol/team.md#audit).
