# Decision records

Status: **draft** for Phase 3 (plan step 3.3, and the escalated case of 3.5). The debate
that produces a Decision is in [debate.md](debate.md). Ticket split:
[../review/42-phase3-tickets.md](../review/42-phase3-tickets.md). Change this document first.

Conventions as in [debate.md](debate.md). Canonical JSON is
[agent-card.md §Canonical serialisation](agent-card.md#canonical-serialisation) (RFC 8785).

## What a Decision is

The artifact of a debate: one JSON object that says what was asked, who took part, what each
side claimed and challenged, what the humans constrained, and how it ended. It is
**derived** deterministically from the debate transcript, so both daemons compute the same
bytes independently, and **signed by both daemons' identity keys**. A Decision exists for a
debate that closed `agreed` or `escalated`; a `cancelled` debate has none.

What the two signatures mean: *"this is the debate as my daemon recorded it"*. They do
**not** mean "I agree": agreement is the `outcome`, taken from the respondent's own `answer`
entry. An escalated Decision is signed the same way (3.5).

## Object

```
decision = {
  "v": 1,
  "id": "d-…",
  "session": "s-…", "request": "r-…", "team": "t-…",
  "participants": {"initiator": <key>, "respondent": <key>},
  "problem": {"title", "topic", "context"?: [{"name", "bytes", "sha256"}]},
  "rounds_max": 1..5,
  "positions": {
     "initiator":  {"initial": <position>, "final"?: <position>},
     "respondent": {"initial": <position>, "final"?: <position>}
  },
  "rounds"?: [{"n", "initiator"?: <move>, "respondent"?: <move>}],
  "converge"?: {"proposal": <proposal>, "answer"?: <answer>},
  "final_agreement"?: <agreement>,
  "remaining_disagreement"?: [<disagreement>],
  "human_decisions"?: [{"id", "by": "initiator"|"respondent", "at", "text"}],
  "affected_artifacts"?: [<artifact>],
  "outcome": "agreed"|"escalated",
  "reason": "accepted"|"rejected"|"timeout",
  "opened", "closed"
}
```

`<position>`, `<move>`, `<proposal>`, `<answer>`, `<agreement>` and `<disagreement>` are the
[debate message](debate.md#messages-32) objects, copied byte-for-byte (they are already
canonical). `<artifact>` is the request artifact shape.

**Plan field → member** (plan 3.3 lists thirteen fields; they all come from the transcript):

| Plan field | Member |
|---|---|
| problem | `problem` (the request title and topic; context files by name, size and SHA-256 of the text, **not** the text) |
| participants | `participants` (identity keys; names are local and are added only by the Markdown renderer) |
| initial positions | `positions.*.initial` |
| assumptions, evidence, arguments, rejected alternatives | inside the positions (`assumptions`, `evidence`, `argument`, `rejected_alternatives`), initial and final |
| counterarguments | the challenges in `rounds` |
| final agreement | `final_agreement` (present iff `outcome = agreed`) |
| remaining disagreement | `remaining_disagreement` |
| human decisions, constraints | `human_decisions`: the approved [constraints](debate.md#human-constraints-34). In Phase 3 a constraint is the only human input, so the plan's two fields are one list; the Markdown heading names both |
| affected artifacts | `affected_artifacts` (from the proposal) |

## Derivation

Each daemon builds the Decision from its own tables at close, with these rules and nothing
else (no local names, no local clock, no config):

1. `id` = `"d-" ‖ lowercase-hex(SHA-256("dorylinae-decision-id-v1\n" ‖ session)[0:16])`.
2. `team`, `problem.title`, `problem.topic` from the stored canonical request; `context`
   in request order, each `{"name", "bytes": len(text), "sha256": hex SHA-256(text)}` where
   `text` is the UTF-8 bytes of the context file's `text` member **exactly as in the stored
   canonical request** (already LF-only; no further transformation); absent when the request
   had none.
3. `positions.X.initial` = slot 0 (initiator) / slot 1 (respondent). `positions.X.final` =
   the last `revision` X made, absent if X never revised.
4. `rounds`: one element per round that has at least one applied move, in order, `n` from 1;
   a side's move is absent if it has none in that round (a timeout). Absent if no move.
5. `converge`: present when a proposal was applied; `answer` inside it when an answer was.
6. `outcome` and `reason`: `agreed`/`accepted` iff the answer has `accept: true`;
   `escalated`/`rejected` iff it has `accept: false`; otherwise `escalated`/`timeout`.
7. `final_agreement` = the proposal's `agreement` iff `agreed`.
8. `remaining_disagreement` = the proposal's list followed by the answer's, exact duplicates
   (same canonical bytes) dropped after the first; absent when empty.
9. `human_decisions`: the constraints whose ids are listed in A's `debate.close`, ordered by
   `(at, id)`; `by` from the author; absent when none. The list is A's (A is authoritative for
   the set); B uses it as given, whatever state its own rows have (`active`, `late` or
   `excess` do not matter, only the id, author, `at` and text).
10. `affected_artifacts` = the proposal's, absent if none.
11. `opened` = the request's `created`; `closed` = the `at` of A's `debate.close`. A's close
    `at` is never earlier than the request's `created`: if A's clock went back since, A sends
    `created` as `at`. B refuses a close whose `at` is earlier (reason `time`; review 47 M1),
    because verify step 5 would reject a Decision with `closed` before `opened`.
12. Only entries with slot `< entries` (from the close) are used; see [Signing](#signing).

Optional members are absent, never empty arrays or `null`.

### Size

The Decision **copies** some entry content twice (review 43 M6): the transcript is at most
14 entries of 32 KiB (458752 bytes), `positions.*.final` repeats up to two revisions (≤ 65536),
`final_agreement` repeats the proposal's agreement, and `remaining_disagreement` and
`affected_artifacts` repeat parts of the proposal and the answer (≤ 3 × 32768 together). The
topic is ≤ 16384 bytes (≤ 32768 escaped), context references about 3 KiB, and 10
constraints of 500 code points about 42 KiB escaped. The worst case is about 740 KB, so
**`MaxDecision` = 786432 bytes** (768 KiB), enforced as a sanity check at derivation (exceeding
it is a bug, not a peer error; a test builds the worst case and checks it derives). A Decision
is never mailed. `decision_show` returns it as a JSON object with the IPC encoder's HTML
escaping off ([debate.md §IPC](debate.md#ipc)), so the view is the canonical size plus the
signatures and names, under the 1 MiB IPC line.

## Signing

```
msg           = "dorylinae-decision-v1\n" ‖ canonical(decision)
decision_hash = lowercase-hex SHA-256(msg)
sig           = base64url( Ed25519-Sign(identity_private_key, msg) )
```

The same identity keys that sign mail and cards (D6). Signing is done by the daemon
automatically; no human step (OD-P3-5).

**Collecting both signatures** (one round trip):

1. **A** decides the outcome (B's answer applied, or a timeout, or a cancel). In one
   transaction: phase `closing` (or `closed` for `cancelled`), derive the Decision, sign it,
   store it in `decisions` with `state = awaiting_peer`, close the work session, and send
   `debate.close {at, constraints, decision, entries, outcome, reason, request, session, sig}`
   (for `cancelled`: no `decision`, no `sig`, and no Decision row). `entries` is the number
   of transcript slots A applied (a B entry that arrived after the close is not counted);
   `constraints` is the sorted list of constraint ids A holds.
2. **B** applies it after its transcript has all `entries` slots **and** every constraint id
   listed in `constraints` (a close that overtakes an A entry or an A constraint is held in
   `close_body` until it arrives; review 43 H2). B does **not** wait for what only B could
   have sent: if a **B-authored** slot `< entries` is missing on B, A's close claims something
   B never sent, and B refuses at once as a mismatch (below). A listed constraint id B does
   not hold is always treated as an A-authored constraint in flight, and the close is held:
   B stores its own constraints at approval, before it sends them, and a constraint that
   reaches B from A is stored with A as its author, so A cannot make B sign a constraint as
   B's that B never approved (review 47). A close that lists an id A never sends is therefore
   held, not refused. A held close is re-checked on every arrival; B's abandon is the way out
   if A never sends the missing mail. B drops its own entries at
   slots `≥ entries` (a late entry A never applied; audited `debate.ignored {reason:
   "late"}`; the stored rows are unchanged, and B's debate view shows them with state
   `late`), derives the Decision itself from its own tables with A's `entries`,
   `constraints`, `outcome`, `reason` and `at`, and checks:
   - its own `decision_hash` equals `decision`;
   - `sig` verifies under A's key over its own `msg`;
   - the outcome is consistent with its transcript (rule 6 above; a `timeout` only while an
     answer is missing);
   - no **A-authored** entry B holds has a slot `≥ entries` (A cannot cut its own applied
     entries), and `entries` is at least 2 (both positions).
   On success, in one transaction: sign, store the Decision with both signatures
   (`state = signed`), phase `closed`, close the mirror and complete the request, and send
   `debate.sign {at, decision, request, session, sig}`.
3. **A** verifies B's `sig` over A's own `msg` for the hash it stored and sets `signed`. A
   `debate.sign` whose `decision` differs from A's stored hash, or whose `sig` does not
   verify, is treated as a refusal (`peer_refused`, B's hash stored as `peer_hash`, audit
   `decision.refuse`).

**If B refuses or never signs.**

- **Mismatch** (B's derivation differs, A's signature is bad, or the outcome is
  inconsistent): B stores A's claimed hash and its own, sets `peer_refused` on its side,
  closes its mirror (`cancelled`), audits `decision.refuse {session, peer, reason}`, notifies
  `debate.broken`, and sends `debate.sign {at, decision: <B's hash>, refused: "mismatch",
  request, session}`. B's phase becomes `broken`. **B's own record** is the Decision B
  derives from its transcript with A's `entries`, `constraints` and `at` and the outcome
  rule 6 gives for that transcript (not A's claim), stored **unsigned** (no `sig_initiator`,
  no `sig_respondent`: B never keeps a signature over bytes it did not check) with
  `state = peer_refused` and `peer_hash` = A's claimed hash. When B cannot derive one
  (fewer than two positions within `entries`, or `at` before `opened`), B stores no row and
  `decision` in its refusal is `Hash` of the empty canonical form (`SHA-256("dorylinae-decision-v1\n")`),
  which no Decision has. A sets `peer_refused`, stores B's hash as `peer_hash`, closes its
  debate (`closed`, the outcome A decided) and notifies `debate.broken`. A correct pair never gets here: the transcript
  is identical by construction, so a mismatch means a bug or a modified daemon, and both
  humans see it.
- **Silence** (B offline, or B abandoned; a Phase 2 daemon cannot get here, it never accepts
  a debate): A's Decision stays `awaiting_peer`. It is still a local record, but it is **not
  a proof of anything the respondent said** (review 43 H1): with one signature, every
  respondent entry in it, including `accept: true`, is only the initiator's claim, and a
  modified initiator daemon (or anyone holding A's key) can produce such a file for a debate
  that never happened. So a single-signed file is **unconfirmed** everywhere: `decision
  verify` does not exit 0 for it ([Signed file](#signed-file-third-party-verification)),
  and the Markdown starts with the banner of [Markdown](#markdown). The `debate.close` is
  outboxed like all mail (7 days, D10), so B signs whenever it comes back. An abandoning B
  does not sign (it left the debate).
- **`peer_refused`** Decisions render the same banner with "the respondent refused to sign:
  its record differs". The signed file does not carry the local state, so a refused
  Decision exported as JSON is indistinguishable from an unconfirmed one, which is why both
  are treated alike.
- **Escalated (3.5)** is not a refusal. `escalated` Decisions are signed by both exactly as
  `agreed` ones.

A single-signed Decision proves only what the initiator's daemon recorded; views and the
Markdown always say how many signatures a Decision has.

## Signed file (third-party verification)

`agentnet decision <id> --json` prints, and `decision_show` returns:

```json
{"decision": <decision>, "hash": "<decision_hash>",
 "signatures": {"initiator": "<sig>", "respondent"?: "<sig>"}}
```

This file is what goes next to the Markdown in a repo (`d-….json`). **Verification**
(`agentnet decision verify FILE [--json]`, which runs **in the CLI without a daemon**, and
the vector checker):

1. Parse strictly; `decision` must pass the schema of this document and of every embedded
   debate message (the same validators as the daemon), and `canonical(decision)` must be at
   most `MaxDecision` bytes. Keys and signatures are strict base64url (the unused bits of the
   last character are zero), so each has one text form (review 47 L1, L2).
2. Recompute `canonical(decision)`, `msg` and `decision_hash`; it must equal `hash`.
3. Verify each present signature under `participants.initiator` / `.respondent` over `msg`.
4. Recompute `id` from `session` (rule 1).
5. Check the derivation invariants that do not need the transcript tables (review 43 L12):
   `outcome`/`reason` against `converge.answer` (rule 6), `final_agreement` present iff
   `agreed` and equal to `converge.proposal.agreement`, `remaining_disagreement` equal to
   rule 8 applied to the proposal and answer, `affected_artifacts` equal to the proposal's,
   `positions.*.final` equal to the last `revision` in `rounds`, `rounds[i].n = i + 1`,
   `participants.initiator ≠ participants.respondent`, and `opened` ≤ `closed`.
6. Report `{"valid": bool, "complete": bool, "signed_by": ["initiator", "respondent"],
   "hash", "id", "participants": {…, "fingerprint"}}`. **Exit 0 only when valid with both
   signatures** (`complete: true`). **Exit 6** when every check passes but only the initiator
   signed (`complete: false`: "unconfirmed: the respondent has not signed; its entries are the
   initiator's claim"). A file with only a respondent signature is invalid (the initiator
   always signs first). Exit 1 invalid, with the failing step. Scripts that test `$? -eq 0`
   therefore never accept an unconfirmed Decision (review 43 H1).

A valid file proves that the holders of those two keys signed that content. **Who** holds a
key is learned out of band: the Markdown and the verify output print each key's
fingerprint ([pairing.md](pairing.md) fingerprints), which a reader compares with the one
`agentnet peers` shows or that the person states.

### Vector

Keys: pairing vector seeds (A = `00…1f`, B = `20…3f`). Decision (one round, both passes,
agreed), canonical:

```
{"closed":"2026-10-01T09:20:00Z","converge":{"answer":{"accept":true},"proposal":{"agreement":{"decision":"Capped exponential backoff with full jitter"}}},"final_agreement":{"decision":"Capped exponential backoff with full jitter"},"id":"d-7eaeb0b78e6bd96981357b500af94044","opened":"2026-10-01T09:00:00Z","outcome":"agreed","participants":{"initiator":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","respondent":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc"},"positions":{"initiator":{"initial":{"argument":"Retries should back off exponentially, capped at 10 minutes.","assumptions":["Clock skew between peers is under 5 s"],"claim":"Use capped exponential backoff for outbox retries","evidence":[{"kind":"file","ref":"internal/mail/outbox.go"}],"rejected_alternatives":[{"option":"Fixed 30 s retry","reason":"Floods the relay after an outage"}]}},"respondent":{"initial":{"argument":"Jitter matters more than the curve.","claim":"Add full jitter to the existing backoff"}}},"problem":{"title":"Outbox retry policy","topic":"How should the outbox retry?"},"reason":"accepted","request":"r-0123456789abcdef0123456789abcdef","rounds":[{"initiator":{"challenges":[]},"n":1,"respondent":{"challenges":[]}}],"rounds_max":1,"session":"s-36375782ceb6baea9cee4d4273dfb035","team":"t-00112233445566778899aabbccddeeff","v":1}
```

```
id              d-7eaeb0b78e6bd96981357b500af94044   (from session s-36375782ceb6baea9cee4d4273dfb035)
decision_hash   6ec367cd5f0f82b1d929878e678ba1aafdd3c33cb13dc22da2fba55836094ede
sig initiator   oU23UZtZYRFm7RVkDdqPfoFfL6ZFlJj8K4Xqq4ySy1EtDdeO58GEo148fZbERBYbW-75C5erU6AhqoDwiDBnAw
sig respondent  xCdaoO0EujU6LbZJOgvIX_FvFlEikdlftxbUmUF9ef_1xTMrqScV_9UDGrsQu8aO_H7adF331lOzjs6PTJv-Cg
```

Negative checks (each fails at the stated verify step): the respondent's signature moved
to `initiator` (3); `outcome` changed to `escalated` (2, `hash` mismatch; with `hash` also
recomputed: 3); a member added (`"note": "x"`: 1); `id` of another session (4); a
`claim` with a newline (1); `final_agreement.decision` changed to another valid line, with
`hash` and both signatures recomputed with the vector keys (5). The vector with the `respondent` signature removed verifies with
**exit 6** (`complete: false`). `tools/specvectors` and `tools/verifyvectors` reproduce the
vector and the negatives (ticket 3.3a).

## Markdown

`agentnet decision <id> --md [--out FILE]` (the plan's "Markdown file suitable for a repo's
decisions folder"). The renderer is a pure function of the signed file plus the local
petnames of the two keys, in `internal/decision`, used by the CLI; `decision verify --md
FILE` renders a verified file without a daemon (names then are the fingerprints).

**Deterministic.** The same input gives the same bytes: UTF-8, LF line endings, one
trailing newline, no render time, no locale-dependent formatting, fixed section order,
entries in slot order. A golden-file test pins the output.

**Safe for a repository.** Every string that came from an agent or a human (topic, title,
names, claims, arguments, evidence refs, constraints, disagreement points, artifact
fields) is rendered **inert**:

- **Multi-line text** (`argument`, the topic): in a fenced code block whose fence is a run
  of backticks one longer than the longest run inside the text (at least three), with no
  info string. CommonMark and GFM render a fenced block literally: no HTML, no links, no
  emphasis, no headings. **Every fence starts at column 0 outside any container** (review 43
  M2): never inside a list item or block quote. Inside a list item, a content line indented
  less than the item's content column ends the item and with it the fence, and the rest of the
  peer text is then parsed as Markdown. So the per-side structure uses headings (`###`,
  `####`) and fences at top level; lists carry only single-line code spans.
- **Single-line text**: in an inline code span whose delimiter is a backtick run one longer
  than the longest run inside, padded with one space on each side. Code spans are literal
  too, including URLs (no autolink).
- **Never** inside a table (a `|` in a code span still splits a GFM table cell); lists and
  headings are used instead.
- Before either, the text goes through **`decision.Visible`** (review 43 M1), **not**
  `notify.Clean`: `Clean` turns newlines into spaces, collapses white space, truncates, and does
  not touch zero-width or tag characters. `Visible` keeps `\n` and `\t` (multi-line text only)
  and replaces every other rune that is not `unicode.IsGraphic` (space excepted) or is a format
  character (`unicode.Cf`: bidi controls, zero-width characters, U+FEFF, tag characters
  U+E0000–U+E007F), every graphic but invisible rune of review 46 H1 (variation selectors,
  the other default-ignorable code points such as the Hangul fillers, every `Zs` space but
  U+0020, U+2800; review 48 L3), and every C0/C1 control, by the visible ASCII text
  `\u{XXXX}` (hex of the code point), the rule of review 40's `DisplayQuote`. The rendered
  text then shows what the bytes say.
- Then **`decision.TemplateInert`** (review 48 H1) replaces every `{` followed by `{`, `%` or
  `#` by `\u{7B}`, so the file holds no `{{`, `{%` or `{#`. Static site generators run their
  template language over a Markdown file before the Markdown parser, inside code spans and
  fences too (Jekyll and GitHub Pages: Liquid, also on files without front matter; Eleventy:
  Liquid by default; Hugo shortcodes; Nunjucks): peer text such as
  `{% for i in (1..9) %}` + a backtick + `{% endfor %}` would expand to a backtick run longer
  than the fence, close it, and publish the rest as live Markdown and raw HTML.
- Neither escape adds a backtick, so the fence and span lengths are computed after both.
  The JSON file keeps the exact bytes; the Markdown is a view.
- Daemon-written text (headings, labels, enum values, ids, hashes, fingerprints, times) is
  plain Markdown; the Verification section's hash and signatures are code spans (base64url
  `_` would otherwise be read as emphasis, review 48 L4). Every paragraph and list ends with
  a blank line, so no label becomes a lazy continuation of the list before it (review 48 M2).
- No raw HTML, no images, no links are ever emitted. The file name is chosen by the user
  (`--out`); the default suggestion is `<id>.md`, never derived from peer text.

**Layout** (headings fixed):

A Decision with one signature (`awaiting_peer`, `peer_refused`, or a verified file with
`complete: false`) starts, before the title, with the fixed line **"> UNCONFIRMED: signed by
the initiator only. The respondent's entries and the outcome below are the initiator's claim
and are not proven."** (plus "The respondent refused to sign: its record differs." for
`peer_refused`), and the Outcome line reads "Outcome claimed by the initiator: …" (review 43
H1). Each human decision is labelled "approved on the initiator's (respondent's) machine; the
other side cannot check this".

```
# Decision d-… : <title as code span>
- Outcome: agreed | escalated (reason) ; Signed by: initiator and respondent | initiator only
- Participants: initiator <name code span> (fingerprint …), respondent … (fingerprint …)
- Session s-…, request r-…, team t-…, opened …, closed …, hash …
## Problem            (topic fenced; context files as a list: name, bytes, sha256)
## Initial positions  (per side: claim, assumptions, evidence, rejected alternatives, argument)
## Rounds             (per round, per side: challenges with targets and evidence; revisions)
## Final positions    (only sides that revised)
## Final agreement    (agreed only: decision, points, argument)
## Remaining disagreement
## Human decisions and constraints
## Affected artifacts
## Verification       (the hash, both signatures, and: "Verify with agentnet decision verify <id>.json")
```

Empty sections are omitted.

## Storage

Migration **20** (ticket 3.3a):

```sql
CREATE TABLE decisions (
    id        TEXT PRIMARY KEY,                 -- d-<32 hex>
    session   TEXT NOT NULL UNIQUE,
    role      TEXT NOT NULL CHECK (role IN ('initiator', 'respondent')),
    peer      TEXT NOT NULL,
    decision  TEXT NOT NULL CHECK (json_valid(decision)),   -- canonical (content)
    hash      TEXT NOT NULL,
    sig_initiator  TEXT,
    sig_respondent TEXT,
    peer_hash TEXT,                             -- the peer's differing hash on a refusal
    state     TEXT NOT NULL CHECK (state IN ('awaiting_peer', 'signed', 'peer_refused')),
    created   TEXT NOT NULL,
    updated   TEXT NOT NULL
);
```

Kept indefinitely. `decisions` goes into the DROP lists of both rewind tests.

## IPC and CLI

| Method | Params | Result / errors |
|---|---|---|
| `decision_list` | `{"state"?, "peer"?}` | `{"decisions": [{"id", "session", "peer", "outcome", "state", "created", "title"}]}` |
| `decision_show` | `{"id"}` (`d-`, `s-` or `r-`) | the [signed file](#signed-file-third-party-verification) plus `{"state", "peer_names"}`. `unknown_decision` |

| Command | Notes |
|---|---|
| `agentnet decisions [--json]` | `decision_list` |
| `agentnet decision <id> [--json [--out FILE]]` | the signed file, to stdout or FILE (refuses to overwrite without `--force`; review 48 M3: a PowerShell 5.1 redirect writes UTF-16, which verify refuses) |
| `agentnet decision <id> --md [--out FILE]` | Markdown to stdout or FILE (refuses to overwrite without `--force`) |
| `agentnet decision verify FILE [--md \| --json]` | offline, no daemon. Exit 0 valid with two signatures, 6 valid but unconfirmed (initiator only), 1 invalid |

## Audit

| Action | Side / actor | Detail |
|---|---|---|
| `decision.create` | both / `daemon` | `{id, session, peer, outcome, hash, bytes, signed_by}` |
| `decision.sign_in` | A / `daemon` | `{id, session, peer}` |
| `decision.refuse` | either / `daemon` | `{id, session, peer, reason}` |

Exporting a Decision (`agentnet decision <id> --md|--json`, `--out`) is **not audited** (D33): it is a local read of a record the daemon already holds, like `agentnet log`. Revisit in the next large security review.

The hash is not content: it cannot be inverted, and it lets an audit reader tie a log row
to a committed Decision file.

## Security considerations

- **What a signature covers.** Everything in the Decision, including both parties' keys,
  the session and the request, so a signature cannot be moved to another debate or party.
  The domain string separates it from mail, card and grant signatures.
- **No fabricated agreement.** `agreed` requires the respondent's own `accept: true` entry
  in the respondent's own transcript; A cannot produce a Decision B signs that says
  otherwise. This holds for a **two-signature** Decision only: a single-signed one is the
  initiator's claim, which is why verify exits 6 and the Markdown carries the UNCONFIRMED
  banner.
- **Truncation by A.** A decides `entries`. It can drop B's *late* entry (after a timeout),
  never an entry it already applied (B has seen A's later entries that depend on it, and A's
  close cannot be earlier than those). The outcome is then `escalated`/`timeout`, never
  `agreed`. A modified A could claim a timeout to drop B's `accept: true`; the result is an
  escalated record, B's view shows the dropped entry as `late`, and both humans are
  notified, so it cannot pass as agreement.
- **Repository safety.** The Markdown is inert (code spans and fences only for untrusted
  text); the JSON is data. Neither is executed by AgentNet.
- **Identity binding** is by fingerprint, out of band, as for everything signed in
  AgentNet.
