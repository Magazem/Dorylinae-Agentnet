# 96: R55-F6b security review

Reviewer: R55-F6bsec-Opus · model `claude-opus-5-5` · 2026-10-02 · branch `p4/r55-f6b`, commit
`45570f4` (diff read with `git diff main...HEAD`; read-only git only). Inputs: review 65 (F6S-1,
F6S-2, F6S-3), D66, plan [95-r55-f6b-plan.md](95-r55-f6b-plan.md), `Docs/protocol/approval.md`
§Sanitising steps 4 and 5. Severity per [55-code-review/01-rubric.md](55-code-review/01-rubric.md).

**Verdict: approve.** Every string from review 65 F6S-1 and F6S-2 is now blanked. D66 is
implemented as written ("Mary & Jake", "Zack.Hart" and "Nate Kent" show again). There is no
second sanitiser, and Name and Term do not disagree in any dangerous way. Two Lows remain. Both
are pre-existing gaps in the same heuristics, both are second lines of defence (the real
fingerprint still comes first, right after `peer`), and neither blocks the merge. Four Infos
follow.

## What I checked

Method: a throw-away probe test in `internal/displaytext` (deleted afterwards; `git status` is
clean), run against this branch and against `main`'s `displaytext.go` copied to a temp module,
so I could tell regressions from pre-existing behaviour.

1. **Bypasses of the new rules.**
   - **Arithmetic ranges** (`displaytext.go:351-402`). I checked each range against the code
     charts:
     - Latin math: 13 × 52 ends at U+1D6A3.
     - Greek math: 5 × 58 ends at U+1D7C9; index 17 (ϴ) and the symbols (∇, ∂, ϵ …) give 0.
     - Math digits: 5 × 10.
     - Enclosed, dingbat and U+1F1xx ranges: each 2-digit form (⑩, ⓫, ❿) gives 0.
     - In U+1F110–1F189, the `% 32` index skips exactly the non-letters (🄪 to 🄯, 🅊 to 🅏,
       🅪 to 🅯).
     - Unassigned holes in the math block are not graphic, so `clean` removes them first.

     All correct.
   - **Normalisation order.** No normalisation runs. `fold` works rune by rune, after `clean`.
     Decomposed text (`2ED9́ TGVE`) is blanked, because marks inside a run are skipped. A
     precomposed accented letter (`2ÉD9`) is not folded, but its accent shows, so it is not a
     look-alike.
   - **`unicode.ToUpper` side effects.** `ı`→`I` and `ſ`→`S` fold. That is harmless: it can only
     cause more blanking.
   - **Mixed scripts** are matched rune by rune. `ТЕАМ ВЕТА` (Cyrillic) folds to `TEAM BETA` and
     shows, as D66 intends.
   - **The 4+ group reading of D66.** D66 reads "requires at least one digit across a 2–3 group
     chain". It says nothing about 4 or more groups, so leaving those blanked as before
     (`displaytext.go:486`) is the literal reading. It is also the safe side: a real fingerprint
     has 5 groups. Consistent and safe. A 4-group digitless fragment has probability ≈ 0.25 %.
   - **Spec probabilities** (about 5 % and 1 %): checked, (22/32)^8 = 0.050 and
     (22/32)^12 = 0.011.
   - **Bypasses found:** L1 and L2 below, and Info I1.
2. **False positives.** Unchanged in every script I tried:
   - Mary & Jake, Mary Kate, Zack.Hart, Nate Kent, Jean-Paul Sartre;
   - Анна Мария, Αθηνα Ελενη, 山田 太郎, محمد علي, דוד כהן;
   - Nguyễn Văn An, Seán Ó Briain, Ngozi Adebayo, Hawaiʻi Team;
   - Ryan's MacBook Pro, iPhone 15 Pro Max, Server 42 Rack 7, Ward 7 Bed 12 Unit 340;
   - Team A3 B4 C5, DEV-2 TEAM, Café 1984, Mary2 & Jake.

   New false positives from the look-alike rule are in I2; the 4-group cases are in I3.
3. **Performance.**
   - `fold` is a switch, at most two map lookups and a 32-byte `ContainsRune`.
   - `standsAlone` scans only across marks. `clean` caps `Mn`/`Me` at 2 but not `Mc`, so I tried
     long `Mc` runs.
   - Probe inputs of 10k–20k runes all ran in 0.5–2 ms:
     - 20k `Mc` followed by `l`;
     - `l` + 50 `Mc`, repeated;
     - 5k `ˑ` joiners;
     - `ABCD ` × 3000;
     - `1 l ` × 5000.
   - Both steps are linear, and card names are capped at 128 code points (`agentcard.go:25`). No
     issue.
4. **Consistency with F10.**
   - `Name` has one caller, `approvaltext.go:190`. It reuses `clean`, `isAnyMark` and `Hidden`;
     there is no second sanitiser.
   - The CLI prints the approval summary as stored (`cmd/agentnet/approve.go:115`). The store has
     already checked it with `Safe`.
   - Every other CLI view prints peer names through `Term` exactly, never blanked (OD-F10-3), and
     none of them is an approval prompt.
   - One related observation, outside F6b's scope, is I4.
5. **Tests.** `displaytext_test.go` covers every review 65 string, the D66 edges (2, 3 and 4
   groups; a digit in a math, circled or Cyrillic form) and ordinary names. It also has a table
   self-check (every entry is a letter and folds as listed; every joiner letter is `Lo` and folds
   to 0). The plan's 6-mutant check is credible.
   - `go test -count=1 ./internal/displaytext ./internal/approvaltext ./internal/notify
     ./internal/approval ./internal/decision`: all pass.
   - The documented residuals are acceptable for a Low, and so is L2's extension of them.

## Findings

### L1 · Low · confirmed (probe): a full fingerprint shows when its groups are 4+ separators apart, or when a 0/1 is written O/I/l
- **Where:** `displaytext.go:425-427` (`maxGroupSeparators = 3`), `:463-475` (`joined`), and
  `fold`, where `O`, `I` and `L` are not in the alphabet.
- **Evidence:** `Name` leaves these unchanged:
  - `2ED9 -- TGVE -- R471 -- KMNP -- QSTV`: each gap is ` -- `, 4 runes;
  - `ABlC DEFG OHJK MNPQ RSTV`: `l` for `1` and `O` for `0` break those groups. D66 then lets
    the digitless chain `MNPQ RSTV` show. On `main` this was `ABlC DEFG OHJK …`.
- **Cause:**
  - The 4+ gap has existed since review 64; it is not a regression.
  - O/I/l substitution also predates F6b. D66 makes it slightly wider, because the digitless
    pieces left between the broken groups now show.
- **Impact:** as F6S-1. The real fingerprint comes first, right after `peer`, so this is a
  second line of defence.
- **Fix:**
  - Raise `maxGroupSeparators` to 8, the same as `maxRunSeparators` in step 4. Only chains with a
    digit, or with 4 or more groups, are affected. The cost is that more names like
    `Team 2024 -- Mary Jake` are blanked; `Team 2024 - Mary Jake` already is today.
  - Add O/I/l-for-0/1 to the step 5 residual list in `approval.md`. Folding `O`/`I`/`L` into
    the alphabet would blank `Windows11`, `Microsoft365` and `John & Mary`, so it should not be
    done.

### L2 · Low · confirmed (probe): other bar-shaped letters still separate a decoy code
- **Where:** `displaytext.go:184-188` (`joinerLetters`).
- **Evidence:** these are unchanged: `Code 4ᛁ8ᛁ2ᛁ9ᛁ1ᛁ3` (Runic U+16C1), `Code 4ⵏ8ⵏ2ⵏ9ⵏ1ⵏ3`
  (Tifinagh U+2D4F) and `Code 4ꟾ8ꟾ2ꟾ9ꟾ1ꟾ3` (U+A7FE).
- **Cause:** this is the documented "narrow letters of other scripts" residual (`ו`, `ا`); a
  hand-made list cannot close it.
- **Impact:** as F6S-2: wasted attempts, at worst the daily lockout. This is acceptable as a
  residual.
- **Fix (pick one):**
  - **(a)** In `blankDigitRuns`, count a single letter as a separator when it stands alone
    between two numbers (reuse `standsAlone`). That closes the whole class, `ו` and `ا`
    included. The cost: `1920x1080` would be blanked. A name of 8 digits is rare, and blanking
    it is harmless.
  - **(b)** Add U+16C1, U+2D4F, U+A7FE, U+05D5 and U+0627 to `joinerLetters`, and name the
    class in the residual text. Then extend the `joinerLetters` test, which today requires `Lo`;
    these are `Lo` too.

### I1 · Info: Roman numerals are not folded in step 5
- **Where:** `fold`, `displaytext.go:351-402`.
- **Evidence:** `2ⅭⅮ9 TGⅤE` shows.
  - Ⅽ, Ⅾ, Ⅿ, Ⅴ and Ⅹ (U+216D/E/F, U+2164, U+2169) and their lower-case forms (U+217D/E/F,
    U+2174, U+2179) look like the alphabet letters C, D, M, V and X.
  - NFKC folds them, and review 64 asked for NFKC.
  - They are `Nl`, so step 4 already counts them as numbers. Only step 5 misses them.
- **Fix:** add the 10 runes to `fpConfusables`. The table test expects letters, so either
  exempt `Nl` there or add a separate `fold` case.

### I2 · Info: the look-alike rule blanks a few names that showed before
- **Where:** `isDigitLike` and `standsAlone` (`displaytext.go:196-224`).
- **Evidence:** `Google I/O 2024` becomes `Google …`, and `Equipo 12 o 3456` becomes
  `Equipo …`. Both showed on `main`. A standalone `I`, `O` or `o` (English, Spanish), or
  Ukrainian `з`, next to 4–5 real digits makes a run of 6.
- **Impact:** only readability. The fingerprint identifies the peer, and a blanked name only
  looks odd.
- **Fix:** none required. Recommend adding `Google I/O 2024` to the spec as a known
  false positive, so the owner sees the trade-off.
  - Tightening the rule (for example, a look-alike counts only when it touches a real number
    with no separator) would re-open `4829 l 3`-style decoys. I do not recommend it.

### I3 · Info: the 4+ group rule still blanks lists of four short names
- **Evidence:** these all become `…`:
  - `Matt, Jack, Ryan & Evan`;
  - `Mary-Jane, Kate & Anne`;
  - `Adam Beth Cara Dawn`;
  - four regional-indicator flags that make 4-letter groups.

  The same is true on `main`, so this is not a regression.
- **Note:** this matches D66's wording, and owner intent ("Mary & Jake must show") is met.
  - If the owner also wants four-name lists to show, the D66 limit could become 4 groups.
    The digitless-fragment risk would then rise to ≈ 0.25 % per 4-group fragment.
  - Recommend leaving it, and stating the example in `approval.md` step 5 so the behaviour is
    explicit.

### I4 · Info (outside F6b's scope): `agentnet decision show` prints the name before the fingerprint
- **Where:** `cmd/agentnet/decision.go:165-166`, which prints
  `initiator: <Term(name)> (fingerprint <fp>)`.
- **Problem:** `Term` escapes but never blanks (OD-F10-3). A peer named
  `Alice (fingerprint <victim fp>)` therefore makes the line read the victim's fingerprint
  first.
- **Context:**
  - This is not an approval prompt, and not F6b code.
  - approval.md's rule "fingerprint first" exists for exactly this reason.
- **Fix (follow-up, F10/F30 owner):**
  - print the fingerprint before the name (`initiator: <fp> named <name>`); or
  - put the name through `Quote`, as `approvaltext` does.

### Doc nit
`95-r55-f6b-plan.md` line 9 says `approvaltext` calls `Name` "for the peer name and the team
name". Only the peer name goes through `Name` (`approvaltext.go:190`); team names use
`plain`/`Quote` (`:409-410`). Correct the sentence; the behaviour is fine.

## Fixes applied

Applied by the reviewer (task 01a0fb7d) on `p4/r55-f6b`, on top of `45570f4` and this review.
No git command was run. Each new test was checked by mutation: the fix was reverted, the test
was confirmed to fail, and the file was restored from a backup copy.

- **L1, fixed.**
  - Code: `maxGroupSeparators` is now 8, the same as step 4 (`displaytext.go`).
  - Tests: `2ED9 -- TGVE -- R471` and `2ED9 - - - TGVE` are blanked. Gaps of 9 or more
    separators still do not join groups.
  - Spec: approval.md step 5 now says "1 to 8". Its residuals list O/I/l for 0/1, and groups
    more than 8 separators apart.
  - Accepted cost: `Team 2024 -- Mary Jake` is now `…`.
- **L2, fixed.**
  - Code: `joinerLetters` gains U+16C1 `ᛁ`, U+2D4F `ⵏ`, U+A7FE `ꟾ`, U+05D5 `ו` and U+0627 `ا`.
    All are `Lo` and fold to 0, which the table test checks. It is still a table, not a new
    rule.
  - Tests: one decoy code each.
  - Spec: step 4 lists the new letters. The residual now reads "beyond the joiner letters of
    step 4".
- **I1, fixed.**
  - Code: `fpConfusables` maps `Ⅰ Ⅴ Ⅹ Ⅼ Ⅽ Ⅾ Ⅿ` (U+2160, 2164, 2169, 216C–216F). The small
    forms fold through their upper case.
  - Tests:
    - the table test now accepts `Nl` as well as letters;
    - `TestFold` checks the small forms;
    - `2ⅭⅮ9 TGⅤE` and `2ⅽⅾ9 tgⅴe` are blanked.
  - Spec: step 5 lists them.
  - `Henry Ⅷ` and `Louis ⅩⅣ` still show.
- **I2, documented.** approval.md step 4 has a "Known false positives" note: `Google I/O 2024`
  and `Equipo 12 o 3456`. A test pins both. The rule is unchanged.
- **I4, fixed.**
  - Code: `cmd/agentnet/decision.go:165-168` now prints
    `initiator: fingerprint <fp>, named <Term(name)>`, and the same for the respondent: 2 lines
    changed plus a comment.
  - Test: `TestDecisionShowFingerprintBeforeName` in `decision_verify_sanitise_test.go`.
  - No doc pinned the old line.
  - Follow-up, not done: `internal/decision/markdown.go:61` (`--md`, Participants line) has the
    same name-then-fingerprint order. Changing it touches the golden files and decision.md, so
    it belongs to a separate ticket.
- **Doc nit, fixed** in `95-r55-f6b-plan.md`.

Verification:
- `go build ./...`: clean.
- `go vet ./...` with GOOS windows, linux and darwin: clean.
- gofmt: clean.
- `go test -count=1 ./internal/displaytext ./internal/approvaltext ./internal/notify
  ./internal/approval ./internal/decision`: pass.
- `go test -count=1 ./cmd/agentnet -run Decision`: pass.
