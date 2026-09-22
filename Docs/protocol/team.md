# Teams

Status: **implemented**, Phase 1 ticket 1.1 (split into 1.1a–1.1d in
[../review/11-phase1-tickets.md](../review/11-phase1-tickets.md)), in `internal/team`. Change
this document first for any further change.

A **team** is a named set of paired peers with one **owner**. Presence
([presence.md](presence.md)) and requests ([request.md](request.md)) are scoped to a team.
There is no group cryptography and nothing about teams is stored on the relay: every team
message is a sealed mail ([mail.md](mail.md)) sent pairwise, and the envelope `team` field
stays `""`.

Conventions are those of [mail.md](mail.md): `<key>` is an identity key (base64url, 43
characters); times are RFC 3339 UTC with `Z` and whole seconds on the wire, and with
milliseconds in SQLite; canonical JSON and strict parsing as in
[agent-card.md](agent-card.md#canonical-serialisation).

## Model

| Concept | Definition |
|---|---|
| Team id | `t-` + 32 lowercase hex characters (16 bytes from `crypto/rand`), chosen by the creator. Globally unique, never changes |
| Name | Display label chosen by the owner, `^[a-z0-9][a-z0-9-]{0,31}$`. May change (rename). Not unique across daemons |
| Owner | The creator's identity key. The only key that may change the roster. Fixed for the team's life (no transfer in Phase 1, see OD-P1-10 in [../review/11-phase1-tickets.md](../review/11-phase1-tickets.md#owner-decisions-needed)) |
| Member | An identity key in the roster. The owner is always a member. At most **32** members |
| Epoch | Integer, starts at 1, incremented by the owner on every roster change. Receivers apply only a higher epoch |
| Roster | The owner-authored list `{id, name, owner, epoch, state, members[]}` sent as mail kind `team.roster` |

**Single writer.** Only the owner changes membership (add by invite, remove, rename,
delete). A member can only leave. Because there is one writer, and epochs only grow, there
are no merge conflicts.

**Members need not have paired with each other.** Each joiner pairs only with the owner
(through the invite code, which *is* a pairing v2 code). The owner then **introduces** the
members to each other: the roster carries each member's self-signed Agent Card and signed
mailbox announcement, and every member stores the others as peers with `trust = team`
([Introduced peers](#introduced-peers)). This is what makes "install, pair once, done" work
for a team of five (4 codes instead of 10 pairwise pairings).

## Trust level `team`

`peers.trust` gains the value `team` (migration 8, [Tables](#tables)):

| `trust` | Meaning | Rank |
|---|---|---|
| `relay` | v1 pairing | 0 |
| `team` | Key introduced by a team owner whose own trust is `code` or `fingerprint` | 1 |
| `code` | v2 pairing | 2 |
| `fingerprint` | Human-verified | 3 |

`Store.Add` still never lowers trust. A later direct pairing (v2) or `peers verify` of an
introduced peer raises it to `code` / `fingerprint` and clears `introduced_by`.

Policy D5 ([pairing.md](pairing.md#storage-and-trust-states)): on a non-loopback relay,
requests are refused to and from `trust = relay` only. `team` is accepted: the key was
vouched for, over an authenticated mail, by an owner the user paired with by code.

The owner is trusted to introduce honest keys. A malicious or compromised owner can
introduce any key into its own team; it can already impersonate itself, and it cannot use an
introduction to get into another team. This is accepted (OD-P1-2).

## Kinds

All three are ordinary application mail: outboxed, acked, deduped, subject to the
14-day receive limit. Bodies are strict: exactly the members listed, no others. A body
that fails validation is handled as `bad_body` ([request.md §Invalid bodies](request.md#invalid-bodies)):
recorded in `mail_seen`, audited `mail.reject {reason: "bad_body"}`, acked as `unsupported`.

### `team.roster` (owner → each member)

```json
{
  "team": {
    "epoch": 3,
    "id": "t-0123456789abcdef0123456789abcdef",
    "members": [
      {
        "added": "2026-10-01T09:00:00Z",
        "card": {"card": {...}, "signature": "..."},
        "key": "<key>",
        "mailbox": {"announcement": {...}, "signature": "..."}
      }
    ],
    "name": "backend",
    "owner": "<key>",
    "state": "active"
  }
}
```

| Member | Rules |
|---|---|
| `team.id` | `t-` + 32 lowercase hex |
| `team.name` | `^[a-z0-9][a-z0-9-]{0,31}$` |
| `team.owner` | `<key>`, must equal `msg.from` |
| `team.epoch` | integer, 1 ≤ epoch < 2^53 |
| `team.state` | `active` or `dissolved` |
| `team.members` | `active`: 1–32 entries, unique `key`, the owner included. `dissolved`: 0–32 entries |
| `members[].key` | `<key>` |
| `members[].added` | when the owner added the member |
| `members[].card` | the member's signed Agent Card object ([agent-card.md](agent-card.md)); must verify, and `card.public_key` = `key` |
| `members[].mailbox` | the member's newest signed mailbox announcement known to the owner, or `null`. Verified as in [mail.md §Announcement](mail.md#announcement) with `identity` = `key`. A **time** failure (check 5) makes it `null` for that member (the owner's copy may be old); any other failure is `bad_body` |

The owner sends the roster, as one outboxed mail per recipient, to every member except
itself. On a removal it also sends a roster to the removed member, which learns it is out.
**A roster sent to a key that is not a member lists only the owner** (same `id`, `name`,
`epoch` and `state`), so a removed member does not learn the remaining members' new
mailbox keys or later changes. The owner puts its own entry in `members` like any other.

Every member learns every other member's card, fingerprint and mailbox announcement, and the
team's name and epoch. That is inherent to introductions. Members do not learn which other
teams a member belongs to.

**Validation (receiver, inside `Apply`)**, in order; failure is `bad_body` unless noted:

1. Strict shape and field rules above.
2. `team.owner = msg.from`.
3. The owner's `peers.trust` is `code` or `fingerprint`. Otherwise the roster is **ignored**
   (not `bad_body`): acked normally, nothing applied, audit `team.roster_ignored {team, peer,
   reason: "owner_trust"}`.
4. Look up `teams.id`:
   - **Unknown team.** Apply only if `team_pending_joins` has a live row with
     `owner_key = msg.from`, `state = active`, and the own key is in `members`. Consume one
     such row (the oldest `created`) by deleting it. Otherwise ignore:
     `team.roster_ignored {reason: "not_invited"}`.
   - **Known team.** `teams.owner` must equal `msg.from` (else ignore,
     `reason: "not_owner"`). If `team.epoch ≤ teams.epoch`, ignore silently (idempotent
     resend or reordering).
   - **Known team, local state `left`, `removed` or `dissolved`** (and a higher epoch).
     Re-entry needs the member's consent. Apply only under the same condition as an
     unknown team: a live pending join from `msg.from`, `state = active`, and self in
     `members`. Consume the row. Otherwise ignore (`reason: "left"`, `"removed"` or
     `"dissolved"`). The owner therefore cannot put a member back into a team, or revive a
     dissolved one, without a new invite. A member that left can rejoin with a new invite.

**Apply** (same transaction):

1. Upsert `teams` (`name`, `epoch`, `updated`; `state` below).
2. Replace the team's `team_members` rows with `members`.
3. For each member other than self: upsert it as an [introduced peer](#introduced-peers).
4. Own membership state: `active` if self ∈ `members` and `team.state = active`;
   `removed` if self ∉ `members`; `dissolved` if `team.state = dissolved`.
5. [Garbage-collect](#introduced-peers) introduced peers that no longer share an active team.

**After commit:** audit `team.roster_apply {team, epoch, added: [keys], removed: [keys], state}`;
send the current `keys` announcement ([mail.md §Kind keys](mail.md#kind-keys), outboxed) to
every **newly inserted** peer that has a mailbox key, so both sides have fresh keys; send an
immediate presence heartbeat to new members ([presence.md](presence.md#sending)).

### `team.join` (joiner → owner)

```json
{"lookup": "7KQ2M"}
```

`lookup` is the first 5 characters of the invite code in the pairing v2 alphabet
([pairing.md §Code format](pairing.md#code-format)), normalised to upper case. The relay has
seen it already; it is not secret.

Owner, in `Apply`: find `team_invites` with `lookup = body.lookup`, `peer_key = msg.from`,
`used IS NULL`, `expires > now`. If none: acked, nothing applied, audit
`team.join_ignored {peer, reason: "no_invite"}`. If the team is not `active` or already has
32 members: mark the invite used, audit `team.join_ignored {reason: "team_full"|"inactive"}`.
Otherwise: mark the invite used, add the member (`added = now`), `epoch += 1`, and after
commit broadcast the roster to all members (the joiner included). Audit
`team.member_add {team, peer, epoch}`.

### `team.leave` (member → owner)

```json
{"team": "t-0123456789abcdef0123456789abcdef"}
```

Owner, in `Apply`: if the team is known, `active`, owned by self, and `msg.from` is a member
other than the owner: remove it, `epoch += 1`, broadcast the roster to the **remaining**
members. Audit `team.member_leave {team, peer, epoch}`. Anything else: acked, ignored,
audit `team.leave_ignored {team, peer}`.

## Operations

| Operation | Who | Effect | IPC |
|---|---|---|---|
| Create | anyone | New id, `epoch = 1`, `state = active`, `members = [self]`, owner = self. Local name must not equal the name of another `active` team on this daemon (`team_exists`). Nothing is sent | `team_create` |
| Invite | owner | Starts a pairing v2 issuer ([pairing.md](pairing.md#issuer-agentnet-pair---new)) tagged with the team. On `pair.complete` the daemon writes `team_invites` (`lookup`, `team_id`, `pairing_id`, `peer_key` = the paired key, `expires = now + 24 h`). An invite code expires like any pairing code (10 min) | `team_invite` |
| Join | invitee | Redeems the code as pairing v2 ([pairing.md](pairing.md#redeemer-agentnet-pair-code)). On `complete`: write `team_pending_joins (owner_key = peer, lookup, expires = now + 24 h)`, then submit `team.join {lookup}` to the owner. The roster that follows is accepted by rule 4 above | `team_join` |
| Remove member | owner | `epoch += 1`, member dropped, roster sent to remaining members **and** the removed one | `team_remove` |
| Rename | owner | `epoch += 1`, new name, roster sent to all | `team_rename` |
| Leave | member (not owner) | Local state `left` at once: stop presence to members who share no other active team (send `offline` first, [presence.md](presence.md#visibility)), GC introduced peers, then submit `team.leave` | `team_leave` |
| Delete | owner | `state = dissolved`, `epoch += 1`, roster (with the final member list) sent to all members; locally `dissolved`, GC | `team_delete` |

Plain `agentnet pair <invite code>` also works (it is a pairing code): it pairs but does not
join, because no `team_pending_joins` row is written and no `team.join` is sent.

An owner whose team loses a member's presence for over 7 days may have sent that member a
roster that expired. **Resync:** members report the epoch they hold for each team *owned by
the recipient* in their presence heartbeat (`epochs`, [presence.md](presence.md#body)). When
the owner sees a lower epoch than its own for a team it owns (in any state), it resubmits
the current roster to that peer, at most once per 10 minutes per peer and team. This also
covers a **former** member that missed its removal roster (it was offline for over 14 days):
it gets the owner-only roster and learns it is out. An id the owner does not own is ignored.

**Removing the owner as a peer.** `peers remove` of a key that owns local teams sets every
such team with local state `active` to `left` (audit `team.leave {team, reason:
"owner_removed"}`, actor `cli`), sends presence goodbyes as for Leave, and runs GC. Rosters
from a removed key can no longer arrive (mail step 1), so otherwise its introductions would
stay trusted forever. No `team.leave` is sent (there is no longer a mailbox key to send it to).

## Local names

Two active teams on one daemon may carry the same name (different owners). A **team
reference** in IPC and CLI is either a full team id or a name; a name matching more than one
active team is `ambiguous_team`, and the user passes the id. `team_create` refuses a name
already used by an active local team (`team_exists`); a roster can still introduce a
duplicate.

## Introduced peers

Upsert of member `m` (not self) from a roster applied under owner `o`:

- **Unknown key:** insert into `peers`: `name = card.name`, `harness`, `skills`, `card` (the
  signed card object), `paired_at = now`, `trust = team`, `mailbox_keys = [mailbox]` or `[]`
  when `mailbox` is `null`, `introduced_by = o`.
- **Known key, `introduced_by` not NULL:** refresh `name`, `harness`, `skills`, `card`; merge
  `mailbox` by [mail.md §Peer storage](mail.md#peer-storage). Trust unchanged.
- **Known key, directly paired (`introduced_by` NULL):** merge `mailbox` only. Card, name and
  trust unchanged.

**Garbage collection.** A peer with `introduced_by` not NULL that is no longer a member of
any team whose own state is `active` is removed exactly like `peers remove`
([../cli/peers.md](../cli/peers.md#peers-remove)): the row is deleted, its non-final outbox
rows become `failed` (`unpaired`), audit `peer.remove {peer, name, fingerprint, reason:
"team"}` (actor `daemon`). A directly paired peer is never removed by a team change.

`agentnet peers` lists introduced peers with `trust: "team"` and `introduced_by` (the
owner's key); see [../cli/peers.md](../cli/peers.md).

## Scoping rules

- **Presence** is sent only to peers that share at least one `active` team with self, subject
  to the visibility mode ([presence.md](presence.md#visibility)). Presence received from any
  paired peer is recorded, but `status --team x` shows only members of `x`.
- **Requests** carry a team id. The sender and recipient must both be members of that team
  in the sender's roster copy at submit, and in the receiver's at receipt; otherwise the
  receiver auto-declines with code `not_team_member` ([request.md](request.md#receiving)).
- `team_members` of a team in state `left`, `removed` or `dissolved` are kept for display
  (`team list --all`) but never used for scoping.
- Acceptance test (1.1): A, B, C; A owns `x` with B; C owns `y` with B. `status --team x` on B
  lists A and B only; `status --team y` on B lists B and C only; A and C are not peers of
  each other and neither sees the other anywhere.

## Tables

```sql
-- migration 8 (1.1a): peers_trust_team. Rebuild: SQLite cannot change a CHECK.
CREATE TABLE peers_new (
    public_key    TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    harness       TEXT NOT NULL,
    skills        TEXT NOT NULL CHECK (json_valid(skills)),
    card          TEXT NOT NULL CHECK (json_valid(card)),
    paired_at     TEXT NOT NULL,
    trust         TEXT NOT NULL DEFAULT 'relay'
                  CHECK (trust IN ('relay', 'team', 'code', 'fingerprint')),
    mailbox_keys  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(mailbox_keys)),
    introduced_by TEXT                              -- owner key; NULL = directly paired
);
INSERT INTO peers_new (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys)
    SELECT public_key, name, harness, skills, card, paired_at, trust, mailbox_keys FROM peers;
DROP TABLE peers;
ALTER TABLE peers_new RENAME TO peers;
-- Safe as one migration transaction: through migration 7 no table, index, trigger or view
-- refers to peers, so DROP and RENAME cannot cascade or fail. Migration 8 must re-check
-- this (sqlite_master) if a later branch adds such a reference first. The column list is
-- explicit on both sides; SELECT * would copy by position.

-- migration 9 (1.1b): teams
CREATE TABLE teams (
    id      TEXT PRIMARY KEY,                       -- t-<32 hex>
    name    TEXT NOT NULL,
    owner   TEXT NOT NULL,
    epoch   INTEGER NOT NULL,
    state   TEXT NOT NULL CHECK (state IN ('active', 'left', 'removed', 'dissolved')),
    created TEXT NOT NULL,
    updated TEXT NOT NULL
);
CREATE TABLE team_members (
    team_id TEXT NOT NULL,
    key     TEXT NOT NULL,
    added   TEXT NOT NULL,
    PRIMARY KEY (team_id, key)
) WITHOUT ROWID;
CREATE INDEX team_members_key ON team_members (key);
CREATE TABLE team_invites (                         -- owner side
    lookup     TEXT PRIMARY KEY,
    team_id    TEXT NOT NULL,
    pairing_id TEXT NOT NULL,
    peer_key   TEXT NOT NULL,                       -- written on pair.complete
    created    TEXT NOT NULL,
    expires    TEXT NOT NULL,
    used       TEXT
);
CREATE TABLE team_pending_joins (                   -- joiner side
    owner_key TEXT NOT NULL,
    lookup    TEXT NOT NULL,
    created   TEXT NOT NULL,
    expires   TEXT NOT NULL,
    PRIMARY KEY (owner_key, lookup)                 -- two invites from one owner can be pending
) WITHOUT ROWID;
```

`team_invites` rows are written only on `pair.complete` (the lookup-to-team mapping for a
still-pending invite is held in memory by the pairing manager). Rows with `expires < now`
are pruned daily and at start, in both invite tables. A lookup is reused by the relay only
after its pairing ends; if a new invite completes with a lookup still in `team_invites`, the
old row is replaced.

## Audit

Actor `cli` for user commands, `daemon` for mail-driven changes. Details never contain
cards, announcements or codes.

| Action | Detail |
|---|---|
| `team.create` | `{team, name}` |
| `team.invite` | `{team, pairing_id}` |
| `team.join` | `{pairing_id, owner}` (joiner, on sending `team.join`) |
| `team.member_add` | `{team, peer, epoch}` |
| `team.member_remove` | `{team, peer, epoch}` |
| `team.member_leave` | `{team, peer, epoch}` (owner, on `team.leave`) |
| `team.leave` | `{team, reason?}` (member; `reason: "owner_removed"` when caused by `peers remove` of the owner) |
| `team.rename` | `{team, name, epoch}` |
| `team.delete` | `{team, epoch}` |
| `team.roster_apply` | `{team, epoch, added, removed, state}` |
| `team.roster_ignored`, `team.join_ignored`, `team.leave_ignored` | `{team?, peer, reason}` |

## IPC and CLI

IPC methods `team_create`, `team_invite`, `team_join`, `team_list`, `team_show`,
`team_remove`, `team_rename`, `team_leave`, `team_delete`: [ipc.md](ipc.md#teams).
CLI: [../cli/team.md](../cli/team.md).
