# `agentnet debate`

Status: draft (Phase 3, 3.1). Protocol: [../protocol/debate.md](../protocol/debate.md).

Argues a question with a teammate's agent in a fixed structure, ending in one signed
[Decision](decision.md) that both daemons sign (the Decision itself and `agentnet decision` are
ticket 3.3, not yet implemented; a debate still runs to a close without it). A debate is a
[request](request.md) of type `debate`; the accept opens a [work session](session.md) of kind
`debate`, which `agentnet session`/`ws_*` refuse (`bad_state`: "use agentnet debate").

## Starting a debate

```
agentnet debate <peer> (--topic TEXT | --topic-from-file F) --position-file P
                [--context-file F]... [--rounds N] [--turn-timeout D]
                [--title T] [--team TEAM] [--urgency low|normal|high|blocking]
                [--urgency-reason R] [--idempotency-key K] [--json]
```

`<peer>` is a peer name or public key, with an optional `@`.

| Flag | Meaning |
|---|---|
| `--topic TEXT` / `--topic-from-file F` | The topic, 1–16384 bytes (the request brief). Exactly one is required |
| `--position-file P` | Your opening position: a JSON object (`-` = stdin), see [Position](#entry-files). The daemon commits to it and never sends it before the peer's own position has left the peer's daemon ([Commit–reveal](../protocol/debate.md#commitreveal)) |
| `--context-file F` | Repeatable, up to 8; same rules as [`agentnet consult`](consult.md) |
| `--rounds N` | 1–5, default 2: the maximum number of challenge rounds |
| `--turn-timeout D` | A Go duration (`90m`, `2h`) or `Nd`, 300 s–86400 s, default 1h. A missed turn ends the debate (`timeout`) |
| `--title T` | Default: the first line of the topic, cut to 120 characters as in `agentnet consult` |
| `--urgency`, `--urgency-reason`, `--team`, `--idempotency-key` | As for [`agentnet request`](request.md) |
| `--json` | Machine-readable output on stdout |

The command returns in under 2 seconds with the request id and the [derived session
id](../protocol/work-session.md#session-id), whether or not the peer is online:

```
Started debate r-8e0c… with bob (team backend)
  session: s-5214… (wait with 'agentnet wait s-5214…')
```

```json
{"ok": true, "id": "r-8e0c…", "mail_id": "m-…", "status": "queued", "duplicate": false,
 "team": {"id": "t-…", "name": "backend"}, "urgency": "normal",
 "peer": {"name": "bob", "public_key": "…", "daemon_online": true, "last_seen": "…"},
 "session": "s-5214…"}
```

**Idempotency** covers the canonical position, `rounds` and `turn_timeout_s` too: a retry with
the same key and different params is `idempotency_conflict` (the daemon never lets an agent
believe it committed to a position that is not the one stored).

## Showing a debate

```
agentnet debate <id> [--json]
agentnet debates [--phase P] [--peer PEER] [--json]
```

`<id>` is the debate's session id (`s-…`) or the request id it belongs to (`r-…`); an `r-` id
that matches more than one debate (a peer reusing an id this daemon sent it) is
`ambiguous_request` — use the session id. `agentnet debates` lists every debate, newest first,
narrowed by `--phase` (`invited`, `positions`, `rounds`, `converge`, `closing`, `closed`,
`broken`) and/or `--peer`; its rows omit the topic, transcript and constraint text, keeping
counts.

Human output:

```
s-5214…  role initiator  phase rounds  turn peer
  peer:    bob
  request: How should the outbox retry? (r-8e0c…)
  rounds:  1/2
  waiting: entry
```

`--json` returns the [debate view](../protocol/debate.md#ipc) under `"debate"`: `session`,
`request {id, title}`, `role`, `peer`, `team`, `phase`, `outcome`/`reason` once decided,
`rounds {max, current}`, `turn` (`you`/`peer`/`none`), `expect` (the next entry kind, when it is
your turn), `deadline`, `waiting` (`reveal`/`entry`/`signature`), `topic`, `context`
(`{name, bytes}`, never the text), `transcript` (`{slot, author, kind, at, entry}`) and
`constraints` (empty until 3.4 adds `--constrain`).

## Submitting an entry

```
agentnet debate <id> --position-file F | --move-file F | --propose-file F | --answer-file F [--json]
```

Give exactly one entry file (`-` = stdin), a JSON object of the matching kind:

<a id="entry-files"></a>

| Kind | Shape | Example |
|---|---|---|
| `position` | `{"claim", "argument", "assumptions"?, "evidence"?, "rejected_alternatives"?}` | `{"claim":"Use capped backoff","argument":"Keeps retries bounded."}` |
| `move` | `{"challenges": [...], "revision"?}` — empty `challenges` is a pass | `{"challenges":[]}` |
| `proposal` | `{"agreement": {"decision", ...}, "remaining_disagreement"?, "affected_artifacts"?}` | `{"agreement":{"decision":"Capped backoff with jitter"}}` |
| `answer` | `{"accept": true\|false, "remaining_disagreement"?, "argument"?}` | `{"accept":true}` |

`--help` shows this table; see [../protocol/debate.md §Messages](../protocol/debate.md#messages-32)
for every field's limits. `debate_submit` refuses a kind or slot that is not yours
(`not_your_turn`, naming the expected kind and author), a bad field (`bad_request`, naming the
path, e.g. `entry.claim`), and a canonical entry over 32768 bytes (`entry_too_large`).

**One-step accept + position.** `--position-file` on a debate that is still `pending` or
`deferred` on you accepts it and submits your opening position in one transaction: either both
happen, or (on any error) neither does and the request stays pending. This is the normal way a
respondent starts a debate.

```
Submitted position to s-5214… (now rounds, turn: peer)
```

## Waiting for your turn

```
agentnet wait <id>
```

`agentnet wait` recognises a debate id automatically (it tries `debate_show` first). It exits 0
with `"wait": "turn"` once it becomes your turn (`expect` names the kind to submit), `"closed"`
once the debate's mirror on your side has closed (`phase` is `closed` or `broken`; the outcome
and the Decision summary appear once 3.3 lands), and exit 4 (`"timeout"`) if nothing changed
within `--timeout` seconds (default 300, max 3600).

```json
{"ok": true, "wait": "turn", "debate": {"session": "s-5214…", "phase": "rounds", "turn": "you", "expect": "move", …}}
```

## Cancel and abandon

```
agentnet debate <id> --cancel [--reason R]
```

- **Before the peer accepts** (`invited`): a Phase 1 `request.cancel` (there is no session yet).
- **After accept, while open**: closes your own debate `cancelled`, no Decision. On the
  initiator this refuses further entries and completes the request on both sides. On the
  respondent, since a debate carries no `ws.state`, **the same command also closes your own
  mirror locally** (`debate.abandon`, [§Cancel and abandon](../protocol/debate.md#cancel-and-abandon)):
  this is your only protection if the initiator's daemon goes silent. A later close from the
  initiator is then stored for the record and changes nothing.
- A cancel refunds nothing.

## Human constraints

```
agentnet debate <id> --constrain TEXT [--json]
```

Adds a constraint: a statement from a human that both agents must respect from
then on ("must stay compatible with Go 1.22", "no new dependency"). It is signed
into the Decision under **human decisions**, so it needs your approval
(OD-P3-3): the command creates a `debate_constraint` approval, the daemon opens
the AgentNet approval window with the peer, the session and the text in full,
and only once you type the code there is the constraint stored and sent to the
peer. An agent that runs `--constrain` gets a pending approval and cannot finish
it. A rejected or expired approval leaves no trace; headless machines confirm in
terminal mode ([approve.md](approve.md)).

- `<id>` is the debate's session (`s-`) or request (`r-`) id.
- `TEXT` is one line of 1–500 **visible** characters: no control characters,
  and no format characters (bidi controls, zero-width characters, U+FEFF, tag
  characters), so the text you approve is exactly what the peer's agent reads.
- Either side may add one while the debate is in `positions`, `rounds` or
  `converge`; a debate holds at most 10 active constraints (both sides
  together). If the debate leaves `converge` before you approve, the approval
  is rejected (`precondition`).
- A constraint that reaches the initiator after it closed the debate is in
  neither record; the respondent's `debate` view marks it `late`.

| Flag | Meaning |
|------|---------|
| `--constrain TEXT` | The constraint text |
| `--json` | Machine-readable output on stdout: `{"ok": true, "approval": <approval view>}` |

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Approval created (the constraint waits for your code) |
| 1 | `bad_request` (text), `bad_state` (phase), `constraint_limit`, `unknown_session`, an approval error |
| 2 | Usage error |
| 3 | Daemon not running |

### Human output

```
Approval a-0123456789abcdef0123456789abcdef: the constraint is added once you type the code into the AgentNet approval window.
```

## Errors

| Code | When |
|---|---|
| `bad_request` | A bad topic/position/entry field; the message names the path |
| `bad_state` | The debate is not in the phase the action needs (e.g. submitting after it closed) |
| `not_your_turn` | The given kind or the caller is not the next slot; the message names the expected kind and author |
| `entry_too_large` | The canonical entry is over 32768 bytes |
| `unknown_session` | No such debate |
| `ambiguous_request` | An `r-` id matches debates with more than one peer; use the session id |
| `quarantine_active` | A sensitive grant to this peer is less than 7 days past its expiry ([§Quarantine interplay](../protocol/debate.md#quarantine-interplay)) |
| `idempotency_conflict`, `no_shared_team`, `ambiguous_team`, `not_team_member`, `unverified_peer`, `unknown_peer` | As for `agentnet request` |
| `daemon_not_running`, `usage` | |

Exit codes: 0 done, 1 error, 2 usage, 3 daemon not running, 4 (`agentnet wait` only) timeout.

## Notifications

Content-free, all on by default: `debate.constraint` (the other side added one), `debate.agreed`
and `debate.escalated` (both sides, body = the request title), `debate.broken` (the respondent,
a bad reveal). Turn changes do not notify; poll with `agentnet wait`.
