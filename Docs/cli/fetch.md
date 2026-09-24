# `agentnet fetch`

Reads a file, a directory listing or a single entry through a [grant](grant.md) you hold
([Docs/protocol/grant.md](../protocol/grant.md)). Introduced by ticket 2.3c.

```
agentnet fetch <g-id> <path> [--out FILE] [--json] [--timeout SECONDS]
agentnet fetch <g-id> --list [<dir>] [--json] [--timeout SECONDS]
agentnet fetch <g-id> --stat <path> [--json] [--timeout SECONDS]
```

`<g-id>` is a grant you hold (`agentnet grants --held`). `<path>` is relative to the
grant's scope, slash-separated, without `..`, `\`, `:` or empty segments. A fetch is
**online**: it needs the grantor's daemon up, like `ping`, and travels over the Noise
session, not as mail.

| Flag | Meaning |
|------|---------|
| `--out FILE` | Write the file to `FILE` (mode 0600) instead of stdout. The file is written only after the whole read succeeded |
| `--list` | List a directory (no argument or `""` = the root of the scope), following the paging cursor |
| `--stat` | One entry: name, type, size |
| `--timeout SECONDS` | Default 30, 1 to 300: how long to wait for the grantor |
| `--json` | Machine-readable output, below |

## Reading

A read fetches the **whole file** in 256 KiB reads (files up to 8 MiB), each an IPC
`fetch_start` followed by `fetch_status` while it is pending; every IPC call returns
within 2 s. The grantor sends each read as up to eight fragments of 32 KiB and the daemon
reassembles them; a read that lacks a fragment for 10 s is sent again, whole, up to twice.
Without `--out` and `--json` the raw bytes go to stdout. If the file (or, for `git.read`,
the branch tip) changes between two reads, the command fails with `changed`: run it again.

The grantor checks the grant on **every** read, so `agentnet revoke` stops a fetch at its
next read, and one in progress at its next fragment. An expired grant, a grant of an ended
or not open session, a revoked grant and a malformed path fail **locally**, with no
message sent to the grantor. `.git` directories, symlinks, devices and FIFOs are not
served; the grantor's limits (two reads in flight per holder, 20 operations per second and
256 MiB per grant per 24 h) answer `rate_limited`.

## Output

| Mode | Human | `--json` |
|------|-------|----------|
| read | raw bytes on stdout, or `Wrote N bytes to FILE` with `--out` | `{"ok":true,"path","size","commit"?,"data":"<base64>"}`; with `--out` the file is written and `data` is left out |
| `--list` | a table `NAME TYPE SIZE` | `{"ok":true,"path","entries":[{"name","type","size"?}],"commit"?}` |
| `--stat` | `name<TAB>type<TAB>size` | `{"ok":true,"path","entry":{"name","type","size"?},"commit"?}` |

`type` is `file`, `dir`, `symlink` or `other`. `commit` (the branch tip the answer was
served from) appears for `git.read` grants only. Failures print
`{"ok":false,"error":{"code","message"}}` under `--json`, and `agentnet: <message>` on
stderr otherwise.

## Errors

`unknown_grant` (not a grant you hold), `expired`, `not_yet_valid`, `session_not_open`,
`unknown_session`, `revoked`, `bad_signature` and the other token checks of the protocol
document (all local); from the grantor: `revoked`, `out_of_scope`, `bad_path`,
`not_found`, `symlink`, `not_regular`, `too_large`, `rate_limited`, `stale`, `io_error`,
`unsupported`; from the client: `timeout` (the grantor is offline or did not answer within
`--timeout`), `changed`, `relay_unavailable`, `no_relay`, and `rate_limited` when two
fetches to the same grantor are already in flight.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error (any code above except `timeout`) |
| 2 | Usage |
| 3 | Daemon not running |
| 4 | Timeout: the grantor did not answer within `--timeout` |
