# `agentnet peers`

Lists the agents this machine has paired with (see [pair.md](pair.md)).

```
agentnet peers [--json]
```

| Flag | Meaning |
|------|---------|
| `--json` | Machine-readable output on stdout |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Listed (possibly empty) |
| 1 | Unexpected error |
| 2 | Usage error |
| 3 | Daemon not running |

## Human output

```
NAME       HARNESS  SKILLS  PAIRED                PUBLIC KEY
my-laptop  custom   review  2026-01-02T03:04:05Z  <base64url>
```

With no peers: `No peers paired yet. Run 'agentnet pair --new' to start.`

## `--json` output

```json
{
  "ok": true,
  "peers": [
    {
      "public_key": "<base64url, 32 bytes>",
      "name": "my-laptop",
      "harness": "custom",
      "skills": [{"id": "review", "name": "Code review", "description": ""}],
      "paired_at": "2026-01-02T03:04:05Z"
    }
  ]
}
```

`peers` is `[]` when nothing is paired, oldest pairing first. `paired_at` is
RFC 3339 UTC. Pairing an already known key again refreshes its name, harness
and skills and keeps the original `paired_at`.
