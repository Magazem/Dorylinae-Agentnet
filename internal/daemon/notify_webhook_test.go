package daemon_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// TestNotifyWebhookIPC exercises the notify_get/notify_set/notify_test IPC
// methods end to end through a real daemon: setting a webhook prints the
// secret once, a queued test event is actually delivered to an httptest
// receiver, and "--webhook off" removes it (Docs/protocol/ipc.md
// §Notifications, Docs/cli/notify.md, ticket 1.8b).
func TestNotifyWebhookIPC(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	p, err := paths.In(testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, p, ready) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("daemon did not stop")
		}
	})

	// No webhook configured yet.
	var get daemon.NotifyGetResult
	if err := ipc.Call(ctx, p.Endpoint, "notify_get", nil, &get); err != nil {
		t.Fatal(err)
	}
	if get.Webhook != nil {
		t.Fatalf("webhook = %+v, want nil before any is set", get.Webhook)
	}

	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		select {
		case received <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()

	url := srv.URL
	var set daemon.NotifySetResult
	if err := ipc.Call(ctx, p.Endpoint, "notify_set", daemon.NotifySetParams{WebhookURL: &url}, &set); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(set.Secret, "whsec_") {
		t.Fatalf("secret = %q, want a whsec_ prefix on first set", set.Secret)
	}
	if set.Webhook == nil || set.Webhook.URL != url {
		t.Fatalf("webhook = %+v, want url %q", set.Webhook, url)
	}

	// Setting again without --rotate-secret must not print a new secret.
	var set2 daemon.NotifySetResult
	format := "generic"
	if err := ipc.Call(ctx, p.Endpoint, "notify_set", daemon.NotifySetParams{Format: &format}, &set2); err != nil {
		t.Fatal(err)
	}
	if set2.Secret != "" {
		t.Fatalf("secret = %q, want empty when not rotating", set2.Secret)
	}

	var test daemon.NotifyTestResult
	if err := ipc.Call(ctx, p.Endpoint, "notify_test", nil, &test); err != nil {
		t.Fatal(err)
	}
	if test.Webhook != "queued" {
		t.Fatalf("notify_test webhook = %q, want queued", test.Webhook)
	}
	select {
	case <-received:
	case <-time.After(10 * time.Second):
		t.Fatal("the test webhook was never delivered")
	}

	// "--webhook off" removes it.
	off := ""
	var set3 daemon.NotifySetResult
	if err := ipc.Call(ctx, p.Endpoint, "notify_set", daemon.NotifySetParams{WebhookURL: &off}, &set3); err != nil {
		t.Fatal(err)
	}
	if set3.Webhook != nil {
		t.Fatalf("webhook = %+v, want nil after removal", set3.Webhook)
	}

	// Each change is audited notify.config, never with the URL or the
	// secret (Docs/protocol/notify.md §Configuration).
	st, err := store.Open(ctx, p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var details string
	if err := st.DB().QueryRowContext(ctx, `SELECT group_concat(detail, '|') FROM audit_events WHERE action = 'notify.config' AND actor = 'cli'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"webhook":"set"`, `"format":"generic"`, `"webhook":"removed"`} {
		if !strings.Contains(details, want) {
			t.Errorf("notify.config audit %s lacks %s", details, want)
		}
	}
	if strings.Contains(details, srv.URL) || strings.Contains(details, "127.0.0.1") || strings.Contains(details, "whsec_") || strings.Contains(details, strings.TrimPrefix(set.Secret, "whsec_")) {
		t.Errorf("notify.config audit %s holds the URL or the secret", details)
	}
}

// TestNotifySetBadWebhookURL checks the URL validation surfaces as
// "bad_webhook" (Docs/cli/notify.md §Error codes).
func TestNotifySetBadWebhookURL(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	p, err := paths.In(testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, p, ready) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("daemon did not stop")
		}
	})

	bad := "http://example.com/hook" // http to a non-loopback host
	var set daemon.NotifySetResult
	err = ipc.Call(ctx, p.Endpoint, "notify_set", daemon.NotifySetParams{WebhookURL: &bad}, &set)
	var ierr *ipc.Error
	if err == nil {
		t.Fatal("expected an error for a non-loopback http:// webhook URL")
	}
	if !errors.As(err, &ierr) || ierr.Code != "bad_webhook" {
		t.Fatalf("error = %v, want code bad_webhook", err)
	}
}
