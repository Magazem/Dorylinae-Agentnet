package mail

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
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
)

// Kind describes how the receiver processes one known application kind.
type Kind struct {
	// Inbox stores a mail_inbox row with the verified plaintext as proof.
	Inbox bool
	// Apply writes the kind-specific rows. It runs inside the dedupe
	// transaction, so it must only use tx. An error rolls everything back and
	// nothing is acked, except an error wrapping ErrBadBody (see there).
	Apply func(ctx context.Context, tx *sql.Tx, op *Opened) error
	// After runs once after the commit of a mail that was not a duplicate. It
	// must not block for long; it may be nil.
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
	Now   func() time.Time // defaults to time.Now

	commit    func(*sql.Tx) error // test hook; defaults to tx.Commit
	beforeBad func()              // test hook; runs between the two bad-body transactions
}

// ErrNoKindHandler is returned for mail of kind keys when no handler is
// registered: it is neither stored nor acked, so the sender keeps resending.
var ErrNoKindHandler = errors.New("mail: no handler for kind keys")

// ErrBadBody is wrapped by an Apply error when the verified body is invalid for
// its kind. The receiver rolls back, records only the mail_seen row (marked, so
// a resend is not re-evaluated), audits mail.reject bad_body without any body
// content, and acks the id as unsupported. Docs/protocol/request.md §Invalid bodies.
var ErrBadBody = errors.New("mail: bad body")

// ReasonBadBody is the audit reason for ErrBadBody.
const ReasonBadBody = "bad_body"

// badBodyMark is appended to mail_seen.received_at of a bad-body row, so a
// resend is re-acked as unsupported without calling Apply and without a
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
	// given up on it, so do not store, dedupe or apply it. Ack it as unsupported
	// so a late resend stops.
	if kind != "keys" && r.now().Sub(op.Msg.Created) > ReceiveMaxAge {
		if r.Opener.Audit != nil {
			r.Opener.Audit.Report(op.Msg.From, op.Msg.ID, ReasonStale)
		}
		r.ack(ctx, op.Msg.From, op.Msg.ID, true)
		return reject(11, ReasonStale, nil)
	}

	res, err := r.store(ctx, op, k, known)
	if err != nil {
		return err
	}
	if res == seenBad || res == seenDupBad {
		if res == seenBad && r.Opener.Audit != nil {
			r.Opener.Audit.Report(op.Msg.From, op.Msg.ID, ReasonBadBody)
		}
		r.ack(ctx, op.Msg.From, op.Msg.ID, true)
		return reject(11, ReasonBadBody, nil)
	}
	dup := res == seenDup
	if !dup && kind != "keys" && r.Audit != nil {
		detail := map[string]string{"peer": op.Msg.From, "id": op.Msg.ID, "kind": kind}
		if aerr := r.Audit.Append(ctx, actorDaemon, ActionIn, detail); aerr != nil {
			r.log().Warn("mail: audit failed", "event", "mail_error", "error", aerr)
		}
	}
	if !dup && known && k.After != nil {
		k.After(ctx, op)
	}
	// After commit (or for a duplicate, after rollback): ack.
	r.ack(ctx, op.Msg.From, op.Msg.ID, !known)
	return nil
}

type seenResult int

const (
	seenNew    seenResult = iota // stored
	seenDup                      // repeat of an accepted (from, id)
	seenBad                      // Apply returned ErrBadBody; only mail_seen recorded
	seenDupBad                   // repeat of a bad-body (from, id)
)

// store runs the single dedupe transaction.
func (r *Receiver) store(ctx context.Context, op *Opened, k Kind, known bool) (seenResult, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return seenNew, fmt.Errorf("mail: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful commit
	at := r.now().UTC().Format(StoreTimeFmt)
	res, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO mail_seen (from_key, id, received_at) VALUES (?, ?, ?)`, op.Msg.From, op.Msg.ID, at)
	if err != nil {
		return seenNew, fmt.Errorf("mail: record seen: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return seenAs(ctx, tx, op) // rolled back by the deferred Rollback
	}
	if known {
		if k.Apply != nil {
			if err := k.Apply(ctx, tx, op); err != nil {
				if errors.Is(err, ErrBadBody) {
					return r.storeBad(ctx, tx, op, at)
				}
				return seenNew, fmt.Errorf("mail: apply %s: %w", op.Msg.Kind, err)
			}
		}
		if k.Inbox {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO mail_inbox (from_key, id, kind, created, received_at, signed) VALUES (?, ?, ?, ?, ?, ?)`,
				op.Msg.From, op.Msg.ID, op.Msg.Kind, op.Msg.Created.UTC().Format(timeFmt), at, string(op.Signed)); err != nil {
				return seenNew, fmt.Errorf("mail: store inbox: %w", err)
			}
		}
	}
	commit := r.commit
	if commit == nil {
		commit = (*sql.Tx).Commit
	}
	if err := commit(tx); err != nil {
		return seenNew, fmt.Errorf("mail: commit: %w", err)
	}
	return seenNew, nil
}

// StoreInboxTx stores a mail_inbox row for op inside tx, exactly as the
// generic path above does for a Kind with Inbox: true. It is for a kind whose
// Apply decides at receive time whether to keep the plaintext (Inbox: false
// at registration, so the generic path above does not also store it): pass
// signed as op.Signed to keep it, or nil to store the row without content
// (Docs/protocol/work-session.md §Early complete and Phase 1 workers, review
// 27 H1 option (a)). The row's presence (from_key, id, kind, created,
// received_at) is unaffected either way: dedupe is mail_seen, not this table,
// so omitting the plaintext here never causes a resend or a re-apply.
func StoreInboxTx(ctx context.Context, tx *sql.Tx, op *Opened, signed []byte, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO mail_inbox (from_key, id, kind, created, received_at, signed) VALUES (?, ?, ?, ?, ?, ?)`,
		op.Msg.From, op.Msg.ID, op.Msg.Kind, op.Msg.Created.UTC().Format(timeFmt), now.UTC().Format(StoreTimeFmt), string(signed)); err != nil {
		return fmt.Errorf("mail: store inbox: %w", err)
	}
	return nil
}

// seenAs classifies an existing mail_seen row as a plain or bad-body duplicate.
func seenAs(ctx context.Context, tx *sql.Tx, op *Opened) (seenResult, error) {
	var got string
	if err := tx.QueryRowContext(ctx,
		`SELECT received_at FROM mail_seen WHERE from_key = ? AND id = ?`, op.Msg.From, op.Msg.ID).Scan(&got); err != nil {
		return seenNew, fmt.Errorf("mail: read seen: %w", err)
	}
	if strings.HasSuffix(got, badBodyMark) {
		return seenDupBad, nil
	}
	return seenDup, nil
}

// storeBad discards the Apply transaction and records only the marked
// mail_seen row in a new one. If another delivery of the same (from, id)
// recorded the row in between, that row decides the outcome, so the reject is
// audited once and the ack matches what was stored.
func (r *Receiver) storeBad(ctx context.Context, tx *sql.Tx, op *Opened, at string) (seenResult, error) {
	_ = tx.Rollback()
	if r.beforeBad != nil {
		r.beforeBad()
	}
	tx2, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return seenNew, fmt.Errorf("mail: begin: %w", err)
	}
	defer func() { _ = tx2.Rollback() }()
	res, err := tx2.ExecContext(ctx,
		`INSERT OR IGNORE INTO mail_seen (from_key, id, received_at) VALUES (?, ?, ?)`,
		op.Msg.From, op.Msg.ID, at+badBodyMark)
	if err != nil {
		return seenNew, fmt.Errorf("mail: record bad body: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return seenAs(ctx, tx2, op)
	}
	commit := r.commit
	if commit == nil {
		commit = (*sql.Tx).Commit
	}
	if err := commit(tx2); err != nil {
		return seenNew, fmt.Errorf("mail: commit: %w", err)
	}
	return seenBad, nil
}

// ack seals and sends one ack directly (not outboxed). Failures are logged:
// the sender resends and dedupe re-acks.
func (r *Receiver) ack(ctx context.Context, peer, id string, unsupported bool) {
	pub, ok := r.Peers.MailboxPub(peer)
	if !ok {
		r.log().Warn("mail: cannot ack, no mailbox key for peer", "event", "ack_no_mailbox_key", "id", id)
		return
	}
	member := "ids"
	if unsupported {
		member = "unsupported"
	}
	priv, err := r.Priv()
	if err != nil {
		r.log().Warn("mail: cannot load identity key for ack", "event", "ack_seal_failed", "id", id, "error", err)
		return
	}
	defer clear(priv)
	sl, err := Seal(SealInput{
		Priv: priv, To: peer, MailboxPub: pub, Kind: "ack",
		Body: map[string]any{member: []string{id}}, Created: r.now(),
	})
	if err != nil {
		r.log().Warn("mail: cannot seal ack", "event", "ack_seal_failed", "id", id, "error", err)
		return
	}
	e := envelope.Envelope{
		From: envelope.KeyString(priv.Public().(ed25519.PublicKey)), To: peer,
		Type: "mail", ID: sl.ID, TS: r.now().UTC().Format(time.RFC3339), Payload: sl.Payload,
	}
	if err := r.Sender.Send(ctx, e); err != nil {
		r.log().Warn("mail: ack not sent", "event", "ack_send_failed", "id", id, "error", err)
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
