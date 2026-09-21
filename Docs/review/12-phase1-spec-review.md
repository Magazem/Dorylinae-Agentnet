# 12: Phase 1 spec review (adversarial)

Reviewer: P1-SpecReviewer (Opus), 2026-09-21. Target: branch `p1/specs` at `5b70df3`,
worktree `AgentNet-wt/p1-specs`. Scope: `Docs/protocol/{team,presence,request,notify}.md`
(new), the Phase 1 edits to `ipc.md`, `envelope.md`, `mail.md` and `README.md`, the CLI pages
`team`, `presence`, `request`, `inbox`, `notify`, `status` and `peers`, and the ticket plan
`11-phase1-tickets.md`. Checked against plan Phase 1, HANDOFF §3 (D1–D10), `pairing.md`,
`mail.md`, reviews 05–10, and the code where a claim depends on it (`internal/store`,
`internal/relay`, `internal/relayclient`, `internal/mail`).

## Verdict

**Ready with changes.** The changes are already applied in this worktree. There are no Critical
or High findings. There are 15 Medium findings, all fixed in the docs, and 19 Lows (3 fixed,
16 listed for the backlog). **No OD recommendation changed.** OD-P1-13 (own notifier, not
`beeep`) is confirmed and extended to Linux (M14).

| Severity | Found | Fixed in docs |
|---|---|---|
| Critical | 0 | — |
| High | 0 | — |
| Medium | 15 | 15 |
| Low | 19 | 3 |

## What holds (checked, no finding)

- **Forged introductions.** Rosters are signed mail from `teams.owner`, and the owner must be
  `code` or `fingerprint` (rule 3). A member, an introduced (`team`) peer, or the relay cannot
  author one. `team` trust cannot chain, because an introduced owner's rosters are ignored.
- **Epoch rollback and replay.** A replayed roster is stopped by `mail_seen`, the 14 d limit,
  and `epoch ≤ stored`. A replayed `team.join` is stopped by dedupe, and by `used` on the
  invite and `peer_key = msg.from`.
- **Escalation of `team` trust.** `team` differs from `code` only in D5, which accepts both.
  A member card can't be forged (it is self-signed, and `card.public_key = key`). A directly
  paired peer's card and trust are never touched by a roster.
- **Migration 8 data loss.** Through migration 7 nothing references `peers`: no FK, trigger or
  view (`internal/store/store.go`). The column lists are explicit, and the migration runs in
  one transaction. A guard comment and stronger acceptance were added (Low, fixed).
- **Presence padding.** The fields are fixed-width or padded. Adding `k` `0` characters adds
  exactly `k` bytes, and the HPKE overhead is constant, so the flags don't change the size.
- **Relabelling.** A `presence` relabelled as `mail` fails mail step 9 (`p-` id), and the
  reverse fails the kind check. Unknown senders are dropped at mail step 1, before HPKE.
- **Lifecycle forgery and reordering.** The mirror looks up `(peer = msg.from, id)`, so only
  the recipient can move a request's state. `seq` makes stale and reordered updates no-ops.
- **Priority formula.** It is integer-only, with `a ≤ n`, so the numerator is at most
  `2000·(n+2)`. It can't overflow, and the vectors in request.md are correct.
- **`ErrBadBody` as an oracle.** The only party that learns the result is the peer that wrote
  the body. `team.join` answers identically whether an invite exists or not.
- **Inbox order.** `priority`, `received_at`, `peer`, `id` is a total order.
- **Webhook signature.** It covers the timestamp, id and exact body, is compared in constant
  time, re-signs per attempt, and never follows redirects.

## Medium (all fixed)

| # | Area | Finding | Fix (file) |
|---|---|---|---|
| M1 | Teams | Rule 4 ignored any roster for a team in local state `left`, so a member that left could **never rejoin**, even with a new invite. Meanwhile a `removed` member, or a `dissolved` team, was reactivated by any higher-epoch roster **without the member's consent**, and presence would start flowing again | Re-entry from `left`/`removed`/`dissolved` now needs a live pending join, like an unknown team (team.md §`team.roster` rule 4) |
| M2 | Teams | `team_pending_joins` had `owner_key` as its primary key, so two invites from one owner (to two of its teams) overwrote each other, and the second join failed `not_invited` | PK `(owner_key, lookup)`; a roster consumes the oldest live row (team.md rule 4, §Tables) |
| M3 | Teams | `peers remove` of a **team owner** left its teams `active` and its introduced peers trusted **forever**: rosters from a removed key can't arrive, so GC never runs | `peers remove` of an owner sets its teams `left`, sends goodbyes, and runs GC (team.md §Operations, cli/peers.md) |
| M4 | Teams | A removed member that was offline for over 14 days never learned it was removed, because resync only served active members. The removal roster also gave the removed member the remaining members' fresh mailbox announcements | A roster to a non-member lists only the owner. Resync serves any peer reporting a lower epoch for a team the owner owns (team.md, presence.md step 7) |
| M5 | Presence | The `interval` wire rule (10–300) rejected the 1 s interval the 1.2c scaled e2e test relies on. A Sonnet worker would hit this at once | 1–300 on the wire; the daemon uses ≥ 30 outside tests (presence.md §Body) |
| M6 | Presence/relay | Presence was forwarded whenever the recipient's 64-frame buffer had room. Many senders could keep the buffer full, pushing the recipient's **mail** into the queue path (`directBusy`, `internal/relay/relay.go`) | Presence is forwarded only when the buffer is at most half full (presence.md §Relay, envelope.md) |
| M7 | Presence | With a fixed 30 s tick, a daemon in several teams with more than about 110 visible peers exceeds 240/min, is silently rate-limited, and flaps offline for peers | `interval = max(30, ⌈visible/3⌉)` and a relay limit of 600/min (`EphemeralPerMinute`); the 90 s bound holds up to 90 visible peers (presence.md §Sending, §Relay) |
| M8 | Presence | `relayclient` puts every non-`mail` envelope into the 8192-entry seen-set, so heartbeats would evict the `session.*` ids it protects | Presence bypasses the seen-set, like mail, and is not acked (presence.md §Relay) |
| M9 | Requests | The auto-decline reply was submitted **after** commit. A crash between the two left a `declined` row with no `last_reply` and no reply ever sent, and a duplicate would then re-send nothing | The decline, `last_reply` and `SubmitTx` go in the Apply transaction (request.md §Receiving step 3) |
| M10 | Requests (D10) | `request resend` re-wraps the old object in a fresh mail, which passes the 14 d limit. It could be repeated every 7 d forever, so a months-old request surfaced as new, defeating D10's receive bound | Resend only while `created + 21 d > now`. The receiver treats a **new** request with `created` over 30 d old as `bad_body`; duplicates are still recognised (request.md) |
| M11 | Webhook | The reference verifier *rejected* a repeated `id`, but retries deliberately reuse the id. After a lost response, the retry got a 4xx and the row ended `failed` although it was delivered | A seen id is not processed but is answered `2xx` (notify.md §Signature) |
| M12 | Webhook | Peer-controlled text (name, team name, title) went into Slack `text` and Discord `content` unescaped: `<!channel>`, `@everyone`, and disguised links `<url\|label>` | Slack `&<>` escaping; Discord `allowed_mentions: {parse: []}` (notify.md §Payload) |
| M13 | Webhook SSRF | The URL is settable by any local IPC client, including an agent steered by a hostile request. The loopback rule was name-based, and nothing stopped `https` to link-local or metadata addresses, or to names resolving there | The check is made at dial time on the address actually connected (`net.Dialer.Control`): no link-local, unspecified or multicast; `http` loopback only; the literal-host rule; `blocked_address` (notify.md §Configuration) |
| M14 | Desktop (OD-P1-13) | `gdbus call` parses argv as **GVariant text**, so a title like `'x'` or `@s "y"` is re-interpreted. freedesktop notification bodies are markup (`<a>`, `<img>`) | Pass GVariant string literals built by the daemon; escape `&<>` in the Linux body (notify.md §Desktop) |
| M15 | Ticket plan | Missing review markers: 1.1a (trust rank, a peer GC that deletes rows and fails outbox, a table rebuild), 1.2a (new relay abuse surface), 1.4c (D5 and auto-decline policy), 1.6a (state driven by peer mail) | Marked **yes**. These are reviewed one at a time: they can't be batched with their dependants, which need them merged (11-phase1-tickets.md) |

The acceptance tests for every fix were added to the matching tickets in
`11-phase1-tickets.md` (1.1a, 1.1b, 1.2a, 1.2b, 1.2c, 1.4c, 1.6a, 1.8a, 1.8b).

## Low

Fixed in the docs:

- **L1** A concurrent `request_submit` with the same idempotency key hit the unique index with
  undefined behaviour. The loser now answers as a duplicate (request.md step 7).
- **L2** The keychain account `webhook-<id>` was undefined. It is now `webhook`, and rotation
  semantics are stated (notify.md §Secret).
- **L3** Migration 8: a guard comment (re-check `sqlite_master` if a reference is ever added
  first), and acceptance now checks the row count, that no `peers_new` is left, and
  `integrity_check`.

Open (backlog; none blocks Phase 1):

- **L4** A hostile relay can delay (not forge) heartbeats inside the ±10 min freshness window,
  and so stretch "online" by up to about 11 min after a kill or a withheld goodbye. A later
  fix is a per-peer clock-offset estimate with a tighter window.
- **L5** Cross-boot rule `created ≥ row.created`: if the sender's clock steps back, its new boot
  is refused for up to 20 min.
- **L6** Refreshing an introduced peer from *any* owner's roster lets an owner replay an older
  (still self-signed) card of a shared member. `introduced_by` semantics are unstated when
  two owners introduce the same key. Suggest: keep the first introducer, and refresh the card
  only from that owner.
- **L7** Team id squatting: an owner that knows team `x`'s id (it is a member of `x`) can
  create its own team with that id and invite a victim. The victim can then never join the
  real `x` (`not_owner`). It needs the victim to join the attacker's team first.
- **L8** Sybils: an owner can introduce any number of keys, each with a fresh urgency budget and
  full priority weight. The budget is per key (OD-P1-4). Accepted by OD-P1-2, but worth a
  note in the beta docs.
- **L9** Name collisions: an introduced peer may share a name with a directly paired one. The
  result is `ambiguous_peer`, not misrouting, but the CLI should show the trust and
  fingerprint when a `team` peer is chosen by name.
- **L10** The `epochs` map is padded only to 256-byte blocks, so the size can reveal how many
  of the recipient's teams the sender is in (above about 4 teams).
- **L11** Invisible mode doesn't hide mail: a sender's outbox reaching `delivered` shows that the
  daemon is up. Document it in cli/presence.md.
- **L12** Slack and Discord webhook URLs are bearer secrets, stored in plain text in `settings`
  and returned by `notify_get`. Consider the keystore, or redaction in `--json`.
- **L13** If both sides' roster `mailbox` is `null`, neither can seal to the other until a
  rotation push (≤ 7 d).
- **L14** A v1 re-pair of an introduced peer does not clear `introduced_by`, so GC could later
  remove a peer the user paired on purpose. v1 needs `--allow-pairing-v1`.
- **L15** There is no per-(sender, recipient) ephemeral limit. Flooding needs a paired peer, and
  the receiver's work is bounded by 600/min per sender.
- **L16** The implementer must pass state from `Apply` to `After` for a duplicate request (to
  re-send `last_reply`), and nothing specifies how (for example a field on `mail.Opened`). A
  Sonnet worker will need to choose.
- **L17** `deadline` has no upper bound.
- **L18** Two invites outstanding for a team of 31 members: the second joiner pairs with the
  owner but gets `team_full`, and the CLI only says "asked to join". `team list` never shows
  it. Consider reporting `join_ignored` back to the joiner.
- **L19** A peer that advertises `interval = 300` stays "online" for 750 s after it dies. This
  affects only how that peer itself is seen.

## Implementability

After these fixes, each ticket can be built from the docs alone, with two exceptions: L16 (the
`Apply` → `After` hand-off) and L6 (`introduced_by` with two introducers). Both are local
choices a worker can make and record. The migration order (8 → 12) matches the dependency
graph. The ∥ columns do not pair two tickets that add migrations, and the critical path is
unchanged.

## Files changed by this review

- `Docs/protocol/team.md`
- `Docs/protocol/presence.md`
- `Docs/protocol/request.md`
- `Docs/protocol/notify.md`
- `Docs/protocol/envelope.md`
- `Docs/protocol/ipc.md`
- `Docs/cli/peers.md`
- `Docs/cli/request.md`
- `Docs/review/11-phase1-tickets.md`
- `Docs/review/12-phase1-spec-review.md` (new)
