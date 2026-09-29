# 55 / 01: Reviewer brief (give this file to every reviewer verbatim)

You are one reviewer in a full code review of the Dorylinae / AgentNet repository. The
**whole review targets commit `6cc26a7`** (code freeze). Many reviewers work in parallel, each
with a fresh context, on one small chunk or one cross-cutting theme. This brief is what keeps
the reviewers consistent. Follow it exactly. When it conflicts with your habits, it wins.

Your assignment names one of these:
- a **chunk** `Cnn`: its section in [03-chunks.md](03-chunks.md) lists the exact files, the
  specs, the prior reviews and the questions you must answer;
- a **theme** `Tn`: its section in [04-themes.md](04-themes.md);
- a **verification** of one finding: see [05-process.md](05-process.md) §4.

Read, in this order: this file, [02-open-findings.md](02-open-findings.md), your own section
in 03 or 04, the governing specs, the prior reviews it lists, and then the code. Tests in the
same package are context: read them to learn intent and what is already covered, but do not
review test code unless your section says so.

## 1. What you are protecting (the threat model)

Derived from `Docs/protocol/*` ("Threat model" and "Security considerations" sections) and
decisions D2, D5, D13, D17, D19, D24 and D41 in `Docs/orchestration/HANDOFF.md` §3. Use it to
judge severity.

| Actor | Trust | Must never be able to |
|---|---|---|
| **The relay** (any relay, hosted, self-hosted or ours: D17) | **Untrusted.** It sees routing metadata and ciphertext. It can read, drop, delay, reorder, replay and inject frames, and create unlimited keys | Read content; forge mail, presence, grants or Decisions; complete a pairing MITM; make a daemon store the wrong key; crash a daemon or grow its memory/disk without bound; make a daemon act on a stale or replayed message outside the spec's windows; send the user's browser or OS opener to a URL it chose (review 50 H3) |
| **Anyone on the internet** (vs the hosted relay) | None. Unlimited keys and connections | Exhaust relay memory, disk, file descriptors or the single SQLite connection; fill a victim's queue; lock out pairing or login for others; bypass accounts when `--accounts` is on; reach test hooks or admin functions |
| **A paired peer** (trust `code`/`fingerprint`, or `team` introduced, or `relay` on loopback) | **Semi-trusted.** Authenticated (its signature is real) but may be modified, buggy or hostile | Read files or repos outside a grant's scope, path, action and expiry; run a command that the helper's human did not put in a scope; bypass quarantine; trigger an approval, a grant, a device link or a scope change; forge another peer's identity; exceed urgency/rate/size limits; put content into our audit log or logs; drive our terminal, notifications or Markdown with control/bidi/markup; make us store unbounded data |
| **A local agent using AgentNet's interface** (CLI, IPC socket/pipe), possibly prompt-injected | **A boundary** (approval.md "Threat model"). It may call any IPC method | Complete any approval-gated action without the human (grant, grant policy, release, accept-result, device link, device scope, debate constraint); learn or submit an approval code; widen a scope or grant; read another peer's content it has no view of; make the daemon exec anything other than the configured argv |
| **A local agent with a raw shell / malware as the user** | Out of scope (approval.md "Boundary, stated plainly"; harness confinement) | — Not a finding **unless** the code breaks a promise the spec makes anyway (e.g. an approval code in an argv, a secret in a log file, a key file with wrong permissions) |
| **Another local OS user** | Untrusted | Read keys, the DB, the IPC endpoint, approval codes, or swap a program a scope runs (D24) |
| **The supply chain** (CI, release, install script, dependencies, GitHub Actions) | Partly trusted: GitHub infra is trusted; any single PR, action or asset swap is not | Get an unsigned or wrongly signed binary installed; get a rollback past `AGENTNET_MIN_VERSION`; ship test hooks or test-only deps (goldmark, D32) in release builds; exfiltrate the signing key (it is offline; CI never sees it) |

**Invariants that are always in scope** (breaking one is at least Medium, usually High):
1. **No content in the audit log, logs, webhooks, telemetry or error strings sent to peers**
   (audit.md "No content, still", notify.md "Privacy summary", telemetry.md, `TestPhase*AuditHasNoContent`).
   Content = message bodies, request/result text, file data, positions/arguments, constraint
   text, paths from a peer, secrets, codes, keys.
2. **Secrets never leave their place:** identity seed and mailbox private keys (keystore),
   the approval code and `approval_key` (memory only), the pairing secret and K, webhook
   secrets and URLs (bearer secrets), the release signing key (offline).
3. **Fail closed** on authentication, authorization, signature, scope, grant, approval and
   quarantine checks, including on DB/read errors.
4. **Peer input is parsed strictly and bounded** before it is trusted, stored or displayed.
5. **Durability promises hold:** mail exactly-once on the receiver, outbox until app ack,
   audit append-only and hash-chained, migrations atomic.

## 2. Severity scale

Pick the **lowest** level whose description fully fits. Severity is about impact times
reachability under the threat model above, not about how much code a fix needs.

| Level | Criteria (any one is enough) | Examples in this project |
|---|---|---|
| **Critical** | Reachable by the relay, an internet stranger or a paired peer **without any user action beyond normal use**, and it gives: code execution on a user machine; reading content not granted; forging an identity, signature, Decision or pairing (MITM); bypassing an approval gate remotely; disclosing a key, seed, approval code or pairing secret; or installing an unsigned/wrong binary through `install.sh`/release | A crafted mail body runs a command on the helper; a grant path escape serves `~/.ssh`; the relay can make `checkConfirm` store its own key; `install.sh` accepts an unsigned `SHA256SUMS` |
| **High** | A core security property breaks, but the attacker needs a narrower position (a local agent via IPC, a paired peer with a specific grant, a relay with timing luck) or the impact is bounded; **or** any content/secret reaches audit, logs, webhooks or telemetry; **or** a cheap unauthenticated attacker can take the hosted relay down (OOM, disk, fd, lock) for everyone; **or** silent loss/corruption of durable state (mail acked but lost, audit chain broken without detection, migration half-applied); **or** an authz check fails open | A local agent completes a device scope change without the window; a peer's `ws.result` text shows while quarantined; one key pins the relay's single DB connection; a DB error makes D5 skip the `unverified_peer` decline |
| **Medium** | Bounded DoS that needs a paired peer or many resources; metadata/privacy leak beyond what the spec lists; a spec deviation with security or data impact but no direct exploit; wrong state that the user sees and must repair by hand; resource leak that grows with peer or relay input; a race that gives wrong results under realistic load; fail-open on a rare error that does not touch authz | A paired peer grows `mail_inbox` without bound; `relay_ca.pem` stays trusted for a new relay; a shutdown race loses an outbox wake-up for 6 h |
| **Low** | Defence in depth; hardening; an edge case that needs unlikely conditions or only harms the attacker; display fidelity of attacker-controlled text that cannot hide or spoof anything important; an error message that misleads; a non-security correctness bug with an easy workaround | A C1 control char in a peer's own title in `agentnet inbox`; a limiter map that grows until the next sweep |
| **Info** | Not a defect: missing test for a real risk, doc drift with no behaviour change, a design note for the owner, a suspicion you could not substantiate | "No test covers the 1 MiB IPC line limit for `decision_show`" |

Adjustments:
- **Hosted relay vs loopback:** judge relay findings for the hosted relay (`relay.dorylinae.net`,
  Hetzner, review-52 sizing: `--max-conns 2000 --max-inflight 48MiB`, `GOMEMLIMIT=400MiB`,
  a 2–3 GB volume) unless the code path is loopback-only.
- **Off-by-default features** (accounts `--accounts`, pairing v1, `DORYLINAE_APPROVAL=terminal`,
  test hooks under the `testhooks` build tag): rate the finding as if the feature were on,
  and say in the finding that it is off by default. The consolidator may lower it by one level.
- **Windows is the owner's platform.** A Windows-only defect is not less severe.
- A finding that needs the owner's own mistake (a hostile `DORYLINAE_HOME`, a world-writable
  config dir they made) is at most Low.
- Do not raise a severity because a fix is easy, or lower it because a fix is hard.

## 3. What is a finding (and what is not)

A **finding** is a concrete way the code does the wrong thing, or can be made to, with a
consequence for security, privacy, data integrity, availability or correctness that a user or
operator would notice. It includes: a spec contradiction (the spec is authoritative unless you
show the spec itself is unsafe, then it is a spec finding); a missing check the spec requires;
a race or leak with a trigger you can describe; a test that claims to cover an invariant but
cannot fail.

**Not a finding** (do not report; at most one line under "Notes"):
- style, naming, formatting, comment wording, CRLF, import order, "I would structure this
  differently", missing comments;
- refactoring suggestions, performance with no DoS angle, duplication;
- anything the threat model puts out of scope (malware as the user, an agent with a raw
  shell reading the DB, the relay dropping frames, a human reading the code to an attacker);
- a behaviour the spec or an owner decision (HANDOFF §3, D1–D42) chose on purpose, unless you
  show a concrete flaw the decision did not consider (then say "reopens Dnn" and why);
- a finding already open in [02-open-findings.md](02-open-findings.md) (see §6 rule 1).

If unsure whether something is a finding, report it as **Info** with confidence `suspected`.
Do not pad the report: one well-evidenced High beats ten speculative Lows.

## 4. Required format of each finding

Every finding uses exactly this block. Keep each field short; the scenario is the part that
matters.

```markdown
### C07-03 · High · confirmed-read
- **Where:** `internal/mail/outbox.go:412` (`Retry`), also `internal/daemon/mail.go:157`
- **What goes wrong:** one or two sentences, stating the defect as a fact.
- **Scenario:** concrete inputs or state → the wrong output, crash, leak or bypass. Name the
  actor from §1 (relay / stranger / paired peer / local agent / other OS user / supply chain)
  and every step they take. If you could not construct one, the confidence is `suspected`.
- **Spec:** `Docs/protocol/mail.md` §Outbox, "…short quote…" (or "none")
- **Fix direction:** one line, no patch.
- **Related:** O-042 (open finding it touches), C12-02 (lead), Dnn (decision) — or "none"
```

- **Id:** your chunk or theme id + a two-digit number in the order you write them
  (`C07-01`, `T3-04`). Never renumber after reporting a Critical.
- **Severity:** one of Critical, High, Medium, Low, Info (§2).
- **Confidence:** exactly one of
  - `confirmed-read`: you traced the code path end to end and the reasoning holds;
  - `confirmed-test`: you ran a test that shows it (name the file and test, give the command
    and the relevant output in two or three lines);
  - `suspected`: plausible, but you could not close the path (say what is missing).
- **Where:** `path:line` from the repository root, at commit `6cc26a7`. Several are fine;
  put the root cause first.

## 5. Required structure of your report

Write one Markdown file (path in [05-process.md](05-process.md) §6). Sections, in this order:

1. **Header:** `# Cnn: <title>` (or `Tn`), then a line
   `Reviewer: <your name> · model <exact model id> · elapsed <minutes> min · commit 6cc26a7`.
2. **Summary:** counts per severity and a one-paragraph verdict.
3. **Questions:** answer every question in your section of 03/04, numbered as there, each in
   a few lines with `file:line` evidence, pointing to finding ids where relevant.
4. **Findings:** most severe first, in the §4 format.
5. **Assumptions and contracts** (mandatory, even if short): what the code in your scope
   relies on from code **outside** it, one bullet each, with the place it relies on it, e.g.
   - "`mail.Open` assumes the caller already ran `envelope.Parse`/`Validate` (`internal/mail/open.go:40`); caller is `internal/daemon/mail.go:150`."
   - "`fs.Read` assumes the grant's path was validated by `capability.Verify` step 6."
   Mark each **checked** (you read the other side and it holds, cite it) or **unchecked**.
   The theme reviewers work from these lists, so be specific.
6. **Checked and fine:** what you verified and found correct, one bullet each with the
   place, e.g. "Every `json.Unmarshal` of peer input goes through `ParseStrict` (…)",
   "O-117 appears fixed: `…:88` now checks `RowsAffected`". This makes coverage visible;
   a report with no such section is incomplete.
7. **Leads for other chunks:** things you noticed outside your file list, one line each with
   `file:line` and why. Do not investigate them yourself beyond a quick look.
8. **Notes (optional):** style or design remarks that are not findings, at most five lines.
9. **Commands run:** every `go test`/`go vet`/other command, with its result in one line.

## 6. Rules

1. **Open findings.** Do not re-report anything listed in
   [02-open-findings.md](02-open-findings.md). If your code touches one, mention its id in
   "Related" or "Checked and fine". Report it again as a new finding **only** if you have
   new evidence that it is worse than recorded (a new actor, a higher impact or a new code
   path); then write "escalates O-nnn" and explain the difference. If an open finding is
   actually fixed, say so under "Checked and fine".
2. **Read-only repository.** No git commands except read-only ones (`git log`, `git show`,
   `git blame`, `git diff`, `git grep`). Never checkout, stash, reset, commit or push.
3. **No fixes.** Do not change production code, specs or docs. You may add **temporary test
   files only**, named `zz_review55_<finding-id>_test.go` in the package under test, to
   confirm a finding. Keep them, and list them under "Commands run", so a verifier can
   rerun them. They are never merged.
4. **Targeted tests only.** Run `go test ./internal/<pkg> -run '<TestName>' -count=1` (or
   `go vet ./internal/<pkg>`); never `go test ./...`, the full suite, or long fuzzing
   (a fuzz run of at most 60 s on one target is fine). Other reviewers share this machine.
   Environment notes: Go is at `C:\Program Files\Go\bin`; there is no cgo, so `-race` cannot
   run locally; `internal/daemon` and `internal/device` tests that need a private directory
   fail on this PC's `C:` ACL (see HANDOFF §0 for the `subst` workaround), so prefer reading
   over running there; symlink tests are skipped on this Windows machine.
5. **Critical = stop and report at once.** If you find (or strongly suspect) a Critical,
   stop, send the Orchestrator (slot `01a0d959-dcbb-7e93-8535-9f1283d5aea1`) a
   `team_send_message` with the id, `file:line`, the actor and a short scenario, then
   continue your review. Do not write exploit code beyond a minimal failing test. Do not
   post anything outside the team (no issues, no PR comments, no web).
6. **Stay in scope.** Review only your file list (chunk) or your theme's question across the
   codebase (theme). Anything else goes to "Leads".
7. **Spec first.** Where the code and a spec disagree, cite both. The spec wins unless it is
   itself unsafe, then say so (that is a spec finding).
8. **Never hand-edit generated files** (vectors, golden files) and never change
   `Docs/protocol/*`.
9. **Evidence over volume.** No finding without a `file:line`. No `confirmed-*` without a
   traced path or a run test. It is fine, and useful, to report zero findings with a
   thorough "Checked and fine" section.
10. **Report** to the Orchestrator when done: model id, elapsed minutes, counts per
    severity, the path of your report file, and any Critical ids. At most 150 words.
