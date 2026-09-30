# Review 73: R55-F19 security review (webhook and notification fixes)

- Scope: commit 874443d on branch p4/r55-f19 (base eadf189). Tickets R55-026, 074, 075,
  076, 120, 174, 175, 176. Owner decision D51.
- Reviewer: R55-F19sec-Opus. Date 2026-09-30.
- **Verdict: changes needed (minor).** One Medium (M1): the notify.md text on Slack says more
  than the code guarantees. Fixing the wording is enough. Everything else is Low or
  informational and does not block.

## Tests run

- `go test ./internal/notify/`: ok (includes the new tests below)
- `go test ./cmd/agentnet -run Notify`: ok
- `go test ./internal/daemon -run 'Notify|Webhook'`: ok
- `go vet ./internal/notify/`: clean

New file `internal/notify/webhook_f19_sec_test.go` (keep it, or drop it if the author prefers):
- `TestF19SecAdversarialRender`: the peer name and title contain a JSON breakout, the text
  `"queued":1`, `<!channel>`, `@everyone`, `<url|text>`, `[c](javascript:…)`, `ftp://` and
  upper-case `HTTPS://`. The test renders them in all 3 formats, with title on and off. Each
  body must be valid JSON with no top-level key the peer injected and no `queued`. With title
  off, no title or `result_status` may be left in the fields or the text. Slack must have no
  raw `<` or `>` and unfurl off. Discord must have `parse: []` and `flags: 4`. No live `://`
  may remain in the Slack or Discord text. It passes.
- `TestF19SecLegacyUntouched`: a legacy row with no marker is sent byte-identical. It passes.

## Findings

### M1: Slack may still link the host part of a broken URL; notify.md says "not clickable"

- `internal/notify/payload.go:188-190` (`breakURLs`), `Docs/protocol/notify.md` §Payload
  "Bare URLs".
- Scenario: a peer names itself or titles a request `https://evil.com/login`. The Slack text
  becomes `https:​//evil.com/login`. Slack automatically links bare domain names with a
  known TLD, and email addresses (`a@evil.com` becomes `mailto:`). So the `evil.com/login` part
  can still be a live link in Slack, even though the scheme is broken. notify.md says the URL
  "shows as text and is not clickable", which does not hold for Slack. The same sentence also
  says scheme-less domains are not neutralised, so this is the known residual showing up inside
  the URL case too. Discord only links URLs that have a scheme, so Discord is fine. I did not
  test this against live Slack; it is based on Slack's documented autolinking.
- Fix direction (the owner picks one):
  - (a) Wording only: say that in Slack the host part of a URL, like any scheme-less
    domain, may still be linked.
  - (b) Code: in the `slack` text only, also put a U+200B after each `.` that sits between
    two letters or digits, and after `@`. This breaks Slack's domain and email detection and
    leaves the text readable.

### L1: the audit row for an expired delivery does not say why it failed

- `internal/notify/webhook.go:174-177` and `:268-279` (`reportFail`).
- Scenario: a row that expires is audited as `notify.fail {channel, event, id}`. That is the
  same row as an HTTP 4xx without a status or a blocked address, so an operator cannot see
  "expired" in the audit log. Only the `webhook_queue.error` column records it. After long
  downtime, every expired row adds one audit row in a burst (20 per tick). That is no worse
  than before, and there is now no POST.
- Fix direction: add `"error": errStr` to the detail, but only for daemon-owned codes
  (`expired`, `redirect`, `http_NNN`, `bad_webhook`, `no_secret`, `blocked_address`). Do not
  pass `transportError` strings: they can hold the webhook host or IP. This matches R55-174's
  `error` key.

### L2: legacy rows keep their old rendering until they expire

- `internal/notify/payload.go:126-128`.
- Scenario: rows queued before the upgrade are sent exactly as stored. Slack and Discord rows
  therefore keep live `://` and unfurling, and a title that was on when they were queued stays
  in them even if the user has since turned the title off. This lasts at most 24 h, because of
  the new check before the POST. It is the right trade-off: re-escaping would double-escape.
  Mention it in notify.md, or accept it.

### L3: a row that cannot be decoded fails as `bad_webhook`

- `internal/notify/webhook.go:194-198`.
- A corrupt queued body gets the permanent error `bad_webhook`, which points the user at the
  URL. Suggest `bad_body`. This is only diagnostic and has no security effect.

### Info

- **"unknown peer" can be spoofed.** A known peer can pick the card name `unknown peer`
  (`internal/daemon/notify.go:62`). This is cosmetic: peer names are untrusted and cleaned
  anyway. The fix does its real job: a public key no longer reaches the desktop or webhook
  text (`notify.go:78-90`, `:120-127`).
- **Raw peer text still reaches Slack and Discord bodies.** Their bodies still carry the
  generic `peer.name` and `request.title` fields without escaping. Both services ignore
  unknown fields and the fields are not rendered. This is not a change in F19.

## Checked and found sound

- **SSRF.** `DisableKeepAlives: true` (`dial.go:250-254`), and each attempt builds its own
  transport (`webhook.go:212`). Every attempt therefore dials fresh through
  `resolvingDialer`, which resolves and checks each IP and then dials that IP. No connection
  is reused, so no check can be bypassed through reuse, and there is no gap between the DNS
  lookup and the dial in which rebinding could happen. Redirects are still refused
  (`CheckRedirect` returns `ErrUseLastResponse`, and a 3xx is a permanent failure). The proxy
  handling has not changed.
- **JSON shape.** Every body is built with `json.Marshal` from a struct or a map, so peer
  strings stay inside string values. `addFields` overwrites `text`, `content`,
  `unfurl_*`, `allowed_mentions` and `flags` last, with values the daemon owns.
- **Mentions and links.** `slackEscape` runs before `breakURLs`, so `<!channel>` and
  `<url|text>` stay inert. Discord uses markdown escaping, `allowed_mentions.parse=[]` (which
  covers `@everyone`) and `flags=4`. `breakURLs` matches `://` in any scheme and any case.
  `javascript:` and other schemes without `//` are not linked by either service. Unicode
  lookalikes of `:` or `/` do not produce links. No escape rule can remove a character
  between `:` and `//`, because neither escaper touches `:` or `/`.
- **`queued` marker.** It is a top-level int that only `enqueue` sets. Peer text is nested
  inside strings and cannot create a top-level key. `renderBody` zeroes it and `omitempty`
  drops it, so it never goes out (both the new test and `TestWebhookQueuedMarkerNotSent`
  check this). A legacy body never had the key: it was built by `payload` without the field,
  or by `addFields` from that. So a legacy row cannot be mistaken for a new one, and the
  reverse is also impossible.
- **Title off.** The text is `line[ (status)][: title]`. Trimming `": "+title` first and
  then `" (status)"` matches how it was built. The title and status go through the same
  `Clean` output on both sides, and an empty cleaned title trims `": "`. Checked by the new
  test.
- **HMAC and replay.** The signature covers the exact bytes sent (`webhook.go:199-201`). The
  `id` is the row id and does not change between attempts. `ts` is the time of the attempt
  and the body's `ts` is when the event happened. If the settings change between retries,
  the body can differ under the same id. This is documented, and a receiver that dedupes on
  id handles it correctly.
- **24 h expiry.** The check runs before the URL, secret and POST steps, and uses the same
  `> queueMaxAge` test as `finish`, so the two cannot disagree. The row becomes `failed` with
  `expired` through `finish(..., false)`, and it is audited (see L1 on the missing reason).
- **Secrets.** F19 adds no logging. `renderBody` errors are mapped to a fixed code. The audit
  detail has no URL, body or secret, and `transportError` still strips the URL.
- **R55-120.** The event list now comes from `DefaultEvents`, so the CLI and the daemon
  cannot drift apart. The usage text matches.
