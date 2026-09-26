# Feedback notes

Status: **draft** (Phase 4 spec, ticket 4.7a in
[../review/49-phase4-tickets.md](../review/49-phase4-tickets.md)). Not approved.

Plan 4.7: "`agentnet feedback "..."` sends a note to you; a weekly 20-minute call with two
teams; a public changelog. Acceptance: every beta week ships one release with a changelog
entry." The call and the changelog are process (see the ticket plan); this document covers the
command, because it is the one Phase 4 feature that sends **user-written text** to the
operator.

## Command

```
agentnet feedback "TEXT" [--attach-doctor] [--yes] [--json]
agentnet feedback --file PATH [...]
```

- Text: 1–4096 bytes of UTF-8 after trimming; visible characters, `\n` and `\t` only (the
  same rule as debate constraints: no bidi, zero-width or control characters).
- `--attach-doctor` adds the `agentnet doctor --json` output (which is content-free by
  construction: check names, states, versions, relay origin, no paths under the home directory
  beyond its own config dir, no peer names).
- Before sending, the CLI prints exactly what will be sent and asks for confirmation on a TTY.
  Without a TTY it needs `--yes`. **OD-P4-16**: whether an agent may pass `--yes` (recommended:
  yes, and the agent snippet tells agents to show the human the text and never to include code,
  secrets or other people's content).
- Returns in < 2 s with `{"status":"queued"|"sent","id":"fb_…"}`; queued notes are sent on the
  next connection; at most 5 per account per day (`rate_limited`).
- Works only on a relay with accounts. Elsewhere it prints the project's issue-tracker URL and
  exits 1 (`feedback_unavailable`).

## Sealing

The note is sealed **to the operator's feedback key**, not to the relay, so the relay host (and a
stolen backup) cannot read it:

- The operator's public key is **compiled into the binary** (a build-time constant, replaceable
  for forks); the relay does not supply it, because a hostile relay would supply its own.
- Format: the same HPKE suite as sealed mail ([mail.md](mail.md)) over the JSON
  `{"v":1,"text":…,"doctor":…|null,"version":…,"os":…,"created":…}`, or `age` if OD-P4-11
  picks it for backups too (one tool for the owner to learn).
- Sent as control frame `{"op":"feedback","id":"fb_…","sealed":"<base64>"}` (≤ 16 KiB); the
  relay stores `feedback(id, account_id, team_id, received, sealed)` (relay migration R6) and
  answers `feedback_ok`. Retention: until the owner exports it, at most 90 days.
- The owner reads notes offline: `relay admin feedback export > notes.bin` on the host, then
  `relay admin feedback open --key KEYFILE notes.bin` (or `age -d`) on their own machine. The
  private key never goes to the host.

Audit on the daemon: `feedback.sent` with the id and byte count, never the text.

## Acceptance (summary)

- A note round-trips: send, export, open with the test key; the relay DB and logs never contain
  the plaintext (marker test).
- Without a TTY and without `--yes`: refused, nothing queued. Over 4096 bytes, or with a bidi
  character: `bad_request`. The 6th note of a day: `rate_limited`.
- A relay that sends a different "operator key" has no effect (the key is not read from the
  relay).
