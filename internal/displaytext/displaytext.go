// Package displaytext holds the one character rule of approval summaries
// (Docs/protocol/approval.md §Sanitising: one character rule, two
// renderings, R55-F5): the hidden predicate shared by the approval-summary
// builder, device.DisplayQuote and decision.Visible, the two renderings Name
// and Quote, and the display-safe check the approval store and window apply.
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
	// Steps 1-3: hidden runes, spaces, stacked marks.
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
	rs = blankDigitRuns(rs)
	rs = blankFingerprints(rs)
	out := strings.TrimSpace(string(rs))
	if out == "" {
		return "(no name)"
	}
	return out
}

func isNumber(r rune) bool { return unicode.Is(unicode.N, r) }

func isSeparator(r rune) bool {
	return r == ' ' || unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// minDigitRun is the length from which a run of numbers reads as a code.
const minDigitRun = 6

// maxRunSeparators is how many separator runes may stand between two numbers
// of one run (review 58a L3: "4 - 8 - 2 - 9 - 1 - 3").
const maxRunSeparators = 3

// blankDigitRuns is step 4: a maximal run of numbers (category N), with up to
// 3 separator runes between two of them and the combining marks right after
// each, is replaced by "…" when it holds 6 or more numbers.
func blankDigitRuns(rs []rune) []rune {
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); {
		if !isNumber(rs[i]) {
			out = append(out, rs[i])
			i++
			continue
		}
		count, end := 0, i
		j := i
		for j < len(rs) {
			if !isNumber(rs[j]) {
				break
			}
			count++
			j++
			for j < len(rs) && isMark(rs[j]) {
				j++
			}
			end = j
			k := j
			for k < len(rs) && k-j < maxRunSeparators && isSeparator(rs[k]) && !isNumber(rs[k]) {
				k++
			}
			if k < len(rs) && isNumber(rs[k]) {
				j = k
				continue
			}
			break
		}
		if count >= minDigitRun {
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

func isFPRune(r rune) bool {
	u := unicode.ToUpper(r)
	return u < utf8.RuneSelf && strings.ContainsRune(fpAlphabet, u)
}

// blankFingerprints is step 5 (OD-R55F5-9, review 58a H1): two or more groups
// of exactly 4 fingerprint-alphabet characters joined by single spaces or
// '-', and any run of 8 or more of them with no separator that holds both a
// digit and a letter, become "…". A peer cannot then show the fingerprint of
// the device it impersonates inside its own name.
func blankFingerprints(rs []rune) []rune {
	// Maximal alphabet runs.
	type span struct{ start, end int }
	var runs []span
	for i := 0; i < len(rs); {
		if !isFPRune(rs[i]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && isFPRune(rs[j]) {
			j++
		}
		runs = append(runs, span{i, j})
		i = j
	}
	blank := make([]span, 0)
	for k := 0; k < len(runs); {
		r := runs[k]
		if r.end-r.start == 4 {
			// Chain groups of 4 joined by exactly one ' ' or '-'.
			last := k
			for last+1 < len(runs) {
				nx := runs[last+1]
				gap := nx.start - runs[last].end
				if gap != 1 || (rs[runs[last].end] != ' ' && rs[runs[last].end] != '-') || nx.end-nx.start != 4 {
					break
				}
				last++
			}
			if last > k {
				blank = append(blank, span{r.start, runs[last].end})
				k = last + 1
				continue
			}
		}
		if r.end-r.start >= 8 && hasDigitAndLetter(rs[r.start:r.end]) {
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

func hasDigitAndLetter(rs []rune) bool {
	var d, l bool
	for _, r := range rs {
		if r >= '0' && r <= '9' {
			d = true
		} else {
			l = true
		}
	}
	return d && l
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
