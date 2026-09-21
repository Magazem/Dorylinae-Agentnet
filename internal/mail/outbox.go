package mail

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// Sender outbox of Docs/protocol/mail.md §Outbox: submitted mail is stored,
// then resent with the same envelope id until the recipient acks it.

// Outbox states.
const (
	StateQueued    = "queued"
	StateRelayed   = "relayed"
	StateDelivered = "delivered"
	StateExpired   = "expired"
	StateFailed    = "failed"
)

const (
	// OutboxExpiry is how long an unacked mail is resent (from msg.created).
	OutboxExpiry = 7 * 24 * time.Hour
	// OutboxRetention is how long final rows are kept after their last update.
	OutboxRetention = 30 * 24 * time.Hour

	// ActionExpired is the audit action for mail that was never acked.
	ActionExpired = "mail.expired"

	outboxTick        = time.Second
	outboxMaintenance = time.Minute
	outboxBatch       = 50
)

// Submit refusals.
var (
	ErrUnpaired     = errors.New("unpaired")
	ErrNoMailboxKey = errors.New("no_mailbox_key")
	ErrKindNotSent  = errors.New("kind ack is never outboxed")
)

// OutboxPeers is what the outbox asks of the peers table.
type OutboxPeers interface {
	IsPaired(key string) bool
	PeerKeys
}

// Outbox stores submitted mail and resends it until it is acked.
type Outbox struct {
	DB *sql.DB
	// Priv loads the own identity key. The outbox clears it after use.
	Priv  func() (ed25519.PrivateKey, error)
	Peers OutboxPeers
	// Sender is the relay connection; nil counts as not connected.
	Sender EnvelopeSender
	Audit  AuditSink // mail.expired; may be nil
	Log    *slog.Logger
	Now    func() time.Time // defaults to time.Now
	// Tick is how often due rows are looked for. Defaults to one second.
	Tick time.Duration

	once sync.Once
	wake chan struct{}
}

// Submitted is the result of Submit.
type Submitted struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// OutboxCounts is the outbox part of the status report.
type OutboxCounts struct {
	Queued  int `json:"queued"`
	Relayed int `json:"relayed"`
	Expired int `json:"expired"`
}

type outboxRow struct {
	id, to, frame, keyID, signed, state string
	attempts                            int
}

func (o *Outbox) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *Outbox) log() *slog.Logger {
	if o.Log != nil {
		return o.Log
	}
	return slog.Default()
}

func (o *Outbox) wakeCh() chan struct{} {
	o.once.Do(func() { o.wake = make(chan struct{}, 1) })
	return o.wake
}

// Wake asks the worker to look for due rows now.
func (o *Outbox) Wake() {
	select {
	case o.wakeCh() <- struct{}{}:
	default:
	}
}

func stamp(t time.Time) string { return t.UTC().Format(StoreTimeFmt) }

// Submit signs and seals a mail to a paired peer and stores it as queued. It
// does not touch the relay, so it returns at once; the worker sends it.
func (o *Outbox) Submit(ctx context.Context, to, kind string, body any) (Submitted, error) {
	if kind == "ack" {
		return Submitted{}, ErrKindNotSent
	}
	if !o.Peers.IsPaired(to) {
		return Submitted{}, ErrUnpaired
	}
	pub, ok := o.Peers.MailboxPub(to)
	if !ok {
		return Submitted{}, ErrNoMailboxKey
	}
	priv, err := o.Priv()
	if err != nil {
		return Submitted{}, fmt.Errorf("mail: load identity key: %w", err)
	}
	defer clear(priv)
	now := o.now()
	sl, err := Seal(SealInput{Priv: priv, To: to, MailboxPub: pub, Kind: kind, Body: body, Created: now})
	if err != nil {
		return Submitted{}, err
	}
	frame, err := buildFrame(priv, to, sl, now)
	if err != nil {
		return Submitted{}, err
	}
	if _, err := o.DB.ExecContext(ctx,
		`INSERT INTO outbox (id, to_key, kind, created, key_id, signed, frame, state, attempts, next_attempt, updated)
VALUES (?, ?, ?, ?, ?, ?, ?, 'queued', 0, ?, ?)`,
		sl.ID, to, kind, now.UTC().Truncate(time.Second).Format(timeFmt), sl.KeyID.String(),
		string(sl.Signed), frame, stamp(now), stamp(now)); err != nil {
		return Submitted{}, fmt.Errorf("mail: store outbox row: %w", err)
	}
	o.Wake()
	return Submitted{ID: sl.ID, State: StateQueued}, nil
}

func buildFrame(priv ed25519.PrivateKey, to string, sl Sealed, now time.Time) (string, error) {
	e := envelope.Envelope{
		From: envelope.KeyString(priv.Public().(ed25519.PublicKey)), To: to,
		Type: MailType, ID: sl.ID, TS: now.UTC().Format(time.RFC3339), Payload: sl.Payload,
	}
	raw, err := e.Marshal()
	if err != nil {
		return "", fmt.Errorf("mail: build envelope: %w", err)
	}
	return string(raw), nil
}

// MailType is the envelope type of every mail.
const MailType = "mail"

// Run is the resend worker. It returns when ctx is cancelled.
func (o *Outbox) Run(ctx context.Context) {
	tick := o.Tick
	if tick <= 0 {
		tick = outboxTick
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	var lastMaint time.Time
	for {
		now := o.now()
		if now.Sub(lastMaint) >= outboxMaintenance {
			lastMaint = now
			o.maintain(ctx, now)
		}
		o.sendDue(ctx, now)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-o.wakeCh():
		}
	}
}

// OnReady is the relay-connected hook: every queued row is sent at once.
func (o *Outbox) OnReady(ctx context.Context) {
	if _, err := o.DB.ExecContext(ctx,
		`UPDATE outbox SET next_attempt = ? WHERE state = 'queued'`, stamp(o.now())); err != nil {
		o.log().Warn("mail: outbox reconnect", "event", "mail_error", "error", err)
		return
	}
	o.Wake()
}

// OnPeerOnline is the presence-online hook of Phase 1.2. Nothing to do until then.
func (o *Outbox) OnPeerOnline(string) {}

// Counts reports the rows in each non-final state and the expired ones.
func (o *Outbox) Counts(ctx context.Context) (OutboxCounts, error) {
	rows, err := o.DB.QueryContext(ctx, `SELECT state, COUNT(*) FROM outbox GROUP BY state`)
	if err != nil {
		return OutboxCounts{}, err
	}
	defer func() { _ = rows.Close() }()
	var c OutboxCounts
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return OutboxCounts{}, err
		}
		switch s {
		case StateQueued:
			c.Queued = n
		case StateRelayed:
			c.Relayed = n
		case StateExpired:
			c.Expired = n
		}
	}
	return c, rows.Err()
}

func (o *Outbox) maintain(ctx context.Context, now time.Time) {
	cutoff := now.Add(-OutboxExpiry).UTC().Format(timeFmt)
	type ex struct{ id, to, kind string }
	var due []ex
	rows, err := o.DB.QueryContext(ctx,
		`SELECT id, to_key, kind FROM outbox WHERE state IN ('queued','relayed') AND created <= ?`, cutoff)
	if err != nil {
		o.log().Warn("mail: outbox expiry", "event", "mail_error", "error", err)
		return
	}
	for rows.Next() {
		var e ex
		if err := rows.Scan(&e.id, &e.to, &e.kind); err == nil {
			due = append(due, e)
		}
	}
	_ = rows.Close()
	for _, e := range due {
		if o.finish(ctx, e.id, "", StateExpired, "") && o.Audit != nil {
			detail := map[string]string{"peer": e.to, "id": e.id, "kind": e.kind}
			if aerr := o.Audit.Append(ctx, actorDaemon, ActionExpired, detail); aerr != nil {
				o.log().Warn("mail: audit failed", "event", "mail_error", "error", aerr)
			}
		}
	}
	if _, err := o.DB.ExecContext(ctx,
		`DELETE FROM outbox WHERE state IN ('delivered','expired','failed') AND updated < ?`,
		stamp(now.Add(-OutboxRetention))); err != nil {
		o.log().Warn("mail: outbox purge", "event", "mail_error", "error", err)
	}
}

// finish moves a non-final row to a final state and clears its plaintext. It
// reports whether a row changed. to, if not empty, must be the row's recipient.
func (o *Outbox) finish(ctx context.Context, id, to, state, errText string) bool {
	res, err := o.DB.ExecContext(ctx,
		`UPDATE outbox SET state = ?, signed = NULL, frame = NULL, next_attempt = NULL, updated = ?, error = NULLIF(?, '')
WHERE id = ? AND (? = '' OR to_key = ?) AND state IN ('queued','relayed')`,
		state, stamp(o.now()), errText, id, to, to)
	if err != nil {
		o.log().Warn("mail: outbox update", "event", "mail_error", "error", err)
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (o *Outbox) sendDue(ctx context.Context, now time.Time) {
	rows, err := o.DB.QueryContext(ctx,
		`SELECT id, to_key, frame, attempts FROM outbox
WHERE state IN ('queued','relayed') AND next_attempt <= ? ORDER BY next_attempt LIMIT ?`, stamp(now), outboxBatch)
	if err != nil {
		if ctx.Err() == nil {
			o.log().Warn("mail: outbox scan", "event", "mail_error", "error", err)
		}
		return
	}
	var due []outboxRow
	for rows.Next() {
		var r outboxRow
		var frame sql.NullString
		if err := rows.Scan(&r.id, &r.to, &frame, &r.attempts); err == nil {
			r.frame = frame.String
			due = append(due, r)
		}
	}
	_ = rows.Close()
	for _, r := range due {
		if ctx.Err() != nil {
			return
		}
		if !o.Peers.IsPaired(r.to) {
			o.finish(ctx, r.id, "", StateFailed, ErrUnpaired.Error())
			continue
		}
		o.send(ctx, r)
	}
}

// send hands the row's stored frame to the relay and schedules the next attempt.
func (o *Outbox) send(ctx context.Context, r outboxRow) {
	e, err := envelope.Parse([]byte(r.frame))
	if err != nil {
		o.finish(ctx, r.id, "", StateFailed, "bad_frame")
		return
	}
	if o.Sender == nil {
		err = errors.New("no relay")
	} else {
		err = o.Sender.Send(ctx, e)
	}
	now := o.now()
	next := stamp(now.Add(backoff(r.attempts + 1)))
	q := `UPDATE outbox SET attempts = attempts + 1, next_attempt = ?, updated = ?, state = CASE WHEN ? THEN 'relayed' ELSE state END
WHERE id = ? AND state IN ('queued','relayed')`
	if _, uerr := o.DB.ExecContext(ctx, q, next, stamp(now), err == nil, r.id); uerr != nil && ctx.Err() == nil {
		o.log().Warn("mail: outbox update", "event", "mail_error", "error", uerr)
	}
}

// backoff is the delay after the n-th hand-off attempt, with 10% jitter.
func backoff(n int) time.Duration {
	d := 6 * time.Hour
	switch n {
	case 1:
		d = time.Minute
	case 2:
		d = 5 * time.Minute
	case 3:
		d = 30 * time.Minute
	}
	return time.Duration(float64(d) * (0.9 + 0.2*rand.Float64())) //nolint:gosec // jitter needs no crypto randomness
}

// OnAck applies a verified ack mail: delivered for ids, failed for unsupported.
// Ids that are unknown, addressed to another peer or already final are ignored.
func (o *Outbox) OnAck(op *Opened) {
	ctx := context.Background()
	for _, id := range stringList(op.Msg.Body["ids"]) {
		o.finish(ctx, id, op.Msg.From, StateDelivered, "")
	}
	for _, id := range stringList(op.Msg.Body["unsupported"]) {
		o.finish(ctx, id, op.Msg.From, StateFailed, "unsupported_kind")
	}
}

// HandleError applies a relay error frame that refers to an outbox row.
func (o *Outbox) HandleError(ef envelope.ErrorFrame) {
	if !ValidID(ef.Ref) {
		return
	}
	ctx := context.Background()
	switch ef.Code {
	case envelope.CodeBadEnvelope, envelope.CodeBadSender:
		o.finish(ctx, ef.Ref, "", StateFailed, ef.Code)
	case envelope.CodeQueueFull, envelope.CodeInternal, envelope.CodePeerOffline, envelope.CodePeerBusy:
		// Temporary: back to queued and keep the backoff.
		if _, err := o.DB.ExecContext(ctx,
			`UPDATE outbox SET state = 'queued', updated = ? WHERE id = ? AND state = 'relayed'`,
			stamp(o.now()), ef.Ref); err != nil {
			o.log().Warn("mail: outbox update", "event", "mail_error", "error", err)
		}
	}
}

// Retry is the key-miss recovery of the sender: peer could not decrypt the
// mails in ids. Each one that is still open and was sealed to another key than
// the peer's newest is re-sealed (same id and created, new ts) and sent now.
func (o *Outbox) Retry(ctx context.Context, peer string, ids []string) {
	pub, ok := o.Peers.MailboxPub(peer)
	if !ok {
		return
	}
	newKey := KeyIDOf(pub).String()
	priv, err := o.Priv()
	if err != nil {
		o.log().Warn("mail: reseal: load identity key", "event", "mail_error", "error", err)
		return
	}
	defer clear(priv)
	from := envelope.KeyString(priv.Public().(ed25519.PublicKey))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] || !ValidID(id) {
			continue
		}
		seen[id] = true
		var r outboxRow
		var signed, keyID sql.NullString
		err := o.DB.QueryRowContext(ctx,
			`SELECT id, to_key, key_id, signed, attempts FROM outbox
WHERE id = ? AND to_key = ? AND state IN ('queued','relayed') AND signed IS NOT NULL`, id, peer).
			Scan(&r.id, &r.to, &keyID, &signed, &r.attempts)
		if err != nil || keyID.String == newKey {
			continue
		}
		sl, err := Reseal([]byte(signed.String), from, peer, pub)
		if err != nil {
			o.log().Warn("mail: reseal failed", "event", "mail_error", "id", id, "error", err)
			continue
		}
		now := o.now()
		frame, err := buildFrame(priv, peer, sl, now)
		if err != nil {
			continue
		}
		// The row may have become final (ack, expiry, peer removed) or been
		// re-sealed since the SELECT: then nothing is sent.
		res, err := o.DB.ExecContext(ctx,
			`UPDATE outbox SET frame = ?, key_id = ?, updated = ? WHERE id = ? AND to_key = ? AND key_id IS ? AND state IN ('queued','relayed')`,
			frame, sl.KeyID.String(), stamp(now), id, peer, keyID)
		if err != nil {
			o.log().Warn("mail: outbox update", "event", "mail_error", "error", err)
			continue
		}
		if n, _ := res.RowsAffected(); n != 1 {
			continue
		}
		r.frame = frame
		o.send(ctx, r)
	}
}

func stringList(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
