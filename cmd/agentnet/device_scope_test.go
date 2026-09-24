package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestDeviceScopeUsageAndArgumentErrors(t *testing.T) {
	shortHome(t)
	var out, errb bytes.Buffer
	if code := run([]string{"device", "scope", "--help"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "agentnet device scope") {
		t.Fatalf("--help: code %d, stdout %q", code, out.String())
	}
	for _, args := range [][]string{
		{"device", "scope"},
		{"device", "scope", "@c"},
		{"device", "scope", "@c", "@d", "--show"},
		{"device", "scope", "@c", "--show", "--clear"},
		{"device", "scope", "@c", "--clear", "--types", "task"},
		{"device", "scope", "@c", "--types", "task"},
		{"device", "scope", "@c", "--types", "task", "--repo", "r=/x", "--command", "t=r:[\"go\"]"},
		{"device", "scope", "@c", "--types", "task", "--repo", "r", "--command", "t=r:[\"go\"]", "--expires", "1d"},
		{"device", "scope", "@c", "--types", "task", "--repo", "r=/x", "--command", "t=r:go", "--expires", "1d"},
		{"device", "scope", "@c", "--types", "task", "--repo", "r=/x", "--command", "t=r:[\"go\"]", "--expires", "soon"},
		{"device", "scope", "@c", "--types", "task", "--repo", "r=/x", "--command", "t=r:[\"go\"]", "--expires", "1d", "--timeout", "u=5"},
		{"device", "scope", "@c", "--from-file", "/no/such/scope.json"},
	} {
		out.Reset()
		errb.Reset()
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: code %d, want %d (stderr %q)", args, code, exitUsage, errb.String())
		}
	}
	if code := run([]string{"device", "scope", "@c", "--show"}, &out, &errb); code != exitDaemonNotFound {
		t.Errorf("--show without a daemon: code %d, want %d", code, exitDaemonNotFound)
	}
}

func TestBuildScopeFromFlags(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	sc, err := buildScope("task, review", []string{"agentnet=/src/agentnet"},
		[]string{`test=agentnet:["go","test","./..."]`, `lint=agentnet:["golangci-lint","run"]`},
		[]string{"test=1200"}, []string{"test=GOFLAGS"}, "7d", now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sc.Types, ",") != "task,review" || len(sc.Repos) != 1 || sc.Repos[0].Path != "/src/agentnet" {
		t.Fatalf("scope = %+v", sc)
	}
	if c := sc.Commands[0]; c.Name != "test" || c.TimeoutS != 1200 || strings.Join(c.Argv, " ") != "go test ./..." || len(c.Env) != 1 {
		t.Fatalf("command 0 = %+v", c)
	}
	if c := sc.Commands[1]; c.TimeoutS != defaultTimeoutS || len(c.Env) != 0 {
		t.Fatalf("command 1 = %+v", c)
	}
	if sc.Expires != "2026-10-08T09:00:00Z" {
		t.Fatalf("expires = %s", sc.Expires)
	}
	for _, v := range []string{"90m", "12h", "2026-10-02T00:00:00Z"} {
		if _, err := parseExpires(v, now); err != nil {
			t.Errorf("--expires %s: %v", v, err)
		}
	}
	for _, v := range []string{"", "0d", "-1h", "tomorrow"} {
		if _, err := parseExpires(v, now); err == nil {
			t.Errorf("--expires %q accepted", v)
		}
	}
}
