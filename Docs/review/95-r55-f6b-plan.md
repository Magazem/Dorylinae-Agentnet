# 95: R55-F6b plan and implementation notes

Ticket R55-F6b. Source: review 65 ([65-r55-f6-security.md](65-r55-f6-security.md)), findings
F6S-1, F6S-2 and F6S-3, with owner decision D66. Branch `p4/r55-f6b`.

## Plan

All three findings are inside `displayName` (approval.md §Sanitising, steps 4 and 5), which is
`displaytext.Name` in code. Only `approvaltext` calls `Name`, for the peer name and the team
name. F10's `displayTerm` (`displaytext.Term`, used by the CLI) **escapes** text and never
blanks it (OD-F10-3), so it does not change. The card text rule (D70) refuses bidi controls and
U+2028/9 at pairing time and does not apply here. This ticket reuses F10's shared predicates
(`Hidden`, `clean`, `isAnyMark`) and adds no second sanitiser. There is no protocol or wire
change, and no OD: every change below makes a name show more or blank more, as the findings ask.

| Finding | Rule | Files |
|---|---|---|
| F6S-1 more confusables | Step 5 folding (`foldFP`) also folds, arithmetically, the mathematical alphanumerics (U+1D400–1D7FF: Latin, Greek via the Greek table, digits), the enclosed alphanumerics (U+2460–24FF; dingbat circled digits U+2776–2793; U+1F100–1F189 digits with full stop or comma, parenthesized, squared and negative letters; regional indicators U+1F1E6–1F1FF). It adds tables for the Letterlike holes of the mathematical block (ℬ ℰ ℂ ℍ …), Lisu, the Cherokee look-alikes (small Cherokee letters reach them through their upper case) and the Latin small capitals. A **joiner letter** (every `Lm`, and the dot- and bar-shaped `Lo` runes U+01C0–01C3, U+A78F, U+318D, U+119E, U+11A2) counts as a separator between groups. | `internal/displaytext/displaytext.go`, `Docs/protocol/approval.md` |
| F6S-2 decoy separators | Step 4: a joiner letter counts as a separator inside a digit run. A **digit look-alike** (a letter that folds to a digit, `I`, `L` or `O`: `З`, `б`, `l`, `I`, `O`, `o`, `І`, `О`, `Ο`, `ꓳ` …) that **stands alone** (the nearest runes before and after it, marks skipped, are not letters other than joiner letters) counts as a number. A run must still hold at least one real number (category `N`). | same |
| F6S-3 / D66 | Step 5: a chain of **2 or 3** groups is blanked only if at least one of its characters folds to a digit. A chain of 4 or more groups is blanked as before (a real fingerprint has 5 groups; a 16-character fragment with no digit has probability (22/32)^16 ≈ 0.25 %). The 8-character run rule already needs a digit. `Mary & Jake`, `Zack.Hart` and `Nate Kent` show again. | same |

Tests: `internal/displaytext/displaytext_test.go`, with every string of review 65 F6S-1/F6S-2,
the D66 cases ("Mary & Jake" shown, chains with digits blanked, the 2-, 3- and 4-group edges)
and ordinary names that must stay unchanged.

Accepted residuals (approval.md §Sanitising, step 5 note): other narrow letters between digits
(Hebrew `ו`, Arabic `ا`), digit look-alikes that are not alone (`4829lO`), letters that look
like digits but are fingerprint letters (`S`→5, `B`→8), superscript and subscript letters, and
scripts not in the tables. In each case the real fingerprint still comes first after `peer`,
and a decoy code only wastes attempts (review 65, F6S-1/F6S-2 impact).

## Verification

- `go build ./...`, `go vet ./...`: clean. `gofmt`: clean.
- `go test ./internal/displaytext ./internal/approvaltext ./internal/notify ./internal/approval ./internal/decision`: pass.
- Full suite (once): only the known `C:\` ACL failures (`internal/device`, and `internal/daemon` scope tests failing with `writable_by_others: "C:\\"`).
- Mutation check: 6 mutants (D66 limit, joiner letters, digit look-alikes, the real-number rule, the mathematical range, the upper-case table lookup), each killed.
