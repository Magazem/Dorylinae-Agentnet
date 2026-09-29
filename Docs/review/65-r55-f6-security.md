# 65: R55-F6 security review (PR #18)

Reviewer: SEC-F6 · model `claude-opus-5-5` · 2026-09-29 · PR head `8a2214b` (diff read with
`gh pr diff 18`; no git commands run). Scope: approval-window rendering on every OS (R55-025,
R55-203), review 64 F5S-1 and F5S-2, R55-105. Severity per
[55-code-review/01-rubric.md](55-code-review/01-rubric.md).

**Verdict: approve.** No finding blocks the merge. The Linux and macOS windows now show exactly
the builder's text, and I could not make them show anything else. Two Lows remain: the F5S-1 and
F5S-2 heuristics still have gaps, but both checks are second lines of defence, and a follow-up
ticket can close them. Two Infos are notes for the owner.

## What I checked

### 1. Linux: zenity and kdialog (`internal/notify/window_dialogtext.go`, `window_linux.go`)
- **zenity.** `zenityText` doubles `_`, then `\`. Neither step creates the other character, so
  the order does not matter. Through the verifier's GLib 2.80 `g_strcompress` port (T11-01) and
  the GTK mnemonic pass, the result is exactly the input:
  - `\0`, `\n`, octal (`\342\200\256`, `\064…`) and a trailing `\` all come out literal;
  - `__init__` and a trailing `_` keep every underscore;
  - `& < >` are left alone, which is right: the label is not markup.
  - GTK's `gtk_label_set_uline_text_internal` gives up on invalid UTF-8. That cannot happen here,
    because doubling every `\` means no escape can produce a raw byte, and `dialogArgSafe`
    refuses invalid UTF-8.
- **kdialog.** `kdialogText` forces rich text with `<qt>`, which makes `Qt::mightBeRichText`
  true. `& < >` become entities, each `&` becomes `&amp;&amp;`, and `\` is doubled for
  `parseString`.
  - The QLabel mnemonic pass runs on the parsed document only when the raw text holds an `&`. It
    deletes each `&` and keeps the next character, so `&&` shows as `&`. The only `&` in the
    document comes from `&amp;` pairs. The test's model matches this.
  - Qt entity parsing ends at the `;` that each entity carries, so `&lt` typed by a peer stays
    literal.
  - `<qt>` injection is impossible: every `<` is `&lt;`, and `kdialogShows` fails on any tag
    inside the text.
  - Side effect: raw `&amp;` registers Alt+A as a shortcut to the buddy (the code field). This
    only moves focus, which is harmless.
- **Titles.** Both tools show `--title` literally, and the title is daemon text
  (`"AgentNet approval " + Clean(tag)`). Every value is still a single `--opt=value` argument.
- **Refusals.** `dialogArgSafe` refuses NUL, C0, DEL, C1, U+2028/9 and invalid UTF-8, and the
  window then fails closed (`notReady`). `windowText` already requires `displaytext.Safe`, and
  `Clean`s the tag and the note, so normal text never trips this check.
- **Toast (`approval_linux.go`).** Unchanged. It is an in-process D-Bus `Notify` with the body
  markup-escaped and no `g_strcompress`. `notify-send` (`desktop_linux.go`) still doubles `\`.

**Evidence:**
- `go test ./internal/notify ./internal/displaytext` passes on Windows (the dialog-text tests
  have no build tag).
- `GOOS=linux` and `GOOS=darwin go vet ./internal/notify` are clean.
- `FuzzDialogText` ran for 90 s, about 19.7M executions, with 0 failures. It ran on a copy in my
  temp dir, without the `windowText` clause.
- Mutation check: 10 mutants, each killed by the tests. Removing the `_` doubling, the zenity `\`
  doubling, the kdialog `\` doubling, `&lt;`, `&gt;`, the doubled `&`, the `<qt>` wrapper, the
  UTF-8 check, the C1 check or the U+2028/9 check all fail the suite.

### 2. macOS (`osa_darwin.go`, `window_darwin.go`, `approval_darwin.go`)
- **Injection.** Values reach the script as `NAME=base64(UTF-8)`. The names are fixed, and a
  base64 value is `[A-Za-z0-9+/=]`, so it cannot inject into the environment or into
  AppleScript. The script is constant, and `-e` receives only the fixed text.
- **Decoding.** `NSData initWithBase64EncodedString:options:0` returns nil on any character
  outside the alphabet, and `NSString initWithData:encoding:UTF8` returns nil on bad UTF-8. Both
  cases raise an error, so osascript exits non-zero, which means "not ready" and never an answer.
- **Display.** `display dialog` and `display notification` show the decoded text verbatim. There
  is no mnemonic or markup layer in either.
- **The code notification** now uses the same transport. The new test asserts that the code never
  appears in the clear in the environment.
- **Tests.** `TestOsascriptReadsNonASCIIExactly` does a real osascript round-trip, reading the
  result back as base64, so it does not depend on the output encoding. `TestOsascriptRefusesBadEnvText`
  and the osacompile test cover both scripts. These run only on macOS CI.

### 3. Windows
No Windows file changed, and the transport (base64 UTF-16 → `Label.Text`) was already literal.
The `displaytext` changes apply the same way on every OS. **Nothing to fix.**

### 4. displaytext (F5S-1, F5S-2)
Review 64's reported strings are all blanked now, and the new tests cover them. The findings
below come from probes I ran on a copy of `displaytext.go`.

### 5. R55-105 (`removable`)
- Approval ids come from `newID` (`a-` + hex). The only `Show` calls with other ids are
  `"outcome-"+id` and `"lock-"+time` (`internal/approval/store.go:620,926`), and the Store never
  removes those.
- Every `Remove` passes an approval id, and code notifications are still recorded.
- **No regression.** The new Linux and cross-OS tests cover this.

## Findings

### F6S-1 · Low · confirmed (probe): fingerprint folding misses other look-alike alphabets
- **Where:** `displaytext.foldFP` and `fpConfusables`.
- **Evidence:** `Name` leaves all of these unchanged:
  - `Desktop 𝟐𝐄𝐃𝟗 𝐓𝐆𝐕𝐄 𝐑𝟒𝟕𝟏` (mathematical bold);
  - `②ⒺⒹ⑨ ⓉⒼⓋⒺ` (enclosed);
  - `2ꓰD9 TꓖVꓰ R471` (Lisu);
  - `2ᎬᎠ9 ᎢᏀᏙᎬ R471` (Cherokee);
  - `2ᴇᴅ9 ᴛɢᴠᴇ ʀ471` (small capitals);
  - `2ED9ㆍTGVEㆍR471` and `2ED9ǀTGVEǀR471`: the joiners U+318D and U+01C0 are letters (`Lo`),
    not separators.
- **Cause:** review 64 asked for NFKC. Only fullwidth forms were folded, since `displaytext` is
  stdlib-only.
- **Impact:** as F5S-1. The real fingerprint still comes first after `peer`, so this check is a
  second line of defence. Low.
- **Fix direction:**
  - fold the mathematical alphanumeric (U+1D400–1D7FF) and enclosed (U+2460–24FF) ranges
    arithmetically;
  - add Lisu (U+A4D0–A4FF), the Cherokee capitals and the Latin small capitals to the table;
  - count `Lm`, and `Lo` runes that are not in a letter run, as joiners;
  - or record the rest as an accepted residual.

### F6S-2 · Low · confirmed (probe): decoy codes survive with letter separators or letter-for-digit look-alikes
- **Where:** `displaytext.blankDigitRuns`.
- **Evidence:** these are unchanged:
  - `Code 4ǀ8ǀ2ǀ9ǀ1ǀ3` (U+01C0 Lo), `4ˑ8ˑ2…` and `4ː8ː2…` (U+02D1/U+02D0 Lm), and `4ㆍ8ㆍ2…`
    (U+318D);
  - `Code 48291З` (Cyrillic Ze) and `Code 4829l3` (a lowercase L among 5 digits).
- **Impact:** the same as F5S-2: wasted attempts, and at worst the daily lockout. This is not a
  regression. The widening to 8 separators opened no new bypass. I found no case that was blanked
  before F6 and is not blanked now, since the new loop only adds marks and separators.
- **Fix direction:**
  - treat `Lm` and the single-rune look-alike `Lo` as separators inside a run;
  - count a run's `foldFP` digit look-alikes (`З`, `б`) toward the 6;
  - or accept this, since the peer cannot reach an approval this way.

### F6S-3 · Info: the wider fingerprint joiner blanks more ordinary names
- Any two 4-letter words from the fingerprint alphabet (no `I L O U`) now chain across 1–3
  separators. So `Mary & Jake` and `Zack.Hart` become `…`, which they did not before F6.
- `Nate Kent` (one space) was already blanked by F5, so this is not new.
- **Impact:** the name shows as `…`. The fingerprint still identifies the peer, so this is
  cosmetic. No security impact.
- **Owner option:** require at least one digit across a chain of 2–3 groups. Real fingerprints
  almost always contain one.

### F6S-4 · Info: the kdialog oracle is the author's own model
The zenity oracle is the verifier's port, checked against GLib 2.80. The kdialog model
(`kdialogParseString`, `kdialogShows`) has no independent check of that kind.
- It matches Qt's `QLabelPrivate` behaviour as I know it (the mnemonic pass on the document only
  when the raw text holds an `&`) and `mightBeRichText` for `<qt>`.
- However, `Utils::parseString` was not re-read from the kdialog source in this review.
- Keep the `tests/phase2-manual.md` kdialog line mandatory on one KDE box before release.

## CI (`gh pr checks 18`, read-only)
All green at `8a2214b`: build, cross, lint, race, test (macos, ubuntu, windows), install-sh,
unix service install (macos, ubuntu), flag-sensitive-paths. govulncheck skipped. The
real-osascript tests ran and passed in test (macos-latest).
