# `agentnet mail send` (debug only)

Queues one mail to a paired agent through the daemon. This is a **debug and test command**:
it exists so the mail path ([../protocol/mail.md](../protocol/mail.md)) can be exercised
end to end before Phase 1 adds real kinds (requests, results, ...). It is available only when
`DORYLINAE_DEBUG=1` is set in the environment of the `agentnet` process. Without it `mail` is
not a command: `agentnet: unknown command "mail"`, exit 2, and it is not listed in
`agentnet --help`.

```
agentnet mail send @peer --kind K --text T [--json]
```

`@peer` is a paired peer's name (case-insensitive) or its public key; the `@` is optional.

| Flag | Meaning |
|------|---------|
| `--kind K` | Required. The mail kind (1–64 characters from `[a-z0-9._-]`). The receiving daemon must understand it: a kind it does not know is acked as `unsupported` and the outbox row ends `failed` |
| `--text T` | The text; the body sent is `{"text": T}` |
| `--json` | Machine-readable output on stdout |

## The `note` kind

When the **receiving** daemon runs with `DORYLINAE_DEBUG=1`, it registers a built-in kind
`note`: the verified mail is stored in its inbox (`mail_inbox`), audited as `mail.in`, and
acked. Nothing else happens. Without the variable `note` is unknown, so a `note` to that
daemon is acked as `unsupported`.

## Behaviour

The command calls the IPC method `mail_submit` ([../protocol/ipc.md](../protocol/ipc.md#mail_submit)).
The mail is signed, sealed to the peer's mailbox key and stored in the outbox as `queued`; the
command returns at once, whether or not the peer or the relay is reachable. The daemon sends
it, resends it with backoff and marks it `delivered` when the peer acks. A relay refusal
(`peer_offline`, `peer_busy`) never fails the mail; it stays queued. Progress is visible in the
`outbox:` line of `agentnet status`. If the peer is offline the mail waits in the relay's
queue (7 days) and is delivered when the peer reconnects.

An outbox row that is not acked within 7 days becomes `expired`, which means **delivery
unknown**, not "not delivered" ([mail.md](../protocol/mail.md#states)).

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Mail queued |
| 1 | Error: unknown or ambiguous peer, not paired, no mailbox key, bad request |
| 2 | Usage error (missing peer or `--kind`, extra arguments) |
| 3 | Daemon not running |

## Output

Human:

```
queued mail m-0123456789abcdef0123456789abcdef to @bob (queued)
```

`--json`:

```json
{"ok": true, "id": "m-0123456789abcdef0123456789abcdef", "state": "queued"}
```

Errors print `{"ok":false,"error":{"code","message"}}` under `--json`, with code
`unknown_peer`, `ambiguous_peer`, `unpaired`, `no_mailbox_key`, `bad_request`,
`daemon_not_running` or `usage`. `no_mailbox_key` means the peer was paired with v1 and must
re-pair.

## Try it

```
# both machines
export DORYLINAE_DEBUG=1            # PowerShell: $env:DORYLINAE_DEBUG = '1'
agentnetd --relay ws://127.0.0.1:8787

# on A, after pairing
agentnet mail send @bob --kind note --text "hello"
agentnet status                     # outbox: 1 queued (or relayed) ... then 0 once acked
```

See step 11 of `tests/phase0-manual.md` for the offline variant.
