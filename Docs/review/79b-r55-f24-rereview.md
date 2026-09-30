# Review 79b — R55-F24 security re-review (review 79 fixes)

Reviewed: branch `p4/r55-f24`, HEAD `55d8e34` (the review 79 fix commit, on main `a74555f`,
not rebased). The placeholder migration 22 is ignored as instructed.
Reviewer: R55-F24rereview-Opus. This is a report only; no production code was changed.

## Verdict

**H1, L1, L2, L3 and I1 are fixed. M1 is fixed for new sessions but not for sessions that
already exist (M1b, Medium, proven by a test).** D60 promises that a blocked peer cannot
tell an invisible daemon from an offline one. The new init gate refuses a new
handshake, but a Noise session opened while the daemon was still visible never
expires. On that session the fetch server still answers, so the peer gets an on-demand
liveness check again. Two more signals outside the session layer (mail acks and the
relay's `queued` frame) still reveal that the daemon is online. Those are older gaps
that the new docs now contradict (M2, owner decision). Everything else holds.

## Findings

### M1b — A session opened while visible survives the switch to invisible and still answers

- **Where:** `internal/session/session.go:593` gates only `onInit`, and `:697` `onData`
  gates only `ping`. Handlers registered with `Handle` (`fetch.req`,
  `internal/daemon/fetch.go:51`) still run and reply. Established sessions have no
  lifetime (`sweep`, `session.go:898`, drops only half-open handshakes). `presence_set`
  (`internal/daemon/presence.go`) does not close or re-gate the open sessions.
- **Scenario:** A and B talk while B is visible (a ping, or a fetch). B then goes
  invisible to hide from A. The session is still in both daemons' maps until one of
  them restarts. A sends `fetch.req` (any request id, no or bogus grant). B's
  `FetchServer.Handle` queues it and answers `unknown_grant`, `revoked` or
  `rate_limited` right away. For an offline B, A gets nothing. This needs no modified
  client: after B revokes A's grant, a stock `agentnet fetch` on A still sends the
  request, and it gets `revoked` back instantly while B is invisible. The docs added in
  this commit (`Docs/cli/ping.md:37-41`, `Docs/protocol/presence.md` §Visibility "Pings")
  say "exactly as for an offline peer", which is false in this case.
- **Proof:** `internal/session/review79b_stale_session_test.go`
  (`TestReview79bOpenSessionOutlivesInvisible`) opens a session with a ping, closes both
  gates, and sends a data message on the old session. The invisible side's handler
  answers. The test fails every time (`-count=1`, 0.14 s).
- **Fix direction:** apply the same rule to data on an established session. In `onData`,
  before any handler runs, call the init gate (`initGate(ctx, from)`, which already
  includes the live-ties exception) without the lock and drop the message silently when
  it refuses. Alternatively, on a presence mode change, drop every open session whose
  peer the new gate refuses (`SyncVisibility` already computes who leaves the visible
  set). Keep the test as the regression test: it should then pass.

### M2 — Mail acks and relay `queued` frames still show an invisible daemon as online (older gap, owner decision)

- **Where:** `internal/mail/receiver.go:286` (`ack` is sent for every applied mail,
  whatever the visibility). `internal/relay/relay.go:916-937` (`enqueue` sends
  `{"op":"queued"}` only when the recipient is not connected). Also
  `internal/presence/sender.go:674` `MaybeResync`: an invisible team owner mails a
  roster to a member whose heartbeat reports a stale epoch, at most once per 10 minutes
  per team.
- **Scenario:** A blocked, paired A sends B any mail with the stock tools
  (`agentnet mail send`). If B is online (invisible), the outbox row turns `delivered`
  within seconds. If B is offline, it stays `relayed` until B comes back. With a
  modified client A does not even need to be paired: any envelope to B's key returns a
  `queued` frame only when B is offline. A member of a team B owns can report a stale
  epoch in its heartbeat and gets a roster mail back from an invisible B.
- **Why this is not a regression:** none of these paths changed in F24, and D60 covers
  only the handshake. But the text added in this commit ("exactly as for an offline
  peer", "does not reveal whether the peer is online") now reads as a general guarantee,
  and `presence.md` §Visibility already says "B sends A nothing".
- **Fix direction (owner):** either (a) narrow the docs now: invisible hides heartbeats,
  pongs and new sessions, but mail delivery (acks), roster resync and the relay's
  queue behaviour still reveal that the daemon is reachable; or (b) track it as a
  follow-up: hold acks and resyncs to gated peers until visible (this costs resends and
  the delivery latency of mail), and relay-side `queued` padding (not possible without
  changing the relay protocol). (a) is enough for this ticket.

### L4 — The live-ties exception is wider than what fetch needs, and lasts as long as the peer wants

- **Where:** `internal/daemon/ping.go:153` `peerHasLiveTies`, `work_sessions ... state <> 'closed'`.
- **Scenario:** fetch needs a grant, and the grant clause alone keeps it working. The
  work-session clause lets any peer with an open, `awaiting_result` or `quarantined`
  work session with us complete a handshake while we are invisible. A completed
  handshake is itself a liveness signal (`Handshake: true`, a `session.open` row), even
  though its pongs are refused. Such a session stays non-closed for as long as the
  requester or worker does not finish it, so the peer controls how long the exception
  lasts. I found no way for a peer to create a tie by itself. Work-session rows are
  created only on our own accept (`worksession/hooks.go:23`, worker role) or for a
  request we sent (`receive.go:122` and `OpenSession`, requester role). An `issued`
  grant needs our own grant flow, and a `held` grant does not count (the test covers
  this).
- **Status:** this matches D60 as written ("active grant or open work session"). If the
  owner wants the tighter reading, drop the work-session clause, or limit it to
  `state = 'open'`. Otherwise record it as accepted.

### I3 — Invite code released after an audit failure is orphaned (not a leak)

- **Where:** `internal/daemon/team.go:309-315`.
- **Scenario:** if `log.Append(team.invite_issued)` fails after `StartTagged` succeeded,
  the caller gets an error and not the `pairing_id`. The pending issuer session keeps
  running until the code TTL expires. Nobody learns the code (it can be read only via
  `pair_status` with the random id), so this is not exploitable. It is only an
  unaudited, dead pairing. Optional: cancel the pairing on audit failure.

## Fixes verified

- **H1 fixed.** `inviteGates.take` (`team.go:181`) does a compare-and-delete under
  `g.mu`, and the handler calls it after `Show` returned `approved` and before
  `StartTagged` (`team.go:305-309`). Exactly one concurrent caller proceeds.
  - Show→take window: `approved` is terminal (a reject or expiry only moves
    `pending`), so the state cannot change after `Show` saw `approved`. `OnReject`
    only drops the entry, which makes `take` fail closed.
  - The id stays spent when `StartTagged` fails: acceptable, and fail-safe. The caller
    needs a new human approval, and no code exists.
  - Owner, active state and size are checked before `Show`. A concurrent
    `team_remove` or dissolve between `take` and `StartTagged` is covered by
    `InviteTag` at redeem time, as before.
  - `TestTeamInviteApprovalConcurrentReuse` ×20: see Tests.
- **M1 (new sessions) fixed as D60 describes.** The gate runs before any handshake work.
  A refusal returns `""`, so `handle` sends nothing and writes no reject row. (`reject`
  only logs and audits locally; it never answers on the wire, so the other session
  rejects are not an oracle either.) The pinger's initiator handshake times out through
  `sweep` exactly as for an Init the relay queued for an offline peer, and both give
  `timeout`, `Handshake: false` and no `session.open` row. The timing does not differ,
  because neither case produces any response. The gate reads the database with no
  session lock held (only `sendMu`), and it fails closed. Granted fetches still work:
  the grantee (`direction = 'issued'`, `state = 'active'`, `exp > now`, the same
  `storeTimeFmt` the capability store writes) passes. `TestPeerHasLiveTies` covers the
  expired, revoked, held and closed cases. Gap: M1b.
- **L1 fixed.** `team.invite_issued {team, approval, pairing_id}` holds ids only, no code
  or secret. It is in `Docs/protocol/team.md` and in `TestAuditInventory`.
- **L2 fixed.** `Created + mail.MaxSkew (10 min) < added` (`kinds.go:567`). This is
  skew-safe up to the same bound mail uses (`open.go:122`). Replaying an old leave is
  impossible: `mail_seen` dedupe with `SeenRetention` 35 d > `MaxAge` 30 d, plus
  `ReceiveMaxAge` 14 d. Residual (Info): a genuine leave sent less than 10 minutes
  before a rejoin, and delivered after it, now removes the rejoined member. That needs
  out-of-order delivery around a live pairing, and it only removes the sender
  itself. It is not an escalation.
- **L3 fixed.** `var _ team.TxOutbox = (*mail.Outbox)(nil)` (`team.go:641`). The sentinels
  come from the seal step before any insert (`outbox.go:193-217`), so the transaction
  is still clean when the leave commits. A peer cannot force a silent leave:
  `LeaveNotify` runs only from the local `team_leave`. `ErrUnpaired` or
  `ErrNoMailboxKey` for the owner arise only from local state (the owner is paired
  directly by the invite, and `peers_remove` is local). The only effect is that the
  owner is not told, so the leaver stays on the owner's roster, which gives nobody new
  trust. Info: the CLI result does not say that the owner was not notified (only a log
  warning).
- **I1 fixed.** `f(context.WithoutCancel(ctx))` (`idle.go:113`). The per-OS query keeps its
  own 1 s timeout.

## Tests run (targeted)

- `go test ./internal/daemon/ -run TestTeamInviteApprovalConcurrentReuse -count=20`: ok
  (20/20; it failed 4/10 before the fix).
- `go test ./internal/daemon/ -run 'Invite|Verify|Presence|Ping|HumanGate|Session|PeerHasLiveTies|AuditInventory$|Fetch'`:
  ok (includes `TestPingInvisibleLooksOffline`, `TestPeerHasLiveTies` and the fetch tests,
  so granted fetches still work).
- `go test ./internal/team/ ./internal/presence/ ./internal/idle/`: ok (includes
  `TestStaleLeaveDoesNotRemoveRejoinedMember` and `TestLeaveNotifyLeavesWhenTheMailCanNeverBeSent`).
- `go test ./internal/session/ -skip Review79b`: ok (`TestInitGateLooksOffline`,
  `TestPingGateSilencesPongs`).
- `go test ./internal/session/ -run Review79b`: **FAIL**, as expected (M1b proof).

## Files created by this review

- `Docs/review/79b-r55-f24-rereview.md` (this file)
- `internal/session/review79b_stale_session_test.go` (M1b proof; fails until M1b is fixed)
