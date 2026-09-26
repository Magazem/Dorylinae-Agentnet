# Telemetry (minimal, content-free)

Status: **draft** (Phase 4 spec, tickets 4.6a–4.6b in
[../review/49-phase4-tickets.md](../review/49-phase4-tickets.md)). Not approved. Open
choices are **OD-P4-n**.

Plan 4.6: "Relay-side counts only: connections, envelopes routed, queue depth, requests by type
and urgency, accept and decline counts, time to accept. No content, no briefs. Opt-out flag.
Acceptance: metrics dashboard shows the section 9 numbers per team." D7 and the plan's note
under 4.6: the relay sees only `mail`, so **per-kind counts can only come from the daemon, by
its own report**.

So there are exactly two sources, and nothing else leaves a machine:

| Source | What | Default | Can be turned off by |
|---|---|---|---|
| **Relay counters** (4.6a) | What the relay already sees, aggregated per billing team per UTC day | Always on at the hosted relay (it is how the operator runs the service and enforces the quota) | Nobody individually; documented in the privacy note |
| **Daemon report** (4.6b) | A fixed set of weekly counters computed from the local database | **OD-P4-8** | The user: `agentnet telemetry off` |

## Rules for both

1. **Only integers from a closed schema.** No strings except the fixed enum values below and
   the ISO week/date. The relay validates strictly: unknown members, out-of-range values,
   strings outside the enums → the whole report is refused (`bad_report`). A future bug cannot
   smuggle a title into a counter without a schema change, which this document then has to
   list.
2. **No per-person rows at the operator.** Counters are stored per billing team, never per
   key or account. (With 2–5 people a team counter is still close to personal data: see
   [Privacy](#privacy).)
3. **No ids of requests, sessions, peers, grants, files or teams** ever appear.
4. **Transparency:** `agentnet telemetry show` prints the exact JSON of the next report and the
   last 12 sent reports; the relay counters are listed in this document.

## Relay counters (ticket 4.6a, relay migration R5)

`telemetry_daily(team_id, day, name, value)`, one row per counter per billing team per UTC day,
updated in memory and flushed every minute (a crash loses at most a minute of counts).

| Name | Meaning |
|---|---|
| `keys_active` | Distinct bound keys of the team that authenticated that day |
| `accounts_active` | Distinct accounts of the team with an authenticated key that day |
| `connections` | Successful authentications |
| `mail_direct`, `mail_queued` | Non-ephemeral envelopes sent by the team's keys, forwarded directly / through the queue |
| `mail_bytes` | Their total size |
| `queue_expired` | Envelopes addressed to the team's keys that expired unread |
| `queue_depth_max` | Largest number of envelopes waiting for any one key of the team that day |
| `refused_rate`, `refused_queue_full`, `refused_quota` | Refusals by cause for the team's keys |
| `pairings` | Successful `pair_redeem` where the redeemer or issuer is in the team |
| `binds` | Keys bound that day |

Relay-wide operator metrics (no team label) are in
[relay-hosted.md §5](relay-hosted.md#5-monitoring-and-operations-tickets-41a-41b).

## Daemon report (ticket 4.6b, daemon migration 22)

### When and how

- Once per ISO week, for the **previous** week, on the first relay connection on or after
  Monday 00:00 UTC (plus a random 0–6 h delay, so reports do not arrive in one burst and their
  timing reveals less). Missed weeks are not back-filled.
- Sent as a control frame on the authenticated connection:
  `{"op":"telemetry_report","report":{…}}` → `{"op":"telemetry_ok"}` or `error` `bad_report`.
  The relay adds it to the sender's billing team's weekly totals (`telemetry_weekly(team_id,
  week, name, value)`) and **does not keep the individual report** (no per-key rows). Only on
  a relay with accounts; the daemon never sends it anywhere else.
- Stored locally first (`telemetry_reports`: `week`, `json`, `sent_at` NULL, `state`), so
  `telemetry show` can show what was sent. Migration **22**.

### Schema v1

```json
{
  "v": 1,
  "week": "2026-W45",
  "client": {"version": "0.4.2", "os": "windows", "arch": "amd64"},
  "requests_sent":     {"review": {"low":0,"normal":3,"high":1,"blocking":0}, "task": {…}, "question": {…}, "debate": {…}},
  "requests_received": {"review": {…}, "task": {…}, "question": {…}, "debate": {…}},
  "received_outcome":  {"accepted": 4, "declined": 1, "deferred": 0, "cancelled_by_sender": 0, "completed": 3},
  "time_to_accept":    {"lt15m": 1, "lt1h": 2, "lt2h": 0, "lt8h": 1, "lt24h": 0, "ge24h": 0},
  "sessions":          {"opened": 4, "results_accepted": 3, "quarantined": 0},
  "grants":            {"fs.read": 1, "git.read": 2},
  "fetches": 12,
  "debates":           {"started": 1, "agreed": 1, "escalated": 0, "cancelled": 0},
  "approvals":         {"confirmed": 3, "rejected": 0, "expired": 0},
  "setup":             {"completed_this_week": 1, "first_request_this_week": 1},
  "doctor_failures":   {"socket": 0, "service": 0, "relay": 1, "keychain": 0, "account": 0}
}
```

- Every integer is 0 … 100 000; enums are exactly the request types, urgencies and grant
  actions of the specs ([request.md](request.md), [grant.md](grant.md)); counts are of events
  **in that week** by the local clock's UTC week.
- `time_to_accept` buckets the time from a received request's arrival to the local accept, for
  requests accepted that week. Working hours (plan section 9) are not known to the daemon; the
  operator reads the median from the buckets and treats it as an upper bound.
- `client.os` and `arch` are `runtime.GOOS`/`GOARCH`; `version` is the build version.
- Nothing about peers, harness names, file names, repos, grant resources, commands, titles,
  briefs, results, reasons, notes, constraints or Decisions.

### CLI

`agentnet telemetry [show|on|off] [--json]`. `off` stops future reports and deletes unsent
ones; it is audited (`telemetry.off`/`telemetry.on`, no content). `agentnet setup` asks (see
OD-P4-8).

## From counters to the plan's section 9 numbers

| Section 9 measure | Computed from | Without daemon reports |
|---|---|---|
| Weekly active teams (2+ members, 1+ request that week) | `accounts_active` ≥ 2 on some day of the week **and** Σ `requests_sent` ≥ 1 | Proxy: `accounts_active` ≥ 2 and `mail_queued + mail_direct` above a threshold (noisy: key rotation and presence-adjacent mail count) |
| Requests per active team per week | Σ `requests_sent` | Not available |
| Time from request to accept, median | `time_to_accept` buckets | Not available |
| Accept rate | (`accepted` + `completed` of questions) / Σ `requests_received` | Not available |
| Retention (active 4 weeks after the first request) | Weekly active teams over time | Proxy as above |
| Install success (first request without contacting us) | `setup.first_request_this_week` + `binds` + the owner's support log | `binds` and first `mail` day per team |

So **Gate 2 can only be measured honestly if most beta teams send daemon reports.** That is the
real weight of OD-P4-8.

## Dashboard

**OD-P4-10**: (a) `relay admin stats [--week W] [--team bt_…] [--csv]` on the host prints the
section 9 table and the four weekly numbers (active teams, requests per team, accept rate,
install failures); the owner copies the CSV into a sheet; no web dashboard; (b) a static HTML
page generated by the same command; (c) a hosted dashboard service (sends data to another
processor). Recommendation **(a)**, with (b) later if useful. Retention: daily and weekly
counters kept until **90 days after the beta ends**, then deleted.

## Privacy

- Per-team counters for teams of 2–5 people let the operator infer individual behaviour
  ("someone on team X sent 3 blocking requests on Tuesday"). The privacy note says so plainly.
- Relay counters are needed to run the service; the daemon report is not. Hence the different
  defaults. Under the GDPR (EU testers) the daemon report is most defensible as **consent**
  (opt-in), the relay counters as legitimate interest; the owner decides with OD-P4-15 whether
  to get a legal review.

## Acceptance (summary)

- Plan 4.6: an e2e run with a fake week produces relay counters and a weekly report, and
  `relay admin stats` prints the section 9 table for that team.
- `TestTelemetryHasNoContent`: markers in titles, briefs, results, file names, grant resources,
  debate text and peer names appear in no report, no relay table and no relay log.
- A report with an extra member, a string in a counter, a negative or an out-of-range value is
  refused whole; the relay keeps no per-key report row.
- `telemetry off` → no report is sent the following week (fake clock); `show` prints the exact
  bytes that were sent.
