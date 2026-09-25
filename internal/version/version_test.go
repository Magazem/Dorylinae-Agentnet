package version

import (
	"bytes"
	"encoding/json"
	"runtime/debug"
	"strings"
	"testing"
)

func TestMain_Flags(t *testing.T) {
	tests := []struct {
		args     []string
		wantCode int
		wantOut  string
	}{
		{[]string{"--version"}, 0, "demo " + Version},
		{[]string{"--help"}, 0, "Usage:"},
		{[]string{"-h"}, 0, "Usage:"},
		{nil, 0, "demo " + Version},
		{[]string{"--bogus"}, 2, ""},
	}
	for _, tc := range tests {
		var out, errb bytes.Buffer
		code := Main("demo", "Demo binary.", tc.args, &out, &errb)
		if code != tc.wantCode {
			t.Errorf("%v: code = %d, want %d", tc.args, code, tc.wantCode)
		}
		if !strings.Contains(out.String(), tc.wantOut) {
			t.Errorf("%v: stdout = %q, want substring %q", tc.args, out.String(), tc.wantOut)
		}
	}
}

func TestDevVersion(t *testing.T) {
	tests := []struct {
		name     string
		settings []debug.BuildSetting
		ok       bool
		want     string
	}{
		{"clean", []debug.BuildSetting{
			{Key: "vcs.revision", Value: "a1b2c3d4e5f6"},
			{Key: "vcs.time", Value: "2026-09-25T10:00:00Z"},
		}, true, "0.0.0-dev+a1b2c3d (2026-09-25)"},
		{"dirty", []debug.BuildSetting{
			{Key: "vcs.revision", Value: "a1b2c3d4e5f6"},
			{Key: "vcs.time", Value: "2026-09-25T10:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
		}, true, "0.0.0-dev+a1b2c3d (2026-09-25, dirty)"},
		{"no vcs settings", nil, false, ""},
	}
	for _, tc := range tests {
		got, ok := devVersion(&debug.BuildInfo{Settings: tc.settings}, true)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: devVersion() = (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := devVersion(nil, false); ok {
		t.Fatal("no build info should report ok=false")
	}
}

func TestCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Command("demo", nil, &out, &errb); code != 0 || out.String() != String("demo")+"\n" {
		t.Fatalf("code=%d out=%q", code, out.String())
	}

	out.Reset()
	if code := Command("demo", []string{"--json"}, &out, &errb); code != 0 {
		t.Fatalf("--json: code = %d, stderr = %q", code, errb.String())
	}
	var body struct {
		OK      bool   `json:"ok"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out.String(), err)
	}
	if !body.OK || body.Name != "demo" || body.Version != Version {
		t.Fatalf("body = %+v", body)
	}

	out.Reset()
	errb.Reset()
	if code := Command("demo", []string{"extra"}, &out, &errb); code != 2 {
		t.Fatalf("extra arg: code = %d", code)
	}

	out.Reset()
	if code := Command("demo", []string{"--help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("--help: code=%d out=%q", code, out.String())
	}
}
