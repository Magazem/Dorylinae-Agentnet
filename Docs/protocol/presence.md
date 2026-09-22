# Presence and visibility

Status: **draft for review**, Phase 1 tickets 1.2 (three levels), 1.3 (visibility) and the
last-seen part of 1.9 (split into 1.2a–1.2d and 1.3 in
[../review/11-phase1-tickets.md](../review/11-phase1-tickets.md)). Nothing here is
implemented yet. Change this document first.

## Levels

| Level | Meaning | Source | Seen by peers as |
|---|---|---|---|
| **Daemon online** | The daemon is running, connected to the relay, and publishing to you | A presence heartbeat every 30 s | `daemon_online` = last heartbeat received ≤ 75 s ago and its `state` was `online` |
| **Agent active** | A CLI call (any IPC request, any method) reached the daemon in the last **5 min** | The daemon's own clock | `agent_active`, from the latest heartbeat's `agent` flag |
| **Human present** | OS input idle time is under **10 min** | Per-OS idle query ([Idle detection](#idle-detection)) | `human_present`: `true`, `false`, or `null` (unknown or not shared) |

**Last seen** (1.9) is the receiver's clock at the moment it accepted the most recent
presence message from that peer, of either `state`. It is persisted, so it survives a
restart. It is `null` for a peer never heard from.

Timing bounds (acceptance tests of 1.2):

- Daemon killed on B (no goodbye): A last accepted a heartbeat at `t ≤ t_kill`; A reports
  `daemon_online = false` once `now − last_rx > 2.5 × interval` = 75 s, which is under
  the required 90 s. The state is computed when read, so there is no extra polling delay.
- Any command on B: the IPC call flips `agent` from 0 to 1, and B sends a heartbeat
  **immediately** (not at the next tick), so A shows `agent_active` within about 1 s (bound
  30 s).
- Graceful stop or going invisible: B sends `state: offline`, and A shows it offline at once.

## Design choice: sealed peer-to-peer heartbeats, relay-agnostic

The plan's diagram has the relay "hold presence". This spec instead keeps presence
**end-to-end**: each daemon sends each visible team peer a small sealed heartbeat, and
the relay only forwards it (OD-P1-3). Reasons:

1. **Visibility controls need per-recipient answers.** "Only team x" and "invisible" mean
   different peers must see different things. If the relay broadcast presence, it would need
   each daemon's team graph and visibility policy, which is new metadata held on the relay.
   With sealed heartbeats the daemon simply does not send to peers it hides from. That is
   exactly "the relay only broadcasts what the daemon publishes".
2. **Agent and human levels are private.** Sealed, they are invisible to the relay. Relay-held
   presence would expose them to the relay operator.
3. **No new trust in the relay.** A hostile relay can drop or delay heartbeats, so a peer looks
   offline. It cannot forge "online" or "agent active", because heartbeats are signed and
   sealed like mail.
4. **Reuse.** The seal, signature and verification are the mail code
   ([mail.md](mail.md)); only the handling after opening is new.

The one relay change is an **ephemeral** envelope type that is never queued
([Relay](#relay-ephemeral-envelopes)). Without it, heartbeats to an offline peer would fill
its offline queue (1000 envelopes) within 8 hours and block real mail with `queue_full`.

Cost: one envelope per visible peer per 30 s. A team of 5 costs each daemon 8 envelopes a
minute. Mail stays the only thing that is outboxed and acked.

## Presence message

### Envelope

| Field | Value |
|---|---|
| `from`, `to` | Sender and recipient identity keys |
| `team` | `""` (always) |
| `type` | `presence` |
| `id` | `p-` + 32 lowercase hex (16 bytes from `crypto/rand`), fresh per message |
| `ts` | Sender clock at send, RFC 3339 |
| `payload` | Exactly the mail payload layout ([mail.md §Envelope](mail.md#envelope)): `0x01 ‖ key_id ‖ enc ‖ ct` |

Sealing is exactly [mail.md §Sealing](mail.md#sealing): the same HPKE suite and `info`, with
`aad` = the envelope `id`. Signing is exactly [mail.md §Message](mail.md#message), with
these differences in `msg`:

- `id` = the envelope id, format `p-` + 32 lowercase hex (**not** `m-`);
- `kind` = `presence`;
- `body` as below.

A presence message relabelled by the relay as `type: mail` fails mail step 9 (`id` format).
A mail relabelled as `presence` fails the kind check below. The shared signature domain is
therefore safe.

### Body

```json
{"agent": 1, "boot": "8f3a0c1d22b4e5f6", "epochs": {"t-0123456789abcdef0123456789abcdef": 3},
 "human": 2, "interval": 30, "pad": "0000000000", "seq": 17, "state": "online"}
```

| Member | Type | Rules |
|---|---|---|
| `state` | string | `online` or `offline` (goodbye) |
| `agent` | integer | `1` if agent active (last IPC call < 5 min ago), else `0`. `0` when `state = offline` |
| `human` | integer | `1` present, `0` idle ≥ 10 min, `2` unknown **or not shared** ([Human sharing](#human-sharing)). `2` when `state = offline` |
| `boot` | string | 16 lowercase hex, random per daemon start |
| `seq` | integer | Per recipient, starts at 1 each boot, +1 per message to that recipient. 1 to 2^53−1 |
| `interval` | integer | The sender's heartbeat period in seconds, 1–300. The daemon uses `max(30, ⌈visible / 3⌉)` ([Sending](#sending)); values under 30 occur only with the test option `PresenceInterval` |
| `epochs` | object | Team id → roster epoch held by the sender, **only** for `active` teams whose owner is the recipient ([team.md §Operations](team.md#operations), resync). 0–32 members; each value 0 to 2^53−1; `{}` when none |
| `pad` | string | Only `0` characters, chosen so that `len(plaintext)` (the canonical signed object) is always the one **fixed size** below |

All members are required, and no others are allowed. Integers and flags use fixed values
instead of booleans or ages, `seq` and every `epochs` value are capped at 2^53−1 (the JSON
safe-integer bound, matching `team.epoch`'s own limit), and `pad` equalises the length to a
single fixed size — not merely "a multiple of 256" — so the ciphertext size shows the relay
nothing about the flags, the state, or how many teams the sender holds (review 17 L1: before
this fix, a goodbye was 1 byte longer than a heartbeat and about 1 message in 256 crossed a
padding boundary, so size alone could sometimes tell a goodbye apart from a heartbeat).

The fixed size is the smallest multiple of 256 that fits the longest possible body: `state:
"offline"` (one byte longer than `"online"`), `interval` at its 3-digit maximum (300), `seq`
and every `epochs` value at 2^53−1, and the maximum 32 `epochs` entries. It depends only on
the byte length of `from`, `to`, `id` and `created` in the envelope, which are fixed by their
formats (identity keys, the `p-` + 32 hex id, RFC 3339 to the second), so it is the same
number of bytes for every presence message a given build sends, whatever its actual content.
The sender computes it once with `pad = ""` at that worst-case content to get the target
length, then builds the real body the same way to get its actual length `L`, and sets `pad`
to `target − L` `0` characters. Adding characters to a string adds exactly that many bytes,
and the member order is fixed by canonical JSON.

### Receiving

`relayclient` hands up `presence` envelopes like other types. It does **not** send a relay
`ack` for them, because they are never queued. The daemon then:

1. Runs [mail.md steps 1–10](mail.md#receiving-verification-order) through the same opener,
   with the step 9 id format `p-` + 32 hex. On failure, drop the message and **do not audit**.
   Presence is too frequent, and a hostile relay could flood the audit log. Instead, count it
   in the log at debug level (`event=presence_reject`, reason), at most one line per peer
   per minute. A `key_miss` does **not** start key-miss recovery. The next rotation push
   repairs it.
2. `kind` = `presence`. The body is strict per the table above.
3. `now − 10 min ≤ created ≤ now + 10 min` (receiver clock).
4. **Order and replay.** Load `presence_peers` for `from`. Accept if there is no row, or
   `boot` = row `boot` and `seq` > row `seq`, or `boot` ≠ row `boot` and `created` ≥ row
   `created`. Otherwise drop (debug log `presence_stale`).
5. Upsert the row: `boot`, `seq`, `created`, `state`, `agent`, `human`, `interval`,
   `last_rx = now`. If `state = online` and `agent = 1`, `last_agent = now`. If `state =
   online` and `human = 1`, `last_human = now`.
6. **Online edge.** If the peer was not effectively online before this message and is now,
   call `Outbox.OnPeerOnline(peer)` ([mail.md §Sending and backoff](mail.md#sending-and-backoff)).
   It sends every non-final outbox row to that peer now.
7. **Roster resync** (owner only). For each `epochs` entry naming a team this daemon owns
   (any state): if the entry is lower than the team's epoch, and no roster was resubmitted
   to `from` for that team in the last 10 minutes, resubmit the current roster to `from`.
   If `from` is no longer a member, that is the owner-only roster
   ([team.md §Operations](team.md#operations)). Entries naming other teams are ignored.

Presence is not deduped in `mail_seen`, not stored in `mail_inbox`, not acked, and not
audited per message.

**Effective state of a peer** at time `now`, from its row (absent row: never heard):

```
daemon_online  = row.state == "online" && now − row.last_rx ≤ 2.5 × row.interval
agent_active   = daemon_online && row.agent == 1
human_present  = daemon_online ? (row.human == 2 ? null : row.human == 1) : false
last_seen      = row.last_rx            (null if no row)
```

### Sending

The daemon sends presence only when the relay advertised the `ephemeral` feature
([Relay](#relay-ephemeral-envelopes)). Otherwise presence is off, and `status` reports
`presence.relay: "unsupported"`.

**Recipients**, the *visible set*, from the [visibility mode](#visibility):

| Mode | Visible set |
|---|---|
| `visible` (default) | Every peer that shares at least one `active` team with self |
| `only_team` (team `x`) | Members of `x` except self. If `x` is no longer `active`, the mode becomes `invisible` (audit `presence.mode {mode: "invisible", reason: "team_gone"}`) |
| `invisible` | Nobody |

Peers without a mailbox key are skipped.

**When:**

- **Tick:** every `interval` × U(0.9, 1.1), `state: online` to the whole visible set.
  `interval = max(30, ⌈|visible set| / 3⌉)` seconds, recomputed at each tick, so the tick
  load stays at most about 180 envelopes a minute and under the relay limit below,
  with room for the immediate sends. Up to 90 visible peers this is 30 s, and the 90 s
  offline bound holds. Beyond that, peers see the longer `interval` in the body and
  scale their online window with it.
- **Immediately**, as `state: online` to the whole visible set: at daemon start (after relay
  `ready`), on every relay `ready`, and on the agent edge (an IPC call arriving when the
  last one was ≥ 5 min ago). The edge send runs asynchronously. The IPC call never waits for it.
- **Immediately to one peer:** when a roster makes it a new member of a shared active team.
- **Goodbye** (`state: offline`, `agent: 0`, `human: 2`): to peers that **leave** the visible
  set (mode change, leaving a team, removal), and to the whole visible set on graceful
  shutdown (best effort, at most 1 s in total).

Each message is sealed to the peer's newest mailbox announcement, like mail. A failed
`Send` (not connected) is dropped. There is no retry.

### Relay: ephemeral envelopes

Relay change (1.2a):

- The `ready` frame gains `"features": ["ephemeral"]`:
  `{"op":"ready","public_key":"<key>","features":["ephemeral"]}`. A daemon that sees no
  `features` member assumes none.
- Envelope type `presence` is **ephemeral**. The relay forwards it if the recipient is
  connected and its send buffer is **at most half full** (32 of the default 64 frames),
  **ignoring** any backlog (ordering does not apply to ephemeral types). The other half is
  reserved for mail and control frames, so a presence flood from many senders cannot push
  a recipient's mail into the queue path. Otherwise the relay drops the envelope
  **silently**: no `queued`, no `error`, never stored, not counted against queue limits. The
  relay still validates routing fields and `from` (`bad_envelope`, `bad_sender`).
- Rate limit: at most **600** ephemeral envelopes per minute per sending key (relay option
  `EphemeralPerMinute`). Excess envelopes are dropped silently and counted in the log
  (`event=ephemeral_limited`, once a minute).
- `relayclient` hands `presence` envelopes up **without** the seen-set (like `mail`), and
  never acks them. Presence has its own replay rule (Receiving step 4). Frequent heartbeats
  would otherwise evict the `session.*` entries that the 8192-entry seen-set protects.
- The logging rule is unchanged (no payload). Per-envelope log lines for ephemeral types are
  at debug level.

## Visibility

`agentnet presence` ([../cli/presence.md](../cli/presence.md)) sets one mode, stored in
`settings` under `presence.mode`:

```json
{"mode": "visible"}
{"mode": "invisible"}
{"mode": "only_team", "team": "t-0123456789abcdef0123456789abcdef"}
```

On a change, the daemon computes the old and new visible sets, sends a goodbye to
`old − new` and an immediate `online` to `new − old`, and audits `presence.mode {mode,
team?}` (actor `cli`).

Acceptance (1.3): with B `invisible`, A's `status --team x` shows B with
`daemon_online: false`, `agent_active: false`, `human_present: false`, and `last_seen` =
the time of B's goodbye. It never shows B online while B stays invisible, because B sends
A nothing.

### Human sharing

Pending OD-P1-6. `agentnet presence --human off|on` stores `presence.human` =
`{"share": false|true}` (default `true`). With `false`, heartbeats always carry `human: 2`.
Local `status` still shows the user their own detected value.

## Idle detection

Package `internal/idle`: `Idle(ctx) (time.Duration, bool)`. The `bool` is false when idle
time is unknown, which gives `human: 2`. The call has a hard **1 s** timeout, and it runs once
per tick and once per immediate send, with the result cached for 5 s. No cgo.

| OS | Mechanism | Unknown when |
|---|---|---|
| Windows | `user32!GetLastInputInfo` and `kernel32!GetTickCount` through `golang.org/x/sys/windows` (`NewLazySystemDLL`). Idle = `uint32(GetTickCount()) − LASTINPUTINFO.dwTime` (uint32 arithmetic handles wrap) | The call fails, or the daemon is not in an interactive session (for example session 0: `ProcessIdToSessionId` returns 0) |
| macOS | Exec `/usr/sbin/ioreg -c IOHIDSystem -d 4 -r -k HIDIdleTime`. Parse the first `"HIDIdleTime" = <n>` (nanoseconds) | Exec fails, times out, or no match |
| Linux | Try in order, first success wins: (1) `gdbus call --session --dest org.gnome.Mutter.IdleMonitor --object-path /org/gnome/Mutter/IdleMonitor/Core --method org.gnome.Mutter.IdleMonitor.GetIdletime` → `(uint64 N,)` ms; (2) `gdbus call --session --dest org.freedesktop.ScreenSaver --object-path /org/freedesktop/ScreenSaver --method org.freedesktop.ScreenSaver.GetSessionIdleTime` → `(uint32 N,)` ms; (3) `xprintidle` (if `DISPLAY` is set and it is on `PATH`) → ms | All three fail. Wayland compositors other than GNOME and KDE usually end here |

`human present` = known and idle < 10 min. Tests inject a fake `Idle`. The real mechanisms
are a manual check per OS ([../review/11-phase1-tickets.md](../review/11-phase1-tickets.md)).

## What the relay sees

| Relay sees | Relay does not see |
|---|---|
| When a daemon is connected. This is inherent, **including when it is invisible**. Invisible hides you from peers, not from the relay operator | Agent-active and human-present flags. They are sealed, and padding makes all heartbeats the same size |
| Presence envelopes `from → to`, about every 30 s, so it learns who publishes presence to whom. That is roughly the team graph, which mail traffic already reveals | Team ids, names and membership lists (`team` is always `""`) |
| When a daemon **stops** sending presence to a peer: goodbye followed by silence, while it stays connected. The relay can infer "invisible to that peer" or "left the team" | Visibility mode and its team |
| An off-schedule presence envelope right after a CLI call. This reveals the timing of agent-active edges (OD-P1-7) | Anything about requests. They are `mail` |

## Tables

```sql
-- migration 10 (1.2b): presence
CREATE TABLE presence_peers (
    key        TEXT PRIMARY KEY,
    boot       TEXT NOT NULL,
    seq        INTEGER NOT NULL,
    created    TEXT NOT NULL,                  -- msg.created of the accepted message
    state      TEXT NOT NULL CHECK (state IN ('online', 'offline')),
    agent      INTEGER NOT NULL CHECK (agent IN (0, 1)),
    human      INTEGER NOT NULL CHECK (human IN (0, 1, 2)),
    interval   INTEGER NOT NULL,
    last_rx    TEXT NOT NULL,                  -- receiver clock = last seen
    last_agent TEXT,
    last_human TEXT
) WITHOUT ROWID;
CREATE TABLE settings (
    key     TEXT PRIMARY KEY,                  -- 'presence.mode', 'presence.human', 'notify.*'
    value   TEXT NOT NULL CHECK (json_valid(value)),
    updated TEXT NOT NULL
);
```

A row is written on every accepted message. That is about 10 writes a minute for a team
of 5, which is fine for SQLite in WAL mode. Rows of peers that are removed are deleted with
the peer.

## Audit

| Action | Actor | Detail |
|---|---|---|
| `presence.mode` | `cli` (or `daemon` for `team_gone`) | `{mode, team?, reason?}` |
| `presence.human` | `cli` | `{share}` |

Heartbeats, rejects and state changes are not audited.

## Status and CLI

`status` with `team` shows members with all three levels: [ipc.md](ipc.md#status) and
[../cli/status.md](../cli/status.md). The visibility command is
[../cli/presence.md](../cli/presence.md), backed by the IPC methods `presence_get` and `presence_set`.
