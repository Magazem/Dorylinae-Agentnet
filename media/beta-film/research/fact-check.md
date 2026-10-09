# Fact check: product-facts.md and beta-facts.md

Adversarial check of both fact sheets against the repo at `/home/user/Dorylinae-Agentnet`
(main `860afe8`, read-only). Scope: product-facts section 3 (BUILT features) and section 6
(true sentences and reserve lines); beta-facts section 0 (state table), section 2 (beta
mechanics) and section 3 (privacy). A few lines from other sections are included at the end of
part B because scriptwriters are likely to lift them.

Method: every claim was read against the cited doc. Where a doc was ambiguous, the code was
checked (`cmd/agentnet/main.go`, `internal/approval`, `internal/capability`, `internal/team`,
`internal/mail`, `internal/request`, `internal/presence`, `cmd/relay/admin.go`). Remote tags were
listed read-only with `git ls-remote --tags origin`. Default verdict when support could not be
found: unsupported.

Paths below are relative to the repo root. `PROT/` = `Docs/protocol/`, `CLI/` = `Docs/cli/`,
`KL` = `Docs/beta/known-limitations.md`, `PLAN` = `Docs/AgentNet Free Tier Build Plan.md`,
`HANDOFF` = `Docs/orchestration/HANDOFF.md`.

---

## A. Confirmed (one line each)

### product-facts section 3 (BUILT)

- Dispatch table: 34 commands plus `version`, help and `--version`, and `mail` only with `DORYLINAE_DEBUG=1`. There is no `setup`, `login`, `logout`, `feedback` or `telemetry` command (`cmd/agentnet/main.go:47-127`).
- Tags `phase-0`, `phase-1`, `phase-2` and `phase-3` exist on origin. There is no `v*` release tag (ls-remote).
- 3.0: there are three parts. The harness only runs the CLI. The 2-second rule has two exceptions, `wait` and `log --verify`. Keys sit in the OS keychain, or in an owner-only file as fallback. The daemon is a per-user service and needs no admin (`CLI/agentnetd-install.md:3-4,30-34`, `PROT/agent-card.md` key storage).
- 3.1: the Agent Card has exactly 6 members, an Ed25519 key, a name of 1-128 characters and 0-32 skills. The fingerprint is 20 characters in 5 groups. The private key never reaches the CLI (`PROT/agent-card.md:31-38`, `CLI/identity.md:3-6`).
- 3.2: the pairing code has 15 characters. The first 5 go to the relay and the last 10 never leave the two machines. It is valid for 10 minutes, is single use and allows 3 attempts. The confirm step times out after 60 s. The code is stretched with Argon2id (t=3, 64 MiB). The command returns in under 2 s, or answers `pending`. Trust levels are relay, code, team and fingerprint. The MITM acceptance test is PLAN:65 (`CLI/pair.md:22-51`, `PROT/pairing.md:71-73,118`).
- 3.3: the creator is the owner, and there are no admins. The invite code is a pairing code valid for 10 minutes, and a team invite needs the owner's approval. A team of 5 needs 4 codes. Names must match `^[a-z0-9][a-z0-9-]{0,31}$`. A team holds at most 32 members (also `internal/team/team.go:23`). There is no group crypto, and the relay sees no team names or rosters (`PROT/team.md:7-38`, `CLI/team.md:36-47`, KL:21-36).
- 3.4: the three levels are a 30 s heartbeat (online if seen within the last 75 s), a CLI call in the last 5 min, and OS idle under 10 min. An offline daemon shows within 90 s, and the agent flag flips in about 1 s. Visibility modes work as described, and invisible shows last-seen only. Heartbeats are sealed and fixed-size, so the relay can neither read nor forge them. Human presence is shared by default (`PROT/presence.md:10-28,30-45,97-105`, `CLI/presence.md`, KL:79-83).
- 3.5: there are three request types, and the sender's agent writes the brief (no platform model, PLAN:89). A request returns `queued` in under 2 s even when the peer is offline. The brief is What / Why / Done when. Artifacts are pointers, at most 20. The title holds 120 characters and the brief 16 KiB, and the total request is capped at 64 KiB. An idempotency key stops a retry from sending twice. A request can be cancelled only before accept, cannot be edited, and a cancel does not refund the urgency budget (`CLI/request.md:27-84`, KL:40-54).
- 3.6: the inbox orders by priority, then age: 1000, 2000, up to 3000 and up to 4000. The weighting covers 30 days, and a new sender starts at full weight. Defer is at most 90 days and a decline reason 1-500 characters. Every answer is logged with a timestamp (`CLI/inbox.md:32-43`, `PROT/request.md:664-698,742`).
- 3.7: the budget is 5 high and 2 blocking per rolling 7 days, counted across all peers. Over budget, a request is sent as normal with a note. A reason is required for high and blocking (`CLI/request.md:31,63-65`). See B6 for the receiver-side nuance.
- 3.8: the event list and defaults are correct. Webhooks are https only (http only to localhost) and signed with HMAC-SHA256 in `Dorylinae-Signature`. The secret is printed once and retries stop after 24 h. The brief is never sent, and the title goes to the webhook only if you turn it on. Slack and Discord cannot verify the signature. Each OS uses a fixed notifier script. 5 s is the acceptance target (`CLI/notify.md`, `PROT/notify.md:13-33,84-96,193-233,269-278`).
- 3.9: the relay holds envelopes for 7 days (`--queue-ttl 168h`), and `expired` means delivery unknown. A resend is safe for 21 days. Mail older than 30 days is refused, and application mail older than 14 days is rejected (`internal/mail/receiver.go:39`). Ping and fetch work only while both are online: ping times out after 10 s, and fetch after 30 s with exit 4 (KL:55-60, `PROT/mail.md:336-339`, `CLI/ping.md:23-35`, `CLI/fetch.md:22,80`).
- 3.10: mail is signed with Ed25519 and sealed with HPKE X25519, and ping and fetch run over Noise XX. The relay's sees / cannot-see lists are correct. Forward secrecy comes only through rotation (keys rotate at 7 days and are deleted at 21). Non-repudiation is by design (`PROT/mail.md:8-29,45-53,659-662`, `PROT/session.md:22-38`, `PROT/relay-hosted.md:886-903`).
- 3.11: session states, commands and limits match the docs: summary 280, output 32 KiB, notes 2000, total 64 KiB, changes 1-4000. `wait` defaults to 300 s with a maximum of 3600 and exits 4 on timeout. `--human` records `human_accepted`, while `tests_passed` is only the worker's claim (`CLI/session.md`, `CLI/inbox.md:23-27`).
- 3.12: the approval code appears only on the notification and in the daemon's own window. No CLI command or IPC call takes it, and it never appears in output, logs or argv. 3 wrong codes reject the approval, and it expires after 10 minutes or when the daemon restarts (`internal/approval/approval.go:60-61`). Linux uses zenity or kdialog, and headless machines use terminal mode. The boundary is stated as in the docs (`CLI/approve.md:14-24,44-54`, `PROT/approval.md:13-54`).
- 3.13: grants are `fs.read` or `git.read` only. They last 1 minute to 7 days (`internal/capability/token.go:28-29`), 2 h by default, and stay `pending_approval` unless a policy matches. There are at most 50 policies, each lasting 30 days by default and 90 at most. Fetch reads files up to 8 MiB in 256 KiB reads. Per holder: 2 reads in flight, 20 operations a second and 256 MiB per grant per 24 h. `.git`, symlinks and devices are not served. Closing the session or removing the peer revokes grants, and only the requester can grant, while the session is open. The token is signed, and the grantor re-checks it on every read. There is no write access, and database grants are deferred (`CLI/grant.md`, `CLI/fetch.md:25-42`, `PROT/grant.md:7-27,275-303`).
- 3.14: every fs.read grant is sensitive, and so is every git.read grant without `--public`. The "ever active" rule and the 7-day per-peer rule hold. The requester's agent sees only status and sizes. Release needs approval, and discard or request-changes deletes the result unseen. The notification works as described, and a debate carries no grants (`PROT/work-session.md:343-386`, `CLI/session.md:26-30,62-70`). See B5.
- 3.15: a consult is a request of type `question`. The question is at most 16384 bytes, with 1-8 text context files of up to 64 KiB each. Only the base name is sent, and the total is capped at 320 KiB. Context never goes to the audit log, notifications or webhooks (`CLI/consult.md`).
- 3.16: the structure has positions, rounds of 1-5 (default 2), then proposal and answer. Commit–reveal is done by the initiator's daemon. Constraints need approval, hold 1-500 visible characters, and at most 10 can be active. A move has up to 3 challenges and an entry up to 32 KiB. The turn timeout runs from 5 min to 24 h, default 1 h. A missed slot 1 means `cancelled`, and a later missed turn means `escalated` with a signed Decision. Debates have two parties only and are refused with `debate_open` while a grant is open. The real-agent runs took 589 s and 603 s with 9 turns each (`CLI/debate.md`, `PROT/debate.md:13-31,95-111,407-417`, `tests/phase3-manual.md:111-117`).
- 3.17: the Decision's contents are as listed. Both daemons derive the same bytes and sign them. `--md` output is deterministic and inert. `decision verify` works offline and exits 0, 6 or 1. The signatures mean "as my daemon recorded it", not "I agree" (`CLI/decision.md`, `PROT/decision.md:10-20`).
- 3.18: the log holds no content, and triggers reject update and delete. It is a hash chain, `--verify` exits 5 on a break, and `--head` gives an anchor. The stated limit is correct (`CLI/log.md:1-37,96-101`, `PROT/audit.md:6-45`, KL:98-104). See B3.
- 3.19: the device helper and the experience record match the docs, which say nothing reads the record. Doctor's checks are as listed. `prune` has a 35 d minimum and `--yes` needs approval (`CLI/device.md`, `PROT/experience.md`, `CLI/doctor.md`, `CLI/prune.md:9-33`, HANDOFF D57).
- 3.20: agents use the CLI from their shell, with a snippet in CLAUDE.md or AGENTS.md. The plan names Claude Code, Codex, OpenCode, Hermes and custom. Claude Code and agy passed real runs in both directions for Phases 1, 2 and 3, with two daemons and a loopback relay on one machine. The owner's two-machine Phase 1 run passed (D29). A stand-in agent runs weekly in CI on 3 OSes. Codex was blocked by a usage limit, and Hermes and OpenCode never ran (`Docs/agents/snippet.md`, `tests/harness/README.md`, HANDOFF:134-163, D29).
- 3.21: CI builds for macOS, Linux and Windows, and the release workflow targets amd64 and arm64. Service names are as listed. Windows builds are unsigned in wave 1. Per-OS limits are as listed (`CLI/install.md:3-11,76-80`, `CLI/agentnetd-install.md:30-34`).
- 3.22: every number in the cheat sheet matches its source.
- 3.23: every command line is valid syntax against the CLI docs, and the sample output lines are quoted from the docs.

### product-facts section 6

- Confirmed: #1 sees who is online. #2 asks for a review. #5 5 high a week, then normal (it is a rolling 7 days). #6 sealed, relay carries ciphertext. #7 no command takes the code; this is the snippet's own claim, so keep the boundary note. #8 grant expires, next read fails after revoke. #9 signed Decision (agreed or escalated only). #10 log records that a request happened, not what it said.
- Reserve lines confirmed: a Decision exports as Markdown; your agent runs it from its shell; a request returns in under 2 s; it runs on macOS, Linux and Windows (CI-built; no release published); invisible means teammates see your last-seen time only.

### beta-facts section 0 (state)

- The built and not-built split is correct. The relay has `admin account|group` only, with no `invite` (`cmd/relay/admin.go:18-23`). The relay has bind frames (4.2a), but there is no code for invites, quotas, telemetry or feedback. The early relay is still on the owner's pending list (HANDOFF:97). Only a dry-run release was made (HANDOFF:107). The cross-harness facts are correct (see B15 for one citation).

### beta-facts section 2 (beta mechanics; every row is an approved spec, not built)

- Beta access is by invite only. A beta invite code lets one person create a quota group, and "every account comes through an invite" (`PROT/invites.md:12-21`, `PROT/accounts.md:64`).
- Code format is `BETA-XXXX-XXXX-XXXX`. Codes are single use, `invite_invalid` covers expired or revoked codes, and they expire after 48 h (D36; `PROT/invites.md:25-31,112`). The P4 table row saying 30 days is stale.
- The beta invite and the team invite are two different codes. The team invite is a 10-minute pairing code that needs the owner's approval (`PROT/invites.md:12-21`, `CLI/team.md:30-42`).
- Waves are 10/10/10 teams. Wave 1 includes the owner's team and starts after the gate, the build tickets and the demo video. Wave 2 starts at week 4 and wave 3 at week 6 (`PROT/invites.md:105-112`; review 49 OD-P4-7).
- Each quota group has 8 seats. A 9th person gets `group_full`, and the contact can remove a member (`PROT/invites.md:42-94`).
- The beta lasts 12 weeks and Gate 2 measures weeks 9-12. No calendar dates exist (PLAN:146, PLAN:208).
- Wave-1 preconditions are a correct subset of the go/no-go list. The fact sheet omits "G-4.0 signed; R-4.2, R-4.4, R-4.6 fixed" (review 49, §Wave 1 go/no-go).
- Sign-in is GitHub only, with no passwords and no scopes. An account binds at most 4 keys. The account page offers unbind, leave group and delete account (D36; `PROT/accounts.md:15-32,163-167`).
- The beta is free (HANDOFF:127 "the beta is free, no payments, ever"). The cap is a soft 30,000 envelopes and 3 GiB per quota group per month, with a warning at 80 % and an operator alert at 100 %. There is no cut-off in wave 1, and the hard mode sits behind a flag (`PROT/relay-hosted.md:842-864`).
- Feedback: up to 4096 bytes, at most 5 a day, shown to you before it is sent, and sealed to the owner's key. An agent's `--yes` only drafts. There is a weekly 20-minute call with two teams and a public changelog, and the KL quote is accurate (`PROT/feedback.md`, PLAN:158, KL:3-5).
- Docs for testers are as listed. The quickstart is not written yet (PLAN:156; 4.5a).
- What testers get (product): every built item listed is confirmed. See B7 for "a direct line to the owner".
- What testers commit to: no doc states a commitment. The inferences are labelled as such, and OD-P4-8 (a) asks teams to turn the report on (review 49 OD-P4-8).

### beta-facts section 3 (privacy; spec unless built)

- Content is sealed and the relay carries ciphertext. The relay is "untrusted whoever runs it", and metadata is not sealed (`PROT/relay-hosted.md:34-36`).
- The list of things the relay never learns is quoted correctly (`PROT/relay-hosted.md:902-903`).
- The weekly report is opt-in (D36). `telemetry show` prints the exact next report, and `off` stops it. Relay counters are always on (`PROT/telemetry.md:15-20,113-117`).
- Counters are kept per quota group and deleted 90 days after the beta ends. Tiny teams are close to personal data, and the privacy note says so (`PROT/telemetry.md:29-31,139-149`).
- Feedback is sealed to an operator key compiled into the binary. A note is deleted on export or after 90 days (`PROT/feedback.md:43-59`).
- There are no passwords, no scopes, and no OAuth token is kept after login (`PROT/accounts.md:30-32`).
- The identity key stays on the machine, in the keychain or an owner-only file. This part is built (`PROT/agent-card.md` key storage).
- The "relay does learn" list is correct, including the graph of named people from 4.2 onward (`PROT/relay-hosted.md:886-901`).

---

## B. Wrong or overstated

Ordered by risk to the film.

**B1. product-facts §6 true sentence #3: "Nobody is online. The request waits in their inbox." Wrong.**
While the teammate is offline, the request waits in the sender's outbox and the relay queue
(up to 7 days). It reaches their inbox only when their daemon comes back. "Nobody is online" is
also not what the scene means.
Correct: "They are offline. The request waits, and arrives when they are back."
Source: `PROT/request.md:732-738`, `CLI/request.md:58-61`, `PROT/mail.md:14-16`.

**B2. product-facts §6 reserve: "A request describes work. It never gives orders." Overstated.**
This is an instruction in the agent snippet, not something the product enforces. Nothing
controls how the receiving agent reads the text. What is enforced is that a request's text
grants no access or authority.
Correct: "A request grants nothing. Access is a separate grant you approve."
Source: `Docs/agents/snippet.md:79-82`, `PROT/grant.md:9-10`, `scope.md:78-79`.

**B3. "Every action" logged. Overstated.** This covers product-facts §3.18 ("every action the
daemon takes"), the §6 reserve "Every action leaves a trail you can verify", and the beta-facts §3
row "Every daemon action is logged locally".
Not audited:
- Decision exports (D33, KL:142).
- Presence heartbeats, rejects and state changes (`PROT/presence.md`, Audit: "not audited").
- Relay-caused rejects, including those from unpaired keys (D49, D58).

Also, the "sample real output" in §3.18 is the doc's example (`CLI/log.md:68-71`), not captured
output.
Correct: "Requests, answers, grants, fetches, approvals and Decisions are logged in a hash
chain you can verify."

**B4. beta-facts §3: "your agent cannot approve its own access." Missing its qualifier.**
This holds only for an agent acting through AgentNet's own interface. The docs say an agent
that runs arbitrary programs as the user can defeat it by reading notification history, driving
the window, or restarting the daemon in terminal mode. The mitigation is harness confinement.
Also, once a human approves a grant policy, matching grants issue with no prompt.
Correct: "No command takes the approval code; the human types it into the daemon's own window."
Source: `PROT/approval.md:40-54`, `CLI/grant.md:76-84`.

**B5. product-facts §3.14: "(so the peer cannot read in one session and hand the data back in
another)". Overstated.**
Rule 2 holds results and consult answers in any session for 7 days. But the worker can still
send the requester new requests, consults or notes with any content, and those are not
quarantined (OD-P2-15).
Correct: "Results that follow a sensitive grant are held until a human releases them."
Source: `PROT/work-session.md:348-350,369-372`.

**B6. product-facts §3.7: "The receiving side enforces the same budget, so a modified sender
cannot bypass it." Overstated.**
The receiver counts only requests from that sender to itself. So a modified sender can still
send 5 high and 2 blocking to each teammate, beyond the global budget "across all teammates".
Correct: "Each teammate also enforces the limit for requests sent to them."
Source: `PROT/request.md:705-718`.

**B7. beta-facts §2, "What testers get": "a direct line to the owner". Unsupported.**
The documented channels are:
- `agentnet feedback`, which is planned and not built;
- a weekly 20-minute call with two teams, so not every team each week;
- a public changelog.

Source: PLAN:158, `PROT/feedback.md:7-11`.

**B8. beta-facts §3: "backups encrypted to an offline key with 14 daily copies". Needs a
"not built" qualifier.**
- The backup job (4.1b) does not exist.
- The operator key format is not specified yet ("Don't generate one yet").
- The early relay runs with no backups.

Source: `Docs/ops/owner-next-steps.md:21-37`, HANDOFF:111, `PROT/relay-hosted.md:776-786`.

**B9. beta-facts §3: "Usage reports are counts only: integers ... no text." Slightly overstated.**
Schema v1 also carries the client version, OS and CPU architecture, and the ISO week.
Correct: "counts plus app version, OS and CPU type".
Source: `PROT/telemetry.md:84-109`.

**B10. product-facts §3.2: "If anything was altered in transit, nothing is stored." Slightly
overstated.**
The doc says nothing is stored "on the side that detects it". The issuer stores the peer before
it sends its final tag, so a dropped or altered final tag leaves a one-sided pairing: the issuer
has stored the peer, while the redeemer fails.
Source: `CLI/pair.md:41-44`, `PROT/pairing.md:329-330,359-362`.

**B11. product-facts §3.4: "the daemon also does not answer `agentnet ping` while invisible".
Missing exception.**
A peer that holds an active grant from you, or shares an open work session, can still open a
session (D60).
Source: `PROT/presence.md:254-257` ("One exception").

**B12. product-facts §3.11: "...accepts, asks for changes, or cancels". Imprecise.**
`--cancel` works only while the session is `open`. Once a result is waiting, the options are
accept-result or request changes, plus release or discard when the result is quarantined.
Source: `CLI/session.md:62-70`.

**B13. product-facts §6 true sentence #4: "Requests arrive most urgent first." Wording.**
Delivery order is not priority order; the inbox lists by effective priority.
Correct: "Your inbox puts the most urgent first."
Source: `CLI/inbox.md:32-40`.

**B14. product-facts §3.9: "resends with backoff until the receiver acknowledges it". Incomplete.**
The resends stop at 7 days, when the row becomes `expired` (delivery unknown). After that,
`request resend` is a manual step, possible up to 21 days.
Source: `PROT/mail.md:519-532`.

**B15. beta-facts §0: phase 1 and 2 cross-harness runs cited to "HAND D34". Wrong citation.**
D34 covers only Phase 3. Phase 1 is HANDOFF:161-163 and 379; Phase 2 is HANDOFF:147. The claim
itself is true.

**Outside the listed sections, but likely to be reused:**

- **B16. product-facts §1a: "Everything is private." Overstated.**
  Content is sealed, but the relay sees who talks to whom, when, how much, and when you are
  online. Desktop notifications show request titles on screen. The hosted relay will tie keys to
  GitHub logins.
  Source: `PROT/mail.md:659-662`, `PROT/relay-hosted.md:886-901`, `PROT/notify.md:269-278`.
- **B17. product-facts §4b: "free-tier cap of 300 relay sessions per team per month" and "8 seats
  per team". Stale.**
  The approved spec is a soft 30,000 envelopes and 3 GiB per quota group per month, with no
  cut-off in wave 1. Seats are per quota group, not per daemon team, which can hold up to 32.
  Source: `PROT/relay-hosted.md:842-864`, `PROT/invites.md:42`.
- **B18. product-facts §6, planned line "10 teams in the first wave." Needs a qualifier.**
  The owner's own team is one of the 10 (`PROT/invites.md:107`).
- **B19. beta-facts §2 Platforms row: the install lines read as if they work today. They do not.**
  - No release exists: there is no `v*` tag on origin.
  - The Homebrew tap is empty (D39).
  - The script's hosting is unconfirmed.

  Section 0's "spec, not shipped" header covers this, but the row should say "planned".

---

## C. Missing important facts

1. **There is no default relay.**
   - `agentnetd` connects only to `--relay` or `$DORYLINAE_RELAY_URL`, and runs with no relay otherwise (`CLI/agentnetd.md:19`).
   - D39 plans to compile in `relay.dorylinae.net` as the default, but the code has no such default (no `dorylinae.net` anywhere in `cmd/` or `internal/`).
   - With no hosted relay live, a team today must run its own relay. Off loopback it needs TLS, and the self-host guide is not written.
2. **There is no published release.**
   - Origin has only `phase-0`..`phase-3` and `pre-review-55` tags, no `v*` tag.
   - Default builds report `0.0.0-dev`.
   - Trying it today means building from source.
3. **Content is stored readable on both machines.**
   - End-to-end sealing covers transit and the relay, not data at rest.
   - Briefs, results and context files are kept in each daemon's local SQLite database ("stored on both machines", `CLI/consult.md:28-29`).
   - Malware running as the user is out of scope (`PROT/approval.md:36`).
   - Deleted content can survive in the WAL until a checkpoint (KL:105-111).
4. **Approvals need a working notifier and window.**
   - Desktop machines need a desktop notification plus an approval window (zenity or kdialog on Linux). Without them, `status` shows `approval window: missing` and approvals fail. Headless machines must run in terminal mode (`CLI/status.md:45-54`, `CLI/approve.md:14-19`).
   - The gated list in §3.12 is incomplete. It also includes `peers verify` (D48), `grant policy add`, `accept-result --human` and helper scopes (`PROT/approval.md:57-80`, `CLI/grant.md:78-79`).
5. **A third real debate passed, and the runs were light.**
   - The third run was 3.H2: Claude-initiated, `agreed`, 211 s, 6 turns, about 0.54 USD (`tests/phase3-manual.md:228-256`).
   - In that run both agents chose the same design, so `--challenge` was never exercised. The docs call each result "one run".
   - The snippet changed after 3.H and was not re-tested by a new real run (`tests/phase3-manual.md:132-134`).
6. **Commit–reveal has limits.** Either side may open with a thin position and revise it after
   seeing the other's. Leaks outside the protocol are not prevented (`PROT/debate.md:161-166`).
   Do not claim the debate is "blind" or "unanchored" beyond the opening positions.
7. **Desktop notifications show the request title and the peer name on screen.** Only the
   webhook keeps the title off by default (`PROT/notify.md:269-278`).
8. **Both sides must run the same phase build to debate or use sessions.** Mixed Phase 2 and
   Phase 3 pairs cannot debate (KL:61-71).
9. **Human presence can read `unknown`.** Idle detection fails on many Linux Wayland desktops
   other than GNOME and KDE (`PROT/presence.md`, idle detection table).
10. **No outside review so far.** "Code so far has been reviewed by AI agents and the maintainer,
    not by an outside security firm" (`SECURITY.md:21-23`). This is the exact wording behind any
    closing-card disclosure.
