# `agentnet presence`

Status: draft (Phase 1, 1.3). Protocol: [../protocol/presence.md](../protocol/presence.md).

Shows or sets who can see this machine's presence. To see teammates' presence, use
`agentnet status --team <team>` ([status.md](status.md)).

```
agentnet presence [--json]                        show the current mode
agentnet presence --visible [--json]              all teams you are in (default)
agentnet presence --invisible [--json]            nobody; teammates see last-seen only
agentnet presence --only-team <team> [--json]     members of one team only
agentnet presence --human on|off [--json]         share "human present" (pending OD-P1-6)
```

`--visible`, `--invisible` and `--only-team` are mutually exclusive (a usage error
otherwise). `--human` may be combined with any of them.

Invisible hides you from **teammates**. The relay operator can still see that your daemon is
connected. Going invisible, or leaving a peer's visibility, sends that peer one "offline"
message, so the peer sees you go offline at once, with a `last_seen` of that moment.

## Exit codes

0 done, 1 error, 2 usage, 3 daemon not running.

## Output

Human: `Presence: visible` / `Presence: invisible` / `Presence: only team backend
(t-0123…cdef)`, then `Human presence: shared` or `not shared`.

`--json`: `{"ok": true, "mode": "visible"|"invisible"|"only_team", "team"?: {"id","name"}, "human_share": true}`.

Error codes: `unknown_team`, `ambiguous_team`, `team_inactive`, `bad_request`,
`daemon_not_running` (exit 3) and `usage` (exit 2).

## Audit

`presence.mode {mode, team?}` and `presence.human {share}`.
