# Human approval

Status: **draft** for Phase 2 (ticket 2.2a, [../review/23-phase2-tickets.md](../review/23-phase2-tickets.md)).
Used by grants ([grant.md](grant.md)), the sensitive-grant release
([work-session.md](work-session.md#quarantine-24)), `accept-result --human`, and the
own-device link and scope ([device.md](device.md)). Change this document first.

## Why

The plan requires a "human approval prompt" for grants, and D13 requires the device link to
be "confirmed on BOTH devices". Both are decisions an **agent must not be able to take on
its own**, even though the agent runs `agentnet` with the user's full rights. The daemon
cannot tell a human from an agent over IPC: the caller is the same OS user either way. So an
approval needs a second channel that a harness agent normally does not see.

Phase 2 uses the **desktop notification** as that channel: the daemon shows a one-time code
only there, and the approving command must quote it.

## Threat model

| Attacker | Goal | Control |
|---|---|---|
| A prompt-injected local agent (through a brief, a result, a file it read) | Issue a grant, release a quarantine, link a device or widen a helper scope without the human | It cannot see the code: the code is never in an IPC result, a CLI output, the audit log, the daemon log, a webhook or SQLite (only its hash). It must guess: 3 attempts per approval, 6 digits → ≤ 3 × 10⁻⁶ per approval, and approvals are rate-limited |
| A remote peer | Trigger approvals to annoy or to phish the human | Only local IPC callers create approvals; a peer's mail never does. The notification names the **local** action and the **peer's name and fingerprint** |
| Malware running as the user | Anything | **Out of scope.** It can read the screen, the keystore file fallback and the database. The approval is a gate against confused-deputy agents, not a sandbox |
| A human approving without reading | A too-broad grant | The notification states action, resource, peer and expiry in plain words; the CLI prints the same before asking |

Residual risks, stated plainly: an agent with a screenshot or accessibility tool, or one that
reads the OS notification history, can read the code. Harnesses used with AgentNet should not
be given those tools. On a machine without a desktop, see [Headless machines](#headless-machines).

## Object

```
approval = {id: "a-" + 32 hex, kind, subject, summary, code_hash, created, expires,
            attempts, state: pending|approved|rejected|expired, decided}
```

- `kind`: `grant`, `grant_policy`, `release`, `accept_result`, `device_link`,
  `device_scope`. The action waits in the owning table in a `pending_approval` state that
  references the approval id.
- `code`: 6 decimal digits from `crypto/rand` (uniform, leading zeros kept). Stored only as
  `code_hash = lowercase hex SHA-256("dorylinae-approval-v1\n" ‖ id ‖ "\n" ‖ code)`. The
  plaintext code lives only in the notification call and is then dropped from memory.
- `expires` = `created + 10 min`. At most **3** attempts; the 3rd wrong code sets `rejected`.
- At most **5 pending approvals** per daemon, and at most **20 created per hour**. Beyond
  that `approval_limit`.

Vector: `id = a-0123456789abcdef0123456789abcdef`, `code = 482913` →
`code_hash = d0260c130e529fe85d5fd66f610f27d19478cc67515329cf3a21670425db4bda`.

Comparison of the hash is constant time.

## Flow

1. An IPC method that needs approval (for example `grant_create`) validates everything
   first, stores the action as `pending_approval`, creates the approval and shows the
   desktop notification:
   `AgentNet: approve grant git.read on agentnet (branch feat-x) to bob (2ED9 TGVE…) for 2h? Code 482913`.
   Peer-supplied text in it goes through `notify.Clean` ([notify.md](notify.md#text-and-sanitising)).
2. The method returns at once: `{"approval": {"id", "kind", "summary", "expires",
   "state": "pending"}}`. The CLI, when its stdin is a terminal, prints the summary and
   prompts `Code from the desktop notification:`; otherwise it prints the approval id and
   exits 0 with `state: pending`.
3. `agentnet approve <a-id> <code>` (IPC `approval_confirm {id, code}`) checks state,
   expiry and the code. On success, in one transaction, it sets `approved` and performs the
   waiting action; the result is the action's result. A wrong code → `bad_code`
   (attempts left in the message); expired → `approval_expired`.
4. `agentnet approve --reject <a-id>` (no code needed) rejects it; the waiting action is
   dropped.

`approval_list` returns pending approvals (never codes) so a human can see what is waiting.

Approvals are never created by peer mail, never forwarded, and never cross machines. The two
confirmations of a device link are two local approvals, one on each device
([device.md](device.md#link-flow)).

## Headless machines

If the desktop notifier is disabled or fails (`notify_test` → `failed`), approvals are
`approval_unavailable`, and the actions that need them fail unless a
[policy](grant.md#policies) covers them. A policy itself needs an approval, so a headless
machine needs a different channel. Phase 2 offers exactly one, chosen at install time:

- `agentnetd` started with the environment variable `DORYLINAE_APPROVAL=terminal` writes the
  approval summary and code to **its own stderr**, never to the log file. It is meant for a
  daemon run in the foreground in a terminal the human watches (`agentnetd` in tmux, for
  example). It cannot be set over IPC, and `status` reports `approval: "terminal"` so it is
  visible. This is weaker (anything that can read that terminal can read the code) and is
  documented as such. See OD-P2-3.

## IPC and CLI

| Method | Params | Result |
|---|---|---|
| `approval_list` | none | `{"approvals": [<approval view>]}` (pending only) |
| `approval_confirm` | `{"id", "code"}` | The waiting action's result, plus `"approval": <view>` |
| `approval_reject` | `{"id"}` | `{"approval": <view>}` |

Approval view: `{"id", "kind", "summary", "created", "expires", "state", "attempts_left"}`.

CLI: `agentnet approve [<a-id> <code> | --reject <a-id> | --list] [--json]`.

Error codes: `unknown_approval`, `bad_code`, `approval_expired`, `approval_limit`,
`approval_unavailable` (exit 1).

## Audit

`approval.create {id, kind, subject}`, `approval.approve {id, kind, subject}`,
`approval.reject {id, kind, subject, reason: "user"|"attempts"|"expired"}`,
`approval.bad_code {id, attempts_left}`. `subject` is the id of the waiting object (`g-…`,
`s-…`, `l-…`). Never the code or its hash.

## Tables

```sql
-- migration 15 (2.2a): approvals, grant_policies (policies: grant.md)
CREATE TABLE approvals (
    id        TEXT PRIMARY KEY,                 -- a-<32 hex>
    kind      TEXT NOT NULL CHECK (kind IN ('grant','grant_policy','release','accept_result','device_link','device_scope')),
    subject   TEXT NOT NULL,
    summary   TEXT NOT NULL,                    -- the text shown (local data)
    code_hash TEXT NOT NULL,
    created   TEXT NOT NULL,
    expires   TEXT NOT NULL,
    attempts  INTEGER NOT NULL DEFAULT 0,
    state     TEXT NOT NULL CHECK (state IN ('pending','approved','rejected','expired')),
    decided   TEXT
);
CREATE INDEX approvals_state ON approvals (state, expires);
```

Decided rows are pruned after 30 days (the audit keeps the record). Both tables of migration
15 go into the DROP lists of both rewind tests in `internal/store/store_test.go`.
