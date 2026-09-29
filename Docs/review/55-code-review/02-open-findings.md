# 55 / 02: Known open findings (do not re-report)

Compiled by CR-Plan-Opus from `Docs/review/05`–`53` (there is no review 54),
`Docs/orchestration/HANDOFF.md` §0/§3 and `Docs/orchestration/board-snapshot.md`, at commit
`6cc26a7`. Each row is a finding that its source marks **open, listed, noted, not fixed,
owner decides, backlog or deferred**, and that no later document records as closed.

How to use it (see [01-rubric.md](01-rubric.md) §6 rule 1):
- Do **not** report these again. Cite the `O-nnn` id in "Related" or "Checked and fine".
- Report again only with **new evidence** that it is worse ("escalates O-nnn").
- The status is **as written in the source**, which may be months old. Several spec-review
  Lows were probably addressed by later tickets without anyone recording it. If you find
  a row fixed in the code, say so under "Checked and fine". That is useful output.
- Section D lists items that look open in an old document but were closed later. Do not
  treat them as open, and do not re-open them without new evidence.
- The file paths are those given by the source. Code may have moved since.

Sources: `Rnn` = `Docs/review/nn-*.md`; `HO` = HANDOFF.md; `BS` = board-snapshot.md
(Sticky Board, taken 2026-09-25); `D`nn = owner decision in HANDOFF §3.

## A. From the prior reviews

### Relay (R05, R08b, R15, R50–R52)

| ID | Source | Title | File(s) | Status as written |
|---|---|---|---|---|
| O-001 | R05 M1 | Direct path delivers at most once: a frame handed to a live connection's `out` channel is not stored; lost if the socket dies before the write (mail recovers via outbox; non-mail types do not) | `internal/relay/relay.go` (forward) | Backlog (HO; R49 "not folded, decide during the beta") |
| O-002 | R05 L3 | Relay auth used `time.Now()` instead of the injected clock | `internal/relay/relay.go` | No status recorded |
| O-003 | R05 L4 | Queue dedupe ignores expiry: an expired, unswept row turns a resend into a no-op until the next sweep | `internal/relay/queue.go` | No status recorded |
| O-004 | R08b L2 | v1 `pair_redeem` `mbox` not validated but forwarded in `pair_peer` | `internal/relay/pairing.go` | Not fixed |
| O-005 | R08b L3 | Expired pairing entries not purged by `sweepLoop` | `internal/relay/pairing.go` | Not fixed |
| O-006 | R08b L4 | Per-key outstanding pairing count is an O(entries) scan under `p.mu` | `internal/relay/pairing.go` | Not fixed |
| O-007 | R08b L6 | `requireLoopback` treats the literal `localhost` as loopback | `cmd/relay/main.go` / `transport.go` | Not fixed |
| O-008 | R08b L7 | Pairing limiter `allow` and `fail` are separate lock sections | `internal/relay/pairing.go` | Not fixed |
| O-009 | R15 L1 | Ephemeral limiter uses a fixed window: 2× burst at a window boundary | `internal/relay/ephemeral.go` | Not fixed (accepted) |
| O-010 | R15 L3 | Ephemeral size check runs after the frame is read and decoded twice | `internal/relay/relay.go` | Not fixed |
| O-011 | R15 L4 | Pre-1.2a daemons ack presence, costing the relay a no-op `DELETE` each | `internal/relay/queue.go` | Not fixed |
| O-012 | R15 L5 | Ephemeral drop reports are logged lazily (only on the next ephemeral frame) | `internal/relay/ephemeral.go` | Not fixed |
| O-013 | R15 L6 | Presence to self is forwarded back to the sender | `internal/relay/relay.go` | Not fixed |
| O-014 | R50 L20 | Two relays with the same origin string (private IPs on two LANs) are not told apart by auth v2 | `internal/envelope/authv2.go` | Listed (residual documented) |
| O-015 | R50 OD-P4-21 | Queue flooding on self-hosted relays without accounts (four fresh keys fill a victim's 1000-envelope queue) | `internal/relay/limits.go`, `queue.go` | Owner: accept and document for the beta |
| O-016 | R51 M2 | In-place upgrade with a LAN `ws://` relay baked into the service: `agentnetd run` exits 2 at every start (whole daemon down) | `cmd/agentnetd/main.go`, `install.go` | Listed (release note, rewrite `tests/phase0-manual.md`) |
| O-017 | R51 L1 | `--relay-ca` is added to the system roots, not a replacement (a public CA for that name is still trusted) | `internal/relayclient/relayclient.go` `LoadRoots` | Listed for the owner |
| O-018 | R51 L2 | `relay_ca.pem` persists across reinstall/uninstall and is loaded for any relay URL | `cmd/agentnetd/relayurl.go`, `install.go` | Listed |
| O-019 | R51 L3 | Same-host proxy in front of a loopback relay without `--behind-proxy`/`--public-origin`: relay reports `public: no` and keeps v1 auth/pairing for old daemons | `cmd/relay/transport.go` | Listed |
| O-020 | R51 L4 | `--allow-non-loopback --behind-proxy` serves plaintext on a public interface | `cmd/relay/transport.go` | Listed (document) |
| O-021 | R51 L6 | Origin form: IPv6 literals not re-compressed, IPv4-mapped differs; trailing dot collapses `relay.`/`relay` | `internal/envelope/authv2.go` | Listed |
| O-022 | R51 L7 | `--acme-domain` not checked against `--public-origin` | `cmd/relay/transport.go` | Listed |
| O-023 | R51 L8 | URL rule (`CheckURL`) enforced by `cmd/agentnetd` only, not by `relayclient.New` | `internal/relayclient/relayclient.go` | Listed |
| O-024 | R52 M2 | Code default flag values (5000 conns, 256 MiB outbound/read) exceed a small host; the early relay sets safe flags in `deploy/early`, code defaults unchanged | `cmd/relay/main.go`, `limits.go` | Listed for 4.1b / owner (D37 sizing) |
| O-025 | R52 L2 | Failed-auth lockout checked only at admission (64 concurrent attempts per prefix); a client closing before the challenge TTL never counts | `internal/relay/relay.go`, `limits.go` | Listed |
| O-026 | R52 L3 | Outbox lost update: a relay `error` frame can race `send`'s `relayed` update (same as O-054) | `internal/mail/outbox.go` | Listed (backlog "relayed rows on reconnect") |
| O-027 | R52 L4 | Free-disk check fails open on a `Statfs` error; trusts the FS; measures the configured dir, not a symlink target | `internal/relay/queue.go`, `diskfree_*.go` | Listed (document) |
| O-028 | R52 L5 | `ack` is exempt from the control rate: one key can flood acks and serialise queue work | `internal/relay/relay.go` | Listed (revisit with 4.2) |
| O-029 | R52 L6 | Client-IP header trusted exactly as configured; a proxy that does not overwrite it lets the client choose its prefix | `internal/relay/limits.go` `clientAddr` | Listed (operator guide) |

### Identity, keystore, pairing (R05, R07, R09, R14)

| ID | Source | Title | File(s) | Status as written |
|---|---|---|---|---|
| O-030 | R05 L2 | Windows never re-checks key file permissions on read (Unix refuses group/world-readable) | `internal/keystore/perm_windows.go` | No status recorded |
| O-031 | R05 L5 | A failed `session.fin` send leaves a half-open session | `internal/session/session.go` | No status recorded |
| O-032 | R05 L7 | Dead placeholder packages `internal/transport`, `protocol`, `config` | those dirs | No status recorded (still present) |
| O-033 | R07 L3 | No explicit rejection of a `pair_peer` whose `public_key` is the daemon's own key (not exploitable) | `internal/peers/pairing.go` | Not fixed (R09 says `verifyPeer` rejects own key: check) |
| O-034 | R07 L9 / R09 L8 | Concurrent Argon2id derivations: 16 pending pairings × 64 MiB; a hostile relay can cause 4 concurrent derivations per issuer pairing via `pair_lookup_taken` | `internal/peers/pairing.go` | Not fixed |
| O-035 | R07 L10 | Crash ordering between keystore write and `mailbox_keys_own` row unspecified | `internal/mailbox/mailbox.go` | Not fixed (R10 says key before row: check) |
| O-036 | R07 L11 | A sender with a skewed clock (>10 min ahead, >30 d behind) is rejected `stale` silently until its row expires | `internal/mail/open.go` | Not fixed |
| O-037 | R07 L12 | `H(lookup)` at the relay is log hygiene only (25 bits) | spec | Not fixed (say so) |
| O-038 | R07 L13 | Asymmetric pair state (one side lacks the other's mailbox key): acks dropped, rows expire, invisible to the user | `internal/mail` | Not fixed |
| O-039 | R09 L7 | A `pair.confirm` arriving before the issuer finished verifying `pair_peer` is dropped | `internal/peers/pairing.go` `onPeerIssuer` | Not fixed |
| O-040 | R09 L9 | The pairing code lives on in immutable Go strings, which the wipe cannot clear | `internal/peers/pairing.go` | Not fixed (language limit) |
| O-041 | R14 L2 | v1 re-pair of an introduced peer clears `introduced_by` but keeps `trust=team`; GC never collects it | `internal/peers/store.go` | Not fixed |
| O-042 | R14 L3 | A directly paired `relay` peer listed in a roster stays `relay` (D5 refuses it) while an unknown key gets `team` | `internal/peers/store.go` | Not fixed (spec) |
| O-043 | R14 L4 / R12 L6 | Introduced peer's card refreshed from **any** owner's roster (replay of an older self-signed card) | `internal/peers/store.go` `Introduce` | Not fixed |
| O-044 | R14 L5 | `SetTrust` accepts any string (`team` raises a relay peer with no introducer; unknown value silent no-op) | `internal/peers/store.go` | Not fixed |
| O-045 | R14 L6 | `Introduce` does not reject `key == self` (callers must skip self) | `internal/peers/store.go` | Not fixed |
| O-046 | R14 L7 | `introduced_by` can outlive its owner (display only) | `internal/peers/store.go` | Not fixed |

### Mail (R07, R08, R10, R13)

| ID | Source | Title | File(s) | Status as written |
|---|---|---|---|---|
| O-047 | R07 L5 | `keys` check `not_after > now` can reject a legitimately delayed rotation push | `internal/mail/keys.go` | Not fixed (harmless) |
| O-048 | R07 L8 | Relay queue dedupe on `(to,from,id)` can swallow a re-sealed frame while the original is queued; self-heals at next backoff (≤ 6 h) | `internal/mail/outbox.go`, relay `queue.go` | Not fixed |
| O-049 | R08 L2 | `Opened.Signed` holds received bytes, not `canonical(signed)` | `internal/mail/open.go` | Not fixed |
| O-050 | R08 L3 | `Open` does not check `env.Type == "mail"` or call `env.Validate()` (relies on caller) | `internal/mail/open.go` | Not fixed |
| O-051 | R08 L4 | Every mail reject logged at Info before the limiter (log volume); zero-value `RejectAudit` panics | `internal/mail/audit.go` | Not fixed |
| O-052 | R08 L6 / L7 | `Seal` does not validate `Created`; `msg.V = int(vi)` truncates on 32-bit | `internal/mail/mail.go`, `open.go` | Not fixed |
| O-053 | R08 L8 | Invalid base64 fails `envelope.Parse` so such mail gets no `mail.reject` audit | `internal/envelope`, `internal/mail` | Not fixed (accept) |
| O-054 | R10 L1 | Relay `error` frame can arrive before `send` sets `relayed`; row stays `relayed` | `internal/mail/outbox.go` `send`/`HandleError` | Not fixed (BS bug "relay error races relayed update") |
| O-055 | R10 L3 | Each repeat of a mail is fully opened and re-acked (relay-driven 1:1 amplification; keystore load per ack) | `internal/mail/receiver.go` | Not fixed |
| O-056 | R10 L4 | `KeyMiss` sets `last[peer]` only after a successful reply; concurrent `Note` can send two replies | `internal/mail/keys.go` | Not fixed |
| O-057 | R10 L5 | Receiver queue (256) full: envelope dropped after relayclient acked it; no log | `internal/daemon/mail.go` `startMail` | Not fixed |
| O-058 | R10 L6 | No cap on non-final outbox rows (~1.9 MB each); `mail_inbox` never pruned | `internal/mail/outbox.go` | Not fixed |
| O-059 | R13 L1 | `ErrBadBody` marks an id permanently (35 d): Apply must return it only for incurable failures | `internal/mail/receiver.go` + every kind | Not fixed (rule for kind authors) |
| O-060 | R13 L2 / L3 | `!bad_body` suffix on `mail_seen.received_at` undocumented in mail.md; reject step number 11 misleading | `internal/mail/receiver.go` | Not fixed |
| O-061 | HO (INV-4) | A hand-off into a dying connection stays `relayed` ~1 min | `internal/mail/outbox.go` | Backlog |
| O-062 | HO (B-4) | "relayed rows on reconnect" (spec change) and presence-online should send all non-final rows (spec vs code mismatch) | `internal/mail/outbox.go`, `presence.md` | Backlog |

### Teams, presence, requests (R12, R15–R20)

| ID | Source | Title | File(s) | Status as written |
|---|---|---|---|---|
| O-063 | R12 L4 | A hostile relay can delay heartbeats within the ±10 min window: "online" stretched ~11 min | `internal/presence` | Open (backlog) |
| O-064 | R12 L5 / R17 L5 | Cross-boot rule `created ≥ row.created`: clock step back refuses a new boot up to 20 min; same-second restart lets the relay replay the old boot's goodbye once | `internal/presence/store.go` | Open |
| O-065 | R12 L7 / R16 L9 | Team-id squatting by a member that knows the id | `internal/team/kinds.go` | Open (note in team.md) |
| O-066 | R12 L8 | Sybil introduced keys each get a fresh urgency budget | `internal/request/urgency.go` | Open (accepted OD-P1-2) |
| O-067 | R12 L9 | Name collisions: introduced peer may share a name; CLI should show trust/fingerprint | `cmd/agentnet` | Open |
| O-068 | R12 L10 / R17 L1 | Presence padding leaks team-count bucket and occasionally a goodbye by size | `internal/presence/body.go` | Open (spec change) |
| O-069 | R12 L11 | Invisible mode does not hide mail delivery (daemon up is visible) | docs | Open |
| O-070 | R12 L12 | Slack/Discord webhook URLs stored in plain text in `settings` and returned by `notify_get` | `internal/notify/webhook_settings.go` | Open |
| O-071 | R12 L13 | Both sides' roster `mailbox` null: no sealing until a rotation push (≤ 7 d) | `internal/team` | Open |
| O-072 | R12 L15 | No per-(sender, recipient) ephemeral limit | `internal/relay/ephemeral.go` | Open |
| O-073 | R12 L17 | Request `deadline` has no upper bound | `internal/request/validate.go` | Open |
| O-074 | R12 L18 | `team_full` for a second joiner is not reported back ("asked to join") | `internal/team` | Open |
| O-075 | R12 L19 | A peer advertising `interval = 300` stays online 750 s after dying | `internal/presence` | Open |
| O-076 | R16 L4 | Missing owner row at trust lookup returns a retryable error (race with `peers_remove`) | `internal/team/kinds.go` | Open |
| O-077 | R16 L5, R19 #16, R20 L6, R27 L7, R28 L10 | Process-global `pending*` maps (`pendingRoster/Join/Leave`, `pendingApply`, `pendingMirror/Cancel`, `pendingResult/State/Cancel`, `pendingGrant/Revoke`) leak an entry when the commit fails after `Apply` | `internal/team`, `internal/request`, `internal/worksession`, `internal/daemon/grant_kinds.go` | Open (pattern, small leak) |
| O-078 | R16 L6 / L7 | Owner team operations do not check state; `RemoveMember` not audited by the store (caller must) | `internal/team/store.go` | Open (1.1c should gate: check) |
| O-079 | R16 L8 | A dissolved or self-removed roster introduces then GCs unknown members (spurious `peer.remove` audits) | `internal/team/kinds.go` | Open |
| O-080 | R16 L10 / R18 L5 | Pending joins match on the owner only, not lookup or team | `internal/team/store.go` | Open (spec-intended) |
| O-081 | R16 L11 | Roster broadcast after commit is not outboxed atomically with the epoch change | `internal/team/store.go` | Open (relies on resync) |
| O-082 | R16 L12 | Owner's `peers remove` of a member of an owned team leaves it in `team_members` | `internal/team`, `internal/daemon/trust.go` | Open (R18 test covers cascade: check) |
| O-083 | R17 L2 | No presence-specific size cap before crypto in `OpenPresence` (a hostile relay can send ~700 KiB) | `internal/mail/presence.go` | Not fixed |
| O-084 | R17 L3 / BS | Step 1 `IsPaired` is a SQLite query per frame (board: "cache paired-peer lookup") | `internal/daemon` `peerDirectory` | Not fixed |
| O-085 | R17 L4 | No team scoping on presence receive | `internal/presence/receive.go` | Not fixed (spec) |
| O-086 | R17 L6 / L7 | Sender does not validate the presence body; `Store.Accept` read-then-write in a deferred tx | `internal/presence` | Not fixed |
| O-087 | R18 L1 / BS | Invite tables (`team_invites`, `team_pending_joins`) not pruned at start or daily | `internal/team/invite.go` | Not fixed |
| O-088 | R18 L2 / BS | `JoinTag` partial failure: `RecordPendingJoin` ok but `Submit` fails → joiner told `complete`, no `team.join` ever sent; owner `RecordInvite` failure only logged | `internal/team/invite.go` | Not fixed |
| O-089 | R18 L3 / BS | `team_join` with a 10-char code suggests `pair --v1` (pairs, never joins) | `internal/daemon/team.go` | Not fixed |
| O-090 | R18 L4 | `team.invite` audited at `pair.complete`, not when the invite starts | `internal/team` | Not fixed |
| O-091 | R18 L6 / BS / HO | `team_delete` does not cancel pending invite pairings: a joiner still pairs `trust=code`, then `inactive` | `internal/daemon/team.go` | Not fixed (backlog) |
| O-092 | R18 L7 | `Completed` runs synchronously (≤ 5 s), delaying `tag_I` | `internal/peers/pairing.go` | Not fixed |
| O-093 | R19 #7 | Auto-declined requests store their full body (≤ 64 KiB) for an unverified peer: DB growth | `internal/request/receive.go` | Open |
| O-094 | R19 #8 | `isUniqueConflict` matches any "UNIQUE constraint" text | `internal/request` / `internal/daemon/request.go` | Open |
| O-095 | R19 #9 | `request.submit` audit failure after commit returns an error; a retry without a key duplicates | `internal/daemon/request.go` | Open (pattern in other handlers too) |
| O-096 | R19 #10 | Auto-decline `SubmitTx` failing with `ErrNoMailboxKey`/`ErrUnpaired` makes the sender retry until expiry | `internal/request/receive.go` | Open, acceptable |
| O-097 | R19 #14 / #15 | No warning for a `trust=relay` peer on a loopback relay; `no_mailbox_key` surfaces after D5/team checks | `internal/daemon/request.go` | Open |
| O-098 | R19 #17 | `params_hash` semantics ("as given") not stated in `Docs/cli/request.md` | docs | Open |
| O-099 | R19 #18 | CLI string flags are not UTF-8 checked (Unix argv) | `cmd/agentnet/request.go` | Open |
| O-100 | R20 L1 / R25 L9 / R48 L2 | C1 and bidi/`Cf` characters pass request text validation and are printed raw by `inbox`, `request show`, `debates`, `decisions` (use `termSafe`) | `internal/request/validate.go`, `cmd/agentnet/{inbox,request_query,debate,decision}.go` | Not fixed |
| O-101 | R20 L2 | Lifecycle mirror does not check `at ≤ msg.created` (future `state_at`, cosmetic) | `internal/request/mirror.go` | Not fixed |
| O-102 | R20 L3 / L5 | Early cancel on an existing tombstone refreshes it and replies again; tombstones pruned only on the early-cancel path | `internal/request/cancel.go` | Not fixed |
| O-103 | R20 L4 | Refused-cancel liveness gap: sender stuck `pending` + `cancel=requested` after a lost 14-day mail | `internal/request/cancel.go` | Not fixed |
| O-104 | R20 L7 | `itoa64` negates its input (MinInt64 overflow), safe only after validation | `internal/request` | Not fixed |
| O-105 | R20 L8 | `request_list`/`request_show` lack `presence` for `out` views (ipc.md says they carry it) | `internal/daemon/request_lifecycle.go` | Not fixed (known deviation) |
| O-106 | R20 L9 | `request_defer` parses `until` against `time.Now()` not the store clock | `internal/daemon/request_lifecycle.go` | Not fixed |

### Notifications and webhooks (R21, R22)

| ID | Source | Title | File(s) | Status as written |
|---|---|---|---|---|
| O-107 | R21 L1 | No daemon-level e2e for desktop notifications; no IPC/CLI tests for `notify_get/set/test` | tests | Not fixed |
| O-108 | R21 L2 | `notifyAdapter` runs `peers.Store.List` synchronously in the `After` hook (full scan per event) | `internal/daemon/notify.go` | Not fixed |
| O-109 | R21 L3 | Windows: XML-invalid non-characters (U+FFFE/FFFF) pass `Clean`; that notification fails | `internal/notify/desktop_windows.go` | Not fixed |
| O-110 | R21 L4 | gdbus without introspection: `int32` parse → fallback spawns notify-send (2 spawns) | `internal/notify/desktop_linux.go` | Not fixed |
| O-111 | R21 L5 | `notify_set` not atomic: desktop written before events validated | `internal/daemon/notify.go` | Not fixed |
| O-112 | R21 L6 / L7 | Settings read errors silent in `fire`; burst drops over 16 not audited | `internal/notify/trigger.go` | Not fixed |
| O-113 | R22 L5 | Overflow drop and `--webhook off` make rows `failed` without a `notify.fail` audit | `internal/notify/queue.go` | Not fixed |
| O-114 | R22 L6 | Trigger's 16-event bound also drops webhook enqueues; webhook waits behind desktop `Show` | `internal/notify/trigger.go` | Not fixed |
| O-115 | R22 L7 | Keychain account `webhook` shared by every config dir (profiles overwrite each other) | `internal/notify/webhook_settings.go` | Not fixed (as spec'd) |
| O-116 | R22 L8 | Rotated webhook secret saved before the settings write | `internal/notify/webhook_settings.go` | Not fixed |
| O-117 | R22 L9 / L13 | Serial deliveries (blackholed receiver 20×10 s per tick); missing settings row polls every second | `internal/notify/webhook.go` | Not fixed |
| O-118 | R22 L10 | SSRF dial check misses NAT64 `64:ff9b::/96`, 6to4 `2002::/16`, CGNAT `100.64/10`, `0/8`, `255.255.255.255` | `internal/notify/dial.go` | Not fixed |
| O-119 | R22 L11 / L12 | Reference verifier test does not check body `id` = header id; response body drained without a byte cap | `internal/notify/sign_test.go`, `webhook.go` | Not fixed |

### Phase 2: approval, grants, fetch, sessions, device (R24–R41)

| ID | Source | Title | File(s) | Status as written |
|---|---|---|---|---|
| O-120 | R24 L10 | `request.cancel` and accept cross: request ends `accepted` with an open session the requester wanted gone | `internal/worksession`, `internal/request` | Open (backlog) |
| O-121 | R24 L11 | A modified B can make A create a session with a `ws.result` without accepting | `internal/worksession/receive.go` | Open (harmless) |
| O-122 | R24 L15 | `peers remove` of the other device does not send `device.unlink` | `internal/daemon/trust.go` | Open |
| O-123 | R24 L16 | Scope check 5 compares the controller's `created` with the helper's `activated_at` (skew) | `internal/device` | Open |
| O-124 | R24 L17 | Fetch fragments for an offline holder stay in the relay queue for the TTL | `internal/capability/fetch.go` | Open |
| O-125 | R24 OD-P2-15 | Worker content outside the session result while a sensitive grant is live is not held | spec | Documented (option a) |
| O-126 | R25 L9 / R34 L6 / BS | Path grammar: `CONIN$`/`CONOUT$`, superscript `COM¹–³`/`LPT¹–³`, `COM0`/`LPT0`, C1/bidi in scope/path | `internal/capability/token.go`, `grant.md` | Owner call with next vector revision |
| O-127 | R26 L1 / L2 | `notifier.Show` under `Store.mu`; CLI may time out after the daemon committed | `internal/approval/store.go` | Note |
| O-128 | R26 L8 / L9 | Wrong-code attempts write and window write not one tx; `Reject` of a lapsed approval records `user` | `internal/approval/store.go` | Note |
| O-129 | R26 N4 / R46 L3 | Decoy code-like digits in peer or constraint text in a summary (terminal mode) | `internal/daemon` summaries | Note (mitigated by "code always last", `stripLongDigits`) |
| O-130 | R27 L2 / L3 | Close caused by an early complete: `ws.close` and the content drop (`ws.ignored early_complete`) not audited | `internal/worksession`, `internal/request/session_hooks.go` | Not done |
| O-131 | R27 L9 | A `closed`→`open` `ws.state` from a misbehaving A reopens B's mirror | `internal/worksession/mirror.go` | No action |
| O-132 | R28 L6 / L8 | Grant dropped by a failed Precondition stays `pending_approval` forever; a pending grant's approval is not rejected at session close | `internal/daemon/grant.go`, `internal/capability/store.go` | Note / cross-ticket |
| O-133 | R28 L7 | `grant_policies.approval` always `''` | `internal/daemon/grant.go` | Note |
| O-134 | R28 L9 / HO | `grant.orphan` audit carries `{peer}` only; spec says `{grant, peer}` | `internal/daemon/grant_kinds.go` | Open (HO) |
| O-135 | R28 L11 / L12 / L13 | Revoke mail for a never-received grant; `grant.revoke` `at` not parsed; APFS NFC/NFD of the config-dir path not folded | `internal/daemon/grant.go` | Note |
| O-136 | R29 L8 | `approval_open` reopen can be abused as a nuisance (rate-limit if audit shows abuse) | `internal/daemon/approval.go` | Listed |
| O-137 | R30 L6 / BS | An answer that arrives before the notification is shown is buffered, not treated as dismiss | `internal/notify/window*.go`, `internal/approval/store.go` | Note (follow-up) |
| O-138 | R30 L7 / BS | Windows approval windows not cascaded | `internal/notify/window_windows.go` | Note |
| O-139 | R30 L8 | Windows job assigned after `Start`, not from a suspended process | `internal/notify/window_windows.go` | Accepted |
| O-140 | R30 L9 | Hourly approval limit does not count reserved slots (≤ 4 over) | `internal/approval/store.go` | Note |
| O-141 | R30 L10 / BS | After a `Perform` error `confirm` restarts the timer for a full TTL | `internal/approval/store.go` | Note |
| O-142 | R32 L4 | Duplicate context file names accepted; bidi in names can spoof display | `internal/request/validate.go` | Noted |
| O-143 | R34 L3 | 24 h byte budget can be exceeded by reads in flight (≤ 512 KiB) | `internal/capability/fetch.go` | Noted |
| O-144 | R34 L4 / BS | Fetch server `Close` can wait up to ~1 min for sends in flight | `internal/capability/fetch.go` | Noted (BS todo) |
| O-145 | R34 L5 | `list` reads entry types by path, not from the opened directory handle (local swap only) | `internal/capability/fs.go` | Noted |
| O-146 | R35 L1 | Grants revoked before 2.4 have NULL approval: `QuarantineHolds` treats them as never active | `internal/capability/store.go` | Noted |
| O-147 | R35 L2 / R40 L10 / R46 L5 | Approval id passed to Perform/OnReject via a closure/mutex set after `Create` returns (`ws_release`, `device_scope_set`, debate constraint) | `internal/daemon/session.go`, `device_scope.go`, `debate_constrain.go` | Noted |
| O-148 | R35 L3 / R27 L4 | Early complete after `closed` drops content whenever the rule holds (spec wording) | `internal/worksession/phase1.go` | Noted |
| O-149 | R35 L5 / HO | `session.quarantined` notification text differs from work-session.md §Notify | `internal/daemon` notify | Noted |
| O-150 | R36 L6 | The two devices can hold different link ids after an asymmetric retry | `internal/device/device.go` | Noted |
| O-151 | R37 L3 / HO | Repository config (`include.path`, `alternates`, `gitdir:`, `commondir`) can make git read other local paths; UNC can leak NTLM | `internal/capability/git.go` | Documented residual |
| O-152 | R37 L4 | `GIT_CEILING_DIRECTORIES` splits on `:` on Unix | `internal/capability/git.go` | Noted |
| O-153 | R37 L5 / L6 | `ls-tree -l` re-run per list page; `commit-graph`/grafts can change what the tip serves (local writer only) | `internal/capability/git.go` | Noted |
| O-154 | R38 L3 / BS / D30 | `fs.read` `changed` cannot detect a same-size rewrite between reads | `internal/daemon/fetch_client.go` | Owner: ticket 4.8c |
| O-155 | R38 L4 / L5 | Fetches in flight not failed locally on revocation; janitor sends retries serially | `internal/daemon/fetch_client.go` | Noted |
| O-156 | R40 L2 | Unix kill after the leader is reaped can hit a reused group id | `internal/device/runner.go`, `runner_unix.go` | Noted |
| O-157 | R40 L8 | Kinds passed in `Options.MailKinds` are not in `ownedKinds` for `mail_submit` | `internal/daemon/outbox.go` | Noted |
| O-158 | R40 L9 | A `device.unlink` `at` in the future pins the watermark (own link only) | `internal/device/device.go` `NoteUnlinkTx` | Noted |
| O-159 | R40 M2 / R41 L7 | Kill on revoke/timeout: macOS has no `Pdeathsig`; processes that `setsid` or start services escape; cgroup is the full fix | `internal/device/runner_*.go` | Documented gap |
| O-160 | R41 L4 / BS | macOS/BSD extended ACLs not read; private-group rule trusts every member | `internal/device/perm_acl_other.go`, `perm_unix.go` | Accepted (documented) |
| O-161 | R41 L5 / BS | UNC/NFS program paths trust the file server's owners/admins | `internal/device/perm_windows.go`, `perm_unix.go` | Owner decision |
| O-162 | R41 L8 | A program reached through a junction is refused with a confusing error | `internal/device/perm.go` | Noted (fail closed) |
| O-163 | R41 L9 | Tools that search upward from the repo can pick up planted directories | docs | Noted |
| O-164 | R46 L1 | Constraint text persists in `approvals.summary` (and notifications) until pruned, even when rejected | `internal/approval/store.go` | Noted (spec text fixed) |
| O-165 | R46 L4 | B holds a close for a constraint id that never arrives, silently | `internal/debate/constraint.go` | Noted |
| O-166 | R46 L6 / L7 | Window display fidelity (space collapse, `json.Marshal` escapes); `Store.List` carries constraint texts | `internal/notify`, `internal/debate/store.go` | Noted |
| O-167 | R46 (outside 3.4) | `device_scope` summaries up to 16384 bytes but the window shows 4096 | `internal/daemon/device_scope.go` | Needs its own ticket |

### Phase 3: audit, debate, decision (R43–R48)

| ID | Source | Title | File(s) | Status as written |
|---|---|---|---|---|
| O-168 | R43 L13 / R45 L3 | Held early entries from a misbehaving A kept (≤ 12 rows, ≤ 14 × 32 KiB) | `internal/debate/apply.go` | Bounded, no fix |
| O-169 | R43 L18 | `agentnetd install` audit row after starting the daemon can make a concurrent `AppendTx` fail with `SQLITE_BUSY_SNAPSHOT` | `cmd/agentnetd/install.go` | Open (rare) |
| O-170 | R43 L19 | `debates.reason` has no `early_complete` value (uses `cancelled`) | `internal/store/store.go` migration 19 | Open |
| O-171 | R43 OD-P3-8 | `debates`, `decisions` and `experience_records` keep content indefinitely; decide retention before the beta | spec | Open (owner) |
| O-172 | R44 L3 | First append after migration 18 hashes the whole legacy log under the write lock | `internal/audit/chain.go` | Noted |
| O-173 | R44 L6 / R45 L5 | A long migration in another process can exceed `busy_timeout` (install exits 1) | `internal/store/store.go` | Noted |
| O-174 | R45 L4 | Wrong error text for `type: debate` when no debate store is wired (unreachable in the daemon) | `internal/daemon/request.go` | Noted |
| O-175 | R47 L6 | A modified A can drop B's last entry by claiming a timeout (outcome `escalated`, shown `late`) | `internal/debate/decision.go` | Accepted in spec |
| O-176 | R47 L8 | A lost `debate.sign` is not re-sent (A stays `awaiting_peer`) | `internal/debate` | Noted |
| O-177 | R48 M4 / D33 | `decision.export` is not audited; **D33: revisit in the next large security review** (this one) | `cmd/agentnet/decision.go` | Owner-accepted; revisit |
| O-178 | R48 L1 | `decision_list` (and `debate_list`) unbounded; can exceed the 1 MiB IPC line | `internal/daemon/decision.go`, `debate.go` | Noted |
| O-179 | R48 L6 / L7 / L8 | `--out` Stat-then-rename TOCTOU (self only); exported file mode 0600; `\u{…}` escape ambiguity; no IPC worst-case `decision_show` test | `cmd/agentnet/decision.go`, `internal/decision/visible.go` | Noted |

### Phase 4: release pipeline (R53)

| ID | Source | Title | File(s) | Status as written |
|---|---|---|---|---|
| O-180 | R53 L5 | Spec 49 §install still claims the signature protects against "a compromised CI step" | `Docs/review/49-phase4-tickets.md` | Open (owner/spec text) |
| O-181 | R53 L9 | Anyone who can swap GitHub assets can serve any older signed release ≥ `AGENTNET_MIN_VERSION` to "latest" installs | `scripts/install.sh` | By design (raise the minimum per security release) |
| O-182 | R53 L10 | minisign legacy verification only tested with Ubuntu's minisign, not Homebrew's (macOS route) | `tests/install/run.sh` | Open |
| O-183 | R53 L11 | Predictable `.agentnet.new.$$` name in the install dir | `scripts/install.sh` | Open (own dir) |
| O-184 | R53 L12 | `releasesign keygen` `0600` means nothing on Windows; key PEM unencrypted | `tools/releasesign/main.go` | Doc note |
| O-185 | R53 L13 | `setup-go` cache on in release test/sums/install-sh jobs | `.github/workflows/release.yml` | Open |
| O-186 | R53 L14 | actionlint/shellcheck SC2034/SC2086 in `ci.yml` | `.github/workflows/ci.yml` | Open (CI chore) |

## B. HANDOFF §0 backlog and carry-overs not tied to one review row

| ID | Source | Title | File(s) | Status as written |
|---|---|---|---|---|
| O-187 | HO (R-4.2 list) | **Accounts (4.2a, merged, off unless `--accounts`, not yet security-reviewed; R-4.2 pending).** Author's must-check list: accounts.md says fail closed on accounts-DB loss but only `/healthz` does; revocation vs concurrent register ordering; `data_version` polling watcher (250 ms) silent on DB outage; `ConfirmBind` API present in release builds (HTTP hook is `testhooks`-only); DB change succeeds but journal append fails; `relay admin` prints display/email unescaped; 11 spec ambiguities resolved in the 4.2a report (presence to ineligible dropped silently; `accounts.group_id` authoritative; login URL = first origin + `/login`) | `internal/relay/accounts*.go`, `admin.go`, `cmd/relay/admin.go` | Open: must be checked by R-4.2. **C03 of this review covers it; findings here are new findings, not re-reports** |
| O-188 | HO | A dedicated `stale` ack status instead of `unsupported` | `internal/mail` | Backlog |
| O-189 | HO | Accepted trade-off: if the LAST fetch send fails, its audit row reads `ok` not `io` | `internal/capability/fetch.go` | Accepted |
| O-190 | HO | Test hygiene: `cmd/relay` `TestStartupLineOnStderr` uses the real `%APPDATA%\dorylinae` | `cmd/relay/*_test.go` | Backlog |
| O-191 | HO | Recurring `cmd/agentnetd` TempDir-cleanup flake on macOS | `cmd/agentnetd/*_test.go` | Known flake |
| O-192 | HO | Session recovery after a peer restart (one ping times out first) | `internal/session/session.go` | Backlog (BS todo) |
| O-193 | HO / BS | Feature: grantor sees holder fetch activity | — | Backlog |
| O-194 | HO | `Docs/` vs `docs/` casing | repo | Open, not urgent |
| O-195 | HO | 4.1p early relay: owner judgment calls (SSH open to all IPs by default, no sudo admin user, Caddy from Debian, cross-built unsigned binary, Hetzner backups/firewall optional); no backups until the operator key exists | `deploy/early/*`, `Docs/ops/early-relay-deploy.md` | Owner decisions |
| O-196 | HO / D41 | No independent outside security review before the private beta (moved to before public launch) | — | Owner decision |

## C. Sticky Board items (board-snapshot.md, 2026-09-25) not already listed above

| ID | Source | Title | File(s) | Status as written |
|---|---|---|---|---|
| O-197 | BS scope | Lost identity key fails start and never rotates (reset by deleting the card file) | `internal/identity/identity.go` | Scope note |
| O-198 | BS todo | Agent card immutable after creation: no edit or rotation path | `internal/identity`, `internal/agentcard` | Todo |
| O-199 | BS todo | `agentnetd` windowless at logon on Windows (task shows conhost) | `internal/service/schtasks.go` | Todo (ticket 4.4b, not built) |
| O-200 | BS bug | `relay_unavailable` right after connect: server ready before the client conn is set | `internal/relayclient/relayclient.go` | Open |
| O-201 | BS bug | Installed golangci-lint cannot load the config (built with go1.26, module is 1.27) | tooling | Open |
| O-202 | BS bug | `TestRequestIdempotencyKey` flaky on Windows (pairing code timeout) | `internal/daemon` tests | Open |
| O-203 | BS bug | No clear error for a long socket path (macOS 104-byte limit) | `internal/ipc/transport_unix.go`, `internal/paths` | Open |

## D. Closed since (listed as open in an older document; do not treat as open)

| Old item | Closed by |
|---|---|
| R05 H1/H2/H3 (pairing MITM, relay queue not persisted, offline delivery) | Pairing v2 (D3), `--queue-db`/`relay.Open`, sealed mail + outbox (D2) |
| R05 M2 (relay TLS + abuse limits), R08b L1 (v1 zero value on), R08b L5 (per-prefix limits), BS "relay TLS + abuse limits", "pairing limits before hosted relay", "per-IP pairing rate limit at proxy" | 4.0a/4.0b/4.0c, R-4.0 (R52), **G-4.0 signed (D38)** |
| R05 L6 (relay `Close` does not wait) | 4.1a (drain + waitgroup, INV-5) |
| R07 L2 (export canonical helpers), R08 L1 (non-strict base64url), R08 L5 (no `Reseal`), R09 L5 (no `mailbox_keys_own` table), R09 L6 (`peers List` fails on a non-canonical key) | 0.8c (`ParseStrict`/`CanonicalValue` exported, strict base64url), 1.0b/1.0e (migration 6 `mailbox_keys_own`, `Reseal`, `List` skip), per R09/R10 |
| R10 M3 (`expired` ≠ not delivered) | D10 |
| R10 L2 / R36 L7 (`mail_submit` accepts daemon-owned kinds) | 2.D2 allowlist (R40); O-157 is the residual |
| R26 L7 (approval code in argv) | D19 / 2.2d: codes typed only in the daemon's window or terminal stdin |
| R29 L7 / R35 L4 / BS "secure_delete" | D30: `secure_delete` ON (3.9) |
| R36 L4 / L5 (offer freshness, activation notification) | D22, built in 2.D2 |
| R36 L8 (`OnReject` hook) | 2.D2 |
| R37 L2 (Git version not enforced) | D23, 2.9 |
| R40 L1 / L11 (kill on revoke, program ownership) | D24, 2.D3 (R41) |
| R44 L2 (unchained head stops `log --verify`) | 3.6b read-only fallback; D31 |
| R44 L4 (`service.install` detail carries paths) | 3.6b "path-free install audit" |
| R45 L1 / L2 (`r-` id ambiguity, Sweep stops) | 3.1b |
| R51 L5 / R52 M1 (`/healthz` rate limit) | R-4.0 fix |
| R53 L7 / L8 (actions by tag, no tag ruleset or environment gate) | L7 merged 70e8921; L8 done on GitHub (tag ruleset + `release` environment) |
| BS "verify keychain and 0600 on Unix", "run linux/macOS service install by hand" | 4.4d |
| BS "surface relay state to CLI" | 4.4c (`doctor`, `status.relay`) |
| BS "CI red: audit inventory tests" | 2afb00d |
| HO INV-4 `OnPeerOnline` reset overwrite | B-4 (8e285e8); O-062 is the residual |
| 4.4a release dry-run version check (`-trimpath` drops ldflags) | 79b2445 `tools/binversion` (not security-reviewed: C15 covers it) |
