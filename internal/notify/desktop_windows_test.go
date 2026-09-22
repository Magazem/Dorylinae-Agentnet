//go:build windows

package notify

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

const windowsInjectionText = "quote'\" backtick` dollar$(rm -rf ~) percent%s newline\nunicode—日本語"

func TestShowDesktopWindowsEnvNotArgv(t *testing.T) {
	orig := run
	defer func() { run = orig }()
	var gotName string
	var gotArgs, gotEnv []string
	run = func(_ context.Context, name string, args []string, env []string) error {
		gotName, gotArgs, gotEnv = name, args, env
		return nil
	}

	if err := showDesktop(context.Background(), windowsInjectionText, windowsInjectionText); err != nil {
		t.Fatal(err)
	}
	if gotName != "powershell.exe" {
		t.Fatalf("name = %q, want powershell.exe", gotName)
	}
	for _, a := range gotArgs {
		if strings.Contains(a, windowsInjectionText) {
			t.Errorf("peer text found on the command line: %q", a)
		}
	}
	if len(gotEnv) != 2 {
		t.Fatalf("env = %v, want 2 entries", gotEnv)
	}
	titleVal := decodeEnv(t, gotEnv, "AGENTNET_N_TITLE")
	bodyVal := decodeEnv(t, gotEnv, "AGENTNET_N_BODY")
	if titleVal != windowsInjectionText {
		t.Errorf("decoded title = %q, want %q", titleVal, windowsInjectionText)
	}
	if bodyVal != windowsInjectionText {
		t.Errorf("decoded body = %q, want %q", bodyVal, windowsInjectionText)
	}
}

func decodeEnv(t *testing.T, env []string, key string) string {
	t.Helper()
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			raw, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				t.Fatalf("%s: bad base64: %v", key, err)
			}
			if len(raw)%2 != 0 {
				t.Fatalf("%s: odd byte length for UTF-16LE", key)
			}
			units := make([]uint16, len(raw)/2)
			for i := range units {
				units[i] = binary.LittleEndian.Uint16(raw[i*2:])
			}
			return string(utf16.Decode(units))
		}
	}
	t.Fatalf("env var %s not found in %v", key, env)
	return ""
}

func TestWindowsScriptIsFixed(t *testing.T) {
	if strings.Contains(windowsToastScript, windowsInjectionText) {
		t.Error("the script source must never contain peer text")
	}
	if !strings.Contains(windowsToastScript, "AGENTNET_N_TITLE") || !strings.Contains(windowsToastScript, "AGENTNET_N_BODY") {
		t.Error("the script must read title/body from the environment")
	}
	if !strings.Contains(windowsToastScript, "SecurityElement") {
		t.Error("the script must XML-escape the decoded text")
	}
}
