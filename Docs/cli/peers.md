# `agentnet peers`

Lists the agents this machine has paired with (see [pair.md](pair.md)), and
verifies or removes them.

```
agentnet peers [--json]
agentnet peers verify <peer> <fingerprint> [--json]
agentnet peers remove <peer> [--json]
```

`<peer>` is a public key or a unique peer name (an optional leading `@` is
accepted). Trust states and fingerprints are defined in
[../protocol/pairing.md](../protocol/pairing.md#storage-and-trust-states).

A public key is base64url, so about 1 in 32 keys starts with `-`; the CLI
still accepts it as a plain argument (it does not need to be quoted or
escaped) because it recognizes a `-`-prefixed value that decodes as a
32-byte Ed25519 key as positional rather than an unknown flag. Any argument
can also be forced positional with a `--` terminator, e.g.
`agentnet peers remove -- -AbC...`. This applies to every `agentnet`
subcommand that takes a peer key, peer ref or `--from <peer>` value.

| Flag | Meaning |
|------|---------|
| `--json` | Machine-readable output on stdout |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Listed (possibly empty), verified or removed |
| 1 | Unexpected error, unknown or ambiguous peer, or fingerprint mismatch |
| 2 | Usage error |
| 3 | Daemon not running |

## Human output

```
NAME       HARNESS  SKILLS  PAIRED                TRUST  FINGERPRINT              PUBLIC KEY
my-laptop  custom   review  2026-01-02T03:04:05Z  relay  2ED9 TGVE R471 63MC C451  <base64url>
```

`TRUST` is `relay`, `code` or `fingerprint`, or `team` for a peer introduced by a team
owner (Phase 1, [../protocol/team.md](../protocol/team.md#introduced-peers)). With no peers:
`No peers paired yet. Run 'agentnet pair --new' to start.`

## `--json` output

```json
{
  "ok": true,
  "peers": [
    {
      "public_key": "<base64url, 32 bytes>",
      "name": "my-laptop",
      "harness": "custom",
      "skills": [{"id": "review", "name": "Code review", "description": ""}],
      "paired_at": "2026-01-02T03:04:05Z",
      "trust": "relay",
      "fingerprint": "2ED9TGVER47163MCC451",
      "introduced_by": null
    }
  ]
}
```

`peers` is `[]` when nothing is paired, oldest pairing first. `paired_at` is
RFC 3339 UTC. Pairing an already known key again refreshes its name, harness
and skills and keeps the original `paired_at` and never lowers `trust`.
`fingerprint` is `fp(public_key)`: 20 characters, no spaces (human output
groups them in fours).

Existing peers from before this feature have `trust` `relay`, as does every
peer from a v1 pairing.

`introduced_by` (Phase 1) is the public key of the team owner who introduced the peer, or
`null` for a directly paired peer. An introduced peer is removed automatically once it
shares no active team with you. Pairing with it directly, or `peers verify`, makes it a
permanent peer.

## `peers verify`

Compare the fingerprint that `agentnet identity` shows on the other machine
(in person, or over a call you trust) with the peer's, then:

```
agentnet peers verify my-laptop "2ED9 TGVE R471 63MC C451"
```

The fingerprint is case-insensitive, `-` and spaces are ignored, and it may be
given as several arguments. On a match the peer's `trust` becomes `fingerprint`
and the command exits 0, printing `Verified <name> (<fingerprint>): trust is now
fingerprint`; with `--json`, `{"ok": true, "peer": {...}}`. On a mismatch it
exits 1 with error code `fingerprint_mismatch` and changes nothing. A value that
is not 20 characters of the alphabet fails with `bad_fingerprint`. Other error
codes: `unknown_peer`, `ambiguous_peer`.

## `peers remove`

Deletes the peer. Later session envelopes from its key are rejected as
`unpaired` ([../protocol/session.md](../protocol/session.md#rejection)) until it
is paired again. Prints `Removed <name> (<fingerprint>)`; with `--json`,
`{"ok": true, "peer": {...the removed peer...}}`. Error codes: `unknown_peer`,
`ambiguous_peer`.

Phase 1: removing a peer that **owns** teams you are in also leaves those teams locally, and
removes the peers it introduced unless they share another active team with you
([../protocol/team.md](../protocol/team.md#operations)). Removing a peer that is a **member**
of a team you own removes it from that team first, the same as `team remove`: the epoch is
bumped and the new roster is broadcast, before the peer itself is deleted. Removing an
introduced peer that is still in one of your teams (and not a member you own it through) is
undone by the owner's next roster. Leave the team instead.

Both subcommands are recorded in the audit log as `peer.verify`,
`peer.verify_fail` and `peer.remove` (see
[../protocol/ipc.md](../protocol/ipc.md)).
