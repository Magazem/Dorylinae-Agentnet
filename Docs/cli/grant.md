# `agentnet grant`, `grants`, `revoke`

Give the worker of a [work session](../protocol/work-session.md) scoped, expiring,
revocable read access to one of your directories or git branches
([Docs/protocol/grant.md](../protocol/grant.md)). The holder reads it with
[`agentnet fetch`](fetch.md). Introduced by ticket 2.3c (the daemon side, IPC
`grant_create`, `grant_list`, `grant_revoke` and `grant_policy_*`, came with 2.2c).

```
agentnet grant @peer --session S --action fs.read|git.read --resource PATH[#BRANCH]
               [--scope P] [--expires D] [--public] [--json]
agentnet grants [--session S] [--issued|--held] [--json]
agentnet revoke <g-id> [--json]
agentnet grant policy add @peer --action A --resource PATH[#BRANCH] --max-expires D
               [--scope P] [--public] [--until D] [--json]
agentnet grant policy list [--json]
agentnet grant policy remove <p-id> [--json]
```

## `grant`

Only the **requester** of the session can grant, and only while the session is open.

| Flag | Meaning |
|------|---------|
| `--session S` | The work session (`s-...`) between you (requester) and `@peer` (worker) |
| `--action A` | `fs.read` (a directory) or `git.read` (a branch) |
| `--resource R` | An absolute path of an existing directory. For `git.read` it must be the top of a work tree or a bare repository and needs `#BRANCH`. The path is never sent to the peer |
| `--scope P` | Only this relative path inside the resource (default: all of it) |
| `--expires D` | A duration, 1 minute to 7 days (default 2 h) |
| `--public` | `git.read` only: the repository is not private. Every `fs.read` grant, and every `git.read` grant without `--public`, is sensitive: the session's result is quarantined until you [release](session.md) it |
| `--json` | `{"ok":true,"grant":{...},"approval":{...}}`; `approval` is absent when a policy issued the grant at once |

The command **never asks for the approval code and no flag takes one**. Unless a policy
matches, the grant waits as `pending_approval` and the command exits 0 after printing
the approval id; you type the code shown by the AgentNet approval window (reopen it with
`agentnet approve --open <a-id>`). Once approved the grant is sent to the peer, sealed.
A grant is never edited: a different scope needs a new grant.

Errors: `unknown_session`, `not_requester`, `bad_state`, `unknown_peer`,
`unverified_peer` (a peer whose trust is `relay` on a non-loopback relay),
`forbidden_resource` (a relative or missing path, the config directory or a path
inside or around it, your home directory itself, a filesystem root, not a git top
level, a missing branch), `bad_request`, and the approval errors `approval_limit`,
`approval_locked`, `approval_unavailable`.

Exit codes: 0 grant created (pending approval or issued), 1 error, 2 usage, 3 daemon not
running.

## `grants`

Lists grants: `--session S` limits to one session, `--issued` to grants you gave,
`--held` to grants you hold (they exclude each other). The table shows id, direction,
peer, action, resource (`label[#branch]`, never a local path), scope, expiry and state
(`pending_approval`, `active`, `revoked`, or `expired` once past its expiry). `--json`:
`{"ok":true,"grants":[...]}` with the grant view of the protocol document; `resource.path`
(the local path) appears on issued grants only, and `grants` is `[]` when there are none.

Exit codes: 0, 1 error, 2 usage, 3 daemon not running.

## `revoke`

`agentnet revoke <g-id>` ends a grant you gave. The grantor's daemon checks the grant on
every fetch, so **the holder's next fetch fails with `revoked`** with no delay, and a
read in progress stops at its next fragment; the holder is also told by mail. Revoking
needs no approval and is idempotent (a second call prints "already revoked" and
`"duplicate":true`). `--json`: `{"ok":true,"grant":{...},"mail_id":"m-..."}`.

Errors: `unknown_grant`, `not_grantor` (you hold this grant; only the grantor revokes).
Exit codes: 0, 1 error, 2 usage, 3 daemon not running.

## `grant policy`

A policy lets matching grants issue **without a prompt**. `add` needs a human approval
(kind `grant_policy`, printed like `grant`); `remove` does not. A policy matches only the
exact peer, action, resolved resource path and branch, a scope inside its own, the same
sensitivity (a `--public` policy covers only `--public` grants) and an expiry up to
`--max-expires`. It ends at `--until` (default 30 d, at most 90 d) and with the peer
(`peers remove`). At most 50 policies. Sensitive grants can be covered; the result
quarantine still applies.

`list --json`: `{"ok":true,"policies":[{"id","peer","action","branch"?,"scope"?,"public",
"max_expires_s","until","created"}]}`.

Exit codes: 0, 1 error, 2 usage, 3 daemon not running.
