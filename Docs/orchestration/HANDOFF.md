# Orchestrator handoff

Read this first if you are a fresh Orchestrator instance taking over this project.
It is the single source of truth for *where we are*. Update it after every merge,
every owner decision, and before you expect a context reset.

Last updated: 2026-09-21, Wave 4 in flight.

## 1. How this team runs

- **Roster reality.** The AionUi team has only the Orchestrator (you) as a standing
  member. There is no Butler, Manager or Mailbox on the roster. **The user acts as
  Manager**: they make every decision, and you talk to them directly.
- **Workers.** You spawn them yourself with `team_spawn_agent` from these templates,
  one ticket per worker, and retire each (`team_shutdown_agent`) once its work is
  merged:
  | Template | assistant_id | Use for |
  |---|---|---|
  | Worker-Haiku | `custom-1789380226358-958d` | read-only summaries, mechanical work |
  | Worker-Sonnet | `custom-1789380226495-f6d5` | well-specified implementation |
  | Worker-Opus | `custom-1789460333299-bbb1` | specs, security review, design research |
  The owner has approved Opus for specs and security reviews (about 4 per wave).
- **Dispatch.** `team_task_create` with `owner=<slot_id>` wakes the worker, so no
  separate message is needed. A new worker's first "ready, standing by" message was
  sent before the task reached it; ignore it.
- **Sequencing.** Never dispatch dependent work with a "wait until X" instruction.
  Dispatch the next wave only after the previous one is merged.

## 2. Hard rules (owner-set or learned; do not break)

1. **Workers cannot run git.** Their rules forbid it. The Orchestrator commits, rebases
   and merges. Every task says: "No git commands; leave changes uncommitted; list
   every file changed."
2. **One git worktree per worker**, under `C:\Users\ysuliman\Documents\AgentNet-wt\<name>`,
   on branch `wN/<name>`. A worker must work only in its own worktree.
3. **Specs before code.** Protocol specs need an Opus adversarial review, then the
   **owner's explicit approval**, before any implementation starts.
4. **Crypto and security tickets need an Opus security review before they merge**
   (0.8c, 0.8d, 1.0c, and 1.0e's crypto paths).
5. **Merge gate:** `go build ./...`, `go vet ./...` and `go test ./... -count=1` pass
   in the worktree, the acceptance tests exist as automated tests, and any required
   review is clean. Then commit on the branch, rebase onto `main`, and `git merge --ff-only`.
6. **Owner-authorized:** committing and merging to `main` after each wave passes review, **and pushing `main` to origin after merges** (authorized 2026-09-21).
   **Not authorized: tags, releases, force-push.** Ask first.
7. Every commit message ends with `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`.
8. **Owner decisions below are final.** Reopen one only for a concrete security or
   correctness flaw, and flag it to the owner explicitly.
9. File bugs, to-dos and decisions on the Sticky Board (`sticky-board` skill). Close
   items when they are fixed.
10. The user's context resets are expected. Keep this file current.

## 3. Owner decisions (final)

| # | Decision | Where recorded |
|---|---|---|
| D1 | Licence: **PolyForm Shield 1.0.0**. Free to use, and the source is public for review; no competing products. | `LICENSE`, README |
| D2 | Session durability: **sealed mail** (HPKE to a rotating, identity-signed mailbox key, plus an inner Ed25519 signature), a **sender outbox** that holds each message until app-level ack, and **receiver dedupe**. Noise XX is kept for ping and interactive traffic only. Persisting Noise state was **rejected** (nonce reuse on rollback). IK/KK was **rejected**. | `Docs/review/06-...md` §B, §7 |
| D3 | Pairing: **v2 code-bound MAC**. 15-char code (5 lookup + 10 secret), Argon2id, transcript over canonical cards and mailbox keys. PAKE deferred to v3 (Go libraries unaudited). Fingerprints plus `peers verify/remove`. | `Docs/protocol/pairing.md` |
| D4 | Accept the 15-char code now. | 06 §7 |
| D5 | Refuse requests from `trust=relay` peers on a hosted relay, once a re-pair path exists. | 06 §7 |
| D6 | Accept non-repudiation (signed mail). | 06 §7 |
| D7 | Relay telemetry counts only `mail`; per-kind counts are kept daemon-side. | 06 §7 |
| D8 | The spec-writer's 5 choices: canonical-JSON transcript; lookup kept until `pair_cancel`, TTL or 3 redemptions; errors `pair_lookup_taken` and `pair_v1_disabled`; 60 s confirm timeout counts as a failure; no check char; 0.8c creates the first mailbox key; mail key life 7 d current + 7 d retired, deleted at t+21 d; unknown kinds acked `unsupported`; outbox keeps signed plaintext for re-seal. | pairing.md, mail.md |
| D9 | From the spec review: **the redeemer sends tag_R first**; the issuer counts attempts at `pair_peer` (max 3); the redeemer's 24 h used-code record is **persisted in SQLite** (`pair_used_codes`). | 06 §7, pairing.md |
| D10 | Review 10 M3: outbox `expired` means **"delivery unknown"**; receivers reject app mail older than 14 d; Phase 1 resubmits must be idempotent. Being implemented in 1.0f. | mail.md (after 1.0f) |

Still **open** (not urgent): relay hosting (Fly.io vs Hetzner) and account binding
(needed by 4.1/4.2); `Docs/` vs `docs/` casing; work rhythm (part-time vs full-time).

## 4. Where things are

### Done and on `main`
| Commit | What |
|---|---|
| `2078f1f` and earlier | Phase 0 as built by the original harness (tag `phase-0`) |
| `0785aa6` | Review docs 01–06, LICENSE, README licence line |
| `395ee48` | H2: relay `--queue-db` / `--queue-ttl`, persistent queue, restart test |
| `322acd9` | CI: linux/macos/windows matrix, `-race` job, identity test flake fixed |
| `a4fa53a`, `18cd191`, `b9dba62` | Specs 0.8a (pairing v2) and 1.0a (mail), review fixes, owner approvals |
| `51334e6` | This handoff file + `CLAUDE.md` pointer |
| `8dd7297` | 0.8e `tools/verifyvectors` (`make verify-vectors`), all vectors reproduce |

### Wave 2: done (2026-09-21)
0.8b `a8cb0a3`; 0.8d `099054e` + `6c78619`; 0.8e `8dd7297`; 1.0c `9e3ddf3` + review fixes `eb7d39e` (strict timestamps, fuzz). Reviews: `Docs/review/08-wave2-security-review.md`, `08b-relay-v2-review.md`.

### Wave 3: done (2026-09-21)
0.8c `96ae3ac` + `e530aa3`; 1.0d `6835a6c`; 1.0b `663eb76` (migration 6 mailbox_keys_own; rotation 7/7/21 d; `keys` kind; key-miss reply; mail receiver ON by default; peers.mailbox_keys keeps max 2 per spec). 1.0b and 1.0d have NOT had an Opus review yet: the 1.0e review must cover them.

### In flight: Wave 4 (started 2026-09-21)
| Ticket | Worker | Worktree / branch | Opus review |
|---|---|---|---|
| ~~1.0e sender outbox~~ | merged `cdb9047` + review fixes `0052764` (retry/final race; harness plaintext checks now decode base64). Review `Docs/review/10-mail-delivery-review.md`: M3 needs an OWNER decision (meaning of `expired`), 7 Lows open. | — | — |
| ~~M4 manual checklist + smoke scripts~~ | merged `bfee756` (smoke 26/26 on Windows). Step 11 (offline mail) to be filled after 1.0e. Owner prep: relay LAN IP, port 8787 open, out-of-band channel for fingerprints, reboot of B | — | — |
| ~~Windows TempDir flakes~~ | merged `a01c877`: use `internal/testutil.TempDir(t)` instead of `t.TempDir()` in any test that writes files. At the 1.0e merge, switch its new tests to it. | — | — |

1.0f is in flight (W4-DocsCLI, `docs-cli` / `w4/docs-cli`): docs/CLI reconciliation, plan table rows, `agentnetd install --relay`, Windows service log, `tests/phase0-manual.md` step 11, spec nits. No Opus review needed unless it touches crypto. Then the owner runs M4 on two machines.

To see live status: `team_members`, `team_task_list`, `git worktree list`.

### Wave 3 detail (reference)
- **0.8c** pairing v2, daemon side. Carry the reviewer's follow-ups:
  - the MITM acceptance test now expects the **redeemer** to fail with `confirm_timeout`
    (not `bad_confirm`);
  - export the canonical-JSON helpers from `internal/agentcard` **and delete the copy in `internal/mail/canonical.go`**;
  - create the first mailbox key;
  - add the `pair_used_codes` table.
  Needs Opus security review.
- **1.0b** mailbox keys: generate, store, signed announcements, rotation, deletion.
  From 1.0d: 1.0b must register `Kinds["keys"]` (until then `keys` mail is neither stored nor acked, so senders retry) and wire key-miss recovery (step 3 reject). 1.0e must wire `OnAck` → outbox. Daemon `Options.MailboxKeys` nil means mail is ignored.
- **1.0d** receiver dedupe and ack. **Include the relayclient change**: mail envelopes
  must bypass the `(from,id)` seen-set so resends get re-acked (review H2).

### Then: Wave 4
- **1.0e** sender outbox, plus the two-daemon harness test: stop B; A sends; stop A;
  start B; start A; delivered exactly once, with a relay restart mid-test.
  Needs an Opus review, which must also cover 1.0d's receive/dedupe/ack path end to end. 1.0e needs a `Reseal` API in internal/mail (1.0c review L5).
- **1.0f** docs and CLI reconciliation (`status --json` shows outbox counts).
- **M4**: write `tests/phase0-manual.md`. The **owner** runs it on two real machines.

### After that
- Re-tag Phase 0 (ask the owner first), then write the Phase 1 protocol docs
  (`team.md`, `presence.md`, `request.md`, ipc additions) **before** any Phase 1 code.
- Remaining defects from `Docs/review/05-expert-review.md`:
  - M1: the direct path is at-most-once; store-then-forward with ack.
  - M2: relay abuse limits and TLS. Must land before hosted relay 4.1.
  - The Low items.
- 15 Low items from `Docs/review/07-spec-review.md` (14 not fixed).
- 7 Low items from `Docs/review/08b-relay-v2-review.md`. Before 4.1: per-IP/account pairing limits (L5); make v1 pairing default-off for library callers of `relay.Options` (L1).
- Spec nits from 0.8e: pairing.md says card_I is "333 bytes" (correct, é is 2 bytes; clarify); state explicitly that u32 length prefixes count canonical bytes.

Ticket definitions and acceptance tests: `Docs/review/06-pairing-session-options.md` §5.

## 5. Lessons learned (environment gotchas)

- **CRLF noise.** `core.autocrlf=true`. A new worktree often shows ~80 "modified" files
  with no content change. Use `git diff --ignore-cr-at-eol --stat` to see real changes.
  `git add` **only the files the worker listed**. Remove merged worktrees with
  `git worktree remove --force`.
- **Go** is at `%USERPROFILE%\tools\go\bin`, and is not on PATH by default in Bash.
  Use `export PATH="$USERPROFILE/tools/go/bin:$PATH"`.
- **No cgo or gcc locally**, so `-race` only runs in CI. First CI run of the matrix (run 35601167726, `e3488f0`) FAILED: lint (~27 findings), race (identity e2e test buffer), Windows harness audit-row timing. Fix in flight on `w4/ci-fix` (W4-CIFix). Check CI with `gh run list --branch main` after every push.

- **Python via `python` fails** (uv trampoline error). Use the Edit tool or Go.
- Local golangci-lint (built with go1.26) cannot load the config; CI lint is the real gate. Try `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run ./...`.
  CRLF artefact, not a real issue.
- The `internal/service` test "hang" on the board was not reproducible; it was closed as stale.
- Windows TempDir-cleanup flakes (AV holds deleted files) are fixed by `internal/testutil.TempDir` (`a01c877`). New tests must use it.
- `gofmt -w` converts CRLF to LF across a whole worktree. Tell workers to run gofmt only on files they changed.
- Merge order matters: rebase each remaining branch onto `main` after every merge.
- Haiku summarizers were good enough for fact-gathering. Spot-check line counts and
  headings before trusting them (one reported "2000 lines" and wrote 464).
- Workers' summaries are leads, not truth: verify key claims in code before reporting
  them to the owner.

## 6. Document index

| Path | What |
|---|---|
| `Docs/AgentNet Free Tier Build Plan.md` | Original plan (phases 0–4, gates) |
| `Docs/review/01-plan-checklist.md` | Plan as a per-phase checklist, doc contradictions |
| `Docs/review/02..04-*.md` | Code summaries (daemon, crypto, relay and build) |
| `Docs/review/05-expert-review.md` | Phase 0 review: ticket status, defects H1–H3, M1–M4, Lows |
| `Docs/review/06-pairing-session-options.md` | Pairing and session design, **ticket list §5**, owner decisions §7 |
| `Docs/review/07-spec-review.md` | Adversarial spec review findings |
| `Docs/protocol/pairing.md`, `mail.md` | Authoritative specs for Waves 2–4 |
| `Docs/orchestration/HANDOFF.md` | This file |
