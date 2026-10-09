# Dorylinae closed beta: fact sheet for the promo film

Built from the repo docs only (read-only). Repo HEAD when read: 860afe8 (3 Oct 2026). Today: 8 Oct 2026.
Paths below are relative to `/home/user/Dorylinae-Agentnet/Docs/` unless stated.
Naming: the docs say "AgentNet" almost everywhere; the brand and the film say Dorylinae. CLI = `agentnet`, daemon = `agentnetd`.

Source keys: PLAN = `AgentNet Free Tier Build Plan.md`; INV = `protocol/invites.md`; TEL = `protocol/telemetry.md`;
FB = `protocol/feedback.md`; ACC = `protocol/accounts.md`; RELAY = `protocol/relay-hosted.md`;
LIM = `beta/known-limitations.md`; OWN = `ops/owner-next-steps.md`; P4 = `review/49-phase4-tickets.md`;
HAND = `orchestration/HANDOFF.md` (decisions D36..D45 in section 3).

---

## 0. Read first: what exists today versus what is only specified

Everything in sections 2 to 4 is an **owner-approved spec (D36, 27 Sep 2026)**, not shipped behaviour. The repo shows:

| State | Items |
|---|---|
| Built and merged | Daemon, CLI, relay, pairing, teams, presence, requests and inbox, grants and fetch, consult and work sessions, debate and signed Decision, local audit log, notifications. Relay TLS and abuse limits (4.0a-c, gate G-4.0 signed D38). Relay store and backup code (4.1a). Relay accounts core (4.2a, off unless the relay runs with `--accounts`). Signed release pipeline and `install.sh` (4.4a; only a dry-run draft was ever made, then deleted). `agentnet doctor` (4.4c). Unix service checks (4.4d). Early-relay deployment kit (4.1p). |
| **Not in the repo** (no code, no `Docs/cli` page) | Web login / GitHub sign-in page (4.2b). `agentnet login`, `agentnet setup` (4.2c, 4.9a). Beta invite codes and quota groups (4.3a). Quotas (4.1c). Telemetry counters and `agentnet telemetry` (4.6a-b). `agentnet feedback` and `CHANGELOG.md` (4.7a). Production deploy with backups (4.1b). Quickstart, privacy note and beta terms (4.5a). Waitlist and wave runbook (4.3b). Clean-machine runs (4.9b). Windows MSI (4.4b). |
| Not deployed yet | The early private relay `relay.dorylinae.net` was still on the owner's "remaining" list on 3 Oct (HAND section 0). D43: URL deliberately unpublished. |

Consequences for the film:
- On-screen terminal footage may only use commands that exist at capture time: `team create|invite|join`, `request`, `inbox`, `accept|decline|defer|complete`, `grant`, `fetch`, `consult`, `debate`, `decision`, `status`, `doctor`, `pair`, `peers`, `presence`, `notify`, `log`. Do **not** show `agentnet setup`, `--invite`, `agentnet feedback`, `agentnet telemetry` unless the owner confirms they ship before the film is published.
- Real cross-harness runs that passed: **Claude Code with agy (Antigravity CLI)**, both directions, for phases 1, 2 and 3 (HAND D34, `tests/harness/README.md`). A Codex CLI real run was blocked by a usage limit and never recorded as passed; Hermes and OpenCode are named in the plan (step 2.7 lists scripted harness tests for Claude Code, Codex CLI and Hermes) but have no recorded real run in the repo; the weekly CI harness uses a stand-in agent, not a real harness.

---

## 1. Ideal tester profile (quoted)

PLAN, "What the free phase must prove":
- "The free phase proves one sentence: **a small team's agents, in different harnesses on different machines, can see each other, hand each other work safely, and end with a decision record.**"
- "Done means a team of two to five people installs one binary each, pairs, and for four weeks uses it instead of chat messages for "can you look at this", without a manual fix from you."
- "The user this phase serves is a person building a project with one to four others, each running their own agent harness (Claude Code, Codex, OpenCode, Hermes, or a custom one), who today coordinates by pasting context into Slack or WhatsApp. The first user is you and one teammate."

PLAN, Phase 4 outcome: "ten invited teams run on a relay you host, you can see what breaks without reading anyone's content, and a new team goes from install to first request in under fifteen minutes." (a design target, not a measured result)

INV, waitlist (OD-P4-9 (a)): the form asks "an email, team size, harnesses used and a free-text 'what would you use it for'".

Scope fence (PLAN): "Nothing about strangers, credits, reputation or money is in scope." Not in the beta: pricing, ratings, discovery, agencies, dashboards, SDK, MCP adapter.

Practical eligibility, **derived from the specs (inferred, not a quoted rule)**:
- 2 to 5 people (quota group = 8 seats at most, INV; a daemon team holds at most 32, LIM).
- Each person runs an agent harness that can call a shell command (Docs/agents/snippet.md: the agent uses `agentnet` "with your shell/Bash tool"; instructions go in CLAUDE.md or AGENTS.md).
- A GitHub account for sign-in (OD-P4-2 (a): GitHub OAuth only; email only if a wave-1 tester lacks GitHub).
- macOS, Linux or Windows; no admin rights or sudo needed (`cli/install.md`, `cli/agentnetd-install.md`). On Linux the approval window needs `zenity` or `kdialog` (`cli/install.md`).

True one-liners the docs support (paraphrase, not approved copy): "For two to five people who each work with their own coding agent." "Your agent can see who is online, hand a teammate's agent a request, and end with a signed record of the decision."

---

## 2. Beta mechanics

| Topic | Fact | Source |
|---|---|---|
| Access | Invite-only. A beta invite code lets one person create a quota group on the hosted relay. Every account comes through an invite. | INV; ACC "Who may send to whom" |
| Code format | `BETA-XXXX-XXXX-XXXX`, single use, made by the operator. Expired or revoked codes answer `invite_invalid`. | INV |
| **Code expiry** | **48 hours**; the owner reissues on request. (Owner decision D36 overrode the 30 days in the OD-P4-7 table row of P4. That row is stale.) | HAND D36; INV "Waves" |
| Two different codes | Beta invite (first person only, via `agentnet setup --invite CODE` or `login --invite CODE`) versus team invite (`agentnet team invite`, a pairing code, valid 10 minutes, one use, needs the inviter's approval in the approval window; later members use only this). Do not blur them on screen. | INV; `cli/team.md` |
| Waves | 3 waves of 10 teams = 30 invited teams. Wave 1 = 10 teams "including the owner's own team", starts only after the beta gate, the build tickets and the demo video. Wave 2 at beta week 4. Wave 3 at week 6. | INV "Waves" (OD-P4-7); D36 |
| Seats | 8 per quota group. A 9th person gets `group_full`. The group contact (the person who redeemed the invite) can remove a member to free a seat. | INV |
| Beta length | 12 weeks ("weeks 18-22, then 12 weeks of beta"). Gate 2 measures weeks 9-12. **No calendar dates exist in any doc**; the wave-1 date is not set. | PLAN Phase 4 and gates |
| Wave-1 preconditions (owner checklist) | privacy note and beta terms published; demo video recorded; staging and production relays pass `doctor` on 3 OSes; restore drill done; owner's own team has used the hosted relay for a week. | P4 "Wave 1 go/no-go" |
| Platforms | macOS, Linux, Windows; amd64 and arm64. Per-user install, no admin. Installs: `curl -fsSL https://dorylinae.net/install.sh \| sh` (macOS, Linux), Homebrew tap, Windows zip (MSI is a later ticket). **Windows builds are unsigned in wave 1**: SmartScreen asks to confirm ("More info", "Run anyway"). | `cli/install.md`; D36 (OD-P4-12 (c)) |
| Sign-in | GitHub only. No passwords stored. No OAuth scopes requested. Up to 4 devices per account. Account page: unbind a device, leave the quota group, delete account. | ACC; OD-P4-2 |
| Free tier / caps | The beta is free ("the beta is free, no payments, ever", HAND session note; PLAN scopes out money). Plan wording: "300 relay sessions per team per month, 7-day offline queue". The relay cannot count sessions, so the spec turned it into a **soft cap of 30,000 envelopes and 3 GiB per quota group per month**: warning at 80 percent, operator alert at 100 percent, **no cut-off in wave 1** (hard mode exists behind a flag, off). Offline queue: 7 days. | PLAN 4.1; RELAY section 4; OD-P4-5 |
| Feedback channels | (1) `agentnet feedback "..."`: 1 to 4096 bytes, at most 5 per account per day, shown to you before sending, sealed to a key only the owner holds; an agent's `--yes` only drafts, a human sends. (2) "a weekly 20-minute call with two teams". (3) "a public changelog". Target: "Every beta week ships one release with a changelog entry". LIM: "If one of them gets in your way, tell us: that is how we decide what to change first." | FB; PLAN 4.7; LIM; D36 (OD-P4-16 (c)) |
| Docs testers get | One quickstart, one page per command, one page "how to tell your agent about agentnet" with snippets for CLAUDE.md, AGENTS.md and Hermes config. The agent snippet already exists (`agents/snippet.md`). Quickstart not yet written (4.5a). | PLAN 4.5 |
| What testers get (product) | Presence (3 levels), requests with urgency and an inbox, desktop and webhook notifications, bounded work sessions, scoped expiring grants with human approval, consult, two-party debate ending in a Decision signed by both daemons, a local hash-chained audit log, hosted relay at no cost, a direct line to the owner. | PLAN phases 1 to 3; ACC |

### What testers commit to

**No doc states a tester commitment, and the beta terms are not drafted yet** (OWN section 4; ticket 4.5a). Do not write one on screen. What the docs imply (inferred, label as such):
- Use it on real work, as the plan's definition of done says: "for four weeks uses it instead of chat messages". Gate 2 counts a team as active with "2+ members, 1+ request that week", and retention "4 weeks after their first request".
- Turn the weekly usage report on. OD-P4-8 (a): "the beta invitation asks teams to turn it on, because Gate 2 needs it". It is opt-in.
- Try the quickstart without contacting the owner first (install-success measure: "reach first request without contacting you").
- Tell the owner what breaks (LIM). The weekly call is with **two teams** per week, not every team; how teams are picked is unspecified.

---

## 3. Privacy and telemetry facts that are safe to state

Each line can go in the film if it keeps its qualifier. All are **spec-approved (D36)**; the telemetry and feedback items are not built yet (section 0).

| Claim (suggested wording, plain) | Qualifier that must stay true | Source |
|---|---|---|
| Your requests, briefs, results, files and debates are sealed end to end. The relay carries ciphertext. | The relay is "untrusted whoever runs it". Content sealed, **metadata not**. | RELAY intro, threat model; PLAN architecture |
| The relay never learns request titles, briefs, results, grants, fetched files, debate content, Decisions, approval codes, pairing secrets, team names or rosters. | Quote the list as is. It does learn the items below. | RELAY "What the hosted relay learns" |
| Usage reports are counts only: integers from a closed schema, no text. "Nothing about peers, harness names, file names, repos, grant resources, commands, titles, briefs, results, reasons, notes, constraints or Decisions." A report with an unexpected field is refused whole. | Daemon report only. | TEL rules 1 to 3, schema v1 |
| The weekly usage report is **opt-in** and you can read the exact next report first (`agentnet telemetry show`); `agentnet telemetry off` stops it. | Opt-in, not opt-out (see section 7, item 2). Relay operational counters (connections, envelope counts and sizes, queue depth, refusals) are always on and cannot be turned off per person. | TEL table; OD-P4-8 (a), confirmed in D36 |
| Counters are stored per quota group, "never per key or account". Counters kept until 90 days after the beta ends, then deleted. | A team of 2 to 5 is close to personal data: TEL Privacy says the privacy note will "say so plainly"; one-member groups have per-person counters outright. | TEL rule 2, Dashboard, Privacy |
| Feedback notes are sealed to the operator's offline key; the relay host and a stolen backup cannot read them. Deleted from the relay on export or after 90 days. | Operator key compiled into the binary. | FB "Sealing" |
| No passwords. Signing in with GitHub asks for no permissions beyond your public profile, and the relay keeps no OAuth token after login. | Say "asks for no permissions", not "cannot see your repos". | ACC "No passwords" |
| Your identity key stays on your machine, in the OS keychain where available (owner-only file otherwise). | Fallback exists. | PLAN architecture; P4 CLI contracts, doctor `keychain` check |
| Access is granted by you, scoped, and expires; your agent cannot approve its own access. Approvals happen in a window the daemon opens. | Needs a human; sensitive grants quarantine results until released. | PLAN phase 2; `agents/snippet.md` |
| Every daemon action is logged locally in a tamper-evident chain you can verify (`agentnet log --verify`). | Say "tamper-evident". LIM: it "does not prove anything against a full database rewrite". | PLAN 3.6; LIM |
| Logs hold no payloads, IP addresses kept at most 14 days, backups encrypted to an offline key with 14 daily copies. You can delete your account; testers can ask for deletion. | Retention is the privacy note's promise; note not yet written. | RELAY section 3, 5; OWN section 4; ACC account page |

What the hosted relay **does** learn (must never be contradicted by the film): who each key belongs to (GitHub login), "which keys send mail / presence to which, when, how much", approximate team membership, when a daemon is online (even when invisible to peers), that a pairing happened and when, client IPs (logs at most 14 days). RELAY top-risk list: "from 4.2 it sees a graph of **named people**".

---

## 4. Disclosures the docs require, and where they belong

| Disclosure | What the docs say | Where the docs put it |
|---|---|---|
| **No independent outside security review** | LIM: "This private beta has not had an independent outside security review. The outside review (OD-P4-20) is scheduled before public launch, not before the beta." HAND D41: "testers are told this honestly (beta invitation / privacy note, ticket 4.5a)". OWN section 4: the privacy note and beta terms "must also disclose" it. SECURITY.md repeats it. | Beta invitation, privacy note, beta terms, LIM |
| Privacy note contents | What is collected (accounts, IPs in logs at most 14 days, usage counters, feedback notes); why; retention; deletion on request; processors (host, object storage provider, GitHub); controller and contact. | Privacy note (OWN section 4) |
| Metadata and named-people graph | Account binding "de-pseudonymises the metadata". | Privacy note (RELAY top risk 1) |
| Tiny-team counters are close to personal | "The privacy note says so plainly." | Privacy note (TEL Privacy) |
| Quota-group members see each other's GitHub login or email | "a privacy note item". | Privacy note (INV) |
| Windows unsigned, SmartScreen click-through | "documented" | Install docs / quickstart (D36) |
| Deliberate limits (one owner per team and no admins, lost owner device, invites by code only, no request editing, 7-day offline hold, debates need both sides on the same release, a Decision can end single-signed, and the rest of LIM) | "deliberate limits ... not bugs" | LIM |
| Early relay has no backups until 4.1b | "must be closed by 4.1b before any beta tester's traffic reaches the host" | Owner checklist, not tester-facing |
| Single operator, one relay | Top risks 4 and 7 in P4: one instance, one person at about 10 hours a week. No uptime promise exists. | Terms |
| Legal | No paid legal review (D36). GDPR basis for telemetry is consent (TEL). | Privacy note |

**Recommendation.** The docs put every disclosure in the invitation, privacy note, beta terms and install docs, **not in a film**. Keep that: the film's body makes no security-assurance claim (no "secure", "audited", "safe", "private by design"); it shows mechanisms only (sealed content, human approval, expiring grants, local log). Then add **one plain, unhidden line on the closing card**: "Closed beta. Invite-only. Not yet independently security-reviewed." Reasons: the film is the first contact and the audience does not know the product, so an unqualified encryption claim with no disclosure anywhere in the film would mislead by omission; the project's own SECURITY.md and LIM already say this in the same words; the brand voice ("Say what happens, then stop", no hype) fits a one-line statement of fact. Everything else on the table stays in the invitation and terms. This is the one place I go beyond the docs, so it needs the owner's yes (section 7, question B).

---

## 5. Call-to-action destinations

| Candidate | Documented where | Exists today? | Use in film? |
|---|---|---|---|
| `dorylinae.net` | HAND D39 ("Project domain = `dorylinae.net`", owner bought it); OWN ("domain/DNS ... done") | Domain owned. **No doc describes any page at the apex.** I could not fetch it (sandbox proxy refused the connection); from the sandbox the apex resolved to two addresses and `relay.dorylinae.net` did not resolve. Unverified. | Only as a name, and only if the owner confirms a live page. Do not append a path. |
| `https://dorylinae.net/install.sh` | `cli/install.md`, `scripts/install.sh`, `ops/release-signing.md` | Script is in the repo with the signing key embedded; **no real release has been published** (only a deleted dry-run draft). Serving status unknown. | No. It is an install step for invited testers, not a first-contact CTA. |
| `wss://relay.dorylinae.net` | D39, `ops/early-relay-deploy.md` | Not deployed as of 3 Oct. D43: "URL unpublished". It is a machine endpoint, not a page. | **Never show.** |
| `relay-staging.dorylinae.net` | D39 | Not deployed | Never show. |
| **Waitlist form** | INV OD-P4-9 (a): "an external form (e.g. a hosted form service) linked from the README"; ticket 4.3b | **Does not exist.** No provider chosen, no URL in any doc, README has no link, 4.3b not built. When built, its own privacy note must say who sees the answers and that they are deleted at the end of the beta (INV). | This is the only real CTA. The owner must create it; use a single `WAITLIST_URL` variable in the end card. **Do not invent a URL** (no `dorylinae.net/beta`, no `/apply`). |
| Contact email | None for the beta. OWN section 4 says the privacy note must name a controller and contact; not drafted. `magazem@gmail.com` appears in the repo only as the git author email in `orchestration/history.md`. | No designated contact | Do not use. |
| GitHub repo, issues, Security Advisories (SECURITY.md), Homebrew tap `magazem/tap` | README, SECURITY.md, `cli/install.md` | Public | **Forbidden by the owner's rule** (no repository exposure). The `brew install magazem/tap/agentnet` line also exposes the handle; keep it off screen. |
| `agentnet feedback`, weekly call | FB, PLAN 4.7 | Installed testers only; no link | Not a CTA. |

Fit with the process: invite-only means the CTA is "apply", then a code arrives that works for 48 hours. The film must not imply instant access.

---

## 6. What the film must not promise

1. **Open source.** License is PolyForm Shield 1.0.0, source-available (README, LICENSE). The plan's "free open-source tool" end state and its Apache-2.0 recommendation were not adopted.
2. **Free forever, pricing, credits, a marketplace, hiring agents, outside experts, strangers' agents, discovery, reputation.** All out of scope (PLAN). Say only that the beta is free.
3. **Any security guarantee**: secure, audited, certified, unhackable, zero-knowledge, anonymous, "we see nothing", SOC 2, GDPR-compliant, "we never share your data". None is documented; the beta has no outside review; the operator sees metadata about named people.
4. **Telemetry that is "opt-out" or "none".** It is opt-in for the weekly report; the relay's operational counters are always on.
5. **Any cap number from the plan** ("300 sessions"). The relay cannot count sessions; the real cap is a soft 30,000 envelopes and 3 GiB per group per month. Do not show a number. Do not say "unlimited".
6. **Install in 15 minutes** or "no support needed". Targets, not measured results.
7. **Availability or delivery guarantees.** Offline delivery is held 7 days; "offline" can lag about 90 seconds; delivery can show `expired` meaning unknown; one relay, one operator.
8. **Agents act on their own with your files.** Access needs your approval, is scoped and expires; "artifacts are only pointers"; results from a sensitive grant are held until released.
9. **Live shared sessions, group or three-way debates, editing a sent request, admins, moving team ownership.** None exist (LIM; PLAN: debates are two-party only).
10. **"Works with every agent."** Verified end to end: Claude Code with Antigravity CLI. Plan-named but unverified: Codex, OpenCode, Hermes. By design it works with any harness that can run a shell command. Also keep third-party logos off unless the owner clears the trademark use.
11. **Team size beyond the target.** 2 to 5 people; 8 seats per group.
12. **Replaces Slack.** The docs say "instead of chat messages for 'can you look at this'", nothing more.
13. **Scarcity or dates not set by the owner**: "10 teams in wave 1" (the plan counts the owner's own team among them), launch dates, "spots left", "weekly releases guaranteed", "weekly call with you".
14. **Internals on screen**: ticket numbers (4.x, R55, D36, OD-P4-n), decision ids, file paths into the repo, source code, the relay hostname, the GitHub handle, commit hashes, wave names like "P4-3".
15. **Commands that are not built yet** (section 0), and the hosted relay as if it were running.

---

## 7. Discrepancies found, and questions for the owner

Discrepancies between sources (I used the newer, owner-approved one):
1. Code expiry: P4 OD-P4-7 table row says 30 days; D36 and INV say **48 hours**. Use 48.
2. Telemetry: the brief and PLAN 4.6 say "opt-out flag"; D36/OD-P4-8 (a) and TEL make the **daemon report opt-in**. Relay counters are always on. Use opt-in.
3. Quota: PLAN "300 sessions per team"; RELAY/OD-P4-5 (b) soft cap 30,000 envelopes and 3 GiB. Use neither number on screen.
4. License: PLAN recommends Apache-2.0 and says "open-source"; the repo ships PolyForm Shield. Use "source-available" if license is ever mentioned, but the owner's rule says do not expose the repo, so best omit.
5. INV "Waves" still says wave 2 comes "after the outside security review's findings are fixed"; D41 moved that review to before public launch. Wave 2 is at week 4 regardless.
6. README still describes only Phase 0 and 1 and is not a reliable status source. HAND is.
7. D36 says Fly.io; D37 replaced it with Hetzner. Hosting is not a film topic.

Questions I would put to the owner (you said you will take questions where quality depends on a route):
- A. What is the real apply URL and who hosts the waitlist form? (None exists; the end card needs it.)
- B. Do you accept one plain line on the closing card, "Closed beta. Invite-only. Not yet independently security-reviewed."? (Recommended in section 4; the docs only require it in the invitation and terms.)
- C. Will `agentnet setup`, invites, telemetry and feedback ship before the film is published? If not, the film must stay with the commands in section 0 and describe the beta in words only.
- D. May the film say "first wave: ten teams"? True only if the plan stands, and your own team is one of them.
- E. Are Claude Code and Antigravity the only harness names to show? Do you want any third-party logos at all?
