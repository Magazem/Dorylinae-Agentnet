package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestWebhookNoIdleConnLeak (R55-026, C27-01): every delivery attempt builds
// its own transport, so no keep-alive connection may outlive the attempt.
func TestWebhookNoIdleConnLeak(t *testing.T) {
	ctx := context.Background()
	var open atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		switch s {
		case http.StateNew:
			open.Add(1)
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
		}
	}
	srv.Start()
	defer srv.Close()

	wh, clock := openWebhook(t)
	mustSetWebhook(t, wh, srv.URL, false)
	const n = 40
	for i := 0; i < n; i++ {
		if err := wh.Enqueue(ctx, Event{Kind: EventReceived, PeerName: "bob", Type: "review", Urgency: "high", RequestID: "r-1", State: "pending", CreatedAt: clock.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	wh.tick(ctx)
	wh.tick(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for open.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := open.Load(); got != 0 {
		t.Fatalf("connection leak: %d connections still open after %d deliveries", got, n)
	}
	if pending, _, err := wh.Queue.Counts(ctx, clock.Now()); err != nil || pending != 0 {
		t.Fatalf("pending = %d, err = %v; want every delivery sent", pending, err)
	}
}

// bodyRecorder is a receiver that keeps the bodies it was sent.
type bodyRecorder struct {
	mu     sync.Mutex
	bodies []string
}

func (r *bodyRecorder) handler(w http.ResponseWriter, req *http.Request) {
	b, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.bodies = append(r.bodies, string(b))
	r.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (r *bodyRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bodies...)
}

// TestWebhookRendersPerAttempt (R55-074): turning the title off, or changing
// the format, after enqueue applies to rows that are already queued.
func TestWebhookRendersPerAttempt(t *testing.T) {
	ctx := context.Background()
	rec := &bodyRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()

	wh, clock := openWebhook(t)
	mustSetWebhook(t, wh, srv.URL, true)
	err := wh.Enqueue(ctx, Event{
		Kind: EventCompleted, PeerName: "bob", Type: "review", Title: "secret title <!channel>",
		HasResult: true, ResultStatus: "pass", RequestID: "r-1", State: "completed", CreatedAt: clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// The owner turns the title off and picks slack before the row is sent.
	if err := wh.Settings.SetWebhook(ctx, WebhookConfig{URL: srv.URL, Format: FormatSlack, Title: false}, clock.Now()); err != nil {
		t.Fatal(err)
	}
	wh.tick(ctx)

	bodies := rec.all()
	if len(bodies) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(bodies))
	}
	if strings.Contains(bodies[0], "secret title") || strings.Contains(bodies[0], "pass") {
		t.Fatalf("title or status leaked with title off: %s", bodies[0])
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &m); err != nil {
		t.Fatal(err)
	}
	if got := m["text"]; got != "bob completed your review request" {
		t.Fatalf("text = %q", got)
	}
}

// TestWebhookRendersTitleOn checks the title survives when the setting stays on
// and that slack escaping is applied at send time.
func TestWebhookRendersTitleOn(t *testing.T) {
	ctx := context.Background()
	rec := &bodyRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()

	wh, clock := openWebhook(t)
	mustSetWebhook(t, wh, srv.URL, true)
	if err := wh.Enqueue(ctx, Event{Kind: EventReceived, PeerName: "bob", Type: "review", Urgency: "high", Title: "a <!channel>", RequestID: "r-1", State: "pending", CreatedAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := wh.Settings.SetWebhook(ctx, WebhookConfig{URL: srv.URL, Format: FormatSlack, Title: true}, clock.Now()); err != nil {
		t.Fatal(err)
	}
	wh.tick(ctx)
	bodies := rec.all()
	if len(bodies) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(bodies))
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(bodies[0]), &m)
	if got := m["text"]; got != "High review request from bob: a &lt;!channel&gt;" {
		t.Fatalf("text = %q", got)
	}
}

// TestWebhookExpiredRowNotPosted (R55-076): a row older than 24 h fails
// before any POST.
func TestWebhookExpiredRowNotPosted(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh, clock := openWebhook(t)
	mustSetWebhook(t, wh, srv.URL, false)
	if err := wh.Enqueue(ctx, Event{Kind: EventReceived, PeerName: "bob", Type: "review", Urgency: "high", RequestID: "r-1", State: "pending", CreatedAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(queueMaxAge + time.Minute)
	wh.tick(ctx)

	if n := calls.Load(); n != 0 {
		t.Fatalf("an expired row was POSTed %d times", n)
	}
	pending, failed, err := wh.Queue.Counts(ctx, clock.Now())
	if err != nil || pending != 0 || failed != 1 {
		t.Fatalf("pending = %d, failed = %d, err = %v; want 0, 1", pending, failed, err)
	}
}

// TestBreakURLsSlackDiscord (R55-175): bare URLs in peer text are not left
// clickable, and Discord embeds and Slack unfurls are switched off.
func TestBreakURLsSlackDiscord(t *testing.T) {
	text := "High review request from https://evil.example"
	broken := "High review request from https:\u200b//evil.example"

	out, err := applyFormat(FormatSlack, []byte(`{"v":1}`), text)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	_ = json.Unmarshal(out, &s)
	if s["text"] != broken || s["unfurl_links"] != false || s["unfurl_media"] != false {
		t.Fatalf("slack = %v", s)
	}

	out, err = applyFormat(FormatDiscord, []byte(`{"v":1}`), text)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	_ = json.Unmarshal(out, &d)
	if d["content"] != broken || d["flags"] != float64(discordSuppressEmbeds) {
		t.Fatalf("discord = %v", d)
	}
}

type recordingAudit struct {
	mu      sync.Mutex
	actions []string
	details []any
}

func (a *recordingAudit) Append(_ context.Context, _, action string, detail any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, action)
	a.details = append(a.details, detail)
	return nil
}

// TestDesktopFailAuditUsesErrorKey (R55-174): the desktop notify.fail detail
// follows notify.md §Audit: {channel, error}.
func TestDesktopFailAuditUsesErrorKey(t *testing.T) {
	audit := &recordingAudit{}
	tr := &Trigger{
		Show:  func(context.Context, string, string) error { return errors.New("no bus") },
		Audit: audit,
	}
	tr.reportFail(context.Background(), errors.New("no bus"))
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.details) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(audit.details))
	}
	d, ok := audit.details[0].(map[string]string)
	if !ok || d["channel"] != "desktop" || d["error"] != "desktop_failed" {
		t.Fatalf("detail = %#v", audit.details[0])
	}
	if _, has := d["code"]; has {
		t.Fatalf("detail still has a code key: %#v", d)
	}
}

// TestWebhookLegacyRenderedRowSentAsStored: a row queued before per-attempt
// rendering already holds its final slack body, so it is sent as stored and
// not escaped a second time.
func TestWebhookLegacyRenderedRowSentAsStored(t *testing.T) {
	ctx := context.Background()
	rec := &bodyRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()

	wh, clock := openWebhook(t)
	mustSetWebhook(t, wh, srv.URL, false)
	if err := wh.Settings.SetWebhook(ctx, WebhookConfig{URL: srv.URL, Format: FormatSlack}, clock.Now()); err != nil {
		t.Fatal(err)
	}
	legacy := `{"v":1,"id":"w-legacy","event":"request.received","ts":"2026-01-01T00:00:00Z","text":"a &lt;b&gt; &amp; c"}`
	if err := wh.Queue.Enqueue(ctx, "w-legacy", EventReceived, []byte(legacy), clock.Now()); err != nil {
		t.Fatal(err)
	}
	wh.tick(ctx)
	bodies := rec.all()
	if len(bodies) != 1 || bodies[0] != legacy {
		t.Fatalf("bodies = %q, want the stored body unchanged", bodies)
	}
}

// TestWebhookQueuedMarkerNotSent: the queue marker never reaches the receiver.
func TestWebhookQueuedMarkerNotSent(t *testing.T) {
	ctx := context.Background()
	rec := &bodyRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()

	wh, clock := openWebhook(t)
	mustSetWebhook(t, wh, srv.URL, false)
	if err := wh.Enqueue(ctx, Event{Kind: EventReceived, PeerName: "bob", Type: "review", Urgency: "high", RequestID: "r-1", State: "pending", CreatedAt: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	wh.tick(ctx)
	bodies := rec.all()
	if len(bodies) != 1 || strings.Contains(bodies[0], "queued") {
		t.Fatalf("bodies = %q", bodies)
	}
}
