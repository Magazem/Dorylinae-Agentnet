# Beta invitations, billing teams, seats and waves

Status: **draft** (Phase 4 spec, tickets 4.3a–4.3b in
[../review/49-phase4-tickets.md](../review/49-phase4-tickets.md)). Not approved. Open
choices are **OD-P4-n**.

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
  `relay admin invite create [--wave N] [--seats 8] [--expires 30d] [--note TEXT]` prints the
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

**(a) Admission by pairing.** When a `pair_redeem` succeeds at the relay (the lookup matched an
outstanding code), and the **redeemer's** account has no billing team, while the **issuer's**
account has one with a free seat, the relay adds the redeemer's account to the issuer's billing
team and says so in the `pair_redeem` answer (`"admitted":"bt_…"`) and in the next `ready`.
The relay already sees who redeems whose lookup, so this adds no new metadata. It makes a
normal `agentnet team join <code>` (or `pair <code>`) the only step for every member after the
first. If there is no free seat the pairing still proceeds, but the redeemer stays
team-less and gets `error` `team_full` (ref = the pairing), so `team join` can report "the
inviting team has no free seats on this relay".

A pairing that later fails its MAC check (a wrong secret, a relay attack) has already admitted
the account. Accepted: the redeemer had a valid lookup, which only the code holder has, and the
billing team's contact (or the operator) can remove the account (below).

Alternatives: (b) separate **seat codes** the contact makes on the account page and hands out
next to the team invite (two codes per person, breaks 4.9's one step); (c) no billing teams:
quota per account (simpler, but the plan's cap is per team and a team of 5 would get 5× the
quota).

Seat management: the account page lists the billing team's members (display names) to every
member; the **contact** can remove a member (frees the seat; that account becomes team-less;
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
expire after 30 days; an unused code is replaced once.

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
- Single use, expiry, revoke, and the failure rate limit; `invite list` never prints a code.
- The contact removes B: B's connection closes, B is team-less, the seat is free.
