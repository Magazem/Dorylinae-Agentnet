# 55 / 99: Consolidated report

Consolidator: CR-Consolidator · model claude-opus-5-5 · elapsed ~261 min wall clock (includes a
usage-limit pause; roughly 120 min of active work) · commit `6cc26a7` (worktree `cr-cons`, HEAD
`b621ea3`; `git diff --stat 6cc26a7 HEAD -- internal cmd tools` is empty) · 2026-09-29

## 1. Verdict

Review 55 found **no Critical**. The cryptographic core (pairing v2, sealed mail, Noise sessions,
capability tokens, Decision signatures, the audit chain), the approval-code gate itself, and
the fail-closed handling of every authorization check hold up under 31 chunk reviews, 15 theme
passes and 14 adversarial verifications. What breaks is at the edges, in five places:

1. **The hosted relay's memory bound is not fair.** A stranger with free keys can take relay-wide
   mail delivery down cheaply: one /24 holding ~50 half-sent frames (R55-001), or a few prefixes
   sending presence to slow readers (R55-002). Both apply to the early private relay (4.1p) as
   configured.
2. **The human approval shows too little.** It shows a random label instead of the path, the
   peer's self-chosen name instead of its fingerprint, and it cuts device scopes at 4096 characters.
   A prompt-injected local agent can get approvals the human did not mean to give (R55-005,
   R55-007).
3. **The forbidden-resource check compares spellings, not files.** `\\?\`, UNC, `subst`, volume
   GUIDs and junctions on Windows, and the `/System/Volumes/Data` firmlink on macOS, all
   bypass it, and the peer can then read `dorylinae.db` (R55-006).
4. **Local and supply-chain trust gaps.** The owner signs a mutable draft `SHA256SUMS` (R55-003),
   the Windows pipe can be squatted by another OS user (R55-008), and `decision verify` lets a
   file's member name overwrite the verdict line (R55-004).
5. **Many growth and poison-mail paths** where a paired peer or the relay makes the daemon store,
   log or retry without bound (R55-015 to R55-021).

No finding lets the relay read content, forge a signature or complete a pairing MITM, and no
content reaches the audit log.

**Counts per severity**

| Stage | Critical | High | Medium | Low | Info | Total |
|---|---|---|---|---|---|---|
| As reported (31 chunks + 15 themes) | 0 | 12 | 32 | 135 | 96 | 275 |
| After verification (14 verified, all `confirmed`, no severity change) | 0 | 12 | 32 | 135 | 96 | 275 |
| After dedup and normalisation (unique root causes, this report) | 0 | 8 | 25 | 108 | 84 | 225 |

Normalisation:
- Two accounts-only Highs (C02-01, C03-01) and three accounts-only Mediums (C03-02/03/04) were
  lowered one level under rubric §2 (accounts are off by default and off on 4.1p). They are a
  hard gate for turning `--accounts` on (§6 D3).
- Eleven lower-rated findings were merged *upward* into an entry of the same root cause: six
  into Highs (C11-06, C12-02, T8-02 → R55-005; C11-02, T4-01, C14-01 → R55-006) and five into
  Mediums (C06-02, T1-02, T5-01 → R55-014; C07-02 → R55-015; T4-04 → R55-027).

The per-entry "Severity reasoning" says which. Within Low, entries are grouped by area and
roughly ranked, so ids are not strictly in rank order.

## 2. Coverage

- **Units done:** 31/31 chunk reports, 15/15 theme reports, 14/14 verifications (every
  Critical/High and every `suspected` Medium: C01-01, C01-02, C02-01, C02-02, C03-01, C11-01,
  C11-04, C14-02, C15-01, C16-01, C25-01, T8-01, T11-01, T13-01). `chunks/C16-leads-R5.md`
  (partial notes from the aborted R5 run) was used for leads only.
- **Lines covered:** 69,135 (62,806 Go in 272 files + 6,329 other in 27 files), every
  production file in exactly one chunk (03-chunks.md).
- **Chunk questions:** every numbered question in 03-chunks.md is answered in its report
  (checked by script: C01–C30 answer 1..n; C31 answers its six).
- **Theme methods:** every theme has its inventory table and an "Assumptions resolved" list.
- **Disagreements I settled by reading code** (read-only, cited):
  - IPC method count: T6a says 73, T8 70, T6c/C28 71. Counting `Handle("…")` in
    `internal/daemon/*.go` (non-test) gives **71 unique**. `00-index.md` §3.a omits
    `notify_test`.
  - O-068: C08 says still open, T6a-24 says fixed. `internal/presence/body.go:85-100`
    (`fixedBodyLen`) pads every presence plaintext to one worst-case size. **Fixed** (T6a is right).
  - C15 unchecked "Caddy XFF": `internal/relay/limits.go:445-470` takes the **last**
    `X-Forwarded-For` entry, and only from the trusted `127.0.0.1`. Caddy
    (`deploy/early/Caddyfile`) sets that entry to the client address. **Holds** on 4.1p.
  - T4 lead `grant.go:148`: read `internal/daemon/grant.go:133-162`. If
    `EvalSymlinks(configDir)` (or `home`) errors, the config-dir (home) refusal is skipped.
    That is a real fail-open, though rarely reachable. Added to R55-006 as a consolidator note.

**Not covered (gaps, with reason)**

| Gap | Reason | Where it goes |
|---|---|---|
| C16-leads-R5 item 3a: Unix `Listen` dial→remove→listen race (`internal/ipc/transport_unix.go:20-30`) | Not in C16.md or any theme. It is a Low lead from an aborted run with a lost test | Unverified lead; re-examine in ticket R55-F6 |
| Cross-user leg of C16-01 | This PC has one account; the verifier confirmed it by reading | Two-account manual check (§6 D7) |
| T13-01 on real macOS | No Mac; the darwin test only cross-compiled | macOS CI run (§6 D6) |
| `-race` | Not runnable here (no cgo); no reviewer asked for a CI race run | None requested |
| C03 assumptions on the 4.2b web layer (`ConfirmBind` rate limits, `EnsureAccount` validation, `LoginURLFromOrigin`) | 4.2b is not built | R-4.2 (§6 D3) |
| C15: install.sh key is the owner's | Not verifiable from code (447f08e/HANDOFF attest it) | Owner knowledge |
| T9: pool use inside a tx in `internal/presence` and `internal/notify/queue.go` | T9 left these unchecked; T2's exhaustive `rows.Next()`/Append scan found no instance | Low risk; check during R55-F19/F22 |
| T12: fetch `req` dedupe across sessions relies on Noise | Left unchecked by T12 | Low risk; T1/T3 confirmed Noise binds peer and session |
| T6b: `capability.GrantIDOf` on a malformed-but-signed token | Used only for the orphan audit | Info |
| C01 coder/websocket write-timeout close | T2 left it open; **T10 resolved it** (`coder/websocket@v1.8.15/conn.go:170-181`) | Closed |

Process note: nine units ran in a reused slot whose context may not have been fully cleared (see
Appendix B). Their findings were not treated differently, but C09–C15 carry that caveat. All
Highs among them (C11-01, C11-04, C14-02, C15-01) were independently verified by fresh workers.

## 3. Ranked findings

Order: severity, then reachability (relay/stranger > paired peer > local agent > other local
user; supply chain is placed by its attacker's position), then blast radius. The format is rubric §4
plus `Aliases`, `Verified` and `Severity reasoning`. Low and Info entries use a compact form
of the same fields, to keep the report readable. The full text is in the cited source reports.

### 3.1 High

### R55-001 · High · confirmed-test
- **Aliases:** C01-02
- **Where:** `internal/relay/relay.go:700-730` (`readFrame`, no per-frame deadline), `relay.go:663-668` (closes the reader that asked), `internal/relay/limits.go:422-433`
- **What goes wrong:** After auth there is no read deadline, and the relay-wide read budget is first come, first served. Connections that start maximum-size frames and never finish them hold the whole budget indefinitely, and every other connection's next frame is closed with 1013.
- **Scenario:** Stranger, one /24 (or one IPv6 /48), ~50 fresh keys (under the 64 per prefix and burst-60 caps). Each opens a ~1 MiB text frame and never sends the last fragment (a one-time upload of 29–45 MiB). Every honest daemon is then closed 1013 on its next mail, ack or presence frame. After 20 reconnects a minute it is refused `rate_limited`.
- **Spec:** envelope.md §Forwarding ("Errors on a single envelope never close the connection"); relay-hosted.md §2 threat model.
- **Fix direction:** Per-frame completion deadline; per-prefix/per-connection share of the read budget; past the budget evict the largest/oldest holder, not the newcomer.
- **Related:** R52 H1 (reopened: "close the reader" choice), O-024, O-025
- **Verified:** `verify/C01-02.md`: confirmed (High). Full-size one-prefix test `zz_review55_C01-02v_test.go`. It applies to 4.1p as configured (Caddy passes WebSockets through with no stream timeout).
- **Severity reasoning:** Rubric High, "a cheap unauthenticated attacker can take the hosted relay down … for everyone". It is cheap: one prefix, no ongoing bandwidth. There is no content, key or code impact, so it is not Critical. Accounts off does not change it.

### R55-002 · High · confirmed-test
- **Aliases:** C01-01, C01-05 (the test gap that let it through)
- **Where:** `internal/relay/conn.go:97-121` (`directEphemeral` → `send` → `charge` with `add`, never refused), `internal/relay/ephemeral.go:95`; effect at `conn.go:211` (`tryAdd`), `conn.go:156` (`reserve`)
- **What goes wrong:** Presence frames are charged to the relay-wide `--max-inflight` budget but never refused by it. Once presence alone spends it, every direct mail send takes the queue path and every drain waits.
- **Scenario:** Stranger with ~190 free keys over ~4 prefixes: ~186 recipients read slowly (enough to keep each 10 s write alive), and 3–10 senders keep 33 × 8 KiB presence frames queued per recipient (within 600/min/key). Honest mail between two online peers gets `queued` and is not delivered while the attack lasts (~200 KiB/s each way). Mail is delayed, not lost.
- **Spec:** presence.md §Relay ("a presence flood from many senders cannot push a recipient's mail into the queue path"); relay-hosted.md §2 memory bound.
- **Fix direction:** Charge ephemeral (and control) pushes with `tryAdd` and drop them when the budget is spent, or give them a small separate budget. Add the missing presence-to-non-reading test.
- **Related:** O-072, O-009, R52 H1 / R50 M2, R55-034 (C01-03, 2× heap)
- **Verified:** `verify/C01-01.md`: confirmed. Default-600/min test `zz_review55_C01-01v_test.go`. 4.1p: yes, and cheapest there (48 MiB flag, no accounts).
- **Severity reasoning:** Same criterion as R55-001, with more keys and some bandwidth, still cheap. Ranked after R55-001 because it costs more.

### R55-003 · High · confirmed-read
- **Aliases:** C15-01
- **Where:** `Docs/ops/release-signing.md` §Every release step 2, `.github/workflows/release.yml:288-325`, `tools/releasesign/main.go:329-363` (`cmdSign` → format-only `checkSums`)
- **What goes wrong:** The owner downloads `SHA256SUMS` from the mutable draft release and signs it. Nothing binds it to the file CI built and tested.
- **Scenario:** Supply chain: a holder of a leaked `contents: write` credential replaces the draft's archives and `SHA256SUMS` before the owner signs them. The owner's runbook passes (`checkSums`, `verify -install-sh`), and every `install.sh` and Homebrew install then accepts the swapped archives with a valid release signature.
- **Spec:** 49 §install ("protects against a swap of the release artefacts on GitHub (a leaked token, …)"); `scripts/install.sh:19-21`.
- **Fix direction:** Bind the signed file to the run: fetch `SHA256SUMS` from the run artifact, or have `releasesign sign` require the digest printed in the immutable run log/summary (or re-hash locally rebuilt archives).
- **Related:** D36/OD-P4-19 (a flaw it did not consider), O-180, O-181, R55-032 (C15-02), R55-110 (C15-04)
- **Verified:** `verify/C15-01.md`: confirmed (High). Tool side tested (`tools/releasesign/zz_review55_C15-01_test.go`); draft mutability is GitHub behaviour, not exercised.
- **Severity reasoning:** The impact is Critical-class, but the actor needs a narrower position (a leaked credential plus the draft window): rubric High. No release has been cut, so there is no exposure yet. It must be fixed before the first release.

### R55-004 · High · confirmed-test
- **Aliases:** C25-01
- **Where:** `cmd/agentnet/decision.go:387` (`printDecisionVerifyHuman`); reason built at `internal/decision/verify.go:180` (raw member name), also `internal/debate/decode.go:322-324`
- **What goes wrong:** `decision verify FILE` (human output) prints the failure reason raw. The reason contains a JSON member name from the file, with `\r`, `\n` or ESC after unescaping. It can overwrite the "invalid" line with a fake "valid, signed by initiator and respondent" verdict and the expected fingerprints.
- **Scenario:** Anyone who hands the user a Decision file (no pairing needed). The exit code (1), `--json` and `--md` stay correct, so only the human reader is fooled, and a human reader is exactly who the command serves (decision.md "compare the fingerprint").
- **Spec:** decision.md §Signed file step 6; rubric invariant 4.
- **Fix direction:** Quote member names (`%q`) in `members`/`fieldErr` errors and pass `Reason` through the shared terminal sanitiser (R55-054); print the verdict on its own line.
- **Related:** O-100, R48 L2, R55-054, R55-055
- **Verified:** `verify/C25-01.md`: confirmed (High); reviewer's test fails for the stated reason.
- **Severity reasoning:** It is not Low: it spoofs the one important output. It is not Critical: nothing machine-checkable is forged. Rubric High, "a core security property breaks … or the impact is bounded", with the weakest actor.

### R55-005 · High · confirmed-read
- **Aliases:** C11-01, T8-01, T8-02, C12-02, C11-06
- **Where:** `internal/daemon/grant.go:616` (grant summary: label, peer name, expiry), `grant.go:960` (policy summary), `internal/daemon/device.go:485-488` (link summary: name only), `internal/daemon/device.go:365-376` (fingerprint taken from IPC params), `internal/daemon/device_scope.go:149-151`, `internal/daemon/session.go:396,533` (accept_result/release: session id only)
- **What goes wrong:** The approval window and notification are the only channel a local agent cannot reach, and for most kinds they identify neither the object nor the peer's key:
  - Grant: a random basename label instead of the resolved path; no scope, branch, public/sensitive flag or fingerprint.
  - Policy: no branch, scope, public flag, `until` or fingerprint.
  - Device link and scope: the peer's self-chosen name only. The fingerprint the flow "types" comes from the calling agent, which reads it from `peers`.
  - Release and accept-result: only an opaque session id.
  - Grant and policy use raw `peer.Name`, without the `stripLongDigits(notify.Clean(…))` the other kinds apply (decoy digits).
- **Scenario:** A prompt-injected local agent:
  - (a) Asks for "share docs/ of app with bob" and gets a whole-repo, `public=true` (quarantine-free) grant, or a 90-day public policy, under a window text identical to the benign request.
  - (b) Links the device to a paired peer whose card name equals the user's other device (`trust=code` via `pair_redeem`, or a team-introduced key), then gets a name-only scope approved. That peer then runs the approved commands on this machine and reads their output.
  - (c) Releases the wrong quarantined session.
- **Spec:** approval.md §The approval window ("the summary (action, resource, peer name and fingerprint, expiry)"); approval.md §Threat model; device.md §Link flow ("each human types the other device's fingerprint"); grant.md §Sensitive grants (the human opts out).
- **Fix direction:** One summary builder for all seven kinds: cleaned name, `stripLongDigits`, grouped fingerprint, full resolved path, branch, scope, sensitive/public, expiry/until, round, request title, with a length check against `notify.MaxWindowSummary`. For device links, consider typing the other device's fingerprint *in the window* (spec change, §6 D9).
- **Related:** O-067 (escalated), O-129 (escalated: its mitigation is absent for grants), C06-04, R55-007, R55-025, R55-066
- **Verified:** `verify/C11-01.md` and `verify/T8-01.md`: both confirmed (High). T8-02 (Medium) and C12-02 (Medium) are the same root cause, merged here.
- **Severity reasoning:** Rubric High, "a core security property breaks, but the attacker needs a narrower position (a local agent via IPC …)", the same class as the rubric's device-scope example. It needs the human to approve, so it is not Critical.

### R55-006 · High · confirmed-test
- **Aliases:** C11-04, T4-01, T13-01, C11-02, C14-01; consolidator note on `grant.go:148-157`
- **Where:** `internal/daemon/grant.go:133-162` (`validateResource`), `:170-186` (`foldPath`, `pathEqualOrContains`), `:209-216` (`isFilesystemRoot`); serving `internal/capability/fs.go:231` (`os.OpenRoot`); also `internal/daemon/device_scope.go:136` (scope repos) and `internal/device/runner.go:180-190` (`CheckTarget`, run-time repo re-check)
- **What goes wrong:** grant.md §Issuance step 3 refuses the config dir, the home dir and filesystem roots by comparing folded **spellings**. These pass:
  - Windows: `\\?\C:\…`, `\\?\UNC\…`, `\\localhost\C$\…`, `\\?\Volume{GUID}\…`, a `subst` or mapped drive letter, and a junction (Go ≥ 1.23 `EvalSymlinks` does not resolve mount points; the same gap lets a junction-swapped repo pass `CheckTarget`).
  - macOS: `/System/Volumes/Data/Users/<me>/…` (firmlink), including `/System/Volumes/Data` itself, which is not `/`.
  - If `EvalSymlinks(configDir)` or `EvalSymlinks(home)` fails, that refusal is skipped (fail-open; my read of `grant.go:148-157`).
- **Scenario:** A prompt-injected local agent with an open requester session calls `grant_create` (or `grant_policy_add`, or a scope repo) with such a spelling of the config dir. The window shows `fs.read on dorylinae-xxxx to bob` (R55-005). After approval, the peer reads `dorylinae.db` (all mail, requests, approvals, audit), key files if the file keystore is used, `~/.ssh` via the home dir, or the whole macOS data volume. `recheckResource` uses the same function, so fetches keep working.
- **Spec:** grant.md §Issuance step 3 ("Refused: the config dir … or any path inside it or containing it, the user's home directory itself, and a filesystem root"); device.md §Running (a repo replaced by a junction is refused).
- **Fix direction:** Compare by **file identity**: open the resource and walk its ancestors with `os.SameFile` against the config dir and home. On Windows also normalise with `GetFinalPathNameByHandle`, and refuse reparse points at the root. Treat any mount point as a root (`st_dev` differs from the parent). Fail closed when the config dir or home cannot be resolved. A prefix denylist is not enough (T4-01: `subst`, mapped drives and volume GUIDs are not prefixes).
- **Related:** O-135, O-151, O-162, R55-027 (UNC dial), R55-028 (`.git`), R55-088 (canonical dir spelling)
- **Verified:** `verify/C11-04.md` confirmed (High, test reads `SECRET-DB`). `verify/T13-01.md` confirmed by reading; the darwin test `zz_review55_T13-01_test.go` has not run, so **a macOS CI run settles it** (§6 D6). T4-01 is confirmed by test (`subst` and volume-GUID read `SECRET-DB`). The verifier's lead that `device_scope.go:136` has the same bypass is folded in.
- **Severity reasoning:** Rubric High ("a core security property breaks … local agent via IPC"). It needs a human approval, which R55-005's label makes easy to give. Windows is the owner's platform (§2: not lowered).

### R55-007 · High · confirmed-test
- **Aliases:** C14-02 (escalates O-167)
- **Where:** `internal/daemon/device_scope.go:31,209-212` (`maxScopeSummary` 16384 bytes), `internal/notify/window.go:27,39` (`Clean(summary, 4096)`)
- **What goes wrong:** A scope summary up to 16384 bytes is accepted, but every window shows only 4096 code points plus "…". Commands after the cut, and the closing "Confirm only if you set this scope yourself", are never shown.
- **Scenario:** A local agent on the helper sets a scope whose first command has a ~4000-character argument and whose last command is the one it wants. The human approves what looks like a test scope, and the controller can then run the hidden command.
- **Spec:** device.md §Scope ("because the approval window must show all of it").
- **Fix direction:** Refuse summaries longer than `notify.MaxWindowSummary` runes, as `debate_constrain.go:91-97` does (R46 H2).
- **Related:** O-167, R46 H2, R55-005
- **Verified:** `verify/C14-02.md`: confirmed (High).
- **Severity reasoning:** This is the rubric's own High example class (a scope change the human never saw), rated High for debates in R46 H2.

### R55-008 · High · confirmed-test
- **Aliases:** C16-01 (R5 lead 1)
- **Where:** `internal/ipc/transport_windows.go:21-50` (`Listen`, `Dial`), name from `internal/paths/paths.go:68-72`; `cmd/agentnetd/main.go:115-121,135-143`
- **What goes wrong:** The Windows pipe name is predictable (a hash of the profile path) in the machine-global namespace, and neither side checks the pipe owner.
- **Scenario:** Another local OS user creates `\\.\pipe\dorylinae-<id>` while the victim's daemon is down (before logon, after a crash or `stop`). The victim's `agentnetd run` then exits "already running", and the victim's CLI and agents send every request (mail bodies, request and debate text) to the squatter and accept its forged results (a prompt-injection channel). Client impersonation is not possible (anonymous level).
- **Spec:** ipc.md §Endpoint ("Pipe DACL grants access to the current user's SID only"); rubric §1 (another OS user must never read the IPC endpoint).
- **Fix direction:** The client checks the server's owner SID (`GetNamedPipeServerProcessId` plus token user, or `GetSecurityInfo`) before sending. Put the user SID in the name, and report "pipe held by another user" on a `Listen` failure.
- **Related:** R55-088 (C16-05 spelling → two pipes), O-203
- **Verified:** `verify/C16-01.md`: confirmed (High) in a single-user test. The cross-user leg is confirmed by reading only; a two-account manual check settles it (§6 D7).
- **Severity reasoning:** Rubric High: content reaches another OS user, and the daemon is denied. The actor needs a local account and a daemon-down window, so it is not Critical, and not "owner's own mistake".

### 3.2 Medium

### R55-009 · Medium · confirmed-test
- **Aliases:** T10-02
- **Where:** `internal/relay/relay.go:651-660` (each new connection drains from cursor 0), `relay.go:885-921` (`drainStep`), `internal/relay/queue.go:333-355` (rows stay until ack), `internal/relay/limits.go:35` (20 reconnects/min/key)
- **What goes wrong:** Queued frames are re-sent from the start on every reconnect until acked, and nothing limits bytes delivered or redelivered per key.
- **Scenario:** Stranger, one /24, no accounts. It queues 32 MiB to each of 12 of its own offline keys (384 MiB, within the per-prefix byte limit and the 1 GiB cap). Each key then connects, reads, never acks and reconnects 20 times a minute. The relay sends ~7.5 GiB/min, which saturates the VM uplink and Hetzner's traffic allowance, and loads the single DB connection. Test: 7 MiB uploaded once, 291 MiB downloaded in 2 fake minutes.
- **Spec:** relay-hosted.md §2 bounds only bytes sent; rubric §1 stranger row.
- **Fix direction:** A per-key/per-prefix delivered-bytes budget that counts redeliveries, or no re-send of a row to the same key within N minutes / more than K times without an ack.
- **Related:** O-015 (escalated: this is the step after the fill), O-028, R55-001
- **Verified:** not verified (Medium, confirmed-test).
- **Severity reasoning:** The theme rated it Medium. **Borderline High:** it is a stranger taking the hosted relay's bandwidth down for everyone, but it needs matching download capacity, which is "many resources". I keep Medium and recommend a verification if the owner wants the rating settled (§6 D2). It applies to 4.1p as configured.

### R55-010 · Medium · confirmed-read
- **Aliases:** T6a-02
- **Where:** `internal/envelope/envelope.go:46-53,117-124` (`Header`/`ParseHeader` ignore `payload`), `internal/relay/relay.go:755-796`; client `internal/relayclient/relayclient.go:460-463` (Parse fails, returns before the ack at `:483`)
- **What goes wrong:** The relay queues an envelope whose `payload` is not base64. The recipient cannot parse it, so never acks it, and it survives every reconnect for the whole 7-day TTL.
- **Scenario:** Stranger with four keys (O-015) fills an offline victim's 1000-row/32 MiB queue with junk. Legitimate senders get `queue_full` for 7 days instead of "until the next connect", and the victim re-downloads and logs 1000 Warn lines per reconnect.
- **Spec:** envelope.md §Envelope ("`payload` … standard base64"), §Forwarding step 1 (`bad_envelope`).
- **Fix direction:** The relay refuses a non-string/non-base64 payload with `bad_envelope`; the client acks frames it cannot parse.
- **Related:** O-015 (escalated), O-053, R55-016
- **Verified:** not verified. The path is short and read end to end.
- **Severity reasoning:** Bounded DoS against one victim at a time (Medium). Applies to 4.1p (accounts off).

### R55-011 · Medium · confirmed-test
- **Aliases:** C02-02
- **Where:** `internal/relay/queue.go:381-402` (`sweep`: one `DELETE … RETURNING` under `q.mu`, 10 s ctx), `:210` (DSN: `secure_delete`, no `journal_size_limit`)
- **What goes wrong:** Expired rows are deleted in one statement on the single connection. A large batch stalls all queue work for >10 s and leaves a WAL as large as the batch. At 2 GiB it rolls back and livelocks every minute.
- **Scenario:** ~16 prefixes queue 1 GiB within one minute (accounts off). Seven days later there is an 11–14 s stall and a ~1 GiB WAL that stays (early relay). Under the 4 GiB default, `queue_full` for everyone until an operator intervenes.
- **Spec:** relay-hosted.md §2 ("acks and deletes still work"), §3.
- **Fix direction:** Batched deletes in a loop; `journal_size_limit` or `wal_checkpoint(TRUNCATE)` after large sweeps.
- **Related:** O-015, O-027, O-024
- **Verified:** `verify/C02-02.md`: confirmed (Medium). Measured on NVMe.
- **Severity reasoning:** Needs many prefixes and precise timing, so Medium (close to High under default flags). Applies to 4.1p (1 GiB cap: a stall, not a livelock).

### R55-012 · Medium · confirmed-test (lowered from High)
- **Aliases:** C03-01
- **Where:** `internal/relay/accounts_store.go:234-268` (`createBind`), `:458-463` (`prune` keeps rows 24 h after expiry), `internal/relay/accounts.go:324-344`
- **What goes wrong:** Each `bind_start` from an unbound key writes a durable row (~425 B) that lives ≥ 24 h 10 min. There is no bind rate limit and no disk cap.
- **Scenario:** Stranger, `--accounts` on: 2–4 prefixes fill the 2–3 GB volume in about a day. The queue then refuses mail below the free-disk floor, and fsync'd inserts load the single connection.
- **Spec:** accounts.md §Frames; relay-hosted.md §2 threat model.
- **Fix direction:** Reuse or overwrite the key's single bind row, delete cancelled and expired rows at once, and add a per-key/per-prefix `bind_start` limit.
- **Related:** O-187, O-027
- **Verified:** `verify/C03-01.md`: confirmed (High as if accounts on). **Not reachable on 4.1p** (accounts off: `pairing.go:146-149` closes on the frame).
- **Severity reasoning:** Rubric High as if on. **Lowered one level** by the consolidator: accounts are off by default and unreviewed (R-4.2 pending). This is a gate for enabling accounts (§6 D3).

### R55-013 · Medium · confirmed-test (lowered from High)
- **Aliases:** C02-01
- **Where:** `cmd/relay/main.go:307-312` (`runRestore`: `os.Stat(*from).ModTime()` as the backup time), `internal/relay/journal.go:111-114` (the cited `:216-219` does not exist)
- **What goes wrong:** `relay restore --replay-journal` takes the backup time from the `--from` file's mtime. A downloaded and decrypted backup is always newer than every journal entry, so nothing is replayed, silently ("replayed 0 … no handler yet"). Separately, even the original file's mtime marks the end of `VACUUM INTO`, not its start.
- **Scenario:** After a DB loss, the operator follows the spec (download, `age -d`, restore). Unbinds, deletions and suspensions made after the backup are rolled back: a stolen device's key is bound again, an erasure is undone.
- **Spec:** relay-hosted.md §3 Restore ("re-applies every journal entry newer than the backup").
- **Fix direction:** Record the backup start inside the backup (a `relay_meta` row written before `VACUUM INTO`), or require `--since`. Replay with a margin (the handlers are idempotent) and count entries skipped by time.
- **Related:** review 50 M6, O-187, R55-048 (T9-03), R55-044 (C03-04)
- **Verified:** `verify/C02-01.md`: confirmed, with a control test showing the handler works on the original file.
- **Severity reasoning:** High as if accounts on. **Lowered one level:** accounts are off by default, and the 4.1b backup job is not deployed (D40). Gate for accounts and 4.1b (§6 D3). Not reachable on 4.1p.

### R55-014 · Medium · confirmed-test
- **Aliases:** C04-01, C28-01, T5-01, T1-02, C06-02
- **Where:** root `internal/relayclient/wire.go:28-32` / `internal/envelope/frames.go:178,338` (relay `error` frame text unbounded, unfiltered); sinks: `internal/relayclient/relayclient.go:223-229` (`lastErr` and the daemon log), `internal/daemon/status.go:41` → `cmd/agentnet/main.go:256-261`, `cmd/agentnet/doctor.go:356-359`; `internal/session/session.go:319-331,800-806` → `cmd/agentnet/ping.go:99-101`; `internal/peers/pairing.go:571-575,1042-1043` → `cmd/agentnet/pair.go:121-125` and the `pair.fail` audit
- **What goes wrong:** Relay-chosen `code`/`message` text is stored, logged and printed raw in four places: `status`/`doctor`, `ping`, `pair` plus its audit row, and the daemon log. It is up to 1 MiB with any characters, except pairing, which is capped at 64/200 bytes.
- **Scenario:** A hostile or compromised relay:
  - (a) Answers one upgrade with an `error` whose message carries ESC sequences or an OSC 8 link. Every `agentnet status`/`doctor` then prints them (fake "connected" lines, a link to a relay-chosen URL).
  - (b) Sends a ~1 MiB message of `<`, which is ×6 through the IPC encoder (R55-024). `status` then fails and `doctor` reports the daemon down.
  - (c) Makes each such line ≥ 1 MiB, which rotates the daemon log, so two reconnects erase its history.
  - (d) Sends pairing errors that print a fake "Paired with alice" and land in the audit log.
- **Spec:** `Docs/cli/status.md:94-96` (`last_error` "content-free"); rubric §1 relay row (browser/opener to a relay URL, disk growth); invariant 4.
- **Fix direction:** Fix it once in `relayclient`: keep only a known `code` from a fixed set, and clip and sanitise `message` before any consumer. Apply the shared terminal sanitiser in `status`, `doctor`, `ping` and `pair`.
- **Related:** R53 L4 (the same fix given to `min_client`), review 50 H3, O-100, R55-024
- **Verified:** not verified (Medium). Confirmed by tests in C04 and C28.
- **Severity reasoning:** The relay actor, with display and diagnostic harm only (it needs a click to reach a URL). Medium: bounded, and it does not hide a security verdict (contrast R55-004).

### R55-015 · Medium · confirmed-test
- **Aliases:** T10-01, C07-02
- **Where:** `internal/mail/open.go:98-101`, `internal/mail/audit.go:47-74` (30/min then `Append`), `internal/session/session.go:533-541,893-919` (unpaired session frames go to `reject`, 30/min); `internal/store/store.go:22-33` (append-only triggers); strings from `audit.go:544,565`, `receiver.go:165`
- **What goes wrong:** A relay makes the daemon append 30 `mail.reject` plus 30 `session.reject` rows a minute forever, from unpaired keys, into an append-only hash-chained table nothing can prune. The rows also carry relay-chosen envelope `id`s, and `mail.in` rows carry peer-chosen unknown `kind`s (charset-limited).
- **Scenario:** A hostile relay injects one junk `mail` and one junk `session.data` frame per second: ~29 MB/day, ~10.5 GB/year of owner disk that can never be pruned. Genuine rejects from real peers are crowded out of the 30/min budget.
- **Spec:** audit.md:294 (the no-prune decision assumed only the user's own activity); rubric §1 relay row.
- **Fix direction:** Do not audit rejects of frames from unpaired keys (count them in the log's suppressed line), or aggregate them per window. Audit `id` only when valid and log unknown kinds as `unknown`.
- **Related:** O-051, O-171, R55-016, R55-042 (C08-05)
- **Verified:** not verified (Medium, confirmed-test `internal/audit/zz_review55_T10-01_test.go`).
- **Severity reasoning:** Unbounded disk growth driven by the relay, but slow. Medium, not High (it does not take the daemon down quickly). **Reopens the audit no-prune decision** for relay-driven rows (§6 D10).

### R55-016 · Medium · confirmed-read
- **Aliases:** C16-02 (R5 lead 2)
- **Where:** `internal/service/launchd.go:200,224-227` (stderr to `<home>/agentnetd.log`, no `--log-file`), `internal/relayclient/relayclient.go:442,462` (one Warn per bad frame, no limit)
- **What goes wrong:** On macOS the daemon's log is the unrotated launchd stderr file, and the relay can make the daemon write a line per frame.
- **Scenario:** A hostile relay streams tiny malformed frames (~50× amplification into the log). The file grows by GBs per hour until the disk fills. Windows (1 MiB rotation) and Linux (journald) are bounded.
- **Spec:** `Docs/cli/agentnetd-install.md` §Logs ("(not rotated)": chosen without the relay as an actor, **reopens** it); rubric §1 relay row.
- **Fix direction:** Pass `--log-file` in the plist too, and rate-limit per-frame relay warnings.
- **Related:** O-051, R55-042, R55-014
- **Verified:** not verified (Medium, read).
- **Severity reasoning:** Unbounded disk growth from relay input on one OS: Medium.

### R55-017 · Medium · confirmed-test
- **Aliases:** T9-01 (escalates O-077)
- **Where:** `internal/mail/receiver.go:195-201` (dedupe on `mail_seen` only), `:221-225` (`mail_inbox` insert after Apply), `:320-327` (`mail_seen` pruned at 35 d; `mail_inbox` never); leak e.g. `internal/request/receive.go:176-182`
- **What goes wrong:** After 35 days, a mail id leaves `mail_seen` but stays in `mail_inbox`. A new mail reusing it passes dedupe, runs Apply fully, fails on the inbox primary key, rolls back and is never acked. Each attempt leaves its `*mail.Opened` (up to ~320 KiB) in a process-global `pending*` map.
- **Scenario:** A paired peer (paired > 35 d) re-seals one of its own old ids with a fresh `created` and sends it repeatedly. Heap grows ~30–60 MiB/min at the relay's per-key rate until the daemon runs out of memory. Nothing is logged (R55-058).
- **Spec:** mail.md §Dedupe ("Pruning can never re-admit a replay": that covers relay replays only); rubric §1 paired-peer row.
- **Fix direction:** Dedupe on `mail_seen` or `mail_inbox` (or `INSERT OR IGNORE` the inbox row, treating a hit as a duplicate). Prune both together. Drop `pending*` entries on a failed tx (pass outcomes through `op`).
- **Related:** O-077, O-058, O-059, R55-058, R55-021, R55-067 (C22-01)
- **Verified:** not verified (Medium, test `internal/mail/zz_review55_T9-01_test.go`).
- **Severity reasoning:** Memory DoS of the daemon by a paired peer ("bounded DoS that needs a paired peer"): Medium.

### R55-018 · Medium · confirmed-read
- **Aliases:** T10-03 (escalates O-058)
- **Where:** `internal/mail/receiver.go:221-225` (`mail_inbox.signed`), `internal/request/receive.go:270-291` (only high/blocking budgeted), `internal/request/request.go:12-16`; no `DELETE FROM requests|mail_inbox` in production code
- **What goes wrong:** Every request is stored twice (`requests.body` plus `mail_inbox.signed`) forever, with no per-peer cap for low/normal urgency.
- **Scenario:** A paired peer (modified, buggy, or its agent in a loop) sends 320 KiB `question`s at normal urgency: ~85 GB/day through the hosted relay's per-key limits, unbounded through a loopback relay. When the disk fills, every DB write fails, and no CLI command can remove the rows.
- **Spec:** the rubric's own Medium example ("A paired peer grows `mail_inbox` without bound").
- **Fix direction:** Per-peer caps on stored incoming requests; blank `mail_inbox.signed` after apply; a user prune command. This needs the retention decision (O-171, §6 D12).
- **Related:** O-058, O-093, O-171, R55-023, R55-064 (C09-03)
- **Verified:** not verified (Medium, read).
- **Severity reasoning:** The rubric example itself: Medium.

### R55-019 · Medium · confirmed-test
- **Aliases:** T1-01
- **Where:** `internal/agentcard/agentcard.go:205-213` (`Verify` decodes the canonical bytes into `Card` with `encoding/json` after checking the signature over the exact-name generic card); root `internal/agentcard/canonical.go:69-72` (only byte-identical duplicates are refused); consumers `internal/team/kinds.go:353-359`, `internal/peers/pairing.go:642-648,690-692`
- **What goes wrong:** `encoding/json` folds member names (ASCII case, U+212A→k, U+017F→s) and keeps the last one. A card signed by K1 that also carries `"public_Key": K2` verifies under K1 and returns `Card.PublicKey = K2`. The self-signature no longer binds the key or the fields.
- **Scenario:** A paired peer that owns a team puts K2 (any key, including a real person the recipient knows) in a roster with a card K2 never signed and a name of the owner's choice. Every member's daemon introduces or overwrites K2 under that name and harness. Independent verifiers (`tools/verifycard`) see a different card.
- **Spec:** agent-card.md §Verification; team.md (roster cards are the members' own signed cards); invariant 4.
- **Fix direction:** Build `Card` from the generic map by exact name with an exact member count (as `capability.decodeGrant` does), or refuse keys that fold together in `ParseStrict`. At least compare the struct's `PublicKey` with the signed `card["public_key"]`.
- **Related:** O-043 (escalated in effect), O-067, R55-005 (name spoofing feeds the name-only approvals), T6a-22
- **Verified:** not verified (Medium, test `internal/agentcard/zz_review55_T1-01_test.go`).
- **Severity reasoning:** A parser differential in a signed object. The actor is a paired team owner, and no secret or grant follows directly; it is identity display spoofing of a real key. Medium. The consolidator considered High ("forging an identity"), but K2's key is still not usable by the attacker.

### R55-020 · Medium · confirmed-test
- **Aliases:** C07-01
- **Where:** `internal/mail/outbox.go:530-582` (`Retry`), triggered by `internal/mail/keys.go:40-52`; `internal/peers/store.go:179` (accepts any newer `created`)
- **What goes wrong:** Each `keys` mail carrying a newer announcement and a `retry` list makes the sender re-seal and re-upload every listed open row at once, ignoring backoff. There is no limit on frequency.
- **Scenario:** A hostile paired peer lets A's mail to it pile up, then sends a stream of `keys` mails, each with an announcement one second newer and up to 256 row ids. A uploads up to 256 × ~1 MB per small mail and burns its per-key relay budget, so its mail to others gets `rate_limited`.
- **Spec:** mail.md §Key-miss recovery 3 ("prevents loops"); R10's "no amplification" claim is wrong for a hostile peer.
- **Fix direction:** Re-seal a row at most once per key-miss window; cap accepted announcements per peer per hour.
- **Related:** R10 M1, O-055, O-056
- **Verified:** not verified (Medium, test).
- **Severity reasoning:** Bandwidth amplification needing a paired peer: Medium.

### R55-021 · Medium · confirmed-test
- **Aliases:** C24-01
- **Where:** `internal/debate/apply.go:503-511` (close `constraints` only pattern-checked), `internal/debate/decision.go:72-84`, `apply.go:626-628` (a `TooLargeError` is returned as a plain error)
- **What goes wrong:** B accepts a `debate.close` whose `constraints` list repeats ids without bound. The derivation exceeds `MaxDecision`, and the plain error rolls the mail back unacked, which makes it a poison mail.
- **Scenario:** A modified initiator repeats one constraint id 400 times. B's debate hangs at `waiting: "signature"`, every redelivery for 7 days costs a full derivation, and nothing is logged or shown. A short repeat instead signs a Decision with a duplicated human decision.
- **Spec:** decision.md §Signing step 1 ("sorted list of ids"), §Size; debate.md (≤ 10 active).
- **Fix direction:** Refuse unsorted, non-unique or oversized lists as `bad_body`; map `TooLargeError` on B to a refusal.
- **Related:** O-165, O-175, R55-058
- **Verified:** not verified (Medium, test).
- **Severity reasoning:** Paired-peer-driven stuck state that the user must repair by hand: Medium.

### R55-022 · Medium · confirmed-test
- **Aliases:** C21-01
- **Where:** `internal/worksession/mirror.go:212` (B completes with its own last `row.result`), `internal/worksession/submit.go:58,78` (a second `ws_result` overwrites), `experience.go:108`
- **What goes wrong:** B completes the request with its *last submitted* result, not the one A accepted. A's request record then holds a result neither A's agent nor A's human reviewed, outside the quarantine rule (the session is closed).
- **Scenario:** B (even unmodified, revising its answer) submits R1 then R2 while A is offline. A accepts R1 (R2 is ignored). B's close sends `request.complete{R2}`, and A stores R2 in `requests`.
- **Spec:** work-session.md §Closing ("`result` = the D14 part of the **accepted** result").
- **Fix direction:** Refuse a second `ws_result` per round on B, or carry the accepted result's hash in the closing `ws.state` and compare it on A.
- **Related:** O-148, O-121, R55-029 (T8-03)
- **Verified:** not verified (Medium, test).
- **Severity reasoning:** Integrity of what the requester stores, reachable by a peer in normal use: Medium.

### R55-023 · Medium · confirmed-test
- **Aliases:** C19-01
- **Where:** `internal/daemon/request_lifecycle.go:178` (list views carry the full brief), `:462-510`; `internal/request/inbox.go:190`, `query.go:116` (no LIMIT); client `internal/ipc/ipc.go:28,218,258`
- **What goes wrong:** `inbox_list`/`request_list` carry full briefs (≤ 16 KiB each) with no paging. Past 1 MiB, the CLI fails with "line too long".
- **Scenario:** A paired peer sends 64 normal-urgency tasks with 16 KiB briefs. `agentnet inbox` then fails, and the user cannot see which ones to decline. Heavy users reach the same state over time, since rows are never pruned.
- **Spec:** ipc.md §Requests ("omit `output` … so list results stay small"), §Framing (1 MiB).
- **Fix direction:** Omit `brief` from list views (like `output`), or page and report truncation.
- **Related:** O-178, O-093, O-058, R55-024
- **Verified:** not verified (Medium, test).
- **Severity reasoning:** A user-visible stuck view caused by a peer: Medium.

### R55-024 · Medium · confirmed-test
- **Aliases:** T6a-01, T6c-01
- **Where:** `internal/ipc/ipc.go:140-155` (`serveConn` uses a default `json.NewEncoder`, which re-escapes the `json.RawMessage`); the intended fix `:194-205` (`marshalResult`) is undone
- **What goes wrong:** The review 43 M7 guarantee ("results encoded with HTML escaping off") does not hold on the wire. Every `<>&` in a result becomes a 6-byte escape.
- **Scenario:** A paired peer fills its debate entries with `<` (≈224 KiB raw, ≈1.3 MB on IPC). `debate show`/`wait` then fail, every `debate_submit` commits and then errors (the agent retries into `not_your_turn`), and after the close the signed Decision cannot be shown or exported through IPC, permanently. The same bug multiplies R55-014 and R55-023.
- **Spec:** ipc.md §Framing (3.1b, R43 M7); debate.md §IPC Size; decision.md §Size.
- **Fix direction:** `SetEscapeHTML(false)` on the connection encoder, plus an end-to-end `ipc.Call` size test.
- **Related:** R43 M7 (reopened in effect), O-178, O-179, C16's "checked and fine" entry for `ipc.go:197-205` is wrong
- **Verified:** not verified (Medium, two tests).
- **Severity reasoning:** A permanent loss of access to one's own Decision, driven by a peer: Medium.

### R55-025 · Medium · confirmed-test
- **Aliases:** T11-01; verifier lead (zenity mnemonic)
- **Where:** `internal/notify/window_linux.go:128-130` (`escapeArg` = `escapeMarkup` only), `:186-190` (zenity `--text`); summaries from `debate_constrain.go:57-70`, `device_scope.go:149-151`, `grant.go:616,960`, `device.go:488`
- **What goes wrong:** zenity passes `--text` through GLib `g_strcompress`. A backslash escape in peer text (a card name like `Bob\0`) becomes a NUL that cuts the window text, or newlines, bidi or invalid bytes, all after `notify.Clean`. The daemon doubles backslashes for notify-send only (R21 M1). **Lead from the verifier:** zenity `--entry` sets the label with a *mnemonic*, not markup, so `escapeMarkup` shows `&amp;` literally and `_` is consumed. The premise of approval.md:177 ("markup-escaped for both tools") is wrong.
- **Scenario:** A paired peer named `Bob\0` plus a prompt-injected agent calling `debate_constrain`: the Linux window reads only "…with Bob". The constraint and the fixed warning sentence are cut, and a fake human decision gets signed. The same name can cut a scope summary after "let Bob".
- **Spec:** approval.md §The approval window, Linux row (line 177); R46 H2.
- **Fix direction:** Escape per zenity's real parser: double `\`, and do not markup-escape a mnemonic label (escape `_` instead). Add a test like `TestShowDesktopLinuxNotifySendBodySurvivesStrcompress`. Fix the approval.md:177 text.
- **Related:** R21 M1, R30 L1, R55-005, R55-066 (decoys)
- **Verified:** `verify/T11-01.md`: confirmed (Medium), from upstream zenity 3.42/4.0 and GLib 2.80 sources plus a Go port of `g_strcompress`. kdialog unchecked. The reviewer's original test was overwritten by the verifier's port (noted in the verify report).
- **Severity reasoning:** It deceives the human approval but does not bypass the code. Linux/zenity only. Medium, "spec deviation with security impact".

### R55-026 · Medium · confirmed-test
- **Aliases:** C27-01
- **Where:** `internal/notify/dial.go:245-257` (`newHTTPClient` per attempt), `internal/notify/webhook.go:203-213`
- **What goes wrong:** Every webhook attempt creates a new `http.Transport` that is never closed. Each successful delivery leaves an idle connection and two goroutines alive while the receiver keeps it open.
- **Scenario:** A paired peer's request stream against a receiver that never closes idle connections grows fds until they run out. The relay connection and IPC then fail, and with R55-083 (Accept fatal) the daemon exits.
- **Spec:** notify.md §Delivery; rubric Medium ("resource leak that grows with peer input").
- **Fix direction:** One shared transport per resolver, or `DisableKeepAlives`, or `CloseIdleConnections()` after each attempt.
- **Related:** O-117, O-119, R55-083
- **Verified:** not verified (Medium, test).
- **Severity reasoning:** The rubric Medium example class.

### R55-027 · Medium · confirmed-read
- **Aliases:** C11-03, T4-04; verifier lead in `verify/C11-04.md`
- **Where:** `internal/daemon/grant.go:137-144` (`EvalSymlinks`/`os.Stat` on the raw resource), called before any approval from `grant.go:420` (`grant_create`) and `:885` (`grant_policy_add`); `internal/daemon/device_scope.go:134-141` and `internal/device/scope.go:316-340` (`device_scope_set` resolves repos and absolute `argv[0]`)
- **What goes wrong:** A UNC resource or program path makes the daemon open an SMB session to a host the caller chose, with the user's NTLM credentials, before any human is involved.
- **Scenario:** A prompt-injected local agent calls `grant_policy_add` (one paired peer is enough, no session) with a `\\host\share` path. The daemon sends a Net-NTLMv2 response to that host. On a helper device, `device_scope_set` does the same.
- **Spec:** approval.md threat model (a local agent may not make the daemon act before approval).
- **Fix direction:** Refuse UNC and device-namespace paths (`VolumeName` starting with `\\`) before touching the filesystem, in `validateResource` and for `argv[0]`.
- **Related:** O-151 (same class via git), R55-006
- **Verified:** not verified. The dial was observed in the C11-04 test (`\\127.0.0.1\C$` resolved).
- **Severity reasoning:** Credential material leaves the machine without approval. The actor is a local agent, and the impact depends on NTLM cracking or relaying. Medium (as reported).

### R55-028 · Medium · confirmed-read
- **Aliases:** C11-05
- **Where:** `internal/daemon/grant.go:133-162`; serving `internal/capability/fs.go:178-205` (`walk` checks `.git` only below the root)
- **What goes wrong:** A resource that *is* a `.git` directory, or lies inside one, is not refused, and everything under it is served.
- **Scenario:** A local agent requests `fs.read` on `/work/app/.git`. After approval of a `.git-ab12` label, the peer reads `.git/config` (tokens in remote URLs), all objects and hooks.
- **Spec:** grant.md §Resource kinds ("Not served: anything under a `.git` directory").
- **Fix direction:** At issuance, refuse any resolved path with a `.git` component (same `isGitName` rule) and bare-repo dirs for `fs.read`.
- **Related:** R55-006, R55-005
- **Verified:** not verified.
- **Severity reasoning:** Approval-gated, and the label shows `.git`, which keeps it Medium.

### R55-029 · Medium · confirmed-read
- **Aliases:** T8-03
- **Where:** `internal/daemon/session.go:286-361` (`ws_result`: worker role is the only check), `internal/request/lifecycle.go:315-329`, `internal/worksession/submit.go:45-60`; the runner uses the same entry (`internal/daemon/device_run.go:442,535`)
- **What goes wrong:** An auto-accepted run session on a helper is an ordinary worker session. The helper's local agent can submit its own `ws.result` (`pass`, `tests_passed`) and beat the runner.
- **Scenario:** A prompt-injected agent on the helper (for example injected by the repo it works in) sees the run session in `ws_list` and submits a forged green result, which the controller accepts.
- **Spec:** device.md §Running (the runner sets the status from the exit code, `verification = none`).
- **Fix direction:** Mark run sessions and refuse `ws_result`, `request_complete` and worker `ws_cancel` on them over IPC (keep the runner's path).
- **Related:** R55-022 (A applies the first result), C14-03
- **Verified:** not verified.
- **Severity reasoning:** Forged integrity signal via a local agent: Medium.

### R55-030 · Medium · confirmed-test
- **Aliases:** C12-01
- **Where:** `internal/approval/store.go:943-961` (`List` takes `s.mu` per row with a cursor open), against `Create`/`confirm` (hold `s.mu`, then need the connection); `internal/store/store.go:546` (one connection)
- **What goes wrong:** Lock-order deadlock between the only SQLite connection and `Store.mu`. The whole daemon hangs permanently. When the holder is the window's `confirm` (Background ctx), it also survives shutdown (T2).
- **Scenario:** `approve --list` while a `grant_create` runs, or while the human's window answer is being confirmed: normal use. A prompt-injected agent can trigger it on demand.
- **Spec:** none (`internal/request/query.go:136-139` documents the pitfall).
- **Fix direction:** Collect rows and close the cursor before `windowState`.
- **Related:** O-127, T2 (no second instance in the codebase)
- **Verified:** not verified (Medium, test deadlocks within 3 s).
- **Severity reasoning:** Availability of the whole daemon from a local agent or plain bad luck: Medium.

### R55-031 · Medium · confirmed-test
- **Aliases:** C06-01 (escalates O-034)
- **Where:** `internal/peers/pairing.go:379-395` (`startKDFLocked`), `:455-498`, `:417-428` (`maxPending` counts sessions)
- **What goes wrong:** Each v2 pairing starts an uncancellable 64 MiB Argon2id goroutine. A session that fails at once frees its slot while its derivation runs on.
- **Scenario:** A local agent loops `pair_redeem` with random codes: 40 calls in 0.76 s gives ~2 GiB heap. A tighter loop drives the daemon to OOM.
- **Spec:** pairing.md §Local timers ("at most 16 pending"), §KDF (64 MiB each).
- **Fix direction:** A semaphore on concurrent derivations; start the KDF only after the relay accepted the frame.
- **Related:** O-034
- **Verified:** not verified (Medium, test).
- **Severity reasoning:** Local-agent DoS of the daemon: Medium.

### R55-032 · Medium · confirmed-read
- **Aliases:** C15-02 (escalates O-185)
- **Where:** `.github/workflows/phase2-harness.yml:21-22,35`, `phase3-harness.yml:16-17,30`, `sensitive-paths.yml:18` (tag-pinned actions); `.github/workflows/release.yml:203-218` (`sums` job restores the Go cache, runs `go run ./tools/releasesign`, uploads `dist/`)
- **What goes wrong:** Three workflows still use mutable action tags, and the release `sums` job runs code built from a default-branch Go cache before it uploads the release files.
- **Scenario:** Supply chain: a retargeted action tag in a scheduled `main` workflow poisons the Go cache. The next tag's `sums` job runs the poisoned `releasesign` and rewrites `dist/` after the build checks, and the owner then signs it (R55-003).
- **Spec:** rubric §1 supply-chain row; R53 L7/L13.
- **Fix direction:** `cache: false` in every release.yml job; pin all actions by SHA.
- **Related:** O-185, R53 L7, R55-003
- **Verified:** not verified.
- **Severity reasoning:** A multi-step supply-chain path, each step plausible: Medium.

### R55-033 · Medium · confirmed-read
- **Aliases:** C08-01
- **Where:** `cmd/agentnetd/main.go:114` (no `Options.Idle`), `internal/daemon/daemon.go:181,368`, `internal/presence/sender.go:205-207`
- **What goes wrong:** `internal/idle` is never wired in production. `human_present` is always `null` on every OS, and the spec's 5 s cache is missing too.
- **Scenario:** Two active users in a team always see `human_present: null`, and `presence --human` has no effect. CI hides it with a fake `Idle`.
- **Spec:** presence.md §Levels, §Idle detection; HANDOFF lists 1.2d as done.
- **Fix direction:** Wire `idle.Idle` behind a 5 s cache and test the default.
- **Related:** T6a-11, C08-06
- **Verified:** not verified.
- **Severity reasoning:** Wrong state users see, a feature silently missing: Medium (no security impact).

### 3.3 Low

Compact form: **id · severity · confidence**, then aliases; where; what goes wrong and the
scenario (actor); spec; fix; related. None of the Lows was sent to verification (the rubric
verifies Critical, High and suspected Medium only), so `Verified: no` is implied. Severity
reasoning is given only where it departs from the reporter's rating. The "why" for each entry
is in the cited source report.

**Relay: stranger or operator**

- **R55-034 · Low · confirmed-read**. C01-03. `internal/relay/relay.go:709-721`, `conn.go:204-216`. `readFrame` grows buffers to 2× length and only `len` is charged, so real outbound heap reaches ~2× `--max-inflight`. With R55-002 at 2000 connections this is above `GOMEMLIMIT=400MiB` on the 4.1p VM (verify/C01-01 notes an OOM path, not re-verified). Spec: relay-hosted.md §2 memory bound. Fix: charge `cap` or copy to `len`. Related: R55-002.
- **R55-035 · Low · confirmed-read**. C01-04. `relay.go:735-763`, `ephemeral.go:86`. Invalid, wrong-`from` and oversized frames are read, decoded twice and answered without being charged to the byte/envelope limits (stranger CPU). Fix: charge the byte buckets per frame before parsing. Related: O-010.
- **R55-036 · Low · confirmed-read**. T2-02, T6c-02. `relay.go:410-441`, `cmd/relay/main.go:237-238`. `Close` closes the DB without waiting for read loops, and the process exits under the `drainClose` goroutines. At every restart, direct non-mail frames in flight (`pair.confirm`, `session.*`) are lost, `add`/`ack` fail `internal`, and an `unbind` in the window fails. Spec: relay-hosted.md:302 ("waits for connection goroutines", R05 L6: listed closed in 02 §D, but only the drain half was built). Fix: WaitGroup on read loops, bounded wait before `q.close()`. Related: O-001.
- **R55-037 · Low · confirmed-read**. C02-06. `cmd/relay/main.go:246-255`. `/metrics` returns 200 with zero queue values when `Stats` fails. Fix: call `Stats` first, then return 500.
- **R55-038 · Low · confirmed-read**. T9-02. `internal/relay/queue.go:207-253`. Relay migrations run in autocommit with one 10 s timeout and no two-opener guard. `relay admin` racing a starting relay fails, and a future non-idempotent R3 can half-apply and keep the relay down. Spec: invariant 5. Fix: mirror `internal/store.apply` (`BEGIN IMMEDIATE` per migration, re-read the version). Related: C28-03, R43 M9.
- **R55-039 · Low · confirmed-read**. C02-03. `internal/relay/backup.go:52-105`. `Restore` deletes the old DB before `integrity_check`, does not fsync, and does not detect a live relay. Operator error only. Fix: temp copy, fsync, check read-only, then rename.
- **R55-040 · Low · confirmed-read**. C02-04. `backup.go:20-45`. `Backup` opens `--db` read-write, so a typo creates an empty DB and a valid-looking empty backup. It also deletes an existing `--out`, against its doc. Fix: `mode=ro`, refuse a missing DB or an existing `--out`.

**Relay → daemon (hostile relay)**

- **R55-041 · Low · confirmed-read**. C04-02 (spec finding). `internal/relayclient/relayclient.go:220-221`. The backoff resets on `ready`, so a relay that sends `ready` and closes drives a ~2 Hz reconnect loop (TLS, signature, outbox re-sends, `SendNow` goroutines). Spec: envelope.md:393-394 (the code follows it; the spec needs changing). Fix: reset only after ≥ 30 s up.
- **R55-042 · Low · confirmed-read**. C08-05. `internal/session/session.go:893,315`. Every session reject is logged at Info before the 30/min limiter, so relay-injected frames control the log volume (rotation erases history; R55-016 on macOS). Fix: log inside the limiter budget, count the rest. Related: O-051.
- **R55-051 · Low · confirmed-read**. T10-04. `internal/mailbox/mailbox.go:263-282`, `internal/daemon/daemon.go:746-755`. Every forged frame carrying a live key_id costs a keystore lookup (an `/usr/bin/security` exec on macOS). For presence it runs on the relay read loop, where a hung keychain stalls all inbound traffic 5 s per frame. Fix: cache live mailbox and identity keys in memory. Related: O-055, O-083.
- **R55-052 · Low · confirmed-read**. T10-06. `session.go:79,220,308-318`, `internal/daemon/mail.go:28,187-192`. Inbound queues are bounded by count (256), not bytes, and session frames are queued at up to 1 MiB (~190 MiB pinned each). Fix: drop oversize `session.*` before queueing; bound in bytes. Related: O-057, O-083.
- **R55-053 · Low · confirmed-test**. T12-02. `internal/mail/receiver.go:319-338`. `mail_seen` is pruned by `received_at` on the prune-time clock. One prune during a ≥ 21-day forward clock step lets the relay replay non-inbox kinds (`team.leave` re-removes a rejoined member). It needs a victim clock step. Fix: prune by the signed `msg.created`. Related: R55-103.

**Accounts (off by default; not on 4.1p; gate for `--accounts`, §6 D3)**

- **R55-043 · Low · confirmed-read (lowered from Medium)**. C03-02. `internal/relay/accounts.go:165-185`. The watcher advances `a.version` before `reload`. A failed reload means a `relay admin` suspend/unbind is never applied to the cache or to live connections. Fix: advance only on success. Related: O-187.
- **R55-044 · Low · confirmed-test (lowered from Medium)**. C03-04. `internal/relay/accounts_store.go:407-447`, `accounts.go:385-393`. Security changes commit first and journal afterwards. A failed append loses the journal line for good, and `unbindSelf` then leaves the cache bound. Fix: journal before commit (replay is idempotent); always reconcile. Related: O-187, R55-013.
- **R55-045 · Low · confirmed-read (lowered from Medium)**. C03-03. `accounts.go:165-167`, `relay.go:940-962`. On a DB outage the relay keeps admitting from the stale cache, and the watcher swallows the errors unlogged. Spec: accounts.md "Outages … fails closed". Fix: track DB health, refuse new connections. Related: O-187.
- **R55-046 · Low · confirmed-read**. C03-05. `cmd/relay/admin.go:132-144`. `relay admin account list/show` prints `display` raw (reachable once 4.2b ships). Fix: sanitise on print, validate in `ensureAccount`.
- **R55-047 · Low · confirmed-read**. C03-06. `admin.go:83-92`. Mutating `relay admin` verbs write no journal line without `--security-journal`, with no warning. Also: the 4.1p unit sets no `--security-journal` (harmless while accounts are off). Fix: refuse without the flag unless `--no-journal`.
- **R55-048 · Low · confirmed-read**. T9-03. `internal/relay/journal.go:38-62,94-97`. Journal lines are not fsynced, and one truncated line aborts the whole replay. Fix: `Sync` per append; skip and report a truncated last line. Related: R55-013, R55-044.
- **R55-049 · Low · confirmed-read**. C02-05. `internal/relay/pairing.go:223-236`. The per-account `pair_new` quota is charged before the other checks, so refusals burn it. Fix: charge at issue.
- **R55-050 · Low · confirmed-read**. T6a-07 (v1, off by default on public relays). `relay/pairing.go:238-253`. v1 `pair_new` is never charged to the per-key/per-prefix limiter. Spec: pairing.md:198. Fix: charge both versions, or scope the spec to v2.

**Untrusted text reaching the human**

- **R55-054 · Low · confirmed-test**. T11-04. `cmd/agentnet/fetch.go:267-272`. `termSafe` quotes only non-`IsPrint` runes. The R46 H1 "graphic but invisible" set (Hangul fillers, variation selectors, U+2800) passes, although `decision.Visible` escapes it. Every O-100 fix names `termSafe`, so all of them would inherit the gap. Fix: one shared terminal sanitiser with `decision.Visible`'s set, used by R55-004, R55-014, R55-055 and R55-056. Related: R38 M3, R48 L3.
- **R55-055 · Low · confirmed-read**. C05-03, C06-03, C20-04, C28-04, T11-05. Root `internal/agentcard/agentcard.go:109-122` (`checkText` refuses Cc only); ~24 print sites (`peers`, `pair`, `team show/join`, `status --team`, `device list/unlink`, `ping`, `inbox`, `request`, `consult`, `debate`, `decision`). Card names may hold bidi, `Cf` or zero-width characters, and the CLI prints them raw. A peer can reorder the `peers` row's fingerprint column or look like another device. Spec: agent-card.md §Card (silent on Cf). Fix: refuse `Cf`/Zl/Zp/default-ignorable in `checkText` (spec plus vector change), or sanitise at every print. Related: O-067, O-100.
- **R55-056 · Low · confirmed-read**. C22-04, C19-03, C23-02, C11-07, T11-03; Info aliases C09-05, C14-05. Peer text printed raw beyond O-100's list: `session`/`sessions` (title, name, summary), multi-line `reason`/`output` in `request show` (fake field lines), a multi-line debate topic (fake transcript lines), the grant `branch` (C1/bidi allowed by `checkBranch`), every `--json` output (C1/bidi not escaped by `encoding/json`), and helper run output bidi. Spec: invariant 4. Fix: the shared sanitiser (R55-054) plus per-line indentation; a JSON encoder that escapes C1/Cf. Related: O-100, O-142.
- **R55-066 · Low · confirmed-test**. T11-02. `internal/daemon/device.go:103-125` (`stripLongDigits`), `internal/notify/notify.go:13-18`. A 6-digit decoy split by zero-width or variation characters survives `stripLongDigits(notify.Clean(…))` and shows as `482913`, so O-129's mitigation is bypassable. It costs attempts and the daily budget, mainly in terminal mode (off by default). Fix: strip `Cf`/invisible runes before digit-run detection. Related: O-129 (escalated), R55-005.

**Paired peer**

- **R55-057 · Low · confirmed-test**. T10-05; Info alias C08-07. `internal/agentcard/agentcard.go:85-101,172`, `internal/team/kinds.go:349-356`, `internal/daemon/ping.go:57-66`. Cards have no size or skill-count bound outside the relay's pairing intake. A team owner can introduce a 480 KiB card, and every inbound session frame (from any key) then runs a full `peers.List` decode (~2.3 ms). Fix: enforce 16 KiB and a skills cap in `Verify`; single-row `IsPaired`. Related: O-084, O-108.
- **R55-058 · Low · confirmed-read**. T6a-09, T9-04. `internal/daemon/mail.go:184` (`_ = rcv.Handle(…)`), `internal/mail/receiver.go:152-155,188-235`. Non-rejection receive errors (DB, Apply, inbox insert, commit) are neither audited nor logged. Poison mails (R55-017, R55-021, R55-067) retry silently for 7 days. Fix: content-free log with a per-(from, id) limiter; after N identical failures, mark the mail bad-body. Related: O-057, O-059.
- **R55-059 · Low · confirmed-read**. C21-04 (spec finding). `internal/worksession/phase1.go:117-137,157-161`, `internal/mail/receiver.go:88-93`. The Phase 1 fallback treats any `unsupported` ack as "peer is Phase 1", but a bad body is acked the same way. One rejected mail closes every worker session with that peer, while a Phase 2 A keeps its side open. Spec: work-session.md §Early complete (unsafe as written). Fix: distinguish the unknown-kind ack from the bad-body ack. Related: R24 M6, R27 M5.
- **R55-060 · Low · confirmed-read**. C22-02. `phase1.go:29-127`. The fallback also closes debate sessions with a raw UPDATE, which leaves `debates` open and writes a wrong-kind experience record. Fix: skip `kind = debate`.
- **R55-061 · Low · confirmed-read**. T9-05. `internal/mail/outbox.go:394-411`, `internal/daemon/daemon.go:473-481`. The fallback trigger runs only from the live `OnFinal` hook in an unawaited goroutine, so a restart in the window loses it for good. Fix: rescan peers with open worker sessions at start. Related: R55-059.
- **R55-062 · Low · confirmed-read**. C18-01. `internal/request/mirror.go:122-160,267-277`. A modified B's `request.decline`/`cancelled` after the accept makes A's mirror final, while A's session stays open with live grants. Spec: work-session.md §Early complete. Fix: close an open requester session as `EarlyComplete` does. Related: O-120.
- **R55-063 · Low · confirmed-read**. C18-02, C21-03, C22-03, C23-01, T6c-07; Info aliases C19-04, T6c-22. Request ids are unique per sender, but several paths resolve by id alone: `request_show` (`internal/request/query.go:12-30`), `GetByRequestID` (`internal/worksession/store.go:268-278`, `LIMIT 1`), `debate --cancel` on the respondent (`cmd/agentnet/debate.go:582-584` calls `request_cancel`, which can cancel the user's own outgoing request with that id), `log --session r-` (`internal/audit/query.go:230-262`), and the debate notification title. A peer reusing one of our ids confuses views and can redirect one CLI action. Spec: request.md §Persistence (`ambiguous_request`). Fix: refuse an incoming id equal to one of our `out` ids to that peer, and return `ambiguous_request` everywhere. Related: R45 L1.
- **R55-064 · Low · confirmed-read**. C09-03. `internal/capability/store.go:191,234`, `internal/daemon/grant.go:638`. Held grants are never pruned and `grant_list` is unpaged, so a session requester can grow them until `agentnet grants` exceeds 1 MiB. Fix: cap per peer or session, prune past `exp`, page. Related: O-058, O-178.
- **R55-065 · Low · confirmed-test**. C09-01. `internal/daemon/grant_kinds.go:51`. The holder re-serialises the token with HTML escaping, so a valid token with many `&<>` exceeds 2048 bytes and is dropped (fails closed). Fix: use `CanonicalValue`.
- **R55-067 · Low · confirmed-test**. C22-01 (escalates O-131). `internal/worksession/mirror.go:187-200`, `internal/experience/experience.go:225`. After the O-131 closed→open reopen, a second close hits the experience primary key. The mail can never apply, and B's mirror is stuck open. Fix: refuse transitions out of `closed` (also closes O-131).
- **R55-068 · Low · confirmed-test**. C20-01. `internal/team/kinds.go:484-490,552-564`. A delayed `team.leave` arriving after a rejoin removes the rejoined member (undoes R16 M2). Fix: ignore leaves older than the member's `added`.
- **R55-069 · Low · confirmed-read**. C20-02. `internal/team/store.go:196,436,493`, `internal/presence/sender.go:459-466`. Introduced peers GC'd by a leave get no goodbye (their mailbox row is gone first). Fix: capture the goodbye set before GC.
- **R55-070 · Low · confirmed-read**. C24-02. `internal/debate/constraint.go:185`. Constraints received while `invited` are stored active and count toward the 10 limit. Fix: ignore outside positions/rounds/converge.
- **R55-071 · Low · suspected**. C23-04. `internal/debate/sweep.go:57-73`. A sleeping initiator can time out a turn the respondent took in time (the sweep may run before the relay drains). Fix: a grace period after (re)connect. Related: O-175.
- **R55-072 · Low · confirmed-test**. C17-01. `internal/request/decode.go:126-139,186-195,301-307`. Optional members sent as `""` are accepted as absent, so the stored body differs from the sent one. Fix: reject `""` for present optional members.
- **R55-073 · Low · confirmed-read**. T1-03 (v1 only). `internal/peers/pairing.go:689-700`. v1 pairing stores the relay-supplied card bytes (extra members), not the canonical card. Fix: store `canonicalPart` as v2 does. Related: O-004.
- **R55-074 · Low · confirmed-read**. T6a-06. `internal/notify/webhook.go:117-132,171`. The webhook body (title, format) is frozen at enqueue while the URL is re-read, so `--webhook-title off` does not stop pending titles, and generic bodies can reach Slack unescaped. Fix: render per attempt. Related: C27-04.
- **R55-075 · Low · confirmed-read**. T6a-10. `internal/daemon/notify.go:74-86,114-124`. If the peer lookup fails, the notification's peer name falls back to the public key, which is sent to the desktop and webhook. Spec: notify.md §Privacy ("never … public keys"). Fix: fall back to "unknown peer". Related: O-108.
- **R55-076 · Low · confirmed-read**. T6a-05. `webhook.go:169-213,240`. Rows older than 24 h are still POSTed; the age rule applies only after a failure. Fix: check age before the POST. Related: O-117.
- **R55-077 · Low · confirmed-read**. C08-04. `internal/session/session.go:692-699`. `ping` is answered while invisible, which gives a paired peer an on-demand liveness oracle. Fix: ignore pings from peers outside the visible set, or document it. Related: O-069.
- **R55-078 · Low · confirmed-test**. C10-01. `internal/capability/fetch.go:187-197,277-289`. `Close` does not flush suppressed `grant.fetch` counts (up to ~70 s of reads unaudited). Fix: flush all windows in `Close`. Related: O-189.
- **R55-079 · Low · confirmed-test**. C10-02. `fetch.go:653-662`. The 256 MiB/24 h budget is a fixed window, so 2× can be served across the boundary; it also resets on restart. Fix: sliding window, or document it. Related: O-143.

**Fail-open on a rare condition (T7 set)**

- **R55-080 · Low · confirmed-read**. C17-02. `internal/daemon/request.go:454-461` (`trustOfTx`: `ErrNoRows` → verified). The only D5 site that reads a missing peer row as "not relay" (a race with `peers remove`). Fix: treat `ErrNoRows` as unverified or retryable. Related: O-082, D5.
- **R55-081 · Low · confirmed-read**. C09-02. `internal/daemon/grant_kinds.go:56-60,73-75`. Any DB error in the holder's grant apply is treated as orphan and acked, and the grant is lost for good. Fix: return non-not-found errors.

**Local agent**

- **R55-082 · Low · confirmed-read**. C06-04 (owner call). `internal/daemon/trust.go:150-180`. `peers_verify` needs only the public fingerprint, so any local agent can raise a peer to `fingerprint` (lifting the D5 refusal; clearing `introduced_by`). Fix: gate it by approval, or document it (§6 D8). Related: D5, O-044.
- **R55-083 · Low · confirmed-read**. T2-03, T10-07 (R5 lead 3). `internal/ipc/ipc.go:115-123`, `internal/daemon/daemon.go:672`. Any temporary `Accept` error (EMFILE) ends `Serve` and the whole daemon. R55-026 and uncapped IPC connections are sources. Fix: retry with backoff; cap IPC connections.
- **R55-084 · Low · confirmed-read**. T8-04 (owner call). `internal/daemon/team.go:133-186`, `internal/team/kinds.go:65-100`. `team_invite` needs no human, so a prompt-injected owner's agent can enrol a stranger whom every teammate's daemon trusts as `team` (passes D5). Fix: owner decision (§6 D8). Related: O-065, O-090.
- **R55-085 · Low · confirmed-read**. C14-04. `internal/daemon/device_scope.go:214-216,268-275`. Concurrent `device_scope_set` calls do not supersede each other. Fix: serialise per controller. Related: O-147.
- **R55-086 · Low · confirmed-read**. C07-03. `internal/daemon/outbox.go:377-385`. `mail_submit` decodes numbers as float64, so integers > 2^53 are silently rounded before signing. Fix: `UseNumber`.
- **R55-087 · Low · confirmed-read**. C19-02. `cmd/agentnet/inbox.go:400`, `request.go:205-218`. `--output-from-file` is read unbounded, and `--brief-from-file`/`--question-from-file` block on a FIFO (R32 L2 fixed only for context files). Fix: one shared bounded regular-file reader. Also: `device scope --from-file` is unbounded (T4 lead).

**Local platform: other OS user, or the owner's environment**

- **R55-088 · Low · confirmed-read**. C16-05, T13-03. `internal/paths/paths.go:43,69-72`, `internal/keystore/keychain.go:28-31`. The pipe name and keychain accounts hash the unnormalised dir spelling. On case-insensitive filesystems two spellings give two daemons on one DB, or `ErrKeyLost` with advice to delete the card. Fix: canonicalise once in `paths.In` (`EvalSymlinks`, `GetFinalPathNameByHandle`, case-fold). Related: R55-008, R55-092.
- **R55-089 · Low · confirmed-read**. T13-02, T6c-11; Info alias C16-08. `internal/paths/paths.go:56-66`, `internal/keystore/perm_windows.go:35-38`, `cmd/agentnet/doctor.go:246-270`. On Windows the config dir is never made or checked owner-only and key files are not re-checked on load. A foreign-created custom home with a planted `identity.key` is used silently, and `doctor` says "owner-only" after checking writers only. It needs the owner's choice of path. Fix: protected DACL in `Ensure`, refuse a foreign owner, `OwnerOnly` on load, doctor checks read access. Related: O-030.
- **R55-090 · Low · confirmed-read**. C12-03, T4-02. `internal/notify/approval_windows.go:86,95`, `desktop_windows.go:47`, `internal/service/schtasks.go:37-51`, `cmd/agentnet/doctor.go:512,518`. `powershell.exe` (the approval toast carries the code in its env; ordinary toasts carry peer text) and `schtasks.exe` are found through `PATH`. Spec: approval.md §window Windows row (R29 L2 marked fixed, but only for the dialog). Fix: `GetSystemDirectory` paths (a `systemTool` helper).
- **R55-091 · Low · confirmed-read**. T4-03. `internal/keystore/file.go:55-66`, `perm_windows.go:14-30`. On Windows the owner-only DACL is applied after `CreateTemp`, so a handle opened in the window keeps its access (it needs a readable parent the owner chose). Fix: create with a security descriptor, share mode 0. Related: R55-089.
- **R55-092 · Low · confirmed-test**. C05-01, C05-02, T3-01, T3-02; Info alias T3-03. `internal/keystore/keystore.go:36-106`, `keychain.go:36-67`. One root cause: an *unavailable* keychain is folded into *not found*, and fallbacks leave stale copies. Effects:
  - mailbox keys are rotated needlessly;
  - start-up says the identity key is lost and advises deleting the card;
  - a secret rotated during an outage is shadowed by the old keychain copy (the webhook signs with the pre-rotation secret);
  - on hosts without a keychain, `Delete` always errors (mailbox keys never marked deleted; `--webhook off` half-done);
  - `identityPriv` does not check the key against the card.
  Fix: distinguish unavailable from absent, delete more-preferred copies after a fallback save, wrap `Delete` errors like `Get`. Related: O-035, O-116, O-197.
- **R55-093 · Low · confirmed-test**. C16-03 (R5 lead). `internal/logfile/logfile.go:62-85`. One failed rotate (Windows sharing violation from a log viewer) kills the log for the process lifetime. Fix: reopen in append mode on failure.
- **R55-094 · Low · confirmed-read**. C16-04. `cmd/agentnetd/install.go:37-44,122`. Install bakes in the binary path without the D24 ownership check (owner's choice of folder). Fix: reuse `device.CheckProgramOwner`, warn or refuse. Related: O-161.
- **R55-095 · Low · suspected**. C16-06. `internal/service/systemd.go:20-24`, `schtasks.go:58-62,99-101`. A newline or `%VAR%` in owner-chosen service paths is not rejected. Fix: reject control characters and `%`.
- **R55-096 · Low · confirmed-test**. C26-01. `internal/store/store.go:540`, `readonly.go:17`; also `internal/relay/queue.go:210`. `"file:" + path` DSN: `#` truncates and `%xx` decodes, so the DB opens at the wrong path (possibly outside the private home) or not at all. A Windows user name with `#` breaks start-up. Fix: build a proper file URI.
- **R55-097 · Low · confirmed-read**. C13-01. `internal/device/perm.go:52`, `scope.go:319`. A scope script's `#!` interpreter is not ownership-checked (owner's choice). Fix: check it too, or document it. Related: O-160.

**Daemon robustness and state**

- **R55-098 · Low · confirmed-read**. T7-01. `internal/daemon/device_run.go:176-183,304-316`. If the revocation sweep's `take` errors, it only logs, and a command whose link or scope was revoked keeps running until its timeout. Spec: device.md ("killed at once"). Fix: stop on error too, or stop directly after the unlink commit.
- **R55-099 · Low · confirmed-read**. C14-03. `device_run.go:338,421-445,546-548`. A failed `finish` wedges the helper queue and a restart sends a second "interrupted" result. Fix: clear `Running` in the submit tx; skip already-resulted rounds.
- **R55-100 · Low · confirmed-read**. T2-01. `internal/daemon/mail.go:168-187`, `daemon.go:572-576`. Shutdown cancels the mail worker's context before the relay stops, so a just-committed mail loses its `After` effects (audit rows, notifications) for good. Fix: a separate mail ctx cancelled in `stopMail`; `After` and ack detached. Related: C07-04.
- **R55-101 · Low · confirmed-read**. C28-02. `internal/daemon/stop.go:39-55`. `stop` reports success before the process exits (teardown can take ~1 min). Fix: wait for the PID. Related: O-144.
- **R55-102 · Low · confirmed-read**. C28-03. `internal/daemon/daemon.go:272,283`; also `cmd/agentnetd/install.go:180`. A losing second `agentnetd` migrates the DB before discovering the lock. Fix: take the IPC lock before `store.Open`. Related: R43 M9, O-173.
- **R55-103 · Low · confirmed-test**. T12-01. `internal/mailbox/mailbox.go:224-226,304-420`. A ≥ 21-day forward clock step deletes the keys peers seal to and mints a future-dated key that peers refuse, so no inbound mail arrives for the length of the step. Fix: treat a future `created` as unusable; bound deletions. Related: R55-053.
- **R55-104 · Low · confirmed-read**. C11-08. `internal/daemon/grant.go:488-497`. The policy match happens outside the issuing tx (ms window after a removal). Fix: re-read the policy in tx.
- **R55-105 · Low · confirmed-read**. C12-04. `internal/notify/approval_linux.go:386-388`. Linux `notifIDs` grows by one entry per outcome forever. Fix: record only code-notification ids.
- **R55-106 · Low · confirmed-read**. C08-02. `internal/presence/sender.go:301-306`. Any `Team.Get` error persists `invisible`. Fix: only on not-found or inactive.
- **R55-107 · Low · confirmed-read**. C08-03. `sender.go:362-382`. `SetMode` returns the audit error after applying the mode and skips the goodbye/online diff. Related: R55-112.
- **R55-108 · Low · confirmed-read**. C08-06. `internal/presence/store.go:116-143`. `human_present` is `null` instead of `false` for offline peers. Spec: presence.md §Receiving.
- **R55-109 · Low · confirmed-read**. T6a-11 (latent until R55-033). `sender.go:120-131,201-204`. With sharing off, local `status` loses the user's own detected value. Spec: presence.md §Human sharing.

**Supply chain and release**

- **R55-110 · Low · suspected**. C15-04. `.github/workflows/ci.yml:1-8`. No `permissions:` block and no `persist-credentials: false` in ci.yml: if the repo default is read-write, test code holds `contents: write` (enough for R55-003). Fix: `permissions: contents: read`, `persist-credentials: false`. (Depends on a repo setting the reviewer could not read.)
- **R55-111 · Low · confirmed-test**. C15-03 (**release blocker**, not security). `tests/install/run.sh:86`, `cases.sh:195`. Since the real key was embedded (447f08e), the `placeholder` install case fails. The `install-sh` job fails in ci.yml and release.yml, so `draft` cannot run. Fix: generate the placeholder copy by rewriting the key lines.

**IPC, CLI and doctor behaviour**

- **R55-112 · Low · confirmed-read**. T6a-03. `internal/daemon/presence.go:22-93`. `presence_set` takes different params than ipc.md and ignores unknown ones, so a spec-shaped `{"mode":"invisible"}` is a successful no-op. Fix: align and refuse unknown fields. Related: C08-03.
- **R55-113 · Low · confirmed-read**. C20-03. `internal/daemon/team.go:332-340`, `internal/team/store.go:417-446`. `team_leave` commits `left` before submitting `team.leave`. A failed submit cannot be retried, and the owner keeps the member. Fix: `SubmitTx` in the same tx, or allow a resend. Related: O-088.
- **R55-114 · Low · confirmed-read**. C21-02. `internal/worksession/cancel.go:42,213-214`, `mirror.go:137,147`. B's `cancel = 'refused'` is never set. Fix: set it when an older-or-equal state arrives. Related: R27 L6.
- **R55-115 · Low · confirmed-read**. C21-05. `internal/daemon/session.go:388,454,477,498`. A-side results omit `mail_id`.
- **R55-116 · Low · confirmed-read**. C23-03. `internal/debate/start.go:191-196`, `cmd/agentnet/debate.go:447-454`. `--rounds 0` or sub-second timeouts silently use the defaults. Fix: "absent" semantics.
- **R55-117 · Low · confirmed-read**. T6a-04. `internal/daemon/request_lifecycle.go:458,492`. `request_list`/`inbox_list` without params return `bad_request`. Fix: add the `len(params) > 0` guard.
- **R55-118 · Low · confirmed-read**. T6a-08. `internal/peers/pairing.go:703-715`. On re-pair, the CLI and the `pair.complete` audit report the requested trust, not the stored (higher) one. Fix: re-read the row.
- **R55-119 · Low · confirmed-read**. T6a-12. `cmd/agentnet/request.go:234-263`. `--artifact` JSON is parsed loosely (unknown keys dropped, case-folded), and the strict `ParseArtifactSpec` is dead code. Fix: use it. Related: R55-072.
- **R55-120 · Low · confirmed-read**. C27-02. `cmd/agentnet/notify.go:34`. The CLI event list lacks the four debate events, so users cannot turn them off.
- **R55-121 · Low · confirmed-read**. T6b-01. No `ws.open` audit row is ever written. Spec: work-session.md §Audit.
- **R55-122 · Low · confirmed-read**. T6b-02. `internal/daemon/session.go:178`. The session view's `grants` is always `[]`, which misleads at release time.
- **R55-123 · Low · confirmed-read**. T6b-03. `internal/approval/store.go:654`. Daemon-caused rejections (superseded, unlinked, scope cleared) are audited `reason: "user"`. Related: O-128.
- **R55-124 · Low · confirmed-read**. T6b-04. `internal/daemon/daemon.go:486-488`, `grant.go:796-802`. Grants revoked by session close or peer removal get no `grant.revoke` row.
- **R55-125 · Low · confirmed-read**. T6b-05. `status.approval_window` and the fix-naming `approval_unavailable` message are not built.
- **R55-126 · Low · confirmed-read**. T6c-03. `internal/debate/decision.go:283,395`. Every Decision refusal says "the opening position did not match its commitment". The specs disagree.
- **R55-127 · Low · confirmed-read**. T6c-05. `cmd/agentnet/debate.go:582-585`. `--reason` is dropped when cancelling an invited debate.
- **R55-128 · Low · confirmed-read (part suspected)**. T6c-06. `internal/daemon/decision.go:61-66`, `debate.go:227-249`. Unknown `s-` or ambiguous `r-` ids give `internal` instead of `unknown_decision`/`ambiguous_request`.
- **R55-129 · Low · confirmed-read**. T6c-08. `cmd/agentnet/log.go:249-251`. `log --verify` advises anchoring the head even when the chain is BROKEN.
- **R55-130 · Low · suspected**. T6c-09. `log.go:30,151-156`. The list mode has a fixed 15 s total timeout, and `--timeout` is ignored. Related: C26-02.
- **R55-131 · Low · confirmed-read**. T6c-10. `doctor.go:152-157`. With the daemon down, doctor ignores the installed `--relay`.
- **R55-132 · Low · confirmed-read**. T6c-12. `cmd/agentnet/main.go:43-127`. `agentnet setup` (4.9a) is not built, but install.sh, Homebrew, install.md and doctor all point to it. Related: C15-06.
- **R55-133 · Low · confirmed-read**. T6c-13. `scripts/install.sh:67-71,311-315`. A partial replace says "nothing was installed". Related: O-183.
- **R55-134 · Low · confirmed-read**. T6c-14. `doctor.go:347,418`. doctor checks the daemon's URL with its own `DORYLINAE_ALLOW_INSECURE_RELAY`. Related: O-023.
- **R55-135 · Low · confirmed-read**. T6c-04, T5-02 (T5 rated it Info). `internal/daemon/audit_inventory_test.go:131-181`. `TestAuditInventoryIsComplete` cannot see the five `debate.*` mail kinds. Spec: audit.md §Scope.

**Test harness (test-only code)**

- **R55-136 · Low · confirmed-test**. C30-01; Info alias C31-07(b). `tests/phase1-smoke.sh:409-439`. The audit/no-content check passes when the DB read errors, and no marker is put in titles or the urgency reason.
- **R55-137 · Low · confirmed-read**. C30-02, C31-03. `tests/harness/phase3-agents.sh:504`, `phase3-agents.ps1:681`. The forced-escalation round accepts `agreed`.
- **R55-138 · Low · confirmed-test**. C30-03. `tests/harness/phase1-agents.sh:325`. `$'\n'` inside double quotes gives a false FAIL.
- **R55-139 · Low · confirmed-read**. C30-04, C31-01. The harness and smoke scripts omit `DORYLINAE_KEYSTORE=file`, so throwaway keys pile up in the owner's real keychain (166 `dorylinae:` entries on this PC).
- **R55-140 · Low · confirmed-read**. C30-05. `tests/harness/phase*-agents.sh:90-97`. Predictable `/tmp/agentnet-stderr.$$`.
- **R55-141 · Low · confirmed-read**. C31-02. `tests/harness/phase2-agents.ps1:363-370` et al. The timeouts are dead code, so a hung child hangs the harness with the daemons left running.

### 3.4 Info

Not defects: test gaps, doc drift and design notes. Confidence is `confirmed-read` unless
marked (s) = suspected or (t) = confirmed-test.

| Id | Aliases | Where | Note | Fix / owner |
|---|---|---|---|---|
| R55-142 | T7-02, C10-03, C22-05, T6c-16 | 64 post-commit `_ = …Append` sites; `daemon/fetch.go:45-49`; `worksession/experience.go:124-133`; `debate/store.go:409-414` | No rule says whether an action fails when its after-commit audit row cannot be written. Most ignore the error (grant issue, fetch, approvals, debate rows); a few return it after committing (O-095, C08-03) | Owner decision §6 D11; document it in audit.md |
| R55-143 | T8-05, C16 lead, T2 lead | `internal/ipc/ipc.go:138-189` | No `recover` in IPC dispatch; any handler panic stops the daemon (none found) (s) | Add `recover` → `internal error` |
| R55-144 | C01-06 | `relay.go:509-510` | `c.release()` not deferred (s) | `defer` |
| R55-145 | T2-04 | `device_run.go:171-173`, `mail.go:171-175` | Unawaited goroutines: a dropped run's `ws.cancel` or a rotation push can be lost at shutdown | Daemon-scoped WaitGroup |
| R55-146 | T7-03 | `approval/store.go:524-557` | `expireNow` DB error leaves the approval `pending` and skips `OnReject` (fails closed) | Run `OnReject` anyway. Related O-132 |
| R55-147 | C12-05 | `approval/store.go:283-313` | The daily wrong-code cap is skipped when its write fails | One tx. Related O-128 |
| R55-148 | T6b-11 (s) | `approval_terminal.go:73-85` | Terminal mode counts a non-6-digit typo as an attempt | `isSixDigits` first |
| R55-149 | T8-06 | `trigger.go:147`, `approval/store.go:143` | `notify --desktop off` does not disable approvals (safe direction); the `mail_submit` CLI gate is cosmetic | Align approval.md |
| R55-150 | T9-06 (t) | `store/store.go:540` | Deleted content survives in `-wal` after a checkpoint while the daemon runs (same-user boundary) | Fix doc, or `journal_size_limit=0` + TRUNCATE. D30 |
| R55-151 | T12-03 | `daemon/*.go` handlers | `time.Now()` next to injected store clocks | One daemon clock. O-106 |
| R55-152 | T1-04 | `daemon/device.go:143-149,278-281` | Device `at` parsed with loose RFC 3339 | Shared `parseWireTime` |
| R55-153 | C05-04 (t) | `agentcard/canonical.go:40-57` | Lone surrogates decode to U+FFFD (aliasing) | Spec rule + reject |
| R55-154 | C05-05 | `tools/verifycard/main.go:38` | verifycard is non-strict base64 and has no schema step | Strict + schema |
| R55-155 | C05-06 (s) | `keystore/file.go:55-80` | No directory fsync after rename | fsync dir |
| R55-156 | C04-03 | `relayclient.go:439-484` | Client does not check `e.To` (safe: crypto binds the recipient) | Optional drop |
| R55-157 | C04-04 | `relayclient.go:257-263` | `ready.account` discarded; 4.2c must sanitise `Display` | Note for 4.2c |
| R55-158 | C07-04 | `mail/receiver.go:170-172` | `After` is at most once (see R55-100) | Document |
| R55-159 | C09-04 | `capability/fs.go:196-206,348-361` | Local-writer TOCTOU into `.git` inside the root | No-follow walk. O-145 |
| R55-160 | C11-09 | `grant.go:951,992` | Policy audit actor/detail drift | Align |
| R55-161 | C13-02 | `device/device.go:520` | A local unlink does not advance the offer watermark | Record `now`. O-158 |
| R55-162 | C13-03 | `daemon/device.go:203,242` | `activatedRoles` leak on commit failure | Via `op`. O-077 |
| R55-163 | C17-03 | `request/receive.go:188-196` | Tombstone path before D5 (spec order) | Owner |
| R55-164 | C18-03 | `request/cancel.go:354-359` | `cancel_in` lacks `age_s` | Add |
| R55-165 | C19-05 | `request/query.go:42-61` | `FindKey` scans all requests per `wait` poll | Store derived id |
| R55-166 | C20-05 | `team/store.go:681-726` | O-087 partly fixed: pruned on insert, not daily/at start | Update team.md |
| R55-167 | C21-06 | `worksession/receive.go:223` et al. | `ws.orphan` lacks `session` | Add |
| R55-168 | C22-06 | `worksession/experience.go:105-109` | `rounds_rejected` only with changes text | `round-1` |
| R55-169 | C23-05 | `daemon/debate.go:287-297` | show/list do not sweep timeouts (≤ 20 s stale) | Sweep in show |
| R55-170 | C24-03 | `debate/experience.go:50-68` | Experience helpers scan cut rows. O-175 | Restrict |
| R55-171 | C25-02 | `decision/verify.go:89` | Non-strict sig fails at step 3, not 1 | Align spec |
| R55-172 | C26-02 (t) | `audit/query.go:129-158` | Sparse filters hold the connection (162 ms/200k rows) | Id-range paging later |
| R55-173 | C26-03 | `store/store.go:573-576,609-621` | Newer-schema check outside the per-migration tx | Check in `apply` |
| R55-174 | C27-03 | `notify/trigger.go:181` | `notify.fail` detail uses `code`, not `error` | Align |
| R55-175 | C27-04 (s) | `notify/payload.go:146-182` | Slack/Discord auto-link bare peer URLs; Discord may unfurl (T11 note) | `flags: 4` / break `://` |
| R55-176 | C27-05 | `notify/settings.go:47-54` | Debate events and `session` not in notify.md | Doc |
| R55-177 | C28-05 | `doctor.go:511-522` | schtasks output parsing is English-only | CSV/state codes |
| R55-178 | C29-01 | `tools/verifyvectors/main.go:579-690` | Mail vector not cross-checked with pairing | Add equalities |
| R55-179 | C29-02 | `verifyvectors/decision.go:221-237` | Verifier does not re-derive the Decision | Optional / doc |
| R55-180 | C29-03 | `specvectors/main.go:40,146-160` | Card vectors made by the production package | Schema check |
| R55-181 | C30-06 (s) | `tests/harness/phase*-agents.sh` | bash 3.2 `$!` is the subshell; daemons survive | `exec` |
| R55-182 | C30-07 | `phase*-agents.sh:186` | Auth detector greps model prose | Match stderr |
| R55-183 | C30-08 | `phase1-agents.sh:177-180` | agy with full permissions reads peer text (design note) | Throwaway HOME |
| R55-184 | C30-09 (s) | `standin/main.go:416,423` | No `--` before peer file names | Add `--` |
| R55-185 | C31-04 | `phase3-agents.ps1:275` | `PowerShell(Get-Content *)` in the allowlist is wider than HANDOFF §5 | `Read` / NOTES.md only |
| R55-186 | C31-05 | `phase3-agents.ps1:527-546` | Stand-ins not killed on the throw paths | Add to `finally` |
| R55-187 | C31-06 | `phase*-agents.ps1` | `-MaxAttempts` skipped when `Wait-Until` throws | try/catch |
| R55-188 | C31-07 (a,c) | `phase1-smoke.ps1:215`; `phase2-agents.ps1:439-445` | Vacuous `-not $m.daemon_online`; grant path not asserted | Guards |
| R55-190 | C02-07 | `cmd/relay/main.go:244-257` | `/metrics` lacks the §5 gauges (backup age, free disk…) | 4.1b |
| R55-191 | C03-07 | `.github/workflows/ci.yml` | No CI job builds `-tags testhooks` | Add step |
| R55-192 | C03-08 (s) | `accounts_store.go:155-158` | Bind codes are unsalted SHA-256 of 40 bits | HMAC (optional) |
| R55-193 | C03-09 | `relay.go:885-921` | Drain does not re-check eligibility after suspension | Owner |
| R55-194 | C15-05 | `.github/dependabot.yml` | No gomod ecosystem, no govulncheck | Add |
| R55-195 | C15-06 | `scripts/install.sh:38-43` | Stale "PLACEHOLDER" banner | Reword |
| R55-196 | C15-07 (s) | `deploy/early/setup.sh:30-33` | Lockout check misses `command=` root keys (unlikely on Hetzner) | Refuse `command=` |
| R55-197 | C16-07 | `internal/ipc/ipc_test.go` | No 1 MiB line or foreign-owner pipe test | Add |
| R55-198 | T4-05 (s) | `grant.go:252-275` | Issuance `git rev-parse` without hardening or ceiling | Same `-c` as serving. O-151 |
| R55-199 | T4-06 | `relay/backup.go:37-45` | Backup file 0644 during `VACUUM INTO` (safe in the deployed unit) | umask 0077 |
| R55-200 | T5-03 | `daemon/e2e_*_test.go` | No-content tests scan audit detail only, not logs, outbox or webhook rows | Buffer logger + asserts |
| R55-201 | T13-04 (s) | `capability/fs.go:161-175` | OneDrive cloud reparse points refused as `symlink` | Owner (§6 D14) |
| R55-202 | T13-05 | `ci.yml:38-58`, `release.yml:94-104` | Lint only for GOOS=linux; release test ubuntu-only; no `window_darwin_test.go` | Multi-GOOS lint, 3-OS release test |
| R55-203 | T13-06 (s) | `notify/window_darwin.go:25-33` | macOS `system attribute` may garble non-ASCII approval text | base64 env; test on the macOS runner |
| R55-204 | T6a-13 | `daemon/outbox.go:108-109` et al. | ipc.md error/ordering/field drift (`mail_submit` raw error text; `shutdown` "always succeeds"; `team_list` order; status fields) | Doc/code align |
| R55-205 | T6a-14 | `request/validate.go:342-346` | `hasControl` also refuses U+FFFD | Doc |
| R55-206 | T6a-15 | `peers/store.go:113` | v1 re-pair clears `introduced_by`. O-041 mirror | Doc/code |
| R55-207 | T6a-16 | mail.md | `ts` wording; no push of keys created without a relay; debate kinds missing | Doc |
| R55-208 | T6a-17 | `peers/pairing.go` | Pairing failure codes and TTL-ended attempts undocumented / not audited | Doc |
| R55-209 | T6a-18 | team.md, presence.md | Delete GC, `leave_ignored` detail, 8 KiB cap | Doc |
| R55-210 | T6a-19 | request.md | Migration 19 type, duplicate `urgency_note`, step order | Doc |
| R55-211 | T6a-20 | notify.md | Keychain account name (O-115 fixed), privacy text | Doc |
| R55-212 | T6a-21 | envelope.md | Relay logs at Warn by default; account frame codes | Doc |
| R55-213 | T6a-22 | `agentcard.go:205-211` | Case-insensitive schema decode; missing skill `description` accepted (root shared with R55-019) | With R55-019 |
| R55-214 | T6a-23 | status.md, agentnetd.md | Forward references (`account`, exit 2) | Doc |
| R55-215 | T6a-24 | 02-open-findings | Ledger corrections (see §7) | Orchestrator |
| R55-216 | T6b-06 | `Docs/cli/approve.md` | **D20's condition is unmet**: the Linux argv exposure and `hidepid=2` are not documented | Doc (§6 D13) |
| R55-217 | T6b-07 | `approval/store.go:221-225` | `approval.limit` action undocumented | Doc |
| R55-218 | T6b-08 | work-session.md | `agentnet release` vs `session --release` | Doc |
| R55-219 | T6b-09 | ipc.md:375 | `ws_discard` missing from the list | Doc |
| R55-220 | T6b-10 | grant.md §Tables | `grant_policies` schema drift. O-133 | Doc |
| R55-221 | T6c-15 | `debate/engine.go:220-240` | B may abandon after its answer; the specs disagree | Owner (§6 D14) |
| R55-222 | T6c-17 (part s) | `experience/*.go` | Experience record drift | Code/doc |
| R55-223 | T6c-18 | `decision/verify.go:466-494` | Verifier's context check looser than the daemon's | Reuse the validator |
| R55-224 | T6c-19 | `Docs/cli/relay.md` | Flags/subcommands/exit codes undocumented | Doc |
| R55-225 | T6c-20 | status.md, ipc.md | `last_error` omitempty, missing fields | Doc |
| R55-226 | T6c-21 | audit.md, decision.md, debate.md | Phase 3 doc drift (`--md` stdout mix, view `hash`) | Doc |

R55-189 is not used (its only alias, C01-05, merged into R55-002). Aliases merged into other
entries: C19-04, T6c-22 → R55-063; C09-05, C14-05 → R55-056; C08-07 → R55-057; C16-08 → R55-089;
T3-03 → R55-092; C31-07(b) → R55-136. Unique Info entries: 84.

## 4. Fix plan

One ticket = one root cause or a tight cluster in one area, so it gets one focused security
review. Each commit is based on tag `pre-review-55`, and the owner picks the tickets.

**Models (D26/D28):**
- Opus: security-critical, OS-level, crypto or peer-input parsing.
- Sonnet: well-specified fixes.
- Lite: routine work.

**Order rules:**
- Highs first.
- A spec change lands before the code that depends on it (HANDOFF rule 3).
- Tickets that touch the same file are sequenced, not run in parallel.

**Acceptance tests:** each reviewer's `zz_review55_*` test (copies in `tests/`, each with a
`.path` file) currently passes or fails *to demonstrate the defect*. Its assertion must be
inverted and made permanent as a normal test.

| Ticket | Title | Findings | Files | Sev | Model | Spec first? | Owner decision? | Order |
|---|---|---|---|---|---|---|---|---|
| **R55-F1** | Relay budget fairness | 001, 002, 034, 035, 144 | `internal/relay/relay.go`, `conn.go`, `limits.go` | High | Opus | yes (relay-hosted.md §2: frame deadline, per-prefix share, ephemeral budget) | D1 (reopens R52 H1) | 1st; before F2 and F16 |
| **R55-F3** | Bind signed SUMS to the CI run; release pipeline hygiene | 003, 032, 110, 111, 132, 133, 194, 195 | `.github/workflows/*.yml`, `Docs/ops/release-signing.md`, `tools/releasesign/main.go`, `tests/install/*`, `scripts/install.sh`, `packaging/homebrew/*` | High | Opus | yes (49 §install / runbook) | D4 | before the first release; independent of the others |
| **R55-F4** | `decision verify` prints raw reason | 004 | `cmd/agentnet/decision.go`, `internal/decision/verify.go`, `internal/debate/decode.go` | High | Sonnet | no | no | early; small |
| **R55-F7** | Forbidden resources by file identity; no FS access before approval | 006, 027, 028, 159, 198 | `internal/daemon/grant.go` (`validateResource`), `device_scope.go`, `internal/device/runner.go`, `scope.go`, `internal/capability/fs.go` | High | Opus | small (grant.md §Issuance: say "by file identity"; UNC refused) | D6 (macOS CI) | before F5 (shares `grant.go`) |
| **R55-F5** | One approval-summary builder | 005, 007, 066, 122, 148 | `internal/daemon/grant.go`, `device.go`, `device_scope.go`, `session.go`, `debate_constrain.go`, new helper | High | Opus | yes (approval.md summary contents per kind) | D9 | after F7; before F6 |
| **R55-F8** | Windows pipe ownership and canonical config dir | 008, 088, 197, R5 Unix-listen lead | `internal/ipc/transport_windows.go`, `transport_unix.go`, `internal/paths/paths.go`, `internal/keystore/keychain.go` | High | Opus | small (ipc.md §Endpoint) | D7 | after F21 (shares `keychain.go`) or together |
| **R55-F2** | Relay queue abuse | 009, 010, 011 | `internal/relay/queue.go`, `relay.go` (drain), `internal/envelope/envelope.go`, `internal/relayclient/relayclient.go` | Med | Opus | yes (relay-hosted.md §2 delivered bytes; envelope.md payload) | D2 | after F1 |
| **R55-F9** | Relay error text: parse once, bound, sanitise; backoff | 014, 041, 156, 157 | `internal/relayclient/wire.go`, `relayclient.go`, `internal/session/session.go`, `internal/peers/pairing.go`, `cmd/agentnet/{main,doctor,ping,pair}.go` | Med | Opus | yes (envelope.md:393 backoff) | no | after F10 (uses the sanitiser) |
| **R55-F10** | Shared terminal sanitiser; card-name charset; O-100 sweep | 054, 055, 056 | `cmd/agentnet/*` (print sites), `internal/agentcard/agentcard.go` (`checkText`), `internal/capability/token.go` (`checkBranch`) | Low | Opus (card rule is peer-input) | yes (agent-card.md charset + vector) | no | after F4 |
| **R55-F6** | Approval window rendering (zenity escaping, macOS encoding) | 025, 203, 105 | `internal/notify/window_linux.go`, `window_darwin.go`, `approval_linux.go`, approval.md:177 | Med | Opus | yes (approval.md:177 is wrong) | no | after F5 |
| **R55-F11** | IPC encoder and list sizes | 024, 023, 117 | `internal/ipc/ipc.go`, `internal/daemon/request_lifecycle.go`, `internal/request/{inbox,query}.go` | Med | Sonnet | no (restores the spec) | no | any time |
| **R55-F12** | Poison mail: receive dedupe, pending leaks, silent errors | 017, 058, 021, 067, 070, 065 | `internal/mail/receiver.go`, `internal/daemon/mail.go`, `internal/request/receive.go`, `internal/debate/apply.go`, `constraint.go`, `internal/worksession/mirror.go`, `grant_kinds.go` | Med | Opus | small (mail.md dedupe; debate close list rules) | no | before F18 (shares `mirror.go`) |
| **R55-F13** | Peer-driven storage, CPU and queue bounds | 018, 064, 057, 052, 051, 020, 079 | `internal/request/receive.go`, `internal/mail/receiver.go`, `internal/capability/store.go`, `internal/agentcard`, `internal/session`, `internal/mailbox`, `internal/mail/outbox.go`, `internal/capability/fetch.go` | Med | Opus | yes (request.md per-peer caps; agent-card.md size) | D12 | after F12 |
| **R55-F14** | Relay-driven audit and log volume | 015, 016, 042 | `internal/mail/audit.go`, `open.go`, `internal/session/session.go`, `internal/service/launchd.go`, `relayclient.go` | Med | Sonnet | yes (audit.md reject rows) | D10 | after F9 (shares `relayclient.go`) |
| **R55-F15** | Accounts gate (before `--accounts`) | 012, 013, 043, 044, 045, 046, 047, 048, 049, 191, 192, 193 | `internal/relay/accounts*.go`, `journal.go`, `backup.go`, `cmd/relay/{main,admin}.go`, `ci.yml` | Med | Opus | yes (restore "since"; journal ordering) | D3 | with R-4.2; before accounts or 4.1b backups |
| **R55-F16** | Relay operations robustness | 036, 037, 038, 039, 040, 050, 190, 196, 199, 096 (relay DSN part) | `internal/relay/{relay,queue,backup,pairing}.go`, `cmd/relay/main.go`, `deploy/early/setup.sh` | Low | Sonnet | small (pairing.md:198 or code) | no | after F2 |
| **R55-F17** | Daemon availability vs local agent | 030, 031, 083, 143, 146 | `internal/approval/store.go`, `internal/peers/pairing.go`, `internal/ipc/ipc.go` | Med | Opus (the KDF bound); Sonnet (the rest) | no | no | any time |
| **R55-F18** | Work-session integrity and the Phase 1 fallback | 022, 029, 062, 114, 115, 167, 168, 059, 060, 061 | `internal/worksession/*`, `internal/request/mirror.go`, `internal/daemon/{session,daemon}.go`, `internal/mail/receiver.go` (ack member) | Med | Opus | yes (work-session.md; mail.md ack distinguishes bad-body from unknown kind) | no | after F12 |
| **R55-F19** | Webhook and notification fixes | 026, 074, 075, 076, 120, 174, 175, 176 | `internal/notify/{dial,webhook,payload,trigger}.go`, `internal/daemon/notify.go`, `cmd/agentnet/notify.go` | Med | Sonnet | small (notify.md) | no | any time |
| **R55-F20** | Request-id collisions | 063, 128 | `internal/request/{query,receive}.go`, `internal/worksession/store.go`, `internal/audit/query.go`, `cmd/agentnet/debate.go`, `internal/daemon/{decision,debate,notify}.go` | Low | Sonnet | yes (request.md: refuse a colliding id) | no | after F18 (`worksession/store.go`) |
| **R55-F21** | Keystore backend semantics | 092, 091, 155, 089 | `internal/keystore/*`, `internal/identity`, `internal/mailbox`, `internal/paths/paths.go`, `cmd/agentnet/doctor.go` | Low | Opus | small (agent-card.md key storage) | no | before F8 |
| **R55-F22** | Windows local hardening | 090, 093, 094, 095, 096 (daemon DSN) | `internal/notify/{approval,desktop}_windows.go`, `internal/service/*`, `internal/logfile`, `internal/store` | Low | Sonnet | no | no | after F6 (`approval_windows.go`) |
| **R55-F23** | Agent-card and peer-input strictness | 019, 213, 073, 152, 153, 154, 072, 119, 086 | `internal/agentcard/*`, `tools/verifycard`, `internal/peers/pairing.go`, `internal/request/decode.go`, `cmd/agentnet/request.go`, `internal/daemon/{device,outbox}.go` | Med | Opus | yes (agent-card.md canonical rules: folded names, surrogates) | no | before F10 (both touch `agentcard.go`) |
| **R55-F24** | Presence and teams | 033, 106, 107, 108, 109, 112, 077, 068, 069, 113, 084, 082 | `cmd/agentnetd/main.go`, `internal/presence/*`, `internal/team/*`, `internal/daemon/{presence,team}.go`, `internal/session` (ping), `internal/daemon/trust.go` | Med | Sonnet | small (presence_set shape; ping-while-invisible) | D8 (team_invite, peers_verify) | any time |
| **R55-F25** | Device runner and link robustness | 098, 099, 085, 097, 161, 162, 145 | `internal/daemon/{device_run,device_scope,device}.go`, `internal/device/*` | Low | Opus | no | no | after F7 and F5 |
| **R55-F26** | Daemon lifecycle ordering | 100, 101, 102, 173, 158 | `internal/daemon/{daemon,mail,stop}.go`, `internal/store/store.go`, `cmd/agentnetd/install.go` | Low | Sonnet | no | no | after F12 (`daemon/mail.go`) |
| **R55-F27** | Fail-closed on rare errors | 080, 081, 147, 104 | `internal/daemon/{request,grant_kinds,grant}.go`, `internal/approval/store.go` | Low | Opus (authz) | no | no | after F17 (`approval/store.go`) |
| **R55-F28** | Clock-step robustness | 103, 053, 151 | `internal/mailbox/mailbox.go`, `internal/mail/receiver.go`, daemon handlers | Low | Opus | small (mail.md prune basis) | no | after F12 |
| **R55-F29** | Debate polish | 116, 126, 127, 169, 170, 071, 221, 222 | `internal/debate/*`, `cmd/agentnet/debate.go`, `internal/experience` | Low | Sonnet | yes (debate.md vs decision.md) | D14 | after F20 |
| **R55-F30** | CLI, doctor and log polish | 087, 118, 129, 130, 131, 134, 165, 177 | `cmd/agentnet/{inbox,request,log,doctor,device_scope}.go`, `internal/peers/pairing.go`, `internal/request/query.go` | Low | Sonnet | no | no | any time |
| **R55-F31** | Audit and status gaps; the post-commit audit rule | 121, 123, 124, 125, 135, 142, 160, 164, 200, 078 | `internal/{worksession,approval,capability,request,debate}`, `internal/daemon/*`, `audit_inventory_test.go`, e2e tests, `internal/capability/fetch.go` (Close flush) | Low | Sonnet | yes (audit.md rule) | D11 | after F18 |
| **R55-F32** | CI coverage | 202, 178, 179, 180 | `.github/workflows/ci.yml`, `release.yml`, `tools/verifyvectors`, `internal/notify` tests | Info | Lite | no | no | after F3 (workflow files) |
| **R55-F33** | Harness fixes | 136, 137, 138, 139, 140, 141, 181–188 | `tests/**` | Low | Lite | no | no | any time |
| **R55-F34** | Doc drift | 149, 150, 163, 166, 171, 172, 201, 204–212, 214–220, 223–226 | `Docs/protocol/*`, `Docs/cli/*` | Info | Lite | it is the spec change | D13, D14 | after the code tickets it describes |

**Top 5 by severity and reach:** R55-F1 (a stranger stops the hosted relay, 4.1p), R55-F3 (a
signed trojan release), R55-F4 (a forged "valid" verdict to anyone with a file), R55-F7 (the
peer reads `dorylinae.db` after a misleading approval), R55-F5 (approvals that hide what they
grant).

### 4.1 Per-ticket detail (acceptance tests)

- **R55-F1**
  - Acceptance: invert `internal/relay/zz_review55_C01-01_test.go`, `…C01-01v…` (honest mail forwarded, not `queued`, under a presence flood with default limits) and `…C01-02_test.go`, `…C01-02v…` (an honest 200-byte mail is delivered while 51 connections hold unfinished frames).
  - New tests: a frame that never completes is closed within N s; `cap(frame)` is charged.
  - Depends on D1.
- **R55-F3**
  - Acceptance: invert `tools/releasesign/zz_review55_C15-01_test.go` (`sign` refuses a SUMS whose digest differs from the expected one); the `install-sh` job passes (C15-03).
  - `release.yml` has `cache: false` in every job, and all actions are SHA-pinned (lint rule or grep test).
  - The runbook's step 2 names the run artifact or digest.
- **R55-F4**
  - Acceptance: invert `cmd/agentnet/zz_review55_C25-01_test.go` (no raw CR/ESC reaches stdout; the verdict is on its own line).
- **R55-F7**
  - Acceptance: invert `internal/daemon/zz_review55_C11-04_test.go` (all 10 spellings refused), `…T4-01…` (`subst`, volume GUID), `…C11-02…` (junction), `internal/device/zz_review55_C14-01_test.go`.
  - On macOS CI: `internal/daemon/zz_review55_T13-01_test.go` refuses all four firmlink spellings.
  - New: a UNC resource is refused without any dial (a test with an unreachable UNC host returns at once); a `.git` resource is refused; `EvalSymlinks(configDir)` failing refuses.
- **R55-F5**
  - Acceptance: invert `internal/daemon/zz_review55_C14-02_test.go` (a summary > `MaxWindowSummary` is refused).
  - New tests: grant and policy summaries contain the resolved path, scope, branch, public/sensitive, expiry/until and the grouped fingerprint; device link and scope, release and accept-result summaries contain name and fingerprint; `internal/daemon/zz_review55_T11-02_test.go` (zero-width decoys stripped).
- **R55-F8**
  - Acceptance: invert `internal/ipc/zz_review55_C16-01_test.go` (the client refuses a pipe served by another owner; `Listen` reports "held by another user"), plus the two-account manual check (D7).
  - New: two spellings of one dir give one pipe name and one keychain account.
- **R55-F2**
  - Acceptance: invert `internal/relay/zz_review55_T10-02_test.go` (redelivered bytes bounded per key and prefix) and `…C02-02_test.go` (a 1 GiB sweep in bounded batches, WAL truncated).
  - New: a non-base64 payload gets `bad_envelope` (T6a-02).
- **R55-F9**
  - Acceptance: invert `internal/relayclient/zz_review55_C28-01_test.go` and the C04-01 test (`LastError` bounded, no ESC).
  - New: `ping` and `pair` failure text is sanitised; the log line is bounded; the backoff does not reset on a sub-30 s connection.
- **R55-F10**
  - Acceptance: invert `cmd/agentnet/zz_review55_T11-04_test.go` (U+3164, VS, U+2800 escaped).
  - New vector: a card name with U+202E is refused (or escaped at every print site).
- **R55-F6**
  - Acceptance: `internal/notify/zz_review55_T11-01_test.go` and `…_linux_test.go` show the full text (run the Linux one in CI).
  - A manual zenity check with `\0` and `_` in the text.
- **R55-F11**
  - Acceptance: invert `internal/ipc/zz_review55_T6a-01_test.go`, `…T6c-01…` (200 000 `<` delivered) and the C19-01 test (64 × 16 KiB briefs listed).
- **R55-F12**
  - Acceptance: invert `internal/mail/zz_review55_T9-01_test.go` (a re-used id is a duplicate, 0 leaked entries), the C24-01 test (a repeated constraint list is refused as bad body) and `internal/worksession/zz_review55_C22-01_test.go`.
  - New: a non-rejection receive error is logged once per (from, id).
- **R55-F13**
  - Acceptance: invert `internal/peers/zz_review55_T10-05_test.go` (a card over 16 KiB is refused).
  - New: per-peer request caps are enforced; `grant_list` pages.
  - Depends on D12.
- **R55-F14**
  - Acceptance: invert `internal/audit/zz_review55_T10-01_test.go` (rejects from unpaired keys write no audit rows, or one row per window).
  - The macOS plist passes `--log-file`.
- **R55-F15**
  - Acceptance: invert `cmd/relay/zz_review55_C02-01_test.go` (the downloaded-copy restore replays the unbind; keep `…ctl…` passing), `internal/relay/zz_review55_C03-01_test.go` (bind rows bounded) and the C03-04 test (the journal is written before the commit).
- **R55-F17**
  - Acceptance: invert `internal/approval/zz_review55_C12-01_test.go` (no deadlock) and the C06-01 test (concurrent derivations bounded).
- **R55-F18**
  - Acceptance: invert the C21-01 test (A's request record holds the accepted result).
  - New: `ws_result` over IPC on a run session is refused; a bad-body ack does not trigger the fallback.
- **R55-F19**
  - Acceptance: invert the C27-01 test (no idle connection left after 40 deliveries).
- **R55-F21**
  - Acceptance: invert `internal/keystore/zz_review55_T3-01_test.go` (T3-01, T3-02) and `internal/mailbox/zz_review55_C05-01_test.go`.
- **R55-F23**
  - Acceptance: invert `internal/agentcard/zz_review55_T1-01_test.go` (a folded duplicate `public_Key` is refused).
- **R55-F28**
  - Acceptance: invert `internal/mailbox/zz_review55_T12-01_test.go` and `internal/mail/zz_review55_T12-02_test.go`.
- **Other tickets:** the acceptance is the cited reviewer test where one exists (C09-01, C10-01, C10-02, C16-03, C17-01, C20-01, C26-01, C30-01, C30-03), otherwise a new test per finding as described in its fix direction.

## 5. Deployment context: which findings apply to the early relay (4.1p)

4.1p as configured (`deploy/early/agentnet-relay.service`, `Caddyfile`): Hetzner, Caddy in front,
`--max-conns 2000 --max-inflight 48MiB --queue-max-total 1GiB`, **no `--accounts`**,
internet-facing.

| Applies as configured | Why |
|---|---|
| **R55-001** (C01-02) | One /24, ~50 connections, then no bandwidth: relay-wide 1013 closes, held indefinitely. Caddy has no stream timeout. The cheapest outage. |
| **R55-002** (C01-01) | ~4 prefixes and ~200 KiB/s stop all direct mail delivery. Cheapest at the 48 MiB flag. |
| **R55-009** (T10-02) | One /24 can saturate the uplink and the traffic allowance with redeliveries. |
| **R55-010** (T6a-02) | Four keys fill any offline victim's queue for 7 days. |
| **R55-011** (C02-02) | ~16 prefixes give an 11–14 s stall plus a 1 GiB WAL 7 days later (no livelock at the 1 GiB cap). |
| R55-034, R55-035 | Heap above `GOMEMLIMIT` with R55-002 at 2000 connections (~32 prefixes); CPU per invalid frame. |
| R55-036 | Every `systemctl restart` drops non-mail frames in flight. |
| O-015 (accepted) | Queue flooding without accounts. |

| Does **not** apply | Why |
|---|---|
| R55-012, R55-013, R55-043–R55-049 | Accounts off (`pairing.go:146-149` closes on account frames); no backup job yet (D40). |
| R55-050 | v1 pairing is off on a public relay unless `--allow-pairing-v1`. |
| R55-014, R55-015, R55-016, R55-041, R55-051, R55-052 | They need a *hostile relay*. On 4.1p that means a compromised host. The threat model treats our relay as untrusted anyway, so these matter for the daemons that connect to it. |

**Should any block the deploy?** None discloses content, keys or identities. All are availability
(and cost) issues that a stranger can trigger once they know the URL. My recommendation:
- The deploy need not wait for them *as a private relay for the owner and a few known testers*,
  **if** the owner accepts that anyone who finds `relay.dorylinae.net` can stop mail delivery for
  everyone. A restart does not help while the attacker holds its connections.
- **R55-F1 (R55-001, R55-002) should land before the URL is shared beyond known testers**, and
  R55-F2 before the beta.
- If the owner wants less exposure now: keep the URL unpublished, and watch `/healthz` plus the
  relay's `event=limit` lines (they log at Warn by default).

This is the owner's call (§6 D1).

## 6. Owner decisions needed

- **D1: 4.1p deploy.** Accept the availability risk of R55-001/002/009/010/011 for the private
  early relay (recommended: yes, with F1 before the URL is shared), or wait for R55-F1. F1 also
  **reopens R52 H1** ("close the reader, never wait"): choose a per-frame deadline plus eviction
  of the largest holder.
- **D2: R55-009 (T10-02) severity.** Keep Medium (it needs matching download bandwidth), or treat it
  as High and ask for a verification. It applies to 4.1p either way.
- **D3: Accounts gate.** R55-012/013 (lowered from High) and R55-043…049 must be fixed, and R-4.2
  done, before `--accounts` is turned on or the 4.1b backup job is deployed. Also set
  `--security-journal` in the unit before accounts.
- **D4: Release process.** No release has been cut. R55-003 (C15-01) is a flaw D36/OD-P4-19 did
  not consider, and the runbook must change before the first signed release. R55-111 (C15-03)
  blocks the draft job anyway. Decide the binding: the run artifact, a digest in the job summary,
  or a local rebuild.
- **D5: D33 revisit (O-177, `decision.export` not audited).** Facts from C25: export and
  `debate_show` read content locally and write no audit row. Nothing leaks from them, and C25
  found the export path sound (atomic `--out`, R48 L5/L6/M3 fixed). The only new C25 finding is
  R55-004, which is display. **Recommendation: keep D33** (a local read by the same user is not
  a boundary crossing). Record the revisit as done.
- **D6: T13-01 (in R55-006).** Confirmed by reading only. Run
  `go test ./internal/daemon -run TestReview55T1301 -count=1 -v` on the CI `macos-latest` runner
  (the test is `//go:build darwin`). That run settles it. Until then, treat it as High.
- **D7: C16-01 cross-user leg.** A two-account manual check on Windows (user B squats user A's
  pipe name; A runs `agentnetd run` and `agentnet status`).
- **D8: Local-agent gates the spec does not cover.** Should `peers_verify` (R55-082) and
  owner-side `team_invite` (R55-084) need a human approval, or be documented as agent-grantable?
- **D9: Device link binding (R55-005).** Approve a spec change so the human types (or compares)
  the other device's fingerprint *in the approval window*, not only via the IPC parameter.
- **D10: Reopened choices.** Pruning or aggregating relay-driven reject rows in the append-only
  audit log (R55-015; audit.md:294); rotating the macOS launchd log (R55-016;
  agentnetd-install.md §Logs).
- **D11: Post-commit audit rule (R55-142).** Pick one: "log and report success" or "`AppendTx`
  for security-relevant actions". Today it is inconsistent.
- **D12: Retention (O-171 plus R55-018).** Retention of content in `requests`, `mail_inbox`,
  `work_sessions` and debates, and per-peer caps on stored requests.
- **D13: D20's condition is unmet (R55-216).** The Linux argv exposure and the `hidepid=2` note
  are not documented. Document it (F34) or revisit D20.
- **D14: Small spec choices.** B abandoning after its answer (R55-221); OneDrive cloud files
  refused (R55-201); tombstone before D5 (R55-163); suspended senders' queued rows (R55-193);
  bare URLs in Slack/Discord (R55-175).

## 7. Delta for 02-open-findings.md (for the Orchestrator to apply)

**Found fixed**
- **O-035:** the key is saved before the row, and the secret is deleted on failure (C05 Checked,
  T6a-24). Residual: R55-155 (no directory fsync).
- **O-068:** every presence plaintext is padded to one fixed worst-case size
  (`internal/presence/body.go:85-100`; T6a-24). C08 said it was open; I settled this by reading.
- **O-115:** the keychain account is now `webhook-<H(dir)>` (`internal/daemon/daemon.go:712`;
  T3, T6a-20, T13). notify.md is stale (R55-211).
- **O-130:** the early-complete close is audited (`internal/worksession/hooks.go:125-146,189`;
  C22, T6b).
- **O-134:** `grant.orphan` carries `{grant, peer}` (`internal/daemon/grant_kinds.go:73,113`;
  T6b, T6c).

**Partly fixed**
- **O-033:** v2 `verifyPeer` rejects our own key (`peers/pairing.go:648`); v1 still does not (C06).
- **O-087:** invite tables are pruned on insert, not daily/at start (C20-05 → R55-166).
- **O-126:** `CONIN$`/`CONOUT$`, superscript COM/LPT, C1/bidi and trailing dot/space are all
  refused (`capability/token.go:320-363`); only `COM0`/`LPT0` remain (C09, T13).

**Escalated (new actor, impact or path)**

| Row | Escalated by | New evidence |
|---|---|---|
| O-015 | R55-009, R55-010 | Redelivery amplification; junk that survives reconnects for 7 days |
| O-034 | R55-031 | The 16-pending bound does not bound derivations; local IPC reaches OOM |
| O-043 | R55-019 | An arbitrary unsigned card for any key, not only a replay |
| O-051 | R55-015, R55-042 | Relay-driven unprunable audit rows; log volume |
| O-058 | R55-018, R55-064 | Second copy in `requests`; rate still allowed; held grants |
| O-067 | R55-005 | Name-only approval windows for device link and scope |
| O-077 | R55-017 | A paired peer drives unbounded heap via re-used ids |
| O-100 | R55-054, R55-055, R55-056, R55-004 | New sinks (`session`, `--json`, multi-line fields, verify reason); `termSafe` itself too weak |
| O-108 | T6a-24 (note) | Also a team `Get`, two `ShowKey` calls and `PeekTypeTitle` in the After hook |
| O-129 | R55-005, R55-066 | The grant path never calls `stripLongDigits`; zero-width bypass |
| O-131 | R55-067 | The reopen makes a later close fail forever (experience PK) |
| O-167 | R55-007 | The local-agent hide-a-command exploit, with a test |
| O-178 | R55-023, R55-024 | Inbox lists too; HTML escaping ×6 |
| O-185 | R55-032 | The cache reaches the job that produces the release files |
| O-069 | R55-077 | `ping` is an on-demand liveness oracle while invisible |

**§D correction:** "R05 L6 (relay `Close` does not wait) → closed by 4.1a" is only half true. The
drain wait was built, but `Close` still closes the DB before the read loops end
(relay-hosted.md:302). Reopen it as R55-036.

**Re-checked and still open as recorded** (by the report in brackets):
- O-001, O-002, O-009, O-010, O-012, O-013, O-025, O-027, O-028, O-029 (C01)
- O-003–O-008, O-020, O-022, O-024 (C02)
- O-014, O-021, O-023 (C04)
- O-030 (C05, T4, T13)
- O-031, O-063, O-064, O-075, O-083, O-085, O-086, O-192 (C08)
- O-032 (C16, C28)
- O-036, O-047, O-188 (T12)
- O-040, O-049, O-050, O-052, O-053, O-054, O-055, O-056, O-057, O-060, O-061, O-062 (C07, T6a)
- O-045, O-041 (T3, T6a)
- O-070, O-108–O-114, O-116–O-119 (C27)
- O-073, O-093, O-094, O-174 (C17)
- O-076, O-078, O-081, O-082 (C20, T7)
- O-101–O-104, O-106, O-120, O-148 (C18)
- O-127, O-128, O-136–O-141, O-147, O-164, O-166 (C12)
- O-132, O-146, O-145 (C09, T8)
- O-143, O-144, O-189 (C10)
- O-151, O-161, O-162, O-203 (T4, T13)
- O-156, O-159, O-160 (C14, T13)
- O-165, O-168, O-170, O-175, O-176, O-177 (by decision, D5), O-179 (C23, C24, T6c)
- O-169, O-172, O-173 (C26)
- O-181, O-182, O-183 (C15, T3)
- O-187 (C03: checked item by item; its open items are R55-043–R55-046)
- O-197 (C05)

Rows not mentioned were not re-examined (outside every unit's path, or doc/process rows such
as O-190, O-194–O-196, O-199–O-202).

## 8. Appendix A: false positives and duplicates

**False positives:** none. All 14 verifications returned `confirmed` with the reported severity.

**Duplicates merged** (original id → R55 id):
- C04-01, C28-01 → R55-014
- T6a-01, T6c-01 → R55-024
- T2-02, T6c-02 → R55-036
- T2-03, T10-07 → R55-083
- T5-02, T6c-04 → R55-135
- T6a-09, T9-04 → R55-058
- C11-06, C12-02 → R55-005
- C21-03, C22-03 (and C18-02, C23-01, T6c-07, C19-04, T6c-22) → R55-063
- C30-02, C31-03 → R55-137
- C30-04, C31-01 → R55-139
- C16-05, T13-03 → R55-088
- C12-03, T4-02 → R55-090
- Root-cause merges (different symptoms, one fix): R55-005, R55-006, R55-014, R55-015,
  R55-027, R55-055, R55-056, R55-092, R55-142. Aliases are listed in each entry.

**Corrections to source reports** (so they are not re-raised):
- C16 "Checked and fine: results encoded with HTML escaping off (`ipc.go:197-205`)" is wrong (R55-024).
- C08 "O-068 still present" is wrong (see §7).
- T6a's 73 and T8's 70 registered IPC methods are wrong: there are 71.
- Wrong citations: C02-01 `journal.go:216-219` (really `:111-114`); T8-01 `window.go:179-184` (really `window.go:36-42`, `store.go:201-211`); C07 `open.go:409-474` (really `:158-223`).
- The reviewer's own T11-01 test was overwritten by the verifier's independent port (`verify/T11-01.md`). The port is the evidence now.

## 9. Appendix B: per-unit summaries

Model for every unit: `claude-opus-5-5`. **(reused)** means the unit ran in a reused slot whose
context may not have been fully cleared. Every other unit had a brand-new worker.

| Unit | Reviewer | Min | C/H/M/L/I | One line |
|---|---|---|---|---|
| C01 | CR-R1 | 12 | 0/2/0/2/2 | Relay budget fairness broken twice (R55-001/002) |
| C02 | CR-R2 | 6 | 0/1/1/4/1 | Restore replays nothing from a downloaded backup; sweep stall |
| C03 | CR-R3 | 6 | 0/1/3/2/3 | Accounts core sound; bind-row disk flood; revocation fragile |
| C04 | CR-R4 | 4 | 0/0/1/1/2 | Wire format tight; relay error text raw in status/doctor |
| C05 | CR-R5 | 10 | 0/0/0/3/3 | Identity/Noise sound; keystore folds unavailable into not-found |
| C06 | CR-R6 | ~35 | 0/0/1/3/0 | Pairing crypto sound; Argon2 not bounded |
| C07 | CR-R7 | ~25 | 0/0/1/2/1 | Mail core sound; `keys` re-seal amplification |
| C08 | CR-R8 | ~5 | 0/0/1/5/1 | Sessions sound; idle never wired |
| C09 (reused, after C04) | CR-R4 | 6 | 0/0/0/3/2 | Tokens/fs serving sound; holder robustness |
| C10 (reused, after C07) | CR-R7 | 8 | 0/0/0/2/1 | Fetch/git sound; audit flush and budget window |
| C11 (reused, after C02) | CR-R2 | 13 | 0/2/3/3/1 | Spelling bypass, approval summaries, UNC dial, `.git` |
| C12 (reused, after C06) | CR-R6 | 6 | 0/0/2/2/1 | Code gate sound; List deadlock; raw names |
| C13 (reused, after C03) | CR-R3 | 14 | 0/0/0/1/2 | Link and ownership walk sound; `#!` interpreter |
| C14 (reused, after C08) | CR-R8 | 9 | 0/1/0/3/1 | Runner careful; scope summary cut at 4096 |
| C15 (reused, after C01) | CR-R1 | ~35 | 0/1/1/2/3 | Bytes checks hold; signing a mutable draft; actions and cache |
| C16 | CR-C16 | ~25 | 0/1/1/4/2 | Pipe squatting; macOS log unbounded |
| C17 | CR-C17 | ~8 | 0/0/0/2/1 | Receive strict; `""` members; D5 missing row |
| C18 | CR-C18 | 5 | 0/0/0/2/1 | Lifecycle sound; decline after accept; id collision |
| C19 | CR-C19 | ~25 | 0/0/1/2/2 | Inbox list over 1 MiB |
| C20 | CR-C20 | 6 | 0/0/0/4/1 | Team trust sound; leave/rejoin gaps |
| C21 | CR-C21 | 7 | 0/0/1/4/1 | B completes with a non-accepted result |
| C22 | CR-C22 | ~25 | 0/0/0/4/2 | O-131 poison; fallback closes debates |
| C23 | CR-C23 | 6 | 0/0/0/4/1 | Debate core sound; CLI edges |
| C24 | CR-C24 | 5 | 0/0/1/1/1 | Repeated constraint ids poison the close |
| C25 | CR-C25 | 4 | 0/1/0/0/1 | Verify core sound; human output spoofable |
| C26 | CR-C26 | ~6 | 0/0/0/1/2 | Chain sound; `file:` DSN parsing |
| C27 | CR-C27 | 5 | 0/0/1/1/3 | Webhook transport leak |
| C28 | CR-C28 | 7 | 0/0/1/3/1 | Wiring sound; relay `last_error` raw |
| C29 | CR-C29 | ~20 | 0/0/0/0/3 | Verifier independent; cross-check gaps |
| C30 | CR-C30 | 5 | 0/0/0/5/4 | Harness assertions that cannot fail; real keychain |
| C31 | CR-C31 | 6 | 0/0/0/3/4 | PowerShell harness: keychain clutter, dead timeouts |
| T1 | TH-T1 | ~70 | 0/0/1/2/1 | Parsing strict; card name-folding differential |
| T2 | TH-T2 | ~65 | 0/0/0/3/1 | Locking disciplined; shutdown edges |
| T3 | TH-T3 | ~70 | 0/0/0/2/1 | Crypto sound end to end; keystore shadowing |
| T4 | TH-T4 | ~95 | 0/0/1/3/2 | `subst`/GUID spellings; PATH tools |
| T5 | TH-T5 | ~60 | 0/0/0/1/2 | No content in audit; relay text in the log |
| T6a | TH-T6a | 247 wall | 0/0/2/10/12 | ~750 statements; IPC escaping; junk payloads |
| T6b | TH-T6b | ~45 | 0/0/0/5/6 | Phase 2 follows its specs; audit gaps |
| T6c | TH-T6c | 243 wall | 0/0/1/13/8 | Phase 3/4 drift; doctor/install edges |
| T7 | TH-T7 | ~90 | 0/0/0/1/2 | Authz fails closed; revocation sweep |
| T8 | TH-T8 | 13 | 0/1/2/1/2 | The human's view is the weak point |
| T9 | TH-T9 | 11 | 0/0/1/4/1 | Durability holds; inbox re-admit leak |
| T10 | TH-T10 | 13 | 0/0/3/4/0 | Relay redelivery; audit growth; request growth |
| T11 | TH-T11 | ~12 | 0/0/1/4/0 | zenity escaping; sanitiser strength |
| T12 | TH-T12 | 7 | 0/0/0/2/1 | Time windows match the specs; local clock steps |
| T13 | TH-T13 | ~9 | 0/1/0/2/3 | macOS firmlink; Windows owner-only dir |
| V C01-01 (reused, after V C02-02) | CR-V2 | ~15 | confirmed | 4.1p cheapest |
| V C01-02 | CR-V-C01-02 | 25 | confirmed | One /24 full size |
| V C02-01 | CR-V1 | 10 | confirmed | With control test |
| V C02-02 | CR-V2 | 5 | confirmed (M) | Measured stalls |
| V C03-01 (reused, after V C02-01) | CR-V1 | 15 | confirmed | Not reachable on 4.1p |
| V C11-01 | V-C11-01 | ~12 | confirmed | Read only |
| V C11-04 | V-C11-04 | 8 | confirmed | Leads: scope repos, SMB dial |
| V C14-02 | V-C14-02 | 2 | confirmed | — |
| V C15-01 | V-C15-01 | 6 | confirmed | Tool side tested |
| V C16-01 | V-C16-01 | ~12 | confirmed | Cross-user by reading |
| V C25-01 | V-C25-01 | ~8 | confirmed | — |
| V T8-01 | V-T8-01 | ~2 | confirmed | Read only |
| V T11-01 | V-T11-01 | ~15 | confirmed (M) | Mnemonic lead; test overwritten |
| V T13-01 | V-T13-01 | 2 | confirmed | macOS run pending |

