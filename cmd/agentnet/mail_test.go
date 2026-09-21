package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

func TestMailCommandOnlyWithDebugEnv(t *testing.T) {
	t.Setenv(mail.DebugEnv, "")
	var out, errb bytes.Buffer
	if code := run([]string{"mail", "send", "@x", "--kind", "note"}, &out, &errb); code != exitUsage || !strings.Contains(errb.String(), `unknown command "mail"`) {
		t.Errorf("without %s: code %d, stderr %q", mail.DebugEnv, code, errb.String())
	}
	out.Reset()
	if code := run([]string{"--help"}, &out, &errb); code != exitOK || strings.Contains(out.String(), "mail") {
		t.Errorf("help lists mail without %s: %q", mail.DebugEnv, out.String())
	}

	t.Setenv(mail.DebugEnv, "1")
	out.Reset()
	if code := run([]string{"--help"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "mail send") {
		t.Errorf("help lacks mail with %s=1: %q", mail.DebugEnv, out.String())
	}
	if code := run([]string{"mail", "send", "--help"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "--kind") {
		t.Errorf("mail send --help: code %d", code)
	}
	for _, args := range [][]string{{"mail"}, {"mail", "send"}, {"mail", "send", "@a"}, {"mail", "send", "@a", "@b", "--kind", "note"}, {"mail", "send", "@a", "--bogus"}} {
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: code %d, want usage", args, code)
		}
	}
}

func TestMailSendNoteDelivered(t *testing.T) {
	t.Setenv(mail.DebugEnv, "1") // both daemons register the note kind
	srv := relay.New(relay.Options{Logger: slog.New(slog.NewTextHandler(&logBuf{}, nil))})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http")

	a := startNode(t, "alice", url)
	b := startNode(t, "bob", url)
	a.waitRelay(t)
	b.waitRelay(t)
	waitConnected(t, srv, a.key, b.key)
	pairNodes(t, a, b)

	code, out, errs := cli(t, a, "mail", "send", "@bob", "--kind", mail.DebugKind, "--text", "hello", "--json")
	var sent struct {
		OK    bool   `json:"ok"`
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &sent); err != nil || code != exitOK || !sent.OK || sent.State != mail.StateQueued || !mail.ValidID(sent.ID) {
		t.Fatalf("mail send = %d %q %q (%v)", code, out, errs, err)
	}

	// The note lands in B's inbox and the ack marks A's outbox row delivered.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if inboxCount(t, b, sent.ID) == 1 && outboxState(t, a, sent.ID) == mail.StateDelivered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not delivered: inbox=%d outbox=%q", inboxCount(t, b, sent.ID), outboxState(t, a, sent.ID))
		}
		time.Sleep(20 * time.Millisecond)
	}

	if code, out, _ := cli(t, a, "mail", "send", "@bob", "--kind", mail.DebugKind, "--text", "again"); code != exitOK || !strings.Contains(out, "queued mail m-") {
		t.Errorf("human output: %d %q", code, out)
	}
	if code, out, _ := cli(t, a, "mail", "send", "@nobody", "--kind", mail.DebugKind, "--json"); code != exitError || !strings.Contains(out, "unknown_peer") {
		t.Errorf("unknown peer: %d %q", code, out)
	}
}

func inboxCount(t *testing.T, n *testNode, id string) int {
	t.Helper()
	return queryInt(t, n, `SELECT COUNT(*) FROM mail_inbox WHERE id = ?`, id)
}

func outboxState(t *testing.T, n *testNode, id string) string {
	t.Helper()
	st, err := store.Open(t.Context(), n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var s string
	if err := st.DB().QueryRow(`SELECT state FROM outbox WHERE id = ?`, id).Scan(&s); err != nil {
		return ""
	}
	return s
}

func queryInt(t *testing.T, n *testNode, q string, args ...any) int {
	t.Helper()
	st, err := store.Open(t.Context(), n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var c int
	if err := st.DB().QueryRow(q, args...).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}
