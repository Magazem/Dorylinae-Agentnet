# AgentNet Plan Review: Per-Phase Checklist

**Source:** Docs/AgentNet Free Tier Build Plan.md (primary), scope.md, README.md, Docs/protocol/, Docs/cli/
**Date:** 2026-09-21

---

## Phase 0: Foundations (weeks 1–4)

Outcome: two daemons on two machines are paired and exchange an encrypted ping through a relay. No presence, no requests yet.

### Tickets and Acceptance Tests

| Ticket | Deliverable | Acceptance Test |
|--------|-------------|-----------------|
| 0.1 | Repo and skeleton | `make build` produces three binaries (agentnet, agentnetd, relay) on macOS, Linux, Windows |
| 0.2 | Daemon lifecycle | Service survives reboot; `agentnet status` reports daemon PID and uptime |
| 0.3 | Identity | `agentnet identity --json` prints signed Agent Card; signature verifies with separate tool |
| 0.4 | Relay minimum | Two daemons connect; envelope sent by A arrives at B unchanged; payload is ciphertext only |
| 0.5a | Pairing (relay half) | One-time code (relay-issued, 10-min TTL) exchanged; Agent Cards routed; both sides receive peer's card |
| 0.5b | Pairing (daemon half) | Pair succeeds once; same code fails second time; both list peer in `agentnet peers` |
| 0.6 | Encrypted session | Noise XX handshake; per-session keys; `agentnet ping @peer` round-trips encrypted message; relay logs show ciphertext only; tampered envelope rejected |
| 0.7 | Offline queue | Relay stores envelopes for disconnected peer up to 7 days; delivers in order on reconnect |

**Before 0.1 (Decisions to make now):**
- Language: **Go** (recommended: single static binary, easy service packaging, coding agents produce it reliably)
- Relay hosting: **Go binary on Fly.io** (recommended: same language as daemon, self-hostable, WebSocket handling; Cloudflare later for scale)
- Session crypto: **Noise XX now** (recommended: mature, small, pairwise is all this phase needs; MLS only if group sessions become paid feature)
- Capability tokens: **Biscuit** (recommended: offline verification, attenuation, expiry built in; Go library maintained)
- Licence: **Apache-2.0 daemon/CLI, source-available relay (BSL or AGPL)** (recommended: closed binary risks for users; business logic in relay)
- Account binding (hosted relay): **GitHub OAuth first** (recommended: all users have one; add email magic link later)
- **Action item:** Check "AgentNet" name before 0.1 (collides with existing projects; unique name saves rename after launch)

---

## Phase 1: Presence, Requests, Inbox (weeks 5–8)

Outcome: I see teammate is online, agent sends task request with brief, lands in inbox sorted by urgency, desktop notification appears. First thing beta tester will use.

### Tickets and Acceptance Tests

| Ticket | Deliverable | Acceptance Test |
|--------|-------------|-----------------|
| 1.1 | Teams | Named set of paired peers; `agentnet team create`, `team invite` (one-time code), `team list`; presence scoped to team; two teams with shared member show only own members |
| 1.2 | Presence, three levels | Daemon heartbeat (30s) → daemon online; CLI call (last 5min) → agent active; OS idle (<10min) → human present; `agentnet status --team x --json` lists all three, last-seen; kill daemon on B: shows offline within 90s; run any command on B: shows active within 30s |
| 1.3 | Visibility controls | `agentnet presence --invisible`, `--only-team x`; relay only broadcasts what daemon publishes; invisible peer shows last-seen only, never online |
| 1.4 | Request object | Schema: `id, from, to, team, type (review, task, question), title, brief, urgency (low, normal, high, blocking), urgency_reason, artifacts[] (url, branch, commit, path), requested_grant?, deadline?, created`; `agentnet request @peer review --title ... --brief ... --artifact ... --urgency ...`; A's request readable by B's daemon with all fields intact, ciphertext in transit |
| 1.5 | Brief written sender-side | CLI documents brief schema in `agentnet request --help`; sender's agent fills it; optional `--brief-from-file`; no platform-side model; request with two-sentence brief from headless Claude Code run |
| 1.6 | Inbox | `agentnet inbox --json` lists pending requests sorted by effective priority, then age; `agentnet accept <id>`, `decline <id> --reason`, `defer <id> --until`; three requests with different urgencies list in right order |
| 1.7 | Urgency guards | Account has 5 high / 2 blocking per week budget; beyond it becomes normal with note; effective priority = declared urgency weighted by sender's acceptance-as-urgent rate (last 30 days); exhaust budget: sixth high arrives as normal |
| 1.8 | Notifications | Desktop notification on new request and on accept/decline; optional webhook URL (Slack, Discord, etc.) with signed JSON body; notification within 5s of arrival; webhook receives payload |
| 1.9 | Offline handling | Sending to offline peer returns `queued` with last-seen; sender's agent gets machine-readable status, never timeout; send to stopped daemon: returns in under 2s with `status: queued` |

**Metrics recorded from day one:** every accept, decline, defer, completion logged with timestamps (data for learned prioritization, metrics in section 9).

---

## Phase 2: Grants, Consult, Sessions (weeks 9–13)

Outcome: accepted request becomes bounded session; requester gives peer's agent scoped, expiring, revocable access (branch, directory, database URL); peer returns structured result. The security story.

### Tickets and Acceptance Tests

| Ticket | Deliverable | Acceptance Test |
|--------|-------------|-----------------|
| 2.1 | Session object | Created on accept: `id, request_id, parties, state (open, awaiting_result, closed), opened, closed, grants[], result?`; `agentnet sessions --json`, `agentnet session <id>`; state transitions match diagram; nothing else reachable |
| 2.2 | Grant issuance | `agentnet grant @peer --session <id> --action repo.read --resource github.com/org/repo#branch --expires 2h`; daemon mints Biscuit token with caveats, sends inside session; human approval prompt unless policy allows it; token verifies offline on peer; widened caveat token rejected |
| 2.3 | Grant enforcement | Peer's daemon exposes granted resources only through `agentnet fetch <grant-id> <path>`; token checked on every call; expiry and revocation honoured; after `agentnet revoke <grant-id>` next fetch fails within 1 second |
| 2.4 | Sensitive-grant rule | Any grant marked sensitive (private repo, database, filesystem) quarantines session's outgoing artifacts; grantor must release with `agentnet release <session>`; session cannot deliver result until released; audit log records release |
| 2.5 | Consult | `agentnet consult @peer --question ... --context-file ...` = request of type question + auto-opened session; peer answers with `agentnet result <session> --file answer.md --json`; round trip headless in two harnesses; `agentnet wait <session> --timeout 300` returns result |
| 2.6 | Result object | `session_id, summary, artifacts[], verification (none, tests_passed, human_accepted), notes`; requester accepts with `agentnet accept-result <session>`; session closes |
| 2.7 | Harness tests | `tests/harness/` folder with scripted headless runs for Claude Code (`--print`), Codex CLI, Hermes; execute full request → grant → consult → result loop; all three pass in CI weekly against pinned harness versions |

**Security focus:** grant issuance, enforcement, sensitive-grant rule. Ticket 3.6 adds audit log hash chain.

---

## Phase 3: Debate, Decision Records, Audit Log (weeks 14–17)

Outcome: two teammates' agents argue technical question, produce one signed Decision record for repo, every daemon action visible in local log.

### Tickets and Acceptance Tests

| Ticket | Deliverable | Acceptance Test |
|--------|-------------|-----------------|
| 3.1 | Debate session | `agentnet debate @peer --topic ... --context-file ...` opens session type debate; round structure: commit position hash, reveal, up to N challenge rounds, converge step; debate with 2 rounds completes headless in two harnesses |
| 3.2 | Positions and challenges | Structured messages: position (claim, assumptions, evidence, rejected alternatives), challenge (targets, argument, evidence), revision; every message validates against schema; free text in argument field only |
| 3.3 | Decision object | Fields: problem, participants, initial positions, assumptions, evidence, arguments, counterarguments, rejected alternatives, final agreement, remaining disagreement, human decisions, constraints, affected artifacts; signed by both daemons; `agentnet decision <id> --md` writes Markdown for repo's decisions folder |
| 3.4 | Human injection | Either human can add constraint mid-debate with `agentnet debate <id> --constrain "..."`; appears in record as human decision; constraint shows in Decision under human decisions |
| 3.5 | Escalation | No agreement after N rounds: Decision records remaining disagreement, session closes as escalated, both humans notified; force disagreement: record still produced and signed |
| 3.6 | Audit log | Hash-chained, local, append-only log of every daemon action (pair, request, accept, grant, fetch, revoke, release, result, decision); `agentnet log --since 24h --json`, `agentnet log --session <id>`, `agentnet log --verify` detects tampering; triggers prevent UPDATE and DELETE |
| 3.7 | Private experience record | On session close, daemon writes local record (problem, approach, what worked, what failed, verification, acceptance); nothing reads it in this phase |

**Scope:** two-party debates only. Three-way or external judge is later study.

---

## Phase 4: Private Beta Operations (weeks 18–22, then 12 weeks of beta)

Outcome: ten invited teams run on hosted relay, see what breaks without reading content, new team goes install-to-first-request in under 15 minutes.

### Tickets and Acceptance Tests

| Ticket | Deliverable | Acceptance Test |
|--------|-------------|-----------------|
| 4.1 | Hosted relay | One Fly.io or Hetzner instance, TLS, daily SQLite backup, health endpoint, uptime monitor; free-tier cap: 300 relay sessions/team/month, 7-day offline queue; relay restarts without losing queued envelopes |
| 4.2 | Accounts | Email + magic link OR GitHub OAuth to bind daemon identity to person, enforce cap; no passwords stored; daemon cannot connect to hosted relay without bound account |
| 4.3 | Invitations | Invite codes per team, 10 teams wave one, waitlist form for rest; invite flow works on macOS, Linux, Windows |
| 4.4 | Install | `curl | sh` and Homebrew tap (macOS/Linux); signed MSI (Windows); `agentnetd install` registers service; `agentnet doctor` checks socket, service, relay reachability, keychain |
| 4.5 | Docs | One quickstart, one page per command (generated from `--help`), one "how to tell your agent about agentnet" with snippets for CLAUDE.md, AGENTS.md, Hermes config; tester who never talked to you completes quickstart |
| 4.6 | Telemetry, minimal | Relay-side counts only: connections, envelopes routed, queue depth, requests by type/urgency, accept/decline counts, time to accept; no content, no briefs; opt-out flag; metrics dashboard shows section 9 numbers per team |
| 4.7 | Feedback loop | `agentnet feedback "..."` sends note; weekly 20-min call with two teams; public changelog; every beta week ships one release with changelog entry |
| 4.8 | Security review | Outside review of grant issuance, enforcement, sensitive-grant rule before wave two; findings fixed or documented; no bypass remains |

**Five-minute demo video:** made at phase 4 start, not end. Features not in video are candidates for cutting.

---

## Gates, Metrics, Stop Conditions

### Gate 1: End of Phase 3 (end of week 20)

**Measure:** You and one teammate complete request, grant, consult, debate across two machines, headless, in two harnesses.

- **Pass:** 10 consecutive runs with no manual fix
- **Fail:** Still needs manual intervention at week 20 → **STOP: rethink CLI/session model**

### Gate 2: End of Beta (end of week 34, 12 weeks of Phase 4)

**Weekly active teams** (2+ members, 1+ request that week):
- **Pass:** 15 of 30 invited teams active in weeks 9–12 of beta
- **Fail:** Fewer than 10 after 8 weeks → **STOP: keep as open source, no next phase**

**Requests per active team per week:**
- **Pass:** Median 5 or more
- **Fail:** Median under 2

**Time from request to accept (median, during receiver's working hours):**
- **Pass:** Under 2 hours
- **Fail:** Over 24 hours

**Accept rate:**
- **Pass:** 60% or more accepted or answered
- **Fail:** Under 40%

**Retention** (teams still active 4 weeks after first request):
- **Pass:** 10 teams
- **Fail:** Fewer than 5

**Install success** (teams reaching first request without contacting you):
- **Pass:** 80% of invited teams
- **Fail:** Under 50%

**Weekly review during beta:** active teams, requests per team, accept rate, install failures. Monthly for everything else.

### Gate 2 Unlock (if passing)

Not a build. Two studies: (1) survey and five interviews on whether active teams want external consults, (2) what they would pay for. Results decide whether next document is written. **Valid end state:** free open-source tool with hosted relay if teams are happy and nobody wants externals.

---

## Protocol Specifications (Docs/protocol/)

| File | Specifies | Introduced by ticket |
|------|-----------|---------------------|
| [protocol/README.md](../protocol/README.md) | Index of all protocol docs | Phase 0 |
| [protocol/ipc.md](../protocol/ipc.md) | Local CLI ↔ daemon protocol: endpoints, framing, methods (status, identity, pair_new, pair_redeem, pair_status, peers, ping, ping_status), responses, error codes, audit events | 0.2a |
| [protocol/agent-card.md](../protocol/agent-card.md) | Signed Agent Card: JSON schema, canonical serialisation (JCS), Ed25519 signature with domain prefix, key storage (keychain/file), test vectors, IPC | 0.3 |
| [protocol/envelope.md](../protocol/envelope.md) | Relay protocol: envelope JSON schema, WebSocket challenge/auth/ready handshake, forwarding rules, offline queue semantics and storage, error frames, logging rules | 0.4 (pairing by 0.5a, offline queue by 0.7) |
| [protocol/pairing.md](../protocol/pairing.md) | Pairing flow: frame types (pair_new, pair_code, pair_redeem, pair_peer), code format and TTL (10 min, 50 bits entropy), single-use, failure modes, rate limiting, daemon-side card verification and storage, audit | 0.5a (relay), 0.5b (daemon) |
| [protocol/session.md](../protocol/session.md) | Encrypted sessions (Noise_XX_25519_ChaChaPoly_SHA256): static-key binding to identity via Ed25519 signature, handshake payload, envelope types (session.init/resp/fin/data), transport with counter nonce and AAD, replay/reorder checks, session lifecycle, rejection reasons | 0.6 |

---

## CLI Specifications (Docs/cli/)

| File | Specifies | Introduced by ticket |
|------|-----------|---------------------|
| [cli/status.md](../cli/status.md) | `agentnet status [--json]`: checks daemon running, PID, uptime; exit codes 0/1/2/3; returns in 2 seconds | Ticket 0.2a |
| [cli/identity.md](../cli/identity.md) | `agentnet identity [--json]`: prints signed Agent Card from daemon; verifiable with standalone tool; exit codes 0/1/2/3 | Ticket 0.3 |
| [cli/agentnetd-install.md](../cli/agentnetd-install.md) | `agentnetd install/uninstall [--home DIR] [--dry-run]`: per-user service registration (launchd/systemd/Task Scheduler); audit events | Ticket 0.2a |
| [cli/pair.md](../cli/pair.md) | `agentnet pair --new`, `agentnet pair <code>`, `agentnet pair --status <id> [--json]`: one-time 10-min code, card verification, 2-second rule, pending/complete/failed states, audit | Ticket 0.5a/0.5b |
| [cli/peers.md](../cli/peers.md) | `agentnet peers [--json]`: lists paired agents with public_key, name, harness, skills, paired_at; oldest first | Ticket 0.5b |
| [cli/ping.md](../cli/ping.md) | `agentnet ping @peer`, `agentnet ping --status <id> [--json]`: round-trip encrypted message; Noise XX; 2-second rule; pending/complete/failed states; RTT milliseconds; handshake flag | Ticket 0.6 |
| [cli/relay.md](../cli/relay.md) | `relay [--listen HOST:PORT] [--allow-non-loopback] [--verbose] [--version]`: WebSocket server; Phase 0 in-memory no-TLS; daemon connects with signed challenge auth | Ticket 0.4 |

---

## Contradictions and Discrepancies

### 1. Docs/ vs docs/ Casing (README.md note, ticket 0.1b alignment needed)
- **Plan/docs location:** Plan refers to `docs/protocol/` and `docs/cli/`
- **Current state:** Actual files are at `Docs/protocol/` and `Docs/cli/`
- **README.md note:** "On case-insensitive filesystems (Windows, macOS) they are the same directory; on Linux rename Docs/ → docs/ (owner decision) to match plan"
- **Status:** Not yet harmonized; ticket 0.1b aligns Go module path; docs path unresolved

### 2. Phase 0 Completion Status (README.md vs. git log)
- **Plan:** Phase 0 is weeks 1–4
- **README.md line 8-9:** "This repository is at **Phase 0, ticket 0.1**: a skeleton with no behaviour beyond `--help` and `--version`"
- **Git log:** Commit `2078f1f` says "Phase 0: pairing, Noise sessions, relay with offline queue, ping (tickets 0.3-0.7)" and ticket `0.1b` aligns module path
- **Discrepancy:** README claims skeleton-only state, but log shows 0.3–0.7 work merged. README is stale.

### 3. Relay Hosting Decision Status (section 10 decision list)
- **Plan decision 2:** "Relay hosting: Go binary on Fly.io" marked as **recommendation**
- **README.md:** Under "Project decisions" says "Licence: *not decided.*" but does not mention relay hosting as unresolved
- **Status:** Decision appears decided in plan (Go/Fly.io), but README doesn't explicitly confirm; unclear if chosen or still open

### 4. No Specification Yet for Phase 1+ Endpoints/Methods
- **Plan:** Phase 1 adds teams, presence, requests, inbox (tickets 1.1–1.9)
- **Docs/protocol/ and Docs/cli/:** Only Phase 0 specs present (ipc.md methods are status, identity, pair_*, ping_*; no methods for teams, requests, inbox yet)
- **Status:** Correct per plan ("Nothing is specified yet; Phase 0 adds the first documents"), but Phase 1 protocol must be written before tickets 1.1+

### 5. Scope.md "Repository and Push Policy" vs. Current Git State
- **Scope rule:** "Every phase ends with a dedicated 'push to origin' ticket that depends on all other tickets of that phase"
- **Current:** Latest commit is Phase 0 work (0.3–0.7), but no explicit "phase-0" push ticket or tag visible
- **Status:** Phase 0 appears merged but push/tag policy not yet visible in git history

### 6. Test Layers (section 7) vs. Protocol Docs
- **Plan:** Lists Unit / Integration / Harness / Manual test layers and tick schedule
- **Docs/protocol/:** Reference audit events in ipc.md, pairing.md, session.md, but no test vector specifications or test infrastructure docs
- **Status:** Plan specifies test strategy; protocol docs don't yet detail test vectors (agent-card.md has one vector for Ed25519; others may follow)

---

## Decisions Confirmed and Pending

### ✅ Confirmed (section 10, "Recommendations" followed)
1. **Language:** Go
2. **Session crypto:** Noise XX
3. **Capability tokens:** Biscuit
4. **Relay hosting:** Go on Fly.io
5. **Licence:** Apache-2.0 daemon/CLI, source-available relay (not yet in LICENCE file per README.md)
6. **Account binding (hosted relay):** GitHub OAuth first

### ⚠️ Pending / Not Yet Resolved
1. **Project name ("AgentNet"):** Plan says "Check the name before 0.1" (collides with existing projects, GitHub org). Current repo is "Dorylinae-Agentnet" (project name: Dorylinae, binaries: agentnet). Status: **unclear if name check completed**.
2. **Docs path harmonization:** Docs/ → docs/ on Linux per plan, but no ticket assigned. **Ticket 0.1b aligns Go module; docs rename decision pending.**
3. **Relay licence specificity:** Plan recommends "BSL or AGPL"; README says "not decided". Which chosen? **Decision deferred.**

---

## Summary: Entry Readiness for Current/Next Ticket

### Phase 0 Status
- **Merged work:** Tickets 0.3–0.7 (per git log commit `2078f1f`)
- **Expected next:** Phase 0 push ticket (phase-0 tag, branch to main) OR Phase 1 entry (depends on Phase 0 push passing)
- **Blocker for Phase 1:** Phase 0 harness test (0.7) and push must complete; Phase 1 protocol specs must be written before ticket 1.1 starts
- **Entry criteria check:** Phase 1 tickets' acceptance tests exist (in plan table); interfaces not yet in code; schema changes (teams, presence, requests) not yet in Docs/protocol/; tickets are table-ordered and sequential

### Contradictions Affecting Build
- README.md is stale (claims Phase 0 skeleton; Phase 0.3–0.7 merged)
- Docs/ casing not harmonized (plan says docs/, repo has Docs/)
- Phase 0 push ticket not explicit in visible git history (unclear if all Phase 0 work is stable/tagged)

---

## Files Written by This Review

- **Output:** Docs/review/01-plan-checklist.md (this file)
