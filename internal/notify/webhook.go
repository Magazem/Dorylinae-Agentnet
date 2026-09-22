package notify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

// SecretLen is the raw secret length (Docs/protocol/notify.md §Configuration:
// "a 32-byte secret from crypto/rand").
const SecretLen = 32

// SecretPrefix is prepended to the printed, base64url-encoded secret
// (Docs/protocol/notify.md §Configuration).
const SecretPrefix = "whsec_"

// AuditSink is reused as the target for notify.fail (webhook channel).

// Webhook is the outgoing-webhook channel: secret storage, enqueueing and the
// delivery worker (Docs/protocol/notify.md §Webhook).
type Webhook struct {
	Settings *Settings
	Queue    *Queue
	Secret   *keystore.Store
	Audit    AuditSink
	Log      *slog.Logger
	Now      func() time.Time // defaults to time.Now
	Resolve  Resolver         // nil uses the system resolver; tests override it
}

func (w *Webhook) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Webhook) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// GenerateSecret returns SecretLen fresh random bytes.
func GenerateSecret() ([]byte, error) {
	b := make([]byte, SecretLen)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("notify: generate webhook secret: %w", err)
	}
	return b, nil
}

// PrintableSecret renders secret as it is shown to the user once
// (Docs/protocol/notify.md §Configuration).
func PrintableSecret(secret []byte) string {
	return SecretPrefix + base64.RawURLEncoding.EncodeToString(secret)
}

// RotateSecret generates a new secret and saves it, returning the printable
// form.
func (w *Webhook) RotateSecret() (string, error) {
	secret, err := GenerateSecret()
	if err != nil {
		return "", err
	}
	if _, _, err := w.Secret.Save(secret); err != nil {
		return "", fmt.Errorf("notify: save webhook secret: %w", err)
	}
	return PrintableSecret(secret), nil
}

// DeleteSecret removes the stored secret ("--webhook off").
func (w *Webhook) DeleteSecret() error {
	return w.Secret.Delete()
}

// Enqueue builds the payload for ev under the current webhook settings and
// adds it to the queue. It is a no-op when no webhook is configured
// (Docs/protocol/notify.md §Delivery: "Enqueue in the trigger").
func (w *Webhook) Enqueue(ctx context.Context, ev Event) error {
	cfg, ok, err := w.Settings.GetWebhook(ctx)
	if err != nil || !ok {
		return err
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = w.now()
	}
	return w.enqueue(ctx, cfg, ev)
}

// EnqueueTest enqueues the fixed "test" event ("agentnet notify --test",
// Docs/protocol/notify.md §Delivery). queued reports whether a webhook is
// configured.
func (w *Webhook) EnqueueTest(ctx context.Context) (queued bool, err error) {
	cfg, ok, err := w.Settings.GetWebhook(ctx)
	if err != nil || !ok {
		return false, err
	}
	if err := w.enqueue(ctx, cfg, Event{Kind: EventTest, CreatedAt: w.now()}); err != nil {
		return false, err
	}
	return true, nil
}

func (w *Webhook) enqueue(ctx context.Context, cfg WebhookConfig, ev Event) error {
	id, err := newDeliveryID()
	if err != nil {
		return err
	}
	p := buildPayload(id, ev, cfg.Title)
	body, err := marshalPayload(p)
	if err != nil {
		return err
	}
	body, err = applyFormat(cfg.Format, body, p.Text)
	if err != nil {
		return err
	}
	return w.Queue.Enqueue(ctx, id, ev.Kind, body, w.now())
}

// deliverBatch is how many due rows one tick processes.
const deliverBatch = 20

// tickInterval is how often the worker checks for due rows.
const tickInterval = time.Second

// Run drives the delivery worker until ctx is cancelled.
func (w *Webhook) Run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	maintenance := time.NewTicker(time.Hour)
	defer maintenance.Stop()
	for {
		w.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-maintenance.C:
			_ = w.Queue.Purge(ctx, w.now())
		}
	}
}

func (w *Webhook) tick(ctx context.Context) {
	rows, err := w.Queue.due(ctx, w.now(), deliverBatch)
	if err != nil {
		w.log().Warn("notify: webhook due query failed", "error", err)
		return
	}
	for _, row := range rows {
		w.attempt(ctx, row)
	}
}

func (w *Webhook) attempt(ctx context.Context, row queueRow) {
	now := w.now()
	cfg, ok, err := w.Settings.GetWebhook(ctx)
	if err != nil || !ok {
		return // removed; --webhook off already failed pending rows.
	}
	if err := ValidateWebhookURL(cfg.URL); err != nil {
		w.finish(ctx, row, 0, "bad_webhook", now, false)
		return
	}
	secret, _, err := w.Secret.Load()
	if err != nil {
		w.finish(ctx, row, 0, "no_secret", now, true)
		return
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		w.finish(ctx, row, 0, "bad_webhook", now, false)
		return
	}

	ts := now.Unix()
	sig := Sign(secret, row.ID, ts, []byte(row.Body))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, strings.NewReader(row.Body))
	if err != nil {
		w.finish(ctx, row, 0, "bad_webhook", now, false)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "agentnetd/"+version.Version)
	req.Header.Set("Dorylinae-Webhook-Id", row.ID)
	req.Header.Set("Dorylinae-Webhook-Timestamp", FormatTimestamp(ts))
	req.Header.Set("Dorylinae-Signature", "v1="+sig)

	client := httpClient(u.Scheme, w.Resolve)
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrBlockedAddress) {
			w.finish(ctx, row, 0, ErrBlockedAddress.Error(), now, false)
			return
		}
		w.finish(ctx, row, 0, err.Error(), now, true)
		return
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	status := resp.StatusCode
	switch {
	case status >= 200 && status < 300:
		if err := w.Queue.MarkSent(ctx, row.ID, status, now); err != nil {
			w.log().Warn("notify: mark webhook sent failed", "error", err)
		}
		return
	case status >= 300 && status < 400:
		w.finish(ctx, row, status, "redirect", now, false)
		return
	case status == 408, status == 429, status >= 500:
		w.finish(ctx, row, status, fmt.Sprintf("http_%d", status), now, true, retryAfter(resp)...)
		return
	default:
		w.finish(ctx, row, status, fmt.Sprintf("http_%d", status), now, false)
		return
	}
}

// finish applies the outcome of one attempt: a permanent failure, or (when
// retryable) a scheduled retry, subject to the 7-attempt and 24 h caps
// (Docs/protocol/notify.md §Delivery). retryAfterArg optionally carries the
// response's Retry-After.
func (w *Webhook) finish(ctx context.Context, row queueRow, status int, errStr string, now time.Time, retryable bool, retryAfterArg ...time.Duration) {
	attemptsSoFar := row.Attempts + 1
	tooOld := now.Sub(row.Created) > queueMaxAge
	var retryAfter time.Duration
	if len(retryAfterArg) > 0 {
		retryAfter = retryAfterArg[0]
	}
	if retryable && !tooOld {
		if delay, ok := nextRetry(attemptsSoFar, retryAfter, nil); ok {
			if err := w.Queue.MarkRetry(ctx, row.ID, status, errStr, now.Add(delay), now); err != nil {
				w.log().Warn("notify: mark webhook retry failed", "error", err)
			}
			return
		}
	}
	if err := w.Queue.MarkFailed(ctx, row.ID, status, errStr, now); err != nil {
		w.log().Warn("notify: mark webhook failed", "error", err)
	}
	w.reportFail(ctx, row.Event, row.ID, status)
}

// reportFail audits a permanent webhook failure. It never holds the URL or
// the body (Docs/protocol/notify.md §Delivery).
func (w *Webhook) reportFail(ctx context.Context, event, id string, status int) {
	if w.Audit == nil {
		return
	}
	detail := map[string]any{"channel": "webhook", "event": event, "id": id}
	if status > 0 {
		detail["status"] = status
	}
	_ = w.Audit.Append(ctx, "daemon", "notify.fail", detail)
}

func retryAfter(resp *http.Response) []time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return nil
	}
	var secs int
	if _, err := fmt.Sscanf(v, "%d", &secs); err != nil || secs <= 0 {
		return nil
	}
	return []time.Duration{time.Duration(secs) * time.Second}
}
