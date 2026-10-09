# SCRIPT voice check

Scope: every on-screen word in SCRIPT.md (S1 to S12, the §3.5 and §5 labels, the §4 copy deck and the §6 end card), checked against `brand/dorylinae-brand/dist/docs/voice.md`, `dist/DESIGN.md` §3 and §7, and the BRIEF rules.

Result: 4 violations (V1 to V4). Each is a copy or type fix; none changes the shot timing.

## Violations

**V1. Numerals in the brief text (S1 plates A and B, 0.000 to 3.600; S3 brief lines, 9.600 to 13.200; §4 rows 1 to 9; §7).**
On screen: "fails about one run in ten", "It blocks merges twice a day", "rule out the three usual suspects".
Rule: numbers as numerals (voice.md).
Fix: "about 1 run in 10", "2 times a day", "the 3 usual suspects". The text is byte-identical to the brief input, so change `capture/run/work/flaky-brief.md` and the `brief` field in `capture/20-grant.txt`, then update S1, S3 and §7. If the owner keeps the wording as quoted input, record that as a waiver.

**V2. Numerals in the hero payoff (S7, 33.600 to 38.400; §4 row 23).**
On screen: "Cause found: two tests share one ledger row".
Rule: numerals.
Fix: "Cause found: 2 tests share 1 ledger row". This is captured command text, so change `--summary` in the capture run and re-capture `capture/21-quarantine-release.txt`. Then recompute the S7 token width and the underline dot count (15:4 onward).

**V3. Sentence case on specimen labels (S5 and S8 relay label; S5 cards, 22.800 to 26.336; §4 rows 12, 14 to 16).**
On screen: "relay", "when", "who", "how big" (Special Elite, lower case first word).
Rule: specimen labels take a capital first word, lower case after it (voice.md; DESIGN.md sentence case).
Fix: "Relay", "When", "Who", "How big".

**V4. Sentence case on collector labels (S3 and S4, 9.600 to 19.200; S6, 30.000 to 33.600; S7, 33.600 to 38.400; §4 rows 8, 20, 22).**
On screen: "maya · 13:41 UTC", "theo · 13:41 UTC", "theo · 13:43 UTC" in Special Elite, lower case first word.
Rule: as V3.
Fix, choose one:
- (a) Capitalise to follow the rule as written: "Maya · 13:41 UTC", "Theo · 13:41 UTC", "Theo · 13:43 UTC".
- (b) Set the collector lines in IBM Plex Mono. Mono labels are exempt from sentence case, so the handles stay lower case as in the prompts `maya$` and `theo$`. This changes the COLLECTOR role in §3.2 and needs the owner's approval. Recommended.

## Checked, no violation

- No exclamation marks, emoji, hype words or misspelt product name (no "Dory", "DoryLinae" or ".ai").
- No ant word on screen. Trail, column and similar appear only in production notes, never as on-screen text.
- "We" appears only in the S10 sender label and the "We are opening a closed beta" line, as voice.md allows.
- On-screen sentences are short, declarative and present tense. "For small teams." and the label fragments are owner-approved or label copy.
- Special Elite is at or above 28 px (16:9) and 42 px (4:5) on every shot, and is never set as a paragraph. Two-line display values (S9 hero, S10 What) are single sentences in display type, which DESIGN.md allows.
- The name "Dorylinae" in running text is the product name (voice.md). The wordmark DORYLINAE appears only in the S11 boot and its typed letters.
- No tagline is invented. The address is the owner's CTA, and the tag and end card use identical wording.
- Dates: no specimen label carries a date. The ISO timestamps in S4, S5 and S6 are captured terminal output, which the BRIEF exempts from the collector-date form.
