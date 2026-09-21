# Protocol

Wire formats, data models and schemas for Dorylinae (AgentNet) live here.

Per the build plan, any schema or interface change must be written in this
directory **before** the ticket that implements it starts.

Nothing is specified yet; Phase 0 adds the first documents.

## Documents

- [ipc.md](ipc.md): local CLI <-> daemon protocol (ticket 0.2a)
- [agent-card.md](agent-card.md): signed Agent Card, canonical JSON, key storage (ticket 0.3)
- [envelope.md](envelope.md): envelope format, relay authentication, forwarding and error frames (ticket 0.4)
- [pairing.md](pairing.md): pairing v2: issuer-generated code, Argon2id + HMAC key confirmation, trust states, fingerprints (tickets 0.5a/b, 0.8a)
- [session.md](session.md): Noise XX sessions for interactive traffic, static-key binding to the identity, session envelopes, replay rejection (ticket 0.6)
- [mail.md](mail.md): sealed application messages: mailbox keys, HPKE seal/open, inner signature, ack, dedupe, outbox (ticket 1.0a)
- [team.md](team.md): teams, owner-signed rosters, invites via pairing v2, introduced peers (ticket 1.1, draft)
- [presence.md](presence.md): three presence levels, sealed heartbeats, ephemeral relay type, visibility, idle detection (tickets 1.2, 1.3, draft)
- [request.md](request.md): request object, lifecycle, idempotency, inbox priority, urgency budget, offline (tickets 1.4–1.7, 1.9, draft)
- [notify.md](notify.md): desktop notifications and signed webhooks (ticket 1.8, draft)
- [../cli/ping.md](../cli/ping.md): `agentnet ping` flags, exit codes, pending/poll behaviour and `--json` output (ticket 0.6)
- [../cli/relay.md](../cli/relay.md): `relay` flags and exit codes, and how `agentnetd` connects to it
- [../cli/pair.md](../cli/pair.md): `agentnet pair` flags, exit codes, pending/poll behaviour and `--json` output (ticket 0.5b)
- [../cli/peers.md](../cli/peers.md): `agentnet peers` flags, exit codes and `--json` output (ticket 0.5b)
- [../cli/status.md](../cli/status.md): `agentnet status` flags, exit codes and `--json` output
- [../cli/identity.md](../cli/identity.md): `agentnet identity` flags, exit codes and `--json` output
