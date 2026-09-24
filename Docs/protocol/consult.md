# Consult

Status: **draft** for Phase 2 (plan step 2.5). Ticket: 2.5 in
[../review/23-phase2-tickets.md](../review/23-phase2-tickets.md). Change this document first.

A **consult** is a question from one agent to a teammate's agent, answered with a result. It
is not a new mechanism: it is a [request](request.md) of type `question`, with optional
context files, whose [work session](work-session.md) opens when the peer answers.

```
agentnet consult @bob --question "Is the retry backoff in outbox.go safe under clock skew?" \
    --context-file internal/mail/outbox.go --json
  → {"ok": true, "id": "r-…", "session": "s-…", "status": "queued", …}

(bob's agent)  agentnet inbox --json            # sees the question and its context
               agentnet result r-… --file answer.md --json

(alice's agent) agentnet wait s-… --timeout 300 --json
  → {"ok": true, "wait": "result", "session": {…, "result": {"status": "n/a", "output": "…"}}}
```

## Request object additions

One optional member is added to the request object ([request.md §Request object](request.md#request-object)):

| Member | Req. | Type | Rules |
|---|---|---|---|
| `context` | no | array | Allowed **only** when `type = question` (else `bad_request` / `bad_body`). 1–8 [context files](#context-files), in order |

### Context files

`{"name": "<string>", "text": "<string>"}`, exactly these members:

| Member | Rules |
|---|---|
| `name` | 1–255 bytes, the file's **base name** as given (no `/`, `\`, NUL or control characters; not `.` or `..`). Informational: the receiver never writes a file with this name on its own |
| `text` | 1–65536 bytes of UTF-8, after CRLF → LF. No control characters except `\n` and `\t` (so no ESC). Binary files are refused by the CLI (`bad_request`, "context file is not text") |

### Size limits

| Limit | Value | Sender error | Recipient |
|---|---|---|---|
| Context files | 1–8 | `bad_request`, field `context` | `bad_body` |
| Each `text` | 65536 bytes | `bad_request`, field `context[i].text` | `bad_body` |
| **Total body of a `question` with `context`** | `len(canonical(request))` ≤ **327680 bytes** (`MaxQuestionBody`, 320 KiB) | `request_too_large` | `bad_body` |

Every other request keeps the 65536-byte cap. 327680 bytes plus the mail wrapping stays far
below `MaxMailPlaintext` (716800), so a consult is still one mail. The larger total is
limited to `question` requests with context, so briefs of other types keep their Phase 1
bounds. Context is **content**: never audited or logged, never in notifications or webhooks
(only the count `context_files` and the size `context_bytes` are audited on `request.submit`
and `request.in`).

Context files are the sender's choice of what to disclose. The CLI prints the names and
sizes it is about to send on stderr in human mode.

## `agentnet consult`

```
agentnet consult @peer --question TEXT | --question-from-file F
                 [--context-file F]… [--title T] [--team T] [--urgency U --urgency-reason R]
                 [--deadline D] [--idempotency-key K] [--json]
```

It is `request_submit` with:

- `type = question`;
- `brief` = the question (the request brief rules: 1–16384 bytes);
- `title` = `--title`, or else the first line of the question, cut to 120 code points
  (with `…` when cut);
- `context` = the files, in the given order.

The **submit result** ([request.md](request.md#submit-result-19)) gains `"session":
"s-…"`, the [derived session id](work-session.md#session-id), for every request type (not
only consults). So the caller can `wait` on it at once, although the session does not exist
until the peer answers.

The default title is cut to 120 code points **in total** (119 code points and `…`), so it is
always a valid title. The daemon (not only the CLI) turns CRLF into LF in each context text.
`request_show` (and so `wait`) also accepts a derived session id `s-…` for a request whose
session does not exist yet, and resolves it to the request. `ws_result` (`agentnet result`) on
a pending or deferred question, given its `r-` or `s-` id, does the one-step answer below; there
`status` defaults to `n/a`, elsewhere it stays required.

IPC: `request_submit` gains the optional param `context: [{"name", "text"}]`. There is no
separate consult method.

## Answering

The recipient's agent sees the question in `inbox` (the request view gains `context` in
`request_show` only; `inbox_list` shows `context_files` and `context_bytes` instead, so the
list stays small).

`agentnet result <r-id or s-id> --file answer.md [--status S] [--summary T] [--json]` on a
**`question` request that is `pending` or `deferred`** does, in **one transaction** on B:
accept the request (`request.accept`), open the session, and submit the result (`ws.result`
with `round = 1`). This is the plan's "auto-opened session". `--status` defaults to `n/a`,
`verification` to `none`. `--file` fills `output` (32 KiB max, D14). A longer answer must be
shortened, or split into `notes`; there is no multi-part answer in Phase 2.

The requester's daemon applies the `request.accept` and the `ws.result` in whichever order
they arrive ([work-session.md §Ordering](work-session.md#ordering)).

For every other request type, and for a `question` that the recipient accepted first, the
normal flow applies (`accept`, then `result`).

The requester accepts the answer with `agentnet accept-result`, which closes the session and
completes the request on both sides. A consult usually carries no grant, so no quarantine
applies; if the requester did grant sensitive access during the session, the answer is
quarantined like any result.

## `agentnet wait`

Specified in [work-session.md §CLI](work-session.md#cli). For a consult: exit 0 with
`wait: "result"` and the full answer once it is visible, exit 0 with `wait: "declined"` or
`"cancelled"` if the peer declined or the requester cancelled, exit 4 on `--timeout`
(`timeout`).

## Security considerations

- The question and the context are untrusted text for the recipient's agent, like a brief.
  They grant nothing.
- The answer is untrusted text for the requester's agent. It grants nothing, and it is
  quarantined when a sensitive grant was issued in the session.
- Context files can leak what the sender did not mean to share; the CLI lists them before
  sending and the audit records only counts and sizes.
- The 320 KiB body cap bounds storage per consult; the Phase 1 urgency budget and request
  limits bound the rate.

## Acceptance (plan 2.5)

Round trip headless in two harnesses: agent A consults B with one context file; agent B
answers with `agentnet result … --file answer.md --json`; `agentnet wait <session>
--timeout 300` on A returns the result; A accepts it; both sides show the session `closed`
and the request `completed`. Ticket 2.H runs it.
