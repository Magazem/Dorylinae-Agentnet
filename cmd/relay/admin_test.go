package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// accountsDB creates a migrated relay database with account acc_t1 (@one,
// in quota group qg_t) and key bound to it.
func accountsDB(t *testing.T, key string) string {
	t.Helper()
	db := filepath.Join(testutil.TempDir(t), "relay.db")
	adm, err := relay.OpenAdmin(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = adm.Close()
	raw, err := sql.Open("sqlite", "file:"+db+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	for _, q := range []string{
		`INSERT INTO quota_groups (id, seats, created) VALUES ('qg_t', 8, 0)`,
		`INSERT INTO accounts (id, provider, subject, display, created, group_id) VALUES ('acc_t1', 'github', '1', '@one', 0, 'qg_t')`,
		`INSERT INTO accounts (id, provider, subject, display, created) VALUES ('acc_t2', 'github', '2', '@two', 1)`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO account_keys (key, account_id, device, os, bound_at) VALUES (?, 'acc_t1', 'laptop', 'linux', 0)`, key); err != nil {
		t.Fatal(err)
	}
	return db
}

func queryOne(t *testing.T, db, q string, args ...any) string {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+db+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var v sql.NullString
	if err := raw.QueryRow(q, args...).Scan(&v); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	return v.String
}

func admin(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), append([]string{"admin"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

func TestAdminAccountCommands(t *testing.T) {
	key := envelope.KeyString(newPriv(t).Public().(ed25519.PublicKey))
	db := accountsDB(t, key)
	journal := filepath.Join(filepath.Dir(db), "journal.jsonl")

	code, out, errOut := admin(t, "account", "list", "--db", db)
	if code != 0 || !strings.Contains(out, "acc_t1") || !strings.Contains(out, "@two") || !strings.Contains(out, "qg_t") {
		t.Fatalf("list: %d %q %q", code, out, errOut)
	}
	code, out, _ = admin(t, "account", "show", "acc_t1", "--db", db)
	fp, _ := envelope.KeyFingerprint(key)
	if code != 0 || !strings.Contains(out, key) || !strings.Contains(out, envelope.FormatFingerprint(fp)) || !strings.Contains(out, "laptop") {
		t.Fatalf("show: %d %q", code, out)
	}

	// Flags before or after the target; "team" is "group".
	for _, step := range []struct {
		args        []string
		query, want string
	}{
		{[]string{"account", "suspend", "--db", db, "--security-journal", journal, "acc_t1"}, `SELECT state FROM accounts WHERE id = 'acc_t1'`, "suspended"},
		{[]string{"account", "unsuspend", "acc_t1", "--db", db, "--security-journal", journal}, `SELECT state FROM accounts WHERE id = 'acc_t1'`, "active"},
		{[]string{"team", "suspend", "qg_t", "--db", db, "--security-journal", journal}, `SELECT state FROM quota_groups WHERE id = 'qg_t'`, "suspended"},
		{[]string{"group", "unsuspend", "qg_t", "--db", db, "--security-journal", journal}, `SELECT state FROM quota_groups WHERE id = 'qg_t'`, "active"},
		{[]string{"account", "unbind", key, "--db", db, "--security-journal", journal}, `SELECT COUNT(*) FROM account_keys`, "0"},
		{[]string{"account", "delete", "acc_t2", "--db", db, "--security-journal", journal}, `SELECT COUNT(*) FROM accounts WHERE id = 'acc_t2'`, "0"},
	} {
		if code, out, errOut := admin(t, step.args...); code != 0 || !strings.Contains(out, "done") {
			t.Fatalf("%v: %d %q %q", step.args, code, out, errOut)
		}
		if got := queryOne(t, db, step.query); got != step.want {
			t.Fatalf("%v: %s = %q, want %q", step.args, step.query, got, step.want)
		}
	}
	lines, err := os.ReadFile(journal) //nolint:gosec // path under testutil.TempDir(t)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(lines), "\n"); n != 6 || strings.Contains(string(lines), "@one") || strings.Contains(string(lines), key) {
		t.Fatalf("journal (%d lines, no display, key prefix only):\n%s", n, lines)
	}

	for _, bad := range [][]string{
		{"account"}, {"account", "list", "extra", "--db", db}, {"account", "show", "--db", db},
		{"account", "rename", "acc_t1", "--db", db}, {"invite", "list", "--db", db}, {"account", "list", "--bogus"},
	} {
		if code, _, _ := admin(t, bad...); code != 2 {
			t.Errorf("%v: code %d, want 2", bad, code)
		}
	}
	if code, _, errOut := admin(t, "account", "suspend", "acc_nope", "--db", db); code != 1 || !strings.Contains(errOut, "no such account") {
		t.Errorf("unknown account: %d %q", code, errOut)
	}
	if code, _, _ := admin(t, "account", "list", "--db", filepath.Join(filepath.Dir(db), "missing.db")); code != 1 {
		t.Errorf("missing database: code %d, want 1", code)
	}
}

// TestAdminUnbindClosesLiveConnection runs the relay binary's code with
// --accounts github and unbinds a connected key with relay admin on the same
// database: the connection gets account_revoked and closes within a second.
func TestAdminUnbindClosesLiveConnection(t *testing.T) {
	priv := newPriv(t)
	key := envelope.KeyString(priv.Public().(ed25519.PublicKey))
	db := accountsDB(t, key)
	url, _ := startRelay(t, "--accounts", "github", "--db", db)
	c := authed(t, url, priv)

	start := time.Now()
	if code, out, errOut := admin(t, "account", "unbind", key, "--db", db); code != 0 {
		t.Fatalf("unbind: %d %q %q", code, out, errOut)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, raw, err := c.Read(ctx)
	var ctl envelope.Control
	if err != nil || json.Unmarshal(raw, &ctl) != nil || ctl.Code != envelope.CodeAccountRevoked {
		t.Fatalf("got %s, %v; want account_revoked", raw, err)
	}
	_, _, err = c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("after account_revoked: %v, want close 1008", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("closed after %v, want within 1 s", d)
	}
}

func TestAccountsFlag(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--listen", "127.0.0.1:0", "--accounts", "gitlab"}, &out, &errb); code != 2 {
		t.Fatalf("--accounts gitlab: code %d, want 2", code)
	}

	db := filepath.Join(testutil.TempDir(t), "relay.db")
	url, _ := startRelay(t, "--accounts", "github", "--db", db)
	priv := newPriv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, ready := dialAuth(t, url, nil, priv, 0, "")
	if !strings.Contains(strings.Join(ready.Features, ","), envelope.FeatureAccounts) || ready.Account == nil || ready.Account.State != envelope.AccountUnbound {
		t.Fatalf("ready = %+v", ready)
	}
	raw, _ := json.Marshal(envelope.Control{Op: envelope.OpBindStart, Device: "laptop", OS: "linux"})
	if err := c.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	pend := readCtl(ctx, t, c)
	host := strings.TrimSuffix(strings.TrimPrefix(url, "ws://"), envelope.ConnectPath)
	if pend.Op != envelope.OpBindPending || pend.URL != "http://"+host+"/login" {
		t.Fatalf("bind_pending = %+v, want url http://%s/login", pend, host)
	}
}
