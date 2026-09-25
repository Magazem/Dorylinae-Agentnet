# Known limitations (beta)

Status: Phase 1–3. These are **deliberate** limits (or, where noted, accepted gaps) of the
beta, not bugs. Several may change after beta feedback. If one of them gets in your way, tell
us: that is how we decide what to change first. The Phase 1 decisions behind them are in
[../review/11-phase1-tickets.md](../review/11-phase1-tickets.md#owner-decisions-needed); the
Phase 3 ones in
[../review/42-phase3-tickets.md](../review/42-phase3-tickets.md#owner-decisions-needed).

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

## Debates and decisions (Phase 3)

- **A Decision can end up single-signed.** If a peer goes silent after your side has closed
  the debate and signed, the exported Decision carries only your signature: `decision verify`
  exits 6 ("unconfirmed") and the Markdown shows an UNCONFIRMED banner, "Outcome claimed by
  the initiator". Only a Decision with **both** signatures proves anything about what the
  respondent agreed to. (OD-P3-5, OD-P3-14)
- **The audit chain does not prove anything against a full database rewrite.** It makes
  tampering with individual rows evident (a changed, deleted or reordered row breaks
  `agentnet log --verify`), but anyone who can write your `dorylinae.db` file — which includes
  any program running as you — can drop the chain's triggers, edit rows and recompute every
  hash from scratch. Use `agentnet log --head` to record an anchor (in a commit message, a
  ticket, a message to a teammate) if you need to detect a rewrite of everything **before**
  that point. (OD-P3-6)
- **Deleted content can survive in free pages and in the WAL until a checkpoint.** The store
  now runs with `PRAGMA secure_delete=ON`, which zeroes bytes it frees or overwrites, but a
  page changed under WAL journal mode is only folded back into the main database file at a
  checkpoint; until then an old copy of a deleted or quarantined value can remain in the
  `-wal` (or `-shm`) file. This narrows, but does not close, the gap described under
  [Requests](#requests) for quarantined results. (OD-P3-13; see also
  [work-session.md](../protocol/work-session.md#release))
- **Program-owner checks are weaker on macOS/BSD and over network paths.** The check that a
  device-linking helper program is owned and only writable by someone you trust does not read
  macOS/BSD ACLs (a `chmod +a` grant is invisible to it), and on a UNC path, mapped drive or
  NFS mount it trusts the file server's reported owner and administrators group, which the
  server (not your machine) controls. (review 41 L4, L5)
- **A same-size file rewrite between two reads of a fetched file is not detected.** `changed`
  compares size and modification time; a rewrite that keeps both the same (rare, and only
  possible for files over 256 KiB rewritten mid-fetch) passes unnoticed. (OD-P3-11; see
  [fetch.md](../cli/fetch.md))
- **On macOS, a long config-directory path can be too long for a Unix domain socket.**
  `agentnetd.sock` lives under your per-user config directory; macOS's `sun_path` limit (104
  bytes, shorter than Linux's 108) can be exceeded by a long username or a relocated
  `$DORYLINAE_HOME`, and the daemon then fails to bind. Move `$DORYLINAE_HOME` to a shorter
  path if this happens.

## Revisit in a later review

- Decision exports are not audited (D33); revisit (a small IPC call that records `decision.export` with no content) in the next large security review.
