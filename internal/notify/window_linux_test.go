//go:build linux

package notify

import (
	"strings"
	"testing"
)

// review 30 M1/M2: every exit yields an answer, and only a real OK or
// Reject press counts as "already exited with a valid answer".
func TestDecodeExit(t *testing.T) {
	cases := []struct {
		zenity    bool
		code      int
		line      string
		wantKind  string
		wantValid bool
	}{
		{true, 0, "123456", "approve", true},
		{true, 1, "Reject", "reject", true},
		{true, 1, "", "dismiss", false}, // Cancel, or GTK could not open the display
		{true, 5, "", "dismiss", false}, // timeout
		{true, -1, "", "dismiss", false},
		{true, 0, strings.Repeat("9", maxAnswerLine+1), "dismiss", false},
		{false, 0, "123456", "approve", true},
		{false, 1, "", "dismiss", false},
		{false, 0, strings.Repeat("9", maxAnswerLine+1), "dismiss", false},
	}
	for _, c := range cases {
		got, valid := decodeExit(c.zenity, c.code, c.line)
		if got.kind != c.wantKind || valid != c.wantValid {
			t.Errorf("decodeExit(%v, %d, %.10q) = %s/%v, want %s/%v", c.zenity, c.code, c.line, got.kind, valid, c.wantKind, c.wantValid)
		}
	}
}

// R55-F6 acceptance (inverts review 55 zz_review55_T11-01_linux_test.go):
// the real zenity and kdialog argv show the whole window text, a peer named
// Bob\0 included, and every value is one "--opt=value" argument.
func TestDialogArgsShowWholeText(t *testing.T) {
	_, kind, s, err := windowText("ab12", "debate_constraint", `add a human constraint to the debate s-1 with Bob\0 and snake_case: "x"`, "")
	if err != nil {
		t.Fatal(err)
	}
	want := kind + ": " + s
	zargs, err := zenityArgs("ab12", kind, s, 30)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, a := range zargs {
		if !strings.HasPrefix(a, "--") {
			t.Fatalf("positional argument %q", a)
		}
		if v, ok := strings.CutPrefix(a, "--text="); ok {
			text = v
		}
	}
	if got := zenityShows(text); got != want {
		t.Fatalf("zenity shows %q, want %q", got, want)
	}
	if zargs[0] != "--title=AgentNet approval ab12" || zargs[len(zargs)-1] != "--timeout=30" {
		t.Fatalf("zenity argv = %q", zargs)
	}
	kargs, err := kdialogArgs("ab12", kind, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(kargs) != 2 || kargs[0] != "--title=AgentNet approval ab12" {
		t.Fatalf("kdialog argv = %q", kargs)
	}
	if got := kdialogShows(t, strings.TrimPrefix(kargs[1], "--inputbox=")); got != want {
		t.Fatalf("kdialog shows %q, want %q", got, want)
	}
}

// R55-F6: a value with a line break or invalid UTF-8 makes the argv builder
// fail, so the window is not ready (defence in depth).
func TestDialogArgsRefuseUnsafeValues(t *testing.T) {
	for _, c := range [][3]string{{"a\nb", "k", "s"}, {"t", "k\x00", "s"}, {"t", "k", "s\xff"}} {
		if _, err := zenityArgs(c[0], c[1], c[2], 1); err == nil {
			t.Errorf("zenityArgs(%q) accepted", c)
		}
		if _, err := kdialogArgs(c[0], c[1], c[2]); err == nil {
			t.Errorf("kdialogArgs(%q) accepted", c)
		}
	}
}
