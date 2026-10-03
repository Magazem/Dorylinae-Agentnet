package daemon

import (
	"context"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
)

// ActionRejectSummary is the audit action of the daily count of relay-causable
// rejects that are logged only (Docs/protocol/audit.md §Who may cause a row,
// OD-F14-7 (b), R55-F14).
const ActionRejectSummary = "relay.reject_summary"

// rejectSummaryInterval is how often a non-zero summary row is written.
const rejectSummaryInterval = 24 * time.Hour

// Layers of the summary.
const (
	summaryMail    = "mail"
	summarySession = "session"
)

// rejectSummaryDetail is the row's detail: fixed keys, and counts keyed by
// the reject reasons of mail.md and session.md (code constants). No peer.
type rejectSummaryDetail struct {
	Since   string         `json:"since"`
	Until   string         `json:"until"`
	Mail    map[string]int `json:"mail"`
	Session map[string]int `json:"session"`
}

type auditAppender interface {
	Append(ctx context.Context, actor, action string, detail any) error
}

// rejectSummary counts every logged-only reject except unpaired, per layer
// and reason, and writes one relay.reject_summary row a day and at a clean
// stop, only when a count is non-zero. The mail RejectAudit and the session
// manager feed it; unpaired never reaches it (D49).
type rejectSummary struct {
	audit    auditAppender
	log      *slog.Logger
	now      func() time.Time
	interval time.Duration

	mu    sync.Mutex
	since time.Time
	mail  map[string]int
	sess  map[string]int

	cancel context.CancelFunc
	done   chan struct{}
}

func newRejectSummary(a auditAppender, log *slog.Logger, now func() time.Time) *rejectSummary {
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &rejectSummary{audit: a, log: log, now: now, interval: rejectSummaryInterval,
		since: now(), mail: map[string]int{}, sess: map[string]int{}}
}

// countMail and countSession take a reject reason, a constant of the code.
func (s *rejectSummary) countMail(reason string)    { s.count(summaryMail, reason) }
func (s *rejectSummary) countSession(reason string) { s.count(summarySession, reason) }

func (s *rejectSummary) count(layer, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if layer == summaryMail {
		s.mail[reason]++
	} else {
		s.sess[reason]++
	}
}

// write appends the row for the counts since the last one, if any, and starts
// a new period either way.
func (s *rejectSummary) write(ctx context.Context) {
	now := s.now()
	s.mu.Lock()
	d := rejectSummaryDetail{
		Since: s.since.UTC().Format(time.RFC3339), Until: now.UTC().Format(time.RFC3339),
		Mail: maps.Clone(s.mail), Session: maps.Clone(s.sess),
	}
	s.since = now
	clear(s.mail)
	clear(s.sess)
	s.mu.Unlock()
	if len(d.Mail) == 0 && len(d.Session) == 0 {
		return
	}
	_ = s.audit.Append(ctx, audit.ActorDaemon, ActionRejectSummary, d) // a failure is logged once, centrally, by internal/audit
}

// start writes the summary every interval until stop.
func (s *rejectSummary) start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(s.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.write(ctx)
			}
		}
	}()
}

// stop ends the ticker and writes the last period (a clean stop). Run it after
// the relay and the session manager have stopped, so nothing is counted after.
func (s *rejectSummary) stop(ctx context.Context) {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	s.write(wctx)
}
