# Agent Card (identity)

Status: v1, introduced by ticket 0.3. Implemented in `internal/agentcard`
(sign, verify) and `internal/identity` (key + card lifecycle). An independent
verifier lives in `tools/verifycard` and shares no code with the daemon.

The Agent Card is an A2A-style, self-signed description of one agent: who it is
(name), how peers verify it (Ed25519 public key), what runs it (harness) and
what it says it can do (declared skills). It is exchanged at pairing (ticket
0.5, confirmed by the code MAC from pairing v2, ticket 0.8) and is public data.

## Card

A JSON object. All fields are required; unknown fields are rejected by the
Go parser (but are still covered by the signature, see *Verification*).

| Field | Type | Notes |
|-------|------|-------|
| `version` | integer | Card schema version. `1` |
| `name` | string | Display name, 1-128 characters, no control characters. Default: the machine hostname |
| `public_key` | string | Ed25519 public key (RFC 8032), 32 bytes, base64url without padding (43 characters) |
| `harness` | string | Agent harness in use, 1-128 characters, e.g. `claude-code`, `codex`, `hermes`, `custom` (default) |
| `skills` | array of skill | Declared skills, possibly empty. Order is preserved and signed |
| `created` | string | Creation time, RFC 3339 UTC with `Z` and whole seconds, e.g. `2026-01-02T03:04:05Z` |

Skill object (all fields required, `description` may be `""`):

| Field | Type | Notes |
|-------|------|-------|
| `id` | string | Stable machine identifier, non-empty |
| `name` | string | Human name, non-empty |
| `description` | string | Free text |

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
6. Input must be valid UTF-8 and must not contain duplicate object keys
   (rejected, not "last wins").

## Signature

```
message   = "dorylinae-agent-card-v1\n" || canonical(card)     (ASCII prefix, then bytes)
signature = Ed25519.Sign(private_key, message)
```

The prefix is a domain separator so a card signature can never be replayed as
a signature over another Dorylinae message.

## Verification

1. Parse the envelope (rules 6 above). Take `card` and `signature`.
2. Decode `card.public_key` (must be 32 bytes) and `signature` (must be 64 bytes).
3. Canonicalise the `card` object **as parsed generically**, not through a
   typed struct, so a modified or added field of any name invalidates the
   signature.
4. `Ed25519.Verify(public_key, message, signature)`; a card is self-signed, so
   the key inside the card is the verifying key.
5. Only then check the schema (version 1, field types and limits above).

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
