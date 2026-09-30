# 67: R55-F9 spec: relay error text parsed once, bounded and sanitised; reconnect backoff

Author: R55-F9spec-Opus (Opus, `claude-opus-5-5`), 2026-09-30. Worktree `AgentNet-wt/r55-f9spec`
(branch `p4/r55-f9spec`, base `main` eadf189). Docs only, no code. Status: **adversarial review done** (review 67b, at the end; fixes made
in place). Next: the owner approves the ODs.

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
2. **`last_error` holds no relay message, close reason or redirect target.** It says `relay: <code>`, or `closed by relay
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
   - Add `func Line(s string, max int) string`: `clean(s)`, trim. If the result exceeds
     `max` bytes, cut on a rune boundary to ≤ `max − 3` bytes, trim trailing spaces and
     append `…`, so the output is **≤ `max` bytes including the `…`** (review 67b F9R-1).
     It may stop scanning once the output is full, or first pre-cut the input to 4 KiB
     (envelope.md allows either), but an unread rest counts as a cut.
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
   - `dispatch`: the `OpError` case calls `OnError(errorFrame(*f.Control))`. Right after
     `envelope.Parse` and **before the ephemeral branch**, if `e.To !=
     envelope.KeyString(c.pub)`, log at Debug (`relay_misrouted`, `type`, `id`). Ack it only
     if `!envelope.IsEphemeral(e.Type)`, then return before the seen-set and `OnEnvelope`
     (review 67b F9R-6).
   - `newHTTPClient`: `CheckRedirect` returns a package sentinel `errRedirect` (review 67b
     F9R-2).
   - `Run`:
     - `msg := connError(err)`, set `c.lastErr = msg`, and log `"error", msg` (not `err`)
       in `relay_disconnect`.
     - `connError` maps, in this order:
       - `envelope.ErrorFrame` → `ef.Error()`;
       - `websocket.CloseError` (`errors.As`) → `fmt.Sprintf("closed by relay (status %d)",
         ce.Code)`;
       - `errors.Is(err, errRedirect)` → `"dial: the relay answered with a redirect (not
         followed)"`: the `*url.Error` would otherwise quote the relay's `Location`;
       - everything else → `displaytext.Line(err.Error(), 256)`.
   - Backoff: `session` returns `readyAt time.Time` (zero if never ready) instead of
     `authed`. `Run` resets `delay` only when `!readyAt.IsZero() && time.Since(readyAt) >=
     c.stableAfter` **and** the error is not a `websocket.CloseError` with code 1013. On a
     1013 close, `delay = max(delay, c.tryAgainFloor)` before the jittered wait (default
     5 s; review 67b F9R-4).
   - `stableAfter` and `tryAgainFloor` are unexported `Config` fields, like `dialContext`
     (defaults 30 s and 5 s), set by a `WithBackoffTiming` helper in `export_test.go` only.
     The `relay_disconnect` line keeps `connected_for`.
5. **`internal/peers/pairing.go`**: `finish` is the one choke point for every `Failure`:
   `Code = displaytext.Line(code, 64)` and `Message = displaytext.Line(msg, 200)` before the
   status, `pair_status` and the `pair.fail` audit row. `truncate` is then no longer needed
   for failures (review 67b F9R-3). `HandleError` keeps its `pair_lookup_taken` retry on
   the converted code.
   **`internal/session/session.go`, `internal/mail/outbox.go`:** no logic change. They now
   receive converted frames. The outbox ignores `relay_error`, as it ignores any unlisted
   code.
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
   - `Message` with no `displaytext.Hidden` rune, ≤ 200 bytes (the `…` included), and ≤ 2
     marks per base;
   - `Ref == ""`.
   A known code with a valid ref passes through unchanged.
4. **`displaytext.Line` table**:
   - ESC/OSC 8, CR/LF and TAB become one space or are removed;
   - bidi, zero-width and the R46 set are removed;
   - invalid UTF-8 becomes U+FFFD;
   - the cut lands on a rune boundary with `…`, and `len(Line(s, n)) <= n` for every row and
     for n in {4, 64, 200, 256}, including a cut through a 4-byte rune and a cut after a
     space (no `" …"`);
   - an input of 5 KiB of U+200B followed by `abc`: the result ends in `…` if the
     implementation pre-cuts at 4 KiB, or is `abc` if it scans on; never `""` without `…`;
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
10. **Backoff** (`WithBackoffTiming`: `stableAfter` 300 ms, `tryAgainFloor` 50 ms; backoff
    10 ms–80 ms). The assertions are on the gaps, not on a dial count, because jitter
    (75–125 %) makes counts in a fixed window flaky (review 67b F9R-5):
    - A relay that sends `ready` and closes at once, observed for 8 dials: from the 5th gap
      on, every gap between dials is ≥ 55 ms (0.75 × 80 ms, minus slack). Today's code gives
      every gap near 10 ms (the reset on each `ready`), far below 55 ms, so the test fails
      on it.
    - A relay that keeps each connection ≥ 350 ms before closing (normal close 1000): the
      gap after each close is < 40 ms (the reset to 10 ms).
    - The same ≥ 350 ms relay closing with status **1013** instead: the gap after each close
      is ≥ 35 ms (0.75 × the 50 ms floor, minus slack), and `LastError == "closed by relay
      (status 1013)"`.
11. **Misrouted envelope**: an envelope with `to` = another key is not passed to
    `OnEnvelope`, and an `ack` for it is sent. The same `(from, id)` addressed to us
    afterwards is still handed up (the seen-set was not touched). A misrouted `presence`
    envelope is not handed up and **no** ack is sent.
12. **Outbox**: an `error` with an unknown code and the row's ref leaves the row `relayed`
    (unchanged behaviour, now via `relay_error`).
13. **Code set completeness**: a test lists every `Code*` constant (by reflection over a
    slice kept next to the consts, or a go/ast scan) and asserts `KnownErrorCode` for each.
    `relay_error` itself is **not** known, since a relay sending it is converted to the
    same value anyway.
14. **Redirect**: a relay that answers the upgrade with `302 Location:
    https://evil.example/fix-now`. `LastError == "dial: the relay answered with a redirect
    (not followed)"` and the `relay_disconnect` log line does not contain `evil.example`.
15. **Pairing choke point**: a `pair_peer` whose card fails verification with a reason that
    would carry U+202E / U+3164 (a key name through the card verifier), and a local
    failure with an over-long message. `Status.Error.Message` and the `pair.fail` `reason`
    hold no `displaytext.Hidden` rune and are ≤ 200 bytes. `Status.Error.Code` is ≤ 64
    bytes.

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

## Review 67b (adversarial)

Reviewer: R55-F9rev-Opus (Opus, `claude-opus-5-5`), 2026-09-30. Read: this spec and every
spec edit of commit 9f48ba8; 99-report R55-014/041/156/157, the F9 row of §4 and §4.1; chunks
C04, C28, C06 and themes T5, T1; the F1 spec (56) and F5 spec (58); HANDOFF §3 (D46–D50).
Code checked: `internal/relayclient/{relayclient,wire}.go`, `internal/envelope/frames.go`,
`internal/displaytext/displaytext.go`, `internal/peers/pairing.go`, `internal/session/session.go`,
`internal/mail/outbox.go`, `internal/daemon/daemon.go` (callbacks),
`cmd/agentnet/{main,doctor,ping,pair}.go`, and coder/websocket v1.8.15 `dial.go` (its error
texts). Attacker: a hostile or compromised relay. Every fix below is made in place.

**Verdict.** The design holds: one conversion in `relayclient`, a fixed code set, a message
bound, CLI re-sanitising and a stable-time backoff close R55-014, 041, 156 and 157. The known
code set matches every code the relay sends (all `Code*` constants of `frames.go`, which equal
envelope.md's table), so no current relay loses a code. I found no Critical or High. Two
Mediums (a relay URL in `last_error`; unsanitised pairing reasons) and one Medium on backoff
(a synchronized return after load shedding) are fixed. With these changes I recommend
approval.

| ID | Sev | Finding | Change made |
|----|-----|---------|-------------|
| F9R-1 | M | **The bound excluded the `…`.** `displayLine` cut to `max` bytes and then appended `…`, so outputs reached 203 / 259 bytes (test 3 even said "≤ 203"). That contradicted "at most 200 / 256 bytes" in status.md, ping.md and pair.md, and pairing's `truncate(200)` was then not a no-op: it would cut exactly the `…` off. Also unstated: whether an early stop at 4 KiB counts as a cut, and a trailing `" …"`. | approval.md, envelope.md, status.md and the plan now say: **output ≤ `max` bytes, `…` included** (cut to `max − 3`, re-trim, append). An unread rest counts as a cut; `max` ≥ 4. Test 3 is now ≤ 200; test 4 checks `len(Line(s,n)) <= n`, a 4-byte rune at the cut, no `" …"`, and the 4 KiB pre-cut case. |
| F9R-2 | M | **Relay-chosen text still reaches `last_error` and the log through local errors.** `CheckRedirect`'s error comes back as a `*url.Error` that quotes the **redirect target**: a relay can put `https://evil.example/reinstall-now` on the `status` / `doctor` line (the same harm OD-2 removes for the message). coder/websocket also quotes upgrade header values (`Connection`, `Upgrade`, `Sec-WebSocket-Accept`, `Sec-WebSocket-Protocol`, `%q`), and x509 errors carry certificate names. status.md's "never carries relay-chosen text" was false. | Redirect → sentinel `errRedirect` → fixed text `dial: the relay answered with a redirect (not followed)` (envelope.md table and `last_error` rule, plan step 4, new test 14). Header values and certificate names stay: they are quoted, one line and ≤ 256 bytes, but may hold relay-chosen words. This residual is recorded in envelope.md, and status.md now claims only what holds (no relay message, close reason or redirect target). |
| F9R-3 | M | **Pairing reasons other than relay errors were not covered.** C06-02 (in R55-014) also names the `bad_card`/`bad_mbox` reasons. These embed verifier text that quotes key names from a relay- or peer-supplied card (`canonical.go` `%q`). `%q` escapes Cf/controls but not U+3164 or the other R46 fillers, and these reasons go to `Status.Error`, `pair_status` and the append-only `pair.fail` row unsanitised (the CLI re-sanitises only the terminal). The spec only converted relay `error` frames. | pairing.md: `finish` is the one choke point: `code` → `displayLine(…, 64)`, `reason`/`message` → `displayLine(…, 200)` for **every** failure. pair.md now points to it. Plan step 5 rewritten; new test 15. |
| F9R-4 | M | **Backoff: a synchronized return after load shedding.** F1 (spec 56) makes 1013 the relay's load-shedding close ("relay busy", "frame too slow", `relay_full`, `rate_limited`). Under the new rule, a connection up ≥ 30 s that is closed 1013 still resets to 500 ms. So every shed daemon comes back within 375–625 ms, into a relay that just said it is overloaded. That is a reconnect storm against a recovering relay. | envelope.md: a close with status 1013 never resets the backoff, and the next wait is ≥ `jitter(5 s)`, then doubling. Plan step 4 (`tryAgainFloor`, default 5 s); test 10 adds the 1013 case. New OD-R55F9-9. |
| F9R-5 | L | **Flaky backoff test.** "At most 10 dials in 500 ms" with 10–80 ms and 75–125 % jitter: the low-jitter path gives 11 dials (0, 7.5, 22.5, 52.5, 112.5, then every 60 ms). | Test 10 asserts on gaps (≥ 55 ms from the 5th gap; < 40 ms after a stable normal close; ≥ 35 ms after a stable 1013), not on counts. |
| F9R-6 | L | **Misrouted check vs ephemeral envelopes.** "After Parse … `c.ack`" was ambiguous. Placed after the ephemeral branch, it would skip presence (the one type C04 marked **unchecked** for recipient binding). Placed before, it would ack presence, which is never acked. It also adds a per-frame, relay-driven log line not named under F14 (D49). | envelope.md and plan: the check comes before the ephemeral branch and covers every type; an ephemeral is dropped **without** ack; `relay_misrouted` falls under F14's per-frame rate limit. Test 11 adds the presence case. The "every consumer binds…" sentence now names presence (through the mail opener). |
| F9R-7 | L | **The relay-text table was incomplete** for "every string the relay chooses": it had no `queued.ref`, no `pair_code`/`pair_peer`, and no envelope header fields. | Rows added, each pointing to its rule (lookup-only; pairing.md plus the choke point, the card name being F10/R55-055; `Validate`). |
| F9R-8 | L | **Relay message still shown on `ping` / `pair`.** Sanitised and one line, but a relay can still write "pairing failed: upgrade at https://evil.example (relay_error)", and terminals auto-link bare URLs. This is the social-engineering residual OD-2 removed from `last_error`. | Not changed: raised as OD-R55F9-10. |
| F9R-9 | I | **Mixed versions.** A new CLI with an older daemon still fails `status` on a 1 MiB `last_error` (the IPC 1 MiB line cap is hit before the CLI can sanitise). `ping`/`pair --json` pass the daemon's values through: JSON escapes C0 but not bidi. | No change: both are fixed by upgrading the daemon (the CLI and daemon ship together). Noted here only. |
| F9R-10 | I | Contradiction checks. **F1:** consistent (1013 semantics used by F9R-4). **F5:** `displayLine` is built on `displaytext.Hidden`, and `Name`'s output stays unchanged. **F14/D49:** F14 comes after F9 and rate-limits per-frame lines, now including `relay_misrouted`; D49's audit rule is untouched. **F10:** unchanged scope. The known code set equals `frames.go`, so no older/newer relay today sends a code that would be lost. | None. |

### Final open decisions (for the owner)

| OD | Question | Options | Recommended |
|----|----------|---------|-------------|
| OD-R55F9-1 | Unknown relay error codes | (a) fixed set, anything else → `relay_error`; (b) keep any `^[a-z][a-z0-9_]{0,31}$` | **(a)** |
| OD-R55F9-2 | `last_error` for a relay `error` frame | (a) `relay: <code>` only; (b) add the sanitised message | **(a)** |
| OD-R55F9-3 | Sanitiser for F9 | (a) F9 adds `displaytext.Line` now, F10 builds on it later; (b) wait for F10 | **(a)** |
| OD-R55F9-4 | Blank digit runs / fingerprint-shaped text in relay messages | (a) no; (b) blank in relay messages only | **(a)** |
| OD-R55F9-5 | Backoff reset rule | (a) reset only after ≥ 30 s up, counted from `ready`; (b) reset on `ready` (today) | **(a)** |
| OD-R55F9-6 | Envelope with `to` ≠ own key | (a) drop, ack queued types (not ephemeral), Debug log; (b) drop, never ack; (c) leave as is | **(a)** |
| OD-R55F9-7 | Relay message bound | (a) 200 bytes, `…` included (= pairing `maxReasonLen`); (b) another bound | **(a)** |
| OD-R55F9-8 | Clear `last_error` on a successful connection | (a) no, "most recent error" as documented; (b) clear on `ready` | **(a)** |
| OD-R55F9-9 (new) | Close 1013 (Try Again Later) and the backoff | (a) never reset on 1013, next wait ≥ `jitter(5 s)`; (b) no special case (a long connection shed with 1013 returns in ~0.5 s); (c) honour a relay-sent retry hint (none exists in the protocol) | **(a)** |
| OD-R55F9-10 (new) | Relay message on `ping` / `pair` output | (a) show the sanitised relay message (spec as written); (b) show a daemon-owned text per known code and a fixed `the relay refused the request` for `relay_error`, keeping the relay message only in the `pair.fail` audit `reason` and at Debug | **(a)** for F9 (it meets R55-014's fix direction, and the message is the only diagnostic for `relay_error`); (b) is the stricter follow-up if the owner wants no relay words on screen at all |

### Files changed by review 67b

- `Docs/protocol/approval.md` (`displayLine` bound includes the `…`)
- `Docs/protocol/envelope.md` (message bound; 1013 backoff rule; misrouted check order and ephemeral; relay-text table rows; `last_error` redirect and residual)
- `Docs/protocol/pairing.md` (pairing failure choke point)
- `Docs/cli/status.md` (accurate `last_error` claim)
- `Docs/cli/pair.md` (points to the choke point)
- `Docs/review/67-r55-f9-spec.md` (status line, owner summary item 2, plan steps 1/4/5, tests 3/4/10/11, new tests 14/15, this section)
