# Board snapshot (for moving to another PC)

Taken at park time on 2026-09-25 (work PC). Two boards:

## 1. AionUi team task board (open tasks only)

The finished history isn't needed (it's in git and Docs/review). The open tasks at park time are
listed in HANDOFF.md §0 (ticket, worktree, branch, state). To continue them on another PC, create
fresh tasks from the ticket text in the phase ticket plan (Docs/review/42-phase3-tickets.md for
Phase 3); the task template is in HOME-SETUP.md §5.1. AionUi has no task import.

## 2. Sticky Board: open notes for this project

Re-file on the other PC with the sticky-board skill
(`node <sticky>/bin/add.js --lane <lane> --area <area> --priority <n> --detail "…" "title"`);
re-filing the same title in the same lane updates rather than duplicates. Lanes: scope, todo,
bug, feature. Format below: `[ ] p<priority> (<area>) <title> — <detail>`.

```
project: AgentNet

== scope (9) ==
  [ ] p3 (identity) lost key fails start, never rotates — delete card file to reset
  [ ] p3 (relay) pairing guessing defence: entropy first — 50-bit codes, 5 fails/min/key
  [ ] p3 (pairing) pair returns ID when pending — poll via pair --status
  [ ] p3 (crypto) Noise XX sessions via relay — identity signs static key
  [ ] p3 (relay) offline queue: SQLite, redeliver until acked — ack + client dedupe, 7d TTL
  [ ] p3 (licence) PolyForm Shield 1.0.0 licence — public source, no competing products
  [ ] p3 (backend) sender outbox until app ack — IK/KK first message under research
  [ ] p3 (status) Phase 1 code-complete — CI green, repo public
  [ ] p3 (ci) repo public 2026-09-23 — Actions free since public

== todo (33) ==
  [ ] p1 (daemon) owner: run two-machine test — top of tests/phase1-manual.md
  [ ] p2 (relay) relay TLS + abuse limits before beta — review-05 M2, D17
  [ ] p3 (identity) agent card immutable after creation — no edit or rotation path
  [ ] p3 (identity) verify keychain and 0600 on Unix — only Windows exercised
  [ ] p3 (windows) run agentnetd windowless at logon — Windows task shows conhost
  [ ] p3 (ci) run linux/macOS service install by hand — only cross-vetted so far
  [ ] p3 (relay) surface relay state to CLI — status IPC, doctor
  [ ] p3 (backend) session recovery after peer restart — one ping times out first
  [ ] p3 (relay) pairing limits before hosted relay — L1, L5 in 08b review
  [ ] p3 (storage) secure_delete for discarded results — review 35; WAL keeps bytes
  [ ] p3 (protocol) fs.read same-size rewrite undetected — owner decision; review 38 L3
  [ ] p3 (device) macOS ACLs, UNC program paths — review 41 L4/L5
  [ ] p4 (backend) cache paired-peer lookup — full table scan per envelope
  [ ] p4 (fetch) fetch Close waits on sends — cancel job ctx; 2.3c
  [ ] p4 (paths) COM0/LPT0 reserved name decision — owner call, next vectors

== bug (25) ==
  [ ] p1 (ci) CI red: audit inventory tests — notify.fail path, device test program, macOS race
  [ ] p3 (build) installed golangci-lint can't load config — built with go1.26, module is 1.27
  [ ] p3 (daemon) relay_unavailable right after connect — server ready before client conn set
  [ ] p3 (mail) relay error races relayed update — review 10 L1
  [ ] p3 (team) join partial failure not surfaced — join submit failure only logged
  [ ] p3 (approval) early window answer not dismissed — review 30 L6
  [ ] p3 (ci) TestRequestIdempotencyKey flaky — windows, pairing code timeout
  [ ] p3 (daemon) clear error for long socket path — macOS 104-byte limit
  [ ] p4 (team) invite tables lack startup prune — only pruned on write
  [ ] p4 (cli) team join v1 hint misleading — suggests pair --v1
  [ ] p4 (team) team delete keeps pending invites — invite still pairs after delete
  [ ] p4 (approval) approval windows not cascaded — review 30 L7
  [ ] p4 (approval) retry timer uses full TTL — review 30 L10

== feature (4) ==
  [ ] p3 (relay) per-IP pairing rate limit at proxy — Sybil keys bypass per-key limit
  [ ] p3 (grants) grantor sees holder fetch activity — 2.H: agent thought B never fetched
  [ ] p4 (audit) audit decision exports (revisit) — D33; next large review
```
