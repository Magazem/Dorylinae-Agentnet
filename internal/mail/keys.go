package mail

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// Kind keys and key-miss recovery, Docs/protocol/mail.md §Kind keys and
// §Key-miss recovery.

// KeyMissWindow is the least time between two key-miss replies to one peer.
const KeyMissWindow = 10 * time.Minute

// maxPending caps the ids remembered per peer for a key-miss reply.
const maxPending = maxIDList

// KeysKind is the receiver handler for kind keys. merge stores the verified
// canonical announcement of peer (peers.MergeMailboxKeysTx) inside the dedupe
// transaction. onRetry, if not nil, runs after the commit with the ids of the
// sender's retry list: the mails it could not decrypt. This is where 1.0e
// re-seals its outbox rows.
func KeysKind(merge func(ctx context.Context, tx *sql.Tx, peer string, ann []byte) error, onRetry func(ctx context.Context, peer string, ids []string)) Kind {
	return Kind{
		Apply: func(ctx context.Context, tx *sql.Tx, op *Opened) error {
			ann, err := agentcard.CanonicalValue(op.Msg.Body["announcement"])
			if err != nil {
				return fmt.Errorf("canonicalize announcement: %w", err)
			}
			return merge(ctx, tx, op.Msg.From, ann)
		},
		After: func(ctx context.Context, op *Opened) {
			list, _ := op.Msg.Body["retry"].([]any)
			if onRetry == nil || len(list) == 0 {
				return
			}
			ids := make([]string, 0, len(list))
			for _, v := range list {
				if s, ok := v.(string); ok {
					ids = append(ids, s)
				}
			}
			onRetry(ctx, op.Msg.From, ids)
		},
	}
}

// Pusher sends keys mail: the rotation push and the key-miss reply.
type Pusher struct {
	// Priv loads the own identity key. The pusher clears it after use.
	Priv func() (ed25519.PrivateKey, error)
	// Peers gives each peer's newest mailbox key.
	Peers PeerKeys
	// List returns the paired peers that have a mailbox key.
	List   func(ctx context.Context) ([]string, error)
	Sender EnvelopeSender
	// Outbox is the hook for 1.0e: when set, the rotation push goes through the
	// sender outbox (resent until acked) instead of being sent once, directly.
	// body is the kind keys body.
	Outbox func(ctx context.Context, peer string, body map[string]any) error
	Log    *slog.Logger
	Now    func() time.Time // defaults to time.Now
}

func (p *Pusher) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pusher) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// PushAll sends the announcement to every paired peer with a mailbox key. A
// failure for one peer is logged and does not stop the others.
func (p *Pusher) PushAll(ctx context.Context, announcement []byte) {
	peers, err := p.List(ctx)
	if err != nil {
		p.log().Warn("mail: cannot list peers for keys push", "event", "mail_error", "error", err)
		return
	}
	body := map[string]any{"announcement": json.RawMessage(announcement)}
	for _, peer := range peers {
		var err error
		if p.Outbox != nil {
			err = p.Outbox(ctx, peer, body)
		} else {
			_, err = p.sendDirect(ctx, peer, body)
		}
		if err != nil {
			p.log().Warn("mail: keys push failed", "event", "mail_error", "error", err)
		}
	}
}

// Reply sends the key-miss reply to peer: the current announcement and the ids
// that could not be decrypted, once and directly. sent is false, with no
// error, if there is no mailbox key for the peer.
func (p *Pusher) Reply(ctx context.Context, peer string, announcement []byte, retry []string) (sent bool, err error) {
	body := map[string]any{"announcement": json.RawMessage(announcement)}
	if len(retry) > 0 {
		body["retry"] = retry
	}
	return p.sendDirect(ctx, peer, body)
}

func (p *Pusher) sendDirect(ctx context.Context, peer string, body map[string]any) (bool, error) {
	pub, ok := p.Peers.MailboxPub(peer)
	if !ok {
		return false, nil
	}
	priv, err := p.Priv()
	if err != nil {
		return false, fmt.Errorf("mail: load identity key: %w", err)
	}
	defer clear(priv)
	sl, err := Seal(SealInput{Priv: priv, To: peer, MailboxPub: pub, Kind: "keys", Body: body, Created: p.now()})
	if err != nil {
		return false, fmt.Errorf("mail: seal keys: %w", err)
	}
	e := envelope.Envelope{
		From: envelope.KeyString(priv.Public().(ed25519.PublicKey)), To: peer,
		Type: "mail", ID: sl.ID, TS: p.now().UTC().Format(time.RFC3339), Payload: sl.Payload,
	}
	if err := p.Sender.Send(ctx, e); err != nil {
		return false, fmt.Errorf("mail: send keys: %w", err)
	}
	return true, nil
}

// KeyMiss runs the receiver side of key-miss recovery: it collects the ids of
// mail sealed to a key we no longer have and answers with a keys mail, at most
// once per KeyMissWindow per peer.
type KeyMiss struct {
	Pusher *Pusher
	// Announcement returns the current own signed announcement.
	Announcement func() ([]byte, error)
	Now          func() time.Time // defaults to time.Now
	// After schedules f once d has passed. It defaults to time.AfterFunc; tests replace it.
	After func(d time.Duration, f func())
	Log   *slog.Logger

	mu      sync.Mutex
	pending map[string][]string
	last    map[string]time.Time
	waiting map[string]bool
}

func (m *KeyMiss) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *KeyMiss) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// Note records a key miss for the envelope id from peer. An id that is not in
// the mail id format is ignored (the payload is not authenticated yet).
func (m *KeyMiss) Note(ctx context.Context, peer, id string) {
	if !ValidID(id) {
		return
	}
	m.mu.Lock()
	if m.pending == nil {
		m.pending, m.last, m.waiting = map[string][]string{}, map[string]time.Time{}, map[string]bool{}
	}
	if set := m.pending[peer]; len(set) < maxPending && !contains(set, id) {
		m.pending[peer] = append(set, id)
	}
	wait := KeyMissWindow - m.now().Sub(m.last[peer])
	if _, ok := m.last[peer]; ok && wait > 0 {
		if !m.waiting[peer] {
			m.waiting[peer] = true
			after := m.After
			if after == nil {
				after = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
			}
			after(wait, func() { m.Flush(context.Background(), peer) })
		}
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	m.Flush(ctx, peer)
}

// Flush sends the pending reply to peer, if any, and starts a new window.
func (m *KeyMiss) Flush(ctx context.Context, peer string) {
	m.mu.Lock()
	delete(m.waiting, peer)
	retry := m.pending[peer]
	delete(m.pending, peer)
	m.mu.Unlock()
	if len(retry) == 0 {
		return
	}
	ann, err := m.Announcement()
	if err != nil {
		m.log().Warn("mail: key-miss reply: no announcement", "event", "mail_error", "error", err)
		return
	}
	sent, err := m.Pusher.Reply(ctx, peer, ann, retry)
	if err != nil {
		m.log().Warn("mail: key-miss reply failed", "event", "mail_error", "error", err)
	}
	if sent {
		m.mu.Lock()
		if m.last == nil {
			m.last = map[string]time.Time{}
		}
		m.last[peer] = m.now()
		m.mu.Unlock()
	}
}

func contains(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}
