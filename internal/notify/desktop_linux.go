//go:build linux

package notify

import (
	"context"
	"strings"
)

// showDesktop calls org.freedesktop.Notifications.Notify on the session bus
// via gdbus, falling back to notify-send if gdbus fails
// (Docs/protocol/notify.md §Desktop). Arguments go as argv, never through a
// shell. title and body are encoded as GVariant string literals the daemon
// builds itself, so a title such as 'x' cannot be re-parsed as a second
// argument. The freedesktop notification body may be interpreted as markup,
// so &, < and > in body are escaped.
func showDesktop(ctx context.Context, title, body string) error {
	escBody := escapeMarkup(body)
	args := []string{
		"call", "--session",
		"--dest", "org.freedesktop.Notifications",
		"--object-path", "/org/freedesktop/Notifications",
		"--method", "org.freedesktop.Notifications.Notify",
		"agentnet", "0", "''",
		gvariantString(title), gvariantString(escBody),
		"[]", "{}", "5000",
	}
	if err := run(ctx, "gdbus", args, nil); err == nil {
		return nil
	}
	return run(ctx, "notify-send", []string{"-a", "agentnet", "--", title, notifySendBody(escBody)}, nil)
}

// notifySendBody doubles every backslash in the already markup-escaped body.
// notify-send passes its body argument through g_strcompress, which turns
// C escapes such as \074 into '<' after escapeMarkup ran; a body of
// `\074a href=...\076` would otherwise become live markup. Doubled, each
// backslash compresses back to one literal backslash.
func notifySendBody(escBody string) string {
	return strings.ReplaceAll(escBody, `\`, `\\`)
}

// gvariantString encodes s as a single-quoted GVariant text-format string
// literal: backslash and single quote are escaped, every other byte
// (including '"', '@' and newlines) is passed through unchanged inside the
// quotes, so it round-trips as literal text and is never re-parsed as
// GVariant syntax (Docs/protocol/notify.md §Desktop).
func gvariantString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// escapeMarkup escapes &, < and > for a freedesktop notification body, which
// may be interpreted as Pango markup (Docs/protocol/notify.md §Desktop).
func escapeMarkup(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
