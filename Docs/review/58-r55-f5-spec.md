# 58: R55-F5 spec, one approval-summary builder

Author: SPEC-F5 (Opus, `claude-opus-5-5`), 2026-09-29. Worktree `AgentNet-wt/r55-f5spec`
(branch `p4/r55-f5spec`). Docs only, no code. Status: **draft**. The Opus adversarial review
comes next, then the owner approves the ODs.

Findings covered (review 55, `Docs/review/55-code-review/99-report.md`):
- **R55-005** (High): C11-01, C11-06, C12-02, T8-01, T8-02
- **R55-007** (High): C14-02
- **R55-066** (Low): T11-02
- **R55-122** (Low): T6b-02
- **R55-148** (Info): T6b-11

Owner decision applied: **D44, which sets review-55 §6 D9**. The device-link window shows the
peer's fingerprint next to its name, and the human compares it (nothing is typed).

## Owner summary

**The problem.** The approval window is the one place a local agent cannot write to, so what
it says is what you approve. Today it often does not say enough:
- **Grants** show a random label (`app-3f2a`), not the folder. They do not show whether the
  grant covers the whole repository or one folder. They do not show `--public`, which turns off
  the result quarantine.
- **Policies** leave out the branch, the scope, `public` and how long the policy lasts (up to
  90 days).
- **Device link, scope, release and accept-result** name the other side only by its
  self-chosen name, or by an opaque session id. The window never shows a key fingerprint for
  any kind.
- **Scopes** can be up to 16 KB of text, but the window shows only the first 4096 characters.
  A command placed after a long argument is approved without anyone seeing it.
- **Decoy codes** can get past the filter: a peer named `Code 4​8​2​9​1​3` (with hidden
  zero-width characters) shows the digits.

A prompt-injected agent can use any of these to get something approved that you did not mean:
- a whole-repository or public grant;
- a link to a look-alike device that can then run commands here;
- the release of the wrong quarantined result.

**The fix (spec in `Docs/protocol/approval.md` §Approval summaries):**
1. **One builder** writes every approval text, from the object that will actually be carried
   out, never from what the agent passed in.
2. **Every kind states everything that decides what you approve.** That includes the full
   path, branch, scope, **PUBLIC** in capitals with "results are NOT quarantined", expiry, the
   session and its request, and the peer's name **with its full 20-character fingerprint**.
   For a device link you compare that fingerprint with `agentnet identity` on the other
   device (D9).
3. **Too long is refused, never cut.** An approval text over 4096 characters is refused when
   the agent asks, before anything is stored. The window no longer shortens or rewrites the
   text.
4. **One cleaning rule.** Names lose control, direction-changing and invisible characters,
   and long digit runs (decoy codes) become "…". Paths, commands and titles are shown exactly,
   in quotes, with invisible characters written out as `\uXXXX`.
5. **The confirm checks the text.** When you type the code, the daemon rebuilds the text from
   the stored object. If anything changed since the window opened, the approval is rejected
   and you start again.

Also included:
- the session view lists the session's grants (`agentnet session` showed `[]`);
- in terminal mode, a typo that is not 6 digits no longer uses up an attempt.

**What it costs you.**
- A scope with one very long argument (thousands of characters) can no longer be set. Use a
  wrapper script on the helper instead.
- An approval is rejected if the peer renames itself in the 10 minutes while it waits.
- Nothing changes for the CLI or for agents: same commands, same flags.

**Order.** F5's code comes **after F7** (both edit `internal/daemon/grant.go`) and **before
F6** (Linux zenity escaping). F6 relies on the builder's output rules, stated in approval.md
§Display-safe output. Until F6 lands, the Linux window can still misshow `\` and `_`
(R55-025).

## Open decisions (full text: approval.md §Open decisions (R55-F5))

| OD | Question | Recommendation |
|---|---|---|
| OD-R55F5-1 | How much of the fingerprint the window shows | (a) All 20 characters, grouped as `agentnet identity` prints them. A short prefix can be forged by generating keys |
| OD-R55F5-2 | Keep `device_link --fingerprint` now that the human compares in the window | (a) Keep it, as a typo check only. No CLI change |
| OD-R55F5-3 | Time format | (a) UTC to the minute plus a duration, so the text cannot change while waiting |
| OD-R55F5-4 | Peer names cut at 40 characters? | (a) No cut; the card already caps names at 128 |
| OD-R55F5-5 | The peer renames itself while the approval waits | (a) Reject; the human starts again |
| OD-R55F5-6 | Digits in paths, argv and titles (terminal-mode decoy) | (a) Keep the digits (exactness first); the residual is only wasted attempts in terminal mode |
| OD-R55F5-7 | What the release text says about grants | (a) The count of sensitive grants; the list is in `agentnet session` |
| OD-R55F5-8 | Accept-result text shows the result's summary? | (a) No, status and sizes only, the same rule as release |

## Files changed (docs only)

- `Docs/protocol/approval.md`:
  - new section §Approval summaries (R55-F5): builder, Precondition comparison, length,
    sanitising, display-safe output, contents per kind;
  - threat model rows;
  - window rule "shown as built";
  - Flow steps 1 and 3;
  - terminal-mode non-6-digit rule (R55-148);
  - §Open decisions.
- `Docs/protocol/device.md`:
  - §Link flow, with D9: the human compares the fingerprint in the window, and `--fingerprint`
    is a typo check only;
  - §Scope: the limit is now 4096 code points, not 16384 bytes.
- `Docs/protocol/grant.md`: §Issuance step 7 (grant summary and re-check), §Policies (policy
  summary), §Sensitive grants (the opt-out is visible).
- `Docs/protocol/work-session.md`:
  - §Quarantine: the release and accept-result summaries;
  - §IPC: the session view's `grants` is filled (R55-122).
- `Docs/protocol/debate.md`: §Human constraints (the fingerprint, the builder).
- `Docs/review/58-r55-f5-spec.md`: this file.

## Code sites for the implementer (Opus, after F7)

- **New:**
  - the builder package, with one facts type per kind;
  - the shared `hidden(r)` predicate, used by `decision.Visible` (whose output must not change;
    `go run ./tools/verifyvectors` stays green) and by `device.DisplayQuote` (widened to the
    full set);
  - `displayName`, which absorbs `stripLongDigits` from `internal/daemon/device.go:103-125`;
  - `displayQuote`.
- **Summaries to replace:**
  - `internal/daemon/grant.go:616` (grant) and `:960` (policy);
  - `internal/daemon/device.go:485-488` (link);
  - `internal/daemon/device_scope.go:149-162,208-212` (scope; delete `maxScopeSummary` at `:31`);
  - `internal/daemon/session.go:396` (accept_result) and `:533` (release);
  - `internal/daemon/debate_constrain.go:57-97` (`constraintSummary`, `peerDisplayName`, and the
    existing R46 H2 length check, which moves into the builder).
- **Preconditions of all seven kinds:** add the facts and text comparison (approval.md §One
  builder).
- `internal/notify/window.go:36-43` (`windowText`): no `Clean` and no cut; check that the text
  is display-safe and within the limit, and fail when it is not.
- `internal/approval/store.go` `Create`: the backstop check.
- `internal/daemon/approval_terminal.go:73-85`: `isSixDigits` before `Confirm`.
- `internal/daemon/session.go:178`: fill `Grants`.

## Acceptance tests

Review 55's reviewer tests are kept as `.txt` in `Docs/review/55-code-review/tests/`, each with
a `.path` file giving its original location. Each one currently demonstrates the defect. Invert
it and make it a permanent, normally named test.

**Naming caveat.** Two of the `.txt` files are named after the wrong finding (see
verify/T11-01.md: "the reviewer's version was overwritten"):
- `zz_review55_T11-01_reviewer_test.go.txt` holds the **decoy-digit** test, which is
  **T11-02 / R55-066** and belongs to F5;
- `zz_review55_T11-02_test.go.txt` holds the **zenity `g_strcompress`** test, which is
  **T11-01 / R55-025** and belongs to F6, not F5.

| # | Test | From | Asserts |
|---|---|---|---|
| A1 | Scope too long is refused | `tests/zz_review55_C14-02_test.go.txt` (inverted) | The C14-02 scope (a 4000-character argument, then a last command with `HIDDEN-PAYLOAD`) is refused by `device_scope_set` with `bad_scope`, field `scope`. No approval row and no audit row exist. Boundary: a summary of exactly 4096 code points is accepted and 4097 is refused |
| A2 | The window shows the text as built | new | `windowText` of a 4096-code-point summary that holds double spaces inside a quoted argv returns it unchanged, followed by the fixed sentence. A 4097-code-point or non-display-safe summary makes `Start` fail (not ready → `approval_unavailable`) |
| A3 | The store backstop | new | `Store.Create` with a 4097-code-point summary, or one that contains U+200B or `\n`, returns an error. Nothing is stored or audited, and no window or notification is started |
| A4 | Decoy digits (R55-066) | `tests/zz_review55_T11-01_reviewer_test.go.txt` (it only logs today; add assertions) | For each of its six names (ZWSP, word joiner, VS16, Hangul filler, octal `\064…`, plain), `displayName` holds `…` and no run of ≥ 6 numbers. New cases: U+0332 combining marks between digits, superscript `⁴⁸²⁹¹³`, circled digits, mathematical bold `𝟒𝟖𝟐𝟗𝟏𝟑`, fullwidth, `48 29 13`, and U+202E inside the name. Negative cases: `laptop 2024` and `v12345` stay unchanged |
| A5 | The grant summary (C11-01) | new (the verify report proposed it) | `grant_create` `git.read` with `public: true` and a scope gives a summary with `displayQuote(resolved path)`, the label, the quoted branch, `only "<scope>"`, `PUBLIC`, `NOT quarantined`, the duration, the UTC expiry, the session id, the quoted request title and `FormatFingerprint(fp(peer))`. `fs.read` with no scope says `the whole folder` and `Sensitive`; `git.read` with no scope says `the whole repository` |
| A6 | The policy summary (C11-01) | new | `grant_policy_add` gives a summary with the branch, the scope, `PUBLIC` or `sensitive grants only`, the maximum expiry, `until` (UTC and duration) and the fingerprint |
| A7 | Raw names (C11-06, C12-02) | new | A peer named `Bob‮ code 482913`: the grant and policy summaries, the notification body (`deliveryText`) and the terminal-mode line hold no U+202E and no `482913` |
| A8 | Device kinds (T8-01) | new | The `device_link` summary holds the role, the fingerprint and `agentnet identity`. Two peers with the same name give summaries that differ in the fingerprint. The `device_scope` summary holds the controller's fingerprint |
| A9 | Release and accept-result (T8-02) | new | The summaries hold the name, the fingerprint, the type, the quoted title, the session id, the round, the status, the sizes and (release only) K or the reason for rule 2. A marker string in the quarantined result's summary, output and notes appears **nowhere** in `approvals.summary` or in `approval_list` |
| A10 | Precondition comparison | new | Per kind (grant, policy, link, scope, constraint, release, accept_result): change the peer's name between Create and Confirm, and the approval is rejected with reason `precondition` and the action is not performed. For grant: an `UPDATE approvals SET summary = …` before Confirm also rejects it |
| A11 | Builder purity and safety | new (fuzz) | `FuzzBuild<Kind>` with any strings in the peer and agent fields: the output is either `ErrTooLong` or display-safe and ≤ 4096 code points. Two calls give the same bytes. The output is unchanged when `TZ` or `time.Local` differs |
| A12 | Session view grants (R55-122) | new | `ws_show` on a session with a sensitive `fs.read` grant lists `{id, action, sensitive: true, state, exp, label}`. `ws_list` omits the label. The local path never appears |
| A13 | Terminal typo (R55-148) | new | The terminal lines `a-012345 48291` and `a-012345 abcdef` leave `attempts_left` unchanged and write no `approval.bad_code` row. The reply is "enter the 6-digit code" |
| A14 | No regressions | existing | The R46 H2 debate length test, the review 40 M1 DisplayQuote tests and the decision vectors (`verifyvectors`) stay green. `tests/phase2-harness` and `phase3-harness` still parse the terminal line |
| M1 | Manual, Windows (owner) | `tests/phase2-manual.md`, the long-summary check still owed | A scope summary near 4096 code points scrolls and shows the last command and "Confirm only if you set this scope yourself". A device-link window shows the other device's fingerprint, which matches `agentnet identity` there |

The gate is the normal merge gate (HANDOFF rule 5), with the Linux and darwin cross-vet (the
window code is per OS), then the Opus security review before the merge.
