# 49: Phase 4 tickets (4.0 beta gate, 4.1–4.9: private beta operations)

Status: **approved by the owner 2026-09-27 (D36 in HANDOFF): OD-P4-1..21 as recommended
except OD-P4-7 (beta invite codes expire after 48 hours), OD-P4-12 (c) (wave 1 ships unsigned
on Windows), OD-P4-16 (c) (an agent drafts, a human sends), OD-P4-19 (a)+(ii) (offline
signing) and OD-P4-20 (decided during wave 1, before wave 2); adversarially reviewed
([50-phase4-spec-review.md](50-phase4-spec-review.md): 0 C, 3 H, 12 M fixed in place).** No code starts
before approval (HANDOFF rule 3). Specs:
[relay-hosted.md](../protocol/relay-hosted.md) (TLS, relay auth v2, abuse and pairing limits,
persistence, backup/restore, quotas, monitoring, what the relay learns),
[accounts.md](../protocol/accounts.md), [invites.md](../protocol/invites.md),
[telemetry.md](../protocol/telemetry.md), [feedback.md](../protocol/feedback.md). CLI-only
contracts (`doctor`, `setup`, install) are in this plan ([§CLI contracts](#cli-contracts))
and become `Docs/cli/*.md` in their tickets.

> **Windows code signing (D36: OD-P4-12 (c)):** wave 1 ships **unsigned** Windows builds with
> the SmartScreen click-through documented. The owner is an individual in the EU/UK, so Azure
> Artifact Signing (individuals: US/Canada only) is not available; the fallback when signing
> is wanted is an OV certificate on a cloud HSM (e.g. Certum Cloud Code Signing, Individual),
> **4–6 weeks** lead time. 4.4b builds the MSI and a signing step that is skipped when no
> certificate is configured. An Apple developer account is **not** needed (curl and Homebrew
> installs, HANDOFF §3).

Every ticket follows the plan's exit criteria, as in Phases 1–3: acceptance tests are automated
Go tests (or scripts in `tests/`); `go vet` and the linter pass (also `GOOS=linux`/`darwin`);
`--help` and `--json` are documented in `Docs/cli/`; audit events exist; new tests use
`internal/testutil.TempDir`; async state is polled with a deadline; test variables written by
goroutines are guarded (review 31). "Fake OAuth" means an in-process OAuth provider in tests;
"fake clock" means injecting `Now`.

## Scope rule

Only what plan 4.1–4.9 and the D17 gate need, plus the Phase 4 items already assigned by owner
decisions (D30: OD-P3-11 fs version and OD-P3-12 OS user-presence "before 4.8"; D33 revisit).
**Not in Phase 4:** a web dashboard (OD-P4-10), email+GitHub account linking, paid tiers,
auto-update, a Windows service (the per-user task stays), Cloudflare, multi-region relays, PAKE
pairing v3, MLS, three-way debates.

## The beta gate (D17) comes first

Tickets **4.0a–4.0c** plus their Opus security review **R-4.0** close review-05 M2 (relay TLS
and abuse limits) and review-08b L1/L5 (pairing limits). **Gate G-4.0:** the owner signs off
that M2, L1 and L5 are closed (a line in HANDOFF §3). Until then:

- no relay listens on a non-loopback address for anyone but CI and the owner's own machines;
- tickets 4.1b (deploy), 4.2a–c (accounts), 4.3a (invites) may be **built** on branches but
  **nothing is deployed** and the self-hosting guide (4.0d) is not published.

4.1a (persistence and ops code), 4.4a/c/d (release pipeline, doctor, Unix checks) and 4.6/4.7
daemon-side work do not expose a relay and may start in parallel with the gate.

## Migrations (pre-assigned)

**Daemon** (`internal/store`, consecutive, merge in order; every new table goes into the DROP
lists of BOTH rewind tests in `internal/store/store_test.go`, and a migration that alters an
existing table recreates it in its pre-migration form there, HANDOFF §5):

| # | Name | Tables | Ticket | Rewind-test note |
|---|---|---|---|---|
| 22 | `telemetry_reports` | `telemetry_reports` | 4.6b | add `telemetry_reports` |

Account state uses the existing `settings` table (no migration). No other daemon migration is
planned; a ticket that finds it needs one stops and asks (next free would be 23).

**Relay** (new, `internal/relay` store, numbered **R1…**, independent of the daemon's; a new
`relay_migrations` table; same consecutive-and-in-order rule; each ticket adds a test that
opens a database at the previous relay version with rows in it and migrates):

| # | Name | Tables | Ticket |
|---|---|---|---|
| R1 | `relay_base` | `relay_migrations`; adopts the existing `queue` table unchanged (old queue files keep their envelopes); adds index `queue_by_sender (from_key, enqueued)` for the 4.0b caps (review 50 M3) | 4.1a |
| R2 | `accounts` | `accounts`, `account_keys`, `bind_requests`, `web_sessions`, `billing_teams`, `billing_members` | 4.2a |
| R3 | `beta_invites` | `beta_invites` | 4.3a |
| R4 | `quota_usage` | `quota_usage` | 4.1c |
| R5 | `telemetry` | `telemetry_daily`, `telemetry_weekly` | 4.6a |
| R6 | `feedback` | `feedback` | 4.7a |

R4–R6 are built in parallel (wave P4-5) and **merge in the order 4.1c, 4.6a, 4.7a**; a later
ticket renumbers nothing, it rebases onto the earlier migration. R5 also holds
`telemetry_seen` (review 50 M9).

## Tickets

"Review" = Opus security review before merge (HANDOFF rule 4). Sizes: **S** ≈ half a day of
agent work, **M** ≈ one day, **L** = at the one-day limit. "Model" per D26/D28: **Opus** for
security-critical code (crypto, auth, parsing untrusted input, web auth, OS-level process and
installer work), **Sonnet** for feature work with design choices, **Lite** for routine,
well-scoped work. "∥" = may run in parallel once dependencies merge (at most ~3 implementation
workers at once, HANDOFF §5).

| ID | Title | Depends on | Migr. | Size | Review | Model | ∥ |
|---|---|---|---|---|---|---|---|
| **4.0a** | TLS for the relay (cert/key, ACME, behind-proxy), "public relay" rule, non-loopback needs TLS, HTTP timeouts, `/healthz`, relay auth **v2** (origin-bound, no downgrade) + vectors, daemon refuses remote `ws://`, `--relay-ca`, no redirects on dial | specs approved | — | M | **yes** (R-4.0) | Opus | 4.0c, 4.1a |
| **4.0c** | Pairing limits: L1 (`AllowPairingV1`, zero = off), L5 per-prefix layer (+ per-account hooks used by 4.2a) | specs approved | — | S | **yes** (R-4.0) | Sonnet | 4.0a, 4.1a |
| **4.0b** | Abuse limits: per-prefix (incl. per-prefix send rates and keys), per-key, per-sender→recipient and per-sender queue caps (in-memory totals), relay-wide caps, per-connection outbound byte cap and in-flight budget, free-disk guard, trusted client-IP header, new error codes and daemon handling (`rate_limited`, `relay_full` as backoff) | 4.0a (same files), 4.1a (R1 index) | — | L | **yes** (R-4.0) | Opus | 4.4c, 4.4d |
| R-4.0 | Opus security review of 4.0a–c → `Docs/review/50-…`; then **G-4.0 owner sign-off** | 4.0a–c | — | — | — | Opus | — |
| 4.0d | Self-hosting guide `Docs/beta/self-host-relay.md` (TLS options, proxy setup, limits, what the operator sees), `Docs/cli/relay.md`, known-limitations | 4.0b | — | S | — | Lite | R-4.0 |
| 4.1a | Relay store: `--db`, relay migrations framework + R1, `secure_delete`, graceful shutdown, `Close` waits for connections (review 05 L6), `relay backup` / `relay restore`, restore-drill test, `--metrics-listen` | specs approved | R1 | M | — | Sonnet | 4.0a, 4.0c |
| 4.2a | Accounts core (relay): R2, `--accounts`, account states in `ready`, `bind_*` frames, bound-only routing, revocation, suspension, admin CLI (`relay admin account …`), per-account pairing limits | G-4.0, 4.1a | R2 | L | **yes** | Opus | 4.4a |
| 4.2b | Web login: GitHub OAuth (and/or magic link per OD-P4-2), login / code / confirm / account pages, cookies, CSRF, CSP, rate limits | 4.2a | — | L | **yes** | Opus | 4.2c, 4.3a |
| 4.2c | Daemon + CLI: `account_*` IPC, `agentnet login/logout`, account in `status`, `account_required` errors with hints, audit; relay-supplied `url` checked before the OS opener, relay strings sanitised (review 50 H3) | 4.2a | — | M | **yes** (R-4.2: untrusted relay input reaches the OS opener) | Opus | 4.2b, 4.3a |
| 4.3a | Beta invites and billing teams: R3, `invite_redeem`, `relay admin invite/team`, admission by pairing vouched by the issuer (`pair_admit`, OD-P4-6, review 50 H2), seats, contact removal, `--invite` on `login` | 4.2a | R3 | M | **yes** (authorization) | Opus (authorization, D26) | 4.2b, 4.2c |
| 4.1c | Quotas: R4, charge per sender's billing team, soft mode + `quota_warning`, hard mode behind a flag, daemon display | 4.3a | R4 | S | — | Sonnet | 4.6a, 4.7a |
| 4.6a | Relay telemetry counters: R5, daily counters, weekly totals intake (strict parser of untrusted reports, once per key and week), `relay admin stats` | 4.3a | R5 | M | **yes** (R-4.6: parses untrusted input) | Sonnet | 4.1c, 4.7a |
| 4.6b | Daemon report: migration 22, weekly builder from local tables, `telemetry_report`, `agentnet telemetry`, `TestTelemetryHasNoContent` | 4.6a | 22 | M | **yes** (privacy invariant) | Sonnet | 4.9a |
| 4.7a | `agentnet feedback`: sealing to the compiled-in operator key, R6, export/open, limits; `CHANGELOG.md` + release-notes step | 4.3a | R6 | S | **yes** (with 4.6b) | Sonnet | 4.1c, 4.6a |
| 4.4a | Release pipeline: tag-triggered build of all binaries for 3 OSes × amd64/arm64 (release builds without `testhooks`), SHA-256 sums **signed** (OD-P4-19), `install.sh` (verifies the signature as in [§install](#install-44)), Homebrew tap formula, version check in `ready` (`min_client`) | specs approved | — | M | **yes** (supply chain) | Opus (signature verification, D26) | 4.2a |
| 4.4b | Windows: per-user MSI (OD-P4-13), code signing in CI, `agentnetd` runs **windowless at logon** (board todo), uninstall | 4.4a, 4.9a (its acceptance runs `setup`); certificate optional (D36) | — | M | **yes** | Opus (OS-level) | any |
| 4.4c | `agentnet doctor` + relay state in `status` (board todo): checks of [§CLI contracts](#doctor) except `account` (added by 4.2c) | 4.0a | — | M | — | Sonnet | 4.0b, 4.4d |
| 4.4d | Unix hardening checks: keychain and 0600 fallback verified on Linux and macOS, service install/uninstall run for real in CI (systemd user unit in a container with a user session; launchd on the macOS runner) (board todos) | specs approved | — | M | — | Sonnet | 4.0b, 4.4c |
| 4.9a | `agentnet setup` (login, service install, default hosted relay, telemetry question, doctor), non-interactive and `--json` | 4.2c, 4.4c, 4.6b | — | M | — | Sonnet | 4.1b |
| 4.1b | Deploy: container image, host config (OD-P4-1/3), DNS + TLS, daily encrypted backup job to object storage, uptime monitor, alerts, `Docs/ops/relay-runbook.md`, `tests/phase4-manual.md` (restore drill on the real host) | 4.1a, 4.2a, 4.3a, G-4.0 | — | M | **yes** (deployment config) | Sonnet | 4.9a |
| 4.9b | Clean-machine runs: CI job per OS from a fresh user profile (download release artefact → install → `setup --non-interactive` against a staging relay with fake OAuth → `team join` → first request); an agent-driven run from the quickstart only (harness, one real Claude run, owner OK needed) | 4.9a, 4.4a, 4.1b | — | M | — | Sonnet | 4.5a |
| 4.5a | Docs: quickstart, "tell your agent" page with CLAUDE.md / AGENTS.md / Hermes snippets, privacy note draft (OD-P4-15), per-command pages checked against `--help` by a test (`TestCLIDocsMatchHelp`) | 4.9a | — | M | — | Lite | 4.9b |
| 4.3b | Waitlist (OD-P4-9) and wave runbook (`Docs/ops/beta-waves.md`: making codes, sending them, weekly call rota, what to watch) | 4.3a | — | S | — | Lite | 4.5a |
| 4.8a | Outside review pack: scope, threat models, build and test instructions, known limitations, the list of relevant reviews; D33 export-audit revisit decision | 4.9b | — | S | — | Opus | 4.8b, 4.8c |
| 4.8b | OS user-presence on top of approvals (OD-P3-12 → Phase 4, OD-P4-18) | specs for it (a short addition to approval.md in the ticket, reviewed first) | — | L | **yes** | Opus | 4.8a |
| 4.8c | fs read version (OD-P3-11 (b), D30): size + mtime + a content hash prefix in fs read responses; grant.md change first | grant.md amendment reviewed | — | S | **yes** | Sonnet | 4.8a |
| 4.P | Phase 4 push; the owner's go for wave 1 (see [§Wave 1 go/no-go](#wave-1-gono-go)) | all above except 4.8b/c | — | — | — | — | — |

**Critical path:** 4.0a → 4.0b → R-4.0 → G-4.0 → 4.2a → 4.3a → (4.1c, 4.6a) → 4.6b → 4.9a →
4.1b → 4.9b → 4.P. The Windows certificate is a parallel critical path (4.4b) set only by the
owner's application date.

**Opus reviews in batches:** R-4.0 (4.0a–c); R-4.2 (4.2a + 4.2b + 4.2c + 4.3a: auth, web,
the relay-supplied login URL, admission); R-4.4 (4.4a + 4.4b: supply chain and installer);
R-4.6 (4.6a + 4.6b + 4.7a: the report parser, and nothing leaves a machine except the listed
integers and a sealed note); 4.1b deploy config; 4.8b, 4.8c.

## Waves (max ~3 implementation workers)

| Wave | Tickets | Notes |
|---|---|---|
| P4-1 | 4.0a (Opus) ∥ 4.0c (Sonnet) ∥ 4.1a (Sonnet) | 4.0a and 4.0c overlap only in `Options` (`relay.go`) and flag parsing in `cmd/relay/main.go` (4.0c's main work is in `pairing.go`); 4.1a touches `queue.go` and adds a store file. Rebase order at merge: 4.0c, 4.0a, 4.1a |
| P4-2 | 4.0b (Opus) ∥ 4.4c (Sonnet) ∥ 4.4d (Sonnet) | starts after 4.1a and 4.0a merge; then **R-4.0**, 4.0d, **G-4.0** |
| P4-3 | 4.2a (Opus) ∥ 4.4a (Sonnet) | + the owner's hosting / domain / OAuth-app setup |
| P4-4 | 4.2b (Opus) ∥ 4.2c (Opus) ∥ 4.3a (Opus) | then R-4.2 |
| P4-5 | 4.1c ∥ 4.6a ∥ 4.7a (all Sonnet) | merge order 4.1c, 4.6a, 4.7a (R4, R5, R6) |
| P4-6 | 4.6b (Sonnet) ∥ 4.1b (Sonnet) ∥ 4.3b (Lite) | 4.9a depends on 4.6b, so it is not in this wave (review 50 M11) |
| P4-7 | 4.9a (Sonnet) ∥ 4.4b (Opus, when the certificate is there) | then R-4.6, R-4.4 |
| P4-8 | 4.9b ∥ 4.5a | then 4.P; 4.8a–c run during beta weeks 1–3, before wave 2 |

## CLI contracts

### doctor

`agentnet doctor [--json]`: never needs the daemon to be healthy to run; exits 0 if no check
is `fail` (`warn` and `skip` still exit 0), 1 if any fail, 2 on usage errors.

**doctor never authenticates to the relay with the identity key** (review 50 M4). The relay
keeps one connection per key and replaces the older one (`internal/relay/relay.go:274-276`),
so a doctor login would kick the running daemon, and envelopes forwarded or drained in that
window would go to doctor's connection. With the daemon running, the `relay` and `account`
checks come from the daemon over IPC (`status`). With it stopped, doctor only dials the relay,
reads the `challenge` (auth versions offered, clock) and closes; `account` is `skip`. Each check: `id`, `state` (`ok`/`warn`/`fail`/`skip`),
`detail` (content-free), `fix` (one command or sentence).

| id | Checks |
|---|---|
| `binary` | CLI and daemon versions match; `min_client` from the relay's `ready` is met |
| `config` | Config dir exists, owner-only (the existing D24/L11 check; a drive-root ACL like the owner's Authenticated Users:(M) shows as `warn` with the fix; paths are printed relative to `~`) |
| `keychain` | Identity key readable from the keychain or the 0600 / DACL fallback; which one |
| `service` | Installed (task/unit/agent present), running, points at this binary and home |
| `socket` | IPC reachable within 1 s; path length under the OS limit (macOS 104) |
| `relay` | URL scheme rule, TCP + TLS reachable, which roots (system or `relay_ca`), `challenge` offers auth v2; from the daemon: `connected` since, last error code |
| `account` | From the daemon: bound / unbound / suspended; billing team; quota state (added by 4.2c) |
| `git` | Git ≥ 2.32 (D23) or `warn` |
| `clock` | Local clock within 2 min of the relay's (from `challenge.expires`) |

`status --json` gains `relay: {url, connected, since, last_error, auth: "v2"}` and `account`.

### setup

`agentnet setup [--invite CODE] [--relay URL] [--relay-ca FILE] [--telemetry on|off]
[--no-browser] [--non-interactive] [--wait] [--json]` does, in order, each step idempotent and skipped when already
done: (1) create identity if missing; (2) `agentnetd install` with the relay (default: the
hosted relay URL compiled into the binary, OD-P4-14); (3) wait for the daemon (≤ 10 s);
(4) login (device flow; prints URL, code and fingerprint; opens the browser unless
`--no-browser`), redeeming `--invite` if given; (5) telemetry choice: interactive → asks with
the full list shown; non-interactive → `--telemetry` is **required** if OD-P4-8 = (a) (an
agent must not choose silently for the human: it asks the human, per the snippet), optional
under (b); (6) `doctor`; (7) prints the one
next command (`agentnet team join <code>` or `agentnet team create …`). With `--json` every
step reports `{step, state, detail}`; a step waiting for the browser returns `pending` with the
URL within 2 s and `setup` can be re-run (or `--wait`) to continue. No config file is ever hand
edited.

### install (4.4)

- macOS/Linux: `curl -fsSL https://<domain>/install.sh | sh` → downloads the release for the
  OS/arch into `~/.local/bin` (no sudo), verifies the SHA-256 against the **signed** sums file
  with a public key embedded in the script, and prints `agentnet setup`. Homebrew:
  `brew install <owner>/tap/agentnet`.
- **What the script can actually verify** (review 50 M8). A POSIX shell cannot check Ed25519
  itself, stock macOS ships LibreSSL (no Ed25519 in `openssl pkeyutl`), and minisign's default
  signatures are over a BLAKE2b prehash. So: the signature is a **plain Ed25519 signature over
  the bytes of `SHA256SUMS`** (not minisign's prehashed form); `install.sh` verifies it with
  `openssl pkeyutl -verify -rawin` when OpenSSL ≥ 3 is present, else with `minisign -V` if
  installed (the release also carries a legacy-format minisign signature), else it **stops**
  and prints the Homebrew command and the manual steps. It never falls back to an unsigned
  install. `SHA256SUMS` names the version; the script refuses a version older than the minimum
  embedded in it (no rollback to a known-bad release by whoever serves the files).
- **What it protects against, honestly:** the script is served from `<domain>`, so whoever
  controls the domain or its host controls the embedded key and every curl install. The
  signature protects against a swap of the release artefacts on GitHub (a leaked token, a
  compromised CI step) only if the signing key is not also in GitHub (OD-P4-19). Homebrew
  users trust the tap repository instead.
- Windows: a **per-user MSI** (installs to `%LOCALAPPDATA%\Programs\AgentNet`, adds to the user
  PATH, no elevation; matters on the owner's no-admin work PC) or a signed zip; `winget` later.

## Ticket details

### 4.0a TLS, relay auth v2, daemon URL rule (review)

- Files: `cmd/relay/main.go` (+ tests), `internal/relay` (auth v2, `/healthz`, public origins),
  `internal/envelope` (auth v2 input, `challenge.auth`, `auth.v`), `internal/relayclient`
  (origin from the dialled URL, v2 signing), `cmd/agentnetd` (URL rule at start and install),
  `tools/specvectors`, `tools/verifyvectors`, `Docs/protocol/envelope.md`, `Docs/cli/relay.md`,
  `Docs/cli/agentnetd*.md`, tests.
- Acceptance: the relay-hosted.md §1 acceptance lines; the auth v2 vector byte for byte,
  recomputed by `verifyvectors`; the relay-in-the-middle test; a v1 daemon against a
  `--require-auth-v2` relay gets `auth_failed`; a v2 daemon against an old relay (no `auth`
  list in `challenge`, or `["v1"]`) falls back to v1 **only on loopback**, and on a
  non-loopback URL signs nothing; a relay on `127.0.0.1` with `--behind-proxy` is public
  (v2 required, pairing v1 off); a dial that gets a 301 fails; `--relay-ca` lets the daemon
  reach a self-signed relay and the system roots still do not; `ReadHeaderTimeout` test with a
  slow client; `/healthz` returns 503 when the DB is closed.

### 4.0c Pairing limits (review, R-4.0)

- Files: `internal/relay/pairing.go`, `internal/relay/relay.go` (Options), `cmd/relay/main.go`,
  tests, `Docs/protocol/pairing.md` (limits section).
- Acceptance: `relay.Open(Options{})` has v1 **off** (L1); 21 `pair_new` from 21 keys in one
  prefix within 10 min → the 21st is `pair_rate_limited`, a key from another prefix is not
  affected; 51 outstanding codes per prefix refused; per-key limits unchanged (existing tests
  pass); `PairStats` unchanged.

### 4.0b Abuse limits (review, R-4.0)

- Files: `internal/relay` (a `limits.go` with token buckets keyed by prefix/key, queue caps in
  `queue.go`, disk check), `cmd/relay/main.go` (flags, `--behind-proxy`, `--client-ip-header`,
  `--trusted-proxy`), `internal/envelope` (codes), `internal/relayclient` and
  `internal/mail/outbox.go` (treat `rate_limited`/`relay_full` as retry-later), `Docs/protocol/envelope.md`,
  `Docs/cli/relay.md`, tests.
- Acceptance: one test per limit row in relay-hosted.md §2 (triggers, error code, other
  keys/prefixes unaffected, limit logged without payload); the stranger-fills-victim test (300
  per pair); the spoofed-header test; the relay stays within a memory bound with 5000 idle
  authenticated connections in a load test (`-short` skips it; runs weekly); the outbox resends
  after `rate_limited` and the mail is delivered once; 64 fresh keys from one prefix cannot
  send more than the per-prefix rate; a non-reading recipient holds ≤ 4 MiB; no cap check scans
  the queue table (the query plan uses the indexes).

### 4.0d Self-hosting guide

- Files: `Docs/beta/self-host-relay.md`, `Docs/cli/relay.md`, `Docs/beta/known-limitations.md`
  (the relay operator's view, the botnet limit).
- Acceptance: the guide's commands are exercised by a script test (`tests/relay-selfhost.sh`
  with a self-signed cert on loopback + `DORYLINAE_ALLOW_INSECURE_RELAY` not set).

### 4.1a Relay store and operations code

- Files: `internal/relay` (a store with `relay_migrations`, R1, `secure_delete`), `cmd/relay`
  (`--db` with `--queue-db` alias, `backup`, `restore`, `--metrics-listen`, SIGTERM drain),
  `internal/relay/relay.go` (`Close` waits), tests.
- Acceptance: a queue file from today's relay opens under R1 with its envelopes intact and
  delivered; kill -9 of the binary mid-traffic loses no queued envelope (plan 4.1); `backup`
  during writes gives a file that passes `integrity_check`; `restore` refuses a non-empty target
  without `--force`; the restore-drill test (relay-hosted.md §3) with two daemons: every unacked
  mail reaches the inbox exactly once; `--replay-journal` restores post-backup unbinds and
  invite redemptions (the journal writer lands here; the account and invite events are
  wired by 4.2a/4.3a); `/metrics` is not served on the public listener.

### 4.2a Accounts core (review)

- Files: `internal/relay` (accounts store R2, states, bind frames, routing check, revocation,
  suspension, per-account pairing limits), `cmd/relay` (`--accounts`, `admin account|team`
  subcommands operating on the DB), `internal/envelope` (frames and codes), tests,
  `Docs/protocol/envelope.md` (pointer to accounts.md).
- Acceptance: accounts.md acceptance lines that do not need the web (the bind is completed by a
  test hook standing in for the confirm page, compiled only with the `testhooks` tag); the routing matrix (bound/unbound × team/no team
  × `mail`/`presence`/`pair.confirm`/pairing frames) as a table test; unbind closes the
  connection within 1 s; no email in any log (marker test).

### 4.2b Web login (review)

- Files: `internal/relay/web` (handlers, templates embedded, no JS), OAuth client (GitHub) and,
  if OD-P4-2 includes email, the magic-link sender behind an interface (SMTP/API provider
  configured by flags/env), `cmd/relay` flags (`--oauth-client-id`, secret from env/file only,
  `--public-origin`), tests with fake OAuth and a fake mailer.
- Acceptance: the web security list of accounts.md (headers asserted, cookie flags, CSRF,
  state/PKCE, fixation, rate limits); the typed code is required (a URL with `?code=` does not
  pre-fill or bind); the confirm page shows the fingerprint; HTML escapes a device label with
  `<script>`; the OAuth token is absent from the DB after login.

### 4.2c Daemon and CLI accounts

- Files: `internal/relayclient` (account state from `ready`, bind frames), `internal/daemon`
  (`account_*` IPC, settings keys, audit), `cmd/agentnet/login.go`, `status`, `Docs/cli/login.md`,
  `Docs/cli/status.md`, `Docs/beta/known-limitations.md` (stolen laptop), tests.
- Acceptance: e2e against a relay with accounts and a test hook for the browser step: `login`
  returns URL + code + fingerprint in < 2 s; `--wait` returns bound; `request` while unbound →
  `account_required` with a hint; `logout` unbinds only its own key; audit rows carry the
  account id, never the display.

### 4.3a Invites and billing teams (review)

- Files: `internal/relay` (R3, invites, billing teams, admission in `pairRedeem`, `team_full`),
  `cmd/relay` (`admin invite|team`), `internal/daemon` + `cmd/agentnet` (`login --invite`,
  `team join` reports `team_full`), `Docs/cli/team.md`, tests.
- Acceptance: invites.md acceptance lines, on the CI matrix (plan 4.3: macOS, Linux, Windows),
  including the wrong-secret redeemer that is **not** admitted and the forged `pair_admit`.
  Files also: `internal/daemon` pairing issuer (`pair_admit` after `tag_R`).

### 4.1c Quotas

- Files: `internal/relay` (R4, charging, soft/hard modes, `quota_warning`), `internal/daemon`
  (display in `status`/`doctor`, one notification a day), tests.
- Acceptance: at 80 % a warning frame once per day per key; at 100 % soft mode routes and
  alerts (metric), hard mode refuses new mail with `quota_exceeded` while acks, pairing and
  presence work; a new month resets (fake clock); presence is not charged.

### 4.6a / 4.6b Telemetry (4.6b review)

- Files 4.6a: `internal/relay` (R5, counters, report intake and validation), `cmd/relay`
  (`admin stats`), tests. 4.6b: `internal/telemetry` (builder, schema), migration 22,
  `internal/daemon` (weekly send, IPC), `cmd/agentnet/telemetry.go`, `Docs/cli/telemetry.md`,
  `internal/store/store_test.go`, tests.
- Acceptance: telemetry.md acceptance lines; both rewind tests pass.

### 4.7a Feedback (review with 4.6b)

- Files: `internal/feedback` (seal/open), `internal/relay` (R6, intake, export),
  `cmd/relay` (`admin feedback export|open`), `cmd/agentnet/feedback.go`, `Docs/cli/feedback.md`,
  `CHANGELOG.md` (new, Keep a Changelog format), the release checklist in
  `Docs/ops/relay-runbook.md` ("every beta week ships one release with a changelog entry").
- Acceptance: feedback.md acceptance lines.

### 4.4a Release pipeline (review, R-4.4)

- Files: `.github/workflows/release.yml` (on a tag; the owner authorises tags; publishes a
  **draft** release with `SHA256SUMS`), `scripts/install.sh`, a local signing script the owner
  runs to sign `SHA256SUMS` offline and upload the signature before publishing (D36:
  OD-P4-19 (ii); the key is never in GitHub),
  `packaging/homebrew/agentnet.rb` template, `Docs/cli/install.md`, `internal/envelope`
  (`ready.min_client`), tests (install.sh in a container against a local file server: good
  sums pass, a flipped byte and a bad signature fail, no sudo used).
- Acceptance: a dry-run tag in a fork (or `workflow_dispatch` with `dry_run`) produces
  artefacts for darwin/linux/windows × amd64/arm64 and `SHA256SUMS` in a draft release; the
  signing script signs it with a test key and the result verifies in `install.sh`; Homebrew
  formula installs on the macOS runner.

### 4.4b Windows installer and windowless daemon (review, R-4.4)

- Files: `packaging/windows` (WiX source, per-user scope), release workflow signing step,
  `internal/service/schtasks.go` (a windowless launch: build `agentnetd` with the Windows GUI
  subsystem, or a tiny launcher; decided in the ticket with evidence), `Docs/cli/install.md`,
  tests.
- Acceptance: on the Windows runner the MSI installs without elevation, `agentnet setup
  --non-interactive` works, the logon task shows no console window (checked by the process's
  subsystem / no conhost child), uninstall removes the task and binaries but keeps the config
  dir; when a certificate is configured, `signtool verify /pa` passes on MSI and exes (none in
  wave 1, D36).

### 4.4c doctor and relay state

- Files: `cmd/agentnet/doctor.go`, `internal/daemon` (status relay fields), `internal/relayclient`
  (last error, since), `Docs/cli/doctor.md`, `Docs/cli/status.md`, tests.
- Acceptance: each doctor check has a failing and a passing test; `doctor` runs with the daemon
  stopped (then `service`/`socket` fail and the rest still run); output has no peer names or
  paths outside the config dir.

### 4.4d Unix hardening checks

- Files: `.github/workflows/ci.yml` (a job per OS that installs, starts, checks and uninstalls
  the real service), `internal/keystore` tests on Linux (Secret Service absent → 0600 fallback
  asserted) and macOS (keychain), fixes if any, `tests/phase4-manual.md` for what CI cannot do.
- Acceptance: the jobs pass on ubuntu and macos runners; a group-readable key file is refused
  on both.

### 4.9a setup

- Files: `cmd/agentnet/setup.go`, `Docs/cli/setup.md`, `Docs/agents/snippet.md` (setup
  paragraph), tests.
- Acceptance: [§setup](#setup) steps; re-running after each step completes the rest; the JSON
  step list; `--non-interactive` without `--telemetry` → exit 2 with a hint.

### 4.1b Deploy (review of the config)

- Files: `deploy/` (Dockerfile, `fly.toml` or `compose.yml` + Caddy/systemd per OD-P4-1/3),
  `scripts/backup.sh` (online backup → age → upload), `Docs/ops/relay-runbook.md` (deploy,
  rollback, restore, rotate the OAuth secret, suspend a team, incident checklist),
  `tests/phase4-manual.md`.
- Acceptance: a staging deploy (the owner creates the accounts) passes `doctor` from all three
  OSes; the production image is built without `testhooks` and a test fails if the hook route
  answers; staging and production share no database, domain, OAuth app or operator key; the backup job's latest file restores into a scratch relay (manual drill recorded);
  the container runs as non-root with a read-only root filesystem except the volume.

### 4.9b Clean-machine runs

- Acceptance (plan 4.9): on fresh CI user profiles on macOS, Linux and Windows: install from the
  release artefact → `setup --non-interactive --telemetry off` against the staging relay with
  a test OAuth hook → `team join <code>` → a first request arrives, **no config file edited**;
  plus one agent-driven run where Claude Code gets only the quickstart and the human confirms the
  login (owner OK for the ~1 USD run, like D35).

### 4.5a Docs

- Acceptance (plan 4.5): `TestCLIDocsMatchHelp` fails when a flag in `--help` is missing from
  `Docs/cli/<cmd>.md`; a tester who never spoke to the owner completes the quickstart (recorded
  in beta week 1 by the owner; 4.9b's agent run is the automated stand-in).

### 4.8a–c

- 4.8a: `Docs/review/security-pack.md`: what to review (grant issuance and enforcement, the
  sensitive-grant rule (plan 4.8), plus relay auth, accounts/web, installer), threat models by
  link, how to build and run the e2e tests, known limitations, open Lows. The owner chooses and
  pays the reviewer (OD-P4-20).
- 4.8b: OS user-presence (Windows Hello `UserConsentVerifier`, macOS LocalAuthentication; Linux
  none) for approvals, off by default, per OD-P4-18.
- 4.8c: OD-P3-11 (b).

### Wave 1 go/no-go

Owner checklist before the first invite is sent: G-4.0 signed; R-4.2, R-4.4, R-4.6 fixed;
staging and production relays pass `doctor` from 3 OSes; restore drill done; privacy note and
beta terms published (OD-P4-15); demo video recorded (plan: at the **start** of Phase 4,
owner); unsigned Windows builds accepted for wave 1 (D36; SmartScreen warning documented); the owner's own team has used the hosted relay for a
week.

## Owner-side work and lead times

| Item | Lead time | Needed by |
|---|---|---|
| **Windows code-signing certificate** (OD-P4-12) | **4–6 weeks** | 4.4b; wave 1 unless unsigned is accepted |
| Domain name + DNS (OD-P4-14) | days | 4.0a staging, 4.1b, 4.4a (install URL) |
| Hosting account (OD-P4-1) and object storage for backups | 1 day | 4.1b |
| GitHub OAuth app (and email provider + SPF/DKIM if email) (OD-P4-2) | 1 day (email: days for deliverability) | 4.2b staging |
| Operator age/HPKE key pair for backups and feedback, kept offline with a backup copy | 1 hour | 4.1b, 4.7a |
| Release-signing key (OD-P4-19) | 1 hour | 4.4a |
| Privacy note and beta terms (OD-P4-15) | 1–2 weeks with review | wave 1 |
| Outside security reviewer (OD-P4-20) | 2–6 weeks to book | before wave 2 |
| Demo video (plan) | — | start of Phase 4 |

## Cost estimates (approximate; check current prices at purchase)

| Item | Estimate |
|---|---|
| Relay on Fly.io (1 shared-CPU machine, 256–512 MB, 1–3 GB volume) | ~3–8 USD / month |
| Relay on Hetzner Cloud (smallest shared x86/Arm VM, IPv4, snapshots) | ~4–7 EUR / month |
| Backup object storage (a few GB) | < 1 USD / month |
| Domain | ~10–20 USD / year |
| Uptime monitor | free tier |
| GitHub OAuth | free |
| Email magic link (transactional provider, beta volume) | free tier to ~15 USD / month, plus setup time |
| Windows code signing | cloud signing service ~10 USD / month (eligibility rules vary by country and entity type: check) **or** an OV certificate with hardware token / cloud HSM ~200–500 USD / year |
| Outside security review of a small, well-scoped surface | widely varying: from a few thousand USD (independent reviewer, a few days) to tens of thousands (firm) |
| Real-agent runs (4.9b agent run, harness checks) | ~1–3 USD per run (3.H experience) |

At 10–30 teams the relay's compute, storage and bandwidth are negligible; the real costs are the
signing certificate, the outside review and the owner's time.

## Top security risks of a hosted, account-bound relay (honest list)

1. **Account binding de-pseudonymises the metadata.** Until now the relay saw a graph of keys;
   from 4.2 it sees a graph of **named people** (GitHub logins or emails): who talks to whom,
   when, how much, who is online. The operator, the hosting provider, anyone who breaches the
   host or obtains a backup, and anyone who can compel the operator, get that graph for every
   beta team. Content stays sealed; metadata does not. Mitigations are partial: minimal
   retention (logs and backups 14 days), encrypted backups with an offline key, no IPs in the
   DB, documenting it plainly. There is no cryptographic fix in scope (that would be sealed
   sender / mixnets).
2. **Supply chain of the installer.** `curl | sh`, the Homebrew tap and the MSI give whoever
   controls the release pipeline, the owner's GitHub account, the domain or the signing keys
   **code execution on every tester's machine, next to their identity keys and repositories**.
   This is a bigger risk than the relay itself. Mitigations: signed sums verified by the
   installer, release workflow gated on an owner-approved environment, 2FA / hardware keys on
   GitHub and the registrar, protected tags, no third-party actions without a pinned SHA.
3. **New web attack surface.** OAuth, sessions, CSRF and a device-flow page are the first web
   code in the project. Device-code phishing can bind an attacker's key to a victim's account
   (mitigated by the typed code and fingerprint page, and bounded: an account grants no peer
   trust, only quota and billing-team admission). The CLI must not pass a relay-chosen login
   URL to the OS opener unchecked (review 50 H3), and billing-team admission must be vouched
   by the issuer's daemon, not granted on a lookup match (review 50 H2). A web bug that allows binding any key to any
   account would let an attacker spend quotas, occupy seats and get admitted to teams' billing
   teams, still without reading content.
4. **Relay compromise = denial of service plus metadata, and the relay is now a single point.**
   A compromised relay cannot read or forge mail and cannot MITM pairing v2, but it can drop
   everything, delay selectively, delete queued mail, lie about account state, and (before
   auth v2, or against a relay that still accepts v1) replay authentication elsewhere. Every tester's default relay URL points at one
   instance. The ack-deletion attack is why auth v2 binds the relay origin (4.0a).
5. **Telemetry and feedback are the only channels that carry data to the operator on purpose,**
   and per-team counters on 2–5-person teams are effectively per-person. A schema slip or an
   agent that pastes a repository into `agentnet feedback --yes` leaks content. Mitigations:
   the closed integer schema with strict refusal, the no-content marker tests, feedback sealed
   to an offline key, the confirmation step, and the snippet's instruction to agents.
6. **Denial of service by many IP prefixes** (a botnet) is not stopped by the 4.0 limits; the
   beta accepts it and relies on the monitor and provider-level protection.
7. **Operational single points:** one owner, ~10 h/week, holds the offline backup key and the
   signing keys; losing the backup key makes backups useless, and incident response depends on
   one person's availability. The runbook and a sealed second copy of the keys are the
   mitigation.

## Owner decisions needed

| # | Decision | Options | Recommendation |
|---|---|---|---|
| OD-P4-1 | Relay hosting and backup location | (a) Fly.io (platform TLS, Fly volume, managed deploys); (b) Hetzner Cloud VM (own TLS, own OS patching); backups in a different provider/region either way | **(a) Fly.io for the beta** (plan §10): least operations work for a ~10 h/week owner; the binary is identical, so moving to Hetzner later is a DNS change plus a DB copy. Choose (b) if cost or EU-only hosting matters more than ops time; Hetzner is cheaper and gives full control. Backups: an object store at another provider |
| OD-P4-2 | Account provider | (a) GitHub OAuth only; (b) email magic link only; (c) both | **(a)** (plan §10): every target user has GitHub, no email infrastructure (sender domain, SPF/DKIM, deliverability, a provider seeing addresses), no scopes requested. Add (b) only if a wave-1 tester lacks GitHub |
| OD-P4-3 | Where TLS ends on the hosted relay | (a) platform/proxy termination + `--behind-proxy` with the platform's client-IP header; (b) TLS in the relay binary (ACME), TCP passthrough | **(a)** on Fly.io (simplest, platform certificates); **(b)** on Hetzner (no extra proxy). Both are implemented in 4.0a for self-hosters. Note that under (a) the platform's proxy sees routing metadata in the clear; it already could via the host |
| OD-P4-4 | Pin the relay's TLS key in daemons | (a) no pinning (system roots); (b) pin the hosted relay's public key | **(a)**: content never depends on TLS; pinning adds rotation outages. Revisit if metadata protection against a CA-level attacker becomes a goal |
| OD-P4-5 | Unit of the "300 relay sessions per team per month" cap | (a) device-days; (b) mail envelopes (30 000 / 3 GiB per billing team per month); (c) daemon-reported sessions | **(b), soft cap** (warn at 80 %, alert at 100 %, no cut-off in the beta; hard mode behind a flag for later). See relay-hosted.md §4 |
| OD-P4-6 | How members after the first join a billing team | (a) admission by pairing (a team invite from a member admits the redeemer); (b) separate seat codes; (c) no billing teams, per-account quota | **(a)**: keeps plan 4.9's single `team join` step and adds no new metadata |
| OD-P4-7 | Waves and seats | wave sizes, spacing, seats per billing team | **10 / 10 / 10 teams**; wave 2 at beta week 4 **after 4.8 findings are fixed** (plan), wave 3 at week 6; **8 seats**; codes expire after 30 days |
| OD-P4-8 | Daemon telemetry report default | (a) opt-in: interactive setup asks, non-interactive requires `--telemetry on|off`; (b) on by default with disclosure and `telemetry off` (the plan's "opt-out flag"); (c) off, never asked | **(a)**. The plan note and D7 say per-kind counts reach the dashboard only "by opt-in reporting"; opt-in is the defensible basis for EU testers; the beta invitation asks teams to turn it on, because Gate 2 needs it (telemetry.md table). Relay counters stay on (operational, documented) |
| OD-P4-9 | Waitlist | (a) external form; (b) a page on the relay; (c) a GitHub issue template | **(a)** (no new relay attack surface; privacy note on the form) |
| OD-P4-10 | Dashboard and telemetry retention | (a) `relay admin stats` CLI + CSV; (b) static HTML; (c) hosted dashboard service | **(a)**; keep counters until 90 days after the beta, then delete |
| OD-P4-11 | Encryption tool for backups and feedback | (a) the `filippo.io/age` module (a new dependency in the relay binary only) so the owner can also use the `age` CLI; (b) only the HPKE code already in the repo, chunked for large files | **(a)**: a well-reviewed format for streaming large backups; keep it out of the daemon and CLI (feedback sealing in the CLI then needs age too, **or** the CLI uses HPKE and only backups use age: the ticket picks the smaller dependency footprint) |
| OD-P4-12 | Windows code signing: route and timing | (a) a cloud signing service (monthly fee, identity validation; check eligibility for an individual in the owner's country); (b) an OV certificate on a hardware token / cloud HSM; (c) ship wave 1 unsigned with a documented SmartScreen click-through | **Apply for (a) now if eligible, else (b); lead time 4–6 weeks before 4.4b.** Keep (c) as the fallback only if the certificate is late; unsigned installers teach testers to click through warnings, which is the wrong habit for a tool that holds keys |
| OD-P4-13 | Windows package form | (a) per-user MSI (no elevation); (b) per-machine MSI; (c) signed zip + `agentnet setup` only | **(a)**: no admin rights needed (matches the owner's work PC), and the daemon is per-user anyway; `winget` later |
| OD-P4-14 | Domain and the default relay URL compiled into binaries | a project domain (the "AgentNet" name collides, plan §10); `relay.<domain>`; staging at `relay-staging.<domain>` | **Buy a Dorylinae-named domain now**; compile `wss://relay.<domain>` as the default; `--relay` overrides; self-hosters unaffected |
| OD-P4-15 | Privacy note, beta terms, legal review | (a) short privacy note + beta terms drafted in 4.5a, owner reviews; (b) the same plus a paid legal review; who is the data controller | **The owner decides** (not a technical call). At minimum: what is collected (accounts, IPs in logs ≤ 14 days, counters, feedback), why, retention, deletion on request, the processors (host, object storage, GitHub/email provider), the controller's contact. A legal review is advisable if testers are in the EU |
| OD-P4-16 | May an agent send feedback with `--yes` | (a) yes, with snippet guidance; (b) no, TTY confirmation only; (c) an agent's `feedback --yes` only drafts the note locally, and a human sends it with `agentnet feedback send` (TTY) or from the approval window (added by review 50 M10) | **(a)** by the author: agents are first-class users; the snippet tells them to show the human the text first. Review 50: an agent steered by a prompt it read (a peer's result, a web page) can send up to 5 × 4 KiB a day of whatever it holds to the operator without a human seeing it; the text reaches only the operator's offline key, so this is a tester-privacy issue, not an exfiltration channel to an attacker. (c) keeps the agent path with a human in the loop at the cost of one step |
| OD-P4-17 | Local content retention before the beta (OD-P3-8 (c) and "the other content tables") | (a) no automatic deletion in the beta, documented; (b) 365-day retention for experience records, requests, results, debates | **(a)** for the beta (users' own machines; deletion adds risk of losing records people rely on); document it in known limitations; decide (b) from beta feedback |
| OD-P4-18 | OS user-presence for approvals (OD-P3-12 → "Phase 4, before 4.8") | (a) ticket 4.8b before wave 2; (b) defer past the beta | **(a)**, off by default: it strengthens the approval gate the outside review will examine. Needs a short spec addition first |
| OD-P4-19 | Release artefact signing, and where the key lives | (a) plain Ed25519 over `SHA256SUMS` (verified by OpenSSL ≥ 3 or minisign, [§install](#install-44)), public key embedded in `install.sh` and docs; (b) Sigstore cosign keyless; (c) checksums only. Key custody: (i) in a GitHub environment secret with the owner as required reviewer; (ii) offline on the owner's machine: CI publishes a **draft** release with `SHA256SUMS`, the owner signs locally and uploads the signature, then publishes | **(a)**, and review 50 M8 recommends **(ii)**: with (i) whoever takes over the owner's GitHub account can approve the environment and sign, so the signature adds nothing against the largest risk (top risk 2); (ii) costs the owner one command per release. A POSIX shell cannot verify any signature unaided; (b) needs cosign on the tester's machine |
| OD-P4-21 | Queue flooding on self-hosted relays **without** accounts (new, review 50 M1) | (a) accept and document: a stranger who knows a victim's key and uses many keys can fill the victim's offline queue (delay, not loss; outbox resends), and many keys can fill the relay-wide queue; (b) recipient-declared senders: the daemon sends the relay the keys of its peers (`queue_allow`), and the relay queues only from those for that recipient (the relay already sees this graph from routing); (c) a key allowlist file for self-hosted relays (`--allow-keys`) | **(a) for the beta** (self-hosted relays serve a known group, and the attacker needs the victim's key), listed in known limitations; (b) if a self-hoster reports abuse. Not needed on the hosted relay (accounts) |
| OD-P4-20 | Outside security review | scope, reviewer, budget | **Scope: grant issuance/enforcement and the sensitive-grant rule (plan), plus relay auth v2, accounts/web and the installer.** Book early (2–6 weeks); the owner chooses and pays |

## Phase 3 and backlog leftovers

Folded into Phase 4: review-05 M2 (4.0b), review-08b L1/L5 (4.0c), review-05 L6 (4.1a), board
todos "surface relay state to CLI" (4.4c), "verify keychain and 0600 on Unix" and "run
linux/macOS service install by hand" (4.4d), "run agentnetd windowless at logon" (4.4b),
"per-IP pairing rate limit at proxy" (4.0b/4.0c), OD-P3-11 (4.8c), OD-P3-12 (4.8b), D33
revisit (4.8a).

Not folded (decide during the beta; none blocks it): review-05 M1 (direct path at-most-once;
mail's outbox resends until the app ack, so only non-mail types are affected); INV-4 backlog
(`OnPeerOnline` reset overwrite; a hand-off into a dying connection stays `relayed` ~1 min);
session recovery after a peer restart; team-invite table prune and `team delete` not cancelling
invites; the "grantor sees holder fetch activity" feature; review Lows in 07–48; COM0/LPT0
(owner call).

## Conflicts and interpretations found while writing

1. **"300 relay sessions per team per month"** cannot be counted by the relay as written: it
   sees neither teams (`team` is `""` on the wire) nor sessions (sealed mail). Hence billing
   teams and OD-P4-5.
2. **Plan 4.6 says "relay-side counts only … requests by type and urgency … opt-out flag";** D7
   and the plan's own note say per-kind counts are daemon-side and reach the dashboard only by
   **opt-in**. This plan follows D7 (daemon report) and puts the default to the owner (OD-P4-8).
3. **Plan 4.2 acceptance "a daemon cannot connect without a bound account"**: taken as "cannot
   do anything but bind": an unbound daemon must connect to run the device flow (4.9 needs it
   agent-runnable). The accounts.md state table makes this exact.
4. **Relay auth v1 does not name the relay.** Harmless with one local relay; with accounts it
   enables quota theft and deleting a victim's queued mail via forwarded authentication. Auth v2
   (4.0a) is a protocol change the gate needs; it was not in review 05.
5. **Relay persistence has no migration system** (the queue schema is `CREATE IF NOT EXISTS`);
   accounts need one. Relay migrations are numbered R1… so they never collide with the daemon's
   22+.
6. **Plan 4.4 "signed MSI for Windows"** and the existing per-user Task Scheduler design (no
   admin): the MSI is per-user (OD-P4-13), and "`agentnetd install` registers the service"
   stays a per-user task.
7. **Plan 4.5 "one page per command (generated from `--help`)":** the hand-written
   `Docs/cli/*.md` are richer than `--help`; this plan keeps them and adds a test that they
   cover every flag, instead of generating them.
8. **Plan 4.1 "TLS"** is in the gate (4.0a), not in 4.1, because D17 makes it a precondition
   for any non-loopback relay, including a self-hosted one.
9. **Team invites vs beta invites:** two different codes; invites.md keeps them apart and makes
   the team invite also admit to the billing team, so 4.9 stays one command per member.
