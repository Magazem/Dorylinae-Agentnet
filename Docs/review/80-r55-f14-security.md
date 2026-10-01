# 80 — R55-F14 security review (Opus)

Ticket R55-F14, commit `edecc82` on `p4/r55-f14` (rebased on main `450bf51`). Spec
[72](72-r55-f14-spec.md) incl. review 72b, owner decision D58 (supersedes D49). Findings
R55-015, R55-016, R55-042. Report only: no code was changed.

## Verdict

**PASS. Nothing blocks the merge.** The audit contract matches D58: a relay alone can no
longer add audit rows beyond one `mail.reject` per genuine peer-signed envelope per run, and
one `relay.reject_summary` a day plus one at a clean stop. The log limiter is keyed by fixed
events, counts per reason, has bounded memory and is flushed at stop. The macOS change is
safe. There are four Low findings (one under-audit, two test gaps, one doc drift) and some
notes. None of them lets a relay grow the audit log or the daemon log.

## Checked

**Which rejects are audited.** I re-derived this from `internal/mail/{open,receiver,body}.go`:

| Reject | Relay alone? | Code result |
|---|---|---|
| Steps 1–7 (`unpaired`, `malformed` 2/5, `key_miss`, `decrypt`, `sender_mismatch`, `bad_signature`) | yes | `auditable` → false (`audit.go:78`, `Step < 8`) |
| Step 8 `wrong_recipient`, 9 `id_mismatch`, 10 `v` | no. HPKE info binds `from`/`to`/`key_id` and aad binds `env.ID` (`open.go` step 4), so only a replay is possible | audited once per `(peer, id)` |
| Step 11 `stale` (Open and receive age) | yes (replay) | logged only |
| Step 12 `ack` malformed, forged `keys` announcement | no | audited |
| Step 12 expired announcement | yes (replay) | logged only via `errors.Is(re.Err, ErrAnnouncementExpired)`. The `%w: %w` wrapping keeps it reachable |
| `bad_body` | no (one row per genuine mail through `mail_seen`, `seenDupBad` is not reported) | audited |
| `limit` | F13, not emitted yet | audited |
| Every session reason | yes | none audited. `session.reject` and its budget are removed (`session.go` `reject`) |

**Other relay paths that write audit rows.**
- `mail.in` is written only when the mail is not a duplicate. Its `id` is peer-signed and
  bound by aad, and an unregistered `kind` is written as `"unknown"`. A replay after
  `mail_seen` is pruned (35 d) fails step 11 first (30 d; 14 d for non-`keys` mail).
- A replayed genuine ack goes to `Outbox.OnAck` → `finish`, which only updates rows still
  `queued`/`relayed`. So it is idempotent and writes no row.
- `session.open` still needs the peer's `fin`.
- `OpenPresence` never audits. Presence, `pair.confirm` and key-miss rejects are Debug or
  per-peer limited, and the daemon runs at Info (`cmd/agentnetd/main.go`).
- No other `Append` is reachable per frame.

**`relay.reject_summary`** (`internal/daemon/reject_summary.go`).
- The detail has fixed keys and holds integers keyed by reason constants: no peer, no id.
- `unpaired` is excluded in both feeders (`audit.go` Report, `session.go:907`).
- A row is written by the 24 h ticker and by `stop` (`WithoutCancel`, 5 s), and only when
  non-zero. A crash writes nothing, and nothing a relay controls triggers a clean stop
  (`min_client` is only reported). So a relay can change the numbers but not the row count.
- The defers run in a safe order: relay → mail flush → sessions → summary → `daemon.stop`.
- The ticker goroutine ends at stop.

**lograte** (`internal/lograte/lograte.go`).
- The map is keyed by event name, and the reasons are code constants. I checked this at
  every call site; session reasons come through `handshakeReason`, which returns constants.
  So memory is at most 9 entries plus the reasons.
- Each event has its own entry, so a flood of one event cannot hide another. Inside an
  event, `reasons` counts every reason. The only residual is the one the spec accepted: the
  first-occurrence peer is chosen by the relay.
- `flushEvent` and `Flush` are safe under the pointer check. Timers are stopped at Flush,
  and `AfterFunc` starts no goroutine that waits.
- Flush runs at connection end (relayclient), `stopMail` (receiver and RejectAudit share one
  limiter) and `Manager.Close`.
- Content: the reject lines no longer carry the envelope id. `relay_error_frame.message`
  goes through `displaytext.Line` (F9). The `mail_ack_failed` id is a valid mail id.

**Audit chain.** `internal/audit` is not touched, and old rows stay as they are.
`TestAuditInventoryIsComplete` passes, including the new `kinds["unknown"]` guard.
`TestPhase3AuditHasNoContent` passes.

**macOS** (`internal/service/launchd.go`, `cmd/agentnetd/main.go:141`).
- The plist passes `--log-file <home>/agentnetd.log`, and Std* go to `agentnetd.out.log`.
- When `--log-file` is set, slog does not write to stderr, so `out.log` only receives early
  errors and crash reports.
- `rotateOutLog` acts only when stderr is a regular file larger than `MaxSize` and
  `os.SameFile` with `<p.Dir>/agentnetd.out.log`. `p.Dir` is the absolute `--home`, the same
  directory the plist uses. It renames within the daemon's own directory.
- `os.Rename` does not follow links. A symlinked `out.log` or `.1` moves or replaces the link
  itself, never its target, and a `.1` that is a directory makes the rename fail, which is
  ignored. Nothing is deleted.
- Existing installs: an old plist does not match the name, so nothing is renamed. The
  limiter bounds that install, and re-installing is documented (OD-F14-4 (a)).

**Dedupe set.** At most 4096 keys of `peer\x00id` (≤ ~175 B each), with FIFO eviction.
`seenRing[1:]` plus `append` keeps the backing array bounded after it is reallocated.

## Implementer's deviations

1. **`limit` as a local const: accepted.** At the F13 reconcile, use F13's exported
   constant. F13 must emit `limit` only after step 7, because `auditable` accepts `limit` at
   any step.
2. **`rotateOutLog` via `os.Rename`: accepted, and better than remove + rename.** It is a
   single atomic replace with no delete, and it does not follow links.
3. **Rejects dropped by the 30/min limit do not enter the dedupe set: accepted.** A dropped
   one can be audited later, still at most once per `(peer, id)` per run. Only genuine
   peer-signed envelopes reach this code, and memory is unchanged.
4. **The acceptance test covers steps 2/3/4 only: acceptable.** Step 7 is covered in
   `reject_audit_test.go`. Steps 5 and 6 are not covered anywhere (L2).

## Findings

**L1 (Low): some peer-caused `keys` defects are logged only.**
- Where: `internal/mail/body.go:143`, `internal/mail/audit.go:78`.
- Problem: `ErrAnnouncementExpired` is returned for four checks, not only for
  `!notAfter.After(now)`:
  - `created` more than the skew in the future;
  - `created ≥ not_after`;
  - a lifetime over 30 d.
- Scenario: a paired peer (not the relay) signs a `keys` mail whose announcement has a 60-day
  lifetime or a future `created`. The reject is logged and counted as `bad_keys` in the
  summary, but no `mail.reject` row is written. This is under-audit only; the relay gains
  nothing.
- Fix direction: add a narrower sentinel (for example `errAnnouncementPast`) for the
  `not_after ≤ now` case only, and test for it in `auditable`. Keep `ErrAnnouncementExpired`
  for the team.md callers.

**L2 (Low): test gap for steps 5 and 6.**
- Where: `internal/mail/reject_audit_test.go:60–74`, `internal/daemon/relay_audit_growth_test.go`.
- Problem: no test shows that `malformed` at step 5 or `sender_mismatch` (step 6) writes 0
  rows. `auditable` handles them correctly by step number.
- Fix direction: add both rows to the table in `TestAuditedRejectOncePerEnvelope`.

**L3 (Low): doc drift on `limit`.**
- Where: `Docs/protocol/audit.md:318`.
- Problem: the "audited" row lists steps 8/9/10/12 and `bad_body` but not `limit`, while
  `mail.md:595` and D58 include it.
- Fix direction: add `limit` to the audit.md row.

**L4 (Low): spec test 9 is not done as written.**
- Where: `internal/daemon/e2e_3_9_test.go`.
- Problem: `TestPhase3AuditHasNoContent` does not produce a `relay.reject_summary` row. The
  no-content property is shown instead by `relay_audit_growth_test.go:487–508`, which
  decodes with `DisallowUnknownFields` and checks that no peer appears.
- Fix direction: either accept this as equivalent coverage or add the action to the Phase 3
  test.

**Notes (Info, no change needed):**
- `audit.go:123`: a pair is marked seen before `Append`. If `Append` fails, that row is lost
  for the run. A relay cannot cause the failure.
- `main.go:90`: `rotateOutLog` runs before the already-running check. This is harmless,
  because launchd runs one instance.
- `mail_reject_suppressed` is only written at the next audited reject after the window
  (existing behaviour, peer-driven only).

## Tests run (worktree, Windows, no `-race`: no gcc)

- `go vet` on the changed packages: clean.
- `go test -count=1` on lograte, mail, session, relayclient, service, cmd/agentnetd and
  audit: all ok.
- `go test ./internal/daemon -run 'RelayDriven|RejectSummary|AuditInventory|Phase3Audit'`:
  all pass except `TestAuditInventoryDevices`. `TestPhase2AuditHasNoContent` (run
  separately) also fails. Both fail on the local ACL error `writable_by_others "C:\"`, which
  is on the known local list and unrelated to this ticket.

## Files created

- `Docs/review/80-r55-f14-security.md` (this file). No other files, and no test files.
