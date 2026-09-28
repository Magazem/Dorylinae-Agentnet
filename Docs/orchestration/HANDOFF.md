# Orchestrator handoff

Read this first if you are a fresh Orchestrator instance. It is the single source of truth
for *where we are*. Update it after every merge and owner decision. The full per-wave
record up to the end of Phase 1 is archived in `Docs/orchestration/history.md`.

Last updated: 2026-09-28, work PC: **Phase 4 waves 1-2 + R-4.0 merged, INV-7 merged, main green; G-4.0 SIGNED (D38), hosting = Hetzner (D37); next is wave P4-3 (4.2a Opus, 4.4a Sonnet). Owner is setting up the domain and the GitHub OAuth app.** Phases 0-3 merged and tagged. The orchestration rules for this PC: relay all hands-on work to workers (no self-edits, no subagents), poll CI in the background, and always check a worker's worktree/report against the logs before merging.

## 0. Status and next steps

**2026-09-28 (work PC): INV-7 merged (f9de516), main green.** After the park, CI on e4e6e55 was red on Windows only: `TestAuditListSessionView` read the session audit view right after `waitState("quarantined")`. Test-only, not a product bug: the state commits inside `applyResult`'s tx and `ws.result_in` is audited after commit in `afterResult` (internal/worksession/receive.go:229 via internal/mail/receiver.go:170, the Apply/After contract every mail kind uses); audit.md/work-session.md promise no ordering, and every other e2e test already polls. Fix: poll with `harnessWait` (8 lines). Local repro 10/300 → 0/200; PR #1 CI all green (run 36393535637). **Owner findings the same day:** Fly.io has NO spending cap and no billing alerts (docs.fly.io/about/cost-management: "We don't support billing alerts (yet)"; prepaid credits roll into normal billing when used up), so the "set a Fly.io spend cap" item for 4.1b cannot be done as written; Fly auto-stop must be off for the relay (`auto_stop_machines = "off"`). **OD-P4-1 re-decided: Hetzner (D37).** Also open for the owner: the repo is public, so the docs/plans are readable (making it private would cost the free Actions minutes). **G-4.0 signed by the owner (D38).** Next: wave P4-3 (4.2a accounts core, 4.4a release pipeline) + a small spec update Fly.io -> Hetzner (D37).

**2026-09-27: Phase 4 specs approved (D36).** Branch `p4/specs` (worktree `AgentNet-wt/p4-specs`): specs f21f0b1, review 50 cc77616, approval commit on top. Next: merge `p4/specs` to main, then wave P4-1 per `Docs/review/49-phase4-tickets.md` (4.0a Opus ∥ 4.0c Sonnet ∥ 4.1a Sonnet). Owner-side now: buy the Dorylinae domain, Fly.io account (EU region) + backup object storage at another provider, GitHub OAuth app, offline operator age key + offline release-signing key (with sealed backup copies), demo video. Carry into tickets: 4.7a stores feedback drafts as owner-only files under the config directory (owner-approved; no daemon migration), reviewed in R-4.6. OD-P4-20: the owner is looking for an outside reviewer early (booking takes 2–6 weeks; wave 2 is at beta week 4).

**2026-09-28: Phase 4 wave 2 MERGED, CI green** (c4a4227, run 36360173223 after one clean rerun of an unrelated one-off flake). **4.0b** abuse limits (token buckets, offline-queue caps, disk/memory bounds, --behind-proxy/--client-ip-header/--trusted-proxy) merged with **R-4.0**, the combined security review of 4.0a+4.0b+4.0c: found and fixed a real High (relay-wide inbound-read budget - any authenticated conn could pin ~1 MiB by never finishing a frame, OOMing a small host with a few dozen keys) and a Medium (/healthz had no rate limit). **VERDICT: the whole 4.0 set is ready for the owner's G-4.0 sign-off**, with two carry-forward conditions for ticket 4.1b: (1) the spec's default flag values (max-conns, max-inflight, disk thresholds) are unsafe for a small Fly.io box - reviewer's recommended defaults are in review 52; (2) do the M3-style rebase carefully when future relay tickets land. **4.4c** (agentnet doctor + relay state in status, review-clean: Probe() deliberately never authenticates, to avoid kicking the daemon's own relay connection) and **4.4d** (real Unix service install/uninstall in CI on ubuntu+macos runners, keystore 0600/keychain hardening) also merged.
Five CI issues surfaced and were resolved along the way (all diagnosed with evidence, none guessed): a real race in the R-4.0 H1 fix's regression test (server sends its reply before uncharging the read budget - INV-6, test-only fix, poll instead of assert-immediately); a real response-before-audit-write bug in internal/capability/fetch.go (fixed at the root: audit before the last response, not per-test workarounds like CI-FIX2's first attempt) - **known accepted trade-off:** if the LAST send itself fails, the audit row now reads "ok" not "io" (unavoidable, narrow, does not touch authorization/content); a macOS-only test that didn't follow this codebase's short-tempdir-for-real-daemon-sockets convention (CI-FIX2); and one confirmed one-off flake (a tempdir cleanup race in cmd/agentnetd, unrelated to any 2026-09-28 change, did not reproduce on rerun).
**Next: owner's G-4.0 sign-off**, then wave 3 (4.2a accounts core, 4.4a release pipeline per wave P4-3).

**2026-09-27/28: Phase 4 wave 1 MERGED and CI green** (1f4bbbe, run 36353336811). Owner approved the Phase 4 specs (D36) and dispatched wave 1: **4.0c** pairing v1 off by default + per-prefix pairing/redeem limits (fc0c188); **4.0a** relay auth v2, origin-bound, closes the review-50 relay-in-the-middle finding (c57237a; security review 51: 0 C/H, 3 M (1 fixed: loopback classification now checks the real TCP peer, not just the URL text), 8 L); **4.1a** relay migration system (R1), backup/restore, SIGTERM drain, --metrics-listen, journal extension point (a8b82cf). **INV-5** (1f4bbbe) fixed two CI regressions the merge exposed on Linux: a real race in the new drain waitgroup (Add(1) now happens under a lock before the goroutine starts, guarded by a closing flag) and a LATENT pre-existing bug in internal/audit/audit.go + internal/store/store.go (a cancelled-context BEGIN could skip the rollback and leave a transaction open on a pooled connection, surfacing as "transaction within a transaction" on the next call) - both reproduced failing-then-passing.
Also this session: renamed "billing team" -> "quota group" everywhere in the Phase 4 specs (owner: the beta is free, no payments, ever; "billing" was misleading leftover language from the plan). Clarified for the owner: inviting a friend/device to test and revoking them needs NO accounts/billing system - Phase 1-3's pairing + team invite + peers/team remove already does this once the relay is hosted (4.1a/4.1b), long before 4.2/4.3 (accounts, formal beta waves) land. Clarified the quota model: the monthly per-team cap is a SOFT warn-only limit in the beta (never cuts anyone off; a hard mode exists behind a flag, off by default); the real defence against overnight abuse is the always-on per-key/per-prefix rate limits (4.0b, not yet built) plus accounts being required to get in at all (max 8 seats/team) - there is still NO dollar circuit-breaker in the app, so **owner action item for ticket 4.1b: set a Fly.io account-level spending cap/billing alert** (independent of app logic) before going live.
**Next: wave 2** (4.0b abuse limits, 4.4c doctor, 4.4d Unix hardening per Docs/review/49-phase4-tickets.md wave P4-2), then R-4.0 (combined security review of 4.0a-c) and the owner's **G-4.0** sign-off gate before anything past that, including 4.1b (actual deployment).

**2026-09-25 (home PC): audit-inventory CI failures fixed** (2afb00d + gofmt). Later flakes were traced by INV-2/INV-3 (see the evening paragraph below); `TestFetchAuditRateLimitAndSummary` and `TestDecisionCLIRoundTrip` were timing, not product bugs. **Orchestrator lesson: no push notification when CI finishes; poll it (background watcher) instead of ending a turn on "waiting".**

**Owner's Phase 2 manual walkthrough (Windows 11, 2026-09-25): PASS** for the approval popup window (appearance/TopMost, toast code in title, approve flow, wrong-code reopen + 3rd-attempt auto-reject, Reject, no default button on Enter/Esc, --open without duplicates, Ctrl+C closes) and for headless/terminal mode (prompt format, typed code approves, redirected stdio refuses to start with exit 2, approve --open fails bad_request). Focus guard: a background console daemon can't take focus (documented in approval.md; not a bug). **Not done:** the long-summary popup check (needs a device-scope or debate-constraint approval; grant labels cap at 59+4 chars) and all of section 3 (own-device helper runs), both blocked because C: and D: carry an Authenticated Users:(M) ACL on the drive root, which correctly trips the D24/L11 writable_by_others check. Fix if wanted: `icacls C:\ /remove:g "NT AUTHORITY\Authenticated Users"` in an elevated PowerShell (it would also let the internal/daemon and internal/device tests run locally). Also untested: toast-history removal after a decision, and the no-desktop-session ("missing") case. (`phase-2` was tagged on the owner's OK, D35.)

**2026-09-25 evening (home PC): 3.H merged** (real Claude Code <-> agy run: both rounds PASS, ~590-600 s, 9 turns each, ~2.3 USD), **DX-2 merged** (inline debate flags, self-explaining errors, hint field, .claude/skills/agentnet-debate), **INV-2** (Windows CI slowness: SQLite fsync on C: temp; CI now uses RUNNER_TEMP + 20m timeout), **INV-3** (three test-side flakes), SH-FIX (bash 3.2 empty array). Weekly phase3-harness passes on all 3 OSes. **INV-4 merged (2fc3612): a REAL mail-outbox bug** (a failed hand-off's backoff UPDATE overwrote OnReady's send-now, so ws.cancel after a restart went out ~1 min late); fixed with an atomic ready counter + regression test. **Main is GREEN** on 3 consecutive CI runs (36177129433, 36177784616, 36178509061). Backlog from INV-4: `OnPeerOnline`'s reset can be overwritten the same way (per-peer, less critical); a hand-off into a dying connection stays `relayed` ~1 min. **Local trick to run internal/daemon + internal/device tests on this PC without changing system ACLs:** `subst P:` onto a folder you own with a tightened ACL (only you, SYSTEM, Admins) and point TEMP, TMP and LOCALAPPDATA at P: (remove the subst afterwards). **PHASE 4 SPECS (2026-09-26):** written (Opus) and adversarially reviewed (Opus): `Docs/review/49-phase4-tickets.md` (26 tickets, waves P4-1..7, OD-P4-1..21) + `Docs/review/50-phase4-spec-review.md` (0 C, 3 H, 12 M, 23 L, all H/M fixed) + `Docs/protocol/{relay-hosted,accounts,invites,telemetry,feedback}.md`. They live on branch `p4/specs` (worktree `AgentNet-wt/p4-specs`, commit cc77616, local, NOT merged, NOT pushed) until the owner approves the ODs. Key review findings: relay auth v1 does not name the relay (a hostile relay can forward a challenge and log in elsewhere as the victim): auth v2 origin-bound in ticket 4.0a; a same-host-proxy relay kept v1/pairing v1 (downgrade); quota-group admission needs `pair_admit` after tag_R verify; the CLI must open only https URLs on the relay origin. Ticket 4.0 (beta gate) is FIRST; nothing deploys before G-4.0. Owner decided OD-P4-8 (opt-in) and OD-P4-12 (unsigned wave 1) = D36; the rest is pending. B-4 merged (8e285e8): OnPeerOnline race fixed; backlog left: relayed rows on reconnect (spec change) and the spec-vs-code mismatch that presence-online should send all non-final rows.

**Tags done (D35):** `phase-2` (adcd27f) and `phase-3` pushed. **3.H2 done (65af926):** third attempt, Claude-initiated real round after DX-2: PASS agreed, 211 s, 6 turns, 0.54 USD vs 589 s, 9 turns, 1.06 USD before; `hint` field and self-explaining errors were used; the skill was read but never invoked via the Skill tool; caveats (one run, uncontested debate, gains not attributable to DX-2 alone) in tests/phase3-manual.md "### 3.H2". Attempts 1-2 (inconclusive, ~0.05 USD) showed the restricted Claude harness can stop before its first agentnet call (allowlist denies exploratory commands): harness now allows `Get-Content`, adds the CLI hint to the prompt, the Skill tool, the skill in the fixture, and pins `--model claude-opus-5-5`. **Future round needs:** several rounds including a contested debate, and a read-only listing allowance. **Now:** Phases 0-3 are code-complete and tagged (phase-0..phase-3); remaining owner-side items are the Phase 2 helper-run/long-summary manual checks (environment-blocked), tests/phase3-manual.md checks, the Phase 0/1 two-machine leftovers, and whatever Phase 4 planning the owner wants next.

**3.H is DONE and merged** (D34: real Claude Code <-> agy run, both PASS; branch p3/p3-harness deleted locally and on origin). Note: on this PC the private-dir tests fail locally (`C:` writable by Authenticated Users), so run internal/daemon and internal/device tests in CI only.

**(Historical, superseded by the paragraphs above: the work-PC park notes.)** Phase 3 is complete except **3.P**:
- Merged on main: 3.2, 3.1a, 3.1b, 3.4 (+3.4-i), 3.6a, 3.6b, 3.3a, 3.3b, 3.7, 3.9, DX-1; decisions D30–D33.
- **3.H work in progress** is pushed as branch `p3/p3-harness` (b36bd13): the stand-in debate mode passes locally (agreed and escalated), scripts `phase3-agents.ps1`/`.sh`, the weekly workflow `phase3-harness.yml`, the snippet's debate paragraph; details in `WIP-3.H.md` on that branch. **Left:** the ONE approved real run (Claude Code ↔ agy, both directions, ~1.5–3.5 USD), with results recorded in `tests/phase3-manual.md`. The branch has a "3.H results" section, and main now has 3.9's version of that file: merge both. Then gate, merge the branch, dispatch the weekly workflow once, check CI.
- **3.P:** push main; tag `phase-3` only with the owner's OK (and `phase-2` after the owner's Phase 2 manual checks).
- Owner checks pending: `tests/phase2-manual.md` (including the long approval-window summary) and `tests/phase3-manual.md`.
- Home PC: follow `Docs/orchestration/HOME-SETUP.md`; the boards are in `Docs/orchestration/board-snapshot.md`.

**Phases 0, 1 and 2 are code-complete, merged and pushed. CI is green on main, and the weekly Phase 2 harness workflow (stand-in agent) passes on Linux, Windows and macOS.** Phase 2 real-agent rounds (Claude Code ↔ agy) passed both ways (2.H).
The repository is **public** (github.com/Magazem/Dorylinae-Agentnet), so GitHub Actions is
free.

**Phase 3 started 2026-09-25** (owner: "start now"). Step 1 done (~17 min, Opus): specs committed on `p3/specs` (bc3c45f): Docs/protocol/{debate,decision,audit,experience}.md + Docs/review/42-phase3-tickets.md (migrations 18–21, OD-P3-1..13). Step 2 done (~14 min, Opus): review 43 (0 C, 3 H, 10 M, all fixed; new OD-P3-14), commit c834ed7 on `p3/specs`. Step 3 done: owner approved (D30); specs merged to main. **Step 4: tickets** per 42. 3.6a **merged** (6422f2f + review 44 fixes 52a8f4f: anchors checked after the walk; trigger refuses REPLACE/gaps). 3.2 + 3.1a **merged** (schemas, debate core, migration 19; review 45: H1 B checks A's close outcome against its own transcript (keep in front of 3.3a's derivation), M1 complete-before-accept ends invited debates). For 3.1b: review 45 L1 (ambiguous r- id → error), L2 (Sweep continues after a SweepOne error). 3.1b **merged** (debate IPC/CLI, ~2h10m on Sonnet; review 45 L1/L2 fixed). 3.4 **merged** (constraints + review 46: invisible characters refused, full summary in a scrolling approval window (manual check added to tests/phase2-manual.md); 3.4-i integration with 3.1b by Lite, ~15 min). 3.3a Decision **built** (~23 min, Opus; vectors match; worktree `decision`, rebased on main); security review R-3.3a slot `01a0d83e-bb76…` → Docs/review/47. 3.3b **merged** (decision output + review 48: H1 template-inert Markdown for Jekyll/Hugo etc., AST-based inertness test; M4 → D33). Running: 3.9 (Sonnet, slot `01a0d87a-e561…`, task `01a0d87b-1889…`, worktree `p3-e2e`) ∥ 3.H (Sonnet, slot `01a0d87a-e6b6…`, task `01a0d87b-421d…`, worktree `p3-harness`; one real-agent run approved). Then 3.P (phase-3 tag needs the owner's OK). 3.7 **merged** (experience record in every closing tx, migration 21; ~32 min Sonnet; cancelled_by is best-effort attribution). Then 3.3b + 3.7 (both after 3.3a), 3.9, 3.H. Then 3.3a (Opus, migration 20). 3.6b **merged** (agentnet log, audit_head, read-only fallback when the daemon won't start, TestAuditInventory, path-free install audit, D31). Also running: DX-1 owner hands-on rough edges (Lite, slot `01a0d7d9-75f8…`, task `01a0d7d9-b387…`, worktree `dx-polish`: `agentnet stop`, second-daemon message, no-command exit 2, `version` command, VCS build version, relay startup line). Keychain-per-home concern checked: AccountFor(home) already separates homes.

**Open items (all owner-side):**
1. **Owner's two-machine run.** The top of `tests/phase1-manual.md` lists the only checks
   that need two real machines (reboot survival, cross-OS, a relay on a real network, idle
   detection, a visible desktop toast). Everything else is automated in
   `tests/phase1-smoke.ps1` (~65 steps, ~8 s, one machine, no admin). The Phase 0 two-machine
   run (`tests/phase0-manual.md`) is also still owed. The owner has one PC without admin
   rights at work; they will run it at home when they have time.
2. **Tags.** `phase-0` and `phase-1` exist. `phase-2` waits for the owner's Phase 2 manual checks. Phase 2 manual checks (approval window per OS, device helper, Windows toast history) are in `tests/phase2-manual.md`.
3. **Codex CLI round of 1.H (optional).** 1.H passed with Claude Code + `agy` (Antigravity CLI)
   in both swapped rounds. Codex was blocked by the owner's account usage limit until
   2026-10-02; rerunning it then is optional.

**Phase 2 started 2026-09-23.** Step 1 done: specs committed on branch `p2/specs` (worktree
`AgentNet-wt/p2-specs`, commit d06c6d7): `Docs/protocol/{work-session,approval,grant,consult,device}.md`
+ `Docs/review/23-phase2-tickets.md` (15 tickets, migrations 14–17, OD-P2-1..15). Step 2 done:
`Docs/review/24-phase2-spec-review.md` (0 C, 3 H fixed, 11 M (10 fixed, M10 → OD-P2-6), 17 L),
commit faefa85. Step 3 done: owner approved all ODs (D16); OD-P2-6 (c) applied (4f28a2b);
specs merged to main. Self-hosted relays: D17.

**Wave P2-1 merged:** 2.2b (0e1b64a, 6b94bb1), 2.1a (01b50c0 + review 27 fixes 0313404), 2.2a (efc62fa + review 26 fixes 80724c0; placeholder 14 replaced).
**Wave P2-2 status:**
- 2.1b **merged** (f566c83 + D18 rework 543bc2e: `mail.Opened.Withhold` → the receiver stores signed='' at receipt). Not security-reviewed (not required by 23); **the 2.4 security review must also cover the D18/withhold path** in internal/mail/receiver.go and worksession receive/cancel.
- 2.2c **merged** (1ceb40e, 9503c6c review 28, bb46694 integration: approvals via window/Store.Confirm; the grant Perform returns an AfterCommitter that wakes the outbox). Review-28 leftovers: L8 (→ 2.4), L9 (grant.orphan audit lacks `grant`), L6/L7 notes.
- 2.5 **merged** (7a68d33 + review 32 fixes a4b1093).
- 2.3a **merged** (fetch server + fs; review 34: 0/0/3 fixed; open Lows L3–L6, L6 = COM0/LPT0 needs a vector change, owner call later).
- 2.4 **merged** (quarantine + L8; review 35: H1 stale release approval now bound to seq, H2 D18 gaps closed). Noted Lows: pre-2.4 revoked rows have NULL approval; SQLite secure_delete off/WAL keeps discarded bytes (→ backlog); session.quarantined text vs work-session.md.
- 2.D1 **merged** (device link, migration 17; review 36: M1 mail_submit could forge device.* kinds, fixed). Open from review 36: L4 (spec: offer freshness + ignore offers older than the last unlink), L5 (notify on activation: implement or amend the spec), L6 (link ids may differ per side, so 2.D2 must not key on it), L7 (mail_submit allowlist for all daemon-owned kinds), L8 (approval OnReject hook, needed by 2.D2).
- 2.3b **merged** (git serving; review 37: M1 exact branch ref via for-each-ref, M2 real git.exe on Windows). Open: L2 → D23 (enforce in 2.9); L3 include/alternates can read local paths (the grantor's own config).
- FIX-CI **merged** (review 39): stat/list/read refuse ANY symlink (final component too); in-flight slots are freed before the last response (a real bug for sequential holders). No security impact.
- 2.D2 helper runner (+ review-36 L7 mail_submit allowlist, L8 OnReject, D22): **Opus trial** P2-Opus-HelperRunner slot `01a0d2ee-b28c…`, task `01a0d2ee-ea1d…`, worktree `helper-runner`.
- 2.3c **merged** (fetch client + CLI; review 38: 0/0/4 fixed). Open: L3 fs `changed` can't detect same-size rewrites (protocol change → owner, Phase 3). 
- 2.D2 **merged** (helper runner + mail_submit allowlist + OnReject + D22; review 40: M1 bidi/zero-width in approval text, M2 Linux Pdeathsig (macOS gap documented)). Runner output control chars become "?" (not U+FFFD). L1/L11 → D24 (2.D3).
**Final wave (P2-4):**
| Ticket | Model | Slot | Task | Worktree |
|---|---|---|---|---|
| 2.D3 (D24) | Opus | **merged** (review 41: link-hop walk, Linux POSIX ACLs, watch expiry). Open: L5 UNC/NFS paths trust the file server admins (owner call later); L4 macOS ACLs not read. | | |
| 2.9 | Sonnet | **merged** (whole-loop e2e, TestPhase2AuditHasNoContent, D23 git version check, status `git` field, docs fixes) | | |
| 2.N (D25) | Sonnet | **merged** (session.result on A, session.changes on B; content-free, after commit) | | |
| 2.H | Sonnet | **merged** (stand-in PASS; real rounds Claude Code↔agy PASS both ways, 157 s / 163 s; ~0.2–0.4 USD per Claude run). The .sh and Linux/macOS legs are first exercised by the weekly workflow `.github/workflows/phase2-harness.yml` (manually dispatched once at merge). | | |
Then 2.D3 security review, 2.P push (a `phase-2` tag only with the owner's OK).
- 2.2d **merged** (2dc1b3d, aeb1680 AfterCommitter, 1664499 review 30 fixes). Review-30 notes L6/L7/L10 on the Sticky Board.
- CI race failures after 2.2d: test-only races in approval/daemon fakes, fixed in 71db65d (review 31); store.go was fine.
- 2.5 consult: P2-Consult slot `01a0ce43-22be…`, task `01a0ce43-450c…`, worktree `consult`.
`tests/phase2-manual.md` exists (created by 2.2d; includes the 2.2a toast-history check).
Review-26 caller rules for 2.2c/2.4/2.D1 (N1–N5 in Docs/review/26): all state checks in Precondition (a Perform error leaves the approval pending); Perform writes only through tx; hooks return *ipc.Error; hooks run under Store.mu and must never call back into the Store; drop the pending row if Create fails; peer text can still show a decoy code. Review-26 L7 is resolved by D19 (2.2d).
**2.2b merged** (0e1b64a + review 25 fixes 6b94bb1). Notes for **2.2c** (from review 25): store/compare `canonical(token)` never raw bytes; never audit `err.Error()` from capability (use `ReasonOf`); add a helper returning the canonical token; review-25 L9 (grant.md §Paths: CONIN$/CONOUT$, COM/LPT with superscript digits, C1/bidi chars) goes into the 2.3a fs ticket.
**Wave P2-3 in progress** (Sonnet; each gets an Opus security review):
| Ticket | Slot | Task | Worktree | Migration |
|---|---|---|---|---|
Then: 2.3b (git serving), 2.3c (fetch client + e2e), 2.D2 (helper runner), 2.9, 2.H, 2.P.

**Phase 2** (plan: grants, consult, sessions; see the build plan) plus the own-device
helper (D13). Follow the same flow as Phase 1:
1. An Opus worker writes the Phase 2 specs **first** (docs only): grants/capabilities (plan says
   Biscuit), consult, sessions, D13 device link + helper scope, and a ticket plan like
   `Docs/review/11-phase1-tickets.md` with pre-assigned migrations (next free: **14**) and
   review markers.
2. An Opus adversarial spec review, fixing in place.
3. **Owner approval** of the specs and any open decisions (OD list) before any code.
4. Then implementation tickets in dependency order, Opus security review on the sensitive ones.

**Backlog (not blocking):** review Lows in `Docs/review/07`–`22`; 05-review M1 (direct path
at-most-once); **M2 (relay abuse limits + TLS) is a beta gate (D17)**; relay pairing limits L1/L5 (08b)
before hosted relay; a dedicated `stale` ack status instead of `unsupported`; `
team-invite table prune and `team_delete` not cancelling pending invites (18).

## 1. How this team runs

- **Roster.** Only the Orchestrator is standing. No Butler/Manager/Mailbox exists: **the user
  is the Manager** and makes every decision; talk to them directly.
- **Workers** (spawn yourself, one ticket each, retire after merge):
  | Template | Work-PC id | Home-PC id (paths: repo `C:\Users\yazan\Documents\AgentNet`, worktrees `...\AgentNet-wt`, Go `C:\Program Files\Go\bin`, lint `%TEMP%\gl\golangci-lint.exe`) | Use for |
  |---|---|---|---|
  | Worker-Haiku | `custom-1789380226358-958d` | `custom-1790347508758-2a2d` | read-only summaries, mechanical work |
  | Worker-Sonnet | `custom-1789380226495-f6d5` | `custom-1790347507696-70dd` | well-specified implementation |
  | Worker-Sonnet-Lite | `custom-1790318917893-ab35` | `custom-1790347508050-a205` | **default for routine, well-scoped tickets** (D28, under watch) |
  | Worker-Opus | `custom-1789460333299-bbb1` | `custom-1790347508399-8e25` | specs, security reviews, investigations/hard debugging, security-critical or OS-level code (D26) |
  Home templates are named with an "AgentNet " prefix. Rule 2's worktree path on this PC is `C:\Users\yazan\Documents\AgentNet-wt\<name>`.
  Owner has approved Opus for specs and security reviews.
- **Dispatch** with `team_task_create owner=<slot>`. New workers often say "ready, no task"
  before the task arrives: send one short `team_send_message` pointing at the task id. If a
  worker still does nothing (empty worktree), see §5 on stuck workers.
- **Sequencing:** never tell a worker to wait for another. Dispatch when prerequisites merge.
  Building on an unmerged branch is fine; rebase later with `--onto` (§5).

## 2. Hard rules (do not break)

1. **Workers cannot run git** (their rules forbid it; read-only git for scans is the only
   exception). The Orchestrator commits, rebases, merges and pushes. Every task says
   "No git commands; list every file changed."
2. **One worktree per worker** under `C:\Users\ysuliman\Documents\AgentNet-wt\<name>`, branch
   `pN/<name>`. Workers stay inside their worktree.
3. **Specs before code.** Opus adversarial review, then explicit owner approval.
4. **Crypto/security tickets get an Opus security review before merge.**
5. **Merge gate:** worker reports + you run in the worktree: `go build ./...`, `go vet ./...`,
   `go test ./... -count=1` **×2**, `go run ./tools/verifyvectors`, and
   `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run ./...` (only
   CRLF gofmt noise allowed). Cross-vet for Unix when touching OS-specific code:
   `GOOS=linux GOARCH=amd64 go vet ./...`, `GOOS=darwin GOARCH=arm64 go vet ./...`. **Also lint per OS:** install the linter once
   (`GOBIN="$TEMP/gl" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2`), then run
   `GOOS=linux "$TEMP/gl/golangci-lint.exe" run ./...` and the same with `GOOS=darwin`. Windows lint
   skips `_linux.go`/`_darwin.go`, and CI lints on Linux. `GOOS=linux go run …golangci-lint…` does
   NOT work: it builds a Linux binary that can't run here, and the error is easy to miss.
6. **Safe commit sequence** (1.0f work was once lost): stage null-safely
   (`git diff --ignore-cr-at-eol --name-only -z | xargs -0 -r git add --` plus untracked via
   `git ls-files --others --exclude-standard -z | xargs -0 -r git add --`), commit, verify
   `git diff --ignore-cr-at-eol --name-only` and untracked are empty, **only then**
   `git checkout -- .` (EOL noise), rebase onto main, `git merge --ff-only`, remove the worktree.
   Chain with `&&` / `set -e`, never `;`.
7. **Authorized:** commit, merge and push `main`. **Not authorized:** tags, releases,
   force-push, repo settings. Ask first.
8. Commit messages end with the Co-Authored-By line given in the current system reminder
   (currently `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`).
9. **Owner decisions (§3) are final.** Reopen only for a concrete security/correctness flaw,
   and say so.
10. **Parking:** when the owner says park, do NOT send an abrupt STOP. Ask workers to finish
    the current step, make sure the tree builds, write WIP notes and report; then
    snapshot-commit their worktree and update §0.
11. **Watch CI after every push** (`gh run list --branch main --limit 3`; for failures
    `gh run view <id> --log-failed`). CI stayed red unnoticed for ~60 pushes once. Batch
    pushes (one per merged ticket, not per note).
12. **No admin on the owner's work PC.** Never run anything that installs services or needs
    elevation there (e.g. `agy --sandbox` raises a UAC prompt).
13. File bugs/to-dos/decisions on the Sticky Board (`sticky-board` skill); close them when done.

## 3. Owner decisions (final)

| # | Decision |
|---|---|
| D1 | Licence **PolyForm Shield 1.0.0** (public source for review, no competing products). |
| D2 | Durability via **sealed mail** (HPKE to rotating identity-signed mailbox key + inner Ed25519 sig), **sender outbox** until app ack, **receiver dedupe**. Noise XX only for ping/interactive. Persisted Noise state and IK/KK rejected. |
| D3 | Pairing **v2 code-bound MAC**: 15-char code (5 lookup + 10 secret), Argon2id, transcript over canonical cards + mailbox keys. PAKE deferred. Fingerprints + `peers verify/remove`. |
| D4 | 15-char code accepted now. |
| D5 | Refuse requests from `trust=relay` peers on a hosted (non-loopback) relay. |
| D6 | Non-repudiation accepted (signed mail). |
| D7 | Relay telemetry counts only `mail`; per-kind counts daemon-side. |
| D8 | Spec-writer choices for pairing/mail (canonical transcript, lookup lifetime, 60 s confirm timeout, key life 7+7 d / delete at 21 d, unknown kinds acked `unsupported`, outbox keeps signed plaintext). |
| D9 | Redeemer sends tag_R first; issuer counts attempts at `pair_peer` (max 3); used-code record persisted in SQLite. |
| D10 | Outbox `expired` = "delivery unknown"; receivers reject app mail older than 14 d; resubmits idempotent. |
| D11 | Phase 1 specs approved with OD-P1-1..13 as recommended **except** OD-P1-11 → `request.cancel` (from delivered/deferred, refused after accept). Single owner (OD-P1-1) and lost-owner-device (OD-P1-10) are documented limitations (`Docs/beta/known-limitations.md`). |
| D12 | Cancel gives **no** urgency-budget refund. |
| D13 | **Own-device helper (Phase 2):** separate `device` trust never created by team/roster/pairing; dedicated link flow confirmed on BOTH devices; only the obeying device can make itself a helper and holds the scope locally (request types, repos/paths, commands, expiry); off by default, audited, revocable from either side; out-of-scope requests go to the normal inbox; one-way hierarchy. |
| D14 | Optional size-capped **result payload on `request.complete`** (status, summary, exit_code, output ≤32 KiB, artifacts). Implemented in 1.6a. |
| D16 | **Phase 2 specs approved (2026-09-23)**, OD-P2-1..15 as recommended in `Docs/review/23-phase2-tickets.md`: in-house signed token (not Biscuit); desktop approval code with the stated boundary (agents with a raw shell need harness confinement; OS user-presence = Phase 3/4 hardening); DB grants deferred; requester-only grants; **OD-P2-6 = (c)** (from `quarantined`: discard, or request changes without release, result deleted unseen); every accept opens a work session; weekly CI with a scripted stand-in agent, real agents manual before releases. |
| D17 | **Self-hosted relays in the beta:** the relay stays untrusted whoever runs it; D5 "non-loopback" includes self-hosted relays; fetch limits are daemon-side caps (grant.md). Review-05 **M2 (relay TLS + abuse limits) is a GATE BEFORE THE BETA**, not a Phase 2 ticket. |
| D18 | Review-27 H1: a discarded / not-released quarantined result (and dropped early-complete content) is also blanked or deleted from `mail_inbox` in the same tx; ack/dedupe metadata stays. Implemented in 2.1b. |
| D19 | Ticket **2.2d**: a daemon-owned approval window. The code is typed only there, so agents never handle it; CLI code entry is removed on desktop machines, and headless machines keep terminal mode (OD-P2-3). The spec comes first, then the build and an Opus security review. **Merges before 2.2c.** Replaces review-26 L7. |
| D20 | OQ-2.2d-1: the Linux approval window (zenity/kdialog) gets the summary text via argv. Accept and document it: other local users can see the summary (never the code); hidepid=2 hides it. |
| D21 | **Model trial (2026-09-24):** gradually move complex implementation and investigations to Opus 5.5 workers and measure against Sonnet (`Docs/orchestration/model-trial.md`: minutes, follow-ups, gate failures, review findings, CI after merge). Sonnet stays the default for well-specified tickets. Plan: INV-1 flake investigation (running), then 2.3b git serving and 2.D2 helper runner on Opus, with 2.3c on Sonnet as a control. |
| D22 | Review-36 device link: **L4** tighten offer freshness (now < offer.at + 10 min; ignore offers older than the last unlink from that peer); **L5** content-free desktop notification when a device link becomes active. Both folded into 2.D2. |
| D23 | Enforce **Git ≥ 2.32** at daemon start. If the git found is older, git.read grants are refused (`unsupported`) with a clear message; fs serving is unaffected. Folded into 2.9. |
| D24 | Review-40 helper runner: **L1** clearing the scope, unlinking, or scope expiry **kills a running command's process tree** (reported as cancelled). **L11** refuse a program (or its folder) that users other than the owner (or admins) can write, checked at scope-set and at every run. Ticket 2.D3. |
| D25 | Add content-free notifications **session.result** (A: a result waits for your accept; not sent when quarantined, which has its own event) and **session.changes** (B: the requester asked for changes). No session.closed (request.completed/cancelled cover it). Ticket 2.N. |
| D26 | **Model policy (after the D21 trial, `Docs/orchestration/model-trial.md`):** Sonnet (Sonnet 5) is the default for well-specified implementation tickets. Opus (Opus 5.5) is for investigations and hard debugging, security-critical or OS-level code (process control, permissions, crypto, parsing input from peers), specs, and security reviews. |
| D27 | **Trial: Sonnet with thinking off** for routine, well-scoped tickets. New template **Worker-Sonnet-Lite** `custom-1790318917893-ab35` (Sonnet, thought_level=off, same worker rules as Worker-Sonnet). First pair: B-1 (Lite) vs B-2 (normal Sonnet control), both small backlog items. Results in `Docs/orchestration/model-trial.md`. |
| D28 | **Worker-Sonnet-Lite (Sonnet, thinking off) is the default for routine, well-scoped tickets** (backlog items, fixes, portability), under watch: keep logging every Lite ticket in `Docs/orchestration/model-trial.md`, and if a Lite ticket needs rework or misses spec, report it to the owner and fall back to normal Sonnet for that kind of work. Normal Sonnet for feature tickets with design choices; Opus per D26. |
| D29 | Owner's Phase 1 two-machine run **passed** (2026-09-25); tag `phase-1` on 6baf246 (the Phase 1 close). Phase 2 manual checks are pending (owner runs them in the evening); `phase-2` tag after that. |
| D30 | **Phase 3 specs approved (2026-09-25)**, OD-P3-1..14 as recommended in `Docs/review/42-phase3-tickets.md`: one-sided commit inside the request; new request type `debate`; constraints approval-gated (window); no grants in debates; daemons sign automatically, and only a two-signature Decision is confirmed (single-signed exports with an UNCONFIRMED banner, verify exit 6); manual audit anchors; experience record has no read command; review-38 L3 → 4.8; OS user-presence → Phase 4; secure_delete ON (3.9). 3.H: one real-agent run near the end. |
| D31 | Review 44: an audit anchor on a pre-chain (legacy) row always reports `anchor_mismatch` (tampering), not bad_request. Changes one D30 acceptance line; implemented in 3.6b. |
| D32 | `github.com/yuin/goldmark` allowed as a **test-only** dependency (3.3b), to prove the Decision Markdown is inert; it must not be linked into shipped binaries. |
| D33 | Review 48 M4: **decision exports are not audited** (a local read, like `agentnet log`); the `decision.export` row is dropped from the spec (3.9). **Revisit in the next large security review:** the alternative (a small IPC call that audits exports with no content). Listed in Docs/beta/known-limitations.md. |
| D34 | Home PC has no `agy`: owner chose to install it and run the original Claude Code <-> agy 3.H rounds (not Claude<->Claude). |
| D35 | 2026-09-25: owner OK to tag **phase-2** (on adcd27f, the Phase 2 close; helper-run manual checks stay open, environment-blocked, not gating) and **phase-3** (on main after CI green x3). Owner approved one ~1 USD Claude-initiated real debate round to confirm DX-2 (inline flags + skill) cuts wasted turns; results go in tests/phase3-manual.md. |
| D36 | **Phase 4 specs approved (2026-09-27)**, OD-P4-1..21 as recommended in `Docs/review/49-phase4-tickets.md` (as amended by review 50) **except**: **OD-P4-7** beta invite codes expire after **48 hours** (not 30 days), the owner reissues on request (10/10/10 teams, 8 seats unchanged); **OD-P4-12 = (c)**: wave 1 ships **unsigned** Windows builds, SmartScreen click-through documented (owner is an EU/UK individual: Azure Artifact Signing is US/Canada-only for individuals; Certum Cloud Individual is the fallback if signing is wanted later); **OD-P4-16 = (c)**: an agent's `feedback --yes` only drafts (owner-only files under the config directory, no migration), a human sends; **OD-P4-19 = (a) + (ii)**: plain Ed25519 over `SHA256SUMS`, key offline, CI publishes a draft release and the owner signs locally; **OD-P4-20** decided during wave 1, before wave 2. Confirmed choices worth naming: Fly.io in an EU region (OD-P4-1), GitHub OAuth only (OD-P4-2), telemetry report opt-in (OD-P4-8), agent-drafted privacy note reviewed by the owner, no paid legal review (OD-P4-15). |
| D37 | 2026-09-28: **OD-P4-1 changed: relay hosting = Hetzner Cloud** (small shared VM in an EU location, flat monthly price), replacing Fly.io from D36. Reason: Fly.io has no spending cap or billing alerts (docs.fly.io/about/cost-management) and the owner wants a predictable bill; Hetzner also matched independent advice the owner gathered. Deploy is host-neutral (one plain VM: relay binary + Caddy for TLS + systemd/compose, OS auto security updates, firewall; no provider-specific services), so a later move is a DB copy + DNS change. Backups stay at a different provider. Review-52 sizing applies (>= 512 MB-class VM; `--max-conns 2000 --max-inflight 48MiB`, `GOMEMLIMIT=400MiB`, disk limits scaled to the volume). Google Cloud trial credit: not part of the plan (optional staging use still open). Specs (49 OD-P4-1, 4.1b, relay-hosted.md) to be updated by a worker before 4.1b. |
| D38 | 2026-09-28: **Gate G-4.0 SIGNED by the owner.** Review-05 M2 (relay TLS + abuse limits; 4.0a, 4.0b) and review-08b L1 (pairing v1 off by default; 4.0c) and L5 (per-prefix pairing limits; 4.0c) are closed, on the evidence of R-4.0 (`Docs/review/52-4.0-r40-review.md`). Carry into 4.1b: review-52 sizing and D37 (Hetzner). Unblocks wave P4-3 (4.2a, 4.4a) and, later, deployment. |
| D15 | Consumer onboarding = plan item **4.9** (install → one `agentnet setup` → one connect command; agent-runnable; clean machine on 3 OSes; no config files). Don't rush it. |

Still open (not urgent): `Docs/` vs `docs/` casing; OD-P4-20 outside security review (before
wave 2, D36). Accounts and code signing are settled by D36; hosting by D37 (Hetzner).

## 4. Phase 1 summary (what exists)

Specs: `Docs/protocol/{pairing,mail,team,presence,request,notify,ipc,envelope}.md`; tickets and
ODs: `Docs/review/11-phase1-tickets.md`; reviews 12–22 in `Docs/review/`.

| Area | Tickets | Notes |
|---|---|---|
| Mail stack | 1.0a–f | sealed mail, mailbox keys + rotation, dedupe/ack, outbox, 14-day limit |
| Pairing v2 | 0.8a–e | code-bound MAC; independent vector checker `make verify-vectors` |
| Teams | 1.1a–d | trust `team` + introductions, roster epochs, invite/join via pairing |
| Presence | 1.2a–d, 1.3 | ephemeral relay type, sealed fixed-size heartbeats, idle detection, visibility modes |
| Requests + inbox | 1.4a–c, 1.6a–b | schema + caps, submit/offline queued, lifecycle, cancel, result payload |
| Urgency | 1.7 | 5 high / 2 blocking per sender per rolling 7 d, weighted priority |
| Notifications | 1.8a–b | own per-OS desktop notifier (no beeep), signed webhook with dial-time SSRF checks |
| E2E + audit | 1.9 | offline lifecycle test, audit contains no content |
| Agents | 1.H | `tests/harness/phase1-agents.ps1/.sh`; Claude Code ↔ agy pass both rounds; snippet `Docs/agents/snippet.md` |
| Smoke | — | `tests/phase1-smoke.ps1/.sh` (one machine, no admin) |

Migrations on main: **1–13** (12 = requests_result, 13 = webhook_queue). Every new migration
must also add its tables to the DROP lists in BOTH rewind tests in `internal/store/store_test.go`.

## 5. Lessons (environment and process gotchas)

- **CRLF:** `core.autocrlf=true`; worktrees show many "modified" files with no content change.
  Use `git diff --ignore-cr-at-eol`. Local gofmt/lint noise is CRLF-only; check with
  `tr -d '\r' < f | gofmt -l`. `gofmt -w` rewrites line endings: only on changed files.
- **Go** at `%USERPROFILE%\tools\go\bin`; in Bash `export PATH="$USERPROFILE/tools/go/bin:$PATH"`.
  No cgo: `-race` runs only in CI. `python` fails (uv trampoline); use node or the Edit tool.
- **Windows vs Unix:** local tests pass on Windows (named pipes) while Unix sockets fail (e.g. a
  missing socket dir). Only CI catches it: watch it.
- **Windows PowerShell 5.1** lacks .NET Core APIs (`Process.Kill($true)`, `ArgumentList`); use
  `taskkill /T /F`. `$Home` is reserved. Wrap `Where-Object` results in `@()` before `.Count`.
- **Headless agents on Windows:** Claude Code's shell tool is **PowerShell**, not Bash; isolate
  connectors (`--strict-mcp-config`, empty `--mcp-config`, `--setting-sources project`).
- **Machine load:** with 4+ workers each running `go test ./...` at the same time, IPC tests time out (i/o timeout in pairing/ping/mail/e2e). Keep at most ~3 implementation workers at once, and re-run failing packages alone before blaming a change.
- **Symlink tests are skipped on this Windows machine** (no symlink privilege), so symlink behaviour is only tested in CI. After merging fs/fetch code, check that CI is green before building on top. TestFetchSymlinkEscapes was red on every OS from the 2.3a merge until FIX-CI.
- **GitHub API rate limit:** 5000/h shared by me and every worker. Poll CI sparingly (one `gh run list` per merge), and tell workers to avoid gh unless needed.
- **Schema-rollback tests** (tests that DELETE FROM migrations WHERE version > N and replay) must also undo every later migration that ALTERs a table (e.g. migration 19 adds work_sessions.kind). Otherwise the replay fails with a duplicate column. See 3ceb049. Tell every ticket that adds a migration.
- **Golden files and line endings:** with core.autocrlf=true, text fixtures (.md, .txt) come out as CRLF on Windows (and Windows CI), so golden tests that compare bytes fail there. Pin them in .gitattributes (`text eol=lf`), as done for internal/decision/testdata/*.golden.md.
- **A worker you have asked to shut down may still be finishing** a follow-up message (it happened with 3.3b applying L7): check `git status` in the worktree after the "removed" notice and commit any late changes.
- **-race runs only in CI** (no cgo here). Every implementation task must say: guard test variables written by goroutines (Perform/AfterCommit hooks, window/notifier fakes) with a mutex or an atomic (review 31). After merging concurrency code, watch the CI `race` job.
- **Test flakiness patterns:** never read async state/audit once; poll with a deadline. Tests
  that write files use `internal/testutil.TempDir(t)` (Windows AV holds deleted files).
- **CLI:** base64url keys can start with `-`; `parseInterspersed` handles it (don't regress).
- **Workers:**
  - Paused after "could not process queued message after 3 delivery attempts": use
    `team_interrupt_agent` to resume from the files in its worktree; don't respawn.
  - Stuck (idle, empty worktree): interrupt once; if still idle, retire it, **wait for
    "Teammate X was removed"**, delete its task, then spawn a fresh worker with a NEW task in
    a NEW worktree (the board is visible to all workers; a "stopped" worker once picked up
    its successor's task).
  - Mark a worker's tasks completed before retiring it, or it declines shutdown.
  - New workers very often say "ready, no task", then go idle without starting, even after the pointer message. If a new worker is idle with an empty worktree at the next check, `team_interrupt_agent` with "start task <id> now" straight away (this happened with most Phase 3 workers).
  - A worker can approve its shutdown and still stay on the team (seen with P3-Review-3.4). After a shutdown, wait for "Teammate X was removed"; if it doesn't come, check team_members and re-issue team_shutdown_agent.
  - Spec/docs workers read for a long time and write files late. `idle` notifications
    while the task is `in_progress` are not proof of being stuck: the P2 spec writer was
    retired as "stuck" yet delivered everything. Wait for its report (or a long silence)
    before retiring it.
  - Workers report a vanished worktree after you merge: expected.
  - Worker summaries are leads, not truth: verify key claims before telling the owner.
- **Branch on an unmerged branch:** after the base merges with review fixes, rebase with
  `git rebase --onto main <old-base-commit> <branch>`.
- **Migrations across parallel branches:** pre-assign numbers; a branch built early uses
  clearly marked NO-OP placeholders that you replace at merge (never merge a placeholder).
- **Merge conflicts** so far were always "both sides added independent code": keep both.
- **CI cost:** the full matrix on every push cost ~$18 in a day while the repo was private.
  It's free now (public). If it ever goes private again, apply a CI diet (paths-ignore docs,
  concurrency cancel, Linux-only per push, full matrix nightly/pre-tag).

## 6. Document index

| Path | What |
|---|---|
| `Docs/AgentNet Free Tier Build Plan.md` | The plan (phases 0–4, gates, 4.9 simple setup) |
| `Docs/protocol/*.md`, `Docs/cli/*.md` | Authoritative specs and per-command docs |
| `Docs/review/05-expert-review.md` | Phase 0 review (defects, backlog M1/M2) |
| `Docs/review/06-pairing-session-options.md` | Pairing/session design and Wave 1–4 tickets |
| `Docs/review/11-phase1-tickets.md` | Phase 1 ticket plan and OD-P1 decisions |
| `Docs/review/07`–`22` | Spec and code reviews (open Lows live here) |
| `Docs/agents/snippet.md` | CLAUDE.md / AGENTS.md snippet for agents |
| `Docs/beta/known-limitations.md` | Beta limitations (single owner, lost owner device) |
| `tests/phase0-manual.md`, `tests/phase1-manual.md` | Owner's manual checklists (two-machine parts at the top) |
| `tests/phase1-smoke.ps1`, `tests/harness/` | One-machine smoke; headless agent harness |
| `Docs/orchestration/board-snapshot.md` | Open Sticky Board notes + task-board note, taken at park (for moving PCs) |
| `Docs/orchestration/HOME-SETUP.md` | How to replicate this team (templates, corrected Orchestrator rule, workflow, lessons) on another PC |
| `Docs/orchestration/history.md` | Archived full handoff (per-wave detail, commit ids) |
