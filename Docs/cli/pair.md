# `agentnet pair`

Pairs this machine with an agent on another machine through the relay, using a
one-time code. The protocol (v2) is specified in
[../protocol/pairing.md](../protocol/pairing.md). The daemon must be running
and connected to a relay (`agentnetd --relay URL` or `$DORYLINAE_RELAY_URL`).

```
agentnet pair --new [--json]           issue a one-time code (valid 10 minutes)
agentnet pair <code> [--json]          redeem a code shown by the other machine
agentnet pair --v1 <code> [--json]     redeem a legacy 10-character code
agentnet pair --status <id> [--json]   check a pairing that was still pending
```

| Flag | Meaning |
|------|---------|
| `--new` | Issue a one-time pairing code (15 characters) and return. Enter the code on the other machine. |
| `--v1` | Allow redeeming a legacy 10-character (v1) code. The peer is stored with `trust=relay` and has no mailbox key |
| `--status ID` | Show the state of a pairing by its pairing ID |
| `--json` | Machine-readable output on stdout |

`<code>` is case-insensitive; `-` and spaces are ignored (`7KQ2M-9XHF4-TRW8N`,
`7kq2m 9xhf4 trw8n`). A v2 code has 15 characters: the first 5 (the lookup) go to
the relay, the last 10 (the secret) never leave the two daemons. A 10-character
code is a v1 code and is refused unless `--v1` is given (`bad_code`).

## The 2-second rule

`pair` always returns in under 2 seconds. The daemon waits up to 1 second for the
relay; if the exchange is not finished by then the command exits 0 with state
`pending` and a **pairing ID**. Poll it with `agentnet pair --status <id>`.
`pair --new` normally returns the code at once and then stays `pending` until the
other machine redeems it (or the code expires); after that `pair --status <id>`
shows `complete`, and `agentnet peers` lists the peer.

## Verification

Both daemons verify the other's signed Agent Card and mailbox key announcement
(signature valid, keys equal to the key the relay authenticated), then prove to each
other that they know the code with a MAC over both cards and both announcements. The
redeemer sends its proof first. If a card or key was altered on the way, or the secret
is wrong, the pairing fails and **nothing is stored on the side that detects it**. A
peer that completes is stored with `trust=code`, its verified mailbox announcement
included; run `agentnet peers verify` to raise it to `fingerprint`.

- The issuer allows at most 3 attempts per code, then fails with `bad_confirm`.
- Each side waits at most 60 seconds for the other's proof (`confirm_timeout`).
- The issuer's code expires 10 minutes after issue by the daemon's own clock (`expired`).
- The redeemer uses a code once: a second redemption of the same code from the same
  daemon in 24 hours fails with `code_used`, even after a restart. Ask for a new code.
- If the relay only supports v1 the issuer fails with `relay_v1` and shows no code.

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
Pairing code: 7KQ2M-9XHF4-TRW8N
Expires:      2026-01-02T03:14:05Z
Pairing ID:   pair-0123456789abcdef

On the other machine run: agentnet pair 7KQ2M-9XHF4-TRW8N
Then check here with:     agentnet peers

$ agentnet pair 7KQ2M-9XHF4-TRW8N
Paired with my-laptop (custom)
  public key:  <base64url>
  fingerprint: 2ED9 TGVE R471 63MC C451
  trust:       code
```

Failures print `agentnet: pairing failed: <message> (<code>)` on stderr.

## `--json` output

```json
{
  "ok": true,
  "pairing_id": "pair-0123456789abcdef",
  "role": "issuer",
  "state": "pending",
  "code": "7KQ2M-9XHF4-TRW8N",
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
| `peer` | On `complete`: `{"public_key","name","harness","skills","paired_at","trust","fingerprint"}`. A v2 pairing is `trust` `code`; a `--v1` pairing is `relay`. Compare `fingerprint` with the other machine and run `agentnet peers verify` |
| `error` | On `failed`: `{"code","message"}`. Codes: the relay's (`pair_invalid`, `pair_rate_limited`, `pair_limit`, `pair_lookup_taken`, `pair_v1_disabled`, `peer_offline`, `peer_busy`, `bad_pairing`) or `bad_card`, `bad_mbox`, `bad_confirm`, `confirm_timeout`, `relay_v1`, `code_used`, `store_error`, `timeout`, `expired` |

A request that fails before anything is sent prints
`{"ok":false,"error":{"code","message"}}` with exit 1. Codes: `no_relay`,
`relay_unavailable`, `bad_code`, `unknown_pairing`, `too_many_pairings`,
`daemon_not_running` (exit 3), `usage` (exit 2).

## Audit

The daemon records `pair.start`, `pair.complete`, `pair.fail` and (issuer, per
failed attempt) `pair.attempt_fail` in its audit log (see
[../protocol/pairing.md](../protocol/pairing.md#logging-audit-counters)). Details
never contain codes, lookups, secrets, keys, tags, cards or announcements.
