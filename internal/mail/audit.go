package mail

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/lograte"
)

// ActionReject is the audit action for a rejected mail envelope.
const ActionReject = "mail.reject"

const (
	rejectsPerMinute = 30
	auditBudget      = 2 * time.Second
	actorDaemon      = "daemon"

	// maxAuditedSeen bounds the (peer, id) pairs remembered as already
	// audited in this run (Docs/protocol/mail.md §Receiving, OD-F14-5).
	maxAuditedSeen = 4096

	// reasonLimit is R55-F13's reject reason for an application mail refused
	// by a per-peer cap: a verified paired peer's mail, so it is audited.
	reasonLimit = "limit"
)

// AuditSink is the part of audit.Log that RejectAudit needs.
type AuditSink interface {
	Append(ctx context.Context, actor, action string, detail any) error
}

// RejectAudit records rejected mail (Docs/protocol/mail.md §Receiving:
// verification order, R55-F14). Every reject is counted into the limited
// event=mail_reject log line. Only rejects a relay alone cannot cause are
// audited as mail.reject: once per (peer, id) per run and at most 30 per
// minute; the rest of those are counted in the log as
// event=mail_reject_suppressed. It never sees payload bytes.
type RejectAudit struct {
	sink  AuditSink
	log   *slog.Logger
	lines *lograte.Limiter
	now   func() time.Time

	// CountLogged, if set, is called with the reason of every reject that is
	// logged only, except unpaired (the daily relay.reject_summary, OD-F14-7).
	// Set it before the first Report.
	CountLogged func(reason string)

	mu         sync.Mutex
	window     time.Time
	count      int
	suppressed int
	seen       map[string]struct{} // audited (peer, id) pairs
	seenRing   []string            // FIFO order of seen, at most maxAuditedSeen
}

// NewRejectAudit returns a RejectAudit over sink. log may be nil.
func NewRejectAudit(sink AuditSink, log *slog.Logger) *RejectAudit {
	if log == nil {
		log = slog.Default()
	}
	return &RejectAudit{sink: sink, log: log, lines: lograte.New(log, 0), now: time.Now, seen: map[string]struct{}{}}
}

// Lines returns the limiter of the mail layer's relay-driven log lines, for
// the receiver's mail_ack_failed line.
func (a *RejectAudit) Lines() *lograte.Limiter { return a.lines }

// Flush writes the pending log lines (daemon stop).
func (a *RejectAudit) Flush() { a.lines.Flush() }

// auditable reports whether a relay alone could not have caused re: a check
// of content the peer signed (steps 8, 9, 10, 12), except a keys announcement
// that has only expired (a replay causes it), and bad_body or limit.
func auditable(re *RejectError) bool {
	if re.Reason == ReasonBadBody || re.Reason == reasonLimit {
		return true
	}
	if re.Step < 8 || re.Step > 12 || re.Step == 11 {
		return false
	}
	return re.Step != 12 || !errors.Is(re.Err, ErrAnnouncementExpired)
}

// Report handles one rejection. err is the *RejectError of the reject; any
// other error is counted as a reject without a reason and not audited.
func (a *RejectAudit) Report(peer, id string, err error) {
	var re *RejectError
	if !errors.As(err, &re) {
		re = &RejectError{}
	}
	a.lines.Note(slog.LevelInfo, "mail_reject", "mail envelopes rejected", re.Reason,
		"reason", re.Reason, "step", re.Step, "peer", peer)
	if !auditable(re) {
		if re.Reason != ReasonUnpaired && a.CountLogged != nil {
			a.CountLogged(re.Reason)
		}
		return
	}
	key := peer + "\x00" + id
	now := a.now()
	a.mu.Lock()
	if _, dup := a.seen[key]; dup {
		a.mu.Unlock()
		return
	}
	if now.Sub(a.window) >= time.Minute {
		if a.suppressed > 0 {
			a.log.Warn("mail rejects not audited", "event", "mail_reject_suppressed", "count", a.suppressed)
		}
		a.window, a.count, a.suppressed = now, 0, 0
	}
	allowed := a.count < rejectsPerMinute
	if allowed {
		a.count++
		if len(a.seenRing) >= maxAuditedSeen {
			delete(a.seen, a.seenRing[0])
			a.seenRing = a.seenRing[1:]
		}
		a.seen[key] = struct{}{}
		a.seenRing = append(a.seenRing, key)
	} else {
		a.suppressed++
	}
	a.mu.Unlock()
	if !allowed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditBudget)
	defer cancel()
	detail := map[string]string{"peer": peer, "reason": re.Reason}
	if ValidID(id) {
		detail["id"] = id
	}
	if err := a.sink.Append(ctx, actorDaemon, ActionReject, detail); err != nil {
		a.log.Warn("mail: audit failed", "event", "mail_error", "error", err)
	}
}
