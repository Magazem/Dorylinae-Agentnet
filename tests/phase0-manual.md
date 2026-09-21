# Phase 0 manual test: two machines + relay

Purpose: the "ran it by hand once" and "service survives reboot" exit criteria for Phase 0
(Docs/review/05-expert-review.md, M4). You run this once on real machines and send the results back.
Time: about 45 minutes, plus one reboot of B.

Commands are given for **bash** (macOS/Linux) and **PowerShell** (Windows). `agentnet`, `agentnetd`
and `relay` below mean the built binaries in `bin/` (put `bin/` on PATH or prefix them with `./bin/`
or `.\bin\`). Mark each step `[x] PASS` or `[x] FAIL` and write notes in the results table at the end.
On any FAIL, keep going where possible and capture the logs listed in "Where to find logs".

## Roles and prerequisites

| Role | Machine | Notes |
|---|---|---|
| **A** | Machine 1 | Also hosts the relay (any machine both A and B can reach on one TCP port works) |
| **B** | Machine 2 | Ideally a different OS from A |
| **Relay** | A (or a third host) | Called `RELAYHOST` below |

Prepare before you start:
- Go installed on both machines (or copy the built binaries; build on the matching OS/arch).
- A and B can reach each other's network. Note the **LAN IP of the relay host** (`ipconfig` / `ip addr` / `ifconfig`).
- Open TCP **8787** inbound on the relay host's firewall (Windows: allow `relay.exe` on Private networks when prompted).
- The Phase 0 relay is plaintext `ws://`: use a trusted LAN or VPN, not the public internet.
- A way to compare text out-of-band (phone call or chat on a different channel) between A and B.
- Fill in: `RELAYHOST` = ______________  A OS = ________  B OS = ________  commit = ________

Set these once per shell (replace the IP):

```bash
export RELAY_URL=ws://192.168.1.10:8787          # bash
```
```powershell
$env:RELAY_URL = "ws://192.168.1.10:8787"        # PowerShell
```

## 0. Build (both machines)

```bash
git clone <repo> && cd <repo>      # or pull the commit under test
make build                          # bin/agentnet bin/agentnetd bin/relay
bin/agentnet --version
```
```powershell
# Windows without make:
go build -trimpath -o bin\ .\cmd\agentnet .\cmd\agentnetd .\cmd\relay
.\bin\agentnet.exe --version
```
Expected: `agentnet 0.0.0-dev` (or similar). [ ] PASS [ ] FAIL  (A)  [ ] PASS [ ] FAIL  (B)

Optional but recommended on A once: `go test ./...` passes. [ ] PASS [ ] FAIL

## 1. Start the relay on A (non-loopback, with queue DB)

```bash
bin/relay --listen 0.0.0.0:8787 --allow-non-loopback --queue-db "$HOME/relay-queue.db" --verbose
```
```powershell
.\bin\relay.exe --listen 0.0.0.0:8787 --allow-non-loopback --queue-db "$env:USERPROFILE\relay-queue.db" --verbose
```
Expected stdout: `relay listening on [::]:8787` (or `0.0.0.0:8787`). Leave it running in its own terminal;
its stderr is the relay log. Because the listen address is non-loopback, **v1 pairing is off**
(`--allow-pairing-v1` defaults to off). From B, check reachability:

```bash
nc -vz 192.168.1.10 8787
```
```powershell
Test-NetConnection 192.168.1.10 -Port 8787      # TcpTestSucceeded : True
```
[ ] PASS [ ] FAIL (relay up)  [ ] PASS [ ] FAIL (B reaches port)

## 2. Install the daemon as a service (A and B)

Preview, then install. `--relay` bakes the relay URL into the service definition (launchd, systemd and
Task Scheduler do not inherit your shell, so `DORYLINAE_RELAY_URL` would not reach the service):
```bash
bin/agentnetd install --relay "$RELAY_URL" --dry-run
bin/agentnetd install --relay "$RELAY_URL"
```
```powershell
.\bin\agentnetd.exe install --relay $env:RELAY_URL --dry-run
.\bin\agentnetd.exe install --relay $env:RELAY_URL
```
Expected: no admin/sudo needed; exit 0; the daemon starts immediately. The dry run shows `--relay <URL>` in the
plist arguments, the systemd `ExecStart` line, or the task XML `Arguments` (on Windows also
`--log-file <home>\agentnetd.log`). Re-running `install` with a different URL replaces the definition.
For start-at-boot without login on Linux: `loginctl enable-linger $USER`.

Then on each machine:
```bash
agentnet status
```
Expected:
```
agentnetd running
  pid:     <number>
  uptime:  <small>
  version: <version>
```
Exit code 0. Note the **PID**: A = ______  B = ______.
[ ] PASS [ ] FAIL (A)  [ ] PASS [ ] FAIL (B)

Relay check: with `--verbose` the relay log on A shows a connect line for each daemon (two total). [ ] PASS [ ] FAIL

## 3. Identity and fingerprints

On each machine:
```bash
agentnet identity
```
Expected: `name`, `harness`, `public key`, `fingerprint: XXXX XXXX XXXX XXXX XXXX` (20 chars in groups of 4),
`signature`, `key storage: keychain` or `file`.
Write down both fingerprints and both names:
A name ______ fingerprint ____ ____ ____ ____ ____
B name ______ fingerprint ____ ____ ____ ____ ____

The two names must differ (peers are addressed by name). If they are equal you cannot use `@name`; use the
public key from `agentnet peers` instead and record that as a note.
[ ] PASS [ ] FAIL (A)  [ ] PASS [ ] FAIL (B)

## 4. Pair with v2 (A issues, B redeems)

On **A**:
```bash
agentnet pair --new
```
Expected: a 15-character code like `7KQ2M-9XHF4-TRW8N`, an expiry 10 minutes ahead, a pairing ID, and it returns in under 2 s.
Failure `relay_v1` or `no_relay` means the daemon has no relay or the relay is v1-only: go back to step 2.

On **B** (within 10 minutes; case, dashes and spaces are ignored):
```bash
agentnet pair 7KQ2M-9XHF4-TRW8N
```
Expected on B: `Paired with <A's name> (custom)`, A's fingerprint, `trust: code`.
If it prints a pairing ID with state pending, poll: `agentnet pair --status <id>` until `complete` (must finish within about a minute).

On **A** confirm it completed and the peer is listed:
```bash
agentnet pair --status <pairing-id-from-A>
agentnet peers
```
Expected: state `complete`; `peers` lists B with `TRUST code`.
The fingerprint listed for the peer on each side must equal the one you wrote down in step 3 for the *other* machine.
[ ] PASS [ ] FAIL (pair)  [ ] PASS [ ] FAIL (fingerprints match step 3)

Negative check (optional): on B, `agentnet pair 7KQ2M-9XHF4-TRW8N` again. Expected failure `code_used`.
[ ] PASS [ ] FAIL [ ] skipped

## 5. Compare fingerprints out-of-band and verify

Over a phone call or a different chat channel, A reads B's fingerprint to B and B reads A's fingerprint to A
(do not paste it through the same channel an attacker could alter). If they match, on each machine:

```bash
agentnet peers verify <other-machine-name> "XXXX XXXX XXXX XXXX XXXX"
agentnet peers
```
Expected: `Verified <name> (<fingerprint>): trust is now fingerprint`; `peers` now shows `TRUST fingerprint`.

Wrong-fingerprint check: change one character and run verify again on one side. Expected: exit code 1,
`fingerprint_mismatch`, trust unchanged.
[ ] PASS [ ] FAIL (A verifies B)  [ ] PASS [ ] FAIL (B verifies A)  [ ] PASS [ ] FAIL (mismatch rejected)

## 6. Ping

On A:
```bash
agentnet ping @<B-name>
agentnet ping @<B-name>
```
Expected: `pong from @<B-name>: rtt N ms (encrypted)`, exit 0. The first ping may include the session handshake
(`--json` shows `"handshake": true` then `false`). Also ping A from B.
RTT A→B ______ ms   B→A ______ ms
[ ] PASS [ ] FAIL (A→B)  [ ] PASS [ ] FAIL (B→A)

## 7. Stop B, ping, restart B

Stop B's daemon **without uninstalling**:

```bash
# Linux:   systemctl --user stop agentnetd
# macOS:   launchctl bootout gui/$(id -u)/dev.dorylinae.agentnetd
```
```powershell
schtasks /End /TN "Dorylinae agentnetd"
```
On B: `agentnet status` -> `agentnet: agentnetd is not running ...`, exit code 3.
[ ] PASS [ ] FAIL

On A, with B stopped:
```bash
agentnet ping @<B-name>; echo "exit=$?"
```
Expected: fails with `timeout` after up to 10 s (the relay queues the envelope for an offline peer; it does
not answer `peer_offline`), non-zero exit,
and **no hang, crash or daemon exit on A** (`agentnet status` on A still running).
Record the exact message: ________________________________
[ ] PASS [ ] FAIL

Start B again:
```bash
# Linux:   systemctl --user start agentnetd
# macOS:   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.dorylinae.agentnetd.plist
```
```powershell
schtasks /Run /TN "Dorylinae agentnetd"
```
Wait about 5 seconds, then on A: `agentnet ping @<B-name>` (twice if the first only reports pending/handshake).
Expected: pong, `handshake: true` (new session after B's restart). Peers survived the restart: `agentnet peers` on B still lists A.
[ ] PASS [ ] FAIL

## 8. Reboot B: service auto-starts

On B note the PID (`agentnet status`), then reboot the machine. Log in again (the Windows task and the
Linux/macOS user units start at **login**; Linux without linger will not start before login).
Without starting anything by hand:
```bash
agentnet status
```
Expected: `agentnetd running`, a **different PID** from before, a **small uptime** (roughly time since login).
PID before ______ after ______ uptime ______
Then `agentnet peers` (A still listed, trust unchanged) and, from A, `agentnet ping @<B-name>` returns a pong
(this proves the service picked up the relay URL from step 2).
[ ] PASS [ ] FAIL (auto-started, new PID)  [ ] PASS [ ] FAIL (peers kept)  [ ] PASS [ ] FAIL (ping after reboot)

## 9. Relay restart

On A, stop the relay (Ctrl-C, expect a clean exit, code 0) and start it again with the **same `--queue-db`**
as in step 1. Wait up to about 30 s (daemons reconnect with backoff 0.5 s doubling to 30 s), then:
```bash
agentnet ping @<B-name>
```
Expected: pong, no need to restart either daemon. Relay log shows two reconnects (with `--verbose`).
[ ] PASS [ ] FAIL

## 10. Remove a peer

On B:
```bash
agentnet peers remove <A-name>
agentnet peers
agentnet ping @<A-name>
```
Expected: `Removed <A-name> (<fingerprint>)`; `peers` says `No peers paired yet...`; the ping fails with
`unknown_peer`, exit 1.
On A, `agentnet ping @<B-name>` should now fail or time out (B rejects envelopes from an unpaired key as `unpaired`):
record the result: ________________________
[ ] PASS [ ] FAIL (remove)  [ ] PASS [ ] FAIL (ping refused)

Re-pair (repeat step 4 with a fresh code) to leave the machines in a paired state if you continue to step 11.

## 11. Offline mail

Mail is the application message path: it waits for an offline peer and is acked. The only CLI for it is the
debug command `agentnet mail send` ([Docs/cli/mail.md](../Docs/cli/mail.md)), which exists only with
`DORYLINAE_DEBUG=1`. B's daemon must also run with `DORYLINAE_DEBUG=1` so it understands the debug kind `note`.
The installed service does not inherit environment variables, so for this step run B's daemon in a console.

You need A and B paired (step 4; if you removed the peer in step 10, re-pair first) and the relay from step 1
running.

1. **On B**, stop the service (step 7 stop commands), then start the daemon by hand with the debug variable
   and leave it running in its own terminal:
   ```bash
   DORYLINAE_DEBUG=1 agentnetd run --relay "$RELAY_URL"
   ```
   ```powershell
   $env:DORYLINAE_DEBUG = "1"; .\bin\agentnetd.exe run --relay $env:RELAY_URL
   ```
   On another B terminal `agentnet status` shows `agentnetd running`. Now stop that foreground daemon with Ctrl-C
   (B is offline).
2. **On A**, send a note to B:
   ```bash
   DORYLINAE_DEBUG=1 agentnet mail send @<B-name> --kind note --text "sent while B was offline"
   agentnet status
   ```
   ```powershell
   $env:DORYLINAE_DEBUG = "1"
   .\bin\agentnet.exe mail send @<B-name> --kind note --text "sent while B was offline"
   .\bin\agentnet.exe status
   ```
   Expected: `queued mail m-... to @<B-name> (queued)`, exit 0, instantly (A does not error or wait). `status` shows an
   `outbox:` line with the mail counted under `queued` or `relayed`.
3. Confirm the relay queued it: relay log (`--verbose`) shows the envelope stored, and `relay-queue.db` on A has grown.
4. **On B**, start the daemon again the same way as in point 1 (with `DORYLINAE_DEBUG=1`). Wait up to about 10 s.
   **On A**: `agentnet status` -> `outbox:  0 queued, 0 relayed, 0 expired`: the mail was delivered and acked. It is
   not resent later (`status` stays at 0 after a further minute).
5. Optional: stop B again, send another note, restart the **relay** while B is down (same `--queue-db`), start B:
   still delivered (the queue survives a relay restart).
6. When done, stop B's foreground daemon and start the service again (step 7 start commands).

Also check without the variable: `agentnet mail send @<B-name> --kind note --text x` prints
`agentnet: unknown command "mail"` and exits 2.

[ ] PASS [ ] FAIL (queued while B offline)  [ ] PASS [ ] FAIL (delivered, outbox back to 0)  [ ] PASS [ ] FAIL (no `mail` command without the variable)

## Where to find logs

| What | Where |
|---|---|
| Relay | its terminal's stderr (warnings only; `--verbose` adds connects and routed-envelope metadata, never payloads). Redirect with `2> relay.log` if wanted |
| Daemon, foreground run | that terminal's stderr (`agentnetd run --relay URL`) |
| Daemon, macOS service | `~/Library/Application Support/dorylinae/agentnetd.log` (the `--home` dir is `~/Library/Application Support/dorylinae` by default; `agentnetd install --dry-run` prints the exact path) |
| Daemon, Linux service | `journalctl --user -u agentnetd -e` |
| Daemon, Windows task | `%APPDATA%\dorylinae\agentnetd.log` (the task passes `--log-file`; rotated at 1 MiB to `agentnetd.log.1`). Task state: `schtasks /Query /TN "Dorylinae agentnetd" /V /FO LIST` or Task Scheduler > History |
| Service definition | Windows: `agentnetd install --dry-run` prints the task XML; Linux `~/.config/systemd/user/agentnetd.service`; macOS `~/Library/LaunchAgents/dev.dorylinae.agentnetd.plist` |
| Config dir (DB, key file, audit) | `agentnetd --help` shows the default; Windows `%APPDATA%\dorylinae`, macOS `~/Library/Application Support/dorylinae`, Linux `~/.config/dorylinae` |
| Relay queue | the `--queue-db` path from step 1 |

## Results

| # | Step | A | B | Result | Notes |
|---|---|---|---|---|---|
| 0 | Build | | | | |
| 1 | Relay up, non-loopback, `--queue-db` | | | | |
| 2 | Service install, `status` shows PID | | | | |
| 3 | Identity + fingerprints | | | | |
| 4 | Pair v2 (issue / redeem) | | | | |
| 5 | Out-of-band compare, `peers verify` | | | | |
| 6 | Ping both ways | | | | |
| 7 | Stop B, ping behaviour, restart B | | | | |
| 8 | Reboot B, new PID, auto-start | | | | |
| 9 | Relay restart | | | | |
| 10 | `peers remove` | | | | |
| 11 | Offline mail (debug `note`, ends delivered) | | | | |

Environment: commit ______  A OS/version ______  B OS/version ______  relay host ______  date ______

## What to send back

1. This file with every box marked and the results table filled in.
2. The output of `agentnet --version` on both machines and `git rev-parse HEAD`.
3. For every FAIL: the exact command, its full output and exit code, plus the relevant log excerpt (see "Where to find logs").
4. The step 2 outcome: whether the service needed `DORYLINAE_RELAY_URL` set by hand, and how you set it.
5. The step 7 error message, the step 8 PIDs and uptime, and the step 6 RTTs.
6. Anything that felt wrong even if it passed (long delays, windows popping up at logon, confusing messages).
