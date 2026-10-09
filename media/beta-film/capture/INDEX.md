# Real AgentNet terminal captures for the film

Captured on 2026-10-08, 13:38 to 13:45 UTC. Everything here came from the real binaries, built from the
repo at commit `860afe8` with `go build -trimpath` into `bin/`. The setup was one local relay
(`ws://127.0.0.1:8787`) and two daemons with separate homes on one machine: **maya** (harness
`claude-code`, owner of team **atlas**) and **theo** (harness `codex`, member). Nothing in these files
was edited, apart from `#` comment lines that I added to say what each step shows.

Each `NN-*.txt` file has a header. Every command appears as `who$ agentnet ...`, followed by its exact
stdout+stderr and then `[exit N]`. Where a command changes state, I ran it once in human form. I got
the `--json` form from a read-only follow-up command (`show`/`list`), or by repeating the command with
the same `--idempotency-key`, which returns the same request with `"duplicate": true` and does not send
a second one.

## Files

| File | What it shows |
|---|---|
| `01-help.txt` | `agentnet --help` (full command list), `agentnet version` / `--json` |
| `02-request-help.txt` | `agentnet request --help`, including the What / Why / Done-when brief template |
| `03-debate-help.txt` | `agentnet debate --help`, covering all entry kinds and inline flags |
| `04-identity.txt` | `identity` on both machines: name, harness, public key, 5-group fingerprint, signature, key storage (+ `--json`) |
| `05-pair.txt` | `pair --new` (one-time code) on maya, `pair <code>` on theo ("Paired with maya"), `pair --status`, `peers` on both (+ `--json`) |
| `06-verify-ping.txt` | `peers verify` (fingerprint compare), approved on the daemon's terminal; trust goes `code -> fingerprint`; `ping` "pong from @theo: rtt 1.4 ms (encrypted)" |
| `07-team.txt` | `team create atlas`, `team invite atlas` (approval-gated), `team join`, `team list` / `team show` on both (+ `--json`) |
| `08-status-team.txt` | `status --team atlas` on both: daemon/agent/human presence table (+ `--json`) |
| `09-presence.txt` | `presence`, `--invisible` (maya sees theo go offline at once), `--visible`, `--only-team atlas`, `--human off/on` |
| `10-request-send.txt` | Three requests maya -> theo: low question, normal task, **high review (hero)** with brief file, artifact `branch=feat/checkout-retry commit=4f2a9c1`, urgency reason; `request list` (+ `--json`) |
| `11-inbox.txt` | theo's `inbox`, sorted high 3000 / normal 2000 / low 1000 even though the high one arrived last; `request show --json` with the full brief |
| `12-accept.txt` | theo `accept` (opens a work session), `defer --until 2h`, `sessions` on both |
| `13-result-loop.txt` | Work-session loop: theo `result --status partial`, maya `wait`, `session`, `--request-changes`, round 2 `result --status pass --verification tests_passed`, `wait --json`, `accept-result`, `request show` with the output |
| `14-notify.txt` | `notify` settings (+ `--json`), `--event request.completed=on`, `--test` (desktop: failed, because the container is headless) |
| `15-offline-delivery.txt` | theo `stop`; maya `status --team` shows theo offline; the request says "theo is offline, last seen ...; it will be delivered when theo is back."; delivery `relayed`; theo restarts, delivery `delivered`, it appears in theo's inbox |
| `16-consult.txt` | `consult --question ... --context-file incident-notes.md`; theo answers in one step with `result --file answer.md`; maya `wait`, `accept-result`; `request show` with the answer |
| `17-debate.txt` | Full debate: maya starts it with a position file; theo accepts with an inline position; a human constraint ("Do not change the 45s gateway timeout.") approved on maya's terminal; challenges and a revision; `--agree` / `--accept`; `wait` reports closed; `debate` views and `debates` (+ `--json`) |
| `18-decision.txt` | `decisions` on both, `decision <id>`, `--md --out`, `--json --out`, `decision verify` (valid, signed by both), **tamper test** ("invalid at step 2: hash does not match canonical(decision)"), `verify --md` |
| `19-decision.md` | The exported Markdown Decision. It was byte-identical when exported from maya and from theo (`cmp` matched) |
| `19-decision.signed.json` | The signed Decision file that `decision verify` reads |
| `20-grant.txt` | `grant theo --action fs.read --resource <dir> --expires 1h` goes to pending approval, the approval prompt, then active; `grants` on both (the path is never sent to theo, only the label); theo `fetch --list`, `fetch logs/run-07.log`, `--stat`; maya `revoke`; theo's next fetch fails "the grant was revoked" |
| `21-quarantine-release.txt` | The result after read access is **quarantined** (`session` shows sizes only); `session --release` goes to approval, then the result is visible; `accept-result`; `request show` |
| `22-log.txt` | `log --verify` ("The audit log is intact: 101 rows checked." + head/anchor), `--json`, `--head`, `--since 10m --limit 15`, `--session <s-id>` (content-free trail of the grant session), `--action decision.`, `--verify --anchor`, theo's verify |
| `22b-log-full-maya.txt` | maya's whole audit log (`log --since 1h`), 101 rows |
| `23-doctor-approve-status.txt` | `doctor` (+ `--json`), `approve --list` ("No pending approvals."), final `status --team atlas`, `status --json` |
| `24-daemon-terminals.txt` | Both daemons' own terminals: each approval line (summary + 6-digit code), the typed answer, "approved" |
| `25-relay-log.txt` | The relay's verbose log: only key prefixes, envelope type, id and size. Shows the offline queue: `queued ... bytes=1444`, then `delivered from queue count=1` |

Supporting material (not captures): `scripts/env.sh` (helpers), `scripts/daemon_pty.py` (runs agentnetd
on a real pseudo-terminal and types approval codes like a human), `run/` (homes, logs, brief and answer
files, `run/work/decisions/`, including `tampered.json`).

## Most striking and legible for the film (short, clear lines)

1. **Pairing** (`05`): `Pairing code: 4NVKB-FN6CA-7A9JA`, then on the other machine `Paired with maya (claude-code)` / `trust: code`.
2. **Encrypted ping** (`06`): `pong from @theo: rtt 1.4 ms (encrypted)`. One line.
3. **Team** (`07`): `Created team atlas (...). You are the owner.` and `Paired with maya (fingerprint YSTP CQTC HBWP 96AK 5KHC); asked to join maya's team.`
4. **Presence table** (`08`/`09`): the 3-row `NAME DAEMON AGENT HUMAN LAST SEEN` table, and theo flipping to `offline` the moment he goes invisible.
5. **Hero request** (`10`): `Queued high review request r-0282… to theo (team atlas)`. The brief file has 3 lines (What / Why / Done when).
6. **Inbox ordering** (`11`): 3 rows with PRIO 3000 / 2000 / 1000. Very clean.
7. **Offline delivery** (`15`): `theo is offline, last seen 13:41:37Z; it will be delivered when theo is back.` then `delivery delivered`. Pairs well with the relay lines `queued ... bytes=1444` and `delivered from queue count=1` (`25`).
8. **Result loop** (`13`): `Submitted result for s-… (open)`, `result: s-… (awaiting_result)`, `Requested changes on s-… (now open)`, `Accepted result for s-… (now closed)`. Four short beats.
9. **Consult** (`16`): `context: sending incident-notes.md (147 bytes)` / `Queued normal consult …` and the 3-line answer in `request show`.
10. **Debate to Decision** (`17`/`18`/`19`): the transcript list `[0] initiator position … [5] respondent answer`, `outcome: agreed (accepted)`, `state signed  signed by initiator and respondent`, the Markdown "Final agreement" + "Human decisions and constraints" sections, and the pair **`valid, signed by initiator and respondent`** vs **`invalid at step 2: hash does not match canonical(decision)`** (tamper test).
11. **Human approval** (`24`): `AgentNet approval a-1a9f03: Grant fs.read to peer … named "theo" … Code 248765.`, then `a-1a9f03 248765`, then `approved`. This is the strongest "a human stays in charge" visual. The lines are long, so crop or wrap them.
12. **Grant / revoke** (`20`): the `grants` table going `pending_approval` to `active` to `revoked`; `fetch --list` output; `agentnet: the grant was revoked`.
13. **Quarantine** (`21`): `quarantined: status pass, 319 result bytes, 209 output bytes, 0 artifacts (run 'agentnet session … --release')`.
14. **Audit chain** (`22`): `The audit log is intact: 101 rows checked.`

## Values to tidy when typesetting

- **Ephemeral demo secrets** (all one-time and already used or expired, from throwaway identities):
  - Pairing code `4NVKB-FN6CA-7A9JA`, team invite code `AK726-63J9J-YPG2G`.
  - Approval codes `047176`, `078163`, `049487`, `520305`, `248765`, `354457`.
  - The identity private keys are in `run/maya` and `run/theo` (`DORYLINAE_KEYSTORE=file`). Do not ship that folder.
- **Absolute and machine paths**:
  - Each home was reached through short symlinks `/tmp/dn-maya` and `/tmp/dn-theo`, because Unix socket paths are limited to about 107 bytes and the scratchpad path is too long. These show up in `15` (`endpoint: /tmp/dn-theo/agentnetd.sock`). The symlinks are now removed.
  - The long scratchpad path appears in `20` (the `grant --resource` argument, `grants --json` `resource.path`) and in the grant approval line (`24`). For the film, swap in something like `~/work/payments-ci`. The label `payments-ci-af11` is what theo sees.
- **Version string**: `0.0.0-dev+860afe8 (2026-10-03)` is a dev build and carries the repo commit hash. It appears in `01`, `08`, `15`, `22b`, `23` and in the `status` outputs. Drop it or replace it with a release version once one exists (there are no tags).
- **Internal references to hide**:
  - `doctor`: `account skip account support is added by a later ticket (4.2c)`. This is an internal ticket number, so cut that row.
  - `debate --help`: mentions `Docs/protocol/debate.md` twice.
  - `log` rows carry internal action names such as `experience.write` and `mailbox.rotate key_id=…`. These are harmless, but trim them for legibility.
- **Ids, keys, pids, ports**: `r-`, `s-`, `g-`, `a-`, `d-`, `t-`, `m-` ids, public keys, fingerprints, pids (4968 / 4977 / 6333) and `ws://127.0.0.1:8787` are all from this run. They are real but meaningless outside it. Truncating ids (`r-0282…`) is fine; the CLI already does this for team ids (`t-56f7…5110`).
- **Doctor warnings** (`config warn`, `program warn`): these come from my setup (homes behind symlinks in `/tmp`, and binaries in a scratch folder). They do not reflect a normal install, so do not show them.

## What failed or behaved unexpectedly (honest notes)

- **Desktop notifications** do not work in this headless container. `notify --test` printed `Desktop: failed`, and the daemon log shows `exec: "notify-send": executable file not found`. This was expected. Approvals used terminal mode.
- **HUMAN presence** is always `unknown`, because there is no desktop session for idle detection. Do not imply "human present" from these captures.
- **`request show <id> --from maya`** (by peer name, with or without `@`) answered `unknown_request` on theo. The same command with maya's public key worked, and plain `request show <id>` also worked. This looks like a bug, or a gap against `Docs/cli/inbox.md`/`request.md`, which say a name is accepted.
- **Wording on the receiver**: theo's `request show` for an *incoming* request prints `review to maya (team atlas)` where you would expect "from maya".
- **`inbox --json` and `request list --json`** have `"brief": ""`, although `Docs/cli/inbox.md` says the inbox view carries the full brief. The full brief is in `request show <id> --json`. The human `request show` does not print the brief at all.
- **`request list` human table**: the rows are not column-aligned with the header (`theo  review  high  pending  -  delivered`). Re-align the whitespace when typesetting; the content is unchanged.
- **Approval prompt** text has a doubled period before the code (`...you started this yourself.. Code 047176.`). This is cosmetic. Fix it when typesetting, or crop before it.
- **The human `debate <id>` view** does not list constraints. They appear only in `--json` (`"constraints"`) and in the Decision Markdown.
- **My sequencing, not a product fault** (annotated in `13`): theo's `wait` after the change request started after the change had already arrived, so it timed out (exit 4) as documented.
- **`agentnet --help`** does not list `sessions`, `session`, `result`, `wait`, `accept-result` or `revoke`/`grants` as top-level commands, although they all work. `grant` mentions `grants` and `revoke`.
- The relay logged two `auth rejected` lines. These came from `agentnet doctor`'s probes, which by design never sign the challenge.
- **Not captured**: webhooks (they print a one-time `whsec_` secret; skipped as not meaningful for the film), the urgency-budget downgrade, device linking, `git.read` grants, real cross-machine or Internet relay (`wss://`), and service install. Everything was on loopback on one machine.
- The grant flow worked on the first try with terminal approvals, with no workarounds.
