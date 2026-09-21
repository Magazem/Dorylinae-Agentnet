package mail

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ActionReject is the audit action for a rejected mail envelope.
const ActionReject = "mail.reject"

const (
	rejectsPerMinute = 30
	auditBudget      = 2 * time.Second
	actorDaemon      = "daemon"
)

// AuditSink is the part of audit.Log that RejectAudit needs.
type AuditSink interface {
	Append(ctx context.Context, actor, action string, detail any) error
}

// RejectAudit records mail.reject events, at most 30 per minute like
// session.reject. The rest are counted in the log as
// event=mail_reject_suppressed. It never sees payload bytes.
type RejectAudit struct {
	sink AuditSink
	log  *slog.Logger
	now  func() time.Time

	mu         sync.Mutex
	window     time.Time
	count      int
	suppressed int
}

// NewRejectAudit returns a RejectAudit over sink. log may be nil.
func NewRejectAudit(sink AuditSink, log *slog.Logger) *RejectAudit {
	if log == nil {
		log = slog.Default()
	}
	return &RejectAudit{sink: sink, log: log, now: time.Now}
}

// Report audits one rejection {peer, id, reason}, unless rate limited.
func (a *RejectAudit) Report(peer, id, reason string) {
	a.log.Info("mail envelope rejected", "event", "mail_reject", "reason", reason, "id", id)
	now := a.now()
	a.mu.Lock()
	if now.Sub(a.window) >= time.Minute {
		if a.suppressed > 0 {
			a.log.Warn("mail rejects not audited", "event", "mail_reject_suppressed", "count", a.suppressed)
		}
		a.window, a.count, a.suppressed = now, 0, 0
	}
	allowed := a.count < rejectsPerMinute
	if allowed {
		a.count++
	} else {
		a.suppressed++
	}
	a.mu.Unlock()
	if !allowed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditBudget)
	defer cancel()
	detail := map[string]string{"peer": peer, "id": id, "reason": reason}
	if err := a.sink.Append(ctx, actorDaemon, ActionReject, detail); err != nil {
		a.log.Warn("mail: audit failed", "event", "mail_error", "error", err)
	}
}
