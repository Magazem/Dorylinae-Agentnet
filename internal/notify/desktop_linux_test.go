//go:build linux

package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const linuxInjectionText = "quote'\" backtick` dollar$(rm -rf ~) percent%s newline\nunicode—日本語"

func TestShowDesktopLinuxGdbusArgv(t *testing.T) {
	orig := run
	defer func() { run = orig }()
	var gotName string
	var gotArgs []string
	run = func(_ context.Context, name string, args []string, _ []string) error {
		gotName, gotArgs = name, args
		return nil
	}

	if err := showDesktop(context.Background(), linuxInjectionText, "a & b < c > d"); err != nil {
		t.Fatal(err)
	}
	if gotName != "gdbus" {
		t.Fatalf("name = %q, want gdbus", gotName)
	}
	joined := strings.Join(gotArgs, "\x00")
	if strings.Contains(joined, "\x00"+linuxInjectionText+"\x00") {
		t.Error("title appears as a bare argv element: it must be GVariant-quoted")
	}
	wantTitle := gvariantString(linuxInjectionText)
	found := false
	for _, a := range gotArgs {
		if a == wantTitle {
			found = true
		}
	}
	if !found {
		t.Errorf("did not find the expected GVariant-quoted title %q in args %v", wantTitle, gotArgs)
	}
	// The body must have &, < and > escaped for markup.
	wantBody := gvariantString("a &amp; b &lt; c &gt; d")
	found = false
	for _, a := range gotArgs {
		if a == wantBody {
			found = true
		}
	}
	if !found {
		t.Errorf("did not find the expected escaped, GVariant-quoted body %q in args %v", wantBody, gotArgs)
	}
}

func TestShowDesktopLinuxFallsBackToNotifySend(t *testing.T) {
	orig := run
	defer func() { run = orig }()
	var calls []string
	run = func(_ context.Context, name string, _ []string, _ []string) error {
		calls = append(calls, name)
		if name == "gdbus" {
			return errors.New("no session bus")
		}
		return nil
	}
	if err := showDesktop(context.Background(), "t", "b"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "gdbus" || calls[1] != "notify-send" {
		t.Errorf("calls = %v, want [gdbus notify-send]", calls)
	}
}

// TestGvariantStringRoundTrip checks that titles containing ', \, " and a
// GVariant type-annotation prefix (@s) are escaped so the single-quoted
// literal cannot be broken out of or re-parsed as GVariant syntax
// (Docs/review/11-phase1-tickets.md 1.8a acceptance).
func TestGvariantStringRoundTrip(t *testing.T) {
	cases := []string{
		`plain`,
		`it's`,
		`back\slash`,
		`quo"te`,
		`@s annotation`,
		`'@s' \'` + "\n" + `multi`,
	}
	for _, in := range cases {
		out := gvariantString(in)
		if out[0] != '\'' || out[len(out)-1] != '\'' {
			t.Fatalf("gvariantString(%q) = %q, not single-quoted", in, out)
		}
		inner := out[1 : len(out)-1]
		// Un-escape exactly as GVariant text format would: \\ -> \, \' -> '.
		var b strings.Builder
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\\' && i+1 < len(inner) {
				i++
				b.WriteByte(inner[i])
				continue
			}
			b.WriteByte(inner[i])
		}
		if b.String() != in {
			t.Errorf("gvariantString(%q) = %q, round-trip got %q", in, out, b.String())
		}
		// No unescaped single quote may appear inside the literal (it would
		// terminate the string early when re-parsed).
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\'' {
				t.Errorf("gvariantString(%q) has an unescaped quote in %q", in, inner)
			}
			if inner[i] == '\\' {
				i++
			}
		}
	}
}

func TestEscapeMarkup(t *testing.T) {
	got := escapeMarkup("a & b < c > d")
	want := "a &amp; b &lt; c &gt; d"
	if got != want {
		t.Errorf("escapeMarkup = %q, want %q", got, want)
	}
}
