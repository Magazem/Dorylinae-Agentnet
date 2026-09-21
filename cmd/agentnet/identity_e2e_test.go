package main_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// secretForms lists the encodings under which the seed or the full private
// key could leak into an output.
func secretForms(seed []byte) []string {
	full := ed25519.NewKeyFromSeed(seed)
	var forms []string
	for _, b := range [][]byte{seed, full} {
		forms = append(forms,
			base64.RawURLEncoding.EncodeToString(b), base64.StdEncoding.EncodeToString(b),
			base64.RawStdEncoding.EncodeToString(b), base64.URLEncoding.EncodeToString(b),
			hex.EncodeToString(b), strings.ToUpper(hex.EncodeToString(b)))
	}
	return forms
}

func assertNoSecret(t *testing.T, what string, seed []byte, data []byte) {
	t.Helper()
	for _, f := range secretForms(seed) {
		if bytes.Contains(data, []byte(f)) {
			t.Errorf("%s contains private key material", what)
		}
	}
}

// TestIdentityE2E runs the real binaries: first start creates the identity,
// `agentnet identity --json` output verifies with the standalone verifier,
// tampering fails, a second daemon start reuses the key, the key file is
// owner-only, and no output or audit row leaks the private key.
func TestIdentityE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in -short mode")
	}
	home, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	bin := testutil.TempDir(t)
	cli := build(t, "github.com/Magazem/Dorylinae-Agentnet/cmd/agentnet", filepath.Join(bin, "agentnet"))
	dmn := build(t, "github.com/Magazem/Dorylinae-Agentnet/cmd/agentnetd", filepath.Join(bin, "agentnetd"))
	ver := build(t, "github.com/Magazem/Dorylinae-Agentnet/tools/verifycard", filepath.Join(bin, "verifycard"))
	env := append(os.Environ(), "DORYLINAE_HOME="+home, "DORYLINAE_KEYSTORE=file",
		"DORYLINAE_AGENT_NAME=e2e-agent", "DORYLINAE_HARNESS=codex")

	var captured [][]byte // every byte the processes printed
	run := func(stdin []byte, name string, args ...string) (int, []byte) {
		t.Helper()
		c := exec.Command(name, args...) //nolint:gosec // binaries we just built
		c.Env = env
		c.Stdin = bytes.NewReader(stdin)
		var out, errb bytes.Buffer
		c.Stdout, c.Stderr = &out, &errb
		err := c.Run()
		captured = append(captured, out.Bytes(), errb.Bytes())
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return code, out.Bytes()
	}

	startDaemon := func() func() {
		t.Helper()
		d := exec.Command(dmn) //nolint:gosec // binary we just built
		d.Env = env
		var out, errb bytes.Buffer
		d.Stdout, d.Stderr = &out, &errb
		if err := d.Start(); err != nil {
			t.Fatal(err)
		}
		stop := func() {
			_ = d.Process.Kill()
			_, _ = d.Process.Wait()
			captured = append(captured, out.Bytes(), errb.Bytes())
		}
		t.Cleanup(func() { _ = d.Process.Kill() })
		deadline := time.Now().Add(15 * time.Second)
		for {
			if code, _ := run(nil, cli, "status"); code == 0 {
				return stop
			}
			if time.Now().After(deadline) {
				stop()
				t.Fatalf("daemon never became ready: %s %s", out.String(), errb.String())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	type idBody struct {
		OK         bool   `json:"ok"`
		Signature  string `json:"signature"`
		KeyBackend string `json:"key_backend"`
		Card       struct {
			Name      string `json:"name"`
			Harness   string `json:"harness"`
			PublicKey string `json:"public_key"`
		} `json:"card"`
	}
	getID := func() ([]byte, idBody) {
		t.Helper()
		code, out := run(nil, cli, "identity", "--json")
		if code != 0 {
			t.Fatalf("identity --json: exit %d: %s", code, out)
		}
		var b idBody
		if err := json.Unmarshal(out, &b); err != nil {
			t.Fatalf("bad JSON %q: %v", out, err)
		}
		return out, b
	}

	// Before the daemon runs: exit 3 with a JSON error.
	if code, out := run(nil, cli, "identity", "--json"); code != 3 || !bytes.Contains(out, []byte("daemon_not_running")) {
		t.Fatalf("no daemon: exit %d out %s", code, out)
	}

	stop := startDaemon()
	out1, id1 := getID()
	if !id1.OK || id1.KeyBackend != "file" || id1.Card.Name != "e2e-agent" || id1.Card.Harness != "codex" {
		t.Fatalf("unexpected identity: %+v", id1)
	}

	// The standalone verifier accepts the CLI output as-is.
	if code, vout := run(out1, ver); code != 0 || !strings.HasPrefix(string(vout), "OK") {
		t.Fatalf("verifier: exit %d out %s", code, vout)
	}
	// Tampering with any card field fails.
	for _, tamper := range [][2]string{
		{`"e2e-agent"`, `"mallory"`}, {`"codex"`, `"claude-code"`}, {`"version":1`, `"version":2`},
		{`"skills":[]`, `"skills":[{"id":"a","name":"b","description":""}]`},
	} {
		bad := strings.Replace(string(out1), tamper[0], tamper[1], 1)
		if bad == string(out1) {
			t.Fatalf("tamper %v did not apply to %s", tamper, out1)
		}
		if code, _ := run([]byte(bad), ver); code != 1 {
			t.Errorf("tampered card %v: verifier exit %d, want 1", tamper, code)
		}
	}
	stop()

	// Key file is owner-only and holds the seed.
	keyPath := filepath.Join(home, identity.KeyFile)
	if err := keystore.OwnerOnly(keyPath); err != nil {
		t.Fatalf("key file not restricted: %v", err)
	}
	seed, _, err := keystore.New(keystore.NewFile(keyPath)).Load()
	if err != nil || len(seed) != ed25519.SeedSize {
		t.Fatalf("read seed: %v", err)
	}

	// Second daemon start reuses the same key.
	stop = startDaemon()
	out2, id2 := getID()
	if id2.Card.PublicKey != id1.Card.PublicKey || id2.Signature != id1.Signature {
		t.Fatalf("identity changed across restart:\n%s\n%s", out1, out2)
	}
	if code, _ := run(out2, ver); code != 0 {
		t.Fatal("verifier rejected the card after restart")
	}
	// Human output and help.
	if code, human := run(nil, cli, "identity"); code != 0 || !bytes.Contains(human, []byte("e2e-agent")) {
		t.Fatalf("human output: exit %d %s", code, human)
	}
	if code, help := run(nil, cli, "identity", "--help"); code != 0 || !bytes.Contains(help, []byte("--json")) {
		t.Fatalf("help: exit %d %s", code, help)
	}
	stop()

	// No output of any process contains the private key.
	for i, c := range captured {
		assertNoSecret(t, fmt.Sprintf("process output #%d", i), seed, c)
	}

	// Exactly one identity.create audit row, holding no secret.
	st, err := store.Open(context.Background(), filepath.Join(home, "dorylinae.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	evs, err := audit.New(st.DB()).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	creates := 0
	for _, e := range evs {
		assertNoSecret(t, "audit row "+e.Action, seed, e.Detail)
		if e.Action == identity.ActionCreate {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("identity.create rows = %d, want 1 (events %+v)", creates, evs)
	}
}
