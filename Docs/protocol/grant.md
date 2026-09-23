# Grants, capability tokens and fetch

Status: **draft** for Phase 2 (plan steps 2.2 grant issuance, 2.3 enforcement, 2.4
sensitive-grant rule). Ticket split: [../review/23-phase2-tickets.md](../review/23-phase2-tickets.md).
Change this document first.

A **grant** is scoped, expiring, revocable read access that the requester of a
[work session](work-session.md) gives to the worker, for one local resource of the
requester's machine. It is the only source of authority in AgentNet: untrusted text (a
brief, a result, a `requested_grant` hint) never grants anything.

Terms: the **grantor** (issuer, A, the requester of the session) owns the resource and
**enforces** the grant; the **holder** (audience, B, the worker) presents it.

## Design summary

- The grantor mints a **signed token** (Ed25519 over canonical JSON) binding action,
  resource, scope, expiry, session and audience. It sends it to the holder in a sealed
  `grant` mail.
- The holder verifies it **offline** (signature by the grantor's identity key, audience =
  itself, not expired, session open) and uses it with `agentnet fetch`.
- A fetch is an **online** request to the grantor's daemon over the existing Noise session
  ([session.md](session.md)): the grantor checks the token **on every call** against its own
  database, so revocation is effective on the **next** call, with no propagation delay.
- Phase 2 serves two resource kinds, both read-only: a **directory** (`fs.read`) and a **git
  branch** (`git.read`). A read-only database URL is **deferred** ([Resource
  kinds](#resource-kinds)).

## Token format (OD-P2-1)

The plan names Biscuit (`biscuit-go`). Evaluation:

| Criterion | Biscuit (`github.com/biscuit-auth/biscuit-go/v2`) | In-house signed caveats (this spec) |
|---|---|---|
| cgo | Pure Go: fine | Pure Go stdlib (`crypto/ed25519`): fine |
| Dependencies | Adds protobuf (`google.golang.org/protobuf`) and a Datalog engine; a small maintainer set; to be re-checked for activity at decision time | None new |
| Attack surface in the verifier | Datalog evaluation of attacker-supplied blocks; parser for protobuf | One strict JSON parse, one signature check, a fixed list of field comparisons |
| Attenuation by the holder | Yes (append-only blocks) | No. Not needed in Phase 2: every grant is audience-bound to one holder, there are no third parties, and the grantor enforces |
| Offline verification | Yes | Yes |
| Independent vector check | Official sample vectors exist, but checking them independently needs the Rust or another Biscuit implementation | Reproducible with any Ed25519 + SHA-256 implementation; `tools/verifyvectors` extends naturally |
| Revocation | Revocation ids, but distribution is still ours | Enforcer-side lookup (same) |

**Recommendation: the in-house token below**, with a format version `v` so that Biscuit (or
holder attenuation) can be added later if delegation is ever needed. The widened-caveat test
of the plan holds because any change to the grant invalidates the signature.

### Grant object

Canonical JSON ([agent-card.md](agent-card.md#canonical-serialisation)), parsed strictly:

| Member | Type | Rules |
|---|---|---|
| `v` | integer | `1` |
| `id` | string | `g-` + 32 lowercase hex (16 bytes `crypto/rand`) |
| `iss` | `<key>` | Grantor identity key |
| `aud` | `<key>` | Holder identity key. `aud ≠ iss` |
| `session` | string | `s-…`, the [work session](work-session.md#session-id) between `iss` (requester) and `aud` (worker) |
| `action` | string | `fs.read` or `git.read` |
| `resource` | object | `{"kind": "fs", "label"}` or `{"kind": "git", "label", "branch"}`. `kind` must match `action`'s prefix |
| `scope` | string | Optional. A relative path prefix inside the resource ([Paths](#paths)); absent = the whole resource |
| `nbf` | time | Issue time |
| `exp` | time | `nbf + 1 min ≤ exp ≤ nbf + 7 d` |
| `sensitive` | boolean | [Sensitive grants](#sensitive-grants-24) |

`label` is 1–64 characters `[a-z0-9._-]`, chosen by the grantor's daemon (the basename of
the local path, lowercased and cleaned, plus `-` and 4 hex characters). **The local path is
never sent**: the token names the resource only by label, and the grantor maps label → path
locally. `branch` follows the request artifact `branch` rules and must be a valid
`refs/heads/<branch>` name.

### Token

```json
{"grant": { ... }, "sig": "<base64url, no padding, 64 bytes>"}

sig = Ed25519-Sign(iss_private_key, "dorylinae-grant-v1\n" ‖ canonical(grant))
```

The wire form is `canonical(token)`. It is at most 2048 bytes.

### Verification

`Verify(token, role, now)`. The first failure rejects with the reason shown.

| # | Check | Reason |
|---|---|---|
| 1 | Strict parse: exactly `grant` and `sig`; `grant` has exactly the members above with their types; optional `scope` absent rather than empty | `malformed` |
| 2 | `v = 1`; `id`, keys, `session`, `label`, `branch`, `scope` formats | `malformed` |
| 3 | `iss` = the expected grantor: holder: `msg.from` of the carrying `grant` mail, or the stored grant's `iss`; grantor: its own key | `wrong_issuer` |
| 4 | `sig` verifies under `iss` over the domain-prefixed canonical `grant`, **as parsed generically** (as for cards) | `bad_signature` |
| 5 | `aud`: holder: its own key; grantor: the identity the Noise session authenticated for this fetch | `wrong_audience` |
| 6 | `nbf ≤ now + 10 min` and `now < exp` | `not_yet_valid` / `expired` |
| 7 | `session` names a known work session between `iss` (requester) and `aud` (worker), in state `open` | `unknown_session` / `session_not_open` |
| 8 | `action` matches `resource.kind` | `malformed` |
| 9 | **Grantor only:** a `grants` row with this `id` exists, `canonical(token)` equals the stored token byte for byte, and it is not revoked | `unknown_grant` / `revoked` |
| 10 | **Grantor only, per call:** the operation is allowed by `action`, and the requested path is inside `scope` ([Paths](#paths)) | `out_of_scope` |

The holder runs 1–8 when the `grant` mail arrives (a failure is `mail.ErrBadBody`) and again
before every fetch, so an expired or ended grant fails locally without a round trip. The
grantor runs 1–10 on **every** fetch message.

**Widened caveat (plan 2.2 acceptance):** the holder changes any member (for example `exp`
one day later, `scope` to a parent directory, `action`, `aud`) and re-serialises: step 4
fails at the holder's offline check and at the grantor; and even a token with a valid
signature but different bytes from the stored row fails step 9.

## Issuance (2.2)

`agentnet grant @peer --session <id> --action fs.read|git.read --resource <path>[#<branch>]
[--scope <relpath>] --expires <duration> [--public]`, IPC `grant_create`.

The grantor's daemon, in order:

1. The session exists with `role = requester`, `peer` = the resolved peer, state `open`;
   otherwise `unknown_session`, `not_requester` or `bad_state`.
2. D5: refused to a `trust = relay` peer on a non-loopback relay (`unverified_peer`).
3. Resource: `<path>` must be an **absolute, existing** directory. For `git.read` it must be
   the top of a git work tree or a bare repository (`git rev-parse --show-toplevel` /
   `--is-bare-repository`), and `#<branch>` must name an existing `refs/heads/` branch. The
   path is resolved with `filepath.EvalSymlinks` once, at issuance, and the resolved path is
   stored. Refused: the config dir (`DORYLINAE_HOME` or default) or any path inside it or
   containing it, the user's home directory itself, and a filesystem root
   (`forbidden_resource`).
4. `--expires`: a duration, 1 min to 7 d, and additionally capped at 7 d; default 2 h.
5. `sensitive` = true unless `--public` is given **and** the action is `git.read`. `fs.read`
   is always sensitive ([Sensitive grants](#sensitive-grants-24)).
6. Build and sign the token. Store the row as `pending_approval`.
7. **Approval.** If a [policy](#policies) matches, approve at once (audit `grant.auto
   {grant, policy}`). Otherwise create a [human approval](approval.md) (kind `grant`) and
   return it. On approval, in one transaction: re-check steps 1–3 against the current state
   ([approval.md §Flow](approval.md#flow) step 3; a session that left `open` or a removed
   peer drops the grant), set the row `active` and `Outbox.SubmitTx` the `grant` mail to
   the holder.

A grant is created by the **requester** of the session only (OD-P2-5). It never widens: there
is no command that edits a grant; a different scope needs a new grant.

### Policies

A policy lets grants issue without a prompt. It matches on **all** of: peer key, action,
resource path (exact resolved path), for `git.read` the exact branch, `--scope` prefix (the
grant's scope must be inside the policy's, by segments), the grant's `sensitive` value
(`--public` in the policy covers only `--public` grants), and a maximum expiry.
`sensitive` grants may be covered by a policy (the result quarantine still applies). Adding
a policy needs a human approval (kind `grant_policy`); removing one does not.

`agentnet grant policy add @peer --action A --resource PATH[#BRANCH] [--scope P] [--public]
--max-expires D [--until DURATION]`, `grant policy list`, `grant policy remove <p-id>`; IPC
`grant_policy_add`, `grant_policy_list`, `grant_policy_remove`. At most 50 policies. Every
policy has its own end, `until` (default 30 d, at most 90 d); an ended policy matches nothing
and is pruned. Policies also end with the peer (`peers remove` deletes them).

### Kinds

Sealed [mail](mail.md), outboxed, acked, `Inbox: true`, strict.

| Kind | Direction | Body |
|---|---|---|
| `grant` | grantor → holder | `{"token": <token object>}` |
| `grant.revoke` | grantor → holder | `{"at", "grant": "g-…", "reason": "user"\|"session_closed"\|"peer_removed"}` |

Holder apply of `grant`: [Verification](#verification) steps 1–8 (step 7 with the holder's
mirror state; a session the holder does not know yet is an orphan: acked, ignored, audited
`grant.orphan`), then insert the holder row. Duplicate `id` with identical token: nothing.
Different token under a known id: keep the first, audit `grant.conflict`.

Holder apply of `grant.revoke`: find the `held` row with this id **and `peer = msg.from`**
and mark it `revoked`. Unknown id, or a row held from another grantor: ignore (so one peer
cannot revoke grants another peer gave).

## Enforcement and fetch (2.3)

### Transport

A fetch is interactive: it needs the grantor online, like `ping`. It travels as
`session.data` plaintext types on the Noise session ([session.md](session.md#plaintext-of-sessiondata)),
not as mail. The Noise session authenticates both identities (static-key binding), and that
identity is the `aud` check (step 5). A fetch to an offline grantor fails with `timeout`.

`session.data` plaintexts are limited by Noise to 65535 bytes, so file content travels in
**fragments** of at most **32768 raw bytes** (base64 in JSON, under 45 KiB per message).

```json
{"type":"fetch.req","req":"f-<32 hex>","ts":"<time>","token":{...},"op":"read","path":"internal/mail/mail.go","offset":0,"length":1048576}
{"type":"fetch.resp","req":"f-…","ok":true,"frag":0,"frags":2,"size":40000,"commit":"<40 hex>"?,"data":"<base64>"}
{"type":"fetch.resp","req":"f-…","ok":false,"error":"revoked"}
```

| `op` | Request members | Response |
|---|---|---|
| `stat` | `path` | `{"entry": <entry>}` |
| `list` | `path` (a directory; `""` = the root of the scope), `cursor`? | `{"entries": [<entry>], "cursor"?}`, at most 1000 per response, sorted by name (byte order) |
| `read` | `path`, `offset` ≥ 0, `length` 1–262144 | 1–8 fragments with `data`, `frag`, `frags`, `size` (the file size). The client reassembles and retries the whole read if a fragment is missing after 10 s |

A read is at most **256 KiB** (8 fragments) for two reasons: the result travels back over IPC,
whose lines are limited to 1 MiB ([ipc.md](ipc.md#framing); a 1 MiB read is about 1.4 MiB in
base64), and a burst of fragments larger than the relay's per-connection buffer (64 frames)
spills into the recipient's persistent relay queue, where it competes with the holder's
mail (`queue_full`). With 8 fragments per read and 2 reads in flight per holder
([Limits](#limits)), at most 16 fragments are in flight towards one holder.

`entry` = `{"name", "type": "file"|"dir"|"symlink"|"other", "size"?}`. `git.read` responses
carry `commit`: the branch tip the operation was served from (the tip is resolved per call;
a caller that needs one snapshot compares `commit` across calls).

Each `fetch.req` carries the **full token** and is verified from scratch (steps 1–10); the
grantor keeps no per-holder cache that could outlive a revocation. `ts` must be within
`now − 30 s … now + 10 min` and `req` must not have been seen in the last 11 min (the whole
`ts` window; a delayed or relay-queued fetch is refused: `stale`). Unknown `op` →
`malformed`.

The grantor serves fetches on its own bounded worker pool, never on the session manager's
receive goroutine or under its lock (`internal/session`), so a slow `git` call (up to 10 s)
cannot stall pings or other peers' sessions.

### Revocation (< 1 s)

`agentnet revoke <g-id>` (IPC `grant_revoke`, grantor only), in one transaction: set
`revoked`, `revoked_at`, and `Outbox.SubmitTx` a `grant.revoke`. Because the grantor is the
enforcer and checks its own row on every call (step 9), **the next fetch after the commit
fails with `revoked`**, and so does every later fragment of a read in progress (the server
checks the row before sending each fragment; fragments already handed to the relay before
the commit, at most 8, still arrive). The `grant.revoke` mail only informs the holder;
enforcement does not depend on it. The plan's "within one second" is met with no clock or
network dependency; the acceptance test asserts it with a fake relay delay of 0 and a real
one.

### Session end

When the work session closes (either outcome), all its grants end in the same transaction
(`revoked`, `reason = session_closed`), including rows still `pending_approval` (their
approvals are rejected with `reason: "precondition"`). The holder learns this from `ws.state closed` and
marks its rows ended; a separate `grant.revoke` is not sent. Grants are usable only while the
session is `open` (step 7): during `awaiting_result` and `quarantined` they fail
`session_not_open`, and work again if changes are requested and the session re-opens, until
`exp`. After `closed` they are revoked for good.

`peers remove` of the holder revokes all its grants (`peer_removed`).

## Resource kinds

| Kind | Action | Served | Not served |
|---|---|---|---|
| `fs` | `fs.read` | Regular files and directory listings under the resolved directory | Anything under a `.git` directory; symlinks (listed as `symlink`, never followed); devices, FIFOs, sockets (`other`, never opened) |
| `git` | `git.read` | Tree listings and blobs of `refs/heads/<branch>` at its tip | Other refs, history, the index, the work tree, submodules (mode 160000 → `other`), symlink blobs (mode 120000 → `symlink`, content not served) |
| database URL | — | **Deferred** (OD-P2-4). Serving a database needs a query proxy (a large new surface), while handing a URL over hands over a credential that cannot be revoked. Neither is needed for the Phase 2 acceptance tests | |

### Paths

A request `path` (and a grant `scope`) is a **relative, slash-separated** path:

- 0–1024 bytes of UTF-8 (`""` only for `list` = the scope root); segments separated by `/`;
- no empty segment, no `.` or `..` segment, no leading or trailing `/`, no `\`, no `:`, no
  NUL or control character, no segment that is a Windows reserved name (`CON`, `PRN`, `AUX`,
  `NUL`, `COM1`–`COM9`, `LPT1`–`LPT9`, with or without extension), no segment ending in `.`
  or space;
- the effective path is `scope + "/" + path`; `out_of_scope` is decided on the segment list
  (prefix of segments), never by string prefix.

Invalid → `bad_path`.

### Serving `fs`

- The grantor opens the resolved directory with **`os.OpenRoot`** (Go ≥ 1.24) and resolves
  every path inside that root, so `..` and symlinks cannot escape it even under a race.
- Before opening, each path component is checked with `Root.Lstat`: a symlink anywhere in
  the path → `symlink`; an intermediate component that is not a plain directory (any
  `ModeType` bit other than `ModeDir`, including `ModeIrregular`, which Go ≥ 1.23 reports
  for Windows junctions and other reparse points) → `symlink`; a component named `.git`
  compared **case-insensitively** (`.GIT` reaches the same directory on NTFS, APFS and HFS+)
  → `out_of_scope`. On Windows a segment shaped like an 8.3 short name (`~` followed by a
  digit, for example `GIT~1`) → `bad_path`, because it can name `.git` or any other entry by
  its alias. `list` never returns entries named `.git` (any case).
- A file is opened through the root, then checked with `Stat` on the **open handle**: not a
  regular file → `not_regular`; larger than **8 MiB** → `too_large`.
- Residual risk, documented in the CLI help: a **hard link** inside the directory to a file
  outside it is readable, and the grantor's own files are served with the grantor's rights.
  The grantor chooses the directory.

### Serving `git`

The daemon runs the `git` executable (no Go git library; no cgo) with a fixed argument
vector, **no shell**, a 10 s timeout, and the daemon's environment with **every `GIT_*`
variable removed** (an inherited `GIT_DIR`, `GIT_WORK_TREE`, `GIT_OBJECT_DIRECTORY`,
`GIT_CONFIG_PARAMETERS` or `GIT_CONFIG_COUNT`/`KEY_n`/`VALUE_n` would redirect the repository
or inject configuration) and then these added: `GIT_CONFIG_NOSYSTEM=1`,
`GIT_CONFIG_GLOBAL=<os.DevNull>`, `GIT_TERMINAL_PROMPT=0`, `GIT_NO_LAZY_FETCH=1`,
`GIT_OPTIONAL_LOCKS=0`, `GIT_LITERAL_PATHSPECS=1` (no `*`, `?`, `[` or `:(magic)` in a
path), `GIT_NO_REPLACE_OBJECTS=1`, `GIT_ATTR_NOSYSTEM=1`. The `git` executable is resolved
once at daemon start with `exec.LookPath`. Commands:

```
git -C <repo> rev-parse --verify --end-of-options refs/heads/<branch>^{commit}
git -C <repo> ls-tree -z --full-tree <commit> -- <dir>/        (list)
git -C <repo> ls-tree -z --full-tree -l <commit> -- <path>     (stat)
git -C <repo> cat-file blob <blob-oid>                          (read, after a stat gave a 100644/100755 blob)
```

Peer-supplied strings reach `git` only as the `<path>` after `--`, already validated by
[Paths](#paths); the branch comes from the token, validated at issuance. None of these
commands runs hooks, filters or diff drivers. Blobs larger than 8 MiB → `too_large`.

### Limits

Per grant, on the grantor: at most 2 fetch operations in flight, 20 per second, and
256 MiB served per 24 h (`rate_limited`). Per holder peer: at most 2 in flight across grants
(so at most 16 fragments travel towards one holder, [Transport](#transport)).
Per daemon: at most 32 in flight. These keep a holder from turning the grantor into a
bandwidth or CPU sink.

## Sensitive grants (2.4)

`sensitive: true` marks a grant whose resource is private: every `fs.read` grant, and every
`git.read` grant not issued with `--public`. The daemon cannot tell a private repository from
a public one, so the default is sensitive and the human opts out. A session in which any
sensitive grant was ever issued quarantines its results
([work-session.md §Quarantine](work-session.md#quarantine-24)); `agentnet release` needs a
human approval and is audited.

## IPC

| Method | Params | Result |
|---|---|---|
| `grant_create` | `{"peer", "session", "action", "resource", "branch"?, "scope"?, "expires"?, "public"?: bool}` | `{"grant": <grant view>}` if a policy approved it, else `{"grant": <view, state pending_approval>, "approval": <approval view>}` |
| `grant_list` | `{"session"?, "direction"?: "issued"\|"held", "state"?}` | `{"grants": [<grant view>]}` |
| `grant_show` | `{"id"}` | `{"grant": <grant view>, "token"?: <token>}` (`token` only on the holder side, for debugging) |
| `grant_revoke` | `{"id"}` | `{"grant": <view>, "mail_id"}`. Grantor only (`not_grantor`). Idempotent: already revoked → `duplicate: true` |
| `grant_policy_add` / `_list` / `_remove` | see [Policies](#policies) | |
| `fetch_start` | `{"grant", "op", "path"?, "offset"?, "length"?, "cursor"?}` | Holder only. Verifies locally (steps 1–8), starts the fetch and waits at most 1 s: `{"fetch_id", "state": "pending"\|"complete"\|"failed", "result"?, "error"?}` |
| `fetch_status` | `{"fetch_id"}` | Same shape. `result` of a completed `read`: `{"data": "<base64>", "offset", "size", "commit"?}` (`commit` for `git.read` only); of `stat`/`list`: the response members. Completed fetches are kept 60 s in memory |

Grant view: `{"id", "direction": "issued"|"held", "peer": <peer ref>, "session", "action",
"resource": {"kind", "label", "branch"?, "path"?}, "scope"?, "nbf", "exp", "sensitive",
"state": "pending_approval"|"active"|"expired"|"revoked", "revoked_at"?, "reason"?}`.
`resource.path` (the local path) appears only on the grantor side.

New error codes: `unknown_grant`, `not_grantor`, `forbidden_resource`, `bad_path`,
`out_of_scope`, `revoked`, `expired`, `session_not_open`, `symlink`, `not_regular`,
`too_large`, `rate_limited`, `stale`, `timeout`, `not_found` (exit 1); plus the approval
codes.

## CLI

Pages `Docs/cli/grant.md` and `Docs/cli/fetch.md` are written by tickets 2.2c and 2.3b.

| Command | Notes |
|---|---|
| `agentnet grant @peer --session S --action A --resource PATH[#BRANCH] [--scope P] [--expires D] [--public] [--json]` | Prompts for the approval code on a terminal; otherwise prints the approval id |
| `agentnet grants [--session S] [--issued\|--held] [--json]` | |
| `agentnet revoke <g-id> [--json]` | |
| `agentnet grant policy add\|list\|remove …` | |
| `agentnet fetch <g-id> <path> [--out FILE] [--json]` | Reads a file (all of it, in 256 KiB reads). Without `--out`, raw bytes to stdout. `--json`: `{"ok", "path", "size", "commit"?, "data": "<base64>"}` |
| `agentnet fetch <g-id> --list [<dir>] [--json]` / `--stat <path>` | |

`fetch` is a waiting command like `wait`: each IPC call returns within 2 s, and the command
gives up after `--timeout` (default 30 s, exit 4).

## Audit

Never file contents, paths inside a resource, scope values, labels, branch names or local
resource paths: these can be content (a path can name a customer). Only ids, enums and
sizes.

| Action | Side / actor | Detail |
|---|---|---|
| `grant.create` | grantor / `cli` | `{grant, session, peer, action, sensitive, expires_s, approval?}` |
| `grant.auto` | grantor / `daemon` | `{grant, policy}` |
| `grant.issue` | grantor / `daemon` | `{grant, peer, mail}` (on approval) |
| `grant.in` | holder / `daemon` | `{grant, session, peer, action, sensitive}` |
| `grant.revoke` | grantor / `cli` or `daemon` | `{grant, peer, reason}` |
| `grant.revoked_in` | holder / `daemon` | `{grant, peer}` |
| `grant.fetch` | grantor / `daemon` | `{grant, peer, op, bytes, result}` where `result` is `ok` or the error code. **Rate-limited** to 60 rows per grant per minute; the rest are counted and summarised once a minute as `grant.fetch_summary {grant, ops, bytes, errors}` |
| `grant.policy_add`, `grant.policy_remove` | grantor / `cli` | `{policy, peer, action}` |
| `grant.orphan`, `grant.conflict` | holder / `daemon` | `{grant, peer}` |

## Tables

```sql
-- migration 16 (2.2c): grants
CREATE TABLE grants (
    id          TEXT PRIMARY KEY,                -- g-<32 hex>
    direction   TEXT NOT NULL CHECK (direction IN ('issued', 'held')),
    peer        TEXT NOT NULL,                   -- holder (issued) / grantor (held)
    session     TEXT NOT NULL,
    action      TEXT NOT NULL CHECK (action IN ('fs.read', 'git.read')),
    label       TEXT NOT NULL,
    path        TEXT,                            -- issued only: resolved local path
    branch      TEXT,
    scope       TEXT,
    sensitive   INTEGER NOT NULL,
    nbf         TEXT NOT NULL,
    exp         TEXT NOT NULL,
    token       TEXT NOT NULL,                   -- canonical token
    state       TEXT NOT NULL CHECK (state IN ('pending_approval', 'active', 'revoked')),
    approval    TEXT,
    policy      TEXT,
    revoked_at  TEXT,
    reason      TEXT CHECK (reason IN ('user', 'session_closed', 'peer_removed')),
    created     TEXT NOT NULL,
    updated     TEXT NOT NULL
);
CREATE INDEX grants_session ON grants (session);
```

```sql
-- in migration 15 (2.2a)
CREATE TABLE grant_policies (
    id          TEXT PRIMARY KEY,                -- p-<32 hex>
    peer        TEXT NOT NULL,
    action      TEXT NOT NULL,
    path        TEXT NOT NULL,                   -- resolved local path
    branch      TEXT,
    scope       TEXT,
    public      INTEGER NOT NULL DEFAULT 0,      -- 1: covers only --public git.read grants
    max_expires_s INTEGER NOT NULL,
    until       TEXT NOT NULL,                   -- the policy's own end (≤ created + 90 d)
    approval    TEXT NOT NULL,
    created     TEXT NOT NULL
);
```

`expired` is derived from `exp` at read time, not stored. Every table goes into the DROP
lists of both rewind tests in `internal/store/store_test.go`.

## Threat model

Assets: the grantor's files and repositories; the integrity of what the holder reads; the
requester's agent (against poisoned results).

| Threat | Control |
|---|---|
| Relay reads or changes fetched data | Noise session: confidentiality, integrity, replay protection |
| Holder widens its grant (scope, expiry, action, resource) | Signature over the whole grant; stored-token byte comparison; grantor recomputes scope on every call |
| Holder passes the token to a third party | `aud` bound; the grantor checks `aud` against the Noise-authenticated identity |
| Stolen token (log, clipboard) | Useless without the holder's identity key (step 5) |
| Revoked or expired grant still used | Checked by the enforcer on every message; no caches |
| Path traversal, symlink escape, TOCTOU swap | Strict path grammar; `os.Root`; `Lstat` of each component; checks on the open handle |
| Serving `.git` internals, secrets in the config dir, the home dir or `/` | `.git` excluded (any case, no 8.3 aliases, no junctions); forbidden resources at issuance |
| Git config/hooks executing code, or an inherited environment redirecting git | Only plumbing commands with no hooks; system/global config disabled; every inherited `GIT_*` removed; literal pathspecs; no lazy fetch |
| Fetch traffic crowding out mail at the relay | 256 KiB reads, 2 in flight per holder: at most 16 fragments towards one holder |
| Resource exhaustion | Per-grant, per-peer and per-daemon limits; 8 MiB files; 256 KiB reads; 1000-entry lists |
| Prompt-injected grantor agent issues a grant | Human approval with an out-of-band code ([approval.md](approval.md)), or an approved policy |
| Prompt-injected holder agent exfiltrates data it read | **Not preventable** by AgentNet: the holder had read access. Mitigations: scope and expiry, sensitive-by-default, requester-side quarantine of results, audit on the grantor |
| Local malware as the grantor's user | Out of scope |
| A relay-queued old fetch replayed later | `ts` window and `req` dedupe; Noise counters |

Non-goals (Phase 2): write access, execution on the grantor, databases, holder attenuation,
multi-holder grants, grants that outlive their session.

## Test vectors

Produced by `tools/specvectors` from ticket 2.2b on (recorded here from the same
computation). Keys as in [pairing.md §Test vectors](pairing.md#test-vectors): grantor
seed `00…1f`, holder seed `20…3f`. Ed25519 is deterministic, so the signature is reproducible
and `tools/verifyvectors` must recompute it independently.

```
iss      A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg
aud      Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc
session  s-36375782ceb6baea9cee4d4273dfb035   (work-session.md vector)
```

Canonical grant (one line, UTF-8):

```
{"action":"git.read","aud":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc","exp":"2026-01-02T05:00:00Z","id":"g-00112233445566778899aabbccddeeff","iss":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","nbf":"2026-01-02T03:00:00Z","resource":{"branch":"feat-x","kind":"git","label":"agentnet-3f2a"},"scope":"internal/mail","sensitive":true,"session":"s-36375782ceb6baea9cee4d4273dfb035","v":1}
```

```
SHA-256(canonical grant)  78da9339e8a1500ec1781d5d4be3b9b52149eac7e7f815a77b0b6dab0abe239d
sig                       l3c5wKLJPH0BGLjAXQk0Z1cjYlQ0aWb0ueo1ZnI4ooCBLXMqbMH8r6n1ox3Wem0q1jXl-MQEpGcGgewtCa_CCg
```

Negative checks (each must fail at the step shown, holder side, with `now =
2026-01-02T04:00:00Z` and the session known and `open`):

- `exp` changed to `2026-01-03T05:00:00Z` with the same `sig` (the **widened caveat**): step
  4 `bad_signature`.
- `scope` removed, same `sig`: step 4 `bad_signature`.
- Verified by a holder whose key is not `aud` (for example seed `40…5f`): step 5
  `wrong_audience`.
- `now = 2026-01-02T05:00:00Z`: step 6 `expired`.
- A member `"write":true` added: step 1 `malformed`.
- The same grant with `"sensitive":true` written as `"sensitive":1`: step 1 `malformed`.
