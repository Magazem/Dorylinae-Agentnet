package notify

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// This file builds the text arguments of the Linux approval window
// (window_linux.go). It has no build tag so its tests run on every OS
// (R55-F6).

// errDialogArg refuses a window value that could cut the text or add a line
// in the dialog: invalid UTF-8, a NUL or any other C0/C1 control (newline
// included), U+2028 or U+2029. windowText never produces one; this is
// defence in depth (R55-F6).
var errDialogArg = errors.New("notify: approval window text is not one line of valid UTF-8")

// dialogArgSafe reports whether s can be passed to a dialog as is.
func dialogArgSafe(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 {
			return false
		}
	}
	return true
}

// zenityText escapes s for zenity --entry --text. zenity passes that value
// through GLib's g_strcompress, which decodes '\' escapes (octal, \n, \t …)
// and cuts the text at a decoded NUL, and then sets it as a GTK mnemonic
// label, where '_' marks the next character and "__" is one '_'
// (review 55 R55-025 / T11-01; zenity 3.42.1 and 4.0.1 src/entry.c). The
// label is not markup, so '&', '<' and '>' are left alone. Every '_' is
// doubled first, then every '\', so that the window shows exactly s.
func zenityText(s string) (string, error) {
	if !dialogArgSafe(s) {
		return "", errDialogArg
	}
	s = strings.ReplaceAll(s, "_", "__")
	return strings.ReplaceAll(s, `\`, `\\`), nil
}

// kdialogText builds s for kdialog --inputbox (R55-F6). kdialog decodes
// "\\" and "\n" in that value (Utils::parseString) and puts the result in a
// QInputDialog label, a QLabel with a buddy: its format is guessed
// (Qt::mightBeRichText) and every '&' marks a mnemonic, "&&" being one '&'.
// So the text is always rich text, forced by the <qt> tag, with & < >
// written as entities, each '&' doubled for the mnemonic pass, spaces kept
// (white-space:pre-wrap), and every '\' doubled for parseString.
func kdialogText(s string) (string, error) {
	if !dialogArgSafe(s) {
		return "", errDialogArg
	}
	var b strings.Builder
	b.WriteString(`<qt><p style="white-space:pre-wrap">`)
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(`</p></qt>`)
	return b.String(), nil
}
