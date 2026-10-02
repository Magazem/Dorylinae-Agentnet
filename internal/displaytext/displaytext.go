// Package displaytext holds the one character rule of approval summaries
// (Docs/protocol/approval.md §Sanitising: one character rule, two
// renderings, R55-F5): the hidden predicate shared by the approval-summary
// builder, device.DisplayQuote and decision.Visible, the two renderings Name
// and Quote, and the display-safe check the approval store and window apply.
// Line is the one-line rendering of untrusted diagnostic text (R55-F9).
// Escape, Term, Block and JSON are the terminal renderings of every agentnet
// print site and of --json output (R55-F10; approval.md §Sanitising:
// displayTerm, displayBlock, JSON output).
// It imports only the standard library, so every package can use it.
package displaytext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxSummary is the longest approval summary, in code points
// (Docs/protocol/approval.md §Length): notify.MaxWindowSummary.
const MaxSummary = 4096

// maxMarks is how many combining marks one base character keeps
// (Docs/protocol/approval.md §Sanitising, step 3; review 58a M4).
const maxMarks = 2

// Hidden reports whether r is a rune a human cannot see as itself: C0 and C1
// controls, format characters (Cf: bidi controls, zero-width characters,
// U+FEFF, tag characters), U+2028 and U+2029, variation selectors, the other
// default-ignorable code points (the Hangul fillers …), every space separator
// other than U+0020, U+2800, and any rune that is not graphic except U+0020
// (Docs/protocol/approval.md §Sanitising). It is exactly decision.Visible's
// set (review 46 H1, review 48 L3).
func Hidden(r rune) bool {
	if r == ' ' {
		return false
	}
	return isC0C1(r) ||
		unicode.Is(unicode.Cf, r) ||
		r == 0x2028 || r == 0x2029 ||
		unicode.Is(unicode.Variation_Selector, r) ||
		unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
		unicode.Is(unicode.Zs, r) ||
		r == 0x2800 ||
		!unicode.IsGraphic(r)
}

func isC0C1(r rune) bool {
	return (r >= 0x00 && r <= 0x1F) || (r >= 0x7F && r <= 0x9F)
}

// rendersAsSpace is the part of the hidden set that a renderer shows as
// blank space or a line break: controls, U+2028/U+2029, Zs and U+2800. Name
// turns these into U+0020 and removes every other hidden rune.
func rendersAsSpace(r rune) bool {
	return isC0C1(r) || r == 0x2028 || r == 0x2029 || unicode.Is(unicode.Zs, r) || r == 0x2800
}

func isMark(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me)
}

// Safe reports whether s is display-safe (Docs/protocol/approval.md
// §Display-safe output): valid UTF-8, no hidden rune but U+0020 (so one
// line), and at most MaxSummary code points.
func Safe(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxSummary {
		return false
	}
	for _, r := range s {
		if Hidden(r) {
			return false
		}
	}
	return true
}

// Name is displayName of Docs/protocol/approval.md §Sanitising, for text
// that only identifies (a peer's card name, a team name): the fingerprint
// binds, so the name may be changed to make it safe. Hidden runes that render
// as space become one space and the others are removed (so a zero-width
// split decoy reads as its digits, R55-066); spaces collapse; at most 2
// combining marks stay on each base; runs of 6 or more numbers and
// fingerprint-shaped text become "…"; an empty result is "(no name)". The
// name is never cut (OD-R55F5-4).
func Name(s string) string {
	rs := clean(s)
	rs = blankDigitRuns(rs)
	rs = blankFingerprints(rs)
	out := strings.TrimSpace(string(rs))
	if out == "" {
		return "(no name)"
	}
	return out
}

// clean is steps 1-3 of displayName: hidden runes that render as space become
// one space and the others are removed, spaces collapse and are trimmed, and
// at most 2 combining marks stay on each base.
func clean(s string) []rune {
	rs := make([]rune, 0, len(s))
	marks := 0
	for _, r := range s { // invalid UTF-8 decodes as U+FFFD
		if Hidden(r) {
			if !rendersAsSpace(r) {
				continue
			}
			r = ' '
		}
		if r == ' ' && (len(rs) == 0 || rs[len(rs)-1] == ' ') {
			continue
		}
		if isMark(r) {
			marks++
			if marks > maxMarks {
				continue
			}
		} else {
			marks = 0
		}
		rs = append(rs, r)
	}
	for len(rs) > 0 && rs[len(rs)-1] == ' ' {
		rs = rs[:len(rs)-1]
	}
	return rs
}

// lineScan is how much of its input Line reads: a longer input is cut there
// first, and the unread rest counts as a cut (Docs/protocol/envelope.md,
// "The daemon's reading").
const lineScan = 4096

// ellipsis marks a cut.
const ellipsis = "…"

// Line is displayLine of Docs/protocol/approval.md §Sanitising, for
// diagnostic text from an untrusted source (a relay's error message, a
// connection error): steps 1-3 of Name, with no digit or fingerprint
// blanking, on one line of at most maxBytes bytes. A cut lands on a rune boundary,
// drops trailing spaces and ends in "…", which counts towards maxBytes. "" stays
// "". maxBytes is at least 4.
func Line(s string, maxBytes int) string {
	cut := false
	if len(s) > lineScan {
		i := lineScan
		for i > 0 && !utf8.RuneStart(s[i]) {
			i--
		}
		s, cut = s[:i], true
	}
	out := string(clean(s))
	if !cut && len(out) <= maxBytes {
		return out
	}
	budget := maxBytes - len(ellipsis)
	if budget < 0 {
		return ""
	}
	end := 0
	for i, r := range out {
		if i+utf8.RuneLen(r) > budget {
			break
		}
		end = i + utf8.RuneLen(r)
	}
	return strings.TrimRight(out[:end], " ") + ellipsis
}

func isNumber(r rune) bool { return unicode.Is(unicode.N, r) }

// isSeparator reports a separator rune of steps 4 and 5: a space, P*, S*,
// or a joiner letter.
func isSeparator(r rune) bool {
	return r == ' ' || unicode.IsPunct(r) || unicode.IsSymbol(r) || isJoinerLetter(r)
}

// joinerLetters are the letters (Lo) shaped like a bar or a dot (review 65
// F6S-1, F6S-2: "4ǀ8ǀ2ǀ9ǀ1ǀ3", "2ED9ㆍTGVE"; review 96 L2: "4ᛁ8ᛁ2ᛁ9ᛁ1ᛁ3").
var joinerLetters = map[rune]bool{
	0x01C0: true, 0x01C1: true, 0x01C2: true, 0x01C3: true, // ǀ ǁ ǂ ǃ
	0xA78F: true,                             // ꞏ
	0x318D: true, 0x119E: true, 0x11A2: true, // ㆍ ᆞ ᆢ
	0x16C1: true, 0x2D4F: true, 0xA7FE: true, // ᛁ ⵏ ꟾ
	0x05D5: true, 0x0627: true, // ו ا
}

// isJoinerLetter reports a letter that reads as a separator: every modifier
// letter (Lm: ˑ ː ʹ …) and the letters of joinerLetters.
func isJoinerLetter(r rune) bool {
	return joinerLetters[r] || unicode.Is(unicode.Lm, r)
}

// isDigitLike reports a letter that reads as a digit (review 65 F6S-2:
// "48291З", "4829l3"): one that folds to a digit, I, L or O.
func isDigitLike(r rune) bool {
	if !unicode.IsLetter(r) {
		return false
	}
	f := fold(r)
	return (f >= '0' && f <= '9') || f == 'I' || f == 'L' || f == 'O'
}

// standsAlone reports whether the nearest runes before and after rs[i],
// combining marks skipped, are not letters (joiner letters aside).
func standsAlone(rs []rune, i int) bool {
	word := func(r rune) bool { return unicode.IsLetter(r) && !isJoinerLetter(r) }
	for k := i - 1; k >= 0; k-- {
		if !isAnyMark(rs[k]) {
			if word(rs[k]) {
				return false
			}
			break
		}
	}
	for k := i + 1; k < len(rs); k++ {
		if !isAnyMark(rs[k]) {
			return !word(rs[k])
		}
	}
	return true
}

// minDigitRun is the length from which a run of numbers reads as a code.
const minDigitRun = 6

// maxRunSeparators is how many separator runes may stand between two numbers
// of one run (review 58a L3: "4 - 8 - 2 - 9 - 1 - 3"; review 64 F5S-2:
// "4 - - 8 - - 2 …" and "4----8----2…").
const maxRunSeparators = 8

// isAnyMark reports a combining mark of any kind, spacing marks (Mc)
// included: inside a digit run or a fingerprint group it is part of the run
// wherever it stands (review 64 F5S-2).
func isAnyMark(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Mc, unicode.Me)
}

// blankDigitRuns is step 4: a maximal run of numbers (category N, and digit
// look-alike letters that stand alone, review 65 F6S-2), with up to 8
// separator runes between two of them and any combining mark (Mn, Mc, Me)
// anywhere in it, is replaced by "…" when it holds 6 or more numbers, at
// least one of them of category N.
func blankDigitRuns(rs []rune) []rune {
	num := func(i int) bool {
		return isNumber(rs[i]) || (isDigitLike(rs[i]) && standsAlone(rs, i))
	}
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); {
		if !num(i) {
			out = append(out, rs[i])
			i++
			continue
		}
		count, real, end := 0, 0, i
		j := i
		for j < len(rs) {
			if !num(j) {
				break
			}
			count++
			if isNumber(rs[j]) {
				real++
			}
			j++
			for j < len(rs) && isAnyMark(rs[j]) {
				j++
			}
			end = j
			k, seps := j, 0
			for k < len(rs) {
				if isAnyMark(rs[k]) {
					k++
					continue
				}
				if seps < maxRunSeparators && isSeparator(rs[k]) && !num(k) {
					seps++
					k++
					continue
				}
				break
			}
			if k < len(rs) && num(k) {
				j = k
				continue
			}
			break
		}
		if count >= minDigitRun && real > 0 {
			out = append(out, '…')
		} else {
			out = append(out, rs[i:end]...)
		}
		i = end
	}
	return out
}

// fpAlphabet is the fingerprint alphabet (envelope.PairAlphabet), matched
// case-insensitively.
const fpAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// fpConfusables folds letters that look like an ASCII letter or digit onto
// it (review 64 F5S-1: "2ЕD9 TGVЕ" with Cyrillic Е; review 65 F6S-1: Lisu,
// Cherokee, small capitals). A rune not found is looked up again in upper
// case, so lower-case Cyrillic, Greek and Cherokee letters fold too. The
// fullwidth, mathematical and enclosed forms are folded in fold.
var fpConfusables = map[rune]rune{
	// Cyrillic
	0x0410: 'A', 0x0430: 'A', 0x0412: 'B', 0x0432: 'B', 0x0415: 'E', 0x0435: 'E',
	0x0417: '3', 0x0437: '3', 0x041A: 'K', 0x043A: 'K', 0x041C: 'M', 0x043C: 'M',
	0x041D: 'H', 0x043D: 'H', 0x0420: 'P', 0x0440: 'P', 0x0421: 'C', 0x0441: 'C',
	0x0422: 'T', 0x0442: 'T', 0x0423: 'Y', 0x0443: 'Y', 0x0425: 'X', 0x0445: 'X',
	0x0405: 'S', 0x0455: 'S', 0x0408: 'J', 0x0458: 'J', 0x0431: '6',
	0x04AE: 'Y', 0x04AF: 'Y', 0x0500: 'D', 0x0501: 'D', 0x051A: 'Q', 0x051B: 'Q',
	0x051C: 'W', 0x051D: 'W', 0x0406: 'I', 0x04C0: 'I', 0x041E: 'O',
	// Greek
	0x0391: 'A', 0x03B1: 'A', 0x0392: 'B', 0x03B2: 'B', 0x0395: 'E', 0x0396: 'Z',
	0x0397: 'H', 0x039A: 'K', 0x03BA: 'K', 0x039C: 'M', 0x039D: 'N', 0x03BD: 'V',
	0x03A1: 'P', 0x03C1: 'P', 0x03A4: 'T', 0x03C4: 'T', 0x03A5: 'Y', 0x03A7: 'X',
	0x03C7: 'X', 0x03F2: 'C', 0x03F9: 'C', 0x037F: 'J', 0x0399: 'I', 0x039F: 'O',
	// Letterlike (also the holes of the mathematical alphanumerics)
	0x212A: 'K', 0x2102: 'C', 0x210A: 'G', 0x210B: 'H', 0x210C: 'H', 0x210D: 'H',
	0x210E: 'H', 0x2115: 'N', 0x2119: 'P', 0x211A: 'Q', 0x211B: 'R', 0x211C: 'R',
	0x211D: 'R', 0x2124: 'Z', 0x2128: 'Z', 0x212C: 'B', 0x212D: 'C', 0x212F: 'E',
	0x2130: 'E', 0x2131: 'F', 0x2133: 'M', 0x2145: 'D', 0x2146: 'D', 0x2147: 'E',
	0x2149: 'J',
	// Latin small capitals
	0x1D00: 'A', 0x0299: 'B', 0x1D04: 'C', 0x1D05: 'D', 0x1D07: 'E', 0xA730: 'F',
	0x0262: 'G', 0x029C: 'H', 0x1D0A: 'J', 0x1D0B: 'K', 0x1D0D: 'M', 0x0274: 'N',
	0x1D18: 'P', 0xA7AF: 'Q', 0x0280: 'R', 0xA731: 'S', 0x1D1B: 'T', 0x1D20: 'V',
	0x1D21: 'W', 0x028F: 'Y', 0x1D22: 'Z', 0x026A: 'I', 0x029F: 'L', 0x1D0F: 'O',
	// Lisu
	0xA4D0: 'B', 0xA4D1: 'P', 0xA4D3: 'D', 0xA4D4: 'T', 0xA4D6: 'G', 0xA4D7: 'K',
	0xA4D9: 'J', 0xA4DA: 'C', 0xA4DC: 'Z', 0xA4DD: 'F', 0xA4DF: 'M', 0xA4E0: 'N',
	0xA4E2: 'S', 0xA4E3: 'R', 0xA4E6: 'V', 0xA4E7: 'H', 0xA4EA: 'W', 0xA4EB: 'X',
	0xA4EC: 'Y', 0xA4EE: 'A', 0xA4F0: 'E', 0xA4E1: 'L', 0xA4F2: 'I', 0xA4F3: 'O',
	// Cherokee
	0x13A0: 'D', 0x13A1: 'R', 0x13A2: 'T', 0x13AA: 'A', 0x13AB: 'J', 0x13AC: 'E',
	0x13B3: 'W', 0x13B7: 'M', 0x13BB: 'H', 0x13C0: 'G', 0x13C3: 'Z', 0x13CE: '4',
	0x13D2: 'R', 0x13D4: 'W', 0x13D5: 'S', 0x13D9: 'V', 0x13DA: 'S', 0x13DE: 'L',
	0x13DF: 'C', 0x13E2: 'P', 0x13E6: 'K', 0x13EE: '6', 0x13F4: 'B',
	// Roman numerals (Nl; review 96 I1); the small forms fold through their
	// upper case.
	0x2160: 'I', 0x2164: 'V', 0x2169: 'X', 0x216C: 'L', 0x216D: 'C', 0x216E: 'D',
	0x216F: 'M',
}

// fold returns the ASCII digit or upper-case ASCII letter r reads as, or 0:
// ASCII in any case, the fullwidth forms, the mathematical alphanumerics
// (U+1D400–1D7FF), the enclosed and dingbat alphanumerics, the regional
// indicators, and the look-alikes of fpConfusables (review 65 F6S-1).
func fold(r rune) rune {
	switch {
	case r < utf8.RuneSelf:
	case r >= 0xFF10 && r <= 0xFF19: // fullwidth
		r = r - 0xFF10 + '0'
	case r >= 0xFF21 && r <= 0xFF3A:
		r = r - 0xFF21 + 'A'
	case r >= 0xFF41 && r <= 0xFF5A:
		r = r - 0xFF41 + 'A'
	case r >= 0x1D400 && r <= 0x1D6A3: // 13 Latin alphabets of 52 letters
		r = 'A' + (r-0x1D400)%52%26
	case r >= 0x1D6A8 && r <= 0x1D7C9: // 5 Greek alphabets of 58 characters
		r = mathGreek((r - 0x1D6A8) % 58)
	case r >= 0x1D7CE && r <= 0x1D7FF: // 5 digit sets
		r = '0' + (r-0x1D7CE)%10
	case r >= 0x2460 && r <= 0x2468: // ①–⑨
		r = r - 0x2460 + '1'
	case r >= 0x2474 && r <= 0x247C: // ⑴–⑼
		r = r - 0x2474 + '1'
	case r >= 0x2488 && r <= 0x2490: // ⒈–⒐
		r = r - 0x2488 + '1'
	case r >= 0x249C && r <= 0x24E9: // ⒜–⒵, Ⓐ–Ⓩ, ⓐ–ⓩ
		r = 'A' + (r-0x249C)%26
	case r == 0x24EA || r == 0x24FF || r == 0x1F100 || r == 0x1F10B || r == 0x1F10C: // ⓪ ⓿ and the zeros of U+1F100
		r = '0'
	case r >= 0x24F5 && r <= 0x24FD: // ⓵–⓽
		r = r - 0x24F5 + '1'
	case r >= 0x2776 && r <= 0x2793: // ❶–❾, ➀–➈, ➊–➒ (a ten is not one digit)
		if i := (r - 0x2776) % 10; i < 9 {
			r = '1' + i
		}
	case r >= 0x1F101 && r <= 0x1F10A: // digit comma 0–9
		r = r - 0x1F101 + '0'
	case r >= 0x1F110 && r <= 0x1F189: // parenthesized, squared, negative circled and negative squared A–Z
		if i := (r - 0x1F110) % 32; i < 26 {
			r = 'A' + i
		}
	case r >= 0x1F1E6 && r <= 0x1F1FF: // regional indicators
		r = r - 0x1F1E6 + 'A'
	}
	if r >= utf8.RuneSelf {
		if c, ok := fpConfusables[r]; ok {
			r = c
		} else if c, ok := fpConfusables[unicode.ToUpper(r)]; ok {
			r = c
		}
	}
	if u := unicode.ToUpper(r); (u >= '0' && u <= '9') || (u >= 'A' && u <= 'Z') {
		return u
	}
	return 0
}

// mathGreek is the Greek letter at index i of a mathematical Greek alphabet
// (Α–Ρ, ϴ, Σ–Ω, ∇, α–ω, ∂, six letter symbols), or 0 for the others.
func mathGreek(i rune) rune {
	switch {
	case i <= 24 && i != 17:
		return 0x0391 + i
	case i >= 26 && i <= 50:
		return 0x03B1 + i - 26
	}
	return 0
}

// foldFP returns the fingerprint-alphabet character r reads as (upper case),
// or 0.
func foldFP(r rune) rune {
	if f := fold(r); f != 0 && strings.ContainsRune(fpAlphabet, f) {
		return f
	}
	return 0
}

// maxGroupSeparators is how many separator runes may join two groups of 4
// (review 64 F5S-1: '.', '_', '/', '—', "--" and " - "; review 96 L1:
// " -- ", as many as maxRunSeparators).
const maxGroupSeparators = 8

// maxDigitlessChain is the longest chain of groups that is blanked only if it
// holds a digit (D66, review 65 F6S-3: "Mary & Jake" is a name).
const maxDigitlessChain = 3

// blankFingerprints is step 5 (OD-R55F5-9, review 58a H1, review 64 F5S-1,
// review 65 F6S-1, D66): a chain of two or more groups of exactly 4
// fingerprint-alphabet characters, joined by 1 to 8 separator runes (space,
// P*, S*, joiner letters), becomes "…" when it has 4 or more groups or holds
// a digit; and any run of 8 or more of them with no separator that holds
// both a digit and a letter becomes "…". The characters are matched after
// foldFP (any case, fullwidth, mathematical, enclosed and look-alike forms),
// and combining marks inside a run are part of it. A peer cannot then show
// the fingerprint of the device it impersonates inside its own name.
func blankFingerprints(rs []rune) []rune {
	// Maximal alphabet runs; n counts the alphabet characters.
	type span struct{ start, end, n int }
	var runs []span
	for i := 0; i < len(rs); {
		if foldFP(rs[i]) == 0 {
			i++
			continue
		}
		j, n := i, 0
		for j < len(rs) {
			if foldFP(rs[j]) != 0 {
				n++
			} else if !isAnyMark(rs[j]) {
				break
			}
			j++
		}
		runs = append(runs, span{i, j, n})
		i = j
	}
	joined := func(a, b span) bool {
		seps := 0
		for _, r := range rs[a.end:b.start] {
			switch {
			case isAnyMark(r):
			case isSeparator(r):
				seps++
			default:
				return false
			}
		}
		return seps >= 1 && seps <= maxGroupSeparators
	}
	blank := make([]span, 0)
	for k := 0; k < len(runs); {
		r := runs[k]
		if r.n == 4 {
			last := k
			for last+1 < len(runs) && runs[last+1].n == 4 && joined(runs[last], runs[last+1]) {
				last++
			}
			if last > k {
				chain := span{r.start, runs[last].end, 0}
				if last-k+1 > maxDigitlessChain || hasDigit(rs[chain.start:chain.end]) {
					blank = append(blank, chain)
				}
				k = last + 1
				continue
			}
		}
		if r.n >= 8 && hasDigit(rs[r.start:r.end]) && hasLetter(rs[r.start:r.end]) {
			blank = append(blank, r)
		}
		k++
	}
	if len(blank) == 0 {
		return rs
	}
	out := make([]rune, 0, len(rs))
	prev := 0
	for _, b := range blank {
		out = append(out, rs[prev:b.start]...)
		out = append(out, '…')
		prev = b.end
	}
	return append(out, rs[prev:]...)
}

// hasDigit reports a rune of rs that folds to a digit of the alphabet.
func hasDigit(rs []rune) bool {
	for _, r := range rs {
		if f := foldFP(r); f >= '0' && f <= '9' {
			return true
		}
	}
	return false
}

// hasLetter reports a rune of rs that folds to a letter of the alphabet.
func hasLetter(rs []rune) bool {
	for _, r := range rs {
		if f := foldFP(r); f >= 'A' && f <= 'Z' {
			return true
		}
	}
	return false
}

// Quote is displayQuote of Docs/protocol/approval.md §Sanitising, for text a
// human must check exactly (paths, branch, scope, label, argv, env names, the
// request title, the constraint text): a JSON string in double quotes, not
// HTML-escaped, in which every hidden rune and U+FFFD is written as \uXXXX
// (a surrogate pair above U+FFFF), and every combining mark after the 2nd on
// one base is escaped too, so the value stays exact but cannot cover its
// neighbours (review 58a M4, L1; review 40 M1). A '\' is always doubled, so
// zenity's escape decoding cannot turn the text into a NUL or a line break
// (review 58a M2).
func Quote(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	raw := strings.TrimSuffix(buf.String(), "\n")
	var b strings.Builder
	b.Grow(len(raw))
	marks := 0
	for _, r := range raw {
		escape := r == utf8.RuneError || Hidden(r)
		if isMark(r) {
			marks++
			if marks > maxMarks {
				escape = true
			}
		} else {
			marks = 0
		}
		if !escape {
			b.WriteRune(r)
			continue
		}
		if r > 0xffff {
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
		} else {
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// Escape is decision.md §Markdown's rule (moved from decision.Visible,
// R55-F10): every hidden rune becomes the visible ASCII escape \u{XXXX}
// (uppercase hex of the code point); \n and \t are kept when multiLine is
// true. U+FFFD is kept, as decision.Visible keeps it (review 82b F1).
func Escape(s string, multiLine bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if multiLine && (r == '\n' || r == '\t') {
			b.WriteRune(r)
			continue
		}
		if Hidden(r) {
			fmt.Fprintf(&b, `\u{%X}`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Term is displayTerm of Docs/protocol/approval.md §Sanitising, for peer
// text on a terminal that must stay exact (names, titles, file names,
// branches): Escape(s, false), and U+FFFD (so invalid UTF-8) and every
// combining mark (Mn, Me) after the 2nd on one base also become \u{XXXX}.
// Nothing is removed or cut; ASCII and ordinary Unicode text is unchanged,
// and Term(Term(s)) == Term(s).
func Term(s string) string {
	return term(s, false)
}

// term is Term; keepTab keeps '\t' as itself (Block's lines).
func term(s string, keepTab bool) string {
	var b strings.Builder
	b.Grow(len(s))
	marks := 0
	for _, r := range s { // invalid UTF-8 decodes as U+FFFD
		if keepTab && r == '\t' {
			marks = 0
			b.WriteRune(r)
			continue
		}
		escape := r == utf8.RuneError || Hidden(r)
		if isMark(r) {
			marks++
			if marks > maxMarks {
				escape = true
			}
		} else {
			marks = 0
		}
		if escape {
			fmt.Fprintf(&b, `\u{%X}`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Block is displayBlock of Docs/protocol/approval.md §Sanitising, for the
// few multi-line fields (a request's reason and output, a debate topic): s
// is split at "\n" (one final "\n" dropped), each line is rendered by Term
// with '\t' kept, and every line after the first is prefixed by indent, so
// a line of s cannot pose as one of the command's own field lines.
func Block(s, indent string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = term(l, true)
	}
	return strings.Join(lines, "\n"+indent)
}

// JSON rewrites encoded JSON (Docs/protocol/approval.md §Sanitising, JSON
// output) so that every hidden rune from U+007F up is written as \uXXXX
// (lowercase hex, a surrogate pair above U+FFFF). encoding/json escapes C0,
// U+2028 and U+2029 but writes U+007F, C1, bidi controls and the other
// hidden runes raw (review 82b F3). Such runes can occur only inside JSON
// strings, and every other byte is copied, so the decoded value is
// unchanged. Input without such a rune is returned as is.
func JSON(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); {
		if b[i] < 0x7F {
			if out != nil {
				out = append(out, b[i])
			}
			i++
			continue
		}
		r, n := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && n <= 1 {
			// An undecodable byte (a lone 0x9B is the 8-bit CSI) is written as
			// U+FFFD, which is what Go's decoder reads it as anyway (review 90 S90-2).
			if out == nil {
				out = make([]byte, i, len(b)+16)
				copy(out, b[:i])
			}
			out = append(out, `\ufffd`...)
			i += n
			continue
		}
		if !Hidden(r) {
			if out != nil {
				out = append(out, b[i:i+n]...)
			}
			i += n
			continue
		}
		if out == nil {
			out = make([]byte, i, len(b)+16)
			copy(out, b[:i])
		}
		if r > 0xffff {
			hi, lo := utf16.EncodeRune(r)
			out = fmt.Appendf(out, `\u%04x\u%04x`, hi, lo)
		} else {
			out = fmt.Appendf(out, `\u%04x`, r)
		}
		i += n
	}
	if out == nil {
		return b
	}
	return out
}
