package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

var (
	relayHelperOnce sync.Once
	relayHelperPath string
	relayHelperErr  error
)

// relayHelper builds this package's own binary once per test run, so a kill
// -9 test has a real OS process to kill, not a goroutine sharing the test's
// memory and file descriptors.
func relayHelper(t *testing.T) string {
	t.Helper()
	relayHelperOnce.Do(func() {
		dir, err := testutil.MkdirPrivate("dn-relay-helper-")
		if err != nil {
			relayHelperErr = err
			return
		}
		relayHelperPath = filepath.Join(dir, "relayhelper")
		if runtime.GOOS == "windows" {
			relayHelperPath += ".exe"
		}
		out, err := exec.Command("go", "build", "-o", relayHelperPath, ".").CombinedOutput() //nolint:gosec // fixed test package
		if err != nil {
			relayHelperErr = &relayBuildError{out: string(out), err: err}
		}
	})
	if relayHelperErr != nil {
		t.Fatalf("build relay helper: %v", relayHelperErr)
	}
	return relayHelperPath
}

type relayBuildError struct {
	out string
	err error
}

func (e *relayBuildError) Error() string { return e.err.Error() + "\n" + e.out }

// TestKillMidTrafficLosesNoQueuedEnvelope is the ticket's plan-4.1 acceptance
// item: kill -9 (Process.Kill, which sends SIGKILL on Unix and a forceful
// TerminateProcess on Windows) of the relay binary mid-traffic loses no
// envelope already queued to disk.
func TestKillMidTrafficLosesNoQueuedEnvelope(t *testing.T) {
	bin := relayHelper(t)
	dir := testutil.TempDir(t)
	db := filepath.Join(dir, "kill.db")

	addr, cmd := startRelaySubprocess(t, bin, db)
	pubA, privA, _ := ed25519.GenerateKey(rand.Reader)
	pubB, _, _ := ed25519.GenerateKey(rand.Reader)
	keyA, keyB := envelope.KeyString(pubA), envelope.KeyString(pubB)

	url := "ws://" + addr + envelope.ConnectPath
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil) //nolint:bodyclose // successful dial has no body
	if err != nil {
		t.Fatal(err)
	}
	var ctl envelope.Control
	_, raw, err := c.Read(ctx)
	if err != nil || json.Unmarshal(raw, &ctl) != nil || ctl.Op != envelope.OpChallenge {
		t.Fatalf("challenge: %s, %v", raw, err)
	}
	nonce, err := envelope.DecodeNonce(ctl.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	a, err := envelope.SignAuth(pubA, nonce, relayclient.NewKeySigner(privA).Sign)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(a)
	if err := c.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	if _, raw, err = c.Read(ctx); err != nil || !strings.Contains(string(raw), `"op":"ready"`) {
		t.Fatalf("ready: %s, %v", raw, err)
	}

	want := []string{"kill-1", "kill-2", "kill-3"}
	for _, id := range want {
		e := envelope.Envelope{From: keyA, To: keyB, Team: "t", Type: "ping", ID: id, TS: time.Now().UTC().Format(time.RFC3339Nano), Payload: []byte(`"x"`)}
		frame, err := e.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Fatal(err)
		}
		if _, raw, err := c.Read(ctx); err != nil || !strings.Contains(string(raw), `"op":"queued"`) {
			t.Fatalf("queued ack for %s: %s, %v", id, raw, err)
		}
	}
	_ = c.CloseNow()

	if err := cmd.Process.Kill(); err != nil { // SIGKILL on Unix; forceful terminate on Windows
		t.Fatalf("kill relay subprocess: %v", err)
	}
	_ = cmd.Wait()

	// The queue file on disk must still hold every envelope: no clean
	// shutdown ran, so this is exactly what a host power-loss leaves behind.
	qdb, err := sql.Open("sqlite", "file:"+db+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = qdb.Close() }()
	var n int
	if err := qdb.QueryRow(`SELECT COUNT(*) FROM queue WHERE to_key = ?`, keyB).Scan(&n); err != nil || n != len(want) {
		t.Fatalf("rows for B after kill -9 = %d, %v; want %d", n, err, len(want))
	}
}

// startRelaySubprocess launches the relay binary as a real OS process
// listening on an ephemeral loopback port and returns its address once the
// listener is up.
func startRelaySubprocess(t *testing.T, bin, db string) (addr string, cmd *exec.Cmd) {
	t.Helper()
	cmd = exec.Command(bin, "--listen", "127.0.0.1:0", "--db", db) //nolint:gosec // fixed test binary
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = stderrW
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = stderrW.Close()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	re := regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`)
	lines := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		var acc []byte
		for {
			n, err := stderrR.Read(buf)
			if n > 0 {
				acc = append(acc, buf[:n]...)
				lines <- string(acc)
			}
			if err != nil {
				close(lines)
				return
			}
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case s, ok := <-lines:
			if !ok {
				t.Fatalf("relay subprocess exited before reporting its address")
			}
			if m := re.FindStringSubmatch(s); m != nil {
				return m[1], cmd
			}
		case <-time.After(10 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatalf("relay subprocess never reported its address")
			}
		}
	}
}
