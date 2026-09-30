package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// TestBackupAndRestoreSubcommands exercises `relay backup` and `relay
// restore` as the CLI, not the internal/relay API directly.
func TestBackupAndRestoreSubcommands(t *testing.T) {
	dir := testutil.TempDir(t)
	db := filepath.Join(dir, "cli.db")
	url, stop := startRelay(t, "--db", db)
	pubA, privA, _ := ed25519.GenerateKey(rand.Reader)
	pubB, _, _ := ed25519.GenerateKey(rand.Reader)
	keyA, keyB := envelope.KeyString(pubA), envelope.KeyString(pubB)
	ca := authed(t, url, privA)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := envelope.Envelope{From: keyA, To: keyB, Team: "t", Type: "ping", ID: "cli-1", TS: time.Now().UTC().Format(time.RFC3339Nano), Payload: []byte(`"x"`)}
	frame, err := e.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	if _, raw, err := ca.Read(ctx); err != nil || !strings.Contains(string(raw), `"op":"queued"`) {
		t.Fatalf("queued: %s, %v", raw, err)
	}
	stop()

	backup := filepath.Join(dir, "cli-backup.db")
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"backup", "--db", db, "--out", backup}, &out, &errb); code != 0 {
		t.Fatalf("backup: code=%d stderr=%s", code, errb.String())
	}

	restored := filepath.Join(dir, "cli-restored.db")
	out.Reset()
	errb.Reset()
	if code := run(context.Background(), []string{"restore", "--from", backup, "--db", restored}, &out, &errb); code != 0 {
		t.Fatalf("restore: code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "restored") {
		t.Errorf("restore stdout = %q", out.String())
	}

	var n int
	rdb, err := sql.Open("sqlite", "file:"+restored)
	if err != nil {
		t.Fatal(err)
	}
	queryErr := rdb.QueryRow(`SELECT COUNT(*) FROM queue WHERE to_key = ?`, keyB).Scan(&n)
	if err := rdb.Close(); err != nil { // Windows: the file must be free before the next restore
		t.Fatal(err)
	}
	if queryErr != nil || n != 1 {
		t.Fatalf("restored queue rows for B = %d, %v; want 1", n, queryErr)
	}

	// restore without --force onto the same non-empty target refuses.
	out.Reset()
	errb.Reset()
	if code := run(context.Background(), []string{"restore", "--from", backup, "--db", restored}, &out, &errb); code == 0 {
		t.Fatal("restore onto a non-empty database without --force succeeded")
	}
	if code := run(context.Background(), []string{"restore", "--from", backup, "--db", restored, "--force"}, &out, &errb); code != 0 {
		t.Fatalf("restore --force: code=%d stderr=%s", code, errb.String())
	}
}

// TestMetricsNotServedOnPublicListener: /metrics only answers on
// --metrics-listen, never on the daemon-facing listener.
func TestMetricsNotServedOnPublicListener(t *testing.T) {
	var out, errb syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	dir := testutil.TempDir(t)
	go func() {
		done <- run(ctx, []string{"--listen", "127.0.0.1:0", "--metrics-listen", "127.0.0.1:0", "--db", filepath.Join(dir, "m.db")}, &out, &errb)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	publicAddr := waitForMatch(t, &errb, `listening on (127\.0\.0\.1:\d+)`)
	resp, err := http.Get("http://" + publicAddr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("/metrics answered 200 on the public listener")
	}
}

// TestMetricsListenerServesMetrics: the separate --metrics-listen address
// does answer /metrics.
func TestMetricsListenerServesMetrics(t *testing.T) {
	var out, errb syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	dir := testutil.TempDir(t)
	go func() {
		done <- run(ctx, []string{"--listen", "127.0.0.1:0", "--metrics-listen", "127.0.0.1:0", "--db", filepath.Join(dir, "m2.db")}, &out, &errb)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	metricsAddr := waitForMatch(t, &errb, `metrics listening on (127\.0\.0\.1:\d+)`)
	resp, err := http.Get("http://" + metricsAddr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200", resp.StatusCode)
	}
	// R55-F2 (acceptance test 17): the redelivery counters, unlabelled.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"relay_queue_redelivered_bytes_total 0\n", "relay_queue_redeliveries_skipped_total 0\n",
		"# TYPE relay_queue_redelivered_bytes_total counter", "# TYPE relay_queue_redeliveries_skipped_total counter"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics lacks %q:\n%s", want, body)
		}
	}
}

func waitForMatch(t *testing.T, errb *syncBuffer, pattern string) string {
	t.Helper()
	re := regexp.MustCompile(pattern)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if m := re.FindStringSubmatch(errb.String()); m != nil {
			return m[1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("never matched %q in stderr: %s", pattern, errb.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSecurityJournalFlagOpensFile exercises --security-journal end to end
// at the CLI level: nothing is written by the relay itself in this ticket
// (no accounts/invites yet to generate events), but the flag must open the
// file and the restore --replay-journal path must read it without error.
func TestSecurityJournalFlagOpensFile(t *testing.T) {
	dir := testutil.TempDir(t)
	journal := filepath.Join(dir, "journal.jsonl")
	f, err := os.OpenFile(journal, os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path under testutil.TempDir(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "j.db")
	_, stop := startRelay(t, "--db", db, "--security-journal", journal)
	stop()

	backup := filepath.Join(dir, "j-backup.db")
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"backup", "--db", db, "--out", backup}, &out, &errb); code != 0 {
		t.Fatalf("backup: code=%d stderr=%s", code, errb.String())
	}
	restored := filepath.Join(dir, "j-restored.db")
	out.Reset()
	if code := run(context.Background(), []string{"restore", "--from", backup, "--db", restored, "--replay-journal", journal}, &out, &errb); code != 0 {
		t.Fatalf("restore --replay-journal: code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "replayed 0 journal entries") {
		t.Errorf("stdout = %q, want a replay summary", out.String())
	}
}
