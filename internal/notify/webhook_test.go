package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// fakeClock lets tests advance time deterministically past retry backoffs.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func openWebhook(t *testing.T) (*Webhook, *fakeClock) {
	t.Helper()
	ctx := context.Background()
	dir := testutil.TempDir(t)
	st, err := store.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	ks := keystore.New(keystore.NewFile(filepath.Join(dir, "webhook.key")))
	wh := &Webhook{
		Settings: NewSettings(st.DB()),
		Queue:    NewQueue(st.DB()),
		Secret:   ks,
		Now:      clock.Now,
	}
	return wh, clock
}

func mustSetWebhook(t *testing.T, wh *Webhook, url string, title bool) {
	t.Helper()
	ctx := context.Background()
	if err := wh.Settings.SetWebhook(ctx, WebhookConfig{URL: url, Format: FormatGeneric, Title: title}, wh.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := wh.RotateSecret(); err != nil {
		t.Fatal(err)
	}
}

// TestWebhookSigned is the core 1.8b acceptance test: an httptest receiver
// gets the payload, and the signature verifies with the stored secret
// (Docs/protocol/notify.md §Signature, ticket 1.8b).
func TestWebhookSigned(t *testing.T) {
	ctx := context.Background()
	var gotBody []byte
	var gotHeaders http.Header
	done := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		done <- struct{}{}
	}))
	defer srv.Close()

	wh, clock := openWebhook(t)
	mustSetWebhook(t, wh, srv.URL, false)

	if err := wh.Enqueue(ctx, Event{
		Kind: EventReceived, PeerName: "bob", PeerFP: "FP1", Type: "review", Urgency: "high",
		Title: "the brief must never leave", RequestID: "r-1", State: "pending", CreatedAt: clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	wh.tick(ctx)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("receiver was never called")
	}

	secret, _, err := wh.Secret.Load()
	if err != nil {
		t.Fatal(err)
	}
	sigHeader := gotHeaders.Get("Dorylinae-Signature")
	sig, ok := ParseSignatureHeader(sigHeader)
	if !ok {
		t.Fatalf("bad signature header %q", sigHeader)
	}
	id := gotHeaders.Get("Dorylinae-Webhook-Id")
	tsStr := gotHeaders.Get("Dorylinae-Webhook-Timestamp")
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		t.Fatalf("bad timestamp header %q: %v", tsStr, err)
	}
	if !Verify(secret, id, ts, gotBody, sig) {
		t.Fatal("signature did not verify with the printed secret")
	}
	if gotHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", gotHeaders.Get("Content-Type"))
	}

	pending, _, err := wh.Queue.Counts(ctx, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Errorf("pending = %d, want 0 after a successful delivery", pending)
	}

	if strings.Contains(string(gotBody), "the brief must never leave") {
		t.Error("the title leaked into the payload with title:false")
	}
}

// TestWebhookRetryThenSent: a 500 then a 200 delivers with exactly one retry
// (Docs/protocol/notify.md §Delivery).
func TestWebhookRetryThenSent(t *testing.T) {
	ctx := context.Background()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh, clock := openWebhook(t)
	mustSetWebhook(t, wh, srv.URL, false)
	if err := wh.Enqueue(ctx, Event{Kind: EventTest, CreatedAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}

	wh.tick(ctx) // attempt 1: 500 -> scheduled retry
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("calls after tick 1 = %d, want 1", n)
	}
	pending, _, _ := wh.Queue.Counts(ctx, clock.Now())
	if pending != 1 {
		t.Fatalf("pending after 500 = %d, want 1 (retry scheduled)", pending)
	}

	wh.tick(ctx) // too early: next_attempt is still in the future
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("calls after an early tick = %d, want still 1", n)
	}

	clock.Advance(15 * time.Second) // past the ~10s (x0.9..1.1) backoff
	wh.tick(ctx)                    // attempt 2: 200 -> sent
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("calls after retry = %d, want 2", n)
	}
	pending, _, _ = wh.Queue.Counts(ctx, clock.Now())
	if pending != 0 {
		t.Fatalf("pending after success = %d, want 0", pending)
	}
}

// TestWebhook404Failed: a non-retryable 4xx fails permanently, no retry.
func TestWebhook404Failed(t *testing.T) {
	ctx := context.Background()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	wh, clock := openWebhook(t)
	mustSetWebhook(t, wh, srv.URL, false)
	if err := wh.Enqueue(ctx, Event{Kind: EventTest, CreatedAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	wh.tick(ctx)
	clock.Advance(time.Hour)
	wh.tick(ctx)
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("calls = %d, want 1 (no retry after a 404)", n)
	}
	pending, failed, _ := wh.Queue.Counts(ctx, clock.Now())
	if pending != 0 || failed != 1 {
		t.Fatalf("pending=%d failed7d=%d, want 0, 1", pending, failed)
	}
}

// TestWebhook302Failed: a redirect is never followed and is a permanent
// failure (Docs/protocol/notify.md §Delivery, review 12).
func TestWebhook302Failed(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/steal", http.StatusFound)
	}))
	defer srv.Close()

	wh, clock := openWebhook(t)
	mustSetWebhook(t, wh, srv.URL, false)
	if err := wh.Enqueue(ctx, Event{Kind: EventTest, CreatedAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	wh.tick(ctx)
	pending, failed, _ := wh.Queue.Counts(ctx, clock.Now())
	if pending != 0 || failed != 1 {
		t.Fatalf("pending=%d failed7d=%d, want 0, 1 (redirect is permanent)", pending, failed)
	}
}

// TestWebhookOffDeletesSecretAndFailsPending mirrors the daemon's
// "--webhook off" handling (Docs/cli/notify.md, Docs/protocol/notify.md
// §Delivery "Removing the webhook marks every pending row failed").
func TestWebhookOffDeletesSecretAndFailsPending(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // stays pending until removed
	}))
	defer srv.Close()

	wh, clock := openWebhook(t)
	mustSetWebhook(t, wh, srv.URL, false)
	if err := wh.Enqueue(ctx, Event{Kind: EventTest, CreatedAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	wh.tick(ctx)
	pending, _, _ := wh.Queue.Counts(ctx, clock.Now())
	if pending != 1 {
		t.Fatalf("pending = %d, want 1 before removal", pending)
	}

	if err := wh.Settings.SetWebhook(ctx, WebhookConfig{}, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := wh.DeleteSecret(); err != nil {
		t.Fatal(err)
	}
	if err := wh.Queue.FailAllPending(ctx, "removed", clock.Now()); err != nil {
		t.Fatal(err)
	}

	if _, _, err := wh.Secret.Load(); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("secret Load() = %v, want ErrNotFound", err)
	}
	pending, failed, _ := wh.Queue.Counts(ctx, clock.Now())
	if pending != 0 || failed != 1 {
		t.Fatalf("pending=%d failed7d=%d, want 0, 1", pending, failed)
	}
}

// TestQueueBounded checks the 1000-row cap: enqueueing past it drops the
// oldest pending row as an overflow failure (Docs/protocol/notify.md
// §Delivery).
func TestQueueBounded(t *testing.T) {
	ctx := context.Background()
	dir := testutil.TempDir(t)
	st, err := store.Open(ctx, filepath.Join(dir, "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	q := NewQueue(st.DB())

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < queueMaxRows; i++ {
		id := fmt.Sprintf("w-%06d", i)
		if err := q.Enqueue(ctx, id, "test", []byte(`{}`), now); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
		now = now.Add(time.Millisecond)
	}
	pending, _, err := q.Counts(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if pending != queueMaxRows {
		t.Fatalf("pending = %d, want %d", pending, queueMaxRows)
	}

	// One more enqueue must overflow the oldest row.
	if err := q.Enqueue(ctx, "w-overflow", "test", []byte(`{}`), now); err != nil {
		t.Fatal(err)
	}
	pending, _, err = q.Counts(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if pending != queueMaxRows {
		t.Fatalf("pending after overflow = %d, want still %d", pending, queueMaxRows)
	}
	var overflowErr string
	if err := st.DB().QueryRowContext(ctx, `SELECT error FROM webhook_queue WHERE state = 'failed'`).Scan(&overflowErr); err != nil {
		t.Fatal(err)
	}
	if overflowErr != "overflow" {
		t.Fatalf("overflowed row error = %q, want \"overflow\"", overflowErr)
	}
}
