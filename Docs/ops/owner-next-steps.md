# Owner next steps (Phase 4 setup)

Status: a plain checklist of what you (the owner) still need to do before wave 1. Not a
spec — see the linked docs for detail.

Already done, for context: domain/DNS, GitHub OAuth apps, Hetzner hosting account. No
action needed on those.

## 1. Release-signing key (OD-P4-19)

Full steps: [release-signing.md](release-signing.md) — read it in full before you start.
In short:

1. On a machine you trust, offline: `go run ./tools/releasesign keygen -out agentnet-release.key`.
2. Keep that file on encrypted media (it's an unencrypted key file) — never on GitHub, never online.
3. Make a sealed backup copy and store it somewhere separate from the first copy.
4. Run `go run ./tools/releasesign embed -key agentnet-release.key -in scripts/install.sh -out scripts/install.sh` to write the public key into the installer.
5. Hand the updated `scripts/install.sh` to the Orchestrator to commit — you don't commit it yourself.
6. Keep the private key available for every future release (you sign each `SHA256SUMS` by hand); see the doc's "Every release" section.

## 2. Operator age/HPKE key pair (for backups and feedback)

This key lets you (and only you) decrypt daily relay backups and read sealed feedback
notes — the relay host itself cannot read either. Background:
[relay-hosted.md](../protocol/relay-hosted.md) (backup section) and
[feedback.md](../protocol/feedback.md).

**Not yet specified: there is no exact generation command in the repo yet.** The docs
say the key will be either an [age](https://age-encryption.org) key or the repo's own
HPKE format (ticket OD-P4-11 decides which, to keep one dependency), but they don't yet
say which, or give the exact command. That decision and the exact step need a short
ticket before ticket 4.1b (deploy) and 4.7a (feedback) can use the key. Don't generate
one yet by guessing a command — wait for that ticket.

What to expect once it exists: one offline key pair, public half compiled into the relay
config, private half kept by you (not on the host), with a backup copy — same handling
as the release-signing key above.

## 3. Backup object storage (a different provider than Hetzner)

Daily encrypted relay backups need to go to object storage at a provider (or at least a
different region) other than the Hetzner host, so a compromise or outage of the host
doesn't also take out the backups ([relay-hosted.md](../protocol/relay-hosted.md),
"Persistence, backup and restore").

**Not yet specified: the exact storage interface (e.g. S3-compatible) isn't confirmed in
the repo yet** — the deploy/backup script (ticket 4.1b) hasn't been written, so pick a
provider once that ticket specifies what it expects. Any reputable object storage
provider is fine in principle; just make sure it's not Hetzner.

## 4. Privacy note and beta terms (OD-P4-15)

Needed before wave 1 / any beta invitation. Lead time: 1–2 weeks with review. Ticket
4.5a drafts this; you review it. It needs to cover, in plain language:

- What's collected: accounts, IPs in logs (kept ≤ 14 days), usage counters, feedback notes.
- Why it's collected, and how long each part is kept.
- That testers can ask for deletion.
- Who processes the data on your behalf: the host, the object storage provider, GitHub.
- Who the data controller is, and their contact details.

It must also disclose that this beta has **not** had an independent security review
(D41) — that's already written up in
[known-limitations.md](../beta/known-limitations.md#security-review); the privacy
note / beta terms should point testers there or repeat it.

Full ticket detail: [49-phase4-tickets.md](../review/49-phase4-tickets.md), row
`OD-P4-15` in "Owner-side work and lead times" / "Owner decisions needed".
