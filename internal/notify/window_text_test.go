package notify

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const whereTheCodeIs = " The code is in the AgentNet notification for a-012345. If notifications are silenced (Do Not Disturb, Focus Assist), open the notification centre."

// R55-F5 A2: the window shows the summary exactly as built, even at the
// 4096-code-point limit and with double spaces inside a quoted argv, followed
// only by the fixed sentence about where the code is (review 29 L5).
func TestWindowTextShowsSummaryAsBuilt(t *testing.T) {
	head := `Let peer X run 1 command(s) (t) on this device: [t] runs ["a  b",  "c"]`
	sum := head + strings.Repeat("z", MaxWindowSummary-utf8.RuneCountInString(head))
	if n := utf8.RuneCountInString(sum); n != MaxWindowSummary {
		t.Fatalf("test summary is %d code points", n)
	}
	tag, kind, got, err := windowText("a-012345", "device_scope", sum, "")
	if err != nil {
		t.Fatalf("windowText: %v", err)
	}
	if got != sum+whereTheCodeIs {
		t.Fatalf("summary was altered: prefix %q", got[:len(head)])
	}
	if tag != "a-012345" || kind != "device_scope" {
		t.Fatalf("tag %q kind %q", tag, kind)
	}
	// The daemon's reopen note comes after the summary, before the sentence.
	_, _, got, err = windowText("a-012345", "grant", "Grant x.", "Wrong code, 2 attempts left")
	if err != nil || got != "Grant x. Wrong code, 2 attempts left"+whereTheCodeIs {
		t.Fatalf("with note: %q, %v", got, err)
	}
}

// R55-F5 A2: a summary one code point over the limit, or one that is not
// display-safe, makes the window fail to open (not ready, so
// approval_unavailable) instead of being cut or cleaned (C14-02).
func TestWindowRefusesLongOrUnsafeSummary(t *testing.T) {
	for name, sum := range map[string]string{
		"4097 code points": strings.Repeat("é", MaxWindowSummary+1),
		"newline":          "Grant x\nApproved by IT",
		"NUL":              "Grant x\x00",
		"bidi":             "Grant \u202ex",
		"zero-width":       "Code 4\u200b8",
		"invalid UTF-8":    "Grant \xff",
	} {
		if _, _, _, err := windowText("a-012345", "grant", sum, ""); err == nil {
			t.Errorf("%s: windowText accepted it", name)
		}
		h, err := (ApprovalWindow{}).Start(context.Background(), "a-0123456789abcdef0123456789abcdef", "a-012345", "grant", sum, "", time.Now().Add(time.Minute))
		if err == nil {
			h.Kill()
			t.Errorf("%s: Start opened a window", name)
		}
	}
}

// review 30 L1: tag, kind and the note never carry a control character to a
// dialog's argv, environment or script.
func TestWindowTextCleansTagKindNote(t *testing.T) {
	tag, kind, sum, err := windowText("a-012345\n", "grant\x00", "Grant x.", "retry\x00\nnow")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{tag, kind, sum} {
		if strings.ContainsAny(v, "\x00\n\r") {
			t.Fatalf("control character survived: %q", v)
		}
	}
	if kind != "grant" {
		t.Fatalf("kind = %q", kind)
	}
}
