# 67: R55-F9 spec: relay error text parsed once, bounded and sanitised; reconnect backoff

Author: R55-F9spec-Opus (Opus, `claude-opus-5-5`), 2026-09-30. Worktree `AgentNet-wt/r55-f9spec`
(branch `p4/r55-f9spec`, base `main` eadf189). Docs only, no code. Status: **draft**. Next:
an adversarial review, then the owner approves the ODs.

Findings covered (review 55, `Docs/review/55-code-review/99-report.md` §4, §4.1):
- **R55-014** (Medium): C04-01, C28-01, T5-01, T1-02, C06-02
- **R55-041** (Low): C04-02 (a spec finding: envelope.md said "resets once authenticated")
- **R55-156** (Info): C04-03 (`e.To` is not checked)
- **R55-157** (Info): C04-04 (`ready.account` is discarded; 4.2c must sanitise `display`)

## Owner summary

**The problem.** The relay is untrusted, but the daemon takes the text of its `error` frames
as they arrive: up to 1 MiB, with any character. That text reaches four places:
- `agentnet status` and `doctor`, through `last_error`;
- `agentnet ping`;
- `agentnet pair` and the `pair.fail` audit row;
- the daemon log.

A hostile or compromised relay can therefore:
- print escape sequences on your terminal: a fake `relay: connected` line, or a clickable
  link to a site it chose;
- print a fake `Paired with alice` after a failed pairing;
- break `status` and make `doctor` report the daemon down (1 MiB of `<` becomes 6 MB of IPC,
  over the 1 MiB line limit);
- erase the daemon's log history with one 1 MiB log line per reconnect.

Separately, the reconnect backoff resets as soon as the relay says `ready`. So a relay that
says `ready` and hangs up at once gets a reconnect about twice a second, forever. Each one
costs a TLS handshake, a signature, an outbox re-send and a presence send.

**The fix.**
1. **Read relay errors once, in one place.** `relayclient` converts every `error` frame before
   anyone sees it:
   - the code must be one of the documented codes, otherwise it becomes `relay_error`;
   - the message becomes one line of at most 200 bytes with no invisible or control
     characters;
   - a malformed `ref` is dropped.
2. **`last_error` holds no relay text.** It says `relay: <code>`, or `closed by relay
   (status N)`, or a sanitised local error of at most 256 bytes. The daemon log line uses
   the same string.
3. **The CLI cleans again** before it prints (`status`, `doctor`, `ping`, `pair`), in case an
   older daemon serves raw text.
4. **Backoff** resets only after a connection stayed up 30 s after `ready`.
5. Envelopes addressed to another key are dropped (acked, not handed up). The 4.2c rule for
   `ready.account` is written down now.

**The sanitiser question (F10).** The report ordered F9 after F10 ("uses the shared
sanitiser"). Since then F5 added `internal/displaytext`, whose `Hidden` predicate is already
the one shared character set (approval.md §Sanitising; `decision.Visible` uses it). F9 needs
only a small **one-line rendering** on top of it, `displaytext.Line`. That is steps 1–3 of
the existing `displayName` (remove hidden runes, collapse spaces, cap stacked marks), without
digit or fingerprint blanking, plus a byte cut. F9 adds it itself, so **F9 no longer waits for
F10.** F10 keeps its scope unchanged:
- replace `termSafe` everywhere (R55-054), which should build on `displaytext.Hidden` too;
- the card-name charset (R55-055);
- the O-100 sweep (R55-056).

Why not the other candidates:
- `termSafe` (`cmd/agentnet/fetch.go:267`) misses the R46 invisible set, which is R55-054
  itself;
- `decision.Visible` escapes every hidden rune as `\u{XXXX}`. That keeps the text exact at
  up to 9× the length, but exactness has no value for a diagnostic line;
- `displayQuote` is JSON-quoted and exact, meant for text a human must check;
- `displayName` blanks digit runs, which would hide `127.0.0.1:8787` in a dial error.

## Spec deltas (this branch)

| File | Change |
|------|--------|
| `Docs/protocol/envelope.md` §`error` frame | New "The daemon's reading": `code` kept only if in the table, else `relay_error`; `message` → `displayLine(message, 200)`; `ref` kept only if a valid envelope id; the message is advisory. |
| `Docs/protocol/envelope.md` §Client behaviour | Backoff resets only after ≥ 30 s up after `ready` (was: on authentication). Misrouted envelope (`to` ≠ own key): not handed up, not in the seen-set, acked, Debug log. New §Relay-supplied text (daemon): a table for every relay string (`error`, `min_client`, `features`, `ready.account` for 4.2c, unexpected `op`, close reason, upgrade headers), the `last_error` rule (`relay: <code>`, or `displayLine(err, 256)`), and the log line uses the same string. |
| `Docs/protocol/approval.md` §Sanitising | New `displayLine(s, max)`: steps 1–3 of `displayName`, no blanking, cut to `max` bytes with `…`, empty stays empty. The heading is unchanged, so anchors survive. |
| `Docs/protocol/pairing.md` §Logging, audit | `pair.fail` `code`/`reason` for relay errors are the converted forms. |
| `Docs/protocol/ipc.md` §`ping_status` | The relay code in a ping failure is the converted one. |
| `Docs/cli/status.md` | `last_error`: not cleared on reconnect (already true, now stated); never relay text; forms `relay: <code>`, `closed by relay (status N)`, else ≤ 256 bytes one line; the CLI re-applies `displayLine`. |
| `Docs/cli/doctor.md` §Output | The `relay` row's `not connected: <last_error>` goes through `displayLine(…, 256)` in human and JSON output; the probe's error text is never shown. |
| `Docs/cli/ping.md` | The failure line's format; the code is known or `relay_error`; the message is at most 200 bytes and sanitised; the CLI re-applies. |
| `Docs/cli/pair.md` | Failure line: code ≤ 64 and message ≤ 200 bytes, relay code known or `relay_error`, the CLI re-applies `displayLine`, `pair.fail` gets the same text; the JSON codes table adds `relay_error`. |

No change to `Docs/cli/relay.md` or `agentnetd.md`. The daemon log format is otherwise
unchanged.

## Implementation plan (Opus; code after the spec is approved)

Order against other tickets: F9 can start now. **F14 comes after F9** (D49; both edit
`relayclient.go`: F14 adds rate limits to the per-frame warnings, while F9 changes `Run`,
`session` and `dispatch`). F10 comes after F9 and reuses `displaytext`.

1. **`internal/displaytext/displaytext.go`**
   - Move steps 1–3 of `Name` into an unexported helper, e.g. `clean(s string) []rune`.
     `Name` then calls it and keeps steps 4–6. `Name`'s output must not change: the existing
     tests guard this.
   - Add `func Line(s string, max int) string`: `clean(s)`, trim, then cut to ≤ `max` bytes
     on a rune boundary and append `…` if anything was cut. It may stop scanning once the
     output is full, or first pre-cut the input to 4 KiB (envelope.md allows either).
2. **`internal/envelope/frames.go`**
   - Add `CodeRelayError = "relay_error"`, commented as "daemon-side only, never sent by a
     relay".
   - Add `func KnownErrorCode(code string) bool` over every `Code*` constant in the table.
     A test must fail if a new `Code*` constant is added without it; keep a slice next to
     the consts.
   - Add `func ValidID(s string) bool`, exporting the `id` rule `Validate` already applies
     (`isIDByte`, 1–128).
   - Change `ErrorFrame.Error()` to `"relay: " + Code`, dropping the message. Its only
     `error` use is the handshake path into `lastErr`.
3. **`internal/relayclient/wire.go`**
   - Add `func errorFrame(c envelope.Control) envelope.ErrorFrame`, the **one** conversion:
     code, `displaytext.Line(msg, 200)`, ref.
   - `readControl` returns `errorFrame(*f.Control)`.
   - An unexpected `op` gives `errors.New("unexpected frame from relay")`, with no `%q` of
     the relay's op.
4. **`internal/relayclient/relayclient.go`**
   - `dispatch`: the `OpError` case calls `OnError(errorFrame(*f.Control))`. After
     `envelope.Parse`, if `e.To != envelope.KeyString(c.pub)`, log at Debug
     (`relay_misrouted`, `type`, `id`), `c.ack`, and return before the seen-set and
     `OnEnvelope`.
   - `Run`:
     - `msg := connError(err)`, set `c.lastErr = msg`, and log `"error", msg` (not `err`)
       in `relay_disconnect`.
     - `connError` maps `envelope.ErrorFrame` → `ef.Error()`, and `websocket.CloseError`
       (`errors.As`) → `fmt.Sprintf("closed by relay (status %d)", ce.Code)`. Everything
       else → `displaytext.Line(err.Error(), 256)`.
   - Backoff: `session` returns `readyAt time.Time` (zero if never ready) instead of
     `authed`. `Run` resets `delay` only when `!readyAt.IsZero() && time.Since(readyAt) >=
     c.stableAfter`.
   - `stableAfter` is an unexported field, default 30 s (`defaultStableAfter`), settable in
     `export_test.go` only. The `relay_disconnect` line keeps `connected_for`.
5. **`internal/session/session.go`, `internal/peers/pairing.go`, `internal/mail/outbox.go`:**
   no logic change. They now receive converted frames. Pairing keeps its 64/200-byte
   `truncate`, which is now a no-op backstop. Outbox ignores `relay_error`, as it ignores
   any unlisted code.
6. **CLI** (each a one-line wrap in `displaytext.Line`):
   - `cmd/agentnet/main.go` status: `res.Relay.LastError` → `Line(…, 256)`;
   - `cmd/agentnet/doctor.go` `relay` check: detail `not connected: ` + `Line(…, 256)`;
   - `cmd/agentnet/ping.go` failure line: `Line(res.Error.Message, 200)` and
     `Line(res.Error.Code, 64)`;
   - `cmd/agentnet/pair.go` failure line: the same.
   Do **not** touch `termSafe` or other print sites (F10).
7. **4.2c note** (no code now): when `ready.account` is read, validate `state` against the
   three constants and store `displaytext.Line(display, 128)`, per the new envelope.md table.

## Acceptance tests

The reviewer tests are in `Docs/review/55-code-review/tests/`, and their `.path` files give
the target locations. Invert them and keep them as normal tests (rename them off the
`zz_review55_` prefix, as earlier tickets did).

1. **C28-01 inverted** (`internal/relayclient`): the relay answers the upgrade with code `x`
   and the OSC 8 + `ESC[2K` + 1 000 000 `<` message.
   - `State().LastError == "relay: relay_error"`: no ESC, ≤ 256 bytes, no `evil.example`.
   - Its JSON encoding (HTML-escaping encoder) is < 2 KiB.
2. **C04-01 inverted**:
   - Code `internal\x1b[31m` gives `LastError == "relay: relay_error"` with no ESC.
   - The same frame with code `internal` gives `"relay: internal"`, with no message text in it.
3. **OnError conversion**: after `ready`, the relay sends an `error` frame with an unknown
   code, a 1 MiB message made of ESC, CR/LF, U+202E, U+200B, U+3164, U+FE0F, U+2800 and 5
   stacked marks, and `ref` = `bad ref!`. The `OnError` frame then has:
   - `Code == "relay_error"`;
   - `Message` with no `displaytext.Hidden` rune, ≤ 203 bytes, and ≤ 2 marks per base;
   - `Ref == ""`.
   A known code with a valid ref passes through unchanged.
4. **`displaytext.Line` table**:
   - ESC/OSC 8, CR/LF and TAB become one space or are removed;
   - bidi, zero-width and the R46 set are removed;
   - invalid UTF-8 becomes U+FFFD;
   - the cut lands on a rune boundary with `…`;
   - `""` gives `""`;
   - `127.0.0.1:8787` and `482913` are kept.
   `Name`'s existing tests pass unchanged.
5. **Ping**: a session test where the relay refuses the `session.init` with an ESC/OSC 8
   message.
   - `PingStatus.Error` holds a clean message and a known or `relay_error` code.
   - A CLI test (fake daemon returning a raw ESC + `\n` message, the older-daemon case):
     `agentnet ping` stderr is one line with no ESC.
6. **Pair**: `Manager.HandleError` with code `x\x1b` and message `\x1b[2K\rPaired with alice`,
   after conversion.
   - `Status.Error` and the `pair.fail` audit detail hold no ESC/CR.
   - A CLI test with a fake daemon returning raw text: stderr is one line starting
     `agentnet: pairing failed:` with no ESC.
   - `pair_lookup_taken` still triggers `reissue`.
7. **Status and doctor CLI**: a fake daemon `status` with `last_error` = ESC + OSC 8 + `\n` +
   300 bytes.
   - Human `status` prints the relay line as one line with no ESC and ≤ 256 bytes of
     error text.
   - `doctor` gives the same for its `relay` row, in human output and in `--json` `detail`.
8. **Log line bounded**: a `slog` capture while the relay sends the 1 MiB error frame. The
   `relay_disconnect` line is < 1 KiB and holds no ESC.
9. **Handshake echo**:
   - A relay that sends `{"op":"<1 MiB of x>"}` instead of `challenge`: `LastError` does not
     contain it and is ≤ 256 bytes.
   - A relay that closes with status 1008 and a reason holding ESC: `LastError == "closed by
     relay (status 1008)"`.
10. **Backoff** (`stableAfter` set to 300 ms and backoff to 10 ms–80 ms in the test):
    - A relay that sends `ready` and closes at once: the gaps between successive dials grow
      and reach the 80 ms cap. There are at most 10 dials in 500 ms, where today's
      code makes about 50.
    - A relay that keeps each connection ≥ 300 ms before closing: the next dial comes after
      about `MinBackoff` (the reset).
11. **Misrouted envelope**: an envelope with `to` = another key is not passed to
    `OnEnvelope`, and an `ack` for it is sent. The same `(from, id)` addressed to us
    afterwards is still handed up (the seen-set was not touched).
12. **Outbox**: an `error` with an unknown code and the row's ref leaves the row `relayed`
    (unchanged behaviour, now via `relay_error`).
13. **Code set completeness**: a test lists every `Code*` constant (by reflection over a
    slice kept next to the consts, or a go/ast scan) and asserts `KnownErrorCode` for each.
    `relay_error` itself is **not** known, since a relay sending it is converted to the
    same value anyway.

## Open decisions

| OD | Question | Recommendation |
|----|----------|----------------|
| OD-R55F9-1 | Unknown relay codes | (a) A fixed set: anything outside envelope.md's table becomes `relay_error`. The outbox and pairing act only on known codes anyway. (b) would keep any `^[a-z][a-z0-9_]{0,31}$`: better diagnostics against a newer relay, but it still lets the relay write words into `pair`/`ping` output and the audit log. |
| OD-R55F9-2 | What `last_error` shows for a relay `error` | (a) `relay: <code>` only (status.md already says "content-free"). (b) would add the sanitised message: more helpful for `auth_failed` detail, but it is relay text on a line the user trusts. |
| OD-R55F9-3 | Sanitiser for F9 | (a) F9 adds `displaytext.Line` (steps 1–3 of `displayName` plus a byte cut) and uses it at its own sites only. F10 later replaces `termSafe` on top of `displaytext`. F9 no longer waits for F10. (b) would wait for F10. |
| OD-R55F9-4 | Blank digit runs and fingerprint-shaped text in relay messages | (a) No: the text sits on one line after a fixed prefix, and blanking would hide addresses and ports in local dial errors. (b) would blank them in the relay message only (two rules for one field). |
| OD-R55F9-5 | Backoff reset rule | (a) Reset only after ≥ 30 s up, measured from `ready` (= `MaxBackoff`), so a ready-and-close relay settles at one reconnect per ~30 s. A sub-30 s connection keeps doubling. |
| OD-R55F9-6 | Envelope with `to` ≠ own key | (a) Drop, **ack** (so it is not redelivered for 7 days) and log at Debug. (b) would drop without an ack; (c) would leave the code as it is (Info, safe today). |
| OD-R55F9-7 | Message bound | (a) 200 bytes, the same as the pairing `reason` (`maxReasonLen`), so every consumer sees one bound. |
| OD-R55F9-8 | Clear `last_error` on a successful connection? | (a) No; keep status.md's meaning, "the most recent error", now stated explicitly. |

## Files changed (docs only)

- `Docs/protocol/envelope.md`
- `Docs/protocol/approval.md`
- `Docs/protocol/pairing.md`
- `Docs/protocol/ipc.md`
- `Docs/cli/status.md`
- `Docs/cli/doctor.md`
- `Docs/cli/ping.md`
- `Docs/cli/pair.md`
- `Docs/review/67-r55-f9-spec.md` (new, this file)
