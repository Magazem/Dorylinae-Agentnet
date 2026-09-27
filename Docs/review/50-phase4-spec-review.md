# 50: Phase 4 spec review (adversarial)

Reviewer: P4-SpecReview-Opus (Opus, `claude-opus-5-5`), 2026-09-26. Target: branch `p4/specs`
at `f21f0b1`, worktree `AgentNet-wt/p4-specs`. Scope: `Docs/protocol/{relay-hosted,accounts,
invites,telemetry,feedback}.md` (new), the `README.md` index lines, and the ticket plan
`49-phase4-tickets.md`. Checked against plan Phase 4 (4.1–4.9 and the note under 4.6),
HANDOFF §3 (D1–D35), `envelope.md`, `pairing.md`, `team.md`, `mail.md`, `request.md`, and the
code wherever a claim depends on it: `internal/relay/{relay,conn,queue,pairing}.go`,
`internal/envelope/{frames,envelope}.go`, `internal/relayclient/{relayclient,wire,seen}.go`,
`internal/mail/outbox.go`, `internal/daemon/daemon.go`, `internal/store/store.go`,
`cmd/relay/main.go`.

## Verdict

**Ready with changes, and one new owner decision.** The changes are applied in this
worktree. There are no Critical findings. There are 3 High findings, all fixed in the docs:

- **H1.** A relay behind a same-host proxy listens on loopback, so it would have accepted
  auth v1 and pairing v1. On top of that, the daemon's "no v1" rule covered only a missing
  `auth` list. Together these left the relay-in-the-middle open.
- **H2.** Quota-group admission was granted on a 25-bit lookup match. That bypasses the
  invite gate and shows a stranger the group's GitHub logins.
- **H3.** The CLI would have handed a relay-chosen login URL to the OS opener. A hostile
  relay could then launch any registered protocol handler.

There are 12 Medium findings, all fixed in the docs. There are 23 Lows: 19 fixed and 4
listed. No D1–D35 decision is reopened.

| Severity | Found | Fixed in docs |
|---|---|---|
| Critical | 0 | — |
| High | 3 | 3 |
| Medium | 12 | 12 |
| Low | 23 | 19 |

**OD changes:** OD-P4-6 keeps its option (a), but the mechanism changes: admission is vouched
by the issuer's daemon (H2). OD-P4-16 gains option (c) and a risk note (M10). OD-P4-19 is
amended: plain Ed25519 instead of minisign's prehashed form, and a key-custody choice, with
offline signing recommended (M8). New: **OD-P4-21**, queue flooding on self-hosted relays
without accounts (M1). The other ODs are confirmed; see [Owner decisions](#owner-decisions).

## The author's own finding (auth v1 does not name the relay): confirmed

- The daemon signs `"dorylinae-relay-auth-v1\n" ‖ nonce` (`internal/envelope/frames.go:66`,
  `AuthMessage` `:128-130`). The relay verifies only that (`internal/relay/relay.go:261`). The
  challenge carries nothing that names the relay (`relay.go:241-246`). A relay X can open a
  connection to Y, pass Y's challenge to a daemon that dialled X, and return the signature to Y
  within the 10 s TTL.
- The consequences are worse than stated. Y registers X's connection under the victim's key
  and **kicks the victim's own live connection** ("replaced", `relay.go:274-276`). It then
  drains the victim's queue to X (`drainStep`, `relay.go:390-415`). An `ack` from X deletes
  queued envelopes for that key (`pairing.go:125-128`, `queue.go` `ack`), and while X holds the
  registration, directly forwarded envelopes go to X. Mail survives because the outbox resends
  until the app ack. `session.*` frames and `pair.confirm` do not.
- **Placement is right.** Auth v2 is in the first ticket (4.0a, wave P4-1). Nothing is
  deployed before G-4.0: 4.1b depends on G-4.0, and the gate text forbids non-loopback relays
  except for CI and the owner. The self-hosting guide waits for R-4.0.
- **The design holds once H1 is fixed.** The v2 input is a fixed-length domain and nonce, then
  a length-prefixed origin, so it parses one way only. Binding to the origin the daemon dialled
  is the right anchor. TLS channel binding is not possible with `--behind-proxy`, and it adds
  nothing against the relay-in-the-middle once the origin is bound; the doc now says so. The
  nonce is fresh per connection and valid only there, so there is no replay window. Downgrade
  and migration were the gaps (H1). Origin normalisation and redirects were unspecified (L19).

## High

### H1: relays behind a same-host proxy keep v1, and the daemon can be downgraded

**Where:** relay-hosted.md §1 ("`--require-auth-v2` (the default for any non-loopback
listen)"; "falls back to v1 only on loopback" was specified only for "no `auth` list"); §2
L1; `cmd/relay/main.go:79-84`.

**Attack.** Many self-hosters, and the Hetzner variant of OD-P4-3 (a), put nginx or Caddy in
front, and the relay listens on `127.0.0.1`. Every "non-loopback" default then flips the wrong
way: v1 auth is accepted, and `cmd/relay` turns **pairing v1 on** (it derives the default from
the listen address). A hostile relay X then does the following:

1. It forwards Y's challenge to a v2 daemon with `"auth":["v1"]`. The spec defined the fallback
   only for a missing list, so the daemon's behaviour was open.
2. It gets a v1 signature.
3. It logs in at Y as the victim.

The same works against every Phase 0–3 daemon, with no downgrade needed.

**Fix (applied).** relay-hosted.md defines a **public relay**: it listens on a non-loopback
address, or runs with any of `--tls-cert`, `--acme-domain`, `--behind-proxy`, or a non-loopback
`--public-origin`. Every former "non-loopback" default keys off "public": v2 required, pairing
v1 off, and `relay` prints `public: yes|no` at start. `--require-auth-v2` is replaced by an
explicit `--allow-auth-v1` escape. The **daemon rule is absolute**: for a non-loopback URL it
signs only v2, and anything else is a connection error.

Also added:

- the migration story for old daemons;
- a valid v1 signature on a v2 relay does not count towards the per-prefix failed-auth
  lockout;
- the downgrade and "loopback plus `--behind-proxy`" acceptance tests (relay-hosted.md, 4.0a).

### H2: admission by pairing is granted on a 25-bit lookup, before the secret is checked

**Where:** invites.md §Seats (OD-P4-6 (a)): "When a `pair_redeem` succeeds at the relay …
adds the redeemer's account". The spec also claimed "the redeemer had a valid lookup, which
only the code holder has".

**Attack.** At the relay, `pair_redeem` checks only the 5-character lookup (25 bits,
`pairing.go:296-321`). A v2 entry accepts up to **3** redemptions (`pairing.go:28`). Any
bound, group-less account can therefore do the following:

1. Grind lookups. The limits are 10/min per prefix and a per-account limit, but GitHub
   accounts and cloud prefixes are cheap.
2. On a hit, it is admitted to a stranger's quota group, even though its `tag_R` then fails.
3. It is past the invite gate, the only abuse gate on the hosted relay. It can now mail any
   bound key and spend that group's quota.
4. The account page shows it the group's members by GitHub login or email.

An honest invitee can also be followed by two strangers on the same entry.

**Fix (applied).** Admission is **vouched by the issuer's daemon**:

1. The relay records `(issuer, redeemer)` at `pair_redeem`.
2. The issuer's daemon verifies `tag_R`, then sends `pair_admit {public_key}` before its
   `tag_I`. It sends this on the same connection, so admission lands before the redeemer can
   send `team.join`.
3. The relay honours `pair_admit` once per recorded redemption, within the TTL.

Also added:

- the rationale;
- the privacy line about member lists;
- acceptance tests: a wrong-secret redeemer is not admitted, and a forged, repeated or late
  `pair_admit` does nothing;
- 4.3a files: the daemon issuer; model: Opus.

### H3: a relay-chosen login URL goes to the OS opener

**Where:** accounts.md: "`bind_pending` … `url`"; "`login` opens the browser with the OS
opener"; setup step 4.

**Attack.** The relay is untrusted (D17), including a self-hosted or compromised one.
`bind_pending.url` is relay-chosen. On Windows the usual Go openers (`rundll32
url.dll,FileProtocolHandler`, `cmd /c start`) launch any registered handler: `file:` to an
executable on an SMB share, `ms-msdt:`-style handlers, `search-ms:`. macOS `open` and
`xdg-open` behave similarly. `agentnet setup` opens the browser automatically. So a hostile
relay could run code, or phish, on every machine that logs in. The `display` and `message`
strings were also printed raw, which allows terminal escape injection.

**Fix (applied).** The CLI opens `url` only if all of these hold:

- it is `https://`;
- its host and port equal the relay origin's;
- it has no user info.

Otherwise it prints a warning and opens nothing. Every relay-supplied string is sanitised with
the debate-constraint rule and truncated before printing (relay-hosted.md §Daemon,
accounts.md). An acceptance test with `file:`, `http:`, a foreign host and control
characters is added. 4.2c becomes **Opus and reviewed** in R-4.2 (D26: untrusted input
reaching an OS-level action).

## Medium

| # | Where | Defect / attack | Fix (applied) |
|---|---|---|---|
| M1 | relay-hosted.md §2 "Offline queue": "On self-hosted relays without accounts the per-pair cap is the main protection"; acceptance "one stranger key cannot queue more than 300" | Keys are free. **Four** fresh keys fill a victim's 1000-envelope queue, so M2 is not closed on relays without accounts. About 64 keys × 64 MiB fill the 4 GiB relay-wide cap from a single /24: there was no per-prefix send limit at all, only upgrade and connection counts | Per-prefix rows added: 64 distinct keys, 600 envelopes/min, 64 MiB/min. The claim is corrected: effect, residual, and that mail is delayed, not lost (`outbox.go:497` resends on `queue_full`). "What the limits do not stop" is extended. New **OD-P4-21** covers self-hosted relays without accounts: accept, recipient-declared senders, or a key allowlist |
| M2 | relay-hosted.md §2; 4.0b acceptance "5000 idle authenticated connections" | Memory DoS. A connection's outbound buffer holds 64 frames of up to 1 MiB (`relay.go:27`, `envelope.MaxFrameBytes`), and a non-reading recipient pins 64 MiB until the 10 s write timeout. A few dozen attacker keys OOM a 256–512 MB host. An idle-connection load test would never see this | A 4 MiB per-connection byte cap sends the excess down the queue path. A relay-wide `--max-inflight` budget is added. The load test uses maximum-size frames to non-reading recipients |
| M3 | 4.0b per-sender and relay-wide caps; migration tables | The queue has no index on `from_key` (`queue.go:40-52`). A per-sender cap, and a relay-wide `SUM(LENGTH(frame))`, would scan the table on every enqueue: up to 4 GiB on one serialised connection. No migration was assigned to 4.0b | R1 (4.1a) adds `queue_by_sender (from_key, enqueued)`. Totals are kept in memory and rebuilt at open. 4.0b depends on 4.1a, and the wave note says so. The acceptance requires index use |
| M4 | 49 §doctor `relay` "relay auth v2 accepted"; "never needs the daemon to be healthy" | For doctor to test auth it must log in with the identity key. The relay keeps one connection per key and **replaces** the older one (`relay.go:274-276`). doctor would kick the running daemon, and queued and direct envelopes would go to doctor's connection (`session.*` frames lost). Running doctor would cause the bug it diagnoses | doctor never authenticates. With the daemon up, the `relay` and `account` checks come over IPC. With it down, doctor reads only the `challenge` (v2 offered, clock). `account` is `skip`, and the check itself is added by 4.2c |
| M5 | accounts.md "at most 2 unbound connections per prefix"; relay-hosted.md failed-auth lockout | A team doing `setup` together behind one office NAT has one unbound connection per member until each confirms in the browser. The third member's daemon is refused, which breaks plan 4.9's clean onboarding. Separately, an old v1 daemon on the same NAT retrying with backoff would trip "10 failed auths / 10 min" and lock the whole office out | 16 unbound connections per prefix, oldest closed first. A valid v1 signature is not a failed auth (H1). A residual line about shared NAT is added |
| M6 | relay-hosted.md §3 Restore | A restore also rewinds **security state**: a stolen laptop's unbound key is bound again, an erased account reappears, a suspension is lifted, and a redeemed single-use invite is open again. The spec discussed only envelopes | An off-host, content-free **security journal** (unbind, delete, suspend, invite redeem/revoke, team remove), same 14-day retention as the logs, replayed by `relay restore --replay-journal`. The restore drill checks it. The journal writer is in 4.1a, and events are wired by 4.2a/4.3a |
| M7 | 4.2a "test hook standing in for the confirm page"; 4.9b "staging relay with fake OAuth / test OAuth hook" | An auth bypass on a public host. If the hook is a flag or route in the shipped binary, anyone can bind any key without a browser. "Staging" was not defined as separate from production | Hooks and the fake provider are compiled only under the `testhooks` build tag. A test asserts that the release configuration lacks them, and 4.1b asserts it for the production image. Staging shares no database, domain, OAuth app or operator key with production and shows a banner (accounts.md, 49) |
| M8 | 49 §install; OD-P4-19 ("simplest to verify in a POSIX shell with no extra tool beyond a small verifier"); 4.4a signing key "in a GitHub environment secret" | (1) A POSIX shell cannot verify Ed25519. Stock macOS has LibreSSL, which has no Ed25519 in `pkeyutl`. minisign's default signature is over a BLAKE2b prehash. The promised check was not implementable without a verifier the script would have to download. (2) With the key in a GitHub environment gated by the owner's own approval, a takeover of the owner's GitHub account can sign, so the signature adds nothing against top risk 2. (3) There was no rollback protection | Plain Ed25519 over `SHA256SUMS`, verified by OpenSSL ≥ 3, else minisign, else **stop** and print the Homebrew and manual path; never an unsigned fallback. `SHA256SUMS` names the version, and the script refuses versions below an embedded minimum. There is an honest statement of what curl-pipe-sh protects. OD-P4-19 is amended with key custody, and offline signing on a draft release is recommended. 4.4a moves to Opus (D26) |
| M9 | telemetry.md daemon report; relay-hosted.md "What the hosted relay learns" | (1) The daemon resends when `telemetry_ok` is lost, and the relay adds every report, so weeks are double-counted and Gate 2 numbers are inflated. (2) "No per-person rows" hid that a one-member quota group has per-person counters, and that the relay sees each report on the sender's connection. (3) relay-hosted.md called the report "opt-out-able", contradicting OD-P4-8's recommendation | `telemetry_seen(HMAC(key), week)` holds no values, is pruned after 3 weeks, and makes a repeat a no-op; weeks other than the previous or current one are refused. Privacy text covers one-member teams, per-key visibility during intake and no k-anonymity threshold. The wording now follows OD-P4-8 |
| M10 | feedback.md | (1) `--file PATH` lets an agent send any readable file (a key, `.env`) with one flag. (2) "queued notes are sent on the next connection" needs daemon storage, but no migration exists (only 22 is planned). (3) `--attach-doctor` carried paths whose home directory shows the OS user name. (4) OD-P4-16's recommendation ignored agents steered by content they read | `--file` is removed. There is no local queue: the command fails with `relay_unavailable` and echoes the text back. doctor paths are shown relative to `~` and `<config>`. OD-P4-16 gains option (c) (agent drafts, human sends) and a risk note, not decided |
| M11 | 49 waves and dependencies | P4-6 ran 4.9a in parallel with 4.6b, which it depends on. 4.4c's `account` check predates accounts. 4.4b's acceptance runs `setup` but did not depend on 4.9a. 4.6a parses untrusted reports but had no review. R4–R6 are built in parallel with no merge order | Waves rebuilt (P4-6: 4.6b ∥ 4.1b ∥ 4.3b; P4-7: 4.9a ∥ 4.4b; P4-8: 4.9b ∥ 4.5a; at most 3 workers each). 4.4c no longer has the `account` check. 4.4b depends on 4.9a. 4.6a is reviewed in R-4.6, and R-4.2 includes 4.2c. Merge order: 4.1c, 4.6a, 4.7a |
| M12 | relay-hosted.md §1 Daemon ("`wss://` uses the system roots"); 4.0d test "self-signed cert on loopback" | Self-hosters with a self-signed or LAN-CA certificate had no supported way in: Go ignores `SSL_CERT_FILE` on Windows and macOS. The only way out was the insecure `ws://` escape hatch, which the gate is meant to retire. 4.0d's own acceptance test could not pass as written | `agentnetd install --relay-ca FILE` (config `relay_ca`), used for the relay connection only. `setup --relay-ca`. doctor shows which roots are in use. 4.0a acceptance covers it |

## Low

| # | Where | Finding | State |
|---|---|---|---|
| L1 | relay-hosted.md §1 "Today only the handler exists" | `ReadHeaderTimeout` 10 s is already set (`cmd/relay/main.go:121`), and it does not cover hijacked WebSockets | Fixed (text) |
| L2 | relay-hosted.md per-key "Reconnects 20/min → HTTP 429" | The key is known only after the upgrade and `auth` | Fixed: `rate_limited` after `auth`, close 1013 |
| L3 | relay-hosted.md "Error codes added" | `quota_warning` is a control op, not an error code | Fixed |
| L4 | relay-hosted.md "opt-out-able" (twice) | Contradicted OD-P4-8 | Fixed (M9) |
| L5 | accounts.md CSP `form-action 'self'` | Chromium applies `form-action` to redirects after a POST, so a POST "Sign in with GitHub" that redirects to github.com is blocked | Fixed: sign-in starts with a GET link |
| L6 | accounts.md `bind_start` | Behaviour for an already-bound key was unspecified (could it move keys between accounts?) | Fixed: `already_bound`; logout first |
| L7 | all Phase 4 frames | The relay kicks unknown control ops (`pairing.go:117-133`, `relay.go:296-298`). A new daemon sending `bind_start`/`telemetry_report` to an older or non-accounts relay would be disconnected | Fixed: send only when `ready.features` lists the feature |
| L8 | relay-hosted.md outbox mapping | `HandleError` (`outbox.go:489-505`) ignores unknown codes, and `account_required` for mail to an unbound recipient was unmapped | Fixed: `rate_limited`, `quota_exceeded` and `account_required` go back to `queued` |
| L9 | relay-hosted.md Backup | The plaintext temp copy on the host, and upload credentials able to delete history | Fixed: 0600, deleted in-job, create-only credentials plus versioning/object lock |
| L10 | 49 §install | Rollback to an older signed release | Fixed (in M8) |
| L11 | relay-hosted.md Restore "other types through the seen-set" | The seen-set is in memory and bounded (`seen.go`). The real reasons old non-mail frames are harmless are different | Fixed (text) |
| L12 | 49 wave P4-1 "touch different files" | 4.0a and 4.0c both edit `Options` and `cmd/relay/main.go` | Fixed (text) |
| L13 | 49 §setup | `--wait` was used but not in the flag list, and `--telemetry` was required regardless of OD-P4-8 | Fixed |
| L14 | 49 §doctor | Exit code for `warn` unspecified | Fixed: 0 unless a `fail` |
| L15 | accounts.md | No statement for a GitHub or account-service outage | Fixed: existing bindings unaffected; DB down fails closed |
| L16 | accounts.md | Device-code phishing is silent for the victim | Fixed: `account_changed` frame and a notification to the account's other keys |
| L17 | feedback.md retention "until the owner exports it, at most 90 days" | Ambiguous | Fixed |
| L18 | 49 models | 4.3a (authorization) and 4.4a (signature verification) on Sonnet, against D26 | Fixed: Opus |
| L19 | relay-hosted.md auth v2 origin | Normalisation (IDN, trailing dot, IPv6, explicit `:443`) and HTTP redirects on dial were unspecified: a source of interop failures, and redirects blur "the URL the daemon dialled" | Fixed: exact origin form, no redirects, extra vectors |
| L20 | relay-hosted.md auth v2 | Two relays with the same origin string (private IPs on two LANs) are not told apart | Listed (residual documented; guide recommends DNS names) |
| — | accounts.md | The `unbound`-state `account_required` reply to senders is an oracle for "is key K bound" to any bound member | Listed (the relay sees this anyway; bound members only) |
| — | 49 4.4b | Building `agentnetd` with the Windows GUI subsystem would silence `agentnetd install` output in a terminal | Listed for the ticket's evidence (a launcher is likely better) |
| — | 49 top risk 1 | Logs keep IPs and account ids for 14 days, which is re-identifiable with the DB | Listed (privacy note item, OD-P4-15) |

## What holds (checked, no finding)

- **Migrations.** The daemon's latest migration is 21 (`internal/store/store.go:522`), so 22
  is right, and the rewind-test note matches HANDOFF §5. The relay `R…` numbering is separate
  and consecutive; the M3 index goes into R1, so nothing is renumbered.
- **Gate ordering.** 4.0a–c → R-4.0 → G-4.0 precede every deploying ticket (4.1b, 4.9b) and
  the guide (4.0d). The accounts and invites tickets may be built but not deployed. 4.1a, 4.4
  and 4.6/4.7 daemon work expose no relay.
- **Beta invite codes.** 60 bits, hashed, single use, a uniform `invite_invalid`, and
  per-account and per-prefix failure limits. Offline or online guessing is hopeless, and there
  is no enumeration surface (`list` never prints codes).
- **OAuth.** No scopes, the numeric GitHub id as subject (renames are harmless), `state` bound
  to a pre-login cookie plus PKCE (stops login CSRF), token discarded, session rotated
  (fixation), SameSite=Lax compatible with the callback, POST+CSRF for state changes.
- **The account invariant.** An account grants no peer trust. D5 is unchanged: v1 pairing,
  and so `trust=relay`, is off on a public relay after H1, and D17's "untrusted whoever runs
  it" is kept. The operator can mis-bind keys or lie about account state, but can gain
  neither content nor peer trust.
- **Quota.** Counting the sender's quota group from `from`, size and the binding table needs
  no content. The soft cap is an alert, not an enforcement hole: hard mode exists behind a
  flag.
- **Telemetry schema.** Its enums match `request.md` (types `review`/`task`/`question`, plus
  `debate` from Phase 3; urgencies `low`/`normal`/`high`/`blocking`; `request.defer`) and
  `grant.md` (`fs.read`, `git.read`). Strict whole-report refusal is the right posture.
- **Feedback sealing.** The key is compiled in, so a hostile relay's key has no effect. The
  note is sealed to an offline key, and the 16 KiB frame fits under `MaxFrameBytes` (1 MiB).
- **Per-user Windows install.** A per-user MSI under `%LOCALAPPDATA%\Programs` and the
  existing per-user task need no admin rights (the owner's work PC).
- **The author's plan-conflict list.** All 9 items were checked against the plan text: 4.2
  acceptance, 4.6 and its note (D7), 4.4 "signed MSI", 4.5 "generated from `--help`". The
  readings are reasonable. Item 2 is now also stated at OD-P4-8 and in telemetry.md.

## Owner decisions

| OD | Review position |
|---|---|
| OD-P4-1, -2, -3, -4, -5, -7, -9, -10, -11, -13, -14, -15, -17, -18, -20 | Confirmed as written. For OD-P4-3 (a) on Hetzner, the public-relay rule (H1) is what makes a same-host proxy safe |
| OD-P4-6 | (a) confirmed, **mechanism amended** (H2): the issuer's daemon vouches with `pair_admit` after `tag_R` |
| OD-P4-8 | Recommendation (a) confirmed; wording aligned (M9) |
| OD-P4-12 | Confirmed; lead time is the real constraint |
| OD-P4-16 | Option (c) added with a risk note (M10); left to the owner |
| OD-P4-19 | **Amended** (M8): plain Ed25519, not minisign's prehash; key custody (i)/(ii) added; review recommends (ii) offline signing |
| **OD-P4-21** (new) | Queue flooding on self-hosted relays without accounts (M1): (a) accept and document for the beta, (b) recipient-declared senders, (c) key allowlist |

## Files changed

- `Docs/review/50-phase4-spec-review.md` (new, this file)
- `Docs/protocol/relay-hosted.md`
- `Docs/protocol/accounts.md`
- `Docs/protocol/invites.md`
- `Docs/protocol/telemetry.md`
- `Docs/protocol/feedback.md`
- `Docs/review/49-phase4-tickets.md`
