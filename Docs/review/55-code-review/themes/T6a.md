# T6a: Spec-versus-code drift, Phases 0–1

Reviewer: TH-T6a · model claude-opus-5-5 · elapsed 247 min (wall clock, includes a usage-limit pause of unknown length) · commit 6cc26a7

Method note: the per-spec inventories were built by seven read-only helper passes (ipc; agent-card + envelope;
pairing + session; mail; team + presence; request; notify), each over the spec, its `Docs/cli/` pages and the
code. Every candidate finding below was re-read by me at the cited lines; T6a-01 is confirmed by a test.

## Summary

Critical 0 · High 0 · Medium 2 · Low 10 · Info 12

About 750 normative statements from `ipc.md`, `agent-card.md`, `envelope.md`, `pairing.md`, `session.md`,
`mail.md`, `team.md`, `presence.md`, `request.md`, `notify.md` and their CLI pages were traced to code.
The Phase 0–1 code follows its specs closely: the pairing v2 KDF/tag/fingerprint vectors pass, the mail receive
steps 1–12 run in the spec's order with the spec's reasons, the request state machine matches its table
exhaustively, the team roster validation order matches §Validation, every relay error code is in the envelope.md
table, and every audit action/detail in the ten specs is emitted as written (content-free). No drift lets the
relay forge, a stranger bypass auth, or content reach the audit log.

Two Mediums. **T6a-01**: the review-43 M7 fix ("results encoded with HTML escaping off", ipc.md §Framing) has no
effect on the wire, because `serveConn` re-encodes the whole `Response` with a default `json.Encoder`, which
re-escapes the `json.RawMessage` result; a `<`-heavy peer transcript therefore still grows 6× and can push
`debate_show`/`decision_show` past the 1 MiB line (confirmed by test; C16 listed this as "checked and fine").
**T6a-02**: the relay validates only routing fields, so it queues envelopes whose `payload` is not base64 that the
recipient can never parse and therefore never acks; such junk occupies the victim's queue caps for the full 7-day
TTL instead of until the next connect (extends O-015). The Lows are IPC shape drift (`presence_set`,
param-less `request_list`/`inbox_list`), webhook age/title rules, the v1 per-prefix pairing limit, misreported trust
on re-pair, and silent mail `Apply` errors. Several open-finding rows turn out fixed or stale (O-068, O-115, O-035).

## Questions (04-themes.md §T6)

1. **Drift table per spec** — see "Drift tables" below. Every normative statement checked is listed or
   summarised; all differences are rows with a finding id or a known id.
2. **Code behaviour with no spec** — see "Undocumented behaviour" below (IPC fields, error codes, log events,
   audit actions, limits).
3. **IPC method list cross-check** (`00-index.md` §3.a vs `ipc.md` vs registrations): 73 methods are registered
   (`grep '\.Handle("' internal/daemon`). Every Phase 0–1 method in `ipc.md` is registered, and every registered
   Phase 0–1 method is in `ipc.md`. `ws_discard` (`internal/daemon/session.go:457`) is registered but missing from
   `ipc.md`'s Phase 2 list (it is in `work-session.md:466`; T6a-I10). `00-index.md` §3.a omits `notify_test`
   (`internal/daemon/notify.go:182`): an index error, not code drift. `approval_confirm` is correctly absent (2.2d).
4. **Chunk "Spec" fields in this area** (used as known drift, not re-reported): C04-01/02/03/04, C05-01..06,
   C06-01..04, C07-01..04, C08-01..07, C16-01..08, C17-01..03, C18-01..03, C19-01..05, C20-01..05, C27-01..05,
   C28-01..05, and the relay-side C01-01/02 (envelope.md §Forwarding). All re-confirmed as still present at
   6cc26a7; see "Assumptions resolved".

## Findings

### T6a-01 · Medium · confirmed-test
- **Where:** `internal/ipc/ipc.go:141` (`enc := json.NewEncoder(c)`, HTML escaping left on) and `:153`
  (`enc.Encode(resp)`); `marshalResult` at `:197-205` is correct but its output is re-escaped.
- **What goes wrong:** `Response.Result` is a `json.RawMessage`. `encoding/json` re-compacts a Marshaler's output
  with the *outer* encoder's `escapeHTML` setting, so every `<`, `>`, `&` in a result is written as a six-byte
  escape anyway. The spec's M7 guarantee does not hold on the wire.
- **Scenario:** paired peer → in a debate, fills its entries (within the per-entry and per-debate limits, whose
  canonical form fits the 1 MiB line) with `<` characters. The local agent or user runs `agentnet debates show` /
  `decisions show` / `wait`: the `debate_show`/`decision_show` response is ~6× larger than the canonical form,
  exceeds `maxLine` (`ipc.go:28`), and the client's `readLine` (`:218`) fails with "line too long". The debate or
  Decision cannot be viewed through IPC. Test: 1000 `<` in a result → `6042 bytes, 0 raw '<'` on the wire.
- **Spec:** `ipc.md` §Framing (3.1b, review 43 M7): "the server encodes results with HTML escaping **off**
  (`json.Encoder.SetEscapeHTML(false)`)"; `debate.md` §IPC "Size".
- **Fix direction:** `enc.SetEscapeHTML(false)` on the connection encoder (and add an IPC round-trip size test).
- **Related:** review 43 M7 (reopened in effect), C16 "Checked and fine" entry for `ipc.go:197-205` is wrong,
  O-178 (list sizes), C19-01.

### T6a-02 · Medium · confirmed-read
- **Where:** relay: `internal/envelope/envelope.go:46-53`, `:117-124` (`Header`/`ParseHeader` ignore `payload`),
  `internal/relay/relay.go:755-796` (forward/queue); client: `internal/relayclient/relayclient.go:460-463`
  (`Parse` fails → `return` before `c.ack` at `:483`).
- **What goes wrong:** the relay accepts and queues an envelope whose `payload` is not a base64 string (or is a
  number/object) and answers `queued`. The recipient's `envelope.Parse` rejects it, logs a Warn and never acks, so
  the row stays in the relay queue until the 7-day TTL and is re-delivered on every reconnect.
- **Scenario:** stranger (any authenticated key; four keys are enough, O-015) → while the victim is offline, sends
  1000 frames with valid routing fields and `"payload": 1` to the victim. They fill the victim's 1000-row /
  32 MiB cap. With valid junk the victim's acks clear it at the next connect; with this junk nothing clears it, so
  every legitimate sender gets `queue_full` for the victim for 7 days, the victim re-downloads the junk and writes
  1000 Warn log lines at every reconnect (C16-02 log growth). On the hosted relay with accounts on, only bound keys
  can send, which narrows the actor.
- **Spec:** `envelope.md` §Envelope ("`payload` … standard base64"), §Forwarding step 1 (`bad_envelope` for "a
  missing or malformed field"); §Queue ("delivered … until acked").
- **Fix direction:** relay rejects a non-string/non-base64 `payload` with `bad_envelope`, or the client acks
  frames it cannot parse (it already cannot use them).
- **Related:** escalates O-015 (new code path: junk survives the victim's reconnect; 7 d instead of "until the next
  connect"), O-053, C16-02.

### T6a-03 · Low · confirmed-read
- **Where:** `internal/daemon/presence.go:22-27` (params), `:34-93`.
- **What goes wrong:** `presence_set` takes `{"visible": bool, "invisible": bool, "only_team": "<ref>",
  "human": "on"|"off"}`, not the specified `{"mode", "team"?, "human_share"?}`. Unknown fields are ignored, so a
  spec-shaped call is a successful no-op.
- **Scenario:** local agent/adapter written from ipc.md sends `{"mode":"invisible"}` → `ok: true`, mode unchanged
  (still `visible`); the user believes they are hidden while heartbeats continue. The returned view does show the
  real mode, which limits the harm.
- **Spec:** `ipc.md:297`.
- **Fix direction:** align spec and code; refuse unknown fields.
- **Related:** C08-03.

### T6a-04 · Low · confirmed-read
- **Where:** `internal/daemon/request_lifecycle.go:458` (`request_list`), `:492` (`inbox_list`).
- **What goes wrong:** both `json.Unmarshal(params, &p)` without the `len(params) > 0` guard the other handlers
  have (`team.go:190`, `presence.go:36`), so a request with no `params` gets `bad_request` "malformed params".
- **Scenario:** a spec-following client calls `{"method":"inbox_list"}` (params optional per ipc.md §Request and
  all fields optional) → error. The CLI always sends an object.
- **Spec:** `ipc.md` §Request ("`params` optional"), §Requests `request_list`/`inbox_list`.
- **Fix direction:** add the guard.
- **Related:** none.

### T6a-05 · Low · confirmed-read
- **Where:** `internal/notify/webhook.go:169-213` (`attempt` never checks age), `:240` (`tooOld` only in `finish`).
- **What goes wrong:** a pending webhook row older than 24 h is still POSTed; the 24 h rule is applied only after
  a retryable failure.
- **Scenario:** a delivery is processed but its response is lost; the daemon is then down > 24 h. At restart the row
  is re-sent with the same id; a receiver that follows notify.md ("remembering ids for 24 h covers the whole retry
  schedule") has forgotten it and processes the event twice. Also a row enqueued in the race with `--webhook off`
  is sent days later once a webhook is set again.
- **Spec:** `notify.md` §Delivery ("older than 24 h → `failed`"; id memory of 24 h).
- **Fix direction:** check `now − created > 24h` before the POST.
- **Related:** O-117.

### T6a-06 · Low · confirmed-read
- **Where:** `internal/notify/webhook.go:117-132` (body, incl. title and format, built at enqueue) vs `:171` (URL and
  settings re-read per attempt).
- **What goes wrong:** `--webhook-title off` and `--format` changes do not apply to rows already queued, while the
  URL change does.
- **Scenario:** user switches `--webhook-title off` → pending retries keep sending peer titles for up to ~9 h.
  User moves from a generic receiver to a Slack URL with `--format slack` → pending generic bodies go to Slack
  unescaped, so a paired peer's title `<!channel>` pings the channel.
- **Spec:** `notify.md` §Configuration (title only with `--webhook-title on`), §Delivery (URL re-read). The spec's
  own design produces the gap; it may be a spec fix.
- **Fix direction:** render the body per attempt from stored fields, or fail pending rows on a format/title change.
- **Related:** C27-04.

### T6a-07 · Low · confirmed-read
- **Where:** `internal/relay/pairing.go:238-253`.
- **What goes wrong:** the per-key and per-prefix `pair_new` limiters are checked and charged only inside
  `if lookup != ""` (v2); v1 `pair_new` is never counted.
- **Scenario:** on a relay with `--allow-pairing-v1` (off by default on public relays), a stranger on one /24 issues
  v1 codes without the 20/10 min prefix cap; only the 50-outstanding and 10000-total caps apply, and new+cancel churn
  is unbounded. DoS only.
- **Spec:** `pairing.md:198` "`pair_new` (v1 and v2, any outcome) per prefix | 20 / 10 min".
- **Fix direction:** charge both versions, or change the spec to v2 only.
- **Related:** O-006, O-008.

### T6a-08 · Low · confirmed-read
- **Where:** `internal/peers/pairing.go:703-715` (`store` returns the *requested* trust and `now`), with the
  never-lower SQL at `internal/peers/store.go:109-118`; audit at `pairing.go:1035-1039`.
- **What goes wrong:** on a re-pair the DB keeps the higher trust and the original `paired_at`, but the pairing
  status, `agentnet pair` output and the `pair.complete` audit row report the requested trust and a new date.
- **Scenario:** user re-pairs a peer already `fingerprint`-verified → CLI prints "trust: code" and the audit row
  records `trust: code`; the user may think trust was lowered and the audit misstates the stored state.
- **Spec:** `pairing.md` §Storage and trust ("never lowers trust … keeps `paired_at`"), §Logging (`pair.complete
  {…, trust}`).
- **Fix direction:** re-read the row after `AddTrusted`.
- **Related:** none.

### T6a-09 · Low · confirmed-read
- **Where:** `internal/daemon/mail.go:184` (`_ = rcv.Handle(mctx, e) // rejections are audited by the receiver`);
  `internal/mail/receiver.go:152-155`, `:188-235` return begin/apply/store/commit errors unlogged.
- **What goes wrong:** a non-`ErrBadBody` `Apply` error (or DB error) is neither a `mail.reject` nor logged; the
  mail is not acked, so the sender resends at 1 m/5 m/30 m/6 h until `expired`.
- **Scenario:** paired peer sends a body that makes a kind's `Apply` fail persistently with a plain error (C24-01 is
  one such case) → a week of silent resends; the local owner has no trace, the sender sees only `expired`.
- **Spec:** `mail.md` §Receiving ("the first failure … audited as `mail.reject`") covers verification failures;
  the spec is silent on post-verification errors, which the code drops without a log line.
- **Fix direction:** log (content-free) the `Handle` error with `event=mail_error`.
- **Related:** C24-01, O-059, O-057.

### T6a-10 · Low · confirmed-read
- **Where:** `internal/daemon/notify.go:74-86`, `:114-124` (peer-name fallback to `info.Peer`).
- **What goes wrong:** when `peers.List` fails or the peer was removed between commit and the `After` hook, the
  notification's peer name is the identity public key (cleaned to 40 code points), sent to the desktop and to the
  webhook (`peer.name`, `text`).
- **Scenario:** request arrives, then the user runs `peers remove` before the After hook → a webhook to Slack
  carries 39 characters of the peer's public key.
- **Spec:** `notify.md` §Payload / §Privacy summary ("Never included: … public keys").
- **Fix direction:** fall back to "unknown peer" or the fingerprint.
- **Related:** O-108.

### T6a-11 · Low · confirmed-read
- **Where:** `internal/presence/sender.go:120-131`, `:201-204`; used by `internal/daemon/status.go:55`, `:100`.
- **What goes wrong:** with `human_share` off, `HumanPresent` returns the shared value (2 → `null`), so local
  `status` no longer shows the user's own detected value.
- **Scenario:** latent today (idle detection is not wired, C08-01); once C08-01 is fixed, a user with
  `presence --human off` sees `human_present: null` for themselves.
- **Spec:** `presence.md` §Human sharing ("Local `status` still shows the user their own detected value").
- **Fix direction:** a local-only accessor that ignores the share flag.
- **Related:** C08-01, C08-06.

### T6a-12 · Low · confirmed-read
- **Where:** `cmd/agentnet/request.go:234-263` (`parseArtifactFlag`, used by `request`, `inbox complete`, `session`);
  strict `internal/request/artifact_spec.go:18-89` (`ParseArtifactSpec`) is called only by tests.
- **What goes wrong:** the JSON form of `--artifact` uses `json.Unmarshal`: unknown keys are dropped and keys match
  case-insensitively; the pair form lets a repeated key win silently.
- **Scenario:** the user's own agent writes `--artifact '{"url":"https://…","comit":"1a2b3c4"}'` → exit 0, request
  queued without the commit; the recipient reviews a different revision.
- **Spec:** `Docs/cli/request.md` (`--artifact` keys url/branch/commit/path); request.md §Artifacts (strict).
- **Fix direction:** use `ParseArtifactSpec`.
- **Related:** C17-01.

### T6a-13 · Info · confirmed-read — IPC error mapping and doc drift in `ipc.md`
- `mail_submit` maps every other `Submit` error (keystore load, seal, DB insert) to `bad_request` with the raw
  `err.Error()` text (`internal/daemon/outbox.go:108-109`); ipc.md reserves `bad_request` for bad params and keeps
  detail out of errors. Local, same user.
- `shutdown` fails if the `daemon.stop_requested` audit fails (`internal/daemon/daemon.go:613-615`), vs ipc.md:128
  "always succeeds". Failing closed is arguably right; fix the text.
- `team_list` is `ORDER BY created, id` (`internal/team/store.go:198`), ipc.md:281 says "sorted by name, then id".
- `status --team` self row never sets `agent_last_active` (`internal/daemon/status.go:96-101`), ipc.md:106,116.
- ipc.md:225 reserved-prefix list lacks `debate.`/`decision.` (`internal/daemon/outbox.go:25`); request_submit
  errors lack `team_inactive` (`request.go:292`) and `unpaired`→`bad_request` (`:359`); `status` fields `approval`,
  `git`, `relay` are not in ipc.md §status.

### T6a-14 · Info · confirmed-read — `hasControl` refuses U+FFFD
- `internal/request/validate.go:342-346` also refuses U+FFFD in every request/result text field; request.md's
  convention defines control characters as U+0000–001F, U+007F only. A test log with "�" is refused as
  `bad_request`. Consistent on both sides, so doc drift (C18 lead).

### T6a-15 · Info · confirmed-read — v1 re-pair clears `introduced_by`
- `internal/peers/store.go:113` clears `introduced_by` on every `AddTrusted`, including v1 (`trust=relay`);
  team.md §Trust says only a v2 pairing or `peers verify` clears it. A team-introduced peer re-paired over v1 keeps
  trust `team` (never lowered), is no longer GC'd and no longer card-refreshed. Same key, no impersonation.
  Related O-041 (the mirror case).

### T6a-16 · Info · confirmed-read — mail.md internal inconsistencies and gaps
- §Envelope says `ts` is the clock "at this (re)send"; §Sending and backoff says resends use the stored frame
  unchanged; code follows the latter (`internal/mail/outbox.go:446-457`).
- A mailbox key created while the daemon runs without a relay is never pushed (`internal/daemon/daemon.go:577-583`
  runs `rot.Run` without `OnRotate`); peers heal via key-miss at 21 d. §Lifecycle says the job pushes.
- §Kinds omits the `debate.*` kinds (`internal/daemon/mail.go:271-277`); `mail_inbox.signed` is blank for
  `Withhold` (`receiver.go:212-220`), documented only in work-session.md.

### T6a-17 · Info · confirmed-read — pairing/session doc drift
- `relay_unavailable` is a pairing failure code (`internal/peers/pairing.go:59,524,606,860`) not listed in
  pairing.md §Failure codes; `store_error` reused for code generation (`:582`).
- Attempts ended by TTL, relay error or another attempt's completion write no `pair.attempt_fail`
  (`pairing.go:1005-1010`).
- `session_reject_suppressed` count is only flushed on the next reject (`internal/session/session.go:896-901`);
  a burst that stops is never logged (same pattern as O-012).
- `pairing.md` §Storage lists trust values without `team` (`peers/store.go:24-29`; covered by team.md).

### T6a-18 · Info · confirmed-read — team/presence doc drift
- `team.Store.Delete` runs no GC (`internal/team/store.go:385-411`), team.md Ops Delete says "GC" (no-op in
  practice: members joined via v2).
- team.md is inconsistent about `team.leave_ignored` detail (`{team?, peer, reason}` in §Audit vs `{team, peer}` in
  §team.leave); code (`internal/team/kinds.go:575`) follows the latter.
- The relay's 8 KiB ephemeral cap (`internal/relay/ephemeral.go:12,86`) is not in presence.md §Relay.
- `internal/presence/body.go:237` error text says "0-1023" for the pad; the real bound is computed `maxPad`.

### T6a-19 · Info · confirmed-read — request.md doc drift
- `request.md` §Tables shows the `type` CHECK as review/task/question; migration 19 adds `debate`
  (`internal/store/store.go:391`).
- A duplicate submit of a downgraded request returns `urgency_declared` without `urgency_note`
  (`internal/request/submit.go:134`).
- Sender budget (step 6) runs before Validate (step 5) (`submit.go:86` vs `:105`): harmless, no row stored.
- The tombstone lookup precedes the 30-day check (`internal/request/receive.go:188-199`): a > 30 d request with a
  tombstone is stored `cancelled` rather than refused (bounded; C17-03 / O-102).
- `request list --state bogus` → empty list, exit 0 (`cmd/agentnet/request_query.go:136`), cli says a bad flag
  value is exit 2.

### T6a-20 · Info · confirmed-read — notify.md doc drift
- §Secret says keychain account `webhook`; code uses `webhook-<dir hash>` (`internal/daemon/daemon.go:712`):
  **O-115 is fixed in code**, the spec is stale.
- `request.state` in the payload is the work-session state for `session.*` events and `""` for debate events
  (`internal/daemon/quarantine.go:46,52-53`, `notify.go:133-136`).
- §Privacy summary says the desktop shows state and team name; the desktop templates (`internal/notify/trigger.go:199-222`) show neither.
- `notify_test` can block ~3.2 s on `Show` (`internal/daemon/notify.go:201`), vs ipc.md:239 "returns within 2 s".
- The 6 h Retry-After cap also clips the jittered 6 h base delay (`internal/notify/queue.go:235-237`).

### T6a-21 · Info · confirmed-read — envelope.md relay logging and account-frame codes
- Connection/route/queue events (`relay.go:388,789,821,919`) are Info level; the relay logs at Warn unless
  `--verbose` (`cmd/relay/main.go:189-193`), so envelope.md §Logging's events are absent by default.
- `bad_envelope`/`internal` are used for account control frames (`internal/relay/accounts.go:331,337,361,388`), and
  an ephemeral frame from an ineligible sender gets `account_required` (`accounts.go:290-301`) against "dropped
  silently" (O-187 area; accounts off by default).
- Every `queue_full` says "recipient's offline queue is full" even when the sender or relay-wide cap refused
  (`relay.go:810`).

### T6a-22 · Info · confirmed-read — agent-card schema leniency
- Schema decode is case-insensitive (`internal/agentcard/agentcard.go:206-209`): a card with `"NAME"` passes the
  daemon, while `tools/verifycard` reads `card["name"]` exactly, so the two verifiers can show different names for
  the same signed card. A skill with no `description` key is accepted (`:205-211`) although agent-card.md says all
  fields are required. Only the key holder can build such a card.
- agent-card.md §IPC omits `fingerprint` from the `identity` result (ipc.md and identity.md have it).

### T6a-23 · Info · confirmed-read — status/CLI forward references
- `Docs/cli/status.md` names an `account` field "(4.2c)"; not implemented (`internal/daemon/daemon.go:44-66`).
- `agentnetd` exits 2 on `ErrApprovalRequiresTerminal` (`cmd/agentnetd/main.go:124-126`), not in agentnetd.md.

### T6a-24 · Info · confirmed-read — open-findings ledger corrections
- **O-068** (presence padding leaks team count): appears fixed; every presence plaintext is padded to one fixed
  worst-case size (`internal/presence/body.go:93-141`) as presence.md §Body now specifies.
- **O-115** (keychain account shared across profiles): fixed in code (T6a-20).
- **O-035** (key/row crash ordering): fixed per C05 (key saved before row), residual C05-06.
- **O-108** scope is larger than recorded: also a team `Get`, two `ShowKey` calls (`internal/daemon/notify.go:88-92,126-131`) and `PeekTypeTitle` (`quarantine.go:21`) run synchronously in the After hook.

## Drift tables

Legend: ✓ matches · **D** differs (finding) · **N** not implemented · K = known id. Rows marked ✓ in bulk list the
code range that implements a whole group of statements.

### ipc.md and Docs/cli/{status,stop,identity,agentnetd}.md

| Spec § | Statement | Code | Result |
|---|---|---|---|
| Endpoint | socket `<dir>/agentnetd.sock` 0600, dir 0700; pipe name SHA-256(dir)[0:8]; DACL user SID | `paths.go:57-74`; `transport_unix.go:20-30`; `transport_windows.go:70-81` | ✓ (K C16-01, C16-05, C16-08) |
| Endpoint | second daemon refuses; stale socket removed | `transport_unix.go:20-24`; `transport_windows.go:79-81` | ✓ (K C28-03) |
| Framing | one JSON object per line; 1 MiB; 30 s idle | `ipc.go:28-29,142-156,218` | ✓ |
| Framing | results with HTML escaping off | `ipc.go:141,153,197-205` | **D T6a-01** |
| Request/Response | `id` echoed; `method` required; `params` optional; error shape | `ipc.go:33-53,161-171` | ✓ except **D T6a-04** |
| Errors | `bad_request`/`unknown_method`/`internal` (no detail) | `ipc.go:22-23,170-186` | ✓ except `mail_submit` **T6a-13** |
| status | pid, started_at, uptime, version, outbox, presence, relay values | `daemon.go:44-80,649-653`; `status.go:16-65` | ✓ |
| status team | member fields, order, times, self row | `status.go:96-141` | ✓ except self `agent_last_active` **T6a-13** |
| shutdown | `{ok:true}`, `daemon.stop_requested`, respond before teardown | `daemon.go:612-619` | ✓ / "always succeeds" **T6a-13**; stop semantics K C28-02 |
| Audit | `daemon.start/stop {pid,version}`, `peer.verify*`/`peer.remove`, `identity.create` | `daemon.go:134-137,289-299,684-688`; `trust.go:42-116` | ✓ |
| identity | `{card, signature, key_backend, fingerprint}`, no private key | `daemon.go:141-147,622-629` | ✓ |
| pair_* | 1 s wait, 10 min expiry, v1 flag, status fields, setup errors | `daemon/pairing.go:14-99`; `peers/pairing.go:66-112,277-309` | ✓ |
| peers* | fields, constant-time verify, errors | `peers/store.go:35-48`; `trust.go:50-120` | ✓ |
| ping* | `@`, 1 s wait, 10 s timeout, status fields, errors | `ping.go:21-145`; `session.go:68-69,131-141` | ✓ |
| mail_submit | params, `{id, state}`, owned kinds refused, errors | `outbox.go:25-112` | ✓ / prefixes & error map **T6a-13**; K O-157 |
| Agent activity | every dispatched call, before handler | `ipc.go:173-175` | ✓ |
| Teams | 9 methods, summary/ref shapes, codes, name regex, 32 cap | `daemon/team.go:19-372`; `team/team.go:23,35` | ✓ / `team_list` order **T6a-13**; K O-089 |
| presence_get/set | shapes | `daemon/presence.go:13-107` | get ✓ / set **D T6a-03** |
| Requests | submit params/defaults/errors; view fields; list omits `output`; show lookup; lifecycle results/errors | `request.go:35-366`; `request_lifecycle.go:20-509` | ✓ (K O-105, O-106, C18-02, C19-01, C19-04) |
| Notifications | notify_get/set/test shapes, errors | `daemon/notify.go:18-58,143-257` | ✓ (K O-070, O-111; time **T6a-20**) |
| CLI status/stop/identity | flags, exit codes 0/1/2/3, human/JSON shapes, 2 s budget | `cmd/agentnet/main.go:24-327`; `stop.go:77-129`; `identity.go:20-96` | ✓ (K C28-01, C28-02; `account` **T6a-23**) |
| agentnetd.md | `run`, flags, listening line, ws:// refusal, insecure env, already-running exit 3, log/CA exits | `cmd/agentnetd/main.go:40-143`; `relayclient.go:407-421` | ✓ (**T6a-23**) |

### envelope.md

| Spec § | Statement | Code | Result |
|---|---|---|---|
| Frames | `op` = control; binary = protocol error; `op:null` → envelope | `frames.go:188-203`; `relay.go:609,674-677`; `wire.go:21` | ✓ |
| Envelope | key 43-char strict base64url; `from` = auth key; `team`/`type`/`id` charsets & lengths; `ts` RFC 3339 | `envelope.go:31,56-76,130-160`; `relay.go:760-763` | ✓ |
| Envelope | `payload` standard base64 | relay `envelope.go:46-53,117-124` (not checked) | **D T6a-02** |
| Payload | opaque, byte-for-byte, never logged | `relay.go:785,789,809,821`; `queue.go:310` | ✓ |
| Size | 1 MiB, 1009; pre-auth 4 KiB | `envelope.go:16,19`; `relay.go:474,508`; `relayclient.go:249` | ✓ (K O-025 for oversize auth) |
| Connection | `/v1/connect`; ws:// refused non-loopback; no redirects; `/healthz` | `relay.go:446-456`; `relayclient.go:364-366,407-422`; `auth.go:80-98` | ✓ (K O-023) |
| challenge/auth | nonce single-use; 10 s; `auth` list; v2 message & origin canonicalisation; v1 message; downgrade rule; rejection & 1008 | `relay.go:32,584-618`; `auth.go:37-76`; `authv2.go:24-174`; `relayclient.go:305-346` | ✓ (K O-002, O-014, O-021) |
| ready | `public_key`, `features:["ephemeral"]`, accounts member, `min_client` advisory | `accounts.go:529-536`; `relay.go:188-192,284-289`; `relayclient.go:257-330` | ✓ (K C04-04) |
| Ephemeral | half-buffer rule, silent drop, no ack, 600/min | `conn.go:195-202`; `ephemeral.go:11,71-98`; `relayclient.go:469-474` | ✓ (K C01-01, O-009; accounts **T6a-21**) |
| One conn per key | new wins, 1008 `replaced` | `relay.go:638-640`; `conn.go:360-362` | ✓ |
| Forwarding 1–5 | bad_envelope, bad_sender, rate, direct, queue/`queued`, errors never close | `relay.go:734-830`; `limits.go:285-305` | ✓ (K C01-02) |
| Control after auth | allowed ops; 60/min; 3 windows → 1008 | `pairing.go:141-165`; `limits.go:34,310-346` | ✓ (K O-028) |
| Queue | store, `queued` after durable, delivery order, ack by recipient only, dedupe, TTL 7 d, sweep 1 min, caps 1000/32 MiB, 300/8 MiB, 2000/64 MiB, 4 GiB, 1 GiB free | `queue.go:19-36,114-134,264-386`; `relay.go:356-373,654-660,885-921` | ✓ (K O-001, O-003, O-027) |
| Error table | every relay code in the table; `relay_full` 1013; `rate_limited` 1013 | `relay.go:488-818`; `pairing.go:171-423` | ✓ (account uses **T6a-21**) |
| Logging | keys cut to 8, events | `relay.go:972-977`; `limits.go:183-207` | **D (default level) T6a-21** |
| Client | backoff 500 ms→30 s, reset on auth; relay_full/rate_limited fail; ErrNotConnected; ack after OnEnvelope; seen-set 8192, mail bypass | `relayclient.go:31-46,199-236,451-497`; `seen.go:51` | ✓ (K C04-02, C28-01) |

### agent-card.md, Docs/cli/identity.md

| Spec § | Statement | Code | Result |
|---|---|---|---|
| Card | fields, lengths, `version 1`, default name/harness, skills, `created` whole-second Z | `agentcard.go:19-107`; `identity.go:40,152-157,199-207` | ✓ / required `description`, key case **T6a-22**; K C05-03 |
| Canonical 1–6 | no whitespace, UTF-16 order, escapes, ints < 2^53, UTF-8, no duplicates | `canonical.go:17-180` | ✓ (K C05-04) |
| Signature/Verify 1–5 | domain, generic canonical form, schema last | `agentcard.go:22,145-214` | ✓ (K C05-05) |
| Key storage | seed from crypto/rand; keychain service/account; `identity.key` 0600 / protected DACL; refuse wider mode (Unix); fallback recorded | `identity.go:58-73,139-176`; `keychain.go:121-137`; `file.go:212-266`; `perm_*.go` | ✓ (K O-030, C05-01, C05-06) |
| Lifecycle table | 5 rows | `identity.go:111-147` | ✓ (K O-197, C05-02) |
| Audit / IPC | `identity.create {public_key, key_backend, key_generated, keychain_error?}`; IPC result | `identity.go:88-93`; `daemon.go:141-147` | ✓ / IPC doc **T6a-22** |
| cli/identity | usage, exit codes, 8 lines, JSON, verifier output | `cmd/agentnet/identity.go:20-96`; `tools/verifycard/main.go:56-106` | ✓ |

### pairing.md, session.md, Docs/cli/{pair,peers,ping}.md

| Spec § | Statement | Code | Result |
|---|---|---|---|
| Code format | 15 chars, Crockford, `b&31`, normalisation, v1/v2 by length, KDF input | `envelope/pairing.go:19-92`; `pairv2.go:22-79` | ✓ |
| Keys/tags/KDF | salt, Argon2id 3/64 MiB/1/32, T layout, role tags, `hmac.Equal`, wipe | `pairv2.go:27-137`; `pairing.go:945,1012-1017` | ✓ (vectors pass; K O-040, O-034/C06-01) |
| Relay frames | ref ≤ 128; lookup; card ≤ 16 KiB; mbox ≤ 4 KiB; lookup hash; taken/retry ≤ 3; 5 per key; 10/min per key; 10000 total | `relay/pairing.go:18-313`; `pairing.go:563-608` | ✓ (K O-004, O-005, O-006) |
| Per-prefix | `pair_new` v1+v2 20/10 min | `relay/pairing.go:238-253` | **D T6a-07** |
| Per-prefix | failed redeem 10/min; 50 outstanding | `relay/pairing.go:38-41,265-284,367-376` | ✓ |
| pair_code/redeem/peer/cancel | no `code` in v2; relay_v1; exactly one of lookup/code; own lookup; offline/busy not counted; 3rd redemption deletes; cancel no reply | `relay/pairing.go:313-460`; `pairing.go:617-626` | ✓ (accounts replies **T6a-21**) |
| pair.confirm | envelope fields, `pc-` id, strict parse, one per attempt, debug drop, before session check | `pairing.go:865-910`; `pairv2.go:140-202`; `daemon.go:757-758` | ✓ (K O-039) |
| Daemon issuer/redeemer | background K; card/mbox checks; 60 s confirm; order store→tag_I→cancel; single-use hash 24 h; verify→mark→tag_R | `pairing.go:449-979`; `peers/store.go:199-233` | ✓ (K O-092) |
| Attempts/timers | max 3; TTL 10 min; 30 s reply; 1 h query; 16 pending | `pairing.go:66-72,422-430,732-791,1005-1025` | ✓ / TTL-ended attempts not audited **T6a-17** |
| Failure codes | list | `pairing.go:49-59` | **D (Info) T6a-17** |
| Storage/trust | migration 3; values; never lowers; keeps `paired_at`; ≤ 2 mailbox keys | `store/store.go:45-50`; `peers/store.go:24-196` | DB ✓ / reported trust **D T6a-08** |
| Policy D5 | `unverified_peer` on non-loopback | `request.go:192`; `grant.go:415`; `device.go:381` | ✓ (K O-097) |
| Fingerprint | SHA-256, 20 base32, 5×4 display, normalise, constant-time | `envelope/fingerprint.go:11-87`; `trust.go:155-179` | ✓ (vectors pass; K C06-04) |
| v1 | relay default by loopback/public; `pair_v1_disabled`; daemon v1 → `trust=relay` | `relay.go:280`; `cmd/relay/main.go:107-111`; `relay/pairing.go:188-348`; `pairing.go:434-700` | ✓ (K O-033) |
| Logging/audit | relay events & counters; `pair.start/complete/fail/attempt_fail` | `relay/pairing.go:23-137`; `pairing.go:459-1044` | ✓ (K C06-02; trust value T6a-08) |
| session: Noise | name; static key memory-only; binding sig & payload; checks 1–4; prologue | `noise.go:23-191`; `session.go:534-639` | ✓ (K C08-07) |
| session: envelopes/transport | 4 types, 16-byte sid, AD layout, counters, replay | `session.go:26-84,443-484,678-685`; `noise.go:223-244` | ✓ |
| session: lifecycle | queue 16; open after fin; simultaneous start; 4 per peer; 10 s handshake; unknown_session; 10 s ping | `session.go:69-859` | ✓ (K O-031, O-192) |
| session: reject/audit | 7 reasons; detail; 30/min + suppressed count | `session.go:531-915` | ✓ / flush **T6a-17**; K C08-05 |
| CLI pair/peers/ping | flags, 2 s, exit codes, human/JSON output, error codes | `cmd/agentnet/pair.go:22-253`; `peers.go:80-183`; `ping.go:269-290` | ✓ (extra codes and `INTRODUCED BY` column: Undocumented) |

### mail.md, Docs/cli/mail.md

| Spec § | Statement | Code | Result |
|---|---|---|---|
| Mailbox keys | X25519; key_id; keychain/file names; keystore env | `mailbox.go:88-129,326`; `mail/mail.go:69-75` | ✓ |
| Lifecycle | 14 d + 7 d; start + hourly; rotate rule; retire; audit `mailbox.rotate`; push; delete; ≤ 3 live | `mailbox.go:37-456`; `announce.go:15,44` | ✓ / push without relay **T6a-16**; extra "unusable" rotate (K C05-01) |
| Announcement/verification 1–5 | domain, members, times, `bad_keys` | `body.go:81-153`; `announce.go:46-62`; `open.go:133-139` | ✓ (K O-047) |
| Peer storage | ≤ 2, newest first, ignore known/older | `peers/store.go:150-189` | ✓ (K C07-01) |
| Message/sealing | `m-` id, `created`, kind regex, body object, sig domain, 716800 B, HPKE suite, info/aad | `mail.go:30-243`; `open.go:225-235` | ✓ (K O-052, C07-03) |
| Envelope | team `""`, type `mail`, id; `ts` | `outbox.go:253-263` | ✓ / spec self-contradiction **T6a-16** |
| Sending 1–3 | unpaired/no_mailbox_key; seal; one insert `queued`; < 2 s; acks not outboxed | `outbox.go:155-251`; `daemon/outbox.go:104-107` | ✓ |
| Receiving 1–12 | order and reasons; audit ≤ 30/min | `open.go:98-220`; `audit.go:14-73` | ✓ (K O-036, O-050, O-051, O-053) |
| Age / dedupe / ack | 14 d receive age; mail_seen; inbox row; `mail.in`; ack after commit; prune 35 d; ack body; delivered/unsupported | `receiver.go:21-343`; `outbox.go:394-411` | ✓ (K O-038, O-049, O-060, O-188, C07-02, C07-04) |
| Post-verification errors | (spec silent) | `daemon/mail.go:184` | **D T6a-09** |
| Kinds / keys / key-miss 1–4 | registrations; ErrBadBody; keys body; 256 set; 10 min; re-seal | `daemon/mail.go:144-288`; `keys.go:21-233`; `outbox.go:530-583` | ✓ (K O-056, O-059, O-048; debate kinds **T6a-16**) |
| Outbox | 5 states; relayed; expired audit; clear frame; 30 d delete; error mapping; backoff 1m/5m/30m/6h ×U; ready flush; presence-online | `outbox.go:22-583`; `store.go:86-101` | ✓ / presence-online **D K O-062**; K O-054, O-061 |
| Tables / audit | DDL; four actions, content-free | `store/store.go:57-101`; `mailbox.go:377`; `audit.go:69`; `receiver.go:165`; `outbox.go:379` | ✓ |
| cli/mail | debug-only, flags, exits, output | `cmd/agentnet/mail.go:16-102`; `main.go:118-135` | ✓ |

### team.md, presence.md, Docs/cli/{team,presence}.md

| Spec § | Statement | Code | Result |
|---|---|---|---|
| Model | id, name regex, fixed owner, 32 members, epoch rules, single writer | `team/team.go:23-71`; `store.go:217-409`; `kinds.go:105-165` | ✓ (K O-078) |
| Trust | `team` rank 1; never lowered; v2/verify clears `introduced_by` | `peers/store.go:22-116,305-308` | ✓ / v1 also clears **T6a-15** |
| team.roster | body shape; validation 2–4; apply 1–5; after; send | `kinds.go:25-409`; `store.go:513-643` | ✓ (K O-065, O-076, O-079, O-080, O-081) |
| team.join / leave | lookup; invite; full/inactive; epoch; `leave_ignored` | `kinds.go:429-583` | ✓ (K C20-01, O-074; audit detail **T6a-18**) |
| Operations | create, invite, join, remove, rename, leave, delete, owner removed | `store.go:217-508`; `daemon/team.go:133-390`; `invite.go:30-73` | ✓ / leave K C20-02, C20-03; delete GC **T6a-18**; K O-087, O-088, O-090, O-091 |
| Resync, names, introduced, GC, scoping | as specified | `sender.go:258-631`; `peers/store.go:373-469`; `daemon/team.go:434-464` | ✓ (K O-085) |
| Tables / audit / IPC | migrations 8–10; pruning; audit actions; 9 methods | `store/store.go:106-176`; `store.go:682-726` | ✓ / pruning K O-087, C20-05 |
| presence Levels | 30 s; 75 s; agent 5 min; human idle 10 min | `sender.go:22,191-212,440-447`; `presence/store.go:294-296` | ✓ / human **N K C08-01**; K O-075 |
| Envelope/body | `p-` id; 8 members; ranges; epochs ≤ 32; fixed-size pad | `mail/presence.go:11-38`; `body.go:17-238` | ✓ (O-068 fixed, T6a-24) |
| Receiving 1–7 | opener steps; ±10 min; order rule; upsert; online edge; resync; not acked/audited | `receive.go:63-135`; `presence/store.go:192-227`; `relayclient.go:465-474` | ✓ (K O-063, O-064, O-083, O-084, O-086; C08-06) |
| Sending | ephemeral feature; visible set; `team_gone`; jitter; immediate sends; goodbye; newest key | `sender.go:179-596`; `daemon.go:572-576,763-768` | ✓ (K C08-02, C20-02) |
| Relay | features; half-buffer; routing validated; 600/min; debug logs | `accounts.go:530`; `conn.go:97-104`; `ephemeral.go:11-98` | ✓ / 8 KiB cap undocumented **T6a-18**; K O-072 |
| Visibility / human sharing | modes, goodbye/online diff, audits; share default true; local status shows own value | `settings.go:21-104`; `sender.go:120-408` | ✓ / local value **D T6a-11**; K C08-03, C08-04 |
| Idle | 1 s timeout; per-OS; 5 s cache | `idle/*.go`; `sender.go:478` | ✓ / cache **N K C08-01** |
| CLI team/presence | subcommands, output, errors, exclusivity | `cmd/agentnet/team.go:46-386`; `presence.go:47-111` | ✓ |

### request.md, Docs/cli/{request,inbox}.md

| Spec § | Statement | Code | Result |
|---|---|---|---|
| Request object | shape; `v`; id; from/to; team; type; title/brief/urgency/reason/artifacts/deadline/created/context/run; body_hash | `decode.go:10-245`; `validate.go:10-161`; `receive.go:140-171`; `canonical.go:13-85` | ✓ (K C17-01, O-073, O-100) |
| Artifacts / grant | members and limits | `validate.go:13-284` | ✓ |
| Size limits | same Validate both sides; field errors; total 65536 `request_too_large` | `canonical.go:92-101`; `submit.go:105-114`; `d/request.go:353-356` | ✓ / U+FFFD **T6a-14** |
| Brief | template; `--brief-from-file`, UTF-8, CRLF→LF | `cmd/agentnet/request.go:22-25,137-232` | ✓ (IPC callers are not normalised; CLI is; C17 lead: Info, no finding) |
| Submitting 1–8 | resolve; D5; team; idempotency; deadline; budget; one tx; audit; < 2 s | `d/request.go:185-448`; `submit.go:72-203` | ✓ / order **T6a-19**; K O-094, O-095, O-097, O-098 |
| Submit result | fields; `urgency_note` | `d/request.go:118-132,275-281` | ✓ / duplicate note **T6a-19** |
| Receiving 1–5 | strict; duplicate/conflict; 30 d; tombstone; auto-decline order; budgets; insert; audit; notify | `receive.go:139-469` | ✓ / tombstone order **T6a-19**; K C17-02, C17-03, O-066, O-093 |
| Lifecycle kinds / state machine | bodies; full transition table; one tx; audit after commit | `lifecycle.go:15-381`; `mirror.go:91-277`; `cancel.go:267-316` | ✓ (K O-106, C18-01) |
| Result payload (D14) | enum; limits; ≤ 65536; stored canonical; lists omit output | `result.go:12-265`; `request_lifecycle.go:193-509` | ✓ (K O-104, C19-01) |
| Sender mirror 1–5 | strict; orphan audit; seq; no transition check; cancel refused | `mirror.go:93-388` | ✓ (K O-101, C18-01) |
| Cancel / idempotency | sender 1–5; recipient; tombstones 1000; prune 31 d; audit; resend rule; key regex | `cancel.go:22-360`; `submit.go:23` | ✓ / K O-102, O-103, C18-03 |
| Inbox / priority / urgency / audit / tables | order; base priority; notes; audit fields; migrations 11–12; never deleted; `ambiguous_request` | `inbox.go:24-116`; `priority.go:12-40`; `urgency.go:278-307`; `store.go:178-229` | ✓ / migration 19 **T6a-19** |
| CLI request/inbox | flags, exits, outputs | `cmd/agentnet/request.go`, `request_query.go`, `inbox.go` | ✓ / `--artifact` **D T6a-12**; `--state` **T6a-19**; K C19-02, C19-03 |

### notify.md, Docs/cli/notify.md

| Spec § | Statement | Code | Result |
|---|---|---|---|
| Triggers | 10 events at the right points; ignored mirror fires nothing; defaults | `request/receive.go:425-430`; `mirror.go:53-367`; `cancel.go:341-346`; `quarantine.go:44-53`; `settings.go:51-80` | ✓ / debate events K C27-05; K O-108, O-114 |
| Text & sanitising | C0/C1/bidi/2028/2029 → space; collapse; truncate 80/40; templates | `notify/notify.go:13-65`; `trigger.go:15-16,186-257` | ✓ (K O-109) |
| Desktop | 3 s; log; audit hourly; no retry; per-OS argv | `desktop*.go`; `trigger.go:153-182` | ✓ (K C27-03, O-110) |
| Configuration | https / loopback http; 2048; dial-time IP check; proxy; secret; defaults; `--webhook off`; audit | `dial.go:21-184`; `webhook.go:22-82`; `daemon/notify.go:172-305` | ✓ (K O-111, O-118) |
| Secret | service/account/file; rotation | `keychain.go:15`; `daemon.go:708-712`; `webhook.go:73-82,179` | ✓ / account name spec stale **T6a-20** (O-115 fixed); K O-116 |
| Payload | fields; 8 KiB; title opt-in; no brief/keys; Slack/Discord escaping; test event | `payload.go:13-182`; `trigger.go:238-250` | ✓ / frozen title/format **D T6a-06**; key fallback **D T6a-10**; `request.state` **T6a-20** |
| Signature | `v1:ts:id:body`; headers; verification | `sign.go:19-42`; `webhook.go:190-201` | ✓ (K O-119) |
| Delivery | table; POST 10 s, no redirects; status mapping; delays; 7 attempts; 24 h; Retry-After ≤ 6 h; `notify.fail`; purge 7 d; ≤ 1000; `--test`; URL re-read; removal | `queue.go:21-244`; `webhook.go:135-293`; `dial.go:242-256` | ✓ / 24 h **D T6a-05**; cap **T6a-20**; K C27-01, O-113, O-117 |
| Privacy / audit | desktop content; public keys never sent; actors | `trigger.go:199-222`; `daemon/notify.go:74-124` | spec inconsistent **T6a-20**; **T6a-10** |
| CLI notify | forms, events, exits, output, JSON, errors | `cmd/agentnet/notify.go:20-228` | ✓ (K C27-02, O-070) |

## Undocumented behaviour (code with no spec)

- **IPC:** `status` fields `approval`, `git`, `relay` absent from ipc.md §status (`internal/daemon/daemon.go:55-65`);
  oversized request → `bad_request` without `id` then close (`internal/ipc/ipc.go:146-148`); every peer reference
  accepts `@` and case-insensitive names, not only `ping` (`internal/daemon/ping.go:104,114`); `request_show`
  accepts `s-` ids (`request_lifecycle.go:435-440`); `ws_discard` (`session.go:457`); unknown methods do not count
  as agent activity (`ipc.go:160-175`); `presence_set` with empty params returns current state.
- **Error codes not in the specs:** CLI `timeout`, `daemon_error` everywhere (`cmd/agentnet/pair.go:238-252`);
  `bad_request` for `ping @` and pairing (`daemon/pairing.go:53,64`, `ping.go:77,92`); request_submit
  `quarantine_active`, `entry_too_large` (Phase 3, `request.go:44-46`).
- **Relay:** read-budget close 1013 "relay busy" without error frame (`relay.go:664-668`); control-rate check before
  op validity (`relay.go:741-751`); `queued`/`error` dropped when the sender's buffer is full (`conn.go:207-220`);
  `/healthz` HEAD/405/429 (`relay.go:451-456`); loopback origins only `127.0.0.1`, `localhost`, `[::1]`
  (`cmd/relay/transport.go:142`); client does not check `ready.public_key`/`challenge.version`
  (`relayclient.go:257-320`).
- **Pairing/session:** audit action `peers.list_skip` and log `peers_bad_key` (`peers/store.go:20,276-280`); log
  events `pair_peer_ignored`, `pair_confirm_send_failed`, `pair_store_error`; redeemer 30 s KDF re-arm
  (`pairing.go:820`); `pair --v1` with a 15-char code runs v2 (`pairing.go:303-317`); session internals: 4 pending
  responder handshakes, 64 pending pings / `too_many_pings`, 256-entry inbox `session_drop`, non-JSON plaintext
  ignored (`session.go:75-79,315,350,687-690,767-784`).
- **Mail:** `MaxPayload` check at step 2 (`open.go:166`); `keys` with no handler not acked (`receiver.go:137-139`);
  relay `rate_limited`/`relay_full` → `queued`, other codes leave `relayed` (`outbox.go:514-524`); batch 50/1 s;
  `OnFinal`; INV-4 re-queue; "unusable" key rotation (`mailbox.go:37,224-237`); legacy `current.json` import; log
  events `ack_seal_failed`, `ack_send_failed`, `mail_error`, `mailbox_error`.
- **Team/presence:** re-join bumps the epoch (`kinds.go:460-489`); members without a peers row skipped from rosters
  (`store.go:563-576`); `team_rename` allows a duplicate active name (`store.go:353-381`); name refs resolve only
  active teams (`daemon/team.go:447-451`); `team.join` accepts non-canonical lookups (`kinds.go:436`); interval cap
  300 (`sender.go:444-446`); `SetMode` audits an unchanged mode; `SetHumanShare` sends no immediate heartbeat;
  log events `team_error`, `presence_error`, `presence_send_failed`.
- **Request:** decline reason and complete note refuse `\n`, cancel reason allows it (`lifecycle.go:272`,
  `result.go:52`, `cancel.go:47`); extra audit fields `context_files`/`context_bytes`; `request.accept` by actor
  `daemon` for helper auto-accept; deadline parser accepts `1.5d`/`1e1d`; empty relay URL counts as loopback for D5
  (`d/request.go:480-491`); `ParseArtifactSpec` is dead code.
- **Notify:** `request.received` suppressed for helper auto-accepts (`receive.go:425`); 16-event trigger bound with
  `notify_desktop_drop`; enqueue failure only logged; worker cadence 1 s / 20 / hourly purge; error strings
  `no_secret`, `redirect`, `http_<n>`; Retry-After integer seconds only; `--test` bypasses `showMu` and is not
  audited on failure; `powershell.exe`/`gdbus`/`notify-send` resolved via PATH (C12 lead).

## Assumptions resolved

T6 has no assigned unchecked-assumption list beyond the chunk "Spec" fields (04-themes §T6). The spec-related
unchecked assumptions in the Phase 0–1 chunks are resolved here:

- C05: `mail.ParseAnnouncement` validates `created`/`not_after` per mail.md step 5 — **holds** (`internal/mail/body.go:134-137`).
- C06: `mail.ParseAnnouncement(raw, key, now)` checks signature, identity and time — **holds** (`body.go:93-137`).
- C06: the IPC server does not log params — **holds** (`internal/ipc/ipc.go:159-195` logs nothing).
- C07 A1: `Open` gets a parsed, `mail`-typed envelope — **holds** (`relayclient.go:460`, `daemon.go:741-759` routes by type); O-050 stands as defence in depth.
- C07 A7: relayclient hands up every mail repeat — **holds** (`relayclient.go:480`).
- C08: `OpenPresence` enforces mail.md steps 1–10 — **holds** (`internal/mail/open.go:151-220`).
- C17 / C20: `op.Msg.From` is the authenticated sender and `created` is window-checked before `Apply` — **holds** (`open.go:160-220`, `receiver.go:122-124`).
- C17: request body from `ParseStrict` (numbers as `json.Number`, duplicates refused) — **holds** (`open.go:184-187,239-292`).
- C20: `mail.ErrBadBody` → `mail_seen` + `mail.reject` + ack `unsupported`; other errors retried — **holds** (`receiver.go:94-102,156-162`), but other errors are silent: **T6a-09**.
- C20: `ParseAnnouncement` returns `ErrAnnouncementExpired` only for the check-5 time failure — **holds** (`body.go:134-137`).
- C20: `agentcard.Verify` bounds card fields — **holds** (`agentcard.go:74-122`), with **T6a-22**.
- C28: `identity.LoadOrCreate` never returns the private key in `rep.Detail` — **holds** (`identity.go:88-93`), ipc.md:425-427 kept.
- C16: `notify_set` / IPC send no webhook secret — **holds**: the secret is generated by the daemon and returned once (`daemon/notify.go:260-266`); params carry only the URL (O-070 known).
- C17 lead (brief CRLF): the CLI normalises every brief (`cmd/agentnet/request.go:144`); the daemon does not for
  direct IPC callers, whose `\r` is then refused by `hasControl` — a stricter-than-spec outcome, no finding.
- C29 lead (15 d vs 30 d): two different limits, both as specified (`ReceiveMaxAge` 14 d in `receiver.go:34`,
  open window 30 d at step 11 `mail.go:42-43`); the test's 15 d is past the 14 d receive limit. **holds**.

## Leads for other chunks/themes

- `internal/ipc/ipc.go:176`: still no `recover` in `dispatch` (C16 lead) — T7/T10.
- `internal/relayclient/relayclient.go:462`: one Warn line per unparseable relay frame, per reconnect (T6a-02) — T10 log growth with C16-02.
- `internal/daemon/notify.go:88-92,126-131`, `quarantine.go:21`: synchronous DB work in the mail `After` hook (O-108 widening) — T2/T10.
- `internal/request/submit.go:187-203`: sender urgency budget counted outside the insert tx (C18 lead) — T2.

## Notes

- Seven helper passes were used to inventory ~750 statements; their line numbers were spot-checked and every
  finding re-read. Earlier chunk citations of `open.go:409-474` (C07) do not exist; the range is `open.go:158-223`.
- C16's "Checked and fine" entry "Results encoded with HTML escaping off (ipc.go:197-205)" should be struck (T6a-01).

## Commands run

- `go test ./internal/ipc -run 'TestReview55T6a01' -count=1 -v` → FAIL as expected: `'<' is HTML-escaped on the wire
  (6042 bytes, 0 raw '<')` (confirms T6a-01). File kept: `internal/ipc/zz_review55_T6a-01_test.go`.
- Helper passes ran `go test ./internal/peers -run 'TestPairingVectors|TestCodeFormat|TestParseConfirmIsStrict' -count=1`
  and `go test ./internal/envelope -run 'TestFingerprintVectors|TestNormalizeFingerprint' -count=1` → PASS.
- `git diff --stat 6cc26a7 HEAD -- . ':!Docs/review'` → empty (worktree code = 6cc26a7).
