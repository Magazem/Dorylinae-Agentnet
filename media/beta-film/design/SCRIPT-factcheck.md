# SCRIPT.md fact-check

Checked: `design/SCRIPT.md` (1085 lines, read in full) against `research/product-facts.md`, `research/fact-check.md`
(overrides), `research/beta-facts.md`, `design/BRIEF.md` (owner decisions) and `capture/NN-*.txt`.
Default verdict when support was not found: fails. Line numbers below are SCRIPT.md lines.

**Verdict: 1 must-fix, 5 low fixes, 1 owner gate. No terminal line, beta fact or forbidden item fails.**

## Problems, fixes, sources

### 1. MUST-FIX. "Their agent finds the cause." claims an outcome the evidence does not show
- Where: caption C10 (l.640), logline (l.39), shot table (l.161), S7 purpose (l.634), copy deck row 21 (l.832), risk 5 (l.1051).
- Why it fails: the capture is scripted. `capture/24` l.3 says "a small script played the human"; SCRIPT risk 5 says "driven by a
  script, not live agents"; `capture/INDEX.md` l.5-6 lists theo's harness as `codex`, whose real run was blocked
  (product-facts §5 #5, §3.20). `flaky-finding.md` and the summary string are staged fixtures. Only "Your agent writes the
  brief." is licensed as a product sentence (product-facts §5 #25, PLAN:89). Nothing licenses "finds the cause" as product or
  agent behaviour. Row 21's cited source (§3.11, structured result) supports a result being sent, not a cause being found.
- Fix: replace with `Their agent sends its answer.` (5 words, same hold floor 2.4 / key 3.6; Special Elite measured 1089.5 px
  at 72 px, 907.9 px at 60 px in 4:5, so update the widths 1047 → 1090 and 873 → 908; both fit their safe areas, S7 has no trail).
  Edit the other places to match:
  - l.39: "...the request lands in his inbox, his agent sends its answer, and it arrives at maya's machine."
  - l.161 and l.634: "Their agent sends its answer: the payoff at 64 px." / "The payoff: their agent sends its answer, and it meets the brief's own *Done when* ("You name the cause")."
  - l.832 source cell: `capture/21` (the visible command `agentnet result … --status pass`); product-facts §3.11.
  - l.1051: "their agent sends its answer".
  Keep the 64 px token `Cause found: two tests share one ledger row` unchanged: it is verbatim capture output
  (`capture/21-quarantine-release.txt` l.4). S8 "The answer comes back." then continues the same noun.
- Sources: capture/24 l.3; capture/INDEX.md l.5-6; SCRIPT risk 5; product-facts §5 #5, #25, §3.20; BRIEF ("real product, real output", nothing claimed beyond it).

### 2. LOW. S1 C2 "They paste it into their coding agent." is an inference, cited as a source
- Where: l.813 (copy deck row 2), l.120, l.339, l.350, risk 7 (l.1056).
- Why: no document says the teammate pastes the context into their agent. The sourced pain is only "you paste the context into a
  chat" (product-facts §1a; PLAN:11 "coordinates by pasting context into Slack or WhatsApp"; each person runs their own agent).
  product-facts §2 says "do not invent" a pain narrative. Row 2's cited quote ("each running their own agent harness") does not
  say it.
- Fix (keeps the line; makes the source honest): row 2 source cell becomes `Inferred from PLAN:11 (context pasted into a chat; each
  person runs their own agent) and product-facts §1a. Not stated in any doc. Owner to accept as framing.` Add the same sentence
  to risk 7. If the owner rejects the inference, strictly sourced replacement for C2 (16:9 one line, 4:5 `Each of you has` /
  `your own coding agent.`): `Each of you has your own coding agent.` (BRIEF: "each of you keeps your own agent").

### 3. LOW. "From the people at Dorylinae" implies several people
- Where: S10 sender label (l.730, l.745), l.47, l.839.
- Why: the docs describe one operator ("one person at about 10 hours a week", beta-facts §4 row "Single operator"). BRIEF's
  "Dorylinae (the people)" permits "we", but the plural "people" is the script's wording, not the owner's.
- Fix: `From Dorylinae`. Widths: 221 px at 28 px (card was 468 px wide, shrink to about 270), 332 px at 42 px in 4:5. "We are
  opening a closed beta for small teams." then still reads as Dorylinae speaking (voice.md: we only where Dorylinae speaks).
  Keep the current label only if the owner confirms the plural.

### 4. LOW. §7 says "Nothing else was changed", but three typesetting edits sit outside BRIEF's list
- Where: l.1005-1006, l.608-611 ("as `capture/INDEX.md` allows"), l.252 and l.1014 (tracking).
- Edits (characters unchanged in all three): inbox ID column re-padded after id shortening (INDEX l.92 only allows re-aligning the
  `request list` table, not this one); 16:9 relay log set with letter-spacing -0.0125 em; 4:5 relay strip scaled from 36 px to
  13.27 px for the overview.
- Fix: l.1006 becomes "Nothing else in the text was changed. Typesetting only: the inbox ID column is re-padded after shortening
  (the CLI pads to the longest id, so shortened ids pad shorter), the 16:9 relay log uses -0.0125 em tracking, and the 4:5 relay
  strip is scaled." On l.609 replace "as `capture/INDEX.md` allows" with "whitespace only, a consequence of BRIEF's id shortening".
- Source: BRIEF "allowed edits"; capture/INDEX.md l.92; capture/15 inbox (original column width 34 + 2).

### 5. LOW. The quarantine started on arrival, not "after S8"
- Where: l.1022-1023 (§7), risk 4 (l.1045-1047).
- Why: `capture/21` shows `state quarantined`, `state_at 2026-10-08T13:43:54Z`, the same instant as the theo → maya mail in
  `capture/25` (13:43:54.425Z) that S8 draws. The answer reached maya's machine but her agent could not read it until a
  human released it. S8's "The answer comes back." is true; the §7 note is not.
- Fix: l.1022-1023 "on arrival at maya's machine (13:43:54) the result was quarantined until she released it with a human
  approval (capture/21); S8 shows the arrival only." Keep S8 as is (nothing shows maya's agent reading it).
- Source: capture/21 l.16 and l.20 (`state_at` 13:43:54Z); capture/25 l.122; fact-check B5; product-facts §3.14.

### 6. LOW. The 21° clip-path recipe cuts a 20.65° edge, not 21°
- Where: l.284 (16:9 and 4:5).
- Why: the vertices (x(0), -10) and (x(H), H+10) span dy = H+20 but only dx = H·tan 21°, so the angle is atan(1080·0.38386/1100) =
  20.65° (4:5: 20.72°). The brand number is 21° and the edge must be razor-hard.
- Fix: polygon = (-10, -10), (x(-10), -10), (x(H+10), H+10), (-10, H+10), where x(-10) = m + (H/2 + 10)·tan 21° and
  x(H+10) = m − (H/2 + 10)·tan 21° (offset 211.13 px in 16:9, 262.95 px in 4:5). m0, m1 stay as written.

### 7. LOW. "the definition appears once" (l.855) is false
- S2 (`With Dorylinae, your agent hands it to theirs.`) and S10 (`Dorylinae lets the agents hand each other work.`) both define the
  product. Fix the voice-check sentence: "the definition appears in S2 and again as the S10 Why value, never with the logo."
  Source: SCRIPT l.855 against l.387 and l.734; BRIEF ("no tagline").

### 8. OWNER GATE (not a script error, already risk 1). `dorylinae.net` has no documented page
- beta-facts §5: "No doc describes any page at the apex"; not verifiable from the sandbox. The tag appears on 16.2 s of the film and
  the end card is the address alone. "Apply" must not imply instant access (beta-facts §5: an invite code follows). The film must not
  ship until the owner confirms a live page with a way to apply. BRIEF fixes the destination; fact-check B19 (no release, Homebrew
  tap empty) is why S9's "macOS, Linux and Windows." must not be paired with any install line.

## Verified, no change needed

- **Terminal lines (all match the capture character for character, after the allowed edits).**
  S4 (`capture/15` l.26-28, all 4 flags kept, `r-97ae…`), S5 relay log lines A and C (`capture/25` l.61 and l.63; only
  `m-b2d3dc4566beac6d9631949d221555c0` → `m-b2d3…`; checked programmatically), S6 inbox (`capture/15` l.66-69, ids and padding
  only), S7 (`capture/21` l.4-5, `s-b3098b5fd25dd5ec2aa2b78fe588fc03` → `s-b309…`), S10 placeholder `<how the other side knows it is finished>` and the field
  names `What:` `Why:` `Done when:` (`capture/02` l.44-46), S1/S3 brief (= `capture/run/work/flaky-brief.md`, 76 / 57 / 68 characters).
- **Indices and arithmetic.** Line A 121 characters, line C 107. `13:41:40.228Z` at 16-28, `from=… to=…` at 64-88, `bytes=1444` at
  111-120. S4 token at characters 51-89, S7 token at 15-57 (43 characters). 4:5 strip windows and anchor characters, underline dot counts,
  S4/S7 push centres (1964.2, 690.6), (1586.8, 657.5), (1057.0, 434.6), (856, 439), typed-letter times (Not what., Your team applies.,
  relay, when, who, how big), bar:beat conversions, shot durations, time shares (5.7 / 9.5 / 32.4 / 17.1 / 35.2 %), word counts per row,
  boot geometry against `boot.svg` and `motion.ts`. All correct.
- **Time labels.** `maya · 13:41 UTC` (request created 13:41:40Z), `theo · 13:41 UTC` (queue flush 13:41:45.886Z; inbox AGE 0m),
  `theo · 13:43 UTC` (result 13:43:54Z). Consistent with capture/15, 21, 25.
- **Beta facts vs BRIEF.** "Closed beta / by invitation", "The first wave opens with 10 teams." (opening, not a cap; owner's team is one of
  the 10, fact-check B18, still true), "Free during the beta." (no later-pricing line), "macOS, Linux and Windows.", "small teams" (no fixed size),
  "Apply at dorylinae.net" worded identically on S3, S4, S10, S12. "We" only on S10.
- **Product sentences.** "With Dorylinae, your agent hands it to theirs." (product-facts §1a), "For small teams." (§5 #27),
  "Your agent writes the brief." (§5 #25), "It waits at the relay." (capture/25 `msg=queued`; fact-check B1 only bans "waits in their
  inbox"), "The relay sees who, when and how big. Not what." (BRIEF; capture/25; product-facts §3.10), "It lands in their inbox."
  (capture/15), "The answer comes back." (capture/25 route theo → maya).
- **Forbidden-item sweep (grep of SCRIPT plus on-screen strings).** No `agentnet setup/login/feedback`, no `BETA-` code, no install
  script or Homebrew, no relay hostname (`ws://`, `relay.dorylinae.net`), no GitHub or handle, no hashtag, no harness name (Claude, Codex,
  OpenCode, Hermes, Antigravity, agy), no version string (`0.0.0-dev`, `860afe8`), no repo or machine path, no ticket or decision number,
  no "secure / private / encrypted / sealed / audited / tamper-proof / open source / zero-knowledge", no fixed team size, no
  later-pricing line. File names `flaky-brief.md` and `flaky-finding.md` are working-directory names, not repo paths. `team atlas`
  is a team name. Glyph coverage (`·`, `…`, `<`, `>`, `$`) confirmed in Special Elite, Plex Sans 500 and Plex Mono with opentype.js.

## Cross-dependency

`SCRIPT-voicecheck.md` V1 and V2 would change the brief text and the `--summary` string. If the owner accepts them, `capture/20` and
`capture/21` must be re-captured, and this check re-run for S1, S3, S7 and §7 (the "byte-identical to the `brief` field" claim included).
Problem 1 does not depend on that.
