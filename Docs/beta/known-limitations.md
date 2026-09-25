# Known limitations (Phase 1 beta)

Status: Phase 1. These are **deliberate** limits of the first beta, not bugs. Several may
change after beta feedback. If one of them gets in your way, tell us: that is how we decide
what to change first. The decisions behind them are in
[../review/11-phase1-tickets.md](../review/11-phase1-tickets.md#owner-decisions-needed).

## Teams

- **One owner per team, no admins.** Only the person who created a team can invite, remove
  or rename members, or delete the team. Other members can only leave. There is no "admin"
  role and no way to share this job yet. (OD-P1-1)
- **If the owner loses their device, the team cannot be changed any more.** Ownership
  cannot be moved to another person or another device, so nobody can add or remove members
  any more. The fix is: everyone leaves the team (`agentnet team leave`), and someone
  creates a new one and invites the others again. Keep the owner's device (and its AgentNet config folder) backed up.
  (OD-P1-10)
- **Invites are by code only.** To add someone, even a person you already paired with, the
  owner runs `agentnet team invite` and gives them the code. (OD-P1-8)
- **At most 32 members per team.**
- **You trust the owner's introductions.** Team members who never paired with each other
  directly are introduced by the owner. If you want to be sure a teammate's key is really
  theirs, pair with them directly or compare fingerprints (`agentnet peers verify`).
- **Leaving is final for that invite.** To rejoin a team you left, or were removed from, you
  need a new invite code.

## Requests

- **No editing.** A sent request cannot be changed. Cancel it and send a new one.
- **Cancel only before it is accepted.** `agentnet request cancel` works while the request
  is waiting or deferred. Once the other side accepts, declines or completes it, cancel is
  refused. Ask them directly to stop, and they can complete it with a note. A cancel sent at
  the same moment the other side accepts may be refused; `agentnet request show` tells you
  which happened.
- **Cancelling does not give back urgency budget.** You can send 5 `high` and 2 `blocking`
  requests per rolling 7 days, across all your teammates. Beyond that, requests go out as
  `normal`, and the output says so. A cancelled request still counts.
- **Size limits.** A title is at most 120 characters, the brief at most 16 KiB, with at most
  20 artifacts (links, branches, commits, paths), and 64 KiB for the whole request.
- **Artifacts are only pointers.** A link or branch in a request gives the other side no
  access. Their agent uses its own access. Access grants come in a later phase.
- **"Complete" is a manual step.** The other side marks the work complete when it is done.
  There are no shared live sessions yet.
- **Offline delivery is held for 7 days.** If your teammate's computer is off for longer,
  the request's delivery shows `expired`, which means "we do not know if it arrived".
  `agentnet request resend <id>` is safe to use (the other side will not see it twice),
  but only for 21 days after you first sent it. After that, send a new request.
- **Very old requests are refused.** A request first sent more than 30 days ago is not
  accepted as new by the recipient.
- **Mixed Phase 1 / Phase 2 teams fall back to Phase 1 behaviour.** If one side of a pair has
  not upgraded yet, a Phase 1 daemon acks work-session mail (`ws.*`) as unsupported, and both
  sides fall back to the plain request lifecycle: the Phase 2 side completes the request
  through the ordinary `request.complete` path once its session ends, with no grants and no
  quarantine (a Phase 1 requester never issues grants). A Phase 1 daemon also refuses a
  Phase 2 request's `context` or `run` members as invalid. Upgrade both sides to get sessions,
  grants, quarantine and the own-device helper.
- **Phase 2 teammates cannot debate.** A debate is a request of the new type `debate`. A
  daemon that has not been upgraded to Phase 3 refuses it as invalid, so the invitation shows
  as failed (`bad_body`) in your request view and nothing is started. Both sides must run
  Phase 3 to debate; there is no fallback to a plain request.
- **No grants during a debate.** A debate's session carries no grants, and while you have a
  debate open with a teammate (invited or running), a sensitive grant to that teammate is
  refused (`debate_open`). Likewise a debate cannot be started or accepted for seven days
  after a sensitive grant to that teammate ended (`quarantine_active`).

## Presence

- **"Offline" can lag by up to about 90 seconds** when a computer loses its connection
  without shutting AgentNet down. A normal stop, or going invisible, shows at once.
- **Invisible hides you from teammates, not from the relay.** The relay can see that your
  computer is connected and when messages flow between whom, but not what they say.
- **"Human present" is shared by default.** Turn it off with `agentnet presence --human off`.

## Notifications

- **Webhooks do not include the request title unless you turn it on**
  (`agentnet notify --webhook-title on`), and never include the brief. Slack and Discord
  cannot check the webhook signature.
