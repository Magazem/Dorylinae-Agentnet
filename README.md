# Dorylinae (AgentNet)

Local-first coordination layer that lets agents in different harnesses and on
different machines discover each other, exchange work safely, collaborate in
bounded sessions and produce signed decision records.

The product definition and build plan is `Docs/AgentNet Free Tier Build Plan.md`;
see `scope.md` for the development rules. This repository is at the end of
**Phase 0** plus the first part of **Phase 1** (sealed mail, tickets 1.0a–1.0f):
identity, pairing, a relay with an offline queue, encrypted ping sessions, and
mail with an outbox, dedupe and acks. There are no application kinds yet
(requests, results, ...), so nothing yet does useful work on top of mail.

## Layout

| Path | Purpose |
| --- | --- |
| `cmd/agentnet` | CLI |
| `cmd/agentnetd` | local daemon |
| `cmd/relay` | relay server |
| `internal/` | shared packages (identity, pairing, sessions, mail, relay client, service install, ...) |
| `Docs/protocol/` | protocol and schema docs (written before the code that uses them) |
| `Docs/cli/` | one page per command: every flag, output and exit code |
| `tests/` | manual test plan and smoke scripts (`phase0-manual.md`, `phase0-smoke.ps1`) |

## Commands

| Command | What it does | Doc |
| --- | --- | --- |
| `agentnetd [run]` | Run the daemon (`--home`, `--relay`, `--log-file`) | [agentnetd.md](Docs/cli/agentnetd.md) |
| `agentnetd install` / `uninstall` | Start the daemon at login as a per-user service (`--relay URL` is baked in) | [agentnetd-install.md](Docs/cli/agentnetd-install.md) |
| `relay` | Relay server with an offline queue | [relay.md](Docs/cli/relay.md) |
| `agentnet status` | Daemon PID, uptime, version and outbox counts | [status.md](Docs/cli/status.md) |
| `agentnet identity` | Signed Agent Card and fingerprint | [identity.md](Docs/cli/identity.md) |
| `agentnet pair` | Pair with another machine using a one-time code | [pair.md](Docs/cli/pair.md) |
| `agentnet peers` | List, verify and remove paired agents | [peers.md](Docs/cli/peers.md) |
| `agentnet ping` | Encrypted round trip to a paired agent (an offline peer times out) | [ping.md](Docs/cli/ping.md) |
| `agentnet mail send` | Debug only (`DORYLINAE_DEBUG=1`): queue a mail | [mail.md](Docs/cli/mail.md) |

> Note: the plan and scope refer to docs/protocol/, but the existing plan lives in Docs/. On case-insensitive filesystems (Windows, macOS) they are the same directory, so the protocol docs sit at Docs/protocol/. Rename Docs/ to docs/ (owner decision) to match the plan on Linux.

## Build

```
make build   # bin/agentnet, bin/agentnetd, bin/relay (.exe on Windows)
make test
make vet
make lint
```

`make build VERSION=1.2.3` stamps the version; the default is `0.0.0-dev`.
CI (`.github/workflows/ci.yml`) vets, tests and lints, then builds all three
binaries on Linux, macOS and Windows.

## Project decisions

- **Project name:** Dorylinae. Binaries keep the plan's names.
- **Go module path:** `github.com/Magazem/Dorylinae-Agentnet`. Changing it
  touches every import.
- **Licence:** PolyForm Shield 1.0.0 (source-available, see `LICENSE`).
  Free to read, audit and use, including against the hosted relay; not
  for building a product that competes with Dorylinae.
- **Stack:** as recommended in the plan (Go, Noise XX, Biscuit, Ed25519, SQLite).

## Toolchain used to build this skeleton (Windows 11)

- Go 1.27.1 (portable zip in `%USERPROFILE%\tools\go`; the winget MSI install was cancelled)
- golangci-lint 2.13.2 (`%USERPROFILE%\tools\bin`)
- GNU Make 4.4.1 (winget `ezwinports.make`)
- git 2.54.0