package mail

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/lograte"
)

// Receiver side of Docs/protocol/mail.md §Dedupe and inbox and §Ack.

const (
	// SeenRetention is how long mail_seen rows are kept. It is longer than
	// MaxAge, so pruning can never re-admit a replay (step 11 rejects it).
	SeenRetention = 35 * 24 * time.Hour

	// ActionIn is the audit action for accepted mail.
	ActionIn = "mail.in"

	// StoreTimeFmt is the stored time format: RFC 3339 UTC, milliseconds.
	StoreTimeFmt = "2006-01-02T15:04:05.000Z"

	// ReceiveMaxAge is how old (by msg.created) an application mail may be and
	// still be processed. The sender's outbox marks a row expired after
	// OutboxExpiry (7 d), but the mail may still be processed until this has
	// passed, so "expired" means delivery unknown. Older mail is neither
	// stored, deduped nor applied.
	ReceiveMaxAge = 14 * 24 * time.Hour

	pruneInterval = 24 * time.Hour

	// maxFailLogged bounds the (from, id) pairs remembered for logFailure. At
	// the bound the set starts over, so a pair may be logged again.
	maxFailLogged = 4096
)

// Kind describes how the receiver processes one known application kind.
type Kind struct {
	// Inbox stores a mail_inbox row for dedupe. Since R55-F13 the row is
	// blank (signed = ''): the plaintext is not kept a second time
	// (Docs/protocol/mail.md §Dedupe and inbox).
	Inbox bool
	// Apply writes the kind-specific rows. It runs inside the dedupe
	// transaction, so it must only use tx. An error rolls everything back and
	// nothing is acked, except an error wrapping ErrBadBody or ErrLimit (see
	// there). An
	// error that quotes body content must wrap ErrBadBody; the receiver never
	// logs an Apply error's text either way (errClass).
	Apply func(ctx context.Context, tx *sql.Tx, op *Opened) error
	// After runs once after the commit of a mail that was not a duplicate. It
	// must not block for long; it may be nil. It runs at most once: a crash
	// between the commit and After loses its effects, as the resend is a
	// duplicate (R55-158); effects that must not be lost belong in Apply.
	After func(ctx context.Context, op *Opened)
}

// PeerKeys returns the newest mailbox public key (32 bytes) announced by a peer.
type PeerKeys interface {
	MailboxPub(peer string) (pub []byte, ok bool)
}

// EnvelopeSender is the relay connection.
type EnvelopeSender interface {
	Send(ctx context.Context, e envelope.Envelope) error
}

// Receiver runs the receiving path: Open, dedupe transaction, then ack.
type Receiver struct {
	Opener *Opener
	DB     *sql.DB
	// Priv loads the own identity key to sign an ack. The receiver clears it after use.
	Priv   func() (ed25519.PrivateKey, error)
	Peers  PeerKeys // to seal acks
	Sender EnvelopeSender
	Audit  AuditSink // mail.in; may be nil
	// Kinds are the application kinds this daemon understands. A kind that is
	// absent is unsupported: recorded in mail_seen and acked as unsupported.
	Kinds map[string]Kind
	// OnKeyMiss is called with each envelope rejected at step 3 (sealed to a
	// key that is not live), to start key-miss recovery. May be nil.
	OnKeyMiss func(ctx context.Context, env envelope.Envelope)
	// OnAck receives each verified ack mail. Acks are never stored or acked.
	OnAck func(op *Opened)
	Log   *slog.Logger
	// Lines limits the receiver's relay-driven log lines (mail_ack_failed);
	// nil means one of its own. The daemon shares the RejectAudit's.
	Lines *lograte.Limiter
	Now   func() time.Time // defaults to time.Now

	linesOnce sync.Once

	commit    func(*sql.Tx) error // test hook; defaults to tx.Commit
	beforeBad func()              // test hook; runs between the two bad-body transactions

	failMu     sync.Mutex
	failLogged map[string]struct{} // (from, id) pairs logFailure has logged
}

// ErrNoKindHandler is returned for mail of kind keys when no handler is
// registered: it is neither stored nor acked, so the sender keeps resending.
var ErrNoKindHandler = errors.New("mail: no handler for kind keys")

// ErrBadBody is wrapped by an Apply error when the verified body is invalid for
// its kind. The receiver rolls back, records only the mail_seen row (marked, so
// a resend is not re-evaluated), audits mail.reject bad_body without any body
// content, and acks the id as rejected: never unsupported, which means an
// unknown kind and triggers the sender's Phase 1 fallback. Docs/protocol/request.md
// §Invalid bodies, Docs/protocol/mail.md §Ack.
var ErrBadBody = errors.New("mail: bad body")

// ReasonBadBody is the audit reason for ErrBadBody.
const ReasonBadBody = "bad_body"

// ErrLimit is wrapped by an Apply error when a per-peer cap refuses the mail
// (Docs/protocol/request.md §Per-peer caps, Docs/protocol/grant.md §Kinds). The
// receiver handles it exactly like ErrBadBody (only the marked mail_seen row,
// acked rejected, a resend re-acked without Apply) but audits mail.reject with
// reason limit.
var ErrLimit = errors.New("mail: limit")

// ReasonLimit is the audit reason for ErrLimit.
const ReasonLimit = "limit"

// badBodyMark is appended to mail_seen.received_at of a bad-body row, so a
// resend is re-acked as rejected without calling Apply and without a
// migration. Prune's string comparison still orders such rows by time.
const badBodyMark = "!bad_body"

func (r *Receiver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Receiver) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// Handle processes one mail envelope from relayclient. Rejections are audited
// by the Opener and returned as *RejectError. The ack is sent only after the
// transaction has committed.
func (r *Receiver) Handle(ctx context.Context, env envelope.Envelope) error {
	op, err := r.Opener.Open(env)
	if err != nil {
		if ReasonOf(err) == ReasonKeyMiss && r.OnKeyMiss != nil {
			r.OnKeyMiss(ctx, env)
		}
		return err
	}
	kind := op.Msg.Kind
	if kind == "ack" {
		if r.OnAck != nil {
			r.OnAck(op)
		}
		return nil
	}
	k, known := r.Kinds[kind]
	if kind == "keys" && !known {
		return ErrNoKindHandler
	}

	// Older than the sender's outbox lifetime (14 d): the sender has already
	// given up on it, so do not store, dedupe or apply it. Ack it as rejected
	// so a late resend stops (Docs/protocol/mail.md §Receive age limit).
	if kind != "keys" && r.now().Sub(op.Msg.Created) > ReceiveMaxAge {
		rerr := reject(11, ReasonStale, nil)
		if r.Opener.Audit != nil {
			r.Opener.Audit.Report(op.Msg.From, op.Msg.ID, rerr) // logged only: a relay can replay old mail
		}
		r.ack(ctx, op.Msg.From, op.Msg.ID, AckRejected)
		return rerr
	}

	res, err := r.store(ctx, op, k, known)
	if err != nil {
		r.logFailure(op, known, err)
		return err
	}
	if res == seenBad || res == seenLimit || res == seenDupBad {
		reason := ReasonBadBody
		if res == seenLimit {
			reason = ReasonLimit
		}
		rerr := reject(11, reason, nil)
		if res != seenDupBad && r.Opener.Audit != nil {
			r.Opener.Audit.Report(op.Msg.From, op.Msg.ID, rerr)
		}
		r.ack(ctx, op.Msg.From, op.Msg.ID, AckRejected)
		return rerr
	}
	dup := res == seenDup
	// The mail is committed: its audit row, After hook and ack must not be lost
	// to a shutdown cancelling ctx now, as no resend would redo them (R55-100).
	ctx = context.WithoutCancel(ctx)
	if !dup && kind != "keys" && r.Audit != nil {
		// A kind this daemon does not register is peer-chosen text: audited
		// as "unknown" (Docs/protocol/mail.md §Audit, R55-F14).
		auditKind := kind
		if !known {
			auditKind = "unknown"
		}
		detail := map[string]string{"peer": op.Msg.From, "id": op.Msg.ID, "kind": auditKind}
		if aerr := r.Audit.Append(ctx, actorDaemon, ActionIn, detail); aerr != nil {
			r.log().Warn("mail: audit failed", "event", "mail_error", "error", aerr)
		}
	}
	if !dup && known && k.After != nil {
		k.After(ctx, op)
	}
	// After commit (or for a duplicate, after rollback): ack.
	member := AckIDs
	if !known {
		member = AckUnsupported
	}
	r.ack(ctx, op.Msg.From, op.Msg.ID, member)
	return nil
}

// stageError is a receive error that is not a rejection, tagged with the fixed
// step of the dedupe transaction that failed. Its text is the same as before
// (mail: <step>: <cause>).
type stageError struct {
	stage  string // fixed name: begin, record seen, read inbox, apply, ...
	prefix string // the message prefix; the stage unless it names the kind
	err    error
}

func stageErr(stage string, err error) error {
	return &stageError{stage: stage, prefix: stage, err: err}
}

func (e *stageError) Error() string { return "mail: " + e.prefix + ": " + e.err.Error() }
func (e *stageError) Unwrap() error { return e.err }

// errClass names the cause of a receive error by a fixed class only: context
// cancellation, an SQLite result code, or "other". A kind's Apply error text is
// never logged, so no body content can reach the log even if an Apply error
// were to quote it (review 70 L2).
func errClass(err error) string {
	var se *sqlite.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.As(err, &se):
		if name, ok := sqlite.ErrorCodeString[se.Code()]; ok {
			return name
		}
		return fmt.Sprintf("sqlite_%d", se.Code())
	}
	return "other"
}

// logFailure logs a receive error that is not a rejection (a database,
// Apply, inbox or commit error): the mail was rolled back and not acked, so the
// sender resends it. Each (from, id) is logged once, not on every redelivery
// (review 55 R55-058). The line names the failed step and the error class
// (errClass), never the error text, so it carries no body content.
func (r *Receiver) logFailure(op *Opened, known bool, err error) {
	key := op.Msg.From + "\x00" + op.Msg.ID
	r.failMu.Lock()
	_, seen := r.failLogged[key]
	if !seen {
		if r.failLogged == nil || len(r.failLogged) >= maxFailLogged {
			r.failLogged = map[string]struct{}{}
		}
		r.failLogged[key] = struct{}{}
	}
	r.failMu.Unlock()
	if seen {
		return
	}
	kind := op.Msg.Kind
	if !known {
		kind = "unknown"
	}
	stage := "other"
	var se *stageError
	if errors.As(err, &se) {
		stage = se.stage
	}
	r.log().Warn("mail: receive failed, not acked", "event", "mail_receive_failed",
		"peer", op.Msg.From, "id", op.Msg.ID, "kind", kind, "stage", stage, "cause", errClass(err))
}

type seenResult int

const (
	seenNew    seenResult = iota // stored
	seenDup                      // repeat of an accepted (from, id)
	seenBad                      // Apply returned ErrBadBody; only mail_seen recorded
	seenDupBad                   // repeat of a bad-body or limit (from, id)
	seenLimit                    // Apply returned ErrLimit; only mail_seen recorded
)

// store runs the single dedupe transaction.
func (r *Receiver) store(ctx context.Context, op *Opened, k Kind, known bool) (seenResult, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return seenNew, stageErr("begin", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful commit
	at := r.now().UTC().Format(StoreTimeFmt)
	res, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO mail_seen (from_key, id, received_at) VALUES (?, ?, ?)`, op.Msg.From, op.Msg.ID, at)
	if err != nil {
		return seenNew, stageErr("record seen", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return seenAs(ctx, tx, op) // rolled back by the deferred Rollback
	}
	// mail_seen is pruned after 35 d, mail_inbox is not: an id still in
	// mail_inbox is a duplicate too, so a re-used id is never applied a
	// second time (review 55 R55-017, mail.md §Dedupe and inbox).
	var inInbox int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mail_inbox WHERE from_key = ? AND id = ?`, op.Msg.From, op.Msg.ID).Scan(&inInbox); err != nil {
		return seenNew, stageErr("read inbox", err)
	}
	if inInbox > 0 {
		return seenDup, nil // rolled back by the deferred Rollback
	}
	if known {
		if k.Apply != nil {
			if err := k.Apply(ctx, tx, op); err != nil {
				if errors.Is(err, ErrBadBody) {
					return r.storeBad(ctx, tx, op, at, seenBad)
				}
				if errors.Is(err, ErrLimit) {
					return r.storeBad(ctx, tx, op, at, seenLimit)
				}
				return seenNew, &stageError{stage: "apply", prefix: "apply " + op.Msg.Kind, err: err}
			}
		}
		if k.Inbox {
			// The row (from_key, id, kind, created, received_at) only
			// deduplicates a redelivery: the signed plaintext is never kept a
			// second time, for any kind (Docs/protocol/mail.md §Inbox rows,
			// R55-F13). Each kind keeps what it needs in its own tables.
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO mail_inbox (from_key, id, kind, created, received_at, signed) VALUES (?, ?, ?, ?, ?, '')`,
				op.Msg.From, op.Msg.ID, op.Msg.Kind, op.Msg.Created.UTC().Format(timeFmt), at); err != nil {
				return seenNew, stageErr("store inbox", err)
			}
		}
	}
	commit := r.commit
	if commit == nil {
		commit = (*sql.Tx).Commit
	}
	if err := commit(tx); err != nil {
		return seenNew, stageErr("commit", err)
	}
	return seenNew, nil
}

// seenAs classifies an existing mail_seen row as a plain or bad-body duplicate.
func seenAs(ctx context.Context, tx *sql.Tx, op *Opened) (seenResult, error) {
	var got string
	if err := tx.QueryRowContext(ctx,
		`SELECT received_at FROM mail_seen WHERE from_key = ? AND id = ?`, op.Msg.From, op.Msg.ID).Scan(&got); err != nil {
		return seenNew, stageErr("read seen", err)
	}
	if strings.HasSuffix(got, badBodyMark) {
		return seenDupBad, nil
	}
	return seenDup, nil
}

// storeBad discards the Apply transaction and records only the marked
// mail_seen row in a new one; ok (seenBad or seenLimit) is the result when it
// records the row. If another delivery of the same (from, id)
// recorded the row in between, that row decides the outcome, so the reject is
// audited once and the ack matches what was stored.
func (r *Receiver) storeBad(ctx context.Context, tx *sql.Tx, op *Opened, at string, ok seenResult) (seenResult, error) {
	_ = tx.Rollback()
	if r.beforeBad != nil {
		r.beforeBad()
	}
	tx2, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return seenNew, stageErr("begin", err)
	}
	defer func() { _ = tx2.Rollback() }()
	res, err := tx2.ExecContext(ctx,
		`INSERT OR IGNORE INTO mail_seen (from_key, id, received_at) VALUES (?, ?, ?)`,
		op.Msg.From, op.Msg.ID, at+badBodyMark)
	if err != nil {
		return seenNew, stageErr("record bad body", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return seenAs(ctx, tx2, op)
	}
	commit := r.commit
	if commit == nil {
		commit = (*sql.Tx).Commit
	}
	if err := commit(tx2); err != nil {
		return seenNew, stageErr("commit", err)
	}
	return ok, nil
}

// Ack members (Docs/protocol/mail.md §Ack): ids for a mail stored or applied,
// unsupported for an unknown kind only, rejected for a known kind refused (a
// bad body or a stale mail).
const (
	AckIDs         = "ids"
	AckUnsupported = "unsupported"
	AckRejected    = "rejected"
)

// ack seals and sends one ack, for one id under one member, directly (not
// outboxed). A rejected ack is always sent alone: a pre-F18 sender refuses a
// whole ack with an unknown member, so any future batching must keep it
// separate (review 69b F3). Failures are logged: the sender resends and
// dedupe re-acks. A relay replaying a genuine mail makes one ack attempt per
// replay, so the failures go to the limited event=mail_ack_failed line
// (Docs/protocol/envelope.md §Relay-driven log lines (daemon), R55-F14).
func (r *Receiver) ack(ctx context.Context, peer, id, member string) {
	pub, ok := r.Peers.MailboxPub(peer)
	if !ok {
		r.ackFailed("no_mailbox_key", id, nil)
		return
	}
	priv, err := r.Priv()
	if err != nil {
		r.ackFailed("seal_failed", id, err)
		return
	}
	defer clear(priv)
	sl, err := Seal(SealInput{
		Priv: priv, To: peer, MailboxPub: pub, Kind: "ack",
		Body: map[string]any{member: []string{id}}, Created: r.now(),
	})
	if err != nil {
		r.ackFailed("seal_failed", id, err)
		return
	}
	e := envelope.Envelope{
		From: envelope.KeyString(priv.Public().(ed25519.PublicKey)), To: peer,
		Type: "mail", ID: sl.ID, TS: r.now().UTC().Format(time.RFC3339), Payload: sl.Payload,
	}
	if err := r.Sender.Send(ctx, e); err != nil {
		r.ackFailed("send_failed", id, err)
	}
}

// ackFailed counts one failed ack into the limited mail_ack_failed line.
// reason is one of the constants no_mailbox_key, seal_failed, send_failed.
func (r *Receiver) ackFailed(reason, id string, err error) {
	args := []any{"reason", reason, "id", id}
	if err != nil {
		args = append(args, "error", err)
	}
	r.lines().Note(slog.LevelWarn, "mail_ack_failed", "mail: acks not sent", reason, args...)
}

// lines returns the limiter of the receiver's relay-driven log lines: Lines
// if set, else one of its own.
func (r *Receiver) lines() *lograte.Limiter {
	r.linesOnce.Do(func() {
		if r.Lines == nil {
			r.Lines = lograte.New(r.log(), 0)
		}
	})
	return r.Lines
}

// Flush writes the pending relay-driven log lines of the receiver and its
// Opener's RejectAudit (daemon stop).
func (r *Receiver) Flush() {
	r.lines().Flush()
	if r.Opener != nil && r.Opener.Audit != nil {
		r.Opener.Audit.Flush()
	}
}

// Prune deletes mail_seen rows received more than SeenRetention before now.
func Prune(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM mail_seen WHERE received_at < ?`,
		now.Add(-SeenRetention).UTC().Format(StoreTimeFmt))
	if err != nil {
		return 0, fmt.Errorf("mail: prune mail_seen: %w", err)
	}
	return res.RowsAffected()
}

// PruneSeenTx is Prune inside tx, for agentnet prune, which must remove
// mail_seen rows before the mail_inbox rows of the same ids in one
// transaction (Docs/protocol/retention.md §Finished items).
func PruneSeenTx(ctx context.Context, tx *sql.Tx, now time.Time) (int64, error) {
	res, err := tx.ExecContext(ctx, `DELETE FROM mail_seen WHERE received_at < ?`,
		now.Add(-SeenRetention).UTC().Format(StoreTimeFmt))
	if err != nil {
		return 0, fmt.Errorf("mail: prune mail_seen: %w", err)
	}
	return res.RowsAffected()
}

// RunPrune prunes at start and then daily, until ctx is cancelled.
func (r *Receiver) RunPrune(ctx context.Context) {
	t := time.NewTicker(pruneInterval)
	defer t.Stop()
	for {
		if _, err := Prune(ctx, r.DB, r.now()); err != nil && ctx.Err() == nil {
			r.log().Warn("mail: prune failed", "event", "mail_error", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
