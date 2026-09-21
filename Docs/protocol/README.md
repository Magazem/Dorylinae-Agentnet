# Protocol

Wire formats, data models and schemas for Dorylinae (AgentNet) live here.

Per the build plan, any schema or interface change must be written in this
directory **before** the ticket that implements it starts.

Nothing is specified yet; Phase 0 adds the first documents.

## Documents

- [ipc.md](ipc.md): local CLI <-> daemon protocol (ticket 0.2a)
- [../cli/status.md](../cli/status.md): `agentnet status` flags, exit codes and `--json` output
