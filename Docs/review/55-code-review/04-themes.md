# 55 / 04: Cross-cutting theme passes

A chunk reviewer sees ~2,000 lines. Some defects exist only **between** chunks: a check that
each side assumes the other does, a sanitiser applied in one sink and forgotten in another,
a lock order across packages, a spec rule nobody owns. The theme passes find those.

**When:** after **all** 31 chunk reports exist (they are the input). Themes run in parallel,
each by one fresh Opus reviewer, across the **whole** codebase at `6cc26a7`.
**Brief:** [01-rubric.md](01-rubric.md) applies unchanged (severity, format, rules). Ids are
`Tn-01`… (`T6a-01` for the split theme). Report path: `themes/Tn.md` ([05-process.md](05-process.md) §6).
**Every theme reviewer must:**
1. Read the "Assumptions and contracts" sections of the chunk reports listed for the theme,
   and **resolve every assumption marked `unchecked`** that falls in the theme: say
   "holds" (cite the other side) or raise a finding. List them all in a section
   **"Assumptions resolved"** (one line each: chunk id, assumption, holds/finding id).
2. Read the chunk findings and leads in the theme's area, so that you do not re-report them;
   cite their ids instead. Chunk findings are **not** in 02; treat them like open findings.
3. Start from the grep list, but do not stop at it: the list is a starting inventory.
4. Produce the inventory table the method asks for (this is the theme's coverage evidence,
   in place of "Checked and fine").

The grep patterns are ripgrep regexes; run them over `cmd internal tools tests scripts .github deploy`
with `--glob '!*_test.go'` unless the theme says tests too.

---

## T1 · Parsing and validation of peer- and relay-supplied input

**Scope:** every place bytes from the relay, a peer (mail kinds, session data, fetch
requests and responses, presence, pairing frames), an unauthenticated client (relay side),
or a file handed to the user (`decision verify`) are decoded, validated, stored or acted on.
**Start from:** `json\.(Unmarshal|NewDecoder)`, `ParseStrict`, `Decode`, `base64\.`,
`\.Strict\(\)`, `strconv\.(Atoi|Parse)`, `time\.Parse`, `utf8\.`, `envelope\.(Parse|Classify|ParseHeader)`,
`\[\]byte\(`, `io\.ReadAll`, `LimitReader`, `MaxBytes`, `\bMax[A-Z]\w+\b` (size constants), `hasControl|hasC1|invisible`.
**Method:** build a table: **source → decoder → size bound before decode → strictness
(unknown/duplicate members, types, integer range, UTF-8, controls) → where the value ends up**.
Then look for: a value validated at one layer and re-parsed differently at another
(parser differentials, e.g. `encoding/json` vs `ParseStrict`); base64 without `Strict()` on
anything compared or signed; integers truncated to `int`; a size bound after allocation;
a peer value used as a map key, file name, SQL fragment, argv element, log field or
Markdown without its own check.
**Chunk assumptions to check:** C01–C10, C13, C14, C17, C18, C20–C25.

## T2 · Concurrency, goroutine lifecycle, shutdown, locking

**Scope:** all packages; the daemon (one process, many subsystems on one SQLite connection)
and the relay (one process, thousands of connections).
**Start from:** `go func`, `go [a-z]\w*\(`, `sync\.(Mutex|RWMutex|Map|Once|WaitGroup|Cond)`,
`atomic\.`, `chan `, `select \{`, `time\.(AfterFunc|NewTimer|NewTicker|Tick)`,
`context\.With(Cancel|Timeout|Deadline)`, `func \(\w+ \*\w+\) Close\(`, `SetMaxOpenConns`,
`BeginTx|\.Begin\(`, `LockOSThread`; in tests: fields written from hooks/fakes (review 31).
**Method:** (1) a **lock inventory**: every mutex, what it guards, and every place two are
held at once, giving a lock order graph; find cycles and callbacks invoked under a lock
(the approval `Store.mu` rule N3 is one known contract). (2) a **goroutine inventory**:
every long-lived goroutine, who starts it, what stops it, and whether `Close` waits for it.
(3) **shutdown order** of `daemon.Run` and the relay's `Close`/drain: nothing uses the DB,
keystore, relay client or IPC after it is closed; no deadlock when a peer or the relay is
slow. (4) any DB call made while a tx is open on the single connection (deadlock), or a
tx held across network I/O. (5) timers and `AfterFunc` that outlive their owner.
Note: `-race` cannot run locally (no cgo). If a race is suspected, describe the
interleaving precisely; the verifier may ask the Orchestrator for a CI `-race` run.
**Chunk assumptions to check:** all chunks' concurrency-related bullets, especially C01,
C04, C06–C08, C10, C12, C14, C26, C28.

## T3 · Cryptography and key handling, end to end

**Scope:** identity (Ed25519), agent cards, Noise XX, HPKE mail and presence, mailbox key
rotation, pairing v2 (Argon2id, HMAC tags), capability tokens, Decision signatures, relay
auth v1/v2, approval code MAC, webhook signing, release signing (`releasesign`, `install.sh`),
randomness.
**Start from:** `crypto/`, `ed25519\.`, `hpke`, `hmac\.`, `subtle\.`, `argon2`, `noise\.`,
`sha256|blake2`, `rand\.(Read|Int|Reader)`, `math/rand`, `bytes\.Equal`, `==` next to
`sig|mac|tag|hash|digest`, `dorylinae-[a-z-]+-v[0-9]` (domain tags), `keystore\.`, `Seed|PrivateKey`.
**Method:** (1) a **domain-separation table**: every signed/MACed/hashed/KDF'd message, its
tag, what it binds (from, to, ids, time, role, origin) and what verifies it; look for two
messages a signer produces that could be confused, missing binding of recipient/origin/role,
and a verifier that checks less than the signer binds. (2) a **key lifecycle table**: every
key/secret: generation (source of randomness), storage (keystore mode and permissions,
memory only), use, rotation, deletion, wipe, and every place it could be logged or returned.
(3) constant-time comparisons of every MAC/tag/secret; `math/rand` never used for secrets or
ids that must be unguessable; nonce/counter reuse; strict signature encodings.
(4) the release chain: key embedded ↔ offline key ↔ `SHA256SUMS` format ↔ `install.sh`
verification ↔ minisign legacy format.
**Chunk assumptions to check:** C03–C07, C09, C12, C15, C25, C27, C29.

## T4 · Filesystem paths, permissions, temp files, process execution

**Scope:** every file the daemon, relay, CLI or tools create, open, rename or delete; every
directory permission; every child process.
**Start from:** `os\.(OpenFile|Create|WriteFile|ReadFile|Open|MkdirAll|Mkdir|Rename|Remove|RemoveAll|Chmod|CreateTemp|MkdirTemp|Symlink|Link|Lstat|Stat)\(`,
`os\.Root|OpenRoot`, `filepath\.(Join|Clean|Abs|EvalSymlinks|Rel|VolumeName)`, `0o?[67][0-7][0-7]`,
`exec\.(Command|CommandContext|LookPath)`, `&exec\.Cmd\{`, `SysProcAttr`, `\.Env =|os\.Environ`,
`GetSystemDirectory`, `windows\.(SetSecurityInfo|GetSecurityInfo|CreateJobObject)`, `icacls`, `umask`;
shell: `mktemp`, `rm -rf`, `>` redirections, `chmod`.
**Method:** (1) a **file inventory**: path source (fixed, config dir, user flag, peer),
create mode and when permissions are applied (before any secret byte), atomic write
(temp + rename in the same dir), symlink handling at the final component and at parents,
behaviour when the path exists. (2) a **process inventory**: program path resolution (never
PATH or the current directory on Windows for a fixed tool), argv construction (never a
shell, never peer text as an option), environment (minimal, no daemon secrets, `GIT_*`
scrubbed), process-tree control and cleanup on every exit path. (3) Windows specifics:
ACLs on created dirs, named pipe DACL, junctions, 8.3 names, ADS, reserved names,
`\\?\` and UNC. (4) shell scripts: quoting, `set -eu`, temp-file safety.
**Chunk assumptions to check:** C02, C05, C09, C10, C12–C16, C25, C26, C28, C30, C31.

## T5 · The audit/log never-contains-content invariant, and privacy

**Scope:** every audit event, log line, error returned to a peer or over IPC, notification,
webhook payload, relay log/metrics/journal, status/doctor output; and the metadata the
relay learns (relay-hosted.md "What the hosted relay learns").
**Start from:** `\.Append(Tx)?\(`, `audit\.`, `slog\.|log\.Print|logger\.`, `\.Error\(\)` inside
audit details or log fields, `fmt\.Errorf\(.*%[sqv]` on peer data, `Detail|detail:`,
`notify\.`, `payload`, `journal\.Append`, `metrics|prometheus`, `TestPhase[0-9]AuditHasNoContent`,
`TestAuditInventory`.
**Method:** (1) an **audit inventory**: every `Append`/`AppendTx` call site, its action and
detail keys, whether each value can carry content, a path from a peer, a secret or an
unbounded peer string; compare with each spec's §Audit table and with
`TestAuditInventory`. (2) a **log inventory** at Info and above (Debug too if it can be
enabled in production): any line carrying bodies, frames, keys, codes, URLs with secrets,
or `err.Error()` of an error that quotes peer bytes. (3) errors sent **to peers** (reject
reasons, fetch errors, ack bodies): must be enum codes, never `err.Error()`. (4) do the
no-content tests actually search every sink, including the logs and the relay's queue
file (R10 M2 pattern: base64 forms)? (5) the relay: log lines, metrics labels and the journal
carry no payloads, emails or full keys beyond what the spec lists.
**Chunk assumptions to check:** all chunks' audit/log bullets; especially C01–C03, C07, C10,
C12, C17, C18, C21, C22, C25–C28.

## T6 · Spec-versus-code drift (split in three passes)

Each pass takes a set of specs and, for every **normative** statement (MUST/never/always,
a limit or constant, an error code, a state or transition, an audit row, an IPC method or
field, a CLI flag or exit code), finds the code that implements it. Output a **drift table**:
spec § → statement → code `file:line` → matches / differs (finding id) / not implemented
(finding id or "not built yet, ticket x.y"). Differences that are harmless go in the table
as Info; the rest are findings. Also list code behaviour with **no** spec (undocumented
IPC methods, frames, flags, audit actions).

- **T6a** — Phases 0–1: `ipc.md`, `agent-card.md`, `envelope.md`, `pairing.md`, `session.md`,
  `mail.md`, `team.md`, `presence.md`, `request.md`, `notify.md`, and `Docs/cli/` pages for
  those commands.
- **T6b** — Phase 2: `work-session.md`, `approval.md`, `grant.md`, `consult.md`, `device.md`,
  and their `Docs/cli/` pages.
- **T6c** — Phases 3–4: `debate.md`, `decision.md`, `audit.md`, `experience.md`,
  `relay-hosted.md`, `accounts.md` (4.2a parts only), and for `invites.md`, `telemetry.md`,
  `feedback.md` only confirm that no partial code exists that a later ticket might assume is
  finished; plus `Docs/cli/relay.md`, `doctor.md`, `status.md`, `install.md`, `log.md`.

**Start from:** each spec's tables (IPC methods, errors, audit, limits) and grep the
constants. Cross-check the IPC method list in `00-index.md` §3.a against `ipc.md`.
**Chunk assumptions to check:** the "Spec" fields of all chunk findings in the pass's area
(do not re-report them; use them as known drift).

## T7 · Error handling: fail-open versus fail-closed

**Scope:** every decision point that grants, allows, trusts, accepts, serves or releases
something; every place an error is swallowed.
**Start from:** `if err != nil \{\s*(return nil|return true|continue|break)`, `_ = `,
`_, _ =`, `// ignore|best.effort|nolint:errcheck`, `sql\.ErrNoRows`, `errors\.Is\(`,
`recover\(\)`, `default:` in switches over kinds/types/roles/states, functions returning
`bool` for authorization (`IsPaired`, `eligible`, `allowed`, `valid`, `Holds`, `UnverifiedPeer`).
**Method:** (1) an **authorization inventory**: every allow/deny function, what it returns on
an error (DB error, parse error, missing row, nil callback), and every caller's handling.
Any `false`-on-error for a deny check, or `true`-on-error for an allow check, is a finding.
(2) swallowed errors that break a documented guarantee (audit append ignored where the spec
requires the action to fail; outbox submit ignored after telling the user "done").
(3) `switch` defaults over peer-controlled enums that fall into a permissive branch
(R25 L2 pattern: unknown role treated as holder). (4) partial failure: a multi-step
operation that commits step 1 and reports success after step 2 failed (R18 L2 pattern).
**Chunk assumptions to check:** all chunks' error-path bullets; especially C01, C03, C06,
C07, C09–C14, C17, C18, C21.

## T8 · Local IPC authority (the prompt-injected local agent)

**Scope:** every IPC method (`00-index.md` §3.a lists 70; confirm against the registry) and
every CLI command, from the point of view of a local agent that can call any of them,
repeatedly, in any order, with any parameters (approval.md threat model, row 1).
**Start from:** the registry in `internal/daemon/daemon.go` and each `register`/handler;
`approval\.(Create|Action)`, `Precondition|Perform`, `DORYLINAE_[A-Z_]+` (env the daemon reads).
**Method:** a **method table**: method → what it reads (content? whose?) → what it changes →
gated by approval? → rate-limited? → audited? Then look for **chains**: sequences of
ungated methods that reach an effect that should be gated (e.g. a policy auto-issuing a
grant; `mail_submit` of a daemon-owned kind; `notify_set` pointing a webhook at an attacker
URL to exfiltrate titles and metadata; `team_invite` + `peers` to add a peer that then gets
D5 trust; `device_link` intents; `approval_open` spam; reading a quarantined result through a
side method). Also: which environment variables change security behaviour
(`DORYLINAE_APPROVAL`, `DORYLINAE_KEYSTORE`, `DORYLINAE_HOME`, …) and whether an agent that
can only call the CLI (not start the daemon) can influence them.
**Chunk assumptions to check:** C07, C11–C14, C16–C21, C23–C28.

## T9 · Persistence, transactions, migrations and durability

**Scope:** the daemon store (migrations 1–21), the relay store (R1–R2), every transaction,
every after-commit hook, crash windows between DB state and network/keystore/file effects.
**Start from:** `BeginTx|Begin\(|Commit\(|Rollback\(`, `AfterCommit|After\(`, `Apply\(`,
`ON CONFLICT|INSERT OR (REPLACE|IGNORE)`, `CREATE (TABLE|TRIGGER|INDEX)`, `ALTER TABLE`,
`DELETE FROM`, `secure_delete|journal_mode|busy_timeout|wal_checkpoint`, `pending\w+ *(=|sync\.Map)`.
**Method:** (1) for each mail kind and each IPC write: the effects inside the tx, the
effects after commit, and what happens if the process dies between them (message lost?
sent twice? state stuck?). (2) migrations: atomic, ordered, safe with two openers, safe on
every earlier schema, rewind tests updated (HANDOFF §4/§5). (3) retention: which tables
keep content, for how long, and whether deletion is real (`secure_delete`, WAL checkpoint,
O-171). (4) the `pending*` map pattern (O-077): any instance where the leak is not small,
or where a stale entry is later *used*.
**Chunk assumptions to check:** C02, C03, C06, C07, C09, C11–C13, C17, C18, C20–C24, C26.

## T10 · Resource bounds and denial of service (daemon and relay)

**Scope:** everything that grows with input from a peer, the relay or an internet client:
tables, maps, slices, goroutines, timers, file sizes, CPU per message, memory per message.
**Start from:** `make\(\[\]|make\(map`, `append\(`, `map\[string\]` fields on long-lived structs,
`INSERT INTO` without a cap check, `go func` per message, `Argon2|argon2\.IDKey`,
`time\.AfterFunc` per message, `ReadAll`, loops over peer-supplied counts.
**Method:** a **growth table**: resource → who can grow it (actor from rubric §1) → bound
(per key/peer/relay-wide) → eviction → worst case on the owner's machine and on the
review-52-sized relay. Pay attention to CPU amplification (one cheap frame → expensive work:
HPKE open, signature verify, Argon2id, keystore/keychain calls, git subprocesses) and to
the daemon side, which chunk reviews tend to rate lower than the relay.
**Chunk assumptions to check:** C01–C04, C06–C10, C17, C20, C23, C24, C27.

## T11 · Untrusted text reaching a human (terminal, notifications, windows, Markdown, webhooks)

**Scope:** every sink that shows text a peer, the relay or an agent controls: CLI human
output, `--json` output, desktop notifications, the approval window and terminal prompt,
Decision Markdown, webhook payloads (Slack/Discord markup), logs viewed in a terminal.
**Start from:** `notify\.Clean`, `DisplayQuote|DisplayArgv`, `termSafe`, `Visible`,
`hasControl|hasC1|invisibleRune|stripLongDigits`, `fmt\.Fprint.*(title|name|summary|reason|note|output)`,
`printf|Println` in `cmd/agentnet`.
**Method:** a **sink table**: sink → which fields → sanitiser used → what it strips/escapes
(C0, C1, DEL, bidi/`Cf`, zero-width, graphic-invisible (R46 H1), ANSI, newlines, markup) →
length cap. Find sinks with a weaker sanitiser than their siblings, fields that bypass it,
and places where sanitising changes meaning (a stripped character that turns two tokens
into one, a decoy code). O-100 and O-129 are known: report only what goes beyond them.
**Chunk assumptions to check:** C06, C11–C14, C19–C25, C27, C28.

## T12 · Time, clocks and replay windows

**Scope:** every time window in the specs (pairing 60 s / 10 min, mail +10 min / −30 d /
35 d / 14 d, outbox 7 d, relay TTL 7 d, presence ±10 min, grants `ts`/`req` 11 min,
approvals 10 min, device offers 10 min, keys 7+7+7 d, invites 48 h, debate turn deadlines).
**Start from:** `time\.Now\(\)`, `\.now\(\)|Now:|nowFn|clock`, `time\.Since|time\.Until`,
`Add\(-?[0-9]`, `\bUTC\(\)`, `Truncate|Round`, `StoreTimeFmt|RFC3339`, string comparison of
timestamps (`<`, `>` on TEXT columns).
**Method:** a **window table**: window → spec § → code → clock used (sender's signed time,
receiver's wall clock, monotonic) → behaviour under ±skew and a clock step → text vs time
comparison correctness (fixed-width formats, `Z` vs offsets, fractional seconds) → injected
clock used consistently (R05 L3, R20 L9 patterns). Look for replay possible just outside a
window, windows that disagree between sender and receiver, and wall-clock timers that
should be monotonic.
**Chunk assumptions to check:** C01, C04, C06–C09, C12, C13, C17, C18, C20, C23.

## T13 · Cross-platform parity (Windows, macOS, Linux)

**Scope:** every file pair split by OS or build tag, and every behaviour that the code only
enforces on some OSes.
**Start from:** file names `_(windows|unix|linux|darwin|other)\.go$`, `//go:build`,
`runtime\.GOOS`, `syscall\.|windows\.|unix\.`.
**Method:** a **parity table**: guarantee → Windows implementation → macOS → Linux → the
`_other` fallback. Find `_other` or unsupported-OS fallbacks that **silently succeed**
(e.g. a permission check that returns OK, a kill that does nothing), guarantees the specs
state without an OS qualifier but only one OS enforces, and code that CI never compiles or
tests on one OS (compare with `.github/workflows/ci.yml` matrix and the known local gaps:
symlink tests skipped on Windows here, `-race` only in CI, macOS ACLs O-160).
**Chunk assumptions to check:** C05, C08, C12–C16, C27, C28.
