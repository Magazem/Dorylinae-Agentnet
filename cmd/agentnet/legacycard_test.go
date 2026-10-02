package main

// R55-F10 A12 and A13 (CLI side): a card made before the text rule (vector
// N18, "Ada <U+202E>tset") keeps working as the own card and as a stored
// peer card.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// cardVector returns the envelope of an agent-card.md vector.
func cardVector(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tools", "verifyvectors", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		AgentCard struct {
			Cases []struct {
				Name     string `json:"name"`
				Envelope string `json:"envelope"`
			} `json:"cases"`
		} `json:"agent_card"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, c := range v.AgentCard.Cases {
		if c.Name == name {
			return c.Envelope
		}
	}
	t.Fatalf("no vector %s", name)
	return ""
}

type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// A12: a daemon whose own card is N18 (seed 00..1f in the file keystore)
// starts and logs one own_card_legacy_text warning; identity prints the name
// escaped with the note, and identity --json is encoding/json's output.
func TestOwnLegacyCard(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	p := shortHome(t)
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ks, err := identity.NewKeystore(p.Dir, "file")
	if err != nil {
		t.Fatal(err)
	}
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	if _, _, err := ks.Save(seed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, identity.CardFile), []byte(cardVector(t, "N18")), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &logBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.RunWithOptions(ctx, p, ready, daemon.Options{Logger: slog.New(slog.NewTextHandler(logs, nil))})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon with a legacy own card did not start: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}
	if n := strings.Count(logs.String(), "event=own_card_legacy_text"); n != 1 {
		t.Fatalf("%d own_card_legacy_text warnings, want 1:\n%s", n, logs.String())
	}

	var out, errb bytes.Buffer
	if code := run([]string{"identity"}, &out, &errb); code != exitOK {
		t.Fatalf("identity: %d %q", code, errb.String())
	}
	if !strings.Contains(out.String(), `name:        Ada \u{202E}tset`+"\n") || !strings.Contains(out.String(), legacyCardNote+"\n") ||
		strings.ContainsRune(out.String(), 0x202E) {
		t.Fatalf("identity output:\n%s", out.String())
	}

	out.Reset()
	if code := run([]string{"identity", "--json"}, &out, &errb); code != exitOK {
		t.Fatalf("identity --json: %d %q", code, errb.String())
	}
	var body identityBody
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	_ = json.NewEncoder(&want).Encode(body)
	if !bytes.Equal(out.Bytes(), want.Bytes()) || !strings.ContainsRune(out.String(), 0x202E) {
		t.Fatalf("identity --json is not encoding/json's output:\n%s\nwant\n%s", out.String(), want.String())
	}
}

// A13 (doctor): a stored peer card that passes only the legacy text rule is
// ok, with a count.
func TestDoctorPeersLegacyCard(t *testing.T) {
	ctx := context.Background()
	p := paths.Paths{DB: filepath.Join(testutil.TempDir(t), "d.db")}
	st, err := store.Open(ctx, p.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.DB().Exec(`INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys)
VALUES (?, 'ada', 'custom', '[]', ?, '2026-01-02T03:04:05Z', 'code', '[]')`, "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg", cardVector(t, "N18")); err != nil {
		t.Fatal(err)
	}
	c := checkPeers(ctx, p)
	if c.State != doctorOK || c.Detail != "every stored peer card verifies (1 with characters refused at new pairings since R55-F10; shown escaped)" {
		t.Fatalf("legacy card: %+v", c)
	}
}
