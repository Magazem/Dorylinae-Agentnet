# Own-device helper (D13)

Status: **draft** for Phase 2 (owner decision D13). Tickets 2.D1 and 2.D2 in
[../review/23-phase2-tickets.md](../review/23-phase2-tickets.md). Change this document first.

**D13 (final):** a separate `device` trust, never created by team, roster or pairing; a
dedicated link flow confirmed on BOTH devices; only the obeying device can make itself a
helper, and it holds the scope locally (request types, repos/paths, commands, expiry); off by
default, audited, revocable from either side; out-of-scope requests go to the normal inbox;
one-way hierarchy.

Example: Alice's laptop (the **controller**) asks her desktop (the **helper**) to run the test
suite of a repository. The desktop runs a command that Alice configured **on the desktop**,
and returns the exit code and the output tail as a [result](work-session.md#result-object-26).
No agent on the desktop is needed.

## Model

- A **device link** is a directed relation between two peers of the same person:
  `controller → helper`. It is stored in its own table, `device_links`, and only the device
  link handlers write it. That table **is** the `device` trust of D13. It is deliberately
  **not** a value of `peers.trust`: `peers.trust` ranks how well a *key* is authenticated
  (`relay < team < code < fingerprint`), while `device` is a *relationship* that grants a
  scope. Keeping it in a separate table means that no team, roster, pairing, `peers verify`
  or `Store.Add` code path can create or raise it, which the tests assert. (See OD-P2-8.)
- A link by itself does **nothing**. It becomes useful only when the **helper** sets a
  **scope** for it, locally. The controller never sends, sees or changes the scope.
- Everything is off by default: no link exists after install, pairing or team join, and a
  new link has no scope.

## Link flow

Both devices must already be paired peers (v2 pairing or team introduction; any trust
level). The link needs a human confirmation **on each device**, through the local
[approval](approval.md) code of that device, and each human types the **other** device's
fingerprint, which binds the link to the right keys even if the pairing was only `team`
trust.

```
helper (desktop)                                          controller (laptop)
agentnet device link @laptop --as helper \                agentnet device link @desktop --as controller \
    --fingerprint <fp(laptop)>                                --fingerprint <fp(desktop)>
  fingerprint compared (constant time)                      fingerprint compared
  approval (code on the desktop's screen)                   approval (code on the laptop's screen)
  → intent{role: helper, nonce_h}, expires 10 min           → intent{role: controller, nonce_c}, expires 10 min
  → mail device.link {role: helper, nonce_h} ──────────▶    ◀────────── mail device.link {role: controller, nonce_c}
  active when: own intent + peer's complementary offer, both unexpired
  link_id = l- ‖ hex(SHA-256("dorylinae-device-link-v1\n" ‖ controller ‖ "\n" ‖ helper ‖ "\n" ‖ nonce_c ‖ "\n" ‖ nonce_h)[0:16])
```

1. `device_link {peer, as, fingerprint}`: resolve the peer; compare the fingerprint with
   `fp(peer)` in constant time (`fingerprint_mismatch`, nothing changes). D5 applies
   (`unverified_peer`). [Hierarchy](#one-way-hierarchy) checks. Then create the approval
   (kind `device_link`); the result is `{"approval", "link": {"state": "pending_approval"}}`.
2. On approval, in one transaction: set `peers.trust = fingerprint` (a matched fingerprint
   is exactly `peers verify`; audit `peer.verify` as today), store the **intent** (role,
   random 16-byte `nonce`, `expires = now + 10 min`), and `Outbox.SubmitTx` a
   `device.link` mail.
3. On receiving `device.link` from that peer: if a matching local intent with the
   complementary role exists and is unexpired, and the offer's `at` is not older than the
   intent's `created − 10 min`, **activate**: compute `link_id` from both nonces, store the
   link `active`, delete the intent, audit `device.link_active`, notify. If no intent
   exists, keep the offer for 10 minutes (at most one per peer) so the other device can
   still confirm; after that it is dropped. An offer never creates a link on its own.
   **Freshness (D22, review 36 L4):** an offer counts, and is kept, only while
   `now < offer.at + 10 min`, so it is alive by its sender's own clock too: a relay that
   delays it cannot stretch the window past the sender's intent. An offer whose `at` is more
   than 10 min ahead of the receiver's clock does not count either (review 40 L7). An offer whose `at` is
   older than the `at` of the last `device.unlink` received from that peer is ignored, so
   a delayed offer reordered after an unlink cannot revive the link. The receiver keeps,
   per peer, the latest `device.unlink` `at` it applied (the `settings` row
   `device.unlinked`; no migration).
   **Notify (D22, review 36 L5):** on activation, on either path (the offer arriving, or
   the local approval finding a kept offer), the device shows the content-free desktop
   notification `device.linked` ([notify.md](notify.md#triggers)): `<peer name> is now
   linked as your helper|controller`.
4. Each side activates independently when it holds both halves, so no third message is
   needed. Both compute the same `link_id`.

A peer that sends offers without a local intent gains nothing: an offer alone is inert, and
at most one is kept per peer.

## Scope (held by the helper only)

`agentnet device scope @controller …` on the **helper**, IPC `device_scope_set`, needs a
[human approval](approval.md) (kind `device_scope`). The CLI prints, and the approval
summary states, each command's name, repo path and **resolved** absolute `argv[0]` with its
full argv, so the human approves exactly what will run. The scope replaces any previous one
atomically. `agentnet device scope @controller --clear` removes it (no approval needed:
narrowing is always allowed); queued runs are then dropped and a running one is killed, like
on unlink ([§Limits](#limits)), and a scope still waiting for its code is rejected. A newer `device_scope_set` rejects an older one still
waiting for its code. The summary (and the CLI's printout) quotes every repo path and argv as
JSON strings, and also escapes as `\uXXXX` every character that is invisible or not graphic
(bidi controls such as U+202E, zero-width characters), so no quote, control or bidi character
can change how it reads (review 40 M1); a scope whose summary would exceed
16384 bytes is refused (`bad_scope`, field `scope`), because the approval window must show
all of it. The scope is keyed on the controller's **key**, never on the link id: the two
devices can hold different link ids after an asymmetric retry (review 36 L6).

```json
{
  "types": ["task"],
  "repos": [{"label": "agentnet", "path": "C:\\Users\\alice\\src\\agentnet"}],
  "commands": [
    {"name": "test", "repo": "agentnet", "argv": ["go", "test", "./...", "-count=1"],
     "timeout_s": 900, "env": ["GOFLAGS"]}
  ],
  "expires": "2026-10-08T09:00:00Z"
}
```

| Member | Rules |
|---|---|
| `types` | 1–3 of `review`, `task`, `question` |
| `repos` | 1–16. `label` 1–64 `[a-z0-9._-]`, unique. `path` absolute, an existing directory, resolved with `EvalSymlinks` at set time; not the config dir (nor inside it or containing it), the home dir or a filesystem root: the same rule as a grant's `fs` resource ([grant.md](grant.md)). A refused path is `forbidden_resource`, any other rule `bad_scope` |
| `commands` | 1–32. `name` 1–64 `[a-z0-9._-]`, unique. `repo` one of the labels (the working directory). `argv` 1–64 strings, each 1–4096 bytes, no NUL; `argv[0]` is resolved with `exec.LookPath` **at set time** and the absolute path is stored, so a later `PATH` change cannot swap the program. `argv[0]` is a program name or an absolute path (a relative path with a directory part is refused). On Windows a `.bat` or `.cmd` file is refused: `CreateProcess` would run it through `cmd.exe`, and the runner never uses a shell; the program must be an `.exe` or `.com`, judged after dropping trailing dots and spaces as Windows does (review 40 L3). `argv[0]` is refused (`bad_scope`, field `commands[i].argv[0]`, reason starting `writable_by_others:` and naming the path and who) when **users other than this device's user or an administrator can change it** ([§Program ownership](#program-ownership), review 40 L11). `timeout_s` 1–3600. `env` 0–32 extra environment variable **names** (`[A-Za-z_][A-Za-z0-9_]*`, at most 128 characters, unique, never `DORYLINAE_*`) passed through (values are the helper daemon's own) |
| `expires` | `now < expires ≤ now + 30 d`. Required: every scope expires |

The scope is stored only on the helper and is never sent anywhere. The controller learns only
what a result tells it.

### Program ownership

The stored `argv[0]` protects against a `PATH` change, not against the file being replaced.
So a program is accepted only when nobody but this device's user or an administrator can
change it, checked at `device_scope_set` **and again when each run starts** (a failed
start-time check is `"<name>: could not start"`). Checked: the file, the directory holding
it and every directory above it along the stored path; every symbolic link (Windows: also
junction) met on the way is followed, and its target and every directory above that are
checked the same way, hop by hop, so a link that sits in a directory others can write is
refused even when that directory is on neither the stored nor the fully resolved path
(review 41 M1; at most 40 links). The content is not pinned (no hash), so an upgrade of the
toolchain by the same user or an administrator keeps working.

- **Unix:** each file or directory must be owned by root or this user, must not be
  writable by every user (so a sticky world-writable directory such as `/tmp` is refused
  too), and may be group-writable only when the group is root's (gid 0), an admin group
  (`root`, `wheel`, `admin`, `sudo`) or this user's private group (this user's primary
  gid, named after the user). A symbolic link's own mode is not used. On Linux the POSIX
  access list (`system.posix_acl_access`) is read too: a named user other than root or this
  user, or a named group not trusted as above, with write permission (not removed by the
  mask) is refused (review 41 M2). macOS and BSD access lists are **not** read, and the
  private-group rule trusts every member of that group.
- **Windows:** the owner (from the security descriptor) must be this user, SYSTEM,
  BUILTIN\Administrators or NT SERVICE\TrustedInstaller, the DACL must exist, and no
  allow ACE may give another SID (Users, Authenticated Users, Everyone, another account…)
  a right that can swap the program: on the file, write/append data, delete, write DAC,
  write owner, generic write or generic all; on the directory holding it, also add file
  (a DLL beside the `.exe` is loaded first) or add subdirectory (a `.local` redirect) and
  delete child; on the directories above, delete child, delete, write DAC, write owner or
  generic all (creating new entries there, like Authenticated Users on `C:\`, cannot
  replace an existing one). Inherit-only ACEs do not apply to the object; deny ACEs are
  ignored (a stricter reading); CREATOR OWNER and OWNER RIGHTS stand for the owner; app
  package and capability SIDs (`S-1-15-…`) never grant access on their own and are
  ignored; an object or compound ACE that grants such a right is refused. A symbolic link
  or junction on the way is judged by its own access list (the link is not followed for
  it), where writing its data or attributes (which could point it elsewhere) also counts.
  A path through a junction is refused today anyway (Go's `EvalSymlinks` does not follow
  mount points). On a network path the owner and ACEs are the file server's: its
  administrators count as administrators.

## Running (in-scope requests)

The request object gains one optional member, `run` ([request.md](request.md#request-object),
Phase 2):

| Member | Rules |
|---|---|
| `run` | `{"command": "<name>"}`, `name` 1–64 `[a-z0-9._-]`. It names a command the helper may have configured. It is a **name only**: no arguments, paths or environment ever come from the request |

On the helper, after the normal receive steps of a request ([request.md §Receiving](request.md#receiving),
including D5 and team membership, which the controller still needs), a request is **in
scope** when **all** of these hold:

1. there is an `active` link with `msg.from` as controller and this device as helper;
2. the scope exists and `now < expires`;
3. `request.type` is in `types`;
4. the request has `run`, and `run.command` names a configured command;
5. `request.created ≥ link.activated_at`, compared at whole seconds because `created` is
   truncated to the second (a request queued before the link existed never runs);
6. the helper is not over its [limits](#limits).

**Out of scope** (any check fails): the request goes to the **normal inbox**, exactly as in
Phase 1, and nothing runs. The reason is recorded in the audit (`device.out_of_scope
{request, peer, check}`), not sent to the controller. The checks are made, and audited, only
for a request that has `run` or comes from this device's controller; any other request is
an ordinary request and gets no `device.*` audit row. The checks are applied in the order
above, and `check` names the first that failed.

**In scope**: in the receive transaction the request is stored and **auto-accepted**
(`request.accept`, `first_response = accept`, actor `daemon`), and its
[work session](work-session.md) opens. After commit the run is queued. The runner:

- starts `argv` with the stored absolute `argv[0]`, **no shell**, working directory = the
  repo path, stdin = the null device;
- environment: only `PATH`, `HOME`/`USERPROFILE`, `TMP`/`TEMP`/`TMPDIR`, `LANG`, `LC_ALL`,
  `SystemRoot`, `SystemDrive`, `windir`, `ComSpec`, `PATHEXT`, `LOCALAPPDATA`, `APPDATA`
  (Windows; without `LOCALAPPDATA` the example's `go test` fails with "GOCACHE is not
  defined") and the scope's `env` names, taken from the helper daemon's environment;
  everything else (tokens, `DORYLINAE_*`) is dropped;
- first re-checks what the scope resolved: the repo path must still resolve to itself (a
  directory replaced since by a symlink or junction is refused) and `argv[0]` must still be
  a regular file that others cannot change ([§Program ownership](#program-ownership));
  otherwise the run is `"<name>: could not start"` (review 40 L5, L11);
- kills the whole process tree at `timeout_s` (Windows: a job object; Unix: a process group);
  if the helper daemon dies mid-run, Windows ends the tree with the job; on Linux the program
  itself gets `SIGKILL` (parent-death signal) but what it started may outlive it, and on
  macOS nothing is killed (review 40 M2). A process that leaves its group (`setsid`) or is
  started by another service (Windows WMI or Task Scheduler) is outside the tree;
- keeps the **last** 32768 bytes of combined stdout/stderr, turns CRLF into LF, strips ANSI
  CSI sequences (`ESC [` and U+009B) and replaces other control characters (except `\n`,
  `\t`), invalid UTF-8 and U+FFFD itself with `?`, and cuts at a UTF-8 boundary. (Not U+FFFD:
  the [D14 result](request.md#result-payload-d14) rules refuse U+FFFD in every text field,
  as the mark of invalid UTF-8.) If JSON escaping makes the whole result larger than its
  65536-byte cap, the output is shortened further from the front;
- when the program ends on its own, kills what it left running in its tree too;
- submits `ws.result` with `status = pass` if the exit code is 0, `fail` otherwise
  (`partial` never), `exit_code`, `output` (absent when empty), `summary = "<name>: exit
  <code> in <duration>"` (a Go duration, e.g. `1.5s`), or `"<name>: timed out after <n> s"`
  (status `fail`, no `exit_code`), or `"<name>: could not start"` (status `fail`, no
  `exit_code`), and `verification = none`.
- The command runs as looked up in the scope **when it starts**: a scope replaced, cleared or
  expired while the run was queued is re-checked, and a run no longer allowed is dropped.
- While it runs, the checks 1–4 are re-applied whenever the link or scope changes (unlink on
  either side, `peers remove`, scope cleared or replaced), when the scope expires (as of the
  last check: a scope replaced with a later expiry moves it), and at least once a minute (a
  changed clock). A replaced scope whose command of that name now has another `argv`, `env`
  or repo path stops the run too (review 41 L2); a changed `timeout_s` alone does not. When
  they fail, the whole process tree is killed at
  once and the run is reported like a dropped queued run ([§Limits](#limits)): `ws.cancel`
  with no reason and no result (review 40 L1).

The controller's agent reads it with `agentnet wait <session>` and closes it with
`accept-result` (or requests changes, which re-opens the session; a re-run needs a new
request, because the runner acts only on arrival).

### Limits

One run at a time per helper; at most 8 queued (beyond that: out of scope, `check:
"queue"`); at most 60 runs per controller per 24 h (a 61st is also `check: "queue"`; the
count is of runs accepted in the last 24 h). The queue is kept in the `settings` row
`device.runs` (no migration), so a restart knows what was queued or running. A run still queued when the scope expires
or the link ends is dropped; the helper (the worker) then sends `ws.cancel` with no reason,
which the controller's daemon applies because the session is still `open`. A run that is
**executing** then is killed with its process tree and reported the same way (`ws.cancel`,
no result): the controller's session closes as `cancelled`, as for a queued run, and no
partial output is sent. A daemon restart
drops queued runs the same way; a run that was executing is reported as `fail` with summary
`"<name>: interrupted"`, unless it is no longer allowed then (link ended, scope cleared or
expired meanwhile): it is reported like a killed run, `ws.cancel` and no result (review 41
L1).

## One-way hierarchy

- A link is directed. If `X → Y` exists (X controls Y), a link `Y → X` is refused
  (`device_cycle`), in either device's `device_link`.
- Depth 1: a device that is a helper of anyone cannot become a controller, and a controller
  cannot become a helper (`device_cycle`). So no chains exist and no request is ever relayed
  to a third device.
- A helper may have at most one controller and a controller at most 8 helpers in Phase 2.

(OD-P2-9 records whether depth 1 is too strict.)

## Unlink and expiry

- `agentnet device unlink @peer` on **either** device, IPC `device_unlink` (no approval:
  removing authority is always allowed). In one transaction: set any link, intent or offer
  with that peer `revoked`/deleted, delete any scope for it, and `Outbox.SubmitTx`
  `device.unlink`. The mail is sent **even when this device holds no active link** with the
  peer: activation is independent on each side, so one side can be `active` while the
  other's intent expired before the peer's offer arrived, and D13 requires that either side
  can revoke. The helper stops accepting in-scope requests **immediately** on its own
  unlink; on a controller-side unlink it stops when the mail arrives.
- `device.unlink` on arrival: revoke every link, intent and offer **with `msg.from`**
  (the `link` member is informational; a peer can only ever end its own links), delete the
  scope. Idempotent: nothing to revoke changes nothing.
- A run already executing when the link is revoked is **killed at once** with its whole
  process tree and reported as cancelled (`ws.cancel`, no result); queued runs are dropped
  (owner decision D24, review 40 L1). Removing authority wins over finishing the work: the
  repository may be left half-written, as after a timeout.
- `peers remove` of the other device revokes the link locally (and stops a run the same way).
- Scope expiry needs no message: after `expires`, requests are out of scope, and a run still
  executing is killed and reported the same way (the helper re-checks at the expiry time).

## Kinds

Sealed mail, outboxed, acked, `Inbox: true`, strict. A `device.link` carries no signature
of its own: the mail signature is its only proof that the sender's human confirmed. So only
the `device_link` and `device_unlink` handlers send these kinds; `mail_submit` refuses any
`device.*` kind (`bad_request`), so a local agent cannot send the other half of a link
itself (review 36 M1). More generally `mail_submit` is an allowlist: it refuses every kind the
daemon owns ([ipc.md](ipc.md#mail_submit), review 36 L7).

A `device_link` or `device_scope` approval that is rejected or expires frees what it held at
once: a rejected link attempt stops counting toward the hierarchy limits without waiting 10
minutes (review 36 L8).

| Kind | Body |
|---|---|
| `device.link` | `{"at", "controller": <key>, "helper": <key>, "nonce": "<32 hex>", "role": "controller"\|"helper"}`. `role` names the sender's role; the sender's key must be the one in that role, and the recipient's key the other |
| `device.unlink` | `{"at", "link"?: "l-…"}` (`link` absent when the sender has no active link) |

Link-id vector (controller = seed `00…1f`, helper = seed `20…3f`,
`nonce_c = 00112233445566778899aabbccddeeff`, `nonce_h = ffeeddccbbaa99887766554433221100`,
nonces as the ASCII hex strings):

```
link_id  l-ee417e25d0625afad79d54cd3539dd64
```

## IPC and CLI

| Method | Params | Result |
|---|---|---|
| `device_link` | `{"peer", "as": "controller"\|"helper", "fingerprint"}` | `{"approval", "link"}` |
| `device_list` | none | `{"links": [<link view>]}` |
| `device_unlink` | `{"peer"}` | `{"link", "mail_id"}` |
| `device_scope_set` | `{"peer", "scope": {...}}` (helper only) | `{"approval", "scope"}` (`scope` as it will be stored: paths and `argv[0]` resolved) |
| `device_scope_clear` | `{"peer"}` | `{"link"}` |
| `device_scope_show` | `{"peer"}` (helper only) | `{"scope"}` (`bad_state` when none is set) |

Link view: `{"id", "peer": <peer ref>, "role": "controller"|"helper" (this device's role),
"state": "pending_approval"|"waiting"|"active"|"revoked", "activated_at"?, "scope"?:
{"expires", "types", "commands": [names]} (helper only)}`.

CLI: `agentnet device link @peer --as controller|helper --fingerprint FP`, `device list`,
`device unlink @peer`, `device scope @controller --types … --repo LABEL=PATH… --command
'NAME=REPO:ARGV-JSON'… [--timeout NAME=S]… [--env NAME=VAR]… --expires D` (or `--from-file
scope.json`), `device scope @controller --clear|--show`. A command's timeout defaults to 900 s.
The controller sends a run with `agentnet request <helper> task --run NAME …`. Page
`Docs/cli/device.md` in 2.D1/2.D2.

Error codes: `fingerprint_mismatch`, `device_cycle`, `not_helper`, `unknown_link`,
`bad_scope` (with the field), `forbidden_resource`, plus the approval codes.

## Audit

Never output, argv, paths or environment values; command **names** and repo **labels** are
local configuration and are not audited either (they can be content). Only ids, enums,
counts, durations and sizes.

| Action | Detail |
|---|---|
| `device.link_intent` | `{peer, role, approval}` |
| `device.link_active` | `{link, peer, role}` |
| `device.unlink` | `{link, peer, side: "local"\|"remote"}` |
| `device.scope_set` | `{link, types, commands: <count>, repos: <count>, expires_s, approval}` |
| `device.scope_clear` | `{link}` |
| `device.run` | `{link, request, session, duration_ms, output_bytes, timed_out, cancelled}` (`cancelled`: killed because it was no longer allowed; then `output_bytes` is 0) |
| `device.out_of_scope` | `{request, peer, check}` (`check`: `link`, `scope`, `expired`, `type`, `command`, `created`, `queue`) |

## Tables

```sql
-- migration 17 (2.D1): device_links, device_scopes, device_offers
CREATE TABLE device_links (
    id           TEXT PRIMARY KEY,               -- l-<32 hex>; intents use i-<32 hex> until active
    peer         TEXT NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('controller', 'helper')),   -- this device's role
    state        TEXT NOT NULL CHECK (state IN ('pending_approval', 'waiting', 'active', 'revoked')),
    nonce        TEXT NOT NULL,                  -- own nonce (32 hex)
    peer_nonce   TEXT,
    approval     TEXT,
    created      TEXT NOT NULL,
    expires      TEXT,                           -- intents only
    activated_at TEXT,
    revoked_at   TEXT,
    updated      TEXT NOT NULL
);
CREATE UNIQUE INDEX device_links_peer ON device_links (peer) WHERE state IN ('pending_approval', 'waiting', 'active');

CREATE TABLE device_scopes (                     -- helper only
    link    TEXT PRIMARY KEY,
    scope   TEXT NOT NULL CHECK (json_valid(scope)),   -- canonical, with resolved paths
    expires TEXT NOT NULL,
    approval TEXT NOT NULL,
    created TEXT NOT NULL
);

CREATE TABLE device_offers (                     -- received offers waiting for a local intent
    peer        TEXT PRIMARY KEY,
    body        TEXT NOT NULL CHECK (json_valid(body)),
    received_at TEXT NOT NULL                    -- dropped after 10 min
);
```

All three tables go into the DROP lists of both rewind tests in `internal/store/store_test.go`.

## Threat model

Assets: the helper machine (it runs commands), its repositories, and the helper's secrets.

| Threat | Control |
|---|---|
| A teammate, team owner or a roster makes itself a controller | Links are created only by `device_link` on both devices with a local human approval each; team, roster, pairing and `peers verify` code never write `device_links` (tested) |
| A compromised or prompt-injected **controller** agent | It can only name commands the helper's human configured, in configured repos, until `expires`. No argument, path or environment comes from the request. Rate and queue limits. The helper's human can unlink at once |
| A stolen controller *machine* | Same as above until someone unlinks from the helper (the helper side works without the controller). Scopes expire (≤ 30 d) |
| A relay or third party injects requests | Mail signature by the controller's identity key; the link is bound to that key and confirmed by fingerprint on both sides |
| Replayed or queued old request runs after linking | `request.created ≥ activated_at`; mail dedupe; 14-day receive limit; request idempotency |
| The repository's own code is hostile (tests run repo code) | Inherent: a scope names repos the human trusts, like running the tests by hand. Documented in the CLI help |
| Secrets leak through the output | Minimal environment; output limited to 32 KiB, tail only; the result goes only to the controller (the same person) and is never audited |
| Command swapped through `PATH` | `argv[0]` resolved to an absolute path at scope-set time |
| Cycles or relaying to a third device | One-way, depth 1 |
| Output drives the controller's terminal | Control characters and ANSI sequences removed (D14 rules) |
| Local malware on the helper | Out of scope |

Non-goals (Phase 2): arguments or parameters in `run`, file transfer to the helper, running
without a request, helpers of helpers, a controller-side view of the scope.
