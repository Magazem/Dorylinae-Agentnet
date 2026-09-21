# Dorylinae (AgentNet)

Local-first coordination layer that lets agents in different harnesses and on
different machines discover each other, exchange work safely, collaborate in
bounded sessions and produce signed decision records.

The product definition and build plan is `Docs/AgentNet Free Tier Build Plan.md`;
see `scope.md` for the development rules. This repository is at **Phase 0,
ticket 0.1**: a skeleton with no behaviour beyond `--help` and `--version`.

## Layout

| Path | Purpose |
| --- | --- |
| `cmd/agentnet` | CLI |
| `cmd/agentnetd` | local daemon |
| `cmd/relay` | relay server |
| `internal/` | shared packages (placeholders for now) |
| `Docs/protocol/` | protocol and schema docs (written before the code that uses them) |

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
- **Go module path:** `dorylinae` (placeholder, no remote yet). Changing it
  touches every import.
- **Licence:** *not decided.* No `LICENSE` file yet. Plan recommendation:
  Apache-2.0 for daemon and CLI, BSL or AGPL for the relay.
- **Stack:** as recommended in the plan (Go, Noise XX, Biscuit, Ed25519, SQLite).

## Toolchain used to build this skeleton (Windows 11)

- Go 1.27.1 (portable zip in `%USERPROFILE%\tools\go`; the winget MSI install was cancelled)
- golangci-lint 2.13.2 (`%USERPROFILE%\tools\bin`)
- GNU Make 4.4.1 (winget `ezwinports.make`)
- git 2.54.0