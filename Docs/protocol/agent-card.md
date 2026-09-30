# Agent Card (identity)

Status: v1, introduced by ticket 0.3; parsing and schema rules made exact by
review 55 ticket R55-F23. Implemented in `internal/agentcard` (sign, verify)
and `internal/identity` (key + card lifecycle). An independent verifier lives
in `tools/verifycard`; it shares no code with the daemon and performs every
step of [Verification](#verification), the schema step included.

The Agent Card is an A2A-style, self-signed description of one agent: who it is
(name), how peers verify it (Ed25519 public key), what runs it (harness) and
what it says it can do (declared skills). It is exchanged at pairing (ticket
0.5, confirmed by the code MAC from pairing v2, ticket 0.8) and is public data.

## Card

A JSON object with **exactly** the six members below and no others. Every
member is required, and any other member makes the card invalid. Extra members
are still covered by the signature, so a verifier checks the signature first and
the member set afterwards (see [Verification](#verification)).

**Member names are matched exactly**, code point for code point, with no case
folding and no Unicode normalisation. A name that some JSON libraries treat as
equal to a listed name is simply another member, and the card is refused. Examples:
`Public_Key`, `public_Key`, `public_\u212Aey` (U+212A KELVIN SIGN in place of
`k`) and `harne\u017F\u017F` (U+017F LATIN SMALL LETTER LONG S in place of
`s`). Go's `encoding/json`, for example, folds all four onto a struct field, and
the last one wins. A verifier that decodes the card that way returns values the
signature did not bind under that name (review 55 R55-019).

| Field | Type | Notes |
|-------|------|-------|
| `version` | integer | Card schema version. `1` |
| `name` | string | Display name, text of 1-128 characters (see below). Default: the machine hostname |
| `public_key` | string | Ed25519 public key (RFC 8032), 32 bytes, strict base64url without padding (43 characters, [Verification](#verification) step 2) |
| `harness` | string | Agent harness in use, text of 1-128 characters, e.g. `claude-code`, `codex`, `hermes`, `custom` (default) |
| `skills` | array of skill | Declared skills, possibly empty. Order is preserved and signed |
| `created` | string | Creation time, RFC 3339 UTC with `Z` and whole seconds, e.g. `2026-01-02T03:04:05Z`: exactly `YYYY-MM-DDThh:mm:ssZ`, a real calendar date, hour 00-23, minute and second 00-59 (no leap second) |

**Text** (every string member except `public_key` and `created`, skill members
included): a count of Unicode code points within the stated range, with no code
point of general category Cc (U+0000-U+001F and U+007F-U+009F) and no U+FFFD
REPLACEMENT CHARACTER. This is what `internal/agentcard` has enforced since
ticket 0.3, so every existing card satisfies it (review 68b).

Skill object: **exactly** these three members, matched by exact name as above.
All three are required; `description` may be `""` but must be present (a
skill without it is refused, review 55 R55-213):

| Field | Type | Notes |
|-------|------|-------|
| `id` | string | Stable machine identifier, text of 1-128 characters |
| `name` | string | Human name, text of 1-128 characters |
| `description` | string | Free text, text of 0-128 characters |

## Signed card (envelope)

```json
{
  "card": { "version": 1, "name": "...", "public_key": "...", "harness": "custom", "skills": [], "created": "..." },
  "signature": "<base64url, no padding, 64 bytes>"
}
```

Only the `card` object is signed. Any other top-level member (for example
`ok` or `key_backend` in `agentnet identity --json`) is unsigned local
metadata and ignored by verifiers.

## Canonical serialisation

The signed bytes come from a deterministic JSON form, a strict subset of
RFC 8785 (JCS):

1. No insignificant whitespace.
2. Object members sorted by key, comparing UTF-16 code units (JCS order).
3. Strings are UTF-8 with only these escapes: `\"` `\\` `\b` `\t` `\n` `\f`
   `\r`; other code points below U+0020 as `\u00xx` (lowercase hex). Every
   other character, including `<`, `>`, `&`, `/`, U+007F and non-ASCII, is
   written literally.
4. Numbers must be integers matching `-?(0|[1-9][0-9]*)` with magnitude below
   2^53, written as-is. Fractions, exponents and `-0` are rejected.
5. `true`, `false`, `null` are literals. Arrays keep their order.
6. Input must be valid UTF-8. (UTF-8-encoded surrogates, the byte
   sequences `ED A0 80` to `ED BF BF`, are not valid UTF-8 and are refused
   by this rule.)
7. **Surrogate escapes must pair.** A `\uXXXX` escape whose value is in
   `D800`-`DFFF` must be a high surrogate (`D800`-`DBFF`) immediately
   followed by a `\uXXXX` escape of a low surrogate (`DC00`-`DFFF`). The
   pair stands for one code point above U+FFFF, which the canonical form
   writes literally as 4 UTF-8 bytes. Any other surrogate escape is refused:
   a lone high, a lone low, a low before a high, or a high followed by
   anything other than a low escape. The escape is never replaced by U+FFFD
   (review 55 R55-153). The rule applies to member names and to values,
   anywhere in the document, including members that a verifier ignores.
   Hex digits in an escape may be upper or lower case.
8. **Duplicate member names are refused**, not "last wins". Names are
   compared after unescaping, as sequences of code points, with no case
   folding or normalisation. So `"a"` and `"\u0061"` are duplicates, while
   `"public_key"` and `"public_Key"` are two different members. The card
   schema refuses the second of those as an extra member ([Card](#card)).

Rules 6-8 are the strict parse. Every document read under these rules obeys
them: card envelopes, and every other signed object whose spec refers to
this section (mail plaintext, announcements, grant tokens, Decisions, pairing
confirms, debate entries). The canonical writer never emits a `\u` escape
except for code points below U+0020, so a canonical form never contains a
surrogate escape.

## Signature

```
message   = "dorylinae-agent-card-v1\n" || canonical(card)     (ASCII prefix, then bytes)
signature = Ed25519.Sign(private_key, message)
```

The prefix is a domain separator so a card signature can never be replayed as
a signature over another Dorylinae message.

## Verification

1. Parse the whole envelope under rules 6-8 of [Canonical
   serialisation](#canonical-serialisation), and apply rule 4 to every
   number in it. A document that breaks one of these rules is malformed,
   even when the offending text sits in a top-level member that step 1 then
   ignores. So `"note":1.5` or `"note":1e400` refuses the envelope, and no
   verifier depends on how its JSON library handles a fraction or an
   out-of-range float (review 68b). The document is RFC 8259 JSON: a leading
   byte order mark (U+FEFF) is not whitespace and makes it malformed. It must
   be an object. Take the members named exactly `card` (an object) and
   `signature` (a string). Ignore any other top-level member.
2. Decode `card.public_key` and `signature` as **strict** base64url:
   - only the alphabet `A-Z a-z 0-9 - _`;
   - no padding and no whitespace;
   - the unused low bits of the last character must be zero, so that every
     byte string has exactly one encoding.

   `public_key` must decode to 32 bytes: 43 characters, the last one of
   `AEIMQUYcgkosw048`. `signature` must decode to 64 bytes: 86 characters,
   the last one of `AQgw` (review 55 R55-154).

   Check the length and the alphabet on the string itself, before decoding.
   Common "strict" decoders are not strict enough: Go's
   `base64.RawURLEncoding.Strict()` still skips CR and LF anywhere in the
   input, so a key or signature with an embedded `\n` or `\r\n` escape would
   decode to the same bytes (vectors N13 and N14, review 68b).
3. Canonicalise the `card` object **as parsed generically**, not through a
   typed struct, so a modified or added member of any name invalidates the
   signature.
4. `Ed25519.Verify(public_key, message, signature)`; a card is self-signed, so
   the key inside the card is the verifying key.
5. Only then check the schema. Check it on the same generic object, reading
   members by exact name:
   - `card` has exactly the six members of [Card](#card);
   - `version` is the integer `1`;
   - `name` and `harness` are text (as defined under [Card](#card)) within
     their limits;
   - `public_key` is the string from step 2;
   - `skills` is an array of objects with exactly the three skill members,
     each a text within its limits;
   - `created` has the exact form and ranges given in the Card table.
6. The verified card is the values read in step 5. An implementation must
   not decode the card again with a parser that folds member names or lets
   the last duplicate win (in Go: no `encoding/json` struct decode of the
   card). A caller that expects a particular key compares it with that
   `public_key`.

**Stored and forwarded form.** A daemon that stores or forwards a peer's card
never keeps the received bytes. This covers the pairing v1 and v2 `peers.card`,
an introduced peer's card from a team roster, and a roster's `members[].card`.
It keeps the canonical form of exactly `{"card": <card>, "signature":
<signature>}` and drops any other top-level member. That form is what pairing
v2 already MACs ([pairing.md](pairing.md#keys-and-tags)); review 55 R55-073
extended it to v1 and to rosters.

A valid signature proves the card was produced by the holder of the private
key; it does not prove the name or harness are true, and it does not prove the
key belongs to the person you meant to pair with. The card itself never gives
trust in a key. Trust comes from a **confirmed pairing**:

- `code`: pairing v2. The code's secret MACs both cards.
- `fingerprint`: a human compared the key fingerprint out of band.

A card received through a v1 pairing (`trust=relay`) is only as trustworthy as
the relay that carried it. A hostile relay can substitute its own key. See
[pairing.md §Storage and trust states](pairing.md#storage-and-trust-states).

## Key storage

The private key is a 32-byte Ed25519 seed, generated with `crypto/rand` on the
daemon's first run. It never appears in any IPC message, CLI output, log line
or audit row.

Order of preference, per config directory:

1. OS keychain via `zalando/go-keyring` (Windows Credential Manager, macOS
   Keychain, Linux Secret Service). Service `dorylinae`, account
   `identity-<id>` where `<id>` is the first 8 bytes (hex) of SHA-256 of the
   config directory path, so separate `DORYLINAE_HOME`s get separate keys.
2. File `<config dir>/identity.key` containing the seed as base64url text.
   Created atomically with owner-only permissions: mode `0600` on Linux and
   macOS (a wider mode is refused on load), and on Windows a protected DACL
   (inheritance disabled) with a single allow entry for the current user.

Setting `DORYLINAE_KEYSTORE=file` skips the keychain (headless servers, CI,
tests). The default is `auto`. The keychain is tried first; if it is
unavailable or errors, the file is used and the reason is recorded in the
audit event. Loading tries the keychain, then the file. A key found only in the
file is not migrated automatically.

The signed card is cached at `<config dir>/agent-card.json`. Lifecycle on
daemon start:

| Key | Card file | Action |
|-----|-----------|--------|
| missing | missing | First run: generate key, create + sign card, audit `identity.create` |
| present | present, valid, key matches | Reuse; no audit event |
| present | missing | Re-create the card for the same key, audit `identity.create` |
| present | invalid or for another key | Daemon fails to start; nothing is overwritten |
| missing | present | Daemon fails to start (key lost; silently rotating the identity would break peers). Delete `agent-card.json` to start a new identity |

Name and harness for a new card come from `DORYLINAE_AGENT_NAME` and
`DORYLINAE_HARNESS` when set at creation time. A card is immutable once
created; rotation and editing are later work.

## Audit

`identity.create` (actor `daemon`), detail:

```json
{"public_key": "<b64url>", "key_backend": "keychain|file", "key_generated": true, "keychain_error": "only when the file fallback was used"}
```

## IPC

Method `identity`, no params. Result:

```json
{"card": {...}, "signature": "...", "key_backend": "keychain"}
```

## Test vector

Ed25519 signatures are deterministic, so this vector is exact. Both
`internal/agentcard` and `tools/verifycard` test against it.

Private key seed (hex): `000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f`

Canonical card (one line, UTF-8):

```
{"created":"2026-01-02T03:04:05Z","harness":"custom","name":"Ada \"test\" <é>","public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","skills":[{"description":"a/b & c","id":"review","name":"Code review"}],"version":1}
```

Signature over `"dorylinae-agent-card-v1\n" || canonical`:

```
XN3GYSED9twF4mei-x7TUzHYzOMQU7aonCRQkebGdcXr8MvkkjLQVjZmtPiCNLTNigKIskMMBqF9hgQW5jdPDA
```

## Negative test vectors (review 55 R55-F23)

All vectors use the key and canonical card of [Test vector](#test-vector)
above, with seed `00…1f`, `public_key` `A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg`
and the signature `XN3G…PDA` given there. `K_R` is the responder key of
[pairing.md §Test vectors](pairing.md#test-vectors), `Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc`.
Each envelope below is one line of UTF-8, exactly as shown. A `\u` sequence in
these envelopes is a JSON escape, written as the six ASCII characters.

"Fails at" is the first [Verification](#verification) step that must refuse
the vector. Where the step is 5, the signature is a real signature over the
canonical form of the card as shown, so step 4 passes. These vectors check
that the schema step, not the signature, refuses them. `internal/agentcard`
(`Verify`, and `ParseStrict` itself for N6-N10), `tools/verifycard` and
`tools/verifyvectors` must all refuse every one of them at that step. They
must also accept P1.

**P1: accepted.** A valid surrogate pair in an ignored top-level member. The
verified card is exactly the card of [Test vector](#test-vector), and `name` is
`Ada "test" <é>`.

```
{"card":<canonical card of the Test vector>,"note":"😀","signature":"XN3GYSED9twF4mei-x7TUzHYzOMQU7aonCRQkebGdcXr8MvkkjLQVjZmtPiCNLTNigKIskMMBqF9hgQW5jdPDA"}
```

**N1: fails at 5. A folded `public_key` (U+212A KELVIN SIGN).** The card
carries `"public_Key"`, where the `K` is U+212A (bytes `E2 84 AA`), with the value `K_R`.
It sorts after `public_key`. Go's `encoding/json` makes it win (review 55
T1-01). This is the acceptance vector of ticket R55-F23.

```
{"card":{"created":"2026-01-02T03:04:05Z","harness":"custom","name":"Ada \"test\" <é>","public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","public_Key":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc","skills":[{"description":"a/b & c","id":"review","name":"Code review"}],"version":1},"signature":"1A0as9bc-SW4UX42sgra2_j58MeJY4-rLEhxIaaEYX-jP67CHE5t55hBrBZvonaGm4e2BMDlalqMkRk-UVzDDw"}
```

**N2: fails at 5. An ASCII-case `public_Key`** (plain `K`, U+004B), value `K_R`,
sorting before `public_key`.

```
{"card":{"created":"2026-01-02T03:04:05Z","harness":"custom","name":"Ada \"test\" <é>","public_Key":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc","public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","skills":[{"description":"a/b & c","id":"review","name":"Code review"}],"version":1},"signature":"Dq2T4Svz3zF-ain_uxsjdDCc90onZdFDUC-y3O7iTYaGIgyV6AaRQZu2sX6VcUo__TiPenhx6m4mqJnfXAKjAA"}
```

**N3: fails at 5. A folded `harness`** (`harneſſ`, each `ſ` U+017F, bytes
`C5 BF`) with the value `spoofed`.

```
{"card":{"created":"2026-01-02T03:04:05Z","harness":"custom","harneſſ":"spoofed","name":"Ada \"test\" <é>","public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","skills":[{"description":"a/b & c","id":"review","name":"Code review"}],"version":1},"signature":"wVEo49U_Ha--ze4SpQVxyzX3DYQz_rwpvQzJKR_8POMA5DtT51IJ7XIeZDrWxJEIDbdfU0GARkPiaqyyFYCaBA"}
```

**N4: fails at 5. A skill without `description`** (review 55 R55-213).

```
{"card":{"created":"2026-01-02T03:04:05Z","harness":"custom","name":"Ada \"test\" <é>","public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","skills":[{"id":"review","name":"Code review"}],"version":1},"signature":"dQi9jlppLavLq5FuPaThp3rW2rtuiEboQ58nTCGy6ShpXZo_8EznS-1ffYcsQWvHWhpTuKkQL_ipIn-GSovbCg"}
```

**N5: fails at 5. A skill with a fourth member `Description`.**

```
{"card":{"created":"2026-01-02T03:04:05Z","harness":"custom","name":"Ada \"test\" <é>","public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","skills":[{"Description":"x","description":"a/b & c","id":"review","name":"Code review"}],"version":1},"signature":"pFDCBOXUlckLSfSo0kZGTL1jdI-Cqx6ptg5lRY_dZ4s04h8DKxZPEpxn8L0NHDZygwvHEcNXnovN8X3Wouq2Cw"}
```

**N6 to N10 and N15: fail at 1.** Each is P1 with the `note` member replaced as
shown. The card and signature are the valid ones from the Test vector, so only
step 1 (rules 7 and 8, and rule 4 over the whole envelope) can refuse them.

| Vector | Replacement for `"note":"😀"` | Rule |
|---|---|---|
| N6 | `"note":"\ud800"` (lone high) | 7 |
| N7 | `"note":"\udc00"` (lone low) | 7 |
| N8 | `"note":"\udc00\ud800"` (low before high) | 7 |
| N9 | `"note":"\ud800\u0041"` (high, then a non-surrogate escape) | 7 |
| N10 | `"note":1,"note":2` (the same name after unescaping) | 8 |
| N15 | `"note":1.5` (a fraction in an ignored member) | 4, applied at step 1 |

**N11: fails at 2. `public_key` with non-zero trailing bits.** The key ends in
`h` instead of `g`. It decodes to the same 32 bytes under a lax decoder, and
the signature is over this exact string, so a verifier with lax base64
accepts it.

```
{"card":{"created":"2026-01-02T03:04:05Z","harness":"custom","name":"Ada \"test\" <é>","public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbh","skills":[{"description":"a/b & c","id":"review","name":"Code review"}],"version":1},"signature":"EXtkmE-d0-8pMsBH2sNOqKQsJTk23NYYv3CWJPiNuZ_fHuICp1Ba2Az2JWuFwIctj-jq8PIVkF5PL5gRtWKtDw"}
```

**N12: fails at 2. A signature with non-zero trailing bits.** This is the Test
vector with the last signature character `A` changed to `B`. It decodes to the
same 64 bytes under a lax decoder.

```
{"card":<canonical card of the Test vector>,"signature":"XN3GYSED9twF4mei-x7TUzHYzOMQU7aonCRQkebGdcXr8MvkkjLQVjZmtPiCNLTNigKIskMMBqF9hgQW5jdPDB"}
```

**N13: fails at 2. `public_key` with an embedded line feed** (the JSON escape
`\n` after the 22nd character), signed by the seed over the canonical form of
this card. Go's `RawURLEncoding.Strict()` skips the LF and decodes the same 32
bytes, and the signature verifies, so a verifier that relies on that decoder
accepts it (checked against `internal/agentcard.Verify` at `eadf189`, review 68b).

```
{"card":{"created":"2026-01-02T03:04:05Z","harness":"custom","name":"Ada \"test\" <é>","public_key":"A6EHv_POEL4dcN0Y50vAmW\nfk1jCbpQ1fHdyGZBJVMbg","skills":[{"description":"a/b & c","id":"review","name":"Code review"}],"version":1},"signature":"roYk-mjPoBVSZUqjc3zC9vHCnr4bccxLkBVVf_i0PnDumzwvysHWbqGCSeULqcNQxqN2D2uDgm5SxfXDXMt7DA"}
```

**N14: fails at 2. A signature with an embedded CR LF** (the escapes `\r\n`
after the 43rd character). It is the Test vector signature otherwise, so a
decoder that skips CR and LF gives the same 64 bytes.

```
{"card":<canonical card of the Test vector>,"signature":"XN3GYSED9twF4mei-x7TUzHYzOMQU7aonCRQkebGdcX\r\nr8MvkkjLQVjZmtPiCNLTNigKIskMMBqF9hgQW5jdPDA"}
```

`<canonical card of the Test vector>` in P1, N12 and N14 stands for the canonical
card line of [Test vector](#test-vector), inserted verbatim. `tools/verifyvectors/vectors.json` carries
every envelope in full, under `agent_card.cases` as `{name, envelope, fails_at}` (0 for P1).
