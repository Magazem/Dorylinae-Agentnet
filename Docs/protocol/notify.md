# Notifications

Status: **implemented**, Phase 1 ticket 1.8 (split into 1.8a desktop and 1.8b webhook
in [../review/11-phase1-tickets.md](../review/11-phase1-tickets.md)), in `internal/notify`.
Change this document first for any further change.

The daemon tells the human that something needs attention through two optional channels: a
**desktop notification** and an outgoing **webhook**. Both are local policy. Nothing about them
crosses the relay.

## Triggers

| Event | Fires on | Default |
|---|---|---|
| `request.received` | The recipient commits a **new** `pending` request (not a duplicate, and not auto-declined) | on |
| `request.accepted` | The sender mirror applies `request.accept` | on |
| `request.declined` | The sender mirror applies `request.decline` (any `code`) | on |
| `request.deferred` | The sender mirror applies `request.defer` | off |
| `request.completed` | The sender mirror applies `request.complete` | off |
| `request.cancelled` | The **recipient** commits a `pending` or `deferred` request as `cancelled` by its sender ([request.md §Cancel](request.md#cancel-od-p1-11)). Not for a cancel that arrives before its request, and not on the sender side | on |

A mirror update that is ignored (`seq` not higher) fires nothing. The trigger runs in the mail
kind's `After` hook ([mail.md](mail.md), `internal/mail.Kind.After`). It enqueues work and
returns, and never blocks the receiver.

**Latency (acceptance, 1.8):** the desktop notification is shown within **5 s** of the mail
being committed.

Settings live in the `settings` table (migration 10, [presence.md](presence.md#tables)):

```json
"notify.events":  {"request.received": true, "request.accepted": true, "request.declined": true,
                   "request.deferred": false, "request.completed": false, "request.cancelled": true}
"notify.desktop": {"enabled": true}
"notify.webhook": {"url": "https://...", "format": "generic", "title": false}
```

`notify.webhook` is absent when no webhook is set. The webhook **secret** is not in SQLite
([Secret](#secret)).

## Text and sanitising

Peer-supplied strings (the request `title`, the peer's card `name`, and the team name) are
**untrusted**. Before they are used in any notification or payload, `notify.Clean(s, max)`:

1. replaces every control character (U+0000–U+001F, U+007F–U+009F), the bidi controls
   (U+200E, U+200F, U+202A–U+202E, U+2066–U+2069) and U+2028/U+2029 with a space;
2. collapses runs of spaces and trims;
3. truncates to `max` code points (title 80, name 40), appending `…` when cut.

Desktop text, where `Urgency` is capitalised and `(from)` is the local peer name:

| Event | Title | Body |
|---|---|---|
| `request.received` | `<Urgency> <type> request from <name>` | `<title>` |
| `request.accepted` | `<name> accepted your <type> request` | `<title>` |
| `request.declined` | `<name> declined your <type> request` | `<title>` |
| `request.deferred` | `<name> deferred your <type> request until <until, local time>` | `<title>` |
| `request.completed` | `<name> completed your <type> request`, plus ` (<status>)` when the completion carried a [result](request.md#result-payload-d14) | `<title>` |
| `request.cancelled` | `<name> cancelled their <type> request` | `<title>` |

The brief, reasons, notes and artifacts are **never** shown. Of a completion
[result](request.md#result-privacy) (D14), only the `status` (`pass`, `fail`, `partial` or
`n/a`) is shown; its summary, exit code, output and artifacts never are.

## Desktop

Package `internal/notify`, `Desktop.Show(ctx, title, body) error`, with a 3 s timeout per call.
A failure is logged (`event=notify_desktop_fail`) and audited once per hour at most
(`notify.fail {channel: "desktop", error}`). It is not retried.

| OS | Mechanism | Note |
|---|---|---|
| macOS | Exec `/usr/bin/osascript` with the script **fixed** and the text passed as `argv`: `osascript -e 'on run argv' -e 'display notification (item 2 of argv) with title (item 1 of argv)' -e 'end run' -- <title> <body>` | Peer text is **never interpolated into AppleScript source**, so no quoting bugs can inject a script. The launchd user agent runs in the Aqua session, which is required |
| Linux | D-Bus `org.freedesktop.Notifications.Notify` on the session bus, via exec `gdbus call --session --dest org.freedesktop.Notifications --object-path /org/freedesktop/Notifications --method org.freedesktop.Notifications.Notify agentnet 0 '' <title> <body> [] {} 5000`. Fall back to `notify-send -a agentnet -- <title> <body>` | Arguments go as argv, never through a shell. `gdbus call` **parses each argument as GVariant text**, so `<title>` and `<body>` are passed as GVariant string literals the daemon builds itself: `'` + the text with `\` → `\\` and `'` → `\'` + `'`. Unencoded, a title such as `'x'` would be re-parsed. The body of a freedesktop notification may be interpreted as markup, so `&`, `<` and `>` in `<body>` become `&amp;`, `&lt;` and `&gt;` (for both gdbus and `notify-send`). The systemd user unit inherits `DBUS_SESSION_BUS_ADDRESS` from the user manager. If there is no bus, it fails |
| Windows | A toast through Windows PowerShell with **fixed** script text. The two strings are passed base64-encoded (UTF-16LE) in the environment variables `AGENTNET_N_TITLE` and `AGENTNET_N_BODY`, and decoded and XML-escaped (`[Security.SecurityElement]::Escape`) inside the script. The AppUserModelID is PowerShell's (`{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe`) | Peer text never appears in the command line or the script source. It needs the interactive session. The Task Scheduler task runs "only when user is logged on" |

The plan's `beeep` is **not** used. It builds AppleScript and PowerShell source by string
formatting, which the requirements above forbid. The mechanisms above are about 150 lines and
need no cgo and no new dependency.

## Webhook

### Configuration

`agentnet notify --webhook URL` ([../cli/notify.md](../cli/notify.md)):

- The URL must be `https://`, or `http://` only to a loopback host. At most 2048 bytes. There
  is no user info in the URL. "Loopback host" means the literal `localhost`, `127.0.0.0/8` or
  `::1`, not a name that resolves there.
- **Dial-time address check** (SSRF). Any local process that can reach the IPC socket,
  including an agent steered by a hostile request, can set the URL, so the check is made on
  the address actually dialled, not on the name (the daemon resolves the name itself, checks
  each address and connects to exactly the address that passed): an `https` URL may not
  connect to link-local addresses (`169.254.0.0/16`, `fe80::/10`, including cloud metadata
  at `169.254.169.254`), unspecified addresses, or multicast, nor (hardening) to private
  ranges (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`); loopback is allowed. An `http` URL
  may connect only to loopback. A refused dial is a permanent failure
  (`error = "blocked_address"`). With a proxy from the environment, the check applies to the
  proxy address with the base list only (a private or loopback proxy is allowed), and the
  proxy is trusted by the user's configuration.
- On first set, and on `--rotate-secret`, the daemon generates a 32-byte secret from
  `crypto/rand` and prints it **once** as `whsec_` + base64url (no padding). Only the owner of
  the receiving endpoint needs it.
- `--format generic|slack|discord` (default `generic`) and `--webhook-title on|off` (default
  **off**, OD-P1-9).
- `--webhook off` removes the URL and deletes the secret.
- Audit `notify.config {desktop?, events?, webhook: "set"|"removed"|"rotated"?, format?, title?}`. The URL
  and the secret are never audited.

### Secret

Keystore secret (same backends as the identity): keychain service `dorylinae`, account
`webhook` (there is one webhook per daemon), or file `<config dir>/webhook.key` (owner-only).
The value is the raw 32 bytes. Rotation replaces it at once. Pending retries are signed with
the new secret, so the receiver must be updated before the next attempt.

### Payload

The event object (`generic` format), UTF-8 JSON, at most 8 KiB:

```json
{
  "v": 1,
  "id": "w-0123456789abcdef0123456789abcdef",
  "event": "request.received",
  "ts": "2026-10-01T09:12:03Z",
  "request": {
    "id": "r-0123456789abcdef0123456789abcdef",
    "type": "review",
    "urgency": "high",
    "state": "pending",
    "title": "Review the retry change"
  },
  "peer": {"name": "bob", "fingerprint": "2ED9TGVER47163MCC451"},
  "team": {"id": "t-...", "name": "backend"},
  "text": "High review request from bob"
}
```

- `id`: `w-` + 32 hex, unique per delivery (the replay id). `ts`: when the event was created.
- `request.title` is present **only** with `title: true`. Never included: the brief, artifacts,
  requested grant, reasons, notes, public keys, deadline and `urgency_declared`.
- `request.result_status` (`pass`, `fail`, `partial` or `n/a`) is present only on
  `request.completed` for a completion that carried a [result](request.md#result-payload-d14),
  and **only with `title: true`** (OD-P1-9, D14). The result's summary, exit code, output and
  artifacts are never included.
- `text`: the desktop title line (sanitised), without the title unless `title: true`, when
  `: <title>` is appended. With `title: false` the ` (<status>)` suffix of
  `request.completed` is left out too.
- `slack` format: the generic object, with `&`, `<` and `>` in `text` escaped as `&amp;`,
  `&lt;` and `&gt;` (Slack's required escaping). Unescaped, a peer name or title such as
  `<!channel>` or `<https://evil|click>` would ping a whole channel or render a disguised
  link. Slack incoming webhooks read `text` and ignore other members. `discord` format: the
  generic object with `content` = `text` added, plus `"allowed_mentions": {"parse": []}`
  so that `@everyone`, `@here` and role or user mentions in peer text never ping anyone.
  In `content`, each of `` \ * _ ~ ` | > # - [ ] ( ) < @ `` is backslash-escaped, so that
  a masked link `[click](https://evil)`, a heading or a spoiler in peer text renders as
  typed. Discord requires `content`. Signing and headers are identical for all formats.

### Signature

Scheme `v1`, HMAC-SHA256, in the same shape as Standard Webhooks:

```
signed_content = "v1:" ‖ timestamp ‖ ":" ‖ id ‖ ":" ‖ body      // timestamp: decimal unix seconds; body: exact bytes sent
signature      = base64url_nopad(HMAC-SHA256(secret, signed_content))
```

Headers:

| Header | Value |
|---|---|
| `Content-Type` | `application/json` |
| `User-Agent` | `agentnetd/<version>` |
| `Dorylinae-Webhook-Id` | the payload `id` |
| `Dorylinae-Webhook-Timestamp` | `timestamp` (the time of **this attempt**) |
| `Dorylinae-Signature` | `v1=<signature>` |

**Receiver verification** (documented for integrators, and implemented by the test
receiver in `tests/`): recompute the signature with a constant-time compare, reject if
`|now − timestamp| > 300 s`, and **do not process** an `id` already seen within the last
24 h, but answer it `2xx`. A retry re-signs with a new timestamp and keeps the same `id` and
body, so a receiver that processed a delivery whose response was lost must acknowledge the
retry; answering it with a `4xx` would end the row `failed`. Remembering ids for 24 h covers
the whole retry schedule. The body `id` must equal the header. Slack and Discord cannot
verify. The signature is for custom receivers.

### Delivery

- Queue table `webhook_queue` (migration 13; 12 is `requests_result`, D14). Enqueue in the trigger, and a worker sends.
- `POST` with a 10 s timeout. **Redirects are not followed.** A 3xx is a permanent failure.
  Proxy from the environment (`http.ProxyFromEnvironment`). TLS verification is always on.
- `2xx` → `sent`. `408`, `429`, `5xx` or a network error → retry. Other `4xx` → `failed`.
- Retry delays: 10 s, 1 min, 5 min, 30 min, 2 h, 6 h (×U(0.9, 1.1)). After the 7th attempt, or
  when older than 24 h, → `failed`. Honour `Retry-After` (seconds) if it is longer, capped at 6 h.
- A `failed` delivery is audited `notify.fail {channel: "webhook", event, id, status?}`. It
  never holds the URL or the body.
- Rows are deleted 7 days after `sent` or `failed`. At most 1000 non-final rows. When full,
  the oldest pending row is dropped (`failed`, `error = "overflow"`).
- `agentnet notify --test` enqueues one `test` event (`request` omitted, `text` =
  `"agentnet test notification"`) and shows a desktop notification.

```sql
-- migration 13 (1.8b): webhook_queue
CREATE TABLE webhook_queue (
    id           TEXT PRIMARY KEY,                 -- w-<32 hex>
    event        TEXT NOT NULL,
    body         TEXT NOT NULL,                    -- exact JSON bytes to send
    state        TEXT NOT NULL CHECK (state IN ('pending', 'sent', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    next_attempt TEXT,
    created      TEXT NOT NULL,
    updated      TEXT NOT NULL,
    status       INTEGER,                          -- last HTTP status
    error        TEXT
);
CREATE INDEX webhook_queue_due ON webhook_queue (state, next_attempt);
```

`body` holds only the payload above, which never holds the brief. The URL is re-read from
settings on each attempt, so changing it redirects pending deliveries. Removing the webhook
marks every pending row `failed` (`error = "removed"`).

## Privacy summary

| Data | Desktop | Webhook |
|---|---|---|
| Event, type, urgency, state | yes | yes |
| Peer name, team name | yes (sanitised) | yes (sanitised), plus the fingerprint and team id |
| Title | yes | only with `--webhook-title on` |
| Completion result `status` (D14) | yes | only with `--webhook-title on` |
| Brief, artifacts, reasons, notes, grant, keys | never | never |
| Result summary, exit code, output, artifacts (D14) | never | never |

## Audit

| Action | Actor | Detail |
|---|---|---|
| `notify.config` | `cli` | see [Configuration](#configuration) |
| `notify.fail` | `daemon` | `{channel, event?, id?, status?, error?}` (desktop: at most once per hour) |
