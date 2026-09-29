//go:build darwin

package notify

import (
	"bytes"
	"encoding/base64"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// R55-F6 (review 55 R55-203): osascript reads non-ASCII approval text from
// the environment exactly, through the same envText handler and the same
// environment entries as the window and the code notification. It runs the
// real /usr/bin/osascript without any dialog.
func TestOsascriptReadsNonASCIIExactly(t *testing.T) {
	const want = `debate_constraint: peer "Zoë 東京 Ωmega \" & <b> é" snake_case \0 ✓ 😀`
	// The script prints what envText read as base64 of its UTF-8 bytes, so
	// osascript's own output encoding cannot change the result.
	script := osaScriptHeader + osaEnvTextHandler + `
on run
	set s to current application's NSString's stringWithString:(my envText("AGENTNET_W_SUMMARY"))
	return ((s's dataUsingEncoding:(current application's NSUTF8StringEncoding))'s base64EncodedStringWithOptions:0) as text
end run
`
	cmd := exec.Command("/usr/bin/osascript", "-e", script)
	env := dialogEnvDarwin("ab12", "k", want, 30)
	cmd.Env = append(cmd.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("osascript: %v: %s", err, errb.String())
	}
	got, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("osascript output %q: %v", out.String(), err)
	}
	if string(got) != want {
		t.Fatalf("osascript read %q, want %q", got, want)
	}
	for _, e := range env[:3] {
		for _, r := range e {
			if r > 0x7e {
				t.Fatalf("environment entry %q is not ASCII", e)
			}
		}
	}
}

// R55-F6: a value that is not base64 makes envText fail, so osascript exits
// non-zero (an early failure is "not ready", never an answer).
func TestOsascriptRefusesBadEnvText(t *testing.T) {
	script := osaScriptHeader + osaEnvTextHandler + `
on run
	return my envText("AGENTNET_W_SUMMARY")
end run
`
	cmd := exec.Command("/usr/bin/osascript", "-e", script)
	cmd.Env = append(cmd.Environ(), "AGENTNET_W_SUMMARY=Zoë*not base64*")
	if err := cmd.Run(); err == nil {
		t.Fatal("osascript accepted a value that is not base64")
	}
}

// R55-F6: the dialog and code-notification scripts compile (osacompile runs
// nothing and shows no dialog).
func TestDarwinScriptsCompile(t *testing.T) {
	for name, script := range map[string]string{"dialog": approvalDialogScript, "notification": approvalScript} {
		out := filepath.Join(t.TempDir(), name+".scpt")
		if b, err := exec.Command("/usr/bin/osacompile", "-o", out, "-e", script).CombinedOutput(); err != nil { //nolint:gosec // fixed path, fixed scripts, test temp dir
			t.Errorf("%s script does not compile: %v: %s", name, err, b)
		}
	}
}
