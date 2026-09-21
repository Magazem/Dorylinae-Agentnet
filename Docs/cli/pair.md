# `agentnet pair`

Pairs this machine with an agent on another machine through the relay, using a
one-time code. The relay side is specified in
[../protocol/pairing.md](../protocol/pairing.md). The daemon must be running
and connected to a relay (`agentnetd --relay URL` or `$DORYLINAE_RELAY_URL`).

```
agentnet pair --new [--json]           issue a one-time code (valid 10 minutes)
agentnet pair <code> [--json]          redeem a code shown by the other machine
agentnet pair --status <id> [--json]   check a pairing that was still pending
```

| Flag | Meaning |
|------|---------|
| `--new` | Issue a one-time pairing code and return. Enter the code on the other machine. |
| `--status ID` | Show the state of a pairing by its pairing ID |
| `--json` | Machine-readable output on stdout |

`<code>` is case-insensitive; `-` and spaces are ignored (`7KQ2M-9XHF4`,
`7kq2m 9xhf4`).

## The 2-second rule

`pair` always returns in under 2 seconds. The daemon waits up to 1 second for the
relay; if the exchange is not finished by then the command exits 0 with state
`pending` and a **pairing ID**. Poll it with `agentnet pair --status <id>`.
`pair --new` normally returns the code at once and then stays `pending` until the
other machine redeems it (or the code expires); after that `pair --status <id>`
shows `complete`, and `agentnet peers` lists the peer.

## Card verification

Both daemons verify the other's signed Agent Card before storing anything: the
signature must be valid and the card's `public_key` must equal the key the relay
authenticated for that side. An invalid card aborts the pairing (state `failed`,
error code `bad_card`) and **nothing is stored**. A code can be used once; using
it again fails with `pair_invalid`.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Request accepted: state `pending` or `complete` |
| 1 | Error, or the pairing `failed` |
| 2 | Usage error |
| 3 | Daemon not running |

## Human output

```
$ agentnet pair --new
Pairing code: 7KQ2M-9XHF4
Expires:      2026-01-02T03:14:05Z
Pairing ID:   pair-0123456789abcdef

On the other machine run: agentnet pair 7KQ2M-9XHF4
Then check here with:     agentnet peers

$ agentnet pair 7KQ2M-9XHF4
Paired with my-laptop (custom)
  public key: <base64url>
```

Failures print `agentnet: pairing failed: <message> (<code>)` on stderr.

## `--json` output

```json
{
  "ok": true,
  "pairing_id": "pair-0123456789abcdef",
  "role": "issuer",
  "state": "pending",
  "code": "7KQ2M-9XHF4",
  "expires": "2026-01-02T03:14:05Z"
}
```

| Field | Notes |
|-------|-------|
| `ok` | `false` when `state` is `failed` or the request itself failed |
| `pairing_id` | Stable id to poll with `--status` |
| `role` | `issuer` (`--new`) or `redeemer` (`<code>`) |
| `state` | `pending`, `complete` or `failed` |
| `code`, `expires` | Issuer only, while `pending` and once the relay issued the code (absent if the relay has not answered yet; poll again). Dropped when the pairing ends |
| `peer` | On `complete`: `{"public_key","name","harness","skills","paired_at","trust","fingerprint"}`. A v1 pairing is `trust` `relay`; compare `fingerprint` with the other machine and run `agentnet peers verify` |
| `error` | On `failed`: `{"code","message"}`. Codes: the relay's (`pair_invalid`, `pair_rate_limited`, `pair_limit`, `peer_offline`, `peer_busy`, `bad_pairing`) or `bad_card`, `store_error`, `timeout`, `expired` |

A request that fails before anything is sent prints
`{"ok":false,"error":{"code","message"}}` with exit 1. Codes: `no_relay`,
`relay_unavailable`, `bad_code`, `unknown_pairing`, `too_many_pairings`,
`daemon_not_running` (exit 3), `usage` (exit 2).

## Audit

The daemon records `pair.start`, `pair.complete` and `pair.fail` in its audit
log (see [../protocol/pairing.md](../protocol/pairing.md#daemon-side-ticket-05b)).
Details never contain codes or card contents.
