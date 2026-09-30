# 78: R55-F18 security review

Reviewer: R55-F18sec-Opus (`claude-opus-5-5`), 2026-09-30. Branch `p4/r55-f18`, ticket commit
`5118cf0` (rebased on main `b8bed43`, F12 merged). Spec: [69-r55-f18-spec.md](69-r55-f18-spec.md)
incl. Review 69b. Report only. One probe test was added (listed below).

## Verdict

**Pass, no Critical/High/Medium findings.** The ticket closes R55-022 and R55-029 as the spec
defines them. B's content can no longer reach A's request record while a result is under review
or after A's close, in either order. An agent on a helper can no longer submit, complete or
cancel a run session. Two Low residuals and two Info items remain. S1 is spec-accepted Phase 1
compatibility, but it can be narrowed cheaply.

## Findings

| # | Sev | Where | Finding |
|---|---|---|---|
| S1 | Low | `internal/worksession/hooks.go:156` (open branch of `EarlyComplete`) | **Unreviewed content still reaches A's record through the open-state early complete, even from a B that is provably Phase 2 in that session.** Scenario (probe, confirmed): B sends `ws.result` R1. A requests changes, so round 2 is `open`. The modified B sends `request.complete{R-evil}`. A's session closes `cancelled`, but A's request record is `completed` with R-evil. A later `request.complete` would overwrite it with "session cancelled" (mismatch audit), but B need not send one. The spec treats this as the intended Phase 1 path (69b "Checked"). The session outcome says `cancelled`, and the quarantine rule still applies. But `request show` displays R-evil as the result, which is exactly the R55-022 symptom. **Fix direction:** on the open branch, if the session shows Phase 2 evidence (`row.round > 1`, or A has ever received a `ws.result` in this session), apply `drop` with A's view ("session cancelled", no result) and audit `early_complete`. Keep B's content only for a round-1 session that never saw a `ws.result`. Owner call: this narrows OD-F18-3/8 scope. |
| S2 | Low (availability) | `internal/worksession/submit.go:59,70`, `internal/daemon/device_run.go:543` | **A run session whose `ws.result` ends `failed` (`rejected`) or `expired` is stuck on B.** The runner cannot resubmit (one result per round). No agent may `ws_cancel` (runner-owned). The runner does not watch outbox final states. Only A's own cancel ends it, and A sees an `open` session with no result and no hint. No integrity impact. **Fix direction:** in `outbox.OnFinal`, for a `ws.result` row on a `runner = 1` session ending `failed`/`expired`, have the runner submit `ws.cancel` (`ByRunner`). Alternatively, document it in device.md §Running as "A must cancel". |
| S3 | Info | `internal/worksession/mirror.go:316`, `Docs/protocol/work-session.md:697` | The run-session auto-cancel audits `ws.cancel` with actor `daemon`. The §Audit table lists `ws.cancel` as actor `cli` only. Content-free and correct in substance. Update the table to `cli` / `daemon`. |
| S4 | Info | `internal/mail/receiver.go:162,175` | Mixed version (accepted by OD-F18-1): a pre-F18 sender refuses a `rejected` ack as malformed and resends the bad mail with backoff until its 7-day expiry. Each resend is re-acked through `seenDupBad` (no Apply, no audit), so the cost is bounded. Nothing to change before the first release. |

## Checked, no finding

- **Forged or unreviewed result into A's record (R55-022).** Results after close: the closed branch overrides with `requesterView` and audits `result_mismatch` only via `AfterApplied` (`seq` advanced), so a stale complete stores and audits nothing (test 15). Result during review: `awaiting_result`/`quarantined` → `drop` + `Withhold`. A's later close (`closeSessionTx` requester, `Discard`) runs `SetOutContentTx`. It only touches a `completed` `out` row, and never `state`/`seq`. Every requester close path goes through these two (grep of `UPDATE work_sessions SET state`). The debate path is unchanged. An honest B's closing complete equals `requesterView` (same D14 projection, `mirror.go:243–256`), so there is no false mismatch (`TestOneResultPerRound`). A late decline or cancelled after close only changes `state` and leaves content alone. That is pre-existing "higher seq wins" behaviour, not a content forgery.
- **Run session forgery (R55-029).** `MarkRunnerTx` runs in the same receive tx as `AutoAcceptInTx`, which opens the session (`lifecycle.go:212`), so there is no window. The refusal is store-level (`submitResultTx` before the state checks, `SubmitCancel`). `CompleteShorthand` and `AnswerQuestion` are `ByAgent`. `request_complete` maps `worksession.BadStateError` to `bad_state` (`request_lifecycle.go:240`). No other IPC method writes a worker result (the `ws_*`/`request_*` handler list was checked). The runner path is `ByRunner` in `execute`, `recoverAfterRestart` and `cancel`. The e2e `TestHelperRunSessionOwnedByRunner` passes on ubuntu/macOS CI and fails locally only on the `C:\` ACL. Pre-upgrade sessions stay `runner = 0` (OD-F18-9 (a), accepted).
- **Ack `rejected`.** It is always sent alone, one id per ack. `finish` requires `to_key == from` and a non-final row, and moves it only to `failed`. So a peer cannot fake delivery, and a relay cannot forge a sealed ack. Rows are deleted only by the retention purge, the same as before. A peer suppressing the fallback gains nothing beyond acking `ids` or staying silent. `checkAckBody`'s two-member refusal only makes the whole ack malformed (the sender resends). That is harmless.
- **Round-2 auto-cancel.** It runs in the mail tx and only on the applied branch (`mirror.go:200`). A duplicate or echo (`seq ≤ row.seq`) sends nothing. Output is one `ws.cancel` per new A `ws.state`, so there is no amplification and no loop (A applies the cancel on `open` and closes).
- **Late decline or cancelled.** `SessionEndedTx` runs only when the mirror applied the mail, and only on an `open` requester row. Debates go through `EarlyCompleteTx`, and `EndedTx` stays `invited`-only.
- **Fallback runner.** `wg.Add` is taken under the mutex only while not `closed`. `stop` sets `closed`, then cancels, then waits. The defer is registered after the `st.Close` defer and before the helper/relay/outbox defers, so it runs after the triggers stop and before the store closes. Checks do not re-enter `start`. The debate skip is present in both reads and in the peer SELECT. The rescan uses the same `created ≥ opened` filter.
- **Migration 22 / rewinds.** Both replaying rewinds (`store_test.go:250`, `audit/chain_test.go:69`) drop `runner`. `migration22_test.go` covers upgrade. The store/audit packages pass.
- **Logs and audit.** The new logs are `session`/`peer`/error only (`logPhase1Error`, `requesterView`). The new audit rows are content-free.

## Tests run (local, Windows)

- `go test ./internal/{store,mail,request,worksession,audit,debate}/`: all ok.
- `go test ./internal/daemon/ -run 'Session|Ws|Fallback|Integrity|Outbox'`: ok, except `TestHelperRunSessionOwnedByRunner`, which fails on the `C:\` ACL (known). `-run TestAuditInventory`: `TestAuditInventory` ok, and `TestAuditInventoryDevices` fails on the same ACL.
- `-race` is CI only (PR #30). See the report message for its status.

## Files created

- `Docs/review/78-r55-f18-security.md` (this file).
- `internal/worksession/zz_sec78_test.go`: the probe for S1. It passes and logs the confirmation. Delete it, or invert it into a regression test if S1 is fixed.
