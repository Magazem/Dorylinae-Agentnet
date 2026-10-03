# `agentnet notify`

Status: draft (Phase 1, 1.8). Protocol: [../protocol/notify.md](../protocol/notify.md).

Configures desktop notifications and the outgoing webhook.

```
agentnet notify [--json]                                  show the settings
agentnet notify --desktop on|off [--json]
agentnet notify --event <event>=on|off [--json]           repeatable
agentnet notify --webhook URL [--format generic|slack|discord] [--webhook-title on|off] [--json]
agentnet notify --webhook off [--json]
agentnet notify --rotate-secret [--json]
agentnet notify --test [--json]
```

The events are `request.received`, `request.accepted`, `request.declined`, `request.cancelled`, `session.quarantined`, `session.result`, `session.changes`, `device.linked`, the debate events `debate.constraint`, `debate.agreed`,
`debate.escalated`, `debate.broken` and `debate.refused` (all on by default), `request.deferred`
and `request.completed` (off by default). The debate events say only that something happened:
the peer name and the request title, never the topic, entries or constraint text
([../protocol/notify.md](../protocol/notify.md#triggers), [../protocol/debate.md](../protocol/debate.md)).
`device.linked` is desktop only. `debate.refused` fires when a Decision close is refused.

The webhook URL must be `https://` (`http://` only to localhost). When a webhook is first
set, or on `--rotate-secret`, the command prints the signing secret **once**
(`whsec_…`). Give it to the receiving service to verify `Dorylinae-Signature`. Slack and
Discord cannot verify signatures. The body never contains the brief. It contains the title
only with `--webhook-title on`.

## Exit codes

0 done, 1 error, 2 usage, 3 daemon not running.

## Output

Human:

```
Desktop:  on
Events:   request.received, request.accepted, request.declined
Webhook:  https://hooks.example.com/… (generic, title off), 0 pending, 0 failed in 7 days
```

`--json`: `{"ok": true, "desktop", "events", "webhook", "secret"?}` (see
[../protocol/ipc.md](../protocol/ipc.md#notifications)). `--test`:
`{"ok": true, "desktop": "shown"|"failed"|"disabled", "webhook": "queued"|"none"}`.

Error codes: `bad_webhook`, `bad_request`, `daemon_not_running` (exit 3) and `usage` (exit 2).
