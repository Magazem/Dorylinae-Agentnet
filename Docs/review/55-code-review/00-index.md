# Code Review Index (Review 55)

## 1. Package Map

### cmd/ packages (main binaries)

| Package | Path | Prod Lines | Test Lines | Purpose |
|---------|------|-----------|-----------|---------|
| agentnet | cmd/agentnet | 7421 | 6458 | (no package doc) |
| agentnetd | cmd/agentnetd | 460 | 511 | (no package doc) |
| relay | cmd/relay | 1005 | 1608 | (no package doc) |

### internal/ packages

| Package | Path | Prod Lines | Test Lines | Purpose |
|---------|------|-----------|-----------|---------|
| agentcard | internal/agentcard | 406 | 202 | defines the signed A2A-style Agent Card: its schema, the |
| approval | internal/approval | 1455 | 1670 | implements human approval of agent-triggered actions |
| audit | internal/audit | 972 | 944 | holds the append-only audit log of every action the daemon takes. |
| capability | internal/capability | 3185 | 3099 | issues and verifies capability tokens: scoped, expiring, |
| config | internal/config | 3 | 0 | locates and loads per-user configuration and data directories. |
| daemon | internal/daemon | 8822 | 12443 | runs the agentnetd lifecycle: open the store, serve the local |
| debate | internal/debate | 4378 | 3621 | holds the debate entry schemas of Docs/protocol/debate.md |
| decision | internal/decision | 1517 | 1061 | builds, signs and verifies Decision records |
| device | internal/device | 2076 | 1569 | stores the own-device link of Docs/protocol/device.md (owner |
| envelope | internal/envelope | 862 | 470 | defines the relay wire format: the routed Envelope, the |
| experience | internal/experience | 236 | 253 | builds and stores the private experience record |
| identity | internal/identity | 207 | 218 | manages the Ed25519 agent identity: generating the keypair |
| idle | internal/idle | 212 | 102 | reports how long the user has been away from the keyboard and |
| ipc | internal/ipc | 387 | 112 | implements the local socket API between the CLI and the daemon. |
| keystore | internal/keystore | 372 | 161 | stores one small secret (the identity seed) in the OS |
| logfile | internal/logfile | 95 | 95 | is a size-capped log file: when a write would push it past |
| mail | internal/mail | 2066 | 1780 | seals and opens application mail between paired daemons: an |
| mailbox | internal/mailbox | 513 | 394 | holds the daemon's own mailbox keys (Docs/protocol/mail.md |
| noise | internal/noise | 251 | 225 | wraps flynn/noise for Dorylinae sessions: the XX handshake, |
| notify | internal/notify | 2918 | 2042 | shows local desktop notifications for request lifecycle |
| paths | internal/paths | 75 | 44 | locates the per-user config directory and the files and |
| peers | internal/peers | 1762 | 2379 | stores the agents this daemon has paired with and runs the |
| presence | internal/presence | 1368 | 738 | implements Docs/protocol/presence.md: the presence |
| protocol | internal/protocol | 3 | 0 | defines wire messages and schemas; see docs/protocol/. |
| relay | internal/relay | 4448 | 5302 | is the AgentNet relay server: it authenticates daemons by a |
| relayclient | internal/relayclient | 694 | 1251 | is the daemon's side of the relay protocol |
| request | internal/request | 4215 | 4054 | implements the request object of Docs/protocol/request.md: |
| service | internal/service | 609 | 403 | registers agentnetd as a per-user service that starts at |
| session | internal/session | 929 | 326 | runs end-to-end encrypted sessions between paired daemons |
| store | internal/store | 652 | 635 | wraps the SQLite persistence layer and its schema migrations. |
| team | internal/team | 1485 | 1521 | implements Docs/protocol/team.md: the team store, the |
| testutil | internal/testutil | 81 | 0 | holds helpers shared by tests. |
| transport | internal/transport | 3 | 0 | implements Noise-secured sessions and relay connections. |
| version | internal/version | 207 | 148 | holds build metadata and the shared --help/--version |
| worksession | internal/worksession | 2931 | 2049 | implements the work session object of |

### tools/ packages

| Package | Path | Prod Lines | Test Lines | Purpose |
|---------|------|-----------|-----------|---------|
| binversion | tools/binversion | 200 | 53 | (no package doc) |
| releasesign | tools/releasesign | 505 | 179 | (no package doc) |
| specvectors | tools/specvectors | 545 | 0 | (no package doc) |
| verifycard | tools/verifycard | 253 | 74 | (no package doc) |
| verifyvectors | tools/verifyvectors | 1342 | 78 | (no package doc) |

### tests/ packages

| Package | Path | Prod Lines | Test Lines | Purpose |
|---------|------|-----------|-----------|---------|
| harness/standin | tests/harness/standin | 600 | 0 | (no package doc) |

## 2. Non-Go Code

### Scripts and Configuration Files

| File | Lines |
|------|-------|
| scripts/install.sh | 326 |
| .github/dependabot.yml | 6 |
| .github/workflows/ci.yml | 215 |
| .github/workflows/phase2-harness.yml | 41 |
| .github/workflows/phase3-harness.yml | 43 |
| .github/workflows/release.yml | 325 |
| .github/workflows/sensitive-paths.yml | 82 |
| .golangci.yml | 23 |
| .gitattributes | 3 |

### Test Scripts

| File | Lines |
|------|-------|
| tests/harness/phase1-agents.sh | 375 |
| tests/harness/phase1-agents.ps1 | 600 |
| tests/harness/phase2-agents.sh | 387 |
| tests/harness/phase2-agents.ps1 | 626 |
| tests/harness/phase3-agents.sh | 585 |
| tests/harness/phase3-agents.ps1 | 830 |
| tests/install/cases.sh | 118 |
| tests/install/run.sh | 130 |
| tests/phase0-smoke.sh | 151 |
| tests/phase0-smoke.ps1 | 214 |
| tests/phase1-smoke.sh | 444 |
| tests/phase1-smoke.ps1 | 461 |

### Deployment and Packaging

| File | Lines |
|------|-------|
| deploy/early/agentnet-relay.service | 66 |
| deploy/early/Caddyfile | 35 |
| deploy/early/setup.sh | 133 |
| packaging/homebrew/agentnet.rb | 51 |
| packaging/homebrew/render.sh | 37 |

## 3. Entry Points

### 3.a IPC Methods (daemon serves)

Registered in internal/daemon/daemon.go and handler files:

| Method | File:Line |
|--------|-----------|
| approval_list | internal/daemon/approval.go:118 |
| approval_open | internal/daemon/approval.go:129 |
| approval_reject | internal/daemon/approval.go:141 |
| audit_head | internal/daemon/audit.go:87 |
| audit_list | internal/daemon/audit.go:61 |
| audit_verify | internal/daemon/audit.go:74 |
| debate_constrain | internal/daemon/debate_constrain.go:82 |
| debate_list | internal/daemon/debate.go:261 |
| debate_show | internal/daemon/debate.go:287 |
| debate_submit | internal/daemon/debate.go:299 |
| decision_list | internal/daemon/decision.go:122 |
| decision_show | internal/daemon/decision.go:156 |
| device_link | internal/daemon/device.go:358 |
| device_list | internal/daemon/device.go:499 |
| device_scope_clear | internal/daemon/device_scope.go:279 |
| device_scope_set | internal/daemon/device_scope.go:191 |
| device_scope_show | internal/daemon/device_scope.go:311 |
| device_unlink | internal/daemon/device.go:511 |
| fetch_start | internal/daemon/fetch_client.go:557 |
| fetch_status | internal/daemon/fetch_client.go:564 |
| grant_create | internal/daemon/grant.go:381 |
| grant_list | internal/daemon/grant.go:631 |
| grant_policy_add | internal/daemon/grant.go:866 |
| grant_policy_list | internal/daemon/grant.go:968 |
| grant_policy_remove | internal/daemon/grant.go:980 |
| grant_revoke | internal/daemon/grant.go:668 |
| grant_show | internal/daemon/grant.go:649 |
| identity | internal/daemon/daemon.go:622 |
| inbox_list | internal/daemon/request_lifecycle.go:487 |
| mail_submit | internal/daemon/outbox.go:76 |
| notify_get | internal/daemon/notify.go:143 |
| notify_set | internal/daemon/notify.go:147 |
| pair_new | internal/daemon/pairing.go:43 |
| pair_redeem | internal/daemon/pairing.go:50 |
| pair_status | internal/daemon/pairing.go:61 |
| peers | internal/daemon/pairing.go:72 |
| peers_remove | internal/daemon/trust.go:81 |
| peers_verify | internal/daemon/trust.go:50 |
| ping | internal/daemon/ping.go:74 |
| ping_status | internal/daemon/ping.go:89 |
| presence_get | internal/daemon/presence.go:30 |
| presence_set | internal/daemon/presence.go:34 |
| request_accept | internal/daemon/request_lifecycle.go:319 |
| request_cancel | internal/daemon/request_lifecycle.go:398 |
| request_complete | internal/daemon/request_lifecycle.go:375 |
| request_decline | internal/daemon/request_lifecycle.go:335 |
| request_defer | internal/daemon/request_lifecycle.go:353 |
| request_list | internal/daemon/request_lifecycle.go:454 |
| request_resend | internal/daemon/request_lifecycle.go:416 |
| request_show | internal/daemon/request_lifecycle.go:428 |
| request_submit | internal/daemon/request.go:177 |
| shutdown | internal/daemon/daemon.go:612 |
| status | internal/daemon/daemon.go:630 |
| team_create | internal/daemon/team.go:173 |
| team_delete | internal/daemon/team.go:348 |
| team_invite | internal/daemon/team.go:133 |
| team_join | internal/daemon/team.go:162 |
| team_leave | internal/daemon/team.go:320 |
| team_list | internal/daemon/team.go:188 |
| team_remove | internal/daemon/team.go:255 |
| team_rename | internal/daemon/team.go:291 |
| team_show | internal/daemon/team.go:213 |
| ws_accept_result | internal/daemon/session.go:364 |
| ws_cancel | internal/daemon/session.go:480 |
| ws_discard | internal/daemon/session.go:457 |
| ws_list | internal/daemon/session.go:237 |
| ws_release | internal/daemon/session.go:507 |
| ws_request_changes | internal/daemon/session.go:434 |
| ws_result | internal/daemon/session.go:286 |
| ws_show | internal/daemon/session.go:270 |

### 3.b CLI Commands

#### agentnet (cmd/agentnet/main.go:38-128)

| Command | Handler | Line |
|---------|---------|------|
| approve | runApprove | 105 |
| accept | runAccept | 85 |
| accept-result | runAcceptResult | 100 |
| complete | runComplete | 91 |
| consult | runConsult | 73 |
| debate | runDebate | 75 |
| debates | runDebates | 77 |
| decision | runDecision | 79 |
| decisions | runDecisions | 81 |
| decline | runDecline | 87 |
| defer | runDefer | 89 |
| device | runDevice | 107 |
| doctor | runDoctor | 57 |
| fetch | runFetch | 115 |
| grant | runGrant | 109 |
| grants | runGrants | 111 |
| help | usage | 44 |
| identity | runIdentity | 59 |
| inbox | runInbox | 83 |
| log | runLog | 117 |
| mail | runMail | 119 |
| notify | runNotify | 103 |
| pair | runPair | 61 |
| peers | runPeers | 63 |
| ping | runPing | 69 |
| presence | runPresence | 67 |
| request | runRequest | 71 |
| revoke | runRevoke | 113 |
| session | runSession | 95 |
| sessions | runSessions | 93 |
| status | runStatus | 55 |
| stop | runStop | 53 |
| team | runTeam | 65 |
| version | version.Command | 51 |
| wait | runWait | 99 |
| result | runResult | 97 |

#### agentnetd (cmd/agentnetd/main.go)

| Command | Handler |
|---------|---------|
| install | runInstall |
| relayurl | runRelayURL |
| stop | runStop |
| version | version.Command |

#### relay (cmd/relay/main.go:41-52)

| Command | Handler | Line |
|---------|---------|------|
| admin | runAdmin | 51 |
| backup | runBackup | 47 |
| restore | runRestore | 49 |
| version | version.Command | 45 |

### 3.c Wire Frame Operations and Mail Kinds

#### Op Constants (internal/envelope/frames.go)

| Constant | Value | File:Line |
|----------|-------|-----------|
| OpChallenge | "challenge" | internal/envelope/frames.go:11 |
| OpAuth | "auth" | internal/envelope/frames.go:12 |
| OpReady | "ready" | internal/envelope/frames.go:13 |
| OpError | "error" | internal/envelope/frames.go:14 |
| OpQueued | "queued" | internal/envelope/frames.go:19 |
| OpAck | "ack" | internal/envelope/frames.go:20 |
| OpPairNew | "pair_new" | internal/envelope/frames.go:23 |
| OpPairCode | "pair_code" | internal/envelope/frames.go:24 |
| OpPairRedeem | "pair_redeem" | internal/envelope/frames.go:25 |
| OpPairPeer | "pair_peer" | internal/envelope/frames.go:26 |
| OpPairCancel | "pair_cancel" | internal/envelope/frames.go:27 |
| OpBindStart | "bind_start" | internal/envelope/frames.go:31 |
| OpBindPending | "bind_pending" | internal/envelope/frames.go:32 |
| OpBindPoll | "bind_poll" | internal/envelope/frames.go:33 |
| OpBindDone | "bind_done" | internal/envelope/frames.go:34 |
| OpBindCancel | "bind_cancel" | internal/envelope/frames.go:35 |
| OpUnbind | "unbind" | internal/envelope/frames.go:36 |
| OpAccountChanged | "account_changed" | internal/envelope/frames.go:37 |

#### Kind Constants (internal/ packages)

| Constant | Value | File:Line |
|----------|-------|-----------|
| KindGrant | "grant" | internal/approval/approval.go:23 |
| KindGrantPolicy | "grant_policy" | internal/approval/approval.go:24 |
| KindRelease | "release" | internal/approval/approval.go:25 |
| KindAcceptResult | "accept_result" | internal/approval/approval.go:26 |
| KindDeviceLink | "device_link" | internal/approval/approval.go:27 |
| KindDeviceScope | "device_scope" | internal/approval/approval.go:28 |
| KindDebateConstraint | "debate_constraint" | internal/approval/approval.go:31 |
| KindFS | "fs" | internal/capability/token.go:41 |
| KindGit | "git" | internal/capability/token.go:42 |
| KindPosition | "position" | internal/debate/schema.go:19 |
| KindMove | "move" | internal/debate/schema.go:20 |
| KindProposal | "proposal" | internal/debate/schema.go:21 |
| KindAnswer | "answer" | internal/debate/schema.go:22 |
| KindLink | "device.link" | internal/device/device.go:43 |
| KindUnlink | "device.unlink" | internal/device/device.go:44 |
| KindWork | "work" | internal/experience/experience.go:22 |
| KindDebate | "debate" | internal/experience/experience.go:23 |
| KindAccept | "request.accept" | internal/request/view.go:23 |
| KindDecline | "request.decline" | internal/request/view.go:24 |
| KindDefer | "request.defer" | internal/request/view.go:25 |
| KindComplete | "request.complete" | internal/request/view.go:26 |
| KindCancelled | "request.cancelled" | internal/request/view.go:27 |
| KindCancel | "request.cancel" | internal/request/view.go:28 |
| SessionKindWork | "work" | internal/worksession/worksession.go:39 |
| SessionKindDebate | "debate" | internal/worksession/worksession.go:40 |
| KindResult | "ws.result" | internal/worksession/worksession.go:51 |
| KindState | "ws.state" | internal/worksession/worksession.go:52 |
| KindCancel | "ws.cancel" | internal/worksession/worksession.go:53 |

### 3.d HTTP Paths (relay)

| Path | Purpose | File:Line |
|------|---------|-----------|
| /v1/connect | WebSocket endpoint for daemon connections | internal/envelope/envelope.go:22 |
| /healthz | Health check (relay.HealthPath) | cmd/relay/main.go:74 |
| /metrics | Prometheus metrics (metrics listener) | cmd/relay/main.go:246 |

### 3.e OS Process Execution

#### Production code

| File:Line | Code |
|-----------|------|
| internal/capability/git.go:118 | `cmd := exec.CommandContext(ctx, gitPath, args...)` |
| internal/device/testdata/runnerhelper/main.go:47 | `child := exec.Command(exe, "beat", os.Args[2])` |
| internal/device/testdata/runnerhelper/main.go:57 | `child := exec.Command(exe, "beat", os.Args[2])` |
| internal/idle/idle.go:38 | `cmd := exec.CommandContext(ctx, name, args...)` |
| internal/notify/desktop.go:34 | `cmd := exec.CommandContext(ctx, name, args...)` |
| internal/notify/window_darwin.go:63 | `cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", approvalDialogScript)` |
| internal/notify/window_linux.go:185 | `cmd = exec.CommandContext(ctx, path,` |
| internal/notify/window_linux.go:192 | `cmd = exec.CommandContext(ctx, path,` |
| internal/notify/window_windows.go:167 | `cmd := exec.CommandContext(ctx, psPath,` |
| internal/service/service.go:119 | `return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()` |

**In tests** (18 occurrences)

| File:Line | Code |
|-----------|------|
| internal/capability/fetch_windows_test.go:15 | `if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()` |
| internal/capability/git_test.go:108 | `cmd := exec.Command(r.git, args...)` |
| internal/capability/git_test.go:573 | `cmd := exec.CommandContext(ctx, r.git, "commit", "-q", "--allow-empty", "-m", "control")` |
| internal/daemon/device_run_e2e_test.go:56 | `if out, err := exec.Command("go", "build", "-o", runHelperPath, "../device/testdata/runnerhelper").CombinedOutput()` |
| internal/daemon/e2e_2_9_test.go:315 | `cmd := exec.Command(gitPath, args...)` |
| internal/daemon/grant_flow_test.go:251 | `cmd := exec.Command(gitPath, args...)` |
| internal/daemon/grant_review37_test.go:23 | `cmd := exec.Command(gitPath, args...)` |
| internal/daemon/quarantine_e2e_test.go:201 | `cmd := exec.Command(gitPath, args...)` |
| internal/decision/markdown_test.go:337 | `out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()` |
| internal/device/perm_windows_test.go:146 | `if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()` |
| internal/device/runner_test.go:40 | `out, err := exec.Command("go", "build", "-o", helperPath, "./testdata/runnerhelper").CombinedOutput()` |
| cmd/agentnet/e2e_test.go:22 | `cmd := exec.Command("go", "build", "-o", out, pkg)` |
| cmd/agentnet/e2e_test.go:46 | `c := exec.Command(cli, "status", "--json")` |
| cmd/agentnet/e2e_test.go:66 | `d := exec.Command(dmn)` |
| cmd/agentnet/identity_e2e_test.go:73 | `c := exec.Command(name, args...)` |
| cmd/agentnet/identity_e2e_test.go:92 | `d := exec.Command(dmn)` |
| cmd/relay/kill_test.go:47 | `out, err := exec.Command("go", "build", "-o", relayHelperPath, ".").CombinedOutput()` |
| cmd/relay/kill_test.go:146 | `cmd = exec.Command(bin, "--listen", "127.0.0.1:0", "--db", db)` |
| tools/binversion/main_test.go:22 | `cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, "../../cmd/relay")` |
| tools/releasesign/main_test.go:144 | `v, err := exec.Command("openssl", "version").Output()` |
| tools/releasesign/main_test.go:164 | `out, err := exec.Command("openssl", "pkeyutl", "-verify", "-pubin", "-inkey", pub, "-rawin", "-in", sums, "-sigfile", sums+".sig").CombinedOutput()` |
| tools/releasesign/main_test.go:171 | `if out, err := exec.Command("openssl", "pkeyutl", "-sign", "-inkey", key, "-rawin", "-in", sums, "-out", osig).CombinedOutput()` |

### 3.f File Open/Read/Write Operations

| File:Line | Operation |
|-----------|-----------|
| internal/device/testdata/runnerhelper/main.go:67 | `f, err := os.OpenFile(os.Args[2], os.O_CREATE\|os.O_APPEND\|os.O_WRONLY, 0o600)` |
| internal/identity/identity.go:181 | `raw, err := os.ReadFile(path)` |
| internal/ipc/transport_unix.go:24 | `if err := os.Remove(endpoint); err != nil && !errors.Is(err, os.ErrNotExist)` |
| internal/keystore/file.go:33 | `raw, err := os.ReadFile(f.Path)` |
| internal/keystore/file.go:63 | `_ = os.Remove(name)` |
| internal/keystore/file.go:79 | `return os.Rename(name, path)` |
| internal/keystore/file.go:84 | `if err := os.Remove(f.Path); err != nil && !errors.Is(err, fs.ErrNotExist)` |
| internal/logfile/logfile.go:40 | `f, err := os.OpenFile(w.path, os.O_WRONLY\|os.O_APPEND\|os.O_CREATE, 0o600)` |
| internal/logfile/logfile.go:76 | `if err := os.Remove(w.path + ".1"); err != nil && !os.IsNotExist(err)` |
| internal/logfile/logfile.go:79 | `if err := os.Rename(w.path, w.path+".1"); err != nil` |
| internal/mailbox/mailbox.go:464 | `raw, err := os.ReadFile(path)` |
| internal/mailbox/mailbox.go:486 | `return os.Remove(path)` |
| internal/mailbox/mailbox.go:493 | `return os.Remove(path)` |
| internal/mailbox/mailbox.go:499 | `return os.Remove(path)` |
| internal/mailbox/mailbox.go:510 | `return os.Remove(path)` |
| internal/relay/backup.go:27 | `if err := os.Remove(outPath); err != nil && !os.IsNotExist(err)` |
| internal/relay/backup.go:66 | `if err := os.Remove(dbPath + suffix); err != nil && !os.IsNotExist(err)` |
| internal/relay/backup.go:91 | `src, err := os.Open(from)` |
| internal/relay/backup.go:96 | `dst, err := os.OpenFile(to, os.O_WRONLY\|os.O_CREATE\|os.O_TRUNC, 0o600)` |
| internal/relay/journal.go:89 | `f, err := os.Open(journalPath)` |
| internal/service/service.go:158 | `err := os.Remove(s.Path)` |
| internal/service/service.go:183 | `if err := os.WriteFile(s.Path, data, s.Mode); err != nil` |
| internal/testutil/tempdir.go:34 | `err := os.RemoveAll(dir)` |
| internal/testutil/tempdir.go:39 | `err = os.RemoveAll(dir)` |
| cmd/agentnet/consult.go:185 | `f, err := os.Open(path)` |
| cmd/agentnet/debate.go:41 | `f, err := os.Open(path)` |
| cmd/agentnet/decision.go:362 | `f, err := os.Open(name)` |
| cmd/agentnet/device_scope.go:263 | `raw, err = os.ReadFile(*fromFile)` |
| cmd/agentnet/doctor.go:423 | `pem, err := os.ReadFile(filepath.Join(dir, relayCAFileName))` |
| cmd/agentnet/fetch.go:257 | `err = os.Rename(tmp, name)` |
| cmd/agentnet/fetch.go:260 | `_ = os.Remove(tmp)` |
| cmd/agentnet/inbox.go:393 | `f, err := os.Open(path)` |
| cmd/agentnet/request.go:211 | `f, err := os.Open(path)` |
| cmd/agentnetd/install.go:148 | `if err := os.WriteFile(caPath, pemCA, 0o600); err != nil` |
| cmd/agentnetd/relayurl.go:43 | `pemCA, err := os.ReadFile(path)` |
| cmd/relay/admin.go:85 | `f, err := os.OpenFile(*journalPath, os.O_APPEND\|os.O_CREATE\|os.O_WRONLY, 0o600)` |
| cmd/relay/main.go:176 | `journalFile, err = os.OpenFile(*securityJournal, os.O_APPEND\|os.O_CREATE\|os.O_WRONLY, 0o600)` |
| tools/binversion/main.go:78 | `f, err := os.Open(path)` |
| tools/releasesign/main.go:120 | `raw, err := os.ReadFile(path)` |
| tools/releasesign/main.go:187 | `f, err := os.OpenFile(*out, os.O_WRONLY\|os.O_CREATE\|os.O_EXCL, 0o600)` |
| tools/releasesign/main.go:317 | `sums, err := os.ReadFile(fs.Arg(0))` |
| tools/releasesign/main.go:341 | `sums, err := os.ReadFile(sumsPath)` |
| tools/releasesign/main.go:370 | `f, err := os.OpenFile(path, flags, 0o644)` |
| tools/releasesign/main.go:430 | `raw, err := os.ReadFile(*in)` |
| tools/releasesign/main.go:441 | `if err := os.WriteFile(*out, []byte(s), 0o755); err != nil` |
| tools/releasesign/main.go:460 | `raw, err := os.ReadFile(*pubFile)` |
| tools/releasesign/main.go:468 | `raw, err := os.ReadFile(*script)` |
| tools/releasesign/main.go:481 | `sums, err := os.ReadFile(sumsPath)` |
| tools/releasesign/main.go:485 | `sig, err := os.ReadFile(sumsPath + ".sig")` |
| tools/releasesign/main.go:492 | `ms, err := os.ReadFile(sumsPath + ".minisig")` |

### 3.g SQL Schema Migrations

**Daemon Migrations** (internal/store/store.go, line 20-531)

| Version | Name | First Table/Alter | File:Line |
|---------|------|-------------------|-----------|
| 1 | audit_events | CREATE TABLE audit_events | store.go:21 |
| 2 | peers | CREATE TABLE peers | store.go:35 |
| 3 | peers_trust | ALTER TABLE peers ADD COLUMN trust | store.go:45 |
| 4 | pair_used_codes | CREATE TABLE pair_used_codes | store.go:51 |
| 5 | mail_seen_inbox | CREATE TABLE mail_seen | store.go:57 |
| 6 | mailbox_keys_own | CREATE TABLE mailbox_keys_own | store.go:75 |
| 7 | outbox | CREATE TABLE outbox | store.go:86 |
| 8 | peers_trust_team | CREATE TABLE peers_new | store.go:106 |
| 9 | teams | CREATE TABLE teams | store.go:124 |
| 10 | presence | CREATE TABLE presence_peers | store.go:158 |
| 11 | requests | CREATE TABLE requests | store.go:178 |
| 12 | requests_result | ALTER TABLE requests ADD COLUMN result | store.go:225 |
| 13 | webhook_queue | CREATE TABLE webhook_queue | store.go:229 |
| 14 | work_sessions | CREATE TABLE work_sessions | store.go:244 |
| 15 | approvals | CREATE TABLE approvals | store.go:272 |
| 16 | grants | CREATE TABLE grants | store.go:304 |
| 17 | device_links | CREATE TABLE device_links | store.go:334 |
| 18 | audit_chain | ALTER TABLE audit_events ADD COLUMN hash | store.go:371 |
| 19 | debates | CREATE TABLE requests_new | store.go:385 |
| 20 | decisions | CREATE TABLE decisions | store.go:503 |
| 21 | experience_records | CREATE TABLE experience_records | store.go:522 |

**Relay Migrations** (internal/relay/queue.go:113-129)

| Version | Name | Tables | File:Line |
|---------|------|--------|-----------|
| R1 | R1_queue_baseline | CREATE TABLE queue + indexes queue_dedupe, queue_by_recipient, queue_by_age, queue_by_sender | queue.go:114 |
| R2 | R2_accounts | CREATE TABLE accounts, account_keys, bind_requests, web_sessions, quota_groups, quota_group_members | queue.go:128 |

## 4. Prior Reviews Coverage

| Review | Title | Ticket/Area | Counts |
|--------|-------|-------------|--------|
| 1 | AgentNet Plan Review: Per-Phase Checklist | Planning | - |
| 2 | Daemon/CLI/Service Code Review – Phase 0 | Daemon/CLI/Service | - |
| 3 | Crypto & Session Security Summary | Crypto/Sessions | - |
| 4 | Relay code summary and build verification | Relay Build | - |
| 5 | Expert review: Phase 0 as built vs plan | Phase 0 | - |
| 6 | Pairing trust model and async session design: options | Pairing Design | - |
| 7 | Adversarial review: pairing v2 and mail specs | Specs 0.8/1.0 | - |
| 8 | Wave 2 security reviews | Security | - |
| 8b | Security review: pairing v2, relay side | Relay 0.8d | - |
| 9 | Security review: pairing v2, daemon side | Daemon 0.8c | - |
| 10 | Security and correctness review: sealed-mail delivery | Mail 1.0 | - |
| 11 | Phase 1 tickets (1.1–1.9) | Phase 1 Planning | - |
| 12 | Phase 1 spec review (adversarial) | Specs | - |
| 13 | 1.4b mail.ErrBadBody receiver path | 1.4b | - |
| 14 | ticket 1.1a (peers trust team, migration 8) | 1.1a | - |
| 15 | 1.2a relay ephemeral envelopes | 1.2a | - |
| 16 | ticket 1.1b (team store and kinds, migration 9) | 1.1b | - |
| 17 | ticket 1.2b (presence, migration 10) | 1.2b | - |
| 18 | ticket 1.1d (team invite and join) | 1.1d | - |
| 19 | 1.4c (request kind, request_submit, CLI) | 1.4c | - |
| 20 | 1.6a (lifecycle, cancel, result payload) | 1.6a | - |
| 21 | 1.8a (desktop notifications) | 1.8a | - |
| 22 | 1.8b (webhook: secret, signing, queue) | 1.8b | - |
| 23 | Phase 2 tickets (2.1–2.7, D13) | Phase 2 Planning | M=1 L=1 |
| 24 | Phase 2 spec review (adversarial) | Specs | - |
| 25 | 2.2b (capability tokens) | 2.2b | - |
| 26 | 2.2a (human approval) | 2.2a | - |
| 27 | 2.1a (work sessions core) | 2.1a | - |
| 28 | 2.2c (grant issuance) | 2.2c | - |
| 29 | 2.2d approval-window spec review | Specs | - |
| 30 | 2.2d (approval window) | 2.2d | - |
| 31 | CI -race failures after 2.2d | Race Fix | - |
| 32 | 2.5 security review (consult) | 2.5 | - |
| 33 | Flake: TestPairingTagCompletedOnSuccess | INV-1 | - |
| 34 | 2.3a security review (fetch server, fs serving) | 2.3a | - |
| 35 | 2.4 security review (quarantine, D18) | 2.4 | - |
| 36 | 2.D1 security review (own-device link) | 2.D1 | - |
| 37 | 2.3b security review (git serving) | 2.3b | - |
| 38 | 2.3c security review (fetch client, CLI) | 2.3c | - |
| 39 | CI red: symlink stat and in-flight limit | Race Fix | - |
| 40 | 2.D2 security review (helper scope, runner) | 2.D2 | - |
| 41 | 2.D3 security review (kill-on-revoke, ownership) | 2.D3 | - |
| 42 | Phase 3 tickets (3.1–3.7: debate, decision, audit, experience) | Phase 3 Planning | M=1 L=1 |
| 43 | Phase 3 spec review (adversarial) | Specs | - |
| 44 | 3.6a security review (hash-chained audit log) | 3.6a | - |
| 45 | 3.2+3.1a security review (debate, debate core) | 3.1a/3.2 | - |
| 46 | 3.4 security review (human constraints) | 3.4 | - |
| 47 | 3.3a security review (signed Decision) | 3.3a | - |
| 48 | 3.3b security review (Decision output, Markdown export) | 3.3b | - |
| 49 | Phase 4 tickets (4.0 beta gate, 4.1–4.9) | Phase 4 Planning | M=1 L=1 |
| 50 | Phase 4 spec review (adversarial) | Specs | - |
| 51 | 4.0a security review (relay TLS, auth v2, URL rule) | 4.0a | - |
| 52 | 4.0 combined security review (4.0a, 4.0b, 4.0c) | 4.0 | - |
| 53 | 4.4a security review (release pipeline, install.sh) | 4.4a | - |

## 5. Open Findings

Open findings: compiled by the review planner (CR-1), not by the indexer.

## 6. Specs

### Protocol Specifications (Docs/protocol/)

| File | Title |
|------|-------|
| README.md | Protocol Specifications |
| accounts.md | Account Management and Access Control |
| agent-card.md | Agent Card: Identity and Signing |
| approval.md | Human Approval: Desktop and Terminal |
| audit.md | Audit Log: Append-Only Chain |
| consult.md | Consult: Ask-with-Context Protocol |
| debate.md | Debate: Multi-Turn Question/Answer |
| decision.md | Decision: Signed Outcome |
| device.md | Device Linking: Own-Device Helper |
| envelope.md | Envelope: Relay Wire Format |
| experience.md | Experience: Private Record |
| feedback.md | Feedback: Agent Response to Question |
| grant.md | Grant: Scoped Read Access |
| invites.md | Team Invites: Private Pairing v2 |
| ipc.md | IPC: Local Daemon API |
| mail.md | Mail: Sealed Messages Between Daemons |
| notify.md | Notifications: Desktop and Webhook |
| pairing.md | Pairing v2: Trust Exchange |
| presence.md | Presence: Online Status and Modes |
| relay-hosted.md | Relay: Hosted Deployment |
| request.md | Request: Async Work Item |
| session.md | Session: Encrypted Peer Connection |
| team.md | Team: Multi-Daemon Collaboration |
| telemetry.md | Telemetry: Privacy-Preserving Metrics |
| work-session.md | Work Session: Interactive Interaction |

### CLI Documentation (Docs/cli/)

| File | Title |
|------|-------|
| agentnetd-install.md | agentnetd-install: Service Installation |
| agentnetd.md | agentnetd: Local Daemon |
| approve.md | approve: Confirm/Reject Approval |
| consult.md | consult: Ask Teammate's Agent |
| debate.md | debate: Multi-Turn Argument |
| decision.md | decision: Show/Verify/Export |
| device.md | device: Link Own Devices |
| doctor.md | doctor: Health Check |
| fetch.md | fetch: Read Under Grant |
| grant.md | grant: Give Scoped Access |
| identity.md | identity: Show Agent Card |
| inbox.md | inbox: List Requests |
| install.md | install: Deploy AgentNet |
| log.md | log: Audit Log and Chain |
| mail.md | mail: Debug Mail Queue |
| notify.md | notify: Desktop Notifications |
| pair.md | pair: Pair with Another Machine |
| peers.md | peers: List Paired Agents |
| ping.md | ping: Test Connection |
| presence.md | presence: Online Status |
| relay.md | relay: Relay Deployment |
| request.md | request: Send Work Item |
| session.md | session: Interactive Work |
| status.md | status: Daemon Status |
| stop.md | stop: Shut Down Daemon |
| team.md | team: Multi-Agent Collaboration |

---
Generated by: CR-Index-Haiku  
Model: claude-haiku-4-5-20251001  
Timestamp: 2026-09-29
