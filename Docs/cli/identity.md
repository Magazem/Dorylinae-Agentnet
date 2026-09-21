# `agentnet identity`

Prints this agent's signed Agent Card, fetched from the daemon over local IPC.
The card format and signing rules are in
[../protocol/agent-card.md](../protocol/agent-card.md). The private key is never
sent to the CLI.

```
agentnet identity [--json]
```

| Flag | Meaning |
|------|---------|
| `--json` | Machine-readable output on stdout |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Card printed |
| 1 | Unexpected error |
| 2 | Usage error |
| 3 | Daemon not running |

## Human output

```
name:        my-laptop
harness:     custom
public key:  <base64url>
fingerprint: 2ED9 TGVE R471 63MC C451
created:     2026-01-02T03:04:05Z
skills:      (none)
signature:   <base64url>
key storage: keychain
```

## `--json` output

```json
{
  "ok": true,
  "card": {
    "version": 1,
    "name": "my-laptop",
    "public_key": "<base64url, 32 bytes>",
    "harness": "custom",
    "skills": [{"id": "review", "name": "Code review", "description": ""}],
    "created": "2026-01-02T03:04:05Z"
  },
  "signature": "<base64url, 64 bytes>",
  "key_backend": "keychain",
  "fingerprint": "2ED9TGVER47163MCC451"
}
```

| Field | Notes |
|-------|-------|
| `ok` | `true` |
| `card`, `signature` | The signed Agent Card. Only `card` is signed |
| `key_backend` | `keychain` or `file`: where the private key lives. Local, unsigned |
| `fingerprint` | `fp(card.public_key)`: 20 characters without spaces (human output groups them 4-4-4-4-4). Read it to the other person so they can run `agentnet peers verify`; format in [../protocol/pairing.md](../protocol/pairing.md#fingerprints). Local, unsigned |

Errors (exit non-zero) use the same shape as `agentnet status`:
`{"ok": false, "error": {"code": "daemon_not_running", "message": "..."}}`.

## Verifying

The output can be piped straight into the standalone verifier, which does not
use the daemon's code:

```
agentnet identity --json | go run ./tools/verifycard
```

It prints `OK <name> <public_key>` and exits 0 when the signature is valid, 1
when it is not, 2 when the input cannot be read or parsed. Extra top-level
members such as `ok` and `key_backend` are ignored.

A leading UTF-8 byte order mark (added by Windows PowerShell 5.1 when piping) is
ignored.
