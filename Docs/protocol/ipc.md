# Local IPC protocol (CLI <-> agentnetd)

Status: v1, introduced by ticket 0.2a. Implemented in `internal/ipc`.

The CLI (and later, adapters) talk to the daemon over a local, per-user
endpoint. This is a **local** API only; it never crosses the network.

## Endpoint

| OS | Endpoint | Access control |
|----|----------|----------------|
| Linux, macOS | Unix domain socket `<config dir>/agentnetd.sock` | Config dir is `0700`, socket is `0600` |
| Windows | Named pipe `\\.\pipe\dorylinae-<id>` where `<id>` is the first 8 bytes (hex) of SHA-256 of the config dir path | Pipe DACL grants access to the current user's SID only |

The config dir is `os.UserConfigDir()/dorylinae`, or the value of the
`DORYLINAE_HOME` environment variable when set (used by tests and for running
several daemons side by side). The SQLite database is `<config dir>/dorylinae.db`.

A second daemon on the same endpoint refuses to start. A stale Unix socket file
(no listener answering) is removed on start.

## Framing

Newline-delimited JSON (one JSON object per line, UTF-8). The client sends one
request line and reads one response line; a connection may carry several
request/response pairs in sequence. Lines are limited to 1 MiB. The server
closes connections that stay idle for more than 30 s.

## Request

```json
{"id": "1", "method": "status", "params": {}}
```

| Field | Type | Notes |
|-------|------|-------|
| `id` | string, optional | Echoed back in the response |
| `method` | string, required | Method name |
| `params` | object, optional | Method specific |

## Response

Success:

```json
{"id": "1", "ok": true, "result": {}}
```

Failure:

```json
{"id": "1", "ok": false, "error": {"code": "unknown_method", "message": "unknown method \"x\""}}
```

Error codes (stable, machine readable):

| Code | Meaning |
|------|---------|
| `bad_request` | Malformed JSON or missing `method` |
| `unknown_method` | No handler registered for `method` |
| `internal` | Handler failed; message has no sensitive detail |

## Methods

### `status`

Params: none.

Result:

| Field | Type | Notes |
|-------|------|-------|
| `pid` | integer | Daemon process ID |
| `started_at` | string | RFC 3339 UTC start time |
| `uptime_seconds` | number | Seconds since start |
| `version` | string | Daemon version |

## Compatibility

New methods and new result fields may be added without a version bump.
Removing or changing the meaning of a field requires a new protocol version and
an entry here first.

## Audit events written by the daemon lifecycle

The `audit_events` table (`internal/audit`) records `daemon.start` and
`daemon.stop` with `actor = "daemon"`. Detail JSON: `{"pid": N, "version": "..."}`.
The table is append-only (triggers reject UPDATE and DELETE) and is not yet
hash-chained; ticket 3.6 adds a chain column by migration.
