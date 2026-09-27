# Beta invitations, billing teams, seats and waves

Status: **approved by the owner 2026-09-27 (D36 in HANDOFF)** (Phase 4 spec, tickets 4.3a–4.3b
in [../review/49-phase4-tickets.md](../review/49-phase4-tickets.md); adversarially reviewed in
[50-phase4-spec-review.md](../review/50-phase4-spec-review.md)). OD-P4-n choices for this
document are recorded in the ticket plan, not open, except OD-P4-20 (outside review timing),
decided during wave 1.

Plan 4.3: "Invite codes per team, 10 teams in the first wave, a waitlist form for the rest.
Acceptance: invite flow works on macOS, Linux, Windows."

Two different codes exist after Phase 4. Keep them apart in every doc and message:

| Code | Made by | Carries | Purpose |
|---|---|---|---|
| **Beta invite** (new) | The operator (`relay admin invite create`) | Nothing secret about peers | Lets one person create a **billing team** on the hosted relay |
| **Team invite** (Phase 1, [team.md](team.md)) | A daemon team owner (`agentnet team invite`) | A pairing v2 code | Pairs a person into a daemon team; on the hosted relay it also admits them to the owner's billing team (below) |

A new team's first person uses both (setup with the beta invite, then invite others with team
invites); every later member uses only the team invite. That keeps plan 4.9's "one command
connects to a peer or team".

## Beta invite codes

- Format: `BETA-` + 12 characters of Crockford base32 (60 bits), shown `BETA-XXXX-XXXX-XXXX`,
  normalised like pairing codes (case, `-`, aliases). Stored as SHA-256 of the normalised code.
- Created by the operator on the relay host:
  `relay admin invite create [--wave N] [--seats 8] [--expires 48h] [--note TEXT]` prints the
  code once. `relay admin invite list` shows ref, wave, seats, state, created, redeemed-at
  (never the code). `relay admin invite revoke <ref>`.
- Single use. Expired or revoked codes answer `invite_invalid` (the message never says which).
- Redeemed by a **bound account without a billing team**: control frame
  `{"op":"invite_redeem","code":"BETA-…"}` → `{"op":"invite_done","team":"bt_…","seats":8}`, or
  `error` `invite_invalid` / `already_in_team` / `rate_limited` (5 failures per account per
  hour, 10 per prefix per hour).
- CLI: `agentnet setup --invite CODE` (4.9) or `agentnet login --invite CODE`. The redeeming
  account becomes the billing team's **contact** (the person the operator writes to). The
  contact has no other power over the daemon team.

## Billing teams

A billing team is `bt_…` with `seats` (default 8, OD-P4-7), `wave`, `state`
(`active`/`suspended`/`closed`), `created`, and its members (accounts). It is the quota unit
([relay-hosted.md §4](relay-hosted.md#4-quotas-ticket-41c)) and the telemetry unit
([telemetry.md](telemetry.md)). It has no name visible to anyone but the operator (the operator
may add a private `--note`).

### Seats: admission by pairing

**OD-P4-6** chooses how members after the first join a billing team. Recommended:

**(a) Admission by pairing, vouched by the issuer.** Admission happens only when the
**issuer's daemon** has checked the redeemer's `tag_R` ([pairing.md](pairing.md)), i.e. when
the redeemer has proved it knows the whole code, not just the lookup (review 50 H2):

1. The redeemer's `pair_redeem` succeeds at the relay (lookup matched). The relay remembers
   `(issuer key, redeemer key)` for the pairing TTL (10 min), in memory.
2. The issuer's daemon verifies `tag_R`. On success, and before it sends its own
   `pair.confirm{tag_I}`, it sends the control frame
   `{"op":"pair_admit","public_key":"<redeemer key>"}`. It sends it for every successful v2
   pairing it issued (team invite or plain `pair`), only if the relay's `ready` lists
   `accounts`.
3. The relay accepts `pair_admit` only from a key for which step 1 recorded that redeemer
   within the TTL, once per record. If the **redeemer's** account has no billing team and the
   **issuer's** account has one with a free seat, it adds the redeemer's account to the
   issuer's billing team (`via = pairing`) and sends the redeemer
   `{"op":"admitted","team":"bt_…"}` and the new state in its next `ready`. With no free seat
   the redeemer gets `error` `team_full` (ref = its pairing ref), and `team join` reports "the
   inviting team has no free seats on this relay". Anything else is ignored silently.

Because the issuer sends `pair_admit` before `tag_I` on the same connection, the relay has
admitted the redeemer before the redeemer completes the pairing and sends `team.join`.

Why not admit at `pair_redeem`: the relay's check there is only the 5-character lookup
(25 bits, and an entry accepts up to 3 redemptions, `internal/relay/pairing.go:28`). Any bound
account without a billing team could guess lookups (per-prefix and per-account limits slow
this, but do not stop many accounts from many prefixes; GitHub accounts are free) and land in a
stranger's billing team: past the invite gate, able to send mail to any bound key, and shown
the team's member list with their GitHub logins. `tag_R` needs the 50-bit secret and cannot be
tested offline.

The relay learns nothing new: it already sees who redeemed whose lookup, and the pairing's
`pair.confirm` envelopes. A hostile relay can admit anyone anyway (it runs the accounts).

Alternatives: (b) separate **seat codes** the contact makes on the account page and hands out
next to the team invite (two codes per person, breaks 4.9's one step); (c) no billing teams:
quota per account (simpler, but the plan's cap is per team and a team of 5 would get 5× the
quota).

Seat management: the account page lists the billing team's members (display names) to every
member (a privacy note item: joining a billing team shows your GitHub login or email to its
other members); the **contact** can remove a member (frees the seat; that account becomes team-less;
its keys are closed and must be admitted again). The operator can do everything with
`relay admin team …`.

## Waitlist

**OD-P4-9** (a): an external form (e.g. a hosted form service) linked from the README, asking
for an email, team size, harnesses used and a free-text "what would you use it for". Its
privacy note says who sees the answers and when they are deleted (at the end of the beta). No
code in the relay; the owner exports the list to pick waves. (b) a `/waitlist` page on the relay
(needs email verification and spam protection: more attack surface for 10 teams). (c) a GitHub
issue template (public: exposes testers' interest). Recommendation: **(a)**.

## Waves

**OD-P4-7** recommendation: **wave 1** = 10 teams (plan), including the owner's own team,
started only after the beta gate (4.0), 4.1–4.4, 4.9 and the demo video; **wave 2** = 10 teams
at beta week 4 **after the outside security review's findings are fixed or documented** (plan
4.8 "before wave two"); **wave 3** = 10 teams at week 6, so that the 30 invited teams of Gate 2
have at least 6 weeks of use before weeks 9–12 are measured. 8 seats per billing team. Codes
expire after **48 hours** (D36); the owner issues a new code on request.

## Relay storage (relay migration R3; `billing_*` tables are created in R2 with accounts)

| Table | Columns |
|---|---|
| `beta_invites` | `ref`, `code_hash` UNIQUE, `wave`, `seats`, `note`, `created`, `expires`, `state` (`open`/`redeemed`/`revoked`), `redeemed_by` NULL, `redeemed_at` NULL |
| `billing_teams` | `id`, `seats`, `wave`, `state`, `contact_account`, `created`, `note` |
| `billing_members` | `team_id`, `account_id` UNIQUE, `joined`, `via` (`invite`/`pairing`/`operator`) |

## Acceptance (summary)

- Plan 4.3 **on macOS, Linux and Windows** (CI matrix, fake OAuth): A redeems a beta invite and
  is contact of a new billing team; A runs `team invite`; B (logged in, team-less) runs `team
  join <code>`, is admitted and can send A a request; the 9th person gets `team_full` and
  cannot send mail.
- A team-less account that redeems the right lookup with a wrong secret is **not** admitted
  (the issuer never sends `pair_admit`); a `pair_admit` for a key that did not redeem this
  issuer's lookup, a second `pair_admit` for the same record, or one after 10 minutes changes
  nothing.
- Single use, expiry, revoke, and the failure rate limit; `invite list` never prints a code.
- The contact removes B: B's connection closes, B is team-less, the seat is free.
