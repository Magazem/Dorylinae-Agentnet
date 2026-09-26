# Accounts on the hosted relay

Status: **draft** (Phase 4 spec, tickets 4.2a–4.2c in
[../review/49-phase4-tickets.md](../review/49-phase4-tickets.md)). Not approved. Open
choices are **OD-P4-n**.

Plan 4.2: "Email plus magic link, or GitHub OAuth, to bind a daemon identity to a person and
enforce the cap. No passwords stored. Acceptance: a daemon cannot connect to the hosted relay
without a bound account."

## What an account is, and what it is not

- An **account** is a person known to the relay operator by a **GitHub user** or an **email
  address** (OD-P4-2). It exists only on a relay started with `--accounts github|email|both`.
  Self-hosted relays default to `--accounts off` and behave as in Phase 3 (with the 4.0 limits).
- A **binding** links one daemon identity key to one account. An account has at most **4**
  bound keys (laptop, desktop, an own-device helper, a spare).
- A **billing team** is the relay's unit for quotas and invites ([invites.md](invites.md)). It
  is **not** a daemon team: it has no roster signature, no names the peers see, and grants
  no trust. An account belongs to at most one billing team in the beta.

**Invariant: an account grants no trust between peers.** Trust still comes only from pairing
([pairing.md](pairing.md)) and team introductions ([team.md](team.md)). Two keys bound to the
same account are not paired by that fact; a key bound to someone's account can do nothing to
their peers that an unbound key could not, except spend that account's quota. D5 (refuse
requests from `trust=relay` peers on a non-loopback relay) is unchanged.

**No passwords.** The relay stores no password, no OAuth access token after the login
completes, and no email content. GitHub OAuth requests **no scopes** (the public profile gives
the numeric user id and login).

## Relay states of a connection

After `auth` (v2, [relay-hosted.md](relay-hosted.md#relay-authentication-v2-binds-the-relays-name))
on a relay with accounts, `ready` carries the key's account state:

```json
{"op":"ready","public_key":"<key>","features":["ephemeral","accounts"],
 "account":{"state":"bound","id":"acc_7Q…","display":"@octocat","team":"bt_3K…"}}
```

| `state` | Meaning | Allowed after `ready` |
|---|---|---|
| `unbound` | The key is not bound | `bind_start`, `bind_poll`, `bind_cancel` only. Every other frame → `error` `account_required` (the connection stays open for binding). Nothing is delivered to it, nothing is queued for it from others' point of view (senders get `account_required`) |
| `bound` without a billing team (`team` absent) | A person who logged in but is not on an invited team | Binding frames, `pair_redeem` (to join a team, [invites.md](invites.md#seats-admission-by-pairing)), `pair.confirm` envelopes, `ack`, `invite_redeem`. Mail and presence → `account_required` |
| `bound` with a billing team | Normal member | Everything in [envelope.md](envelope.md), within the limits and quota |
| `suspended` | Operator suspended the account, or its billing team | Nothing; `ready` is followed by `error` `account_suspended` and close 1008 |

An `unbound` connection is still subject to every per-prefix and per-key limit, and at most
**16 unbound connections per prefix** are kept open (they exist only to bind; the oldest is
closed first). Not 2: a team that runs `agentnet setup` together behind one office NAT has one
unbound connection per member until each confirms in the browser (review 50 M5).

### Who may send to whom

On an accounts relay an envelope is routed (directly or through the queue) only if **both**
`from` and `to` are bound keys of **active** accounts, both with a billing team, except
`pair.confirm`, which only needs both keys bound. Any bound member may send to any other
bound member, across billing teams: daemon teams can span billing teams (a member of two
teams, introductions), and the relay must not need the daemon team graph. Abuse between bound
accounts is bounded by the per-pair queue caps and rate limits and, ultimately, by the invite
gate (every account comes through an invite).

## Binding flow (device authorization)

Shaped like the OAuth 2.0 device authorization grant (RFC 8628) so that an agent can run it
and a human only confirms in a browser (plan 4.9).

```
agentnet login           daemon                 relay                    browser (human)
   | account_login ------>|--- bind_start -------->|                          |
   |                      |<-- bind_pending --------|  user_code, url, 10 min  |
   |<- url + code --------|                         |                          |
   |  (prints, opens browser unless --no-browser)   |<--- GET url, sign in ----|
   |                      |                         |<--- type user_code ------|
   |                      |                         |---- "bind key FP to you?"|
   |                      |                         |<--- confirm (POST) ------|
   |  (poll) ------------>|--- bind_poll ---------->|                          |
   |<- bound @octocat ----|<-- bind_done -----------|                          |
```

### Frames

`bind_start` (daemon → relay): `{"op":"bind_start","device":"laptop","os":"windows"}`.
A key that is already bound gets `error` `already_bound`: moving a key to another account
needs `agentnet logout` first (review 50 L6).
`device` is a label the human sees on the confirm page (≤ 32 chars, `[A-Za-z0-9 ._-]`,
defaults to the host name shortened); `os` from `runtime.GOOS`.

`bind_pending` (relay → daemon):
`{"op":"bind_pending","ref":"bnd_…","user_code":"WDJB-MJHT","url":"https://relay.example/login","expires":"…","interval":5}`

- `user_code`: 8 characters of Crockford base32 (40 bits), shown as `XXXX-XXXX`; stored hashed;
  single use; valid 10 minutes; at most one pending bind per key; 5 wrong codes per browser
  session, then that session must restart.
- `url` is the fixed login page. **The code is never put in the URL** (no pre-filled
  `?code=`): typing it is what stops a phishing link from binding an attacker's key to the
  victim's account in one click.
- **The daemon does not trust `url`** (review 50 H3). The relay is untrusted (D17), and the
  CLI would otherwise hand a relay-chosen string to the OS opener (`rundll32
  url.dll,FileProtocolHandler`, `open`, `xdg-open`), which also launches `file:`, `ms-msdt:`,
  `search-ms:` and other registered handlers. The CLI accepts `url` only if it parses as
  `https://`, its host and port equal the relay origin's, and it has no user info; otherwise
  it prints "the relay sent an unexpected login URL" and opens nothing. The URL is printed with
  control and bidi characters replaced ([relay-hosted.md](relay-hosted.md#daemon)).

`bind_poll` → `bind_pending` (unchanged), `bind_done`
`{"op":"bind_done","account":{…as in ready…}}`, or `error` `bind_expired` / `bind_denied`.
Polling faster than `interval` gets `rate_limited`. On `bind_done` the relay re-sends
`ready`-equivalent state; the daemon does not reconnect.

`bind_cancel` drops a pending bind.

### Confirm page

After sign-in and a correct code, the page shows: the key's **fingerprint** in the same form as
`agentnet identity` (`fp()`), the `device` label, the `os`, the time the bind was started, and
the account's current bound keys. Text: "Only confirm if you just ran `agentnet login` or
`agentnet setup` on this computer and the fingerprint matches what it printed." Buttons
**Bind this device** and **Deny**. The fingerprint is also printed by `agentnet login`.

If the account already has 4 keys, the page offers to unbind one first.

When a key is bound, every **other** live key of the account gets a content-free control frame
`{"op":"account_changed"}` and its daemon shows a desktop notification "a new device was
bound to your account; check `agentnet status`", so a victim of device-code phishing sees it
(review 50 L16).

### Web security requirements (ticket 4.2b)

- Every page over TLS; HSTS (`max-age` 1 year); `Content-Security-Policy: default-src 'none';
  style-src 'self'; form-action 'self'; frame-ancestors 'none'`; no third-party scripts, fonts
  or analytics.
- Session cookie: random 256-bit id, `HttpOnly; Secure; SameSite=Lax; Path=/`, lifetime 1 hour,
  server-side row, rotated after sign-in (no session fixation).
- OAuth: `state` (256-bit, bound to the pre-login cookie) and PKCE; the callback verifies both;
  the access token is used once to read `GET /user` and then discarded. "Sign in with GitHub"
  is a **GET link** to a relay endpoint that sets the pre-login cookie and redirects: a form
  POST that redirects to `github.com` is blocked by `form-action 'self'` in Chromium browsers
  (review 50 L5).
- Magic link (if OD-P4-2 enables email): a 256-bit token, stored hashed, single use, valid
  15 minutes, bound to the browser that asked (a cookie), 3 emails per address per hour and 20
  per prefix per hour. The email contains the link and nothing about the device.
- Every state-changing request is a POST with a CSRF token tied to the session.
- Rate limits: sign-in starts 20 per prefix per 10 minutes; wrong user codes 5 per web
  session **and** 20 per account per day.
- **Outages** (review 50 L15): the relay is the account service (one binary, one database).
  If GitHub (or the mail provider) is down, new sign-ins fail and bindings in progress expire;
  existing bindings, routing and quotas are unaffected (nothing is checked against GitHub
  after the bind). If the relay's database is unavailable, the relay refuses new connections
  (fails closed) and `/healthz` is 503.
- **Test hooks are not in release builds** (review 50 M7): the hook that completes a bind
  without the browser, and the fake OAuth provider, are compiled only with the build tag
  `testhooks`. Release and container builds do not set it, and a test builds the release
  configuration and asserts that the hook's route and flag are absent. A staging relay that
  needs them (4.9b) is a separate deployment with its own database, domain, OAuth app and
  operator keys, and a banner on every page.

## Account page (minimal)

Signed in, a person sees: their account (GitHub login or email), their billing team and its
seats, their bound keys (fingerprint, device label, OS, bound at, last connected **day**), and
three actions: **Unbind** a key, **Leave billing team**, **Delete account**. There is no
profile, avatar or settings beyond this.

## Revocation

| Action | Effect |
|---|---|
| Unbind a key (page, `agentnet logout`, or operator) | The binding row is deleted; the key's live connection gets `error` `account_revoked` and close 1008; the key is `unbound` on its next connect. Envelopes already queued for it stay until the TTL (a re-bind gets them; they are ciphertext) |
| `agentnet logout` | Sends `unbind` for **its own key** only, then clears the local account state. Needs no browser |
| Delete account | All bindings removed, account row deleted, the email / GitHub id removed. If it was the last member of its billing team, the team is closed. Counters already aggregated per billing team stay (they carry no account id) |
| Suspend (operator) | `relay admin account suspend <acc>` or `… team suspend <bt>`; live connections closed; reversible |
| Lost device | Unbind it from the account page on another device (or sign in in any browser). This does **not** remove it from anyone's peers: that is `agentnet peers remove` / `team remove` on the peers' side, as today |

A stolen laptop whose key is still bound can keep using the relay until unbound; unbinding
stops relay access at once, and peers must still remove its key (documented in
[../beta/known-limitations.md](../beta/known-limitations.md) by 4.2c).

## Enforcing the cap without seeing content

The relay charges each routed envelope to the **sender's account's billing team**
([relay-hosted.md §4](relay-hosted.md#4-quotas-ticket-41c)). It needs only `from` (already
authenticated), the size, and the binding table. It never needs a kind, a team id from the
envelope, or a request count. Presence (ephemeral) is not charged.

## Daemon side (ticket 4.2c)

- IPC methods `account_login` (sends `bind_start`, returns `url`, `user_code`, `fingerprint`,
  `expires` in < 2 s), `account_status`, `account_logout`. `wait`-style polling:
  `agentnet login --wait [--timeout 600]` polls `bind_poll` every `interval` and returns when
  bound, denied or expired (exit 0 / 1 / 4).
- CLI `agentnet login [--no-browser] [--wait] [--json]`, `agentnet logout [--json]`; the
  account (display, billing team, state) in `agentnet status` and `doctor`. `login` opens the
  browser with the OS opener unless `--no-browser` or no desktop session; it always prints the
  URL, code and fingerprint.
- Local state lives in the existing `settings` table (keys `account.state`,
  `account.display`, `account.team`, `account.relay_origin`): **no daemon migration**. The
  state is a cache for display; the relay is authoritative.
- A daemon whose relay says `unbound` keeps running (IPC, local data, loopback use) and shows
  "not logged in to <relay>; run agentnet login" in `status`, `doctor` and on every CLI call
  that needs the relay (`request`, `pair`, `team join` …) as the error `account_required`
  with a `hint`.
- Audit: `account.login` (started), `account.bound`, `account.logout`, `account.revoked`, with
  the relay origin and the account **id**, never the email.

## Relay storage (relay migration R2)

| Table | Columns |
|---|---|
| `accounts` | `id` (random `acc_…`), `provider` (`github`/`email`), `subject` (GitHub numeric id, or the normalised email), `display` (`@login` or the email), `state`, `created`, `team_id` NULL |
| `account_keys` | `key` PRIMARY KEY, `account_id`, `device`, `os`, `bound_at`, `last_day` (UTC date only) |
| `bind_requests` | `ref`, `key`, `code_hash`, `device`, `os`, `created`, `expires`, `state`, `account_id` NULL |
| `web_sessions` | `id_hash`, `account_id` NULL, `csrf`, `created`, `expires`, `oauth_state_hash` NULL |
| `billing_teams`, `billing_members` | see [invites.md](invites.md#relay-storage-relay-migration-r3-billing_-tables-are-created-in-r2-with-accounts) |

`subject` is unique per provider. The same person signing in with GitHub and with email gets
two accounts (no linking in the beta).

## Acceptance (summary)

- Plan 4.2: on a relay with `--accounts github`, an unbound key's mail, presence and
  `pair_new` are refused with `account_required`; after the browser flow (a fake OAuth provider
  in tests) the same connection sends mail without reconnecting.
- The code is not in the URL; a wrong code 5 times ends the web session; an expired bind is
  `bind_expired`; the 5th key needs an unbind; `bind_start` from a bound key is `already_bound`.
- A `bind_pending` whose `url` is `file:///…`, `http://…`, another host, or carries control
  characters is not opened and not printed raw.
- Unbind closes the live connection within 1 s; a suspended account's key is closed at `ready`.
- Mail from a bound key to an unbound key gets `account_required`, and no row is queued.
- Web: CSRF token missing → 403; cookie flags asserted; OAuth `state`/PKCE mismatch → error
  page, no session; no access token in the database after login.
- No email or GitHub login appears in any relay log line or daemon audit row.
