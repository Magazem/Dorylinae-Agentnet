# 64: Security review of R55-F5 (commit d7cdaf7, PR #17)

- **Reviewer:** SEC-F5, model `claude-opus-5-5`, 2026-09-29. This was a read-only review. The only file written is this report.
- **Scope:** `git diff main...p4/r55-f5` (34 files). It was read against:
  - `Docs/review/58-r55-f5-spec.md`: review 58a, A1–A19 and M1, and OD-R55F5-1..10 (all approved as recommended, D45);
  - HANDOFF §3 D44 (D9: show the fingerprint, the human compares);
  - `Docs/protocol/approval.md` §Approval summaries;
  - `55-code-review/verify/{C11-01,C14-02,T8-01}.md`.

  Two attackers were considered: a prompt-injected local agent (IPC), and a hostile paired peer (it controls its card name and its key).
- **Evidence:**
  - These pass locally (Windows):
    - `go test -count=1 ./internal/{displaytext,approvaltext,approval,decision,notify}`;
    - `go run ./tools/verifyvectors` ("all vectors reproduced").
  - Some `./internal/device` and `./internal/daemon` tests fail locally, only the ones that set a scope with an `argv[0]` under `C:\`: `writable_by_others: "C:\" can be changed by Authenticated Users`. This is this machine's ACL (the F7 check), not F5. `test (windows-latest)` passes in CI.
  - `FuzzBuild` ran for 90 s (about 180 000 execs) on a copy of `internal/` in `%TEMP%`, with no failure.
  - A probe of `displaytext` (a copy, in `%TEMP%`) fed it the names and strings listed under the findings.
  - **CI, PR #17** (`gh pr checks 17`):
    - pass: `test` on ubuntu, macOS and windows, lint, install-sh, flag-sensitive-paths, and unix service install on macOS and ubuntu;
    - `govulncheck` was skipped;
    - **`race` was still pending** when this report was written.

## Verdict: **approve** (0 Critical, 0 High, 0 Medium, 2 Low, 3 Info)

The design holds against both attackers:
- every kind states its deciding facts, taken from the object that is performed;
- the fingerprint comes first, right after fixed text;
- the peer name is quoted and cannot close its quotes;
- a summary that is too long is refused, never cut;
- the confirm transaction rebuilds the text and compares it.

The two Lows are defence-in-depth gaps in `displayName`. Both match the spec as written, and the primary control (the fingerprint first) still holds.

**Conditions (already on record):**
- F6 ships in the same release as F5. Until then zenity mis-shows `\` and `_`, but cannot cut the window (A16 proves this).
- Merge only after `race` is green.

## Findings

### F5S-1 · Low · confirmed (probe): fingerprint-shaped text in a name survives with other separators or look-alike letters
- **Where:** `internal/displaytext/displaytext.go` `blankFingerprints` (step 5, OD-R55F5-9).
- **Evidence:** `Name` leaves these unchanged:
  - `Desktop 2ED9.TGVE.R471.63MC.C451`, and the same with `_`, `/`, `—` or `--` between the groups;
  - `2ЕD9 TGVЕ R471 63МС С451`, which uses Cyrillic Е, М and С;
  - fullwidth `２ＥＤ９ ＴＧＶＥ`.

  Only groups joined by a single ASCII space or `-`, in the ASCII alphabet, are blanked. The spec says exactly this, so the code conforms.
- **Impact:** a twin can still show the impersonated device's fingerprint, but only inside `named "…"`, after its own real fingerprint, which comes first after the fixed word `peer` (H1 fix).
  - A human who compares "the fingerprint" as instructed compares the right one.
  - Step 5 is a second line of defence. That makes this Low, not a reopening of H1.
- **Fix direction:**
  - Chain groups of 4 across any 1–3 separator runes (`P*`, `S*`, space), not just `' '`/`'-'`.
  - Fold confusables before matching: NFKC for fullwidth, plus a small Cyrillic/Greek to Latin skeleton for the fingerprint alphabet.
  - Add these names to `TestNameBlanksFingerprints`.

### F5S-2 · Low · confirmed (probe): decoy codes in peer names survive with 4+ separators or a mark that is not right after a digit
- **Where:** `displaytext.blankDigitRuns` (step 4).
- **Evidence:** `Name` leaves these unchanged:
  - `Code 4----8----2----9----1----3` and `Code 4 - - 8 - - 2 - - 9 - - 1 - - 3` (4 separator runes);
  - `Code 4 ̲ 8 ̲ 2 ̲ 9 ̲ 1 ̲ 3` (U+0332 on the separator, not on the digit);
  - `Code 4ः 8ः 2ः 9ः 1ः 3` (U+0903, a spacing mark `Mc`, which is neither a number, a separator nor `Mn`/`Me`).

  Every A4 case is blanked, and so are the ZWSP, tag-character, U+2028, soft-hyphen, Hangul-filler, superscript, circled, math-bold and Arabic-Indic forms. The spec allows at most 3 separators, and marks only directly after a number, so the code conforms.
- **Impact:**
  - A paired peer's name is in the notification body (desktop mode) and on the terminal line. The digits can lead a hurried human to type a wrong code.
  - The cost is wasted attempts, and at worst the daily wrong-code lockout (`approval_locked`). The peer cannot reach an approval this way.
  - This is not a regression: the removed `stripLongDigits` allowed only one separator.
- **Fix direction:**
  - Inside a run, treat every mark (`Mn`, `Mc`, `Me`) as part of the run wherever it stands.
  - Allow separators up to a larger cap (for example 8) when every one is space, `P*` or `S*`.
  - Add the four names above to `TestNameBlanksDecoyCodes`.

### F5S-3 · Info: OD-R55F5-6's residual is not terminal-only
In desktop mode the notification body is `summary + "Type this code only into …"`, with the code in the notification **title**. So the quoted agent-chosen fields can also put a decoy next to the code in the toast: request title, path, argv, constraint text.
- Only the local agent controls those fields, and it can already reject any approval (approval.md §Flow step 4). So it gains no new power.
- This is doc drift in the OD text ("wasted attempts in terminal mode"), not a code defect.

### F5S-4 · Info: the grant's peer is not cross-checked against the token's `Aud`
`grantFacts` shows `peer.PublicKey` from the handler, and `rowMatchesToken` compares `rec.Peer` with that same value.
- The token's `g.Aud` is set from `peer.PublicKey` a few lines earlier, and Perform submits to `peerKey`. So the window names the real recipient, and there is no exploit.
- For M3's "the token agrees with the row on every shared field", add `g.Aud == rec.Peer` (and `g.Iss == id.Self`) to `rowMatchesToken`.

### F5S-5 · Info: terminal line punctuation
`deliveryText` (terminal) formats `"%s. Code %s…"`. Every builder output already ends in `.`, so the line reads `…exactly this.. Code 482913.`. This is cosmetic. `phase2-harness`/`phase3-harness` parse the code after `Code `, so they are unaffected.

## Checked and found sound

1. **Every kind states its facts, from verified sources.**
   - **grant:** decoded from `wire`, the exact bytes Perform submits. It shows:
     - action, label, branch, scope (or "the whole folder/repository");
     - `Sensitive…` or `PUBLIC … NOT quarantined`;
     - duration, UTC expiry, session, and the quoted request title.

     The path comes from the row (F7's resolved path; `recheckResource` at confirm).
   - **grant_policy:** facts from `pol`, which Perform inserts. It shows `until` in UTC with its duration, action, path, branch, scope, the per-grant maximum, and PUBLIC or sensitive-only. `Match` enforces `Public == !Sensitive`.
   - **device_link:** facts from the intent row: role, the full 5-group fingerprint, and "compare all five groups".
   - **device_scope:** facts from `resolved`, which Perform stores. It shows:
     - the command count and names first, then the expiry and types;
     - per command: quoted dir, argv, timeout and env names;
     - the controller's fingerprint.
   - **release / accept_result:** facts from the session row in the transaction. They show status and sizes only (never content), K or the rule-2 reason, the round and the session. K uses clause (1) of `QuarantineHolds` exactly.
   - **debate_constraint:** facts from the captured `pc`.
   - No summary field comes from IPC params.
2. **Spoofing.**
   - The fingerprint is computed from the bound key, never from a card. It comes first, after the fixed LTR word `peer`, so a right-to-left name cannot move its digits.
   - Names go through `Name` and then `Quote`, so `"` shows as `\"` and `\` as `\\`, and no name can close the quotes or cut zenity.
   - Same-name twins differ in the fingerprint (A8).
   - Confusable names pass by design; the fingerprint binds.
   - Stacked marks: at most 2 per base, removed in names and escaped in exact fields (probe: `Mn` and `Me`, Thai, Tamil).
   - Names are not cut; card names are at most 128 code points.
3. **Precondition/Rebuild.**
   - `checkAction` runs after the `approved` UPDATE inside the confirm transaction, so SQLite's write lock is held. No rename can commit between `Rebuild`, the byte comparison with `approvals.summary` (read in the same transaction) and `Perform`.
   - All seven kinds set `Rebuild`.
   - `DeepEqual` is safe here: every time goes through `.UTC()`, which strips the monotonic clock, and slices come from one source.
   - Facts outside the structs:
     - `Aud`, `Iss` and `Resource.Kind` sit in the constant `wire` (F5S-4);
     - scope repos that no command uses are not executable;
     - the link state is covered by the Precondition.
   - A re-key or unpair gives `(no longer paired)`, which is a mismatch, so the approval is rejected.
   - Tampering with a grant row's scope or sensitive flag is caught by `rowMatchesToken` (A19). An UPDATE of `approvals.summary` is caught too (A10).
4. **4096, never cut.**
   - The builder returns `ErrTooLong` for every kind (scope checks inside its loop). Handlers map it to `bad_scope` or `bad_request` before any row, approval or supersede. The link intent row is deleted on error.
   - Backstops:
     - `Store.Create` refuses a text that is not `Safe` before rate limits, audit, window or notification (A3);
     - `windowText` refuses to open (A2).
   - The reopen note is cleaned (at most 200 code points) and not counted. Toast, terminal and `approval_list` carry the text uncut.
5. **Sanitiser.** `Hidden` covers:
   - C0, C1 and DEL;
   - all of `Cf`: bidi, ZW*, U+2060–2064, U+FEFF, U+00AD, U+061C, U+180E and tags;
   - U+2028/9;
   - both variation-selector blocks;
   - `Other_Default_Ignorable` (Hangul fillers, U+17B4/5, U+034F);
   - `Zs` other than U+0020, and U+2800;
   - everything that is not graphic (`Co` private use, `Cn`, `Cs`).

   Invalid UTF-8 becomes `\ufffd`. `Quote` does not HTML-escape. `decision.Visible` now calls `Hidden` directly; its old union is the same set, and `verifyvectors` reproduces every vector.
6. **Regressions.**
   - `Name` is strictly stronger than the removed `stripLongDigits(notify.Clean(…,40))`: every old case is still blanked, plus the invisible-split cases.
   - Digits kept in exact fields are OD-6's accepted residual, which only the local agent can reach (F5S-3).
   - Pre-upgrade approvals cannot be confirmed: the in-memory `live` map is lost on restart and every `pending` row is expired at start (`approval.go` `live`), so no old, cut or unclean text reaches a window or a confirm.
7. **Tests.**
   - A1–A19 are all present. A10 covers a rename for all seven kinds, and the summary UPDATE for grant and the store.
   - `FuzzBuild` covers every kind with display-safety, length and determinism under a `time.Local` switch.
   - Mutation sense:
     - dropping `Rebuild`, the `Store.Create` backstop, the `windowText` check, `isSixDigits`, fingerprint-first or step 5 each breaks a named test (A10, A3, A2, A13, A8/A15, A15);
     - removing the quote escaping breaks A15/A16.
   - Gaps: the F5S-1 and F5S-2 names above.
