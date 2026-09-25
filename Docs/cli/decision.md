# `agentnet decision` / `agentnet decisions`

Shows, exports or verifies a Decision, the signed artifact of a closed debate
([../protocol/decision.md](../protocol/decision.md)).

```
agentnet decisions [--state S] [--peer PEER] [--json]
agentnet decision <id> [--json]
agentnet decision <id> --md [--out FILE [--force]]
agentnet decision verify FILE [--md] [--json]
```

`<id>` is a Decision's own id (`d-…`), or the debate's session (`s-…`) or request (`r-…`) id.

| Flag | Meaning |
|------|---------|
| `--state S` (`decisions`) | only `awaiting_peer`, `signed` or `peer_refused` |
| `--peer PEER` (`decisions`) | a peer name or public key, with an optional `@` |
| `--json` | machine-readable output on stdout |
| `--md` | render Markdown suitable for a repository's decisions folder |
| `--out FILE` (with `--md`) | write to `FILE` instead of stdout |
| `--force` (with `--out`) | overwrite an existing file |

## `decision verify`

Reads a signed file — the one `--json` prints or `--out` writes — **without a daemon**:
recomputes the hash, checks each present signature, the id and the derivation invariants
([decision.md §Signed file](../protocol/decision.md#signed-file-third-party-verification)).
`--md` also renders Markdown to stdout; offline, names are the keys' fingerprints, never
petnames (nothing here reaches a daemon to look one up).

## Exit codes

`agentnet decision <id>` / `agentnet decisions`:

| Code | Meaning |
|------|---------|
| 0 | done |
| 1 | error (`unknown_decision`, …) |
| 2 | usage |
| 3 | the daemon is not running |

`agentnet decision verify`:

| Code | Meaning |
|------|---------|
| 0 | valid, signed by both the initiator and the respondent |
| 6 | valid, but signed by the initiator only: **unconfirmed**. The respondent's entries and the outcome are the initiator's claim and are not proven |
| 1 | invalid. The printed step names what failed |
| 2 | usage |

A script that checks `$? -eq 0` never treats an unconfirmed Decision as agreed.

## `--json`

`agentnet decisions --json`:

```json
{"ok": true, "decisions": [{"id", "session", "peer", "outcome", "state", "created", "title"}]}
```

`agentnet decision <id> --json` prints the signed file itself (no `"ok"` wrapper: this is the
same bytes `decision verify` reads, so it can be redirected straight to `d-….json`):

```json
{"decision": {...}, "hash": "...", "signatures": {"initiator": "...", "respondent"?: "..."}}
```

`agentnet decision verify --json`:

```json
{"valid", "complete", "step"?, "reason"?, "signed_by"?, "hash"?, "id"?,
 "participants"?: {"initiator": {"key","fingerprint"}, "respondent": {"key","fingerprint"}}}
```

## Markdown

`--md` renders the fixed layout of
[decision.md §Markdown](../protocol/decision.md#markdown): a title line, the outcome and who
signed, participants with fingerprints, the problem, initial and (if any) final positions,
rounds, the final agreement or remaining disagreement, human decisions and constraints,
affected artifacts, and a verification section with the hash and both signatures.

Deterministic: the same input gives the same bytes (UTF-8, LF only, one trailing newline, no
render time). **Repository-safe**: every string that came from an agent or a human is rendered
inert — a fenced code block for multi-line text, an inline code span for single-line text,
never raw Markdown, a link, an image or a table. A Decision signed by the initiator only starts
with a fixed **UNCONFIRMED** banner and its outcome line reads "claimed by the initiator".

```
agentnet decision d-7eaeb0b78e6bd96981357b500af94044 --md --out decisions/d-7eaeb0b7.md
Wrote decisions/d-7eaeb0b7.md
```

## Without a daemon

`agentnet decision <id>` and `agentnet decisions` talk to the daemon (IPC): the debate's own
tables are the source of truth, and a Decision that only one side has signed is not on disk
anywhere else. `agentnet decision verify` is the only offline path — it needs just the signed
file, which is why a Decision is meant to leave the daemon as a file next to its Markdown in a
repository, not only viewed live.
