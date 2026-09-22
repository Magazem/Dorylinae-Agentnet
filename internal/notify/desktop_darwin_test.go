//go:build darwin

package notify

import (
	"context"
	"strings"
	"testing"
)

// injectionText carries quotes, backticks, $(), % and a newline: exactly the
// shell/script metacharacters the per-OS mechanism must never interpret
// (Docs/review/11-phase1-tickets.md 1.8a acceptance).
const injectionText = "quote'\" backtick` dollar$(rm -rf ~) percent%s newline\nunicode—日本語"

func TestShowDesktopDarwinArgv(t *testing.T) {
	orig := run
	defer func() { run = orig }()
	var gotName string
	var gotArgs, gotEnv []string
	run = func(_ context.Context, name string, args []string, env []string) error {
		gotName, gotArgs, gotEnv = name, args, env
		return nil
	}

	if err := showDesktop(context.Background(), injectionText, injectionText); err != nil {
		t.Fatal(err)
	}
	if gotName != "/usr/bin/osascript" {
		t.Errorf("name = %q, want osascript", gotName)
	}
	if gotEnv != nil {
		t.Errorf("env = %v, want nil (title/body go as argv)", gotEnv)
	}
	// The fixed script source (the three -e arguments) must never contain
	// the peer text.
	for i, a := range gotArgs {
		if a == "-e" {
			continue
		}
		if i < len(gotArgs)-2 && strings.Contains(a, injectionText) {
			t.Errorf("peer text found in script source arg %d: %q", i, a)
		}
	}
	if len(gotArgs) < 2 {
		t.Fatalf("too few args: %v", gotArgs)
	}
	title, body := gotArgs[len(gotArgs)-2], gotArgs[len(gotArgs)-1]
	if title != injectionText || body != injectionText {
		t.Errorf("title/body were not passed as literal argv elements: title=%q body=%q", title, body)
	}
	if gotArgs[len(gotArgs)-3] != "--" {
		t.Errorf("expected -- immediately before title/body, got %q", gotArgs[len(gotArgs)-3])
	}
}
