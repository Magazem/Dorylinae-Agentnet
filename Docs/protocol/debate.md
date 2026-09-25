# Debate

Status: **draft** for Phase 3 (plan steps 3.1, 3.2, 3.4 and 3.5). The Decision record (3.3)
is in [decision.md](decision.md). Ticket split: [../review/42-phase3-tickets.md](../review/42-phase3-tickets.md).
Change this document first.

Conventions are those of [request.md](request.md) and [work-session.md](work-session.md):
`<key>` is an identity key in wire form, wire times are RFC 3339 UTC with `Z` and whole
seconds, bodies are canonical JSON ([agent-card.md](agent-card.md#canonical-serialisation))
parsed strictly, optional members are absent (never `null`), "code points" counts Unicode
scalar values, and "no control characters" means none of U+0000–U+001F and U+007F.

## Model

A **debate** is a question two teammates' agents argue in a fixed structure, ending in one
[Decision](decision.md) that both daemons sign. It is not a new transport or lifecycle. It
reuses the Phase 2 path exactly:

1. **A** (the *initiator*) sends a [request](request.md) of the new type **`debate`**. Its
   brief is the topic, and it carries A's **commitment** to A's opening position
   ([Commit–reveal](#commitreveal)).
2. **B** (the *respondent*) accepts it. As for every request, the accept opens a
   [work session](work-session.md) in the same transaction (OD-P2-11), with the derived id
   `s-…`. The session's `kind` is `debate`.
3. The two agents exchange **entries** (positions, moves, a proposal and an answer) as sealed
   mail in fixed turns ([Turns](#turns)).
4. The debate ends `agreed` or `escalated` (both produce a signed Decision), or `cancelled`
   (no Decision). The work session closes, and B completes the request through the
   unchanged Phase 1 path ([work-session.md §Closing the request](work-session.md#closing-the-request)).

Two parties only (plan). A third teammate takes part as a human who asks either participant
to add a [constraint](#human-constraints-34); a third daemon never joins.

**Authority.** As for work sessions, **A is authoritative** for the debate's state (turn,
timeouts, close) and B mirrors it. A applies B's entries only in B's turn; B applies A's
entries in slot order. Unlike a work session, the *content* of the debate (the transcript)
is identical on both sides by construction: every entry is mailed to the other side and
has a fixed slot. So each daemon derives the Decision from its own copy, and the two
signatures cover the same bytes without the Decision ever being sent
([decision.md §Signing](decision.md#signing)).

### What a debate session does not do

- **No grants.** `grant_create` on a debate session is `bad_state` ("debates carry no
  grants"). Evidence travels as pointers ([Evidence](#evidence)) and as context files
  (below). Scope: Phase 3 acceptance does not need grants in a debate (OD-P3-4).
- **No `ws.result`, `accept-result`, `request-changes`, `release` or `discard`.** They are
  `bad_state` on a session of kind `debate`. A `ws.result` for a debate session is ignored
  (`ws.ignored {reason: "kind"}`, inbox copy stored blank as for every ignored result, D18).
- **No quarantine inside the debate.** A debate carries no grant, so the session-level
  quarantine rule cannot hold. The **peer-wide** clause (rule 2 of
  [work-session.md §Quarantine](work-session.md#quarantine-24)) is handled at the edges
  instead ([Quarantine interplay](#quarantine-interplay)).

## Request type `debate`

`type` gains the value **`debate`** ([request.md §Request object](request.md#request-object)),
and one member is added:

| Member | Req. | Type | Rules |
|---|---|---|---|
| `debate` | iff `type = debate` | object | `{"commitment", "rounds", "turn_timeout_s"}`, exactly these members. `commitment`: 64 lowercase hex ([Commit–reveal](#commitreveal)). `rounds`: integer 1–5, the maximum number of challenge rounds (default 2 at the CLI). `turn_timeout_s`: integer 300–86400 (default 3600) |

- `brief` is the **topic** (the request brief rules: 1–16384 bytes). `title` as for every
  request; `agentnet debate` defaults it to the first line of the topic, cut as in
  [consult.md](consult.md#agentnet-consult).
- `context` ([consult.md §Context files](consult.md#context-files)) is allowed for
  `question` **and `debate`**, with the same caps and the same `MaxQuestionBody` (327680
  bytes) total. Every other type keeps the Phase 2 rules.
- `requested_grant` and `run` are refused on a `debate` (`bad_request` / `bad_body`).
- A `debate` without the `debate` member, or any other type with it, is `bad_request`
  (sender) / `bad_body` (recipient).
- A creates its `debates` row (`invited`, with the committed position at slot 0) in the
  `request_submit` transaction; B creates its row (`invited`, with the commitment) in the
  transaction that stores the received request. A decline, a `request.cancel` or an
  auto-decline closes both rows `cancelled`; A's committed position is never sent.

The `requests.type` CHECK constraint gains `debate` (migration 19 rebuilds `requests`,
[Persistence](#persistence)). A Phase 2 daemon rejects a `debate` request as `bad_body`
(unknown type and member), so the sender's mail ends `failed` with `bad_body`; the request
view shows it. Mixed Phase 2/Phase 3 pairs cannot debate (a documented beta limitation,
added to `Docs/beta/known-limitations.md` by 3.1a).

Urgency, the urgency budget, idempotency, deferral, decline, `request.cancel` before accept
and the inbox are unchanged: a debate invitation is an ordinary request until it is
accepted. **Idempotency (review 43 L11):** the `params_hash` of a `debate` submit also covers
the canonical `position`, `rounds` and `turn_timeout_s` of the IPC `debate` member. A retry with
the same key and the same parameters returns the first request and its commitment (no new
nonce is drawn), and a retry with a different position is `idempotency_conflict`. Otherwise an
agent could believe it committed to a position that is not the one stored.

## Commit–reveal

The plan asks each side to commit to a position before seeing the other's, to reduce
anchoring. The protocol achieves that with **one** commitment, because only the side that
speaks first needs one:

- **A** writes its opening position before sending the request. A's daemon (never the
  agent) draws a nonce, computes the commitment, sends only the commitment in the request,
  and keeps the position and the nonce locally.
- **B** never sees A's position before B's own opening position has left B's daemon: A's
  daemon reveals A's position **only after it has applied B's position** (slot 1).
- **A** cannot change its position after seeing B's: the reveal must match the commitment,
  which B's daemon checks.

So neither opening position is influenced by the other. A symmetric scheme (both commit,
then both reveal) adds two mails and a phase without adding a property. OD-P3-1 records
the choice.

**Commitment.**

```
nonce      = 32 bytes from crypto/rand, as 64 lowercase hex
commitment = lowercase-hex SHA-256( "dorylinae-debate-commit-v1\n" ‖ sid ‖ "\n" ‖ A ‖ "\n"
                                    ‖ nonce ‖ "\n" ‖ canonical(position) )
```

`sid` is the [derived session id](work-session.md#session-id) for `(A, B, request_id)`,
which A knows before sending. `A` is A's identity key in wire form. `position` is A's
opening [position](#position) object. Binding `sid` and `A` means B cannot copy A's
commitment into another debate, and cannot claim A's commitment as B's own. The nonce makes
the commitment hiding: B cannot confirm a guessed position (for example "yes" or "no") by
hashing it.

Vector (pairing vector keys: A = seed `00…1f`, B = seed `20…3f`; `request_id =
r-0123456789abcdef0123456789abcdef`, so `sid = s-36375782ceb6baea9cee4d4273dfb035`):

```
nonce       404142434445464748494a4b4c4d4e4f505152535455565758595a5b5c5d5e5f
position    {"argument":"Retries should back off exponentially, capped at 10 minutes.","assumptions":["Clock skew between peers is under 5 s"],"claim":"Use capped exponential backoff for outbox retries","evidence":[{"kind":"file","ref":"internal/mail/outbox.go"}],"rejected_alternatives":[{"option":"Fixed 30 s retry","reason":"Floods the relay after an outage"}]}
commitment  a24d1e0306c3bd0a129a4f15da39d0fce4f36258c13b2c4c65ba0e2b6cb81ed2
```

**Reveal.** When A's daemon applies B's slot-1 position, it sends, in the same transaction,
`debate.reveal {at, nonce, position, request, session}` ([Kinds](#kinds)). B's daemon checks
that `nonce` is exactly 64 lowercase hex, that `position` is canonical and passes the
[schema](#position), and recomputes the commitment from the request it stored at receipt
(never from anything in the reveal except `nonce` and `position`). On a match it stores A's
position as slot 0. A schema failure is treated like a changed reveal (below).

**What stops a late or changed reveal.**

- *Changed:* a reveal whose hash differs from the commitment is refused. B's daemon marks
  the debate `broken` (a local state: no more entries are accepted or sent, no Decision),
  audits `debate.reveal_bad {session, peer}`, notifies `debate.broken`, and sends a
  `ws.cancel` to A. In the same transaction B closes its work-session mirror `cancelled`,
  completes the request through the Phase 1 path (`note = "session cancelled"`) and writes its
  [experience record](experience.md); a later `debate.close` from A is stored for the record
  and changes nothing (review 43 L3). A correct A daemon never sends a bad reveal: the reveal
  is built from the stored position and nonce, not by the agent.
- *Late:* the reveal is automatic and immediate on A, so it is late only if A's daemon is
  modified or offline. B's mirror shows `waiting: "reveal"` with the deadline. B has no
  authority to time out; B's human or agent may **abandon** with `agentnet debate <id>
  --cancel` ([Cancel](#cancel-and-abandon)). A modified A gains nothing by delaying: its
  position is already fixed.
- *Withheld B position:* B simply never sends slot 1; A's turn timeout closes the debate
  `cancelled` ([Timeouts](#timeouts)).
- *Leaks outside the protocol* (A's agent writes its position into the topic, or the two
  agents talk elsewhere) are not prevented. Commit–reveal reduces anchoring inside the
  protocol; it is not a secrecy mechanism.
- *Placeholder positions:* either side can open with a thin position and replace it through
  a `revision` after seeing the other's. That is allowed by design (a symmetric scheme has
  the same property); the Decision keeps both `initial` and `final`, so a reader sees it.

## Turns

The transcript is a list of entries with fixed **slots**:

| Slot | Author | Kind | When |
|---|---|---|---|
| 0 | A | `position` (revealed) | sent automatically after slot 1 is applied on A |
| 1 | B | `position` | after accept (or together with it, [below](#submitting-entries)) |
| 2, 3 | A, B | `move` (round 1) | A first, then B |
| … | A, B | `move` (round k) | … up to round `rounds` |
| next | A | `proposal` | the converge step |
| next + 1 | B | `answer` | the converge step |

**Moving to converge.** After each applied `move`, the next slot is the `proposal` (not
another move) when **either**:

1. the last two applied moves both have **no challenges** (`challenges: []`), in any
   order: both sides have nothing left to challenge; or
2. B's move of round `rounds` was applied (the rounds are used up).

Both rules depend only on the transcript, so both daemons compute the same next slot. A
`move` in slot `next` (after rule 1 or 2 fired) is `bad_state` at IPC and `bad_body` on
receipt.

**Outcome.** From the `answer`: `accept: true` → `agreed`; `accept: false` → `escalated`
(the plan's "no agreement after N rounds"). A timeout after both positions exist also ends
`escalated` ([Timeouts](#timeouts)). Escalation is **not** an error: the Decision is still
produced and signed (3.5), with the remaining disagreement filled in.

Early escalation by one side before the converge step (a `--escalate` flag) is deferred:
not needed for the acceptance tests (OD-P3-7 lists it).

### State (on A, mirrored on B)

`debates.phase`:

| Phase | Meaning | Next slot author |
|---|---|---|
| `invited` | request sent (A) or received (B), not accepted | — |
| `positions` | session open, slot 1 (B's position) or slot 0 (reveal) outstanding | B, then A's daemon |
| `rounds` | moves | alternating, A first |
| `converge` | proposal, then answer | A, then B |
| `closing` | A decided the outcome; waiting for the peer's signature ([decision.md](decision.md#signing)) | — |
| `closed` | final: `outcome` ∈ `agreed`, `escalated`, `cancelled` | — |
| `broken` | B only: a bad reveal ([above](#commitreveal)) | — |

The work session row stays `open` until the debate is `closing` or `closed`, and then
closes in the same transaction as the phase change (`outcome = accepted` when a Decision
exists, `cancelled` otherwise). The work-session states `awaiting_result` and `quarantined`
are never used by a debate.

## Messages (3.2)

Every entry validates against a strict schema: exactly the listed members, optional members
absent, every limit checked by one function (`internal/debate.Validate…`) on both sides
(sender at IPC, recipient in `Apply`), as for requests.

**The free-text rule** (plan 3.2): the only multi-line free text is the member named
**`argument`**. Every other string is a **single line**: 1–N code points, no control
characters at all, no leading or trailing white space. So a claim, an assumption, an
evidence note or a disagreement point is a short labelled statement, and the reasoning lives
in `argument`, where a reader expects prose. `argument` allows `\n` and `\t` and no other
control character.

**Debate text** (review 43 M7, L1): in every debate string, including `argument`, the
constraint text and the topic of a `debate` request, "control characters" means U+0000–U+001F,
U+007F **and** U+0080–U+009F (C1, which some terminals treat as escape introducers), and
U+2028 and U+2029 are refused as well. That keeps the IPC encoding of a debate the same size as
its canonical form ([IPC](#ipc)). Other invisible characters (bidi controls, zero-width
characters) are allowed in entries, which are untrusted input anyway, and are made visible in
the [Markdown](decision.md#markdown). Constraints are stricter ([Human constraints](#human-constraints-34)).

"Line(N)" below means a single-line string of 1–N code points.

### Position

| Member | Req. | Type | Rules |
|---|---|---|---|
| `claim` | yes | string | Line(280) |
| `assumptions` | no | array | 1–10 × Line(280) |
| `evidence` | no | array | 1–10 [evidence](#evidence) |
| `rejected_alternatives` | no | array | 1–5 × `{"option": Line(120), "reason": Line(280)}` |
| `argument` | yes | string | 1–4000 code points, multi-line |

### Evidence

`{"kind", "ref", "note"?}`: `kind` ∈ `file`, `commit`, `url`, `test`, `doc`, `measurement`;
`ref` Line(1024) (a path, a commit id, a URL, a test name, a document name or a measured
value); `note` Line(280). Evidence is a **pointer**, as request artifacts are: no daemon
fetches, opens or checks it, and it grants nothing. A `url` is not validated beyond the
line rule (it is rendered inert, [decision.md §Markdown](decision.md#markdown)).

### Challenge

| Member | Req. | Type | Rules |
|---|---|---|---|
| `targets` | yes | array | 1–5 distinct [targets](#targets) in the **other** side's current position |
| `argument` | yes | string | 1–4000 code points, multi-line |
| `evidence` | no | array | 1–5 [evidence](#evidence) |

### Targets

A target names one item of the other side's **current** position (its latest revision at
the time of the move): `claim`, `argument`, `assumptions/<i>`, `evidence/<i>` or
`rejected_alternatives/<i>`, with `<i>` a decimal index without leading zeros that exists in
that position. The sender's daemon checks the index against its transcript, and the
recipient checks it again against its own (identical) transcript; an out-of-range index is
`bad_request` / `bad_body`.

### Move

| Member | Req. | Type | Rules |
|---|---|---|---|
| `challenges` | yes | array | 0–3 [challenges](#challenge). Empty means "nothing to challenge" (the converge rule) |
| `revision` | no | object | A full new [position](#position) of the mover, replacing its current one. Its `argument` explains the change |

A move with no challenges and no revision is a pass. The plan's separate "revision" message
is the `revision` member of a move, so every round has exactly one entry per side.

### Proposal (A, converge step)

| Member | Req. | Type | Rules |
|---|---|---|---|
| `agreement` | yes | object | `{"decision": Line(280), "points"?: 1–10 × Line(280), "argument"?: 1–4000 multi-line}` |
| `remaining_disagreement` | no | array | 1–10 [disagreements](#disagreement) |
| `affected_artifacts` | no | array | 1–20 artifacts in the [request artifact](request.md#artifacts) shape and rules |

### Answer (B, converge step)

| Member | Req. | Type | Rules |
|---|---|---|---|
| `accept` | yes | boolean | `true`: B agrees with the proposal's `agreement`. `false`: escalate |
| `remaining_disagreement` | no | array | 1–10 [disagreements](#disagreement), added to the proposal's |
| `argument` | no | string | 1–4000 code points, multi-line |

### Disagreement

`{"point": Line(280), "initiator": Line(280), "respondent": Line(280)}`: the point and each
side's view in one line each.

### Size caps

| Limit | Value | Sender | Recipient |
|---|---|---|---|
| Field caps | as above | `bad_request`, field path | `bad_body` |
| Canonical entry (the `entry` member) | **32768 bytes** (`MaxDebateEntry`) | `entry_too_large` | `bad_body` |
| Entries per debate | 2 + 2 × `rounds` + 2 ≤ 14 | — | structural |

The total is checked after the field caps (as for requests). Fourteen entries of 32 KiB
bound the transcript at 448 KiB, which bounds the Decision ([decision.md](decision.md#size)).
Every body fits one mail (`MaxMailPlaintext` 716800).

All entry text is **content**: never audited, logged, notified or sent to a webhook
([Audit](#audit)).

## Human constraints (3.4)

`agentnet debate <id> --constrain "TEXT"` adds a constraint: a statement from a human that
both agents must respect from then on ("must stay compatible with Go 1.22", "no new
dependency"). It appears in the Decision under **human decisions**.

- **Who.** The local user on **either** side, while the debate is in `positions`, `rounds`
  or `converge`. A third teammate asks one of the two.
- **Approval-gated.** The daemon cannot tell a human from an agent over IPC, and a
  constraint is shown to others as a *human* decision in a signed record. So `--constrain`
  creates a [human approval](approval.md) of the new kind **`debate_constraint`** (the
  approval window shows the peer, the session and the constraint text in full). Only on
  approval is the constraint stored and sent. An agent that runs `--constrain` gets
  `{"approval": …, "state": "pending"}` and cannot finish it (OD-P3-3). Headless machines use
  terminal mode, as for every approval (the 3.H harness script confirms it).
- **Text.** Line(500) (a single line, no control characters in the [debate text](#messages-32)
  sense), and **only visible characters** (review 43 H3): every rune must satisfy
  `unicode.IsGraphic` or be U+0020, and none may be a format character (`unicode.Cf`: bidi
  controls, zero-width characters, U+FEFF, the invisible tag characters U+E0000–U+E007F, and so
  on). Otherwise `bad_request` at `debate_constrain` and `bad_body` on receipt. The reason: the
  human approves what the window shows, and the peer's *agent* reads the raw bytes, so any
  invisible character would let a local agent smuggle an unseen instruction into a record that
  says "human decision". The approval summary also renders the text with the review-40
  `DisplayQuote` rule (`internal/device/scope.go`), never through `notify.Clean` (which does
  not handle zero-width or tag characters). On Linux the approval window receives the summary
  through argv (D20), so other local users can see a constraint's text while the window is
  open; that is the accepted D20 boundary.
- **Id.** `c-` + 32 hex from `crypto/rand`, chosen at `--constrain`. **`at`** is the time of
  the approval (`approval_confirm`), whole seconds; the author stores exactly the `at` it
  sends, so both sides order constraints identically.
- **Transport.** On approval, in one transaction: store the constraint `active`, audit
  `debate.constraint {session, peer, id, approval}`, and send `debate.constraint {at, id,
  request, session, text}` to the peer. The peer stores it (author = `msg.from`) and
  notifies `debate.constraint`. Precondition at confirm: the debate is still in one of the
  three phases above (else `rejected`, reason `precondition`).
- **Ordering.** Constraints are not slots. They are a set, ordered in the Decision by
  `(at, id)`. A's `debate.close` lists the ids A holds as `active`; both Decisions contain
  exactly those ([decision.md](decision.md#derivation)). **A is authoritative for the set.**
  An A-authored constraint mail can be overtaken by the close, so B holds the close until it
  has every listed id ([decision.md §Signing](decision.md#signing), review 43 H2). A
  constraint that reaches A after the close is not in the record (audited `debate.ignored
  {reason: "closed"}` on A); B's view marks its own unlisted constraints `late`.
- **Limits.** At most 10 `active` constraints per debate (both sides together). The sender
  checks the count it holds when adding (`constraint_limit`). The receiver never drops a valid
  constraint for the limit, because A's close may list it (two sides adding their 10th at the
  same time): A stores an 11th received one as `excess` (not shown to agents, never listed in
  the close, `debate.ignored {reason: "limit"}`); B stores it as `active` and lets A's close
  decide. A receiver stores at most 20 constraints per debate in total; beyond that the mail is
  ignored (a peer that sends more is misbehaving).
- **Agents see them.** `debate_show` lists the active constraints, so both agents can
  respect them in their next entry. The daemon does not check that they do.
- **What the record proves.** A constraint signed into the Decision proves that the named
  side's **daemon** accepted it through a local approval. It does not prove who typed the
  code (the approval boundary of [approval.md §Threat model](approval.md#threat-model)
  applies), and the other side cannot verify the approval, only the author key. A modified
  peer daemon can therefore put an unapproved "human decision" under **its own** side's name
  (never under the other side's). The Markdown says so for every constraint: "approved on the
  initiator's (respondent's) machine; the other side cannot check this" (review 43 L16).

## Timeouts

A enforces one per-turn deadline: `turn_deadline = time the previous slot was applied (or
the session opened) + turn_timeout_s`. A's daemon checks it in its existing sweep (at least
once a minute) and on every debate IPC call. When the side whose slot is next misses it
(A's own agent included):

| Missing | Close |
|---|---|
| slot 1 (B's position) | `cancelled`, reason `timeout`, no Decision (there is nothing to record) |
| any later slot | `escalated`, reason `timeout`, Decision produced and signed with what exists |

The reveal (slot 0) is automatic on A, so it cannot time out on A. B keeps its own view of
the deadline only for display (`waiting`, `deadline`); B never closes on time.

## Cancel and abandon

- **A:** `agentnet debate <id> --cancel` (or `session <id> --cancel`) in `invited`: a
  Phase 1 `request.cancel`. After accept, in `positions`, `rounds` or `converge`: close
  `cancelled`, no Decision.
- **B:** after accept, the same command sends the existing `ws.cancel` (reason optional,
  content). A applies it while the debate is open: close `cancelled`. After A decided an
  outcome, it is refused as for sessions.
- **B abandon:** when A's daemon is silent (no reveal, no entry, no close) past the deadline
  B displays, `--cancel` on B also closes B's mirror **locally** (`closed`, `cancelled`,
  audit `debate.abandon {session, peer}`) after sending `ws.cancel`. A later `debate.close`
  from A is then stored for the record but changes nothing, and B does not sign it. This is
  B's only protection against a stalled or modified A.

A cancel refunds nothing (D12).

**`request_complete` and early complete on a debate** (review 43 M4). A debate has no
result, so:

- On B, `request_complete` (`agentnet complete`, which the snippet teaches) on a debate
  request whose session is not `closed` is `bad_state` ("use agentnet debate"); the Phase 2
  shorthand for `ws_result` does not apply. B's daemon completes the request itself when the
  mirror closes (normal close, [abandon](#cancel-and-abandon), [broken](#commitreveal)).
- On A, a `request.complete` from B for a debate request while A's session is not `closed`
  is the [early complete](work-session.md#early-complete-and-phase-1-workers) of Phase 2,
  with two differences: its `result` and `note` are **always** dropped unread (inbox copy
  blank, D18), and if the debate is in `positions`, `rounds` or `converge` A closes it
  `cancelled`, reason `cancelled`, no Decision (this is how B's abandon reaches A). In
  `closing` it changes nothing (A already has its Decision).

## Submitting entries

`debate_submit {id, kind, entry}` ([IPC](#ipc)):

1. Resolve the session (`s-` or `r-` id). The session must be of kind `debate`
   (`bad_state` otherwise).
2. `kind` must be the kind of the next slot and the caller must be its author (`not_your_turn`
   otherwise, with the expected kind and author in the message).
3. Validate the entry ([Messages](#messages-32)), including targets against the local
   transcript.
4. **On A:** in one transaction, store the entry at its slot, advance the phase, set the new
   `turn_deadline`, and `SubmitTx` a `debate.entry` to B. If the entry is the proposal and
   B's answer arrives later, nothing else happens now.
5. **On B:** in one transaction, store the entry at its slot as `sent` and `SubmitTx` a
   `debate.entry` to A. B's mirror treats it as applied for turn purposes.

**Accept and position in one step (B).** `debate_submit` with `kind: "position"` on a
`debate` request that is `pending` or `deferred` does, in one transaction on B: accept the
request, open the session and store and send B's position, exactly like `result` on a
pending question ([consult.md §Answering](consult.md#answering)).

**Applying peer entries** (inside the mail dedupe transaction):

- **On A** (`debate.entry` from B): strict body, derived id, session open and of kind
  `debate`, phase not `closing`/`closed`, `slot` = the next slot and its author is B,
  schema and targets. Otherwise ignore, audit `debate.ignored {session, peer, kind, reason}`
  and re-send A's `last_state` echo (the 10-minute rule of requests) so a B that missed an
  entry catches up. On success: store, advance, set the deadline; on slot 1 also send the
  reveal; on B's answer, decide the outcome and start [closing](decision.md#signing).
- **On A, slot 1 before the accept** (review 43 M3). With the one-step accept + position, B
  sends `request.accept` and the slot-1 `debate.entry` in one transaction, and mail can
  overtake mail. B never re-sends an entry it considers applied, so A must not drop it. A valid
  slot-1 entry from B for a `debate` request that A holds `pending` or `deferred` (A's
  `debates` row `invited`) is applied **as if the accept had arrived first**, exactly as an
  early `ws.result` opens the session in Phase 2
  ([work-session.md §Ordering](work-session.md#ordering)): in one transaction A opens its
  session (kind `debate`), moves the debate to `positions`, applies slot 1 and sends the
  reveal. The `request.accept` that arrives later creates nothing new. The same entry for a
  request A cancelled or that was declined is ignored.
- **On B** (`debate.entry` or `debate.reveal` from A): the same checks with A as author. An
  entry for a slot beyond the next is **held** (stored as `early`) until the missing slot
  arrives; mail can overtake mail. A duplicate slot is ignored.

**The echo.** A keeps the last mail it sent for the debate (an entry, the reveal or the
close) as `last_state`, and re-sends it when B's entry is ignored, at most once per 10 minutes.

## Quarantine interplay

The quarantine's peer-wide clause ([work-session.md §Quarantine](work-session.md#quarantine-24),
rule 2) exists so that a peer who read sensitive data cannot return it through a grant-less
session. A debate would be such a channel in both directions, and debate entries are content
the other side's agent reads at once. Phase 3 closes it at the edges instead of quarantining
entries (OD-P3-4):

- `debate` submit (A) is refused with **`quarantine_active`** when A issued a sensitive grant
  to B whose `exp` is later than `now − 7 d` (the rule-2 test, `capability.Store.QuarantineHolds`
  for the peer).
- Accepting a debate (B), including the one-step accept + position, is refused with
  `quarantine_active` under the same test from B's side (B issued a sensitive grant to A).
- `grant_create` of a **sensitive** grant to a peer with whom the grantor has a debate in
  `invited`, `positions`, `rounds` or `converge` is refused with **`debate_open`**. `invited`
  is included (review 43 M5): otherwise A could invite, then issue a sensitive grant to B in
  another session, and B's accept (which checks only B's grants) would open a debate while
  rule 2 holds on A.
- The same test is a **precondition at `approval_confirm`** for a sensitive grant that was
  `pending_approval` when the debate started, and it applies to grants created through a
  [policy](grant.md) as well: a policy never activates a sensitive grant to a peer with an open
  debate (the grant is refused `debate_open`, audited as for every refused grant).

The debate request's `context` files and the topic are sent **by** the initiator; they are
ordinary request content, as in Phase 2.

## Kinds

All are sealed [mail](mail.md), outboxed, acked, registered with `Inbox: true` and strict.
A validation failure is `mail.ErrBadBody`. Every body's `session` must be the derived id for
the pair and `request` (initiator = the side that sent the request), so a body cannot be
moved to another debate. `debate.` and `decision.` are added to `reservedKindPrefixes` in
`internal/daemon/outbox.go`, so `mail_submit` can never send them (review 36, L7).

| Kind | Direction | Body |
|---|---|---|
| `debate.entry` | both | `{"at", "entry", "kind", "request", "session", "slot"}`; `kind` ∈ `position`, `move`, `proposal`, `answer`; `slot` 1–13 (0 travels only as a reveal) |
| `debate.reveal` | A → B | `{"at", "nonce", "position", "request", "session"}` |
| `debate.constraint` | both | `{"at", "id", "request", "session", "text"}` |
| `debate.close` | A → B | `{"at", "constraints", "decision"?, "entries", "outcome", "reason", "request", "session", "sig"?}`: [decision.md §Signing](decision.md#signing) |
| `debate.sign` | B → A | `{"at", "decision", "request", "session", "sig"}` or `{"at", "decision", "refused", "request", "session"}`: [decision.md §Signing](decision.md#signing) |

B's cancel uses the existing `ws.cancel`. `ws.state` is **not** sent for debate sessions:
`debate.close` closes B's mirror, and B then completes the request (`note` = the fixed text
`debate agreed`, `debate escalated` or `session cancelled`, no `result`).

## Persistence

Migration **19** (ticket 3.1a), in one migration:

```sql
-- requests: rebuild to add 'debate' to the type CHECK (SQLite cannot alter a CHECK).
-- Same columns (including migration 12's result), same indexes, rows copied unchanged.
CREATE TABLE requests_new ( … type TEXT NOT NULL CHECK (type IN ('review','task','question','debate')), … );
-- Explicit column lists on BOTH sides (review 43 M10): migration 12 appended `result` as the
-- last physical column, so `SELECT *` into a table declared in another order would shift
-- data silently.
INSERT INTO requests_new (direction, peer, id, team_id, type, urgency, urgency_declared,
    downgraded_by, body, body_hash, state, state_seq, state_at, deferred_until, decline_code,
    reason, note, first_response, first_response_at, created, received_at, mail_id,
    last_reply, last_reply_sent, idem_key, params_hash, cancel, cancel_at, cancel_mail_id,
    updated, result)
  SELECT direction, peer, id, team_id, type, urgency, urgency_declared,
    downgraded_by, body, body_hash, state, state_seq, state_at, deferred_until, decline_code,
    reason, note, first_response, first_response_at, created, received_at, mail_id,
    last_reply, last_reply_sent, idem_key, params_hash, cancel, cancel_at, cancel_mail_id,
    updated, result FROM requests;
DROP TABLE requests;
ALTER TABLE requests_new RENAME TO requests;
-- recreate requests_idem, requests_state, requests_peer_time exactly as in migration 11
-- (no table references requests or approvals by FOREIGN KEY and none has a trigger, checked
-- against migrations 1-17, so DROP + RENAME is safe with foreign_keys=1)

-- approvals: rebuild to add 'debate_constraint' to the kind CHECK (same pattern, explicit
-- column lists, recreate approvals_state).

ALTER TABLE work_sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'work'
    CHECK (kind IN ('work', 'debate'));

CREATE TABLE debates (
    session        TEXT PRIMARY KEY,               -- s-<32 hex>, derived
    role           TEXT NOT NULL CHECK (role IN ('initiator', 'respondent')),
    peer           TEXT NOT NULL,
    request_id     TEXT NOT NULL,
    rounds_max     INTEGER NOT NULL CHECK (rounds_max BETWEEN 1 AND 5),
    turn_timeout_s INTEGER NOT NULL CHECK (turn_timeout_s BETWEEN 300 AND 86400),
    commitment     TEXT NOT NULL,
    nonce          TEXT,                           -- initiator only; kept after the reveal (re-send)
    phase          TEXT NOT NULL CHECK (phase IN ('invited','positions','rounds','converge','closing','closed','broken')),
    next_slot      INTEGER NOT NULL DEFAULT 1,
    turn_deadline  TEXT,
    outcome        TEXT CHECK (outcome IN ('agreed', 'escalated', 'cancelled')),
    reason         TEXT CHECK (reason IN ('accepted', 'rejected', 'timeout', 'cancelled', 'abandoned')),
    last_state     TEXT CHECK (last_state IS NULL OR json_valid(last_state)),  -- initiator: {"kind","body"}
    last_state_sent TEXT,
    close_body     TEXT CHECK (close_body IS NULL OR json_valid(close_body)),  -- respondent: a close held for missing entries
    created        TEXT NOT NULL,
    updated        TEXT NOT NULL,
    CHECK ((phase = 'closed') = (outcome IS NOT NULL))
);
CREATE TABLE debate_entries (
    session  TEXT NOT NULL,
    slot     INTEGER NOT NULL CHECK (slot BETWEEN 0 AND 13),
    author   TEXT NOT NULL CHECK (author IN ('initiator', 'respondent')),
    kind     TEXT NOT NULL CHECK (kind IN ('position', 'move', 'proposal', 'answer')),
    entry    TEXT NOT NULL CHECK (json_valid(entry)),  -- canonical entry (content)
    at       TEXT NOT NULL,                            -- the carrying body's "at" (wire form)
    state    TEXT NOT NULL CHECK (state IN ('committed', 'applied', 'sent', 'early')),
    PRIMARY KEY (session, slot)
);
CREATE TABLE debate_constraints (
    session  TEXT NOT NULL,
    id       TEXT NOT NULL,                            -- c-<32 hex>
    author   TEXT NOT NULL CHECK (author IN ('initiator', 'respondent')),
    text     TEXT NOT NULL,                            -- content
    at       TEXT NOT NULL,
    state    TEXT NOT NULL CHECK (state IN ('pending_approval', 'active', 'late', 'excess')),
    approval TEXT,
    PRIMARY KEY (session, id)
);
```

A's slot-0 position is stored `committed` at submit (the request transaction) and becomes
`applied` when revealed. Rows are kept indefinitely (like `work_sessions`). `debates`,
`debate_entries`, `debate_constraints` and the rebuilt tables go into the DROP lists of
**both** rewind tests in `internal/store/store_test.go`; the requests rebuild gets its own
test (rows, `result` column, indexes and the unique idempotency index survive).

## IPC

Every method returns within 2 s and never waits for a peer.

**Debate view:**

```json
{
  "session": "s-…", "request": {"id", "title"}, "role": "initiator"|"respondent",
  "peer": <peer ref>, "team": {"id", "name"}, "phase", "outcome"?, "reason"?,
  "rounds": {"max", "current"}, "turn": "you"|"peer"|"none",
  "expect"?: "position"|"move"|"proposal"|"answer", "deadline"?, "waiting"?: "reveal"|"entry"|"signature",
  "topic", "context"?: [{"name", "bytes"}],
  "transcript": [{"slot", "author", "kind", "at", "entry"}],
  "constraints": [{"id", "author", "at", "text", "state"}],
  "decision"?: {"id", "hash", "state"}
}
```

The list view omits `topic`, `transcript` and constraint texts, and keeps counts.

**Size** (review 43 M7). The full view is at most about 448 KiB of transcript plus the topic
(≤ 32 KiB escaped) and ≤ 20 constraints, so it fits the 1 MiB IPC line **only** if the IPC
encoding does not inflate it. `internal/ipc` today encodes results with `json.Marshal`, which
writes `<`, `>` and `&` as six-byte `<` escapes, so a transcript of `<`-heavy text (or a
peer doing it on purpose) would reach 2.7 MB and break `debate_show` and `wait`. Ticket 3.1b
therefore switches the IPC server's encoder to `SetEscapeHTML(false)` ([ipc.md](ipc.md)), and
the [debate text](#messages-32) rule refuses U+2028/U+2029 (which Go always escapes). With
both, the view's size is its canonical size plus a few hundred bytes.

| Method | Params | Result / errors |
|---|---|---|
| `request_submit` | gains `debate: {"position", "rounds"?, "turn_timeout_s"?}` for `type = debate` | as Phase 2, plus `session`. The daemon computes nonce and commitment. `bad_request` naming the field; `quarantine_active` |
| `debate_list` | `{"phase"?, "peer"?}` | `{"debates": [<list view>]}`, newest first |
| `debate_show` | `{"id"}` (`s-` or `r-`) | `{"debate": <view>}`. `unknown_session` |
| `debate_submit` | `{"id", "kind", "entry"}` | `{"debate": <view>, "mail_id"}`. `bad_state`, `not_your_turn`, `bad_request`, `entry_too_large`, `quarantine_active` (one-step accept) |
| `debate_constrain` | `{"id", "text"}` | `{"approval": <approval view>}`. `bad_state`, `bad_request`, `constraint_limit`, the approval errors |

Cancel is `ws_cancel`. New error codes: `not_your_turn`, `entry_too_large`,
`constraint_limit`, `quarantine_active`, `debate_open` (exit 1).

## CLI

Per-command page `Docs/cli/debate.md` (ticket 3.1b).

| Command | IPC | Notes |
|---|---|---|
| `agentnet debate @peer --topic TEXT \| --topic-from-file F --position-file P [--context-file F]… [--rounds N] [--turn-timeout D] [--title T] [--team T] [--urgency U --urgency-reason R] [--idempotency-key K] [--json]` | `request_submit` | `P` is a JSON [position](#position). Prints `id`, `session` |
| `agentnet debates [--phase P] [--json]` | `debate_list` | |
| `agentnet debate <id> [--json]` | `debate_show` | |
| `agentnet debate <id> --position-file F \| --move-file F \| --propose-file F \| --answer-file F` | `debate_submit` | Each file is the JSON entry of that kind; `-` = stdin. `--position-file` on a pending debate accepts it too |
| `agentnet debate <id> --constrain TEXT` | `debate_constrain` | Prints the approval id; the human approves in the window |
| `agentnet debate <id> --cancel [--reason R]` | `ws_cancel` | |
| `agentnet wait <id>` | polls `debate_show` | For a debate: exit 0 with `wait: "turn"` when `turn` becomes `you`, `wait: "closed"` when closed (with the decision summary), `4` on timeout |

`--help` prints the JSON shape of each entry kind with one example each, so an agent needs
nothing else. The snippet ([Docs/agents/snippet.md](../agents/snippet.md)) gains a short
"debates" paragraph (3.H).

## Notifications

Events for [notify.md](notify.md), content-free (never the topic, entries or constraint
text), all on by default:

- `debate.constraint` (peer's side): "<name> added a constraint to your debate".
- `debate.agreed` (both): "Debate with <name> ended in agreement", body = the request title
  (cleaned on B, where it is peer text).
- `debate.escalated` (both, **3.5**): "Debate with <name> needs your decision: no agreement",
  body as above. Fired on A when it closes `escalated`, on B when B applies that close.
- `debate.broken` (B): "Debate with <name> stopped: the opening position did not match its
  commitment".

The invitation itself is `request.received`. Turn changes do not notify (agents poll with
`wait`). Webhooks carry the event name, request and session ids and the peer only.

## Audit

Never topics, entries, constraint texts, reasons or decision content. Only ids, enums,
counts and sizes ([audit.md](audit.md)).

| Action | Side / actor | Detail |
|---|---|---|
| `debate.start` | A / `cli` | `{session, request, peer, rounds, turn_timeout_s, position_bytes}` |
| `debate.entry` | author / `cli` | `{session, peer, slot, kind, bytes, challenges?, revision?}` (counts and a boolean) |
| `debate.entry_in` | receiver / `daemon` | same as `debate.entry` |
| `debate.reveal` | A / `daemon` | `{session, peer}` |
| `debate.reveal_in` | B / `daemon` | `{session, peer, ok}` |
| `debate.reveal_bad` | B / `daemon` | `{session, peer}` |
| `debate.constraint` | author / `cli` | `{session, peer, id, approval}` |
| `debate.constraint_in` | receiver / `daemon` | `{session, peer, id}` |
| `debate.close` | A / `daemon` (or `cli` for a cancel) | `{session, peer, outcome, reason, entries, constraints, age_s}` |
| `debate.close_in` | B / `daemon` | `{session, peer, outcome, reason, entries}` |
| `debate.abandon` | B / `cli` | `{session, peer}` |
| `debate.ignored` | either / `daemon` | `{session, peer, kind, reason}` |

The Decision's own audit actions are in [decision.md](decision.md#audit).

## Security considerations

- **Authority.** A debate grants nothing. Entries, constraints, the topic and the Decision
  are data; no daemon executes, fetches or acts on them.
- **Forged human decisions.** Constraints need a local approval (OD-P3-3); an agent using
  AgentNet's interface cannot add one, and cannot hide text the human does not see in one
  (visible characters only). A modified peer daemon can add unapproved ones under its own
  side's name only, and the Markdown says the other side cannot check them.
- **Commitment.** Hiding by a 256-bit nonce, binding by SHA-256 over a domain-separated
  preimage that includes the session and the author. The daemon, not the agent, holds the
  nonce and reveals automatically.
- **One writer.** A decides turns, timeouts and the close; B's entries are checked against
  A's state. B cannot skip A's turn, add a round or change the outcome. A cannot fabricate
  an `agreed` outcome: agreement exists only as B's own `answer` entry, and B's daemon derives
  the Decision from its own transcript before signing ([decision.md](decision.md#signing)).
- **Binding.** Every body carries the derived session id and is checked against the pair.
- **Resource use.** ≤ 14 entries of ≤ 32 KiB, ≤ 10 constraints, the request-level caps and
  urgency budget, and the 10-minute echo rule bound what a peer can make a daemon store or
  send.
- **Untrusted text.** Everything the peer writes is untrusted input for the local agent,
  exactly like a brief. The Markdown rendering makes it inert in a repo
  ([decision.md §Markdown](decision.md#markdown)).
- **Version skew.** Phase 2 peers refuse `debate` requests (`bad_body`), a documented beta
  limitation.

## Acceptance (plan 3.1, 3.2, 3.4, 3.5)

- 3.1: a debate with 2 rounds completes headless in two harnesses (ticket 3.H).
- 3.2: every entry kind validates against its schema; a table test shows free text is
  accepted only in `argument` (a newline in any other string is `bad_request`/`bad_body`).
- 3.4: a constraint added through `--constrain` and approved shows in the Decision under
  human decisions; without the approval it appears nowhere.
- 3.5: a forced disagreement (`accept: false`) ends `escalated`, both humans get
  `debate.escalated`, and the Decision is produced and signed by both.
