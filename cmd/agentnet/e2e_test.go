package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func build(t *testing.T, pkg, out string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, pkg) //nolint:gosec // test builds fixed packages
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, b)
	}
	return out
}

// TestE2E builds both binaries, starts agentnetd as a subprocess and checks
// `agentnet status --json` against it, then against a stopped daemon.
func TestE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in -short mode")
	}
	home, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	bin := t.TempDir()
	cli := build(t, "dorylinae/cmd/agentnet", filepath.Join(bin, "agentnet"))
	dmn := build(t, "dorylinae/cmd/agentnetd", filepath.Join(bin, "agentnetd"))
	env := append(os.Environ(), "DORYLINAE_HOME="+home)

	status := func() (int, []byte, time.Duration) {
		c := exec.Command(cli, "status", "--json")
		c.Env = env
		var out bytes.Buffer
		c.Stdout = &out
		start := time.Now()
		err := c.Run()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return code, out.Bytes(), time.Since(start)
	}

	if code, _, _ := status(); code != 3 {
		t.Fatalf("status before start: exit %d, want 3", code)
	}

	d := exec.Command(dmn) //nolint:gosec // binary we just built
	d.Env = env
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Process.Kill(); _, _ = d.Process.Wait() })

	deadline := time.Now().Add(15 * time.Second)
	for {
		code, out, took := status()
		if code == 0 {
			var body struct {
				OK            bool    `json:"ok"`
				PID           int     `json:"pid"`
				UptimeSeconds float64 `json:"uptime_seconds"`
			}
			if err := json.Unmarshal(out, &body); err != nil {
				t.Fatalf("bad JSON %q: %v", out, err)
			}
			if !body.OK || body.PID != d.Process.Pid {
				t.Fatalf("body = %+v, want pid %d", body, d.Process.Pid)
			}
			if took > 2*time.Second {
				t.Fatalf("status took %v", took)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never became ready; last exit %d out %q", code, out)
		}
		time.Sleep(100 * time.Millisecond)
	}

	_ = d.Process.Kill()
	_, _ = d.Process.Wait()
	if runtime.GOOS != "windows" {
		// A killed Unix daemon leaves a stale socket; status must still say "not running".
		if code, _, _ := status(); code != 3 {
			t.Fatalf("status after kill: exit %d, want 3", code)
		}
	}
}
