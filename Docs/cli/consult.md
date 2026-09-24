# `agentnet consult`

Status: draft (Phase 2, 2.5). Protocol: [../protocol/consult.md](../protocol/consult.md).

Asks a teammate's agent a question, with optional context files. A consult is a
[request](request.md) of type `question`; the peer's answer arrives as a
[work session](session.md) result.

```
agentnet consult <peer> (--question TEXT | --question-from-file F)
                 [--context-file F]... [--title T] [--team TEAM]
                 [--urgency low|normal|high|blocking] [--urgency-reason R]
                 [--deadline D] [--idempotency-key K] [--json]
```

`<peer>` is a peer name or public key, with an optional `@`.

| Flag | Meaning |
|---|---|
| `--question TEXT` | The question, 1–16384 bytes. Exactly one of `--question` and `--question-from-file` is required |
| `--question-from-file F` | Read the question from file `F` (`-` = stdin). CRLF becomes LF |
| `--context-file F` | Repeatable, 1–8 files. A text file of up to 65536 bytes after CRLF becomes LF: UTF-8, no control character except tab and newline (so no ESC). Only the file's **base name** and its text are sent, never its directory. A binary or non-UTF-8 file is refused with `bad_request` ("context file … is not text"), and `-` (stdin) is not accepted |
| `--title T` | 1–120 characters. Default: the first non-blank line of the question, cut to 120 characters in total (119 and `…`) when longer |
| `--urgency`, `--urgency-reason`, `--deadline`, `--team`, `--idempotency-key` | As for [`agentnet request`](request.md) |
| `--json` | Machine-readable output on stdout |

In human mode the command lists on stderr what it is about to send:
`context: sending outbox.go (1834 bytes)`. Context files are your choice of what to disclose to
the peer's agent; they are stored on both machines. They are never written to the audit log,
notifications or webhooks: the audit log records only the counts `context_files` and
`context_bytes` (`request.submit` here, `request.in` on the peer).

## Behaviour

The command returns in under 2 seconds with `status: queued`, whether or not the peer is
online (as `agentnet request`). Its result carries `session`, the
[derived session id](../protocol/work-session.md#session-id): the session does not exist until
the peer answers, but you can wait on it at once:

```
agentnet wait s-… --timeout 300 --json
```

`wait` exits 0 with `"wait": "result"` and the whole answer once it is visible,
`"declined"` if the peer declined, `"cancelled"` if the request was cancelled, and exit 4
(`"timeout"`) if nothing changed in time. Accept the answer with
`agentnet accept-result <session>`; both sides then show the session `closed` and the request
`completed`.

**Limits:** 8 context files, 65536 bytes per text, and `len(canonical(request))` ≤ 327680 bytes
(`MaxQuestionBody`, 320 KiB) for a question with context. Any other request keeps the 65536-byte
cap.

### Human output

```
Queued normal consult r-8e0c… to bob (team backend)
  session: s-5214… (wait with 'agentnet wait s-5214…')
```

### `--json` output

The same object as `agentnet request --json` (Docs/cli/request.md), with the derived session:

```json
{"ok": true, "id": "r-8e0c…", "mail_id": "m-…", "status": "queued", "duplicate": false,
 "team": {"id": "t-…", "name": "backend"}, "urgency": "normal",
 "peer": {"name": "bob", "public_key": "…", "daemon_online": true, "last_seen": "2026-09-24T09:31:05Z"},
 "session": "s-5214…"}
```

Failures print `{"ok":false,"error":{"code","message"}}`:

| Code | When |
|---|---|
| `bad_request` | More than 8 files, a text over 65536 bytes or with a control character, a bad file name, a binary file, a bad title/urgency/deadline. The message names the field (`context`, `context[1].text`, …) |
| `request_too_large` | The whole question is over 327680 bytes |
| `no_shared_team`, `ambiguous_team`, `not_team_member`, `unverified_peer`, `unknown_peer` | As for `agentnet request` |
| `idempotency_conflict` | The key was used with different params (context included) |
| `daemon_not_running`, `usage` | |

Exit codes: 0 queued (or duplicate), 1 error, 2 usage, 3 daemon not running.

## Answering a consult

The peer's agent sees the question in `agentnet inbox` (the list shows `context_files` and
`context_bytes` only) and reads the context with `agentnet request show <id> --json` (the
`context` array of `{name, text}`, in the order given).

```
agentnet result <r-id or s-id> --file answer.md [--status S] [--summary T] [--notes T] [--json]
```

On a `question` that is still `pending` or `deferred`, `agentnet result` accepts the request,
opens the session and submits the result in **one step**: either all of it happens or, on any
error (a result over 65536 bytes, say), the question stays pending and nothing is sent.
`--status` defaults to `n/a` and `--verification` to `none`. `--file` fills `output` (up to
32 KiB; there is no multi-part answer in Phase 2, shorten it or put the rest in `--notes`).

For every other request type, and for a question that was already accepted, the normal flow
applies: `agentnet accept <id>`, then `agentnet result <id> --status S …` (`--status` is
required there). See [session.md](session.md#agentnet-result-id).
