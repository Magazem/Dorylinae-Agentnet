# Review 79c — R55-F24 focused check (M1b fix and rebase port)

Reviewed: branch `p4/r55-f24`, HEAD `0005538` (rebased on main). Reviewer: R55-F24check-Opus.
This is a report only; no production code or tests were changed.

## Verdict

**Approve.** M1b is closed. The per-message gate in `onData` is what closes it;
`DropGatedSessions` only cleans up. Forged responses give a blocked peer no signal. No
path in the session layer answers a blocked peer on the wire. The drop is silent and
has no lock-order problem. The `Outcome` port is correct. There are three Low/Info
notes below and none of them block the merge.

## Checks

1. **Response exemption (`session.go:796-800`).** Only two session handlers exist:
   `fetch.req` (`daemon/fetch.go:51`) and `fetch.resp` (`daemon/fetch_client.go:157`).
   `fetchClient.onResp` (`fetch_client.go:259-272`) returns without a log line, an audit
   row or a reply unless `req` names one of our pending ops to that same peer. A
   `*.resp` type with no handler falls through silently. A forged `pong`
   (`session.go:789`) counts only if its id is a pending ping of ours to that peer.
   Nothing goes back to the peer in any of these cases, so it has no answer to time.
   The only "injection" possible is finishing our own pending fetch from that peer,
   which is the peer we asked. A blocked peer also needs an open session to send data
   at all. After the drop it gets one only when we dial it ourselves.
2. **Other answers to a blocked peer.** There are none on the wire. `onInit` returns
   `""` before any work. `onData` returns `""` for an unknown sid, for a request type
   and for bad JSON. The `ping` case asks `pingGate`, and `PingAllowed` is true only if
   the init gate is also true (`daemon.go:394-400`). Malformed, replay and decrypt
   failures, and `onFin`/`onResp` with an unknown sid, still reach `reject`
   (`session.go:988`). `reject` only logs and audits locally, rate limited, and nothing
   is sent. It is not an oracle.
3. **`DropGatedSessions` (`session.go:712`).** It sends no Fin or close frame, and the
   peer's later data on the old sid is dropped with no answer. It reads the gate with
   no lock held. It is called from `SetMode` (`sender.go:427`) after `s.mu` is
   released. The gate takes `sender.mu`, then the DB, and never `m.mu` or `sendMu`, so
   the locks cannot be taken in the wrong order. A handler that is already running
   when the session is dropped answers through `SendData`. `SendData` finds no
   `current` session and returns `ErrNoSession`, so nothing is sent. The ping path
   re-checks `cur != s` after the gate.
4. **Leave `Outcome` port (`team/kinds.go:541-600`).** Every `return nil` in
   `applyLeave` sets `op.Outcome` first. `op` exists only for one `Handle` call, and
   `store` runs one transaction per op (`mail/receiver.go:276-332`). `After` runs only
   for a new message whose transaction committed (`receiver.go:185`), so it sees exactly
   the outcome its own transaction produced. A nil `Outcome` fails the type assertion
   and returns. No `pendingLeave` reference is left in the repo.

## Findings

### L1 — Gate changes other than `SetMode` leave old sessions open (hygiene only)

- **Where:** the drop runs only from `SetMode` (`sender.go:427`). It does not run when
  `checkTeamGone` auto-switches to invisible (`sender.go:343`), when a grant expires or
  is revoked, when a work session closes, or when a member leaves an `only_team` team.
- **Scenario:** in those cases the session stays in the map. The per-message gate in
  `onData` still drops every request, so nothing leaks. The effect is stale map
  entries, and our own later sends to that peer reuse the old session.
- **Fix direction:** none needed for security. Optionally call `ModeChanged` from
  `checkTeamGone` as well.

### L2 — The drop also cancels our own sessions and dials to that peer

- **Where:** `DropGatedSessions` drops every session of a refused peer, including ones
  we started (`s.initiator`) and a dial that is still running. For a dial,
  `dropLocked` also deletes `m.queue[peer]`.
- **Scenario:** we are fetching from A (A granted us), then go invisible. A holds no
  grant from us, so A is refused and the session is dropped. The `fetch.resp`
  fragments A sends next arrive on an unknown sid and are dropped silently, so the
  fetch times out. A retry dials again and works. A ping of ours to A that is in flight
  at that moment also times out.
- **Fix direction:** optional. Skip sessions where `s.initiator` is true, since our own
  requests are exempt by design and the peer's requests on them are dropped by the
  per-message gate anyway.

### I1 — The exemption is a suffix match on a type the peer chooses, and the ping case does not check `gated`

- **Where:** `strings.HasSuffix(msg.Type, ".resp")` (`session.go:799`). The `ping` case
  relies on `pingGate` alone (`session.go:770`).
- **Scenario:** today this is safe, because `fetch.resp` is the only response handler
  and the daemon sets both gates. It would break if a future request type ended in
  `.resp`, or if a caller set only `SetInitGate`: with a nil `pingGate`, a refused peer
  on an old session gets a pong.
- **Fix direction:** mark responses when a handler is registered (for example a
  `HandleResponse`), and add `|| gated` to the ping refusal.

### I2 — A database query per inbound data message while not visible

- The gate runs before the sid lookup (`session.go:744`), on the single worker
  goroutine. In visible mode, `PingAllowed` returns true without a query. When the
  daemon is invisible or `only_team`, every data envelope from a paired peer costs one
  or two queries, including junk sent with unknown sids. The peer must be paired and
  the relay limits its rate, so this is acceptable. Optional: look up the session
  first, and gate only when the sid is known or the answer would be a reject.

## Tests run

- `go build ./...`, `go vet` on session, team, presence and daemon: clean.
- `go test -count=1 ./internal/session/ ./internal/team/ ./internal/presence/`: ok.
- `go test -count=1 -run 'Presence|Ping|Session|Gate|Invite|Verify|Leave' ./internal/daemon/`:
  one failure, `TestHelperRunSessionOwnedByRunner`. It fails because `C:\` on this
  machine is writable by Authenticated Users (`device_scope_set: bad_scope`). This is
  the environment, not the change. Everything else passed.
- `-race` was not run: cgo is not available on this machine.

Files created: `Docs/review/79c-r55-f24-check.md` (this file).
