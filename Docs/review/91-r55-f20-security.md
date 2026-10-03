# 91 — R55-F20 security review

Branch `p4/r55-f20`, ticket commit `eb7994c`. Spec: [83-r55-f20-spec.md](83-r55-f20-spec.md) incl.
Review 83b (owner D67: ODs 1–3, 5–7 (a), OD-4 (b)). Report only. The proof tests are in
[91-tests/f20sec_test.go.txt](91-tests/f20sec_test.go.txt). To run them, copy the file to
`internal/request/` as a `_test.go` file.

## Verdict

**Approve with two Low fixes recommended (S1, S2) and one Low follow-up (S3).** Nothing is
High or Medium. Refusing colliding ids holds up: an honest peer's request cannot be blocked,
and no new way was found for a peer to make a by-id reference resolve to the wrong row.

## Findings

| # | Severity | Finding |
|---|---|---|
| S1 | Low | Collision check is an unindexed scan placed before the daily cap |
| S2 | Low | R55-163 `{}` row still keeps the refused peer's cancel `reason` text |
| S3 | Low (legacy rows only) | `ws_*`/`wait r-X` ignore session-less request rows when counting matches |
| S4 | Info | Membership oracle: whether this node holds any row with a given id |

### S1 — the collision scan runs before the cap (Low)

`internal/request/receive.go:206` (`idTakenTx`, `:480-490`) runs before `checkDailyCaps`
(`:217`). The query `WHERE id = ? AND NOT (...)` has no index that starts with `id`. Measured
plan: `SCAN requests USING COVERING INDEX sqlite_autoindex_requests_1`. At 100,000 rows it
took **22.8 ms per call** (proof test `TestF20SecIDTakenScanCost`). The real index is bigger,
because peers are full keys rather than the short `peerN` used in the test.

Scenario: a paired peer that is already over its daily cap keeps sending new request mails.
Before F20, each one cost only PK/index lookups before the `limit` refusal. Now each one
costs a full index scan first. The store uses `SetMaxOpenConns(1)`
(`internal/store/store.go:616`), so each scan stalls every other DB user of the daemon. The
spec's OD-7 premise ("at most 200 per peer a day") is false, because the cap does not limit
the scan. On a 100k-row table the scan is also above OD-7's own 10 ms threshold for adding
the index. Submit runs the same scan on every submit (`submit.go:178`), which the local user
controls.

Fix direction: take OD-7 (b), migration 26 `CREATE INDEX requests_id ON requests (id)`. Keep
the check where it is: its place before the caps is what keeps refusals out of the cap count.

### S2 — `{}` tombstone row keeps peer text in `reason` (Low)

`internal/request/receive.go:441-460`: when `policyDeclineCode` returns a code, `body` becomes
`{}`, but `reasonArg` is still the cancel reason that the same refused peer sent in its
`request.cancel`. `toView` returns it (`internal/request/view.go:174`), so `request_show` and
`inbox --all` display it.

Proof: `TestF20SecTombstonedRowKeepsPeerReason`, with an unverified peer. Result:
`title="" reason="PEER TEXT visit evil.example"` (fails). OD-4 (b) was meant to keep no peer
text from a peer that D5 or the team rules refuse. Bounded by the tombstone and daily caps.

Fix direction: in the `code != ""` branch also set `reasonArg = nil`. Extend acceptance test
13 to check `reason` is empty for the three refused cases and kept for a verified member.

### S3 — `r-` session shorthand counts sessions only (Low, legacy rows only)

`internal/daemon/session.go:141-146` → `GetByRequestID` (`internal/worksession/store.go:280`)
returns `ambiguous_request` only when two or more **sessions** share the id.

Scenario: a pre-F20 database holds `out r-X` to C (pending, no session) and `in (B, r-X)` with
a worker session. `agentnet wait r-X` or `ws_* r-X`, meant for the out request, silently
resolves to B's session. This is the "first row found" pattern that review 83b F3 fixed for
`log --session` by counting session and request rows together. Post-F20 rows cannot reach this
state (receive refuses, Submit re-draws), so the impact is limited to old collisions.

Fix direction: count `(direction, peer)` pairs from `work_sessions` and `requests`, as
`audit.sessionClause` does. Or document it as an accepted legacy gap.

### S4 — id membership oracle (Info)

A paired peer that holds an id `r-X` (for example from a shared Decision file) can send a
request `r-X` and tell from the ack whether this node holds **any** row with that id. The ack
is `rejected` if it does. Otherwise the request is stored and acked `ids`, or, when the peer
has filled its open cap, auto-declined. The wire does not distinguish `bad_body` from `limit`
(both `rejected`), so the oracle answers only this. It is bounded by the daily cap for
negative answers. Ids are 128-bit `crypto/rand` (`internal/request/wire.go:14`), so it confirms
only ids the peer already has, and those already name the parties. No fix needed. Mention it in
request.md if wanted.

## Checked, no issue

- **Blocking a third peer's request.** Ids come from `crypto/rand`. The request id travels only
  inside the sealed payload: the envelope `id` is the mail id (`internal/mail/outbox.go`
  `SubmitTx`), so a relay operator never sees it. `request_resend` reuses the id only for
  undelivered mail (outbox `expired`/`failed`). A Decision exists only after accept, and by
  then the row is stored here. The legitimate sender's resend dedupes on `(from, id)`
  (`receive.go:180`) before the new check. A tombstone is keyed `(peer, id)`, so another
  peer's `request.cancel` cannot touch it. No path was found for a peer to learn an id before
  the recipient stores it.
- **Wrong-row resolution.** Every id-alone query was swept: `Show` with `from` gives the `in`
  row only; `FindIn`/`findInRow` returns ambiguous for several senders; accept/decline/defer go
  through `findInRow`; cancel/resend go through `out` only, and out ids are locally unique and
  re-drawn. Consult checks the session by the exact `DeriveID(peer, self, id)`. The debate
  title direction comes from `DeriveID(rs.Self, …) == sid`, and `rs.Self` is wired
  (`daemon.go:496`). `decision_show r-` is resolved through `debates` with the ambiguous
  mapping. Retention, worksession hooks, phase 1 and mirror use exact keys. Device runs and
  helper routing create rows only after the check.
- **Races.** Receive and Submit check and insert in one transaction on a single connection
  (`SetMaxOpenConns(1)`), so the check and the insert are serialised.
- **Rebuild loop.** `insertOut` checks before `Outbox.SubmitTx`. On `errIDTaken` the deferred
  rollback discards everything: no outbox row, no debates row, because `InTx` runs after. The
  loop is bounded at 8 draws. `Prepare` draws a fresh nonce and session id on each try, so
  there is no commitment or nonce reuse, and the closure keeps the last values, which match the
  stored request.
- **F12/F13/F14/F18.** A refusal uses `storeBad`: one marked `mail_seen` row, no `mail_inbox`
  row; a resend of the same mail is re-acked `rejected` without `Apply`. It is not counted
  towards the caps and does not consume a tombstone. It is audited once per mail as
  `mail.reject {bad_body}` (rate-limited) with no request id. The F18 ack is `rejected`. The
  ordering (Decision → collision → caps → tombstone → 30-day) matches the spec.
- **Error text.** `ambiguous_request`, `unknown_decision`, the `debate --cancel` `bad_state`
  (the user's own session and request ids only), the `wait` exit and `audit.ErrAmbiguous`
  carry no other peer's key, title or state. `bad_body` text is never put on the wire.
- **`{}` readers** (apart from S2): show, inbox, resend duplicate (`body_hash` kept) and a
  second cancel. The existing test covers these and passes.

## Tests run

`go test ./internal/request ./internal/worksession ./internal/debate ./internal/decision
./internal/audit`, `./internal/daemon -run 'Request|Ws|Debate|Decision|Log|Notify|IDCollision'`,
`./cmd/agentnet -run 'Request|Debate|Log|Wait|Consult'`: everything passes except the known
`audit TestQueryFilters` (time bomb), `daemon TestHelperRunsInScopeRequests` (local C:\ ACL), and
the S2 proof test, which was run with the proof file temporarily in `internal/request` and then
moved out.

## Files created

- `Docs/review/91-r55-f20-security.md` (this file)
- `Docs/review/91-tests/f20sec_test.go.txt` (proof tests for S1 and S2; was briefly
  `internal/request/f20sec_scan_test.go`, removed)
