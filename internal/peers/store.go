// Package peers stores the agents this daemon has paired with and runs the
// daemon side of pairing (Docs/protocol/pairing.md): it verifies each received
// Agent Card before anything is stored.
package peers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// Trust states, ordered by rank (Docs/protocol/pairing.md).
const (
	TrustRelay       = "relay"
	TrustCode        = "code"
	TrustFingerprint = "fingerprint"
)

// ErrNoPeer is returned by Remove and SetTrust for a key that is not paired.
var ErrNoPeer = errors.New("peers: no such peer")

// Peer is one paired agent.
type Peer struct {
	PublicKey string            `json:"public_key"`
	Name      string            `json:"name"`
	Harness   string            `json:"harness"`
	Skills    []agentcard.Skill `json:"skills"`
	PairedAt  string            `json:"paired_at"`
	// Trust is how the key was confirmed: relay, code or fingerprint.
	Trust string `json:"trust"`
	// Fingerprint is fp(PublicKey) without spaces.
	Fingerprint string `json:"fingerprint"`
}

// Store reads and writes the peers table (created by the store migrations).
type Store struct {
	db *sql.DB
}

// NewStore returns a Store over a migrated database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Add stores a peer from a card that the caller has already verified, as a v1
// pairing does: trust=relay and no mailbox key. raw is the card envelope as
// received. See AddTrusted.
func (s *Store) Add(ctx context.Context, sc *agentcard.Signed, raw []byte, at time.Time) error {
	return s.AddTrusted(ctx, sc, raw, at, TrustRelay, nil)
}

// AddTrusted stores a verified peer with trust. mbox, if not nil, is the peer's
// verified canonical mailbox announcement; it is merged into mailbox_keys by
// the rule of Docs/protocol/mail.md §Peer storage. Pairing an already known key
// refreshes its card details and keeps the original paired_at. AddTrusted never
// lowers the trust of a known key.
func (s *Store) AddTrusted(ctx context.Context, sc *agentcard.Signed, raw []byte, at time.Time, trust string, mbox []byte) (err error) {
	skills, err := json.Marshal(sc.Card.Skills)
	if err != nil {
		return fmt.Errorf("peers: marshal skills: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("peers: store peer: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var old string
	switch err := tx.QueryRowContext(ctx, `SELECT mailbox_keys FROM peers WHERE public_key = ?`, sc.Card.PublicKey).Scan(&old); {
	case errors.Is(err, sql.ErrNoRows):
		old = "[]"
	case err != nil:
		return fmt.Errorf("peers: store peer: %w", err)
	}
	keys, err := mergeMailboxKeys(old, mbox)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (public_key) DO UPDATE SET name = excluded.name, harness = excluded.harness,
	skills = excluded.skills, card = excluded.card, mailbox_keys = excluded.mailbox_keys,
	trust = CASE WHEN (CASE peers.trust WHEN 'fingerprint' THEN 2 WHEN 'code' THEN 1 ELSE 0 END) >=
	                  (CASE excluded.trust WHEN 'fingerprint' THEN 2 WHEN 'code' THEN 1 ELSE 0 END)
	            THEN peers.trust ELSE excluded.trust END`,
		sc.Card.PublicKey, sc.Card.Name, sc.Card.Harness, string(skills), string(raw),
		at.UTC().Format(time.RFC3339), trust, keys)
	if err != nil {
		return fmt.Errorf("peers: store peer: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("peers: store peer: %w", err)
	}
	return nil
}

// maxMailboxKeys is the most announcements kept per peer.
const maxMailboxKeys = 2

type announcementHead struct {
	Announcement struct {
		Created string `json:"created"`
		KeyID   string `json:"key_id"`
	} `json:"announcement"`
}

// mergeMailboxKeys adds the verified announcement ann to the stored JSON array
// old: an already known key_id, or a created that is not newer than the newest
// stored one, is ignored; otherwise ann goes first and the array is cut to two.
func mergeMailboxKeys(old string, ann []byte) (string, error) {
	var stored []json.RawMessage
	if err := json.Unmarshal([]byte(old), &stored); err != nil {
		return "", fmt.Errorf("peers: stored mailbox_keys: %w", err)
	}
	if ann != nil {
		var h announcementHead
		if err := json.Unmarshal(ann, &h); err != nil {
			return "", fmt.Errorf("peers: announcement: %w", err)
		}
		accept := true
		for i, raw := range stored {
			var o announcementHead
			if err := json.Unmarshal(raw, &o); err != nil {
				return "", fmt.Errorf("peers: stored mailbox_keys: %w", err)
			}
			// Times are RFC 3339 UTC with Z and whole seconds, so text order is time order.
			if o.Announcement.KeyID == h.Announcement.KeyID || (i == 0 && h.Announcement.Created <= o.Announcement.Created) {
				accept = false
				break
			}
		}
		if accept {
			stored = append([]json.RawMessage{ann}, stored...)
			if len(stored) > maxMailboxKeys {
				stored = stored[:maxMailboxKeys]
			}
		}
	}
	out, err := json.Marshal(stored)
	if err != nil {
		return "", fmt.Errorf("peers: mailbox_keys: %w", err)
	}
	return string(out), nil
}

// usedCodeRetention is how long the redeemer remembers a code it sent a tag for.
const usedCodeRetention = 24 * time.Hour

// CodeUsed reports whether the code with this hash was used in the last 24
// hours. It also drops older rows.
func (s *Store) CodeUsed(ctx context.Context, hash []byte, now time.Time) (bool, error) {
	if err := s.pruneUsed(ctx, now); err != nil {
		return false, err
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pair_used_codes WHERE hash = ?`, hash).Scan(&n); err != nil {
		return false, fmt.Errorf("peers: check used code: %w", err)
	}
	return n > 0, nil
}

// MarkCodeUsed records the hash as used. fresh is false if it already was, so
// two concurrent redemptions of one code cannot both send a tag.
func (s *Store) MarkCodeUsed(ctx context.Context, hash []byte, now time.Time) (fresh bool, err error) {
	if err := s.pruneUsed(ctx, now); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO pair_used_codes (hash, used_at) VALUES (?, ?)`, hash, now.Unix())
	if err != nil {
		return false, fmt.Errorf("peers: record used code: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) pruneUsed(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM pair_used_codes WHERE used_at < ?`, now.Add(-usedCodeRetention).Unix()); err != nil {
		return fmt.Errorf("peers: prune used codes: %w", err)
	}
	return nil
}

// List returns all peers, oldest pairing first. It never returns nil.
func (s *Store) List(ctx context.Context) ([]Peer, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT public_key, name, harness, skills, paired_at, trust FROM peers ORDER BY paired_at, public_key`)
	if err != nil {
		return nil, fmt.Errorf("peers: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Peer{}
	for rows.Next() {
		var (
			p      Peer
			skills string
		)
		if err := rows.Scan(&p.PublicKey, &p.Name, &p.Harness, &skills, &p.PairedAt, &p.Trust); err != nil {
			return nil, fmt.Errorf("peers: scan: %w", err)
		}
		if err := json.Unmarshal([]byte(skills), &p.Skills); err != nil {
			return nil, fmt.Errorf("peers: decode skills: %w", err)
		}
		fp, err := envelope.KeyFingerprint(p.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("peers: fingerprint of %q: %w", p.PublicKey, err)
		}
		p.Fingerprint = fp
		if p.Skills == nil {
			p.Skills = []agentcard.Skill{}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetTrust raises the trust of the peer with this key to trust. It never
// lowers it. It returns ErrNoPeer if the key is not paired.
func (s *Store) SetTrust(ctx context.Context, key, trust string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE peers SET trust = CASE
	WHEN (CASE trust WHEN 'fingerprint' THEN 2 WHEN 'code' THEN 1 ELSE 0 END) >=
	     (CASE ? WHEN 'fingerprint' THEN 2 WHEN 'code' THEN 1 ELSE 0 END) THEN trust ELSE ? END
WHERE public_key = ?`, trust, trust, key)
	if err != nil {
		return fmt.Errorf("peers: set trust: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoPeer
	}
	return nil
}

// Remove deletes the peer with this key. It returns ErrNoPeer if the key is
// not paired.
func (s *Store) Remove(ctx context.Context, key string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM peers WHERE public_key = ?`, key)
	if err != nil {
		return fmt.Errorf("peers: remove: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoPeer
	}
	return nil
}
