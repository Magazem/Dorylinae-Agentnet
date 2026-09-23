# Phase 1 manual tests

Mark each step `[x] PASS` or `[x] FAIL` and add notes.

## Two-machine only

[phase1-smoke.ps1](phase1-smoke.ps1) / [phase1-smoke.sh](phase1-smoke.sh) automate
everything below that fits on one machine: pairing v2 and fingerprints; team
create/invite/join (including a third member); presence levels
(visible/invisible/only-team/human) and the "outside peer sees offline"
visibility check; a request with brief/artifacts/urgency, queued while the peer
is offline then delivered; inbox ordering by urgency; accept/decline/defer/
complete with a D14 result; cancel before accept (→ cancelled) and cancel after
accept (→ refused); the 6th-high-in-a-week urgency downgrade; a webhook to a
local HTTP listener with HMAC verification; the desktop notification *setting*
(not a visible toast); and an audit-log content check. Each corresponding
section below is marked "(smoke script)".

What genuinely needs the owner's two separate machines, and is **not** covered by
the smoke scripts:

- **Reboot / service survival**: `agentnetd install` as an OS service, a real
  reboot or logoff/logon, and the daemon coming back up unattended. The smoke
  scripts only run `agentnetd` as a plain foreground child process (no admin
  rights available here) and never install a service.
- **Cross-OS pairing and requests**: one real Windows machine and one real
  macOS or Linux machine (or two different real OSes) exchanging pairing
  codes, presence, and requests over an actual network path, not loopback.
- **A real network relay**: a relay reachable over the internet or a LAN
  (TLS, real latency, NAT, reconnect after a real network drop), instead of
  `ws://127.0.0.1:<port>` on one host.
- **Idle detection per OS** ([below](#idle-detection-ticket-12d-internalidle)):
  needs a real logged-in desktop session left untouched for 10 minutes, once
  per OS (Windows, macOS, Linux).
- **Desktop notifications actually appearing on screen** per OS
  ([below](#desktop-notifications-ticket-18a-internalnotify)): the smoke
  scripts only check the `notify --test`/event *setting* and that the call
  path runs without requiring a human to see a toast.

## Two-machine run (ticket 1.9)

The automated e2e (`internal/daemon` `TestOfflineLifecycleEndToEnd`,
`TestAuditHasNoContent`) covers this flow in-process with two daemons and a real relay.
This checklist is the owner's manual run of the same features across two real machines
(A and B), each paired and running its own daemon.

### Team invite/join (ticket 1.1) (smoke script)

1. On A: `agentnet team create backend`.
   - [ ] Prints the team id and confirms A is the owner.
2. On A: `agentnet team invite backend`.
   - [ ] Prints a code and a pairing id.
3. On B (not yet paired with A): `agentnet team join <code>`.
   - [ ] B pairs with A and asks to join.
4. On both: `agentnet team list` / `agentnet team show backend`.
   - [ ] Both machines see a 2-member roster within a few seconds.

### Presence levels and visibility (tickets 1.2, 1.3) (smoke script)

1. On A: `agentnet status --team backend`.
   - [ ] B shows as online, with a fresh `last_seen`.
2. On B: `agentnet presence --invisible`.
   - [ ] A sees B go offline at once (a `last_seen` at that moment), not just after a timeout.
3. On B: `agentnet presence --visible`.
   - [ ] A sees B online again within one heartbeat.
4. On B: `agentnet presence --only-team backend`.
   - [ ] `agentnet presence` on B reports `only_team backend`; a peer outside the team sees B
     as never seen / offline.

### Request → inbox → accept/complete with result (tickets 1.4-1.6a) (smoke script)

1. On A: `agentnet request @bob task --title "Run the tests" --brief "What: run go test"`.
   - [ ] Returns in under 2 s with `status: queued`.
2. On B: `agentnet inbox`.
   - [ ] The request appears, with A's title and priority.
3. On B: `agentnet accept <id>`.
   - [ ] On A, `agentnet request show <id>` shows `state: accepted` within a few seconds, and
     a desktop notification fires (if enabled, see below).
4. On B: `agentnet complete <id> --status pass --summary "all green" --output-from-file out.log`.
   - [ ] On A, `agentnet request show <id>` shows `state: completed` with the result (status,
     summary, output).

### Cancel (ticket 1.6a, D11) (smoke script)

1. On A: send a second request, then `agentnet request cancel <id2> --reason "not needed"`
   before B answers it.
   - [ ] Returns at once; B's `agentnet inbox` no longer lists it (checked with `--all`:
     `state: cancelled`).
2. On A: `agentnet request show <id2>`.
   - [ ] Shows `state: cancelled` once B's daemon confirms.

### Urgency downgrade (ticket 1.7) (smoke script)

1. On A: send 6 `high` requests to B inside a few minutes.
   - [ ] The 6th is shown (on both `request show` on A and `inbox` on B) as
     `urgency: normal`, `urgency_declared: high`, with a note explaining the weekly budget.

### Desktop notification (ticket 1.8a) (setting only: smoke script; visible toast: two machines / per OS)

See [Desktop notifications](#desktop-notifications-ticket-18a-internalnotify) below; run at
least the accept/complete/cancel steps against the live request exchanged above. The smoke
scripts check `notify --desktop on|off` and that `notify --test` runs the call path
(`shown`, `failed`, or `disabled` when off) without requiring a human to see a toast; actually
seeing the toast, per OS, still needs a manual run below.

### Webhook (ticket 1.8b) (smoke script)

1. On B: `agentnet notify --webhook https://example.test/hook` (a URL you control, or a
   local `httptest`-style receiver).
   - [ ] Prints a `whsec_...` secret once.
2. On A: send a request; on B: accept it.
   - [ ] The receiver gets a signed POST for `request.received` and `request.accepted`,
     verifiable with the secret ([notify.md](../Docs/protocol/notify.md#delivery)).
3. `agentnet notify --webhook off` on B.
   - [ ] `agentnet notify` no longer lists a webhook; no further deliveries.

## Idle detection (ticket 1.2d, `internal/idle`) (two machines / per OS, not covered by the smoke scripts)

Run once per OS (Windows, macOS, Linux with GNOME, KDE, or X11 + `xprintidle`). Human present
means OS idle time under 10 min ([presence.md](../Docs/protocol/presence.md#idle-detection)).

For each OS:

1. Start the daemon in a logged-in desktop session, and use the keyboard or mouse.
   - [ ] `agentnet status` shows human present (`human: 1`).
2. Leave the machine untouched for 10 min. Do not run CLI commands from that machine's own
   input devices (use SSH or a remote shell if needed).
   - [ ] After 10 min idle, human flips to not present (`human: 0`).
3. Touch the keyboard or mouse.
   - [ ] Human flips back to present within one heartbeat plus the 5 s cache.
4. Unknown cases:
   - [ ] Windows: daemon running as a service in session 0 reports unknown (`human: 2`).
   - [ ] Linux without gdbus, `xprintidle` or a supported compositor reports unknown, and
     presence keeps working.

| OS | Mechanism that answered | Flips at 10 min | Notes |
|---|---|---|---|
| Windows | GetLastInputInfo | | |
| macOS | ioreg HIDIdleTime | | |
| Linux | gdbus Mutter / ScreenSaver / xprintidle | | |

## Desktop notifications (ticket 1.8a, `internal/notify`) (visible toast: two machines / per OS; setting only is covered by the smoke scripts)

Run once per OS (Windows, macOS, Linux with a session bus running `org.freedesktop.Notifications`).
`agentnet notify --desktop on` first, then trigger each event with a paired peer
([notify.md](../Docs/protocol/notify.md#desktop)):

1. `agentnet notify --test` shows a visible notification titled "AgentNet".
   - [ ] Notification appears within a few seconds.
2. A peer sends a request (`request.received`, on by default).
   - [ ] Title is `<Urgency> <type> request from <name>`; body is the title, within 5 s of
     the request landing in the inbox.
3. Accept / decline / defer / complete round trips from the peer's side mirror back.
   - [ ] `request.accepted` / `request.declined` shown (on by default); `request.deferred` /
     `request.completed` only after `agentnet notify --event request.deferred=on` /
     `--event request.completed=on`.
4. The sender cancels a pending request; the recipient sees `request.cancelled` (on by default,
   recipient side only).
   - [ ] Notification shown only on the recipient's machine.
5. Turn off one event (`agentnet notify --event request.received=off`) and repeat step 2.
   - [ ] No notification for that event; others still fire.
6. `agentnet notify --desktop off`, repeat step 2.
   - [ ] No notification at all; `agentnet notify --test` reports `"desktop":"disabled"`.

| OS | Mechanism used | Notes |
|---|---|---|
| Windows | PowerShell toast (Windows.UI.Notifications) | |
| macOS | osascript `display notification` | |
| Linux | gdbus `org.freedesktop.Notifications.Notify` (fallback `notify-send`) | |

## Headless agent harness (ticket 1.H)

Script: [harness/phase1-agents.ps1](harness/phase1-agents.ps1) (run) and
[harness/phase1-agents.sh](harness/phase1-agents.sh) (written, not run here —
see [harness/README.md](harness/README.md)).

- **Date:** 2026-09-22.
- **Harnesses:** Claude Code CLI (`claude` at `%USERPROFILE%\.local\bin\claude.exe`,
  app/session build `2.2553.1`) and Codex CLI `codex-cli 0.152.1`.
- **Environment:** Windows PowerShell 5.1, Go 1.27.1, worktree `AgentNet-wt/t1-H`
  (branch `p1/t1-H`, base `main` `5111bee`).
- **Result: FAIL (blocked), not a script defect.** Round 1 (Claude Code sends,
  Codex CLI receives) ran the full pipeline for real: built the binaries,
  started a loopback relay and two `agentnetd` daemons with separate `--home`
  dirs, paired them, created team `t1h` with both as members, then launched a
  real headless `claude -p` process in a working directory holding only the
  `CLAUDE.md` snippet. Given only the plain-English instruction "ask agent-b
  for a review of branch p1/t1-H with idempotency key ...", the agent found
  and ran `agentnet request agent-b review ...` unaided (from the snippet +
  `--help`) and queued **exactly one** request
  (`r-135910bf0890f1b3db083649844d788f` in the final run;
  `r-85cafcc0a12a6662aad7a915ebfb789a` in an earlier one), matching the given
  idempotency key. The recipient step (`codex exec`) then failed immediately:
  `codex` returned `"You've hit your usage limit... try again at Oct 2nd,
  2026 8:18 AM."` — an account-level rate limit on the installed Codex CLI,
  confirmed independently with a bare `codex exec "reply with exactly OK"`
  smoke test outside the harness. Round 2 (roles swapped) was not attempted
  since it depends on the same Codex account. **This blocks 1.P until Codex
  usage resets (or the owner supplies a different account/harness for the
  recipient role).**
- **Supplementary check (not part of the official two-harness matrix):** to
  confirm the rest of the pipeline (inbox → accept → complete → D14 result →
  audit log) actually works with a real agent, the recipient role was
  temporarily pointed at Claude Code instead of Codex for one throwaway run.
  That run's sender agent gave up in a single turn claiming "no AgentNet
  tool" (an inconsistent, non-reproduced response — a second identical
  attempt is what produced the clean pass above), so the recipient side of
  the pipeline itself was not exercised by a real agent this session; the
  request → inbox → accept → complete → audit chain is implemented and was
  exercised only by the script's own CLI calls in earlier development, not by
  a second live agent turn. This should be re-attempted once Codex is
  available, or with another second harness.
- **Snippet:** no change made. The one clean sender run shows
  `Docs/agents/snippet.md` plus `--help` is sufficient for Claude Code to
  find and use the CLI correctly (correct subcommand, correct peer name,
  correct idempotency key, single request, no protocol names in the prompt).
  The single-turn "no AgentNet tool" response from the same harness on a
  different invocation looks like model-level non-determinism (it invented
  tool names — "Claude Docs, Google Drive, Picsart" — that do not exist in
  this restricted session) rather than a snippet gap; flag for a retry count
  in the script (e.g. one retry of a sender/recipient invocation that
  produces zero tool calls) if this recurs.
- **Script bugs found and fixed during this run** (both now fixed in
  `phase1-agents.ps1`, no Go/product code touched):
  1. `ProcessStartInfo.ArgumentList` does not exist under .NET Framework
     (Windows PowerShell 5.1) — only under .NET Core/5+. Every process launch
     now builds a quoted `Arguments` string by hand (`Format-ArgList`).
  2. A function parameter named `$Home` collided with PowerShell's read-only
     automatic `$HOME` variable, throwing `VariableNotWritable` on every
     `agentnet ... --json` call. Renamed to `$HomeDir` throughout.
  3. `Process.Kill($true)` (tree-kill) also does not exist under .NET
     Framework; the call threw a `MethodException` that a bare `catch {}`
     silently swallowed, so the relay and both `agentnetd` daemons were never
     actually killed after a round failed or timed out, leaking processes
     across runs (this is what looked like a 26-minute "hang" mid-session —
     the top-level script had already exited; only its orphaned children were
     still running). Replaced with a `Stop-ProcessTree` helper that shells out
     to `taskkill /T /F`.
- **Real CLI issue found (not fixed, per instructions):** none in the
  `agentnet`/`agentnetd`/`relay` binaries themselves. `agentnetd`'s stderr
  showed one transient `noise: sign static key: load identity key: keystore:
  secret not found` on daemon B during an earlier run; it did not recur in
  the clean final run and did not block anything (the daemon reconnects with
  backoff and re-signs on the next attempt per `Docs/cli/relay.md`), so it is
  not reported as a defect — flag it if it reproduces reliably.
- **Total run time:** 19.9 s for round 1 up to the Codex blocker (well under
  the 10-minute budget).
- **Prerequisite note:** there is no `sqlite3` CLI or CGo SQLite driver
  available in this environment (the daemon uses the pure-Go
  `modernc.org/sqlite`), so the audit-log assertion shells out to
  `python3 -c "import sqlite3; ..."` (present here as Python 3.12.10) rather
  than adding a new Go tool, per the "scripts and docs only" instruction for
  this ticket; see `harness/README.md`.

**Next step for the owner:** re-run `tests/harness/phase1-agents.ps1` (or the
`.sh` port) once Codex CLI's usage limit resets (2026-10-02) or with a second
account/harness substituted, to get a real PASS on both rounds before 1.P.

## Follow-up: Claude-only round, both roles (interim evidence, not a substitute)

Added `-SenderHarness`/`-RecipientHarness` (`--sender-harness`/
`--recipient-harness` in the `.sh`) to override the default two swapped
Claude/Codex rounds with one round using a chosen harness for both roles.
Defaults are unchanged. Used here as `-SenderHarness claude -RecipientHarness
claude` to probe the parts of the pipeline Codex's usage limit currently
blocks (inbox → accept → complete → D14 result → audit), while Codex CLI
remains unavailable until 2026-10-02.

**Date:** 2026-09-22 (same session as above). **Result: no clean pass in 5
attempts — reported as interim evidence of flakiness, not a PASS.** The
non-agent infrastructure (build, relay, two daemons with separate `--home`
dirs, pairing, team creation, and this session's process cleanup fix) was
100% reliable across all 5 attempts: every attempt reached the "team t1h has
both members" step in a few seconds with no leaked processes afterward
(verified with `Get-Process relay,agentnetd` after every attempt). The
variable part was the real headless `claude -p` agent turn itself:

| Attempt | Sender (agent-a) | Recipient (agent-b) |
|---|---|---|
| 1 | FAIL (1 turn, refused) | not reached |
| 2 | PASS (queued 1 request) | FAIL (1 turn, refused) |
| 3 | FAIL (1 turn, refused) | not reached |
| 4 | PASS (queued 1 request) | FAIL (1 turn, refused) |
| 5 | FAIL (1 turn, refused) | not reached |

Sender: 2/5 passed. Recipient: 0/2 chances passed (both times the sender
succeeded, the recipient then failed). Every failure was the same one-turn
refusal pattern, e.g. (recipient, attempt 4): *"I don't have a tool for an
'AgentNet inbox' in this session — my available tools are Bash, Google
Drive, Picsart, and Claude Docs, none of which connect to an AgentNet
service."* The model lists Bash as available (consistent with `--restricted
--tools Bash --allowedTools "Bash(agentnet *)"`) but never attempts to run
it — it does not mention checking `CLAUDE.md` or trying `agentnet --help`,
it just declines in one turn. The successful runs took 4 turns with visible
thinking before finding and using the CLI correctly. "Claude Docs, Google
Drive, Picsart" appearing verbatim and repeatedly as the model's own account
of "available tools", alongside Bash, suggests `--restricted` (which only
documents removing tools that run commands/code, e.g. Bash/PowerShell/REPL,
plus WebFetch) does not hide other first-party connector-type tools that may
be enabled on this account — that clutter may be part of why the model
sometimes doesn't recognize it should shell out via Bash. This is a
plausible contributing factor, not a confirmed root cause; flagging for the
owner rather than acting on it, since diagnosing the exact tool-list
`claude` sends the model is outside this ticket's scope (scripts and docs
only, no product/CLI changes).

No snippet change was made: the failures are not about the snippet's
content (the two passing runs used the identical, unmodified snippet and
found the right command immediately) but about the agent not attempting a
tool call at all in the failing turns. Widening the acceptance to "retry the
agent invocation once if it made zero tool calls" would likely raise the
pass rate, but that is a script-behavior change beyond what this follow-up
asked for; flagged here for the owner to decide rather than made
unilaterally.

## Follow-up 2: root cause found and fixed — Claude↔Claude now passes reliably

**Date:** 2026-09-22 (same session). **Result: PASS, 3/3 attempts, fully
clean (0 leaked processes).** Diagnosed the flakiness above precisely using
`claude ... --output-format stream-json --verbose` to read the `system/init`
event's `tools` array directly, and found **two real, distinct causes**, both
now fixed without any Go/product change:

1. **On this Windows install, Claude Code's shell-execution tool is named
   `PowerShell`, not `Bash`.** The `system/init` event's `tools` list with no
   flags at all includes `PowerShell` (never `Bash` — that name does not
   exist anywhere in this installation's tool universe). Passing `--tools
   Bash --allowedTools "Bash(agentnet *)"` (as the original ticket's own
   phrasing, and Anthropic's own docs, assume) therefore named a
   nonexistent tool: combined with `--restricted` it silently produced an
   **empty tool list** (`"tools": []`), which is exactly why every earlier
   attempt's agent either refused outright in one turn (no tools at all) or
   got auto-denied on its very first "Bash" call. Fixed in
   `phase1-agents.ps1` by using `"PowerShell"` /
   `"PowerShell(agentnet *)"` instead. `phase1-agents.sh` (Linux/macOS) keeps
   `"Bash"`, which is correct there. **This is the actual explanation for
   the "I don't have an AgentNet tool" refusals reported in the two prior
   sections** — not model non-determinism as first guessed, though see (2)
   below for the remaining source of flakiness once the tool name was fixed.
2. **The owner's account-level connectors (Claude Docs, Google Drive,
   Picsart) and this session's own MCP servers were still visible to the
   model** even under `--restricted`, because `--restricted` only removes
   tools *documented* as code/command runners — it does not touch MCP-served
   tools. Fixed by adding `--strict-mcp-config --mcp-config
   '{"mcpServers":{}}' --setting-sources project` to the `claude`
   invocation in both scripts (`--setting-sources project` drops the
   user-level settings where those connectors are enabled; the empty
   `--mcp-config` with `--strict-mcp-config` drops every MCP server).
   Verified empirically: with both fixes, `tools` is exactly `["PowerShell"]`
   — no connectors, no other built-ins.
3. **A secondary, narrower issue surfaced once (1) and (2) were fixed:** the
   model's very first move was a reasonable existence probe chained into one
   command, e.g. `Get-Command agentnet -ErrorAction SilentlyContinue;
   Get-Command agentnet.exe -ErrorAction SilentlyContinue; where.exe
   agentnet`. Claude Code will not auto-approve a multi-statement command
   off a prefix match (by design — otherwise an allowed prefix could smuggle
   arbitrary extra commands past the allowlist via `;`/`&&`), so this whole
   probe was denied, and the CLI's own denial message ("do not retry it...
   anything else that requires approval will be denied the same way for the
   rest of this session") made the model give up entirely rather than try
   `agentnet --help` next. Fixed by adding one sentence to the **harness's
   own generated prompts** (not the product snippet, which stays short and
   general per this ticket): "Run agentnet --help directly as your first
   command; do not check whether it exists first ...; and do not chain it
   with any other command." Confirmed via the same stream-json inspection
   that with this sentence the model runs `agentnet --help`, then `agentnet
   request --help`, then the correct `agentnet request agent-b review
   --title ... --idempotency-key ... --json` — all single, unchained,
   allowlisted commands.

**Files changed for this fix:** `tests/harness/phase1-agents.ps1` (tool
name `PowerShell`, MCP/connector isolation flags, anti-probing prompt
sentence), `tests/harness/phase1-agents.sh` (MCP/connector isolation flags
and matching prompt sentence; tool name kept as `Bash`, which is correct on
Linux/macOS), `Docs/agents/snippet.md` (see below), this file.

**Snippet change (recorded per the ticket):** added an unambiguous opening
to `Docs/agents/snippet.md`'s pasted block: "AgentNet is used through a
command-line program named `agentnet`... Run it with your shell/Bash tool...
There is no separate 'AgentNet' tool, app or connector... Start with
`agentnet --help`..." Why: the original snippet assumed the agent would
infer it should shell out; when connector tools were visible (see (2)
above) the model sometimes concluded "AgentNet" must be one of *those*
instead. The three PASS runs below still needed the harness-level
anti-probing sentence in addition to this; the snippet change alone did not
fully eliminate the failure mode while the tool-name and connector-leak bugs
were still present, so its standalone contribution wasn't isolated —
recommend keeping it regardless, since it is accurate and harmless.

**Runs (all-Claude round, `-SenderHarness claude -RecipientHarness
claude`), after all three fixes:**

| Attempt | Sender | Recipient | Elapsed | Processes leaked |
|---|---|---|---|---|
| 1 | PASS | PASS | 27.6 s | 0 |
| 2 | PASS | PASS | 26.0 s | 0 |
| 3 | PASS | PASS | 25.7 s | 0 |

3/3 (100%) on both roles, all well under the 10-minute budget, all
assertions from `--json` output confirmed each time (request `show`
completed with note + result, `inbox --all` completed, exactly one request,
all five audit actions present on one side or the other).

**Consequence for the required Claude + Codex rounds:** since the Claude leg
of `claude -p ... --restricted --tools PowerShell/Bash ...` is the same
invocation regardless of which peer it plays, the default two swapped
rounds (round 1: claude sends, codex receives; round 2: codex sends, claude
receives) should now pass their Claude leg too. **Codex CLI itself remains
blocked** by the account usage limit reported in the first section (resets
2026-10-02) — that half is unchanged and still needs a real Codex run to
confirm end to end.

## Follow-up 3: agy (Antigravity CLI) added as substitute second harness

**Date:** 2026-09-23. **agy version:** 1.2.9 (`%LOCALAPPDATA%\agy\bin\agy.exe`).
**Worktree:** `AgentNet-wt/h-agy` (branch `p1/h-agy`, base `main`). **Result:
PASS, both required swapped rounds, 1/1 attempts each** (owner approved agy
as the substitute second harness while Codex CLI's usage limit is in effect;
see the first section above).

Confirmed logged in and callable headless: `agy -p "reply with exactly OK"
--output-format json --print-timeout 60s` returned
`"status":"SUCCESS","response":"OK\n"` on the first try — no interactive
login was needed (step 2 of the ticket, not applicable here).

**Permission model investigated, no working fine-grained allowlist found:**
agy denies any tool call headless mode can't prompt for, with the message
"Add an allow-rule under `permissions.allow` in `settings.json` (e.g.
`command(<target>)`)". The only settings.json location found (via string
search of the binary) is the operator's real, shared global
`~/.gemini/antigravity-cli/settings.json` (project-specific overrides
apparently exist under `~/.gemini/config/projects/`, tied to agy's own
`--project` concept, not a plain per-directory file). Overriding `HOME`/
`USERPROFILE` to point agy at a scratch settings file had no effect (agy
still read/enforced against the real global config, or ignored the
override entirely — could not fully determine which without further
elevated diagnostics). Editing the operator's real global settings.json to
test the allowlist was avoided as out of scope for this ticket (scripts/docs
only) and risky to mutate shared state affecting other sessions. **Per the
ticket's documented fallback**, agy is run with
`--dangerously-skip-permissions` (auto-approves every tool call, not just
`agentnet`), and the agy working directory is kept containing **only** the
`AGENTS.md` snippet (no repo source, no other files) to compensate. No MCP
servers are configured on this account (`agy mcp list` → "No MCP servers
configured."), so there was nothing to isolate there.

**`--sandbox` caused a Windows UAC elevation prompt — removed, do not
re-add on this machine.** This session initially added `--sandbox` (listed
in `agy --help` as "Run in a sandbox with terminal restrictions enabled") to
the agy invocation in both harness scripts and ran both required rounds with
it. The orchestrator reported a UAC (administrator) consent prompt appeared
on the owner's screen during this session's agy runs; this machine has no
admin rights, so headless mode can never answer such a prompt. The exact
triggering command was `agy.exe ... --sandbox ...` as invoked from
`phase1-agents.ps1`'s `agy` case in `Invoke-Agent` (both the sender and
recipient legs use the same flag set). **`--sandbox` has been removed from
both scripts' agy invocations** (`tests/harness/phase1-agents.ps1`,
`tests/harness/phase1-agents.sh`) and documented as a do-not-use flag on
this machine in `tests/harness/README.md`. agy **does** run headless
without it: the login smoke test above and both PASS rounds below used
`--dangerously-skip-permissions` without `--sandbox` and needed no
elevation. No admin-requiring command was retried after the report; all of
this worktree's own relay/agentnetd/agy processes were confirmed not
running afterward (`Get-Process relay,agentnetd,agy` → none found).

**Runs (required two swapped rounds, `-MaxAttempts 3`):**

| Round | Sender | Recipient | Result | Attempts | Request ID | Elapsed |
|---|---|---|---|---|---|---|
| 1 | claude | agy | PASS | 1/1 | `r-1adb5e664ca4a9d8cfaab55711a3a852` | 50.9 s |
| 2 | agy | claude | PASS | 1/1 | `r-902070232cc10ee5d80a63b8b125b9e6` | 38.9 s |

Both rounds passed on the first attempt (no retries needed). All assertions
came from `agentnet ... --json` and the audit log, never agent prose:
request `show` completed with note + D14 result, `inbox --all` completed,
exactly one request per round, all five audit actions present. Both well
under the 10-minute budget. No leaked processes after either round. **Note:**
this run's agy invocation still included `--sandbox`, which is what
triggered the UAC prompt reported below — see the confirmation rerun
immediately after.

### Confirmation rerun without `--sandbox`

**Date:** 2026-09-23 (same day, after `--sandbox` was removed from both
harness scripts). Re-ran both required rounds once each with the current
scripts (no `-MaxAttempts` override, i.e. 1 attempt) to confirm agy runs
headless with no admin/UAC prompt now that `--sandbox` is gone.

| Round | Sender | Recipient | Result | Request ID | Elapsed | Prompt seen |
|---|---|---|---|---|---|---|
| 1 | claude | agy | PASS | `r-ce801645971f05b08f39b26c4f10ffd3` | 43.9 s | none |
| 2 | agy | claude | PASS | `r-910ea6f708fae99b4add8f3b5ea66b37` | 34.3 s | none |

No UAC or other admin prompt appeared during either round. Both passed on
the first attempt, well under the 10-minute budget, all `--json`/audit-log
assertions confirmed as above. `Get-Process relay,agentnetd,agy` after both
rounds found nothing running — no leaked processes.

**Script changes:** added `agy` as a valid `-SenderHarness`/
`-RecipientHarness` (`--sender-harness`/`--recipient-harness`) value in both
`tests/harness/phase1-agents.ps1` and `tests/harness/phase1-agents.sh`; a
new `Invoke-Agent`/`invoke_agent` case for `agy`; a `-MaxAttempts`/
`--max-attempts` flag (default 1) that retries a failed round and reports a
pass rate, used for these two runs; and the **default two swapped rounds
now use agy as the second harness** (`claude` ↔ `agy`) instead of `codex`,
since agy is the documented substitute while Codex remains blocked — Codex
is still fully supported via `-SenderHarness codex -RecipientHarness codex`
(or mixed with `claude`). `tests/harness/README.md` updated to match
(prerequisites, default rounds, `--max-attempts` flag, the `--sandbox`/UAC
warning, and the permission-model limitation). No Go/product code touched.
No snippet change: agy read `Docs/agents/snippet.md`'s block from
`AGENTS.md` and found the right command via the same anti-probing sentence
already used for Codex, with no observed issues.

**Next step for the owner:** decide whether the settings.json
`permissions.allow` mechanism is worth pursuing further for a real
per-binary allowlist (would need testing against the real global config, or
finding agy's per-project settings path), or whether
`--dangerously-skip-permissions` + an isolated snippet-only working
directory is acceptable as the standing containment model for agy in this
harness.
