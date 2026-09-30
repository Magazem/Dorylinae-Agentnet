package daemon

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// mailQueue is how many mail envelopes may wait for the receiver, and
// mailQueueBytes how many decoded payload bytes (R55-F13, Docs/protocol/mail.md
// §Receive queue). The relay read loop must not block; a dropped envelope is
// resent by its sender.
const (
	mailQueue      = 256
	mailQueueBytes = 64 << 20
)

// mailDropWindow is how often at most the dropped mail envelopes are logged:
// one mail_queue_drop line per window.
var mailDropWindow = time.Minute

// peerDirectory answers the mail package's Peers and PeerKeys questions from
// the peers table.
type peerDirectory struct{ db *sql.DB }

// rowQuerier is satisfied by both *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func (d peerDirectory) IsPaired(key string) bool { return isPairedQ(d.db, key) }

// IsPairedTx is mail.TxOutboxPeers, read through the caller's transaction
// instead of the connection pool (see mail.TxOutboxPeers).
func (d peerDirectory) IsPairedTx(tx *sql.Tx, key string) bool { return isPairedQ(tx, key) }

func isPairedQ(q rowQuerier, key string) bool {
	var one int
	return q.QueryRow(`SELECT 1 FROM peers WHERE public_key = ?`, key).Scan(&one) == nil
}

// MailboxPub returns the pub of the newest stored announcement of a peer.
func (d peerDirectory) MailboxPub(peer string) ([]byte, bool) { return mailboxPubQ(d.db, peer) }

// MailboxPubTx is mail.TxOutboxPeers, read through the caller's transaction
// instead of the connection pool (see mail.TxOutboxPeers).
func (d peerDirectory) MailboxPubTx(tx *sql.Tx, peer string) ([]byte, bool) {
	return mailboxPubQ(tx, peer)
}

func mailboxPubQ(q rowQuerier, peer string) ([]byte, bool) {
	var raw string
	if err := q.QueryRow(`SELECT mailbox_keys FROM peers WHERE public_key = ?`, peer).Scan(&raw); err != nil {
		return nil, false
	}
	var anns []struct {
		Announcement struct {
			Pub string `json:"pub"`
		} `json:"announcement"`
	}
	if json.Unmarshal([]byte(raw), &anns) != nil || len(anns) == 0 {
		return nil, false
	}
	pub, err := base64.RawURLEncoding.DecodeString(anns[0].Announcement.Pub)
	if err != nil {
		return nil, false
	}
	return pub, true
}

// PeersWithKeys returns the paired peers that have a mailbox key, for the
// rotation push.
func (d peerDirectory) PeersWithKeys(ctx context.Context) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT public_key FROM peers WHERE mailbox_keys <> '[]' ORDER BY public_key`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ownKeys is what the daemon needs of its own mailbox keys beyond mail.Keys
// (implemented by *mailbox.Keys): the current announcement, the rotation hook
// and the rotation job. Options.MailboxKeys without it gets no rotation.
type ownKeys interface {
	mail.Keys
	Announcement() ([]byte, error)
	OnRotate(func(announcement []byte))
	Run(ctx context.Context)
}

// newMailReceiver builds the receiver and the pusher of keys mail. keys supplies
// the own mailbox private keys (created by pairing and rotation, tickets 0.8c
// and 1.0b). Both need Sender, which startMail sets.
func newMailReceiver(db *sql.DB, log *audit.Log, idKey *identityKey, self ed25519.PublicKey, keys mail.Keys, lg *slog.Logger, ts *team.Store, rs *request.Store, ws *worksession.Store, caps *capability.Store) (*mail.Receiver, *mail.Pusher) {
	dir := peerDirectory{db}
	selfKey := envelope.KeyString(self)
	priv := idKey.Priv
	pusher := &mail.Pusher{Priv: priv, Peers: dir, List: dir.PeersWithKeys, Log: lg}
	rcv := &mail.Receiver{
		Opener: &mail.Opener{
			Self: selfKey, Peers: dir, Keys: keys,
			Audit: mail.NewRejectAudit(log, lg),
		},
		DB:    db,
		Peers: dir,
		Audit: log,
		Priv:  priv,
		Log:   lg,
		Kinds: map[string]mail.Kind{
			// startMail replaces the nil hook with the outbox re-seal (1.0e).
			"keys": mail.KeysKind(peers.MergeMailboxKeysTx, nil),
		},
	}
	for k, v := range daemonKinds(ts, rs, ws, caps, selfKey, log) {
		rcv.Kinds[k] = v
	}
	if os.Getenv(mail.DebugEnv) == "1" {
		// Debug only: lets `agentnet mail send --kind note` exercise the mail
		// path before Phase 1 brings real kinds. Stored to the inbox, nothing else.
		rcv.Kinds[mail.DebugKind] = mail.Kind{Inbox: true}
	}
	if rot, ok := keys.(ownKeys); ok {
		km := &mail.KeyMiss{Pusher: pusher, Announcement: rot.Announcement, Log: lg}
		rcv.OnKeyMiss = func(ctx context.Context, env envelope.Envelope) { km.Note(ctx, env.From, env.ID) }
	}
	return rcv, pusher
}

// startMail runs the receiver on its own goroutine and returns the function
// the relay read loop calls for each mail envelope, plus a stop function.
func startMail(ctx context.Context, rcv *mail.Receiver, pusher *mail.Pusher, client *relayclient.Client, keys mail.Keys, ob *mail.Outbox) (handle func(envelope.Envelope), stop func()) {
	rcv.Sender = client
	pusher.Sender = client
	ob.Sender = client
	rcv.OnAck = ob.OnAck
	rcv.Kinds["keys"] = mail.KeysKind(peers.MergeMailboxKeysTx, ob.Retry)
	pusher.Outbox = func(ctx context.Context, peer string, body map[string]any) error {
		_, err := ob.Submit(ctx, peer, "keys", body)
		return err
	}
	q := newMailInbox(rcv.Log)
	mctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go rcv.RunPrune(mctx)
	if rot, ok := keys.(ownKeys); ok {
		// A new key is pushed to every peer. The job starts after the hook is set.
		rot.OnRotate(func(ann []byte) { go pusher.PushAll(mctx, ann) })
		go rot.Run(mctx)
	}
	go func() {
		defer close(done)
		for {
			select {
			case <-mctx.Done():
				return
			case e := <-q.ch:
				q.bytes.Add(-int64(len(e.Payload)))
				_ = rcv.Handle(mctx, e) // rejections are audited by the receiver
			}
		}
	}()
	return q.push, func() {
		cancel()
		<-done
		q.flush()
	}
}

// mailInbox is the receive queue of Docs/protocol/mail.md §Receive queue: at
// most mailQueue envelopes and mailQueueBytes of decoded payload. bytes is
// added before an envelope is queued and taken off when the receiver gets it.
type mailInbox struct {
	ch    chan envelope.Envelope
	bytes atomic.Int64
	log   *slog.Logger

	mu        sync.Mutex
	dropped   int
	dropBytes int64
	timer     *time.Timer
}

func newMailInbox(lg *slog.Logger) *mailInbox {
	if lg == nil {
		lg = slog.Default()
	}
	return &mailInbox{ch: make(chan envelope.Envelope, mailQueue), log: lg}
}

// push queues e, or drops it when either bound is reached (the sender's
// outbox resends it). It never blocks.
func (q *mailInbox) push(e envelope.Envelope) {
	n := int64(len(e.Payload))
	if q.bytes.Add(n) > mailQueueBytes {
		q.bytes.Add(-n)
		q.drop(n)
		return
	}
	select {
	case q.ch <- e:
	default:
		q.bytes.Add(-n)
		q.drop(n)
	}
}

// drop counts a dropped envelope into the next mail_queue_drop line.
func (q *mailInbox) drop(n int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.dropped++
	q.dropBytes += n
	if q.timer == nil {
		q.timer = time.AfterFunc(mailDropWindow, q.flush)
	}
}

// flush logs the drops counted so far, if any. Never audited.
func (q *mailInbox) flush() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.timer != nil {
		q.timer.Stop()
		q.timer = nil
	}
	if q.dropped == 0 {
		return
	}
	q.log.Warn("mail receive queue full, envelopes dropped", "event", "mail_queue_drop", "count", q.dropped, "bytes", q.dropBytes)
	q.dropped, q.dropBytes = 0, 0
}

// newOutbox builds the sender outbox. Its Sender is set by startMail once the
// relay client exists; until then rows stay queued.
func newOutbox(db *sql.DB, log *audit.Log, idKey *identityKey, lg *slog.Logger) *mail.Outbox {
	dir := peerDirectory{db}
	return &mail.Outbox{
		DB:    db,
		Peers: dir,
		Audit: log,
		Log:   lg,
		Priv:  idKey.Priv,
	}
}

// debateStore returns the debate store wired into rs, if any.
func debateStore(rs *request.Store) (*debate.Store, bool) {
	if rs == nil || rs.Debates == nil {
		return nil, false
	}
	ds, ok := rs.Debates.(*debate.Store)
	return ds, ok && ds != nil
}

// daemonKinds are the application kinds the daemon itself owns: each has a
// handler here and is sent only by the daemon's own methods. mail_submit
// refuses all of them (review 36 L7): a local IPC client must never be able
// to send the daemon's half of a protocol.
func daemonKinds(ts *team.Store, rs *request.Store, ws *worksession.Store, caps *capability.Store, selfKey string, log *audit.Log) map[string]mail.Kind {
	kinds := map[string]mail.Kind{}
	if ts != nil {
		kinds["team.roster"] = ts.RosterKind()
		kinds["team.join"] = ts.JoinKind()
		kinds["team.leave"] = ts.LeaveKind()
	}
	if rs != nil {
		kinds["request"] = rs.Kind()
		kinds[request.KindAccept] = rs.AcceptKind()
		kinds[request.KindDecline] = rs.DeclineKind()
		kinds[request.KindDefer] = rs.DeferKind()
		kinds[request.KindComplete] = rs.CompleteKind()
		kinds[request.KindCancelled] = rs.CancelledKind()
		kinds[request.KindCancel] = rs.CancelKind()
	}
	// ws.* is registered only once sessions are wired into the request
	// lifecycle (rs.Sessions, 2.1b). Before that no session ever opens here,
	// so a ws.* mail can only come from a misbehaving peer, and must not be
	// able to create session rows or store results: unregistered, it is
	// acked unsupported, which is also the honest Phase 1 answer.
	if ws != nil && rs != nil && rs.Sessions != nil {
		kinds[worksession.KindResult] = ws.ResultKind()
		kinds[worksession.KindCancel] = ws.CancelKind()
		kinds[worksession.KindState] = ws.StateKind()
	}
	// debate.* (Docs/protocol/debate.md §Kinds) needs sessions too: a debate
	// is argued inside a work session of kind debate; debate.sign carries
	// the respondent's Decision signature (3.3a).
	if ds, ok := debateStore(rs); ok && ws != nil && rs.Sessions != nil {
		kinds[debate.MailEntry] = ds.EntryKind()
		kinds[debate.MailReveal] = ds.RevealKind()
		kinds[debate.MailClose] = ds.CloseKind()
		kinds[debate.MailConstraint] = ds.ConstraintKind()
		kinds[debate.MailSign] = ds.SignKind()
	}
	// grant/grant.revoke are safe to register even before sessions ever open
	// here (2.1b): a grant mail's step 7 (session known and open) fails
	// closed for a session this daemon has never seen, so nothing is stored
	// (Docs/protocol/grant.md §Kinds, "a session the holder does not know yet
	// is an orphan").
	if caps != nil && ws != nil {
		kinds["grant"] = grantKind(caps, ws, selfKey, log)
		kinds["grant.revoke"] = grantRevokeKind(caps, log)
	}
	return kinds
}
