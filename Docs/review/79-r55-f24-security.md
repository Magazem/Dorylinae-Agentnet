# Review 79 — R55-F24 security review (D48 approval gates, presence, teams)

Reviewed: branch `p4/r55-f24`, commit `b4c7b26` (on main `a74555f`, not rebased).
The placeholder migration 22 is ignored as instructed.
Reviewer: R55-F24sec-Opus. This is a report only; no production code was changed.

## Verdict

**Changes required: one High and one Medium.** The peer_verify gate is sound. The
team_invite gate breaks its own "one approval, one code" guarantee under concurrent
calls (H1, reproduced by a test). The new ping gate does not stop a paired peer from
learning that an invisible daemon is online (M1), so the claim added to the docs is
false. Everything else checked is correct, or has only Low or Info items.

## Findings

### H1 — One team_invite approval releases several invite codes (TOCTOU in the gate)

- **Where:** `internal/daemon/team.go:271` (`gates.has`), then `apprStore.Show`, then
  `team.go:287` (`gates.drop`), then `:288` `pairs.StartTagged`.
- **Scenario:** a prompt-injected owner's agent gets one human approval. It then sends N
  concurrent `team_invite {"team", "approval": id}` calls. The IPC server runs one
  goroutine per connection (`internal/ipc/ipc.go:138`). Every call passes `has()` and
  `Show()` (state `approved`) before any call reaches `drop()`, so each one mints its
  own invite code. `peers.Manager` allows up to 16 pending issuer sessions
  (`internal/peers/pairing.go:71`). The result is up to 16 strangers enrolled, and every
  teammate's daemon trusts them. That is exactly what D48 / R55-084 is meant to stop.
  The human approved "a one-time invite code".
- **Proof:** `internal/daemon/review79_invite_race_test.go`
  (`TestReview79TeamInviteApprovalConcurrentReuse`, 8 concurrent calls) fails 4 of 10
  runs (`-count=10`) with "one approval released 2 invite codes".
- **Fix direction:** make the claim atomic. Add `inviteGates.take(id, team)` that, under
  `g.mu`, checks the entry and marks it `claimed`, or deletes it. Only the claimant may
  call `Show`; while the approval is still pending, put the entry back or unmark it.
  Simpler: keep the entry, and once `Show` returns approved, do a compare-and-delete
  under the mutex (`if _, ok := g.m[id]; !ok { return bad_state }; delete`) before
  `StartTagged`. Only the goroutine that deleted the entry proceeds. Keep the new test
  as a regression test.

### M1 — The ping gate does not hide an invisible daemon from a paired peer

- **Where:** `internal/session/session.go:704` gates only the `pong`, but
  `session.go:581` `onInit` answers every Noise handshake `Init` with a `Resp`
  regardless of visibility. Docs: `Docs/cli/ping.md:39` ("does not reveal whether the
  peer is online") and `Docs/protocol/presence.md` §Visibility "Pings".
- **Scenario:** A pings invisible B when no session is open. B answers the handshake,
  and A's session opens. With the stock tools, A's `ping_status` / `--json` shows
  `"handshake": true` (the session was set up) and A's audit log gets a `session.open`
  row for B. A ping to an offline B gives neither. So A learns B is online on demand,
  which is what R55-077 set out to remove. The RTT of the `Resp` also works as a
  liveness timer for a modified client. The error text is the same in both cases
  (`timeout`), but the side channels are not.
- **Fix direction (owner choice):**
  - (a) Apply the same gate in `onInit`. When `PingAllowed(from)` is false, do not
    answer the Init, or drop it silently. The only other session user is capability
    fetch (`internal/daemon/fetch.go:51`), so either allow peers that hold an active
    grant, or accept that an invisible daemon does not serve fetches.
  - (b) Keep the behaviour, and correct `ping.md` / `presence.md` to say that invisible
    hides heartbeats and pongs but not session reachability.

  Either way, add a test asserting A sees no difference between "B invisible" and
  "B offline" (`Handshake` and the audit rows).

### L1 — Releasing an invite code is not audited

- **Where:** `internal/daemon/team.go:287-292`.
- **Scenario:** the audit log shows `approval.create` and `approval.approve` for kind
  `team_invite` (subject = team id). It has no row for the moment a code was released,
  or which `pairing_id` it became. With H1 unfixed, several codes per approval leave no
  trace. Even after the fix, an approved approval whose code was never collected (or
  was collected by another local caller that read the id from `approval_list`) looks
  the same as one that was used.
- **Fix direction:** append `team.invite {team, approval, pairing_id}` (ids only) when
  the code is released, and add it to `TestAuditInventory`.

### L2 — The rejoin guard compares the leaver's clock with the owner's clock

- **Where:** `internal/team/kinds.go:565`
  (`op.Msg.Created.Truncate(time.Second).Before(at)`).
- **Scenario:** `added` is stamped by the owner. `Created` is stamped by the leaving
  member. If the member's clock is behind by more than the time since they (re)joined,
  a genuine leave is parked in `pendingLeave` and the member stays on the owner's
  roster. The owner keeps sending them rosters, and teammates keep trusting them as a
  member, although they have left locally. A backdated `Created` can only keep the
  sender itself in the team, so this is not an escalation, only an availability and
  hygiene gap.
- **Fix direction:** allow a skew margin, for example `Created + maxSkew < added`, with
  the same skew bound mail already uses. Better: carry the membership epoch or joined
  lookup in `team.leave` and compare that instead of wall clocks.

### L3 — `LeaveNotify` makes a send failure block leaving

- **Where:** `internal/team/store.go:458-462`.
- **Scenario:** if `SubmitTx` fails permanently (`ErrUnpaired` or `ErrNoMailboxKey` for
  the owner), the leave rolls back every time, and the user can never leave the team.
  Before this commit, the leave committed and only the mail failed. I found no
  reachable path to this state: the owner of an active team stays paired, and
  `peers_remove` cascades. So it is Low, but it is a trap for future changes.
- **Fix direction:** on those two sentinel errors, commit the local leave without the
  mail and warn. Also add `var _ team.TxOutbox = (*mail.Outbox)(nil)` (a compile-time
  check) so that a signature drift cannot silently switch to the non-atomic fallback.

### I1 — `idle.Cached` can cache a cancellation as "unknown" for 5 s

- **Where:** `internal/idle/idle.go:111`.
- **Scenario:** the first caller's ctx is used for the OS query. If that ctx is
  cancelled (a short IPC call), `unknown` is cached for `CacheTTL`, and heartbeats in
  that window carry `human: 2`. This is cosmetic, not a security issue.
- **Fix direction:** query with `context.WithoutCancel(ctx)` (the 1 s `Timeout` still
  bounds it), or do not cache results after `ctx.Err() != nil`.

### I2 — Environment note

`TestAuditInventoryDevices` fails on this machine: `bad_scope ... "C:\\" can be
changed by Authenticated Users`. It is a filesystem ACL check on the device-scope
path, which F24 does not touch. I did not attribute it to this ticket.

## Checked and found correct

- **peer_verify cannot be reached without a human.** Trust is raised only in `Perform`,
  inside the confirm transaction (`internal/daemon/trust.go`, `SetTrustTx` +
  `auditTx`). The subject is the public key, pinned in the closure, and the fingerprint
  is derived from it, so a changed key cannot slip in. `Rebuild` re-reads the name and
  paired state in the transaction and `sameFacts` compares them, so a peer card or name
  change between Create and confirm → `ErrChanged`. `Precondition` refuses an unpaired
  peer. `setTrust` never lowers trust and clears `introduced_by`.
- **Approval ids cannot be reused across kinds, teams or restarts.** `inviteGates` holds
  only team_invite ids created by this daemon, keyed to the team, and `has` checks the
  team. An approved grant or peer_verify id is refused, and so is another team's id
  (covered by `TestTeamInviteNeedsHumanApproval`). A restart empties the map, so the
  caller must ask again. Approvals pending at a restart are expired by `ExpireStale`.
  After confirm, the handler re-checks owner, active state and team size before
  releasing a code, and `Rebuild` checks name, owner and state at confirm.
- **Approval window text.** It uses the shared `peer()` renderer: the full grouped
  fingerprint plus the display-safe name (D9). The team_invite text names the team, its
  id, and the consequence ("every member's daemon will then trust them"). Both texts
  end with "Confirm only if you … yourself" (F5 rules).
- **Test hooks.** `autoHuman` and `humanApprove` exist only in `_test.go`.
  `Options.ApprovalNotify` / `ApprovalWindow` are set only from Go, and production
  `agentnetd` (`cmd/agentnetd/main.go` `daemonOptions`) sets neither. There is no
  env-var auto-approve (grepped). Terminal mode takes the code from the daemon's own
  stdin, so the human is still in the loop.
- **Gate memory and TTL.** Entries are added only after a successful `Create`, which is
  bounded by `MaxPending` 5 and `MaxPerHour` 20. Expired entries are swept on `put`,
  and `has` ignores stale ones. With a 2×TTL (20 min) lifetime, the map holds at most
  about 20 entries. `OnReject` and the expired or rejected paths drop entries.
- **fingerprint_mismatch.** The comparison is constant-time, the message is fixed, no
  approval is created, and the audit row (`peer.verify_fail`) carries only the peer's
  own public fingerprint. Nothing secret is involved. The row is unbounded per call,
  which is audit-volume territory (F14).
- **Audit.** `peer.verify` is written once, in the confirm transaction.
  `approval.create` and `approval.approve` cover both kinds. `TestAuditInventory`
  passes (updated for the approval step).
- **presence_set strictness.** `DisallowUnknownFields`, an empty body refused,
  `team` only with `only_team`, the team must be active, and an unknown mode is
  refused (`TestPresenceSetSpecShape`). Minor: if `SetHumanShare` fails, the new mode
  is already applied (non-atomic). This is harmless.
- **Ping gate internals.** It fails closed on DB errors, the lock is released around the
  DB read, and the session is re-validated after relock. Invisible vs offline shows no
  timing difference in the pong path, because neither sends one. The handshake path is
  the M1 issue.
- **team leave outbox.** `SubmitTx` runs in the leave transaction, before
  `GCIntroduced`, so the owner's mailbox key is still readable. The frame is sealed at
  submit, and `Wake` runs after commit. `mail.Outbox` does implement
  `SubmitTx` / `Wake`.
- **Idle per OS.** `GOOS=linux|darwin|windows go vet` is clean for `internal/idle`,
  `cmd/agentnetd`, `internal/presence`, `internal/session` and `internal/daemon`. The
  per-OS files are `idle_{linux,darwin,windows,other}.go`. The cache holds its mutex
  across a query bounded to 1 s.
- **Migration 23.** The DDL is identical to migration 19's approvals table apart from the
  CHECK. It uses explicit column lists, and the `approvals_state` index is recreated.
  No foreign key or trigger references `approvals`. New rewind test
  `internal/store/review79_migration23_test.go`: a schema-22 table with rows of every
  old kind and state is migrated, every column survives, the index exists, and
  `team_invite` is accepted. It passes.

## Tests run (targeted)

- `go test ./internal/approval/ ./internal/approvaltext/ ./internal/presence/ ./internal/team/ ./internal/session/ ./internal/idle/ ./internal/store/`: all ok.
- `go test ./cmd/agentnet/ -run 'Peers|Team|Presence|Ping'`: ok.
- `go test ./internal/daemon/ -run 'Verify|Invite|Presence|HumanGate|Trust|TestAuditInventory$|Review79'`: ok on one run. `Review79` is the H1 reproduction and fails intermittently (4/10), as expected.

## Files created by this review

- `Docs/review/79-r55-f24-security.md` (this file)
- `internal/daemon/review79_invite_race_test.go` (H1 proof; fails until H1 is fixed)
- `internal/store/review79_migration23_test.go` (migration 23 rewind; passes)
