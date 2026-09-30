# 76: R55-F23 security review (signed peer input)

Reviewer: R55-F23sec-Opus · model `claude-opus-5-5` · 2026-09-30 · worktree
`AgentNet-wt/r55-f23`, branch `p4/r55-f23`, commit `eb25c07` on `main` `f2e3415` (diff read with
`git diff f2e3415..HEAD`; read-only git only). Spec: [68](68-r55-f23-spec.md) with review 68b
(D55: ODs 1–6 (a)). Severity per [55-code-review/01-rubric.md](55-code-review/01-rubric.md).

**Verdict: approve.** No finding blocks the merge. Every card and signed-body consumer in scope
now reads on the strict path, the verifiers agree with each other, and the stored-card migration
cannot fail the start, stall it or change trust. There is one Low: a member left out of the
final dissolved roster applies it as `removed`, not `dissolved`, and team.md does not say this
can happen. No peer can use the omission against another member. Three Infos follow.

## What I checked

### 1. Card verification (`internal/agentcard`)
- **No second decode.** `Verify` reads the schema from the generic object that the signature
  covered (`cardFromGeneric`): exact names, `len == 6`, `len == 3` per skill, `version` as
  `json.Number` `"1"`, and every member type-asserted. The struct decode is gone.
  `Signed.Card` holds the values read there, so pairing (`pairing.go:642,690`), rosters
  (`kinds.go:365`), `memberEntry` (`store.go` `forwardCard`) and `identity.readCard` all get
  exact-name values.
- **Whole envelope.** `CanonicalValue(doc)` runs over the whole envelope at step 1
  (`agentcard.go:279`), so rule 4 covers ignored members too (N15).
- **Base64** (`decodeStrict`, `agentcard.go:175`):
  - the exact length (43/86) is checked before decoding, then the alphabet byte by byte, so CR,
    LF, `=`, `+`, `/` and non-ASCII are all refused;
  - the unused low bits of the last character must be zero. `1<<unused-1` parses as
    `(1<<unused)-1` in Go, so the masks are 3 and 15;
  - `validate()` uses the same helper for `public_key`.
- **Surrogates** (`checkSurrogates`, `canonical.go:58`):
  - it tracks string state, so `\\ud800` is accepted and `\\\ud800` is refused;
  - a low half on its own, a high half followed by anything but a `\u` low escape, and a
    truncated pair are all refused, in names and values alike;
  - a raw encoded surrogate (`ED A0 80`) is already refused by `utf8.Valid`;
  - invalid hex is left to the decoder, which refuses it.
- **Other parse rules.** Duplicate keys are compared after unescaping, with no folding (the
  decoded Go strings are compared). A BOM makes `json.Decoder` refuse the input, as does
  trailing data.
- **No panics.** I found no panic path on hostile input: every type assertion uses the
  two-value form, and every index is bounds-checked.

### 2. Vectors and verifier independence
- `vectors.json` holds 16 cases, which match agent-card.md. `go run ./tools/verifyvectors`
  reports `all vectors reproduced`, and each N case fails at its stated step.
- The targeted tests pass: `internal/agentcard`, `internal/peers`, `internal/team`,
  `internal/request`, `tools/...`, `cmd/agentnet -run 'Artifact|Doctor'` and
  `internal/daemon -run 'Card|Strict|DeviceAt|MailSubmit'`.
- **Differential fuzz** (reviewer probe, deleted after the run; see I1):
  - Setup: 1–3 random insert, delete or replace mutations of each of the 16 envelopes, 20,000
    per case. The fragments included `\ud800`, `\udc00`, a BOM, `ſ`, U+212A, `1e400`, `1.5`,
    `-0`, 2^53, CR LF, `=`, `+` and `/`.
  - Result: `agentcard.Verify` against verifycard `verify`, and against verifyvectors
    `verifyCardEnvelope`, gave **0 differences** in accept/refuse and in the returned name
    (2 × 320,016 inputs).

### 3. Stored-card migration (`internal/peers/cardcheck.go`, `daemon.go:319-335`)
- **Crash or stall.**
  - `peers.card` is `NOT NULL CHECK (json_valid(card))`, so every row is JSON, and SQLite's
    json_valid bounds its depth.
  - `RescueStored` has no panic path and does linear work per row.
  - A verification failure goes into `bad`. Only a scan, close or UPDATE error fails the open,
    and a test covers it (`TestDaemonOpensWithBadStoredCard`).
  - The cursor is closed before the UPDATEs, so it does not deadlock even with one
    connection.
- **Race.** The migration runs before `peers.NewManager` (`daemon.go:354`) and before
  `srv.Serve`, so no pairing runs at the same time. The UPDATE is also a compare-and-set on the
  old bytes (`cardcheck.go:61`), so a concurrent writer would win.
- **Trust.** Only the `card` column is written. Trust, name, skills, mailbox keys and
  `introduced_by` are never touched, and a failing row is neither deleted nor downgraded.
- **Rescued rows.** A rescued row's name, harness and skills columns match the rescued card.
  A row whose card relies on a folded or duplicate member fails the new exact-name `Verify`
  and is reported, not rescued, so a planted R55-019 card cannot be "rescued" under a
  different name.
- **Writers after the migration.** Every writer of `peers.card` now stores bytes that pass the
  new `Verify`:
  - `AddTrusted` stores canonical v1 or v2 cards (`pairing.go:655,698`);
  - `Introduce` stores the canonical `cardRaw` from `parseRosterMember`.

  So no remote party can later turn a stored row into a failing one, block the owner's
  rosters (`errStoredCard`) or trigger the dissolved omission. The only failing rows are
  pre-F23 rows.
- **Doctor.** `checkPeers` opens the database read-only and uses the same `RescueStored`, so
  it reports exactly the rows the migration leaves (and does not flag rows the daemon would
  rescue on its next start).

### 4. Smaller peer and local inputs
- **Request.** A present `""` is refused for `urgency_declared`, `urgency_reason`,
  `requested_grant.note` and every artifact member (`decode.go:141,200,315`). `WireBody` and
  `artifactsWire` never emit `""`, so honest senders are unaffected. `mirror.go`'s
  `decodeNonEmpty` already refused `""`, and debate artifacts share `decodeArtifactsAt`.
- **Device `at`.** `parseWireTimeStrict` (`device.go:101`) checks that the time round-trips, so
  `+00:00`, `.5Z`, `z` and `-00:00` are refused. It is used for `device.link` (as `bad_body`)
  and `device.unlink`. Kept offers are the daemon's own marshal of `offerBody`. An old kept
  offer with a non-wire `at` now fails `offer()` and is ignored, not trusted.
- **`mail_submit`.** It uses `ParseStrict(p.Body)`, then the object check, then
  `CanonicalValue` (`outbox.go:96`). `json.RawMessage` keeps the raw bytes, so duplicates and
  lone surrogates reach the strict parse unchanged.
- **CLI `--artifact`.** One `request.ParseArtifactSpec` serves `request`, `inbox complete` and
  `session`. Keys are exact and never repeated, values are non-empty, JSON is parsed strictly,
  and at least one key is required. Errors are prefixed and exit 2 (`exitUsage`).

## Findings

| # | Severity | Finding |
|---|---|---|
| L1 | Low | Member left out of a dissolved roster applies it as `removed`; team.md does not say this can happen |
| I1 | Info | The three verifiers share no repo code but do share `encoding/json`'s tokenizer |
| I2 | Info | Some decodes into `encoding/json` structs remain after verification, on stored canonical data |
| I3 | Info | A future tightening of card rules (R55-F10) will make stored rows fail and block rosters |

### L1 (Low): member left out of a dissolved roster applies it as `removed`
- **Where.**
  - `internal/team/store.go:612`: `rosterBody` skips a non-self member whose stored card fails,
    only when `t.State == StateDissolved`.
  - `store.go:575`: the recipients were collected before that, so the skipped member is still
    sent the roster.
  - `internal/team/kinds.go:148-156`: `!selfIn` gives `removed`, whatever the wire state.
- **Scenario.**
  1. The owner holds a pre-F23 row for member B whose card no longer verifies, for example a
     planted N1-style folded card.
  2. The owner runs `agentnet team delete`.
  3. B receives a final roster with `state: dissolved` that does not list B. B stores its local
     state as `removed`, not `dissolved`.
  4. The other members' final membership lacks B, while the owner's `team_members` still has
     B.
- **Proof.** The reviewer probe `internal/team/zz_sec76_dissolved_test.go`
  (`TestSec76OmittedMemberSeesRemoved`) logs `"removed"`.
- **Against team.md.**
  - §Operations Delete says "roster (with the final member list) sent to all members; locally
    `dissolved`". The final list sent differs from the owner's list, and team.md does not
    mention the omission.
  - The `0–32 entries` rule for `dissolved` does make the roster itself valid.
- **Impact.** The impact is cosmetic or state-only. `removed` and `dissolved` are both
  inactive, re-entry needs a new invite either way, and GC runs the same way.
- **Not abusable.** A peer cannot use this to drop someone else. The omission needs the
  target's own row at the owner to fail verification, and after this ticket every writer of
  `peers.card` stores cards that pass the strict `Verify` (§3). Only pre-F23 rows can fail,
  and only a planted card makes the owner's own peer row fail. Active rosters are not affected;
  they are refused locally, which is OD-3 as intended.
- **Fix direction** (owner's choice). Either:
  - document it in team.md Delete: "a member whose stored card no longer verifies is left out
    of the final list; it applies the roster as `removed`"; or
  - drop that member from `recipients` too, so that it never receives a roster that
    misdescribes its state. Its team then stays `active` locally until it is removed some
    other way, which is arguably worse.

  I recommend documenting it.

### I1 (Info): the verifiers share `encoding/json`'s tokenizer
- **What is independent.** `tools/verifycard` imports only the standard library, and
  `tools/verifyvectors` only the standard library and `golang.org/x/crypto`. Neither imports
  `internal/`. The surrogate scans, base64 checks, canonicalisers and schema checks were each
  rewritten, not copied. The code is structurally similar but not shared.
- **What is shared.** All three verifiers (`agentcard`, verifycard, verifyvectors) tokenize
  with `json.Decoder.Token` and `UseNumber`. They also share the standard library's base64
  decoder (after their own checks) and `crypto/ed25519`. A tokenizer quirk would therefore
  affect all three at once.
- **Mitigation.** Review 68b's pure-Python checker covered this for the published vectors, and
  the fuzz in §2 found no difference between the three.
- **Fix direction.** None needed now. If an independent verifier is required later, a
  non-Go one (for example the 68b Python checker kept in `tools/`) would remove the
  common-mode risk.

### I2 (Info): decodes into `encoding/json` structs remain after verification, on stored canonical data
- **Where.** The new `ParseStrict` code rule ("never decode data that passed ParseStrict with
  encoding/json into a struct") still has exceptions:
  - `internal/capability/fetch.go:568` re-reads `sig` from a grant token that `Verify` has
    just accepted;
  - `debate/constraint.go:304`, `debate/view.go:146,180` and `debate/apply.go:731`;
  - `worksession/receive.go:262`;
  - `request/cancel.go:388`.
- **Why they are safe today.**
  - `capability.parseToken` requires exactly `{grant, sig}` under the strict parse, so no
    folded twin can exist.
  - The others read canonical bodies that were schema-checked by exact name before they were
    stored.
- **Fix direction.** Add them to the R55-F31/F34 sweep that OD-1 names. Convert them to
  exact-name generic reads, or add a comment on each saying why it is safe.

### I3 (Info): a future tightening of card rules will make stored rows fail and block rosters
- `forwardCard` re-runs the full `Verify` on every roster build. If R55-F10 (charset) tightens
  step 5, honest stored cards that break the new rule will fail. Owners will then find roster
  updates blocked (`errStoredCard`) until they remove the member or re-pair.
- `MigrateCards` runs at every open, so doctor and the log will report such rows.
- **Fix direction.** R55-F10's spec should either keep stored-card checks at the rules the card
  was paired under, or plan the same kind of rescue as OD-3.

## Files created by this review
- `Docs/review/76-r55-f23-security.md` (this file).
- `internal/team/zz_sec76_dissolved_test.go`: probe for L1. It passes and logs the state.
  Delete it or keep it as a regression test, as the owner prefers.
- `tools/verifyvectors/zz_sec76_diff_test.go` and `tools/verifycard/zz_sec76_diff_test.go`:
  differential fuzz probes, run once and then deleted (single files, no directories).

No product code was changed, and no git write commands were run.
