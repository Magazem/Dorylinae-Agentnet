# Protocol

Wire formats, data models and schemas for Dorylinae (AgentNet) live here.

Per the build plan, any schema or interface change must be written in this
directory **before** the ticket that implements it starts.

Nothing is specified yet; Phase 0 adds the first documents.

## Documents

- [ipc.md](ipc.md): local CLI <-> daemon protocol (ticket 0.2a)
- [agent-card.md](agent-card.md): signed Agent Card, canonical JSON, key storage (ticket 0.3)
- [envelope.md](envelope.md): envelope format, relay authentication, forwarding and error frames (ticket 0.4)
- [pairing.md](pairing.md): one-time pairing codes and Agent Card exchange through the relay (ticket 0.5a)
- [session.md](session.md): Noise XX sessions, static-key binding to the identity, session envelopes, replay rejection (ticket 0.6)
- [../cli/ping.md](../cli/ping.md): `agentnet ping` flags, exit codes, pending/poll behaviour and `--json` output (ticket 0.6)
- [../cli/relay.md](../cli/relay.md): `relay` flags and exit codes, and how `agentnetd` connects to it
- [../cli/pair.md](../cli/pair.md): `agentnet pair` flags, exit codes, pending/poll behaviour and `--json` output (ticket 0.5b)
- [../cli/peers.md](../cli/peers.md): `agentnet peers` flags, exit codes and `--json` output (ticket 0.5b)
- [../cli/status.md](../cli/status.md): `agentnet status` flags, exit codes and `--json` output
- [../cli/identity.md](../cli/identity.md): `agentnet identity` flags, exit codes and `--json` output
