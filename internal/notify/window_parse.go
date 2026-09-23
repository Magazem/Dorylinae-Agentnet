//go:build windows || darwin

package notify

import "strings"

// parseAnswerLine decodes one line of dialog stdout
// (Docs/protocol/approval.md §The approval window, "Answer format").
// Anything that is not exactly "reject" or "approve <value>" counts as
// dismiss. Windows and macOS scripts print this line themselves; Linux maps
// zenity/kdialog's exit status instead (window_linux.go, mapZenityExit).
func parseAnswerLine(line string) dialogAnswer {
	line = strings.TrimRight(line, "\r\n")
	switch {
	case len(line) > maxAnswerLine:
		return dialogAnswer{kind: "dismiss"}
	case line == "reject":
		return dialogAnswer{kind: "reject"}
	case strings.HasPrefix(line, "approve "):
		return dialogAnswer{kind: "approve", code: strings.TrimPrefix(line, "approve ")}
	default:
		return dialogAnswer{kind: "dismiss"}
	}
}
