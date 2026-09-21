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

// Add stores a peer from a card that the caller has already verified. raw is
// the card envelope as received. Pairing an already known key refreshes its
// card details and keeps the original paired_at. A new peer gets trust=relay
// (v1 pairing); Add never lowers the trust of a known key.
func (s *Store) Add(ctx context.Context, sc *agentcard.Signed, raw []byte, at time.Time) error {
	skills, err := json.Marshal(sc.Card.Skills)
	if err != nil {
		return fmt.Errorf("peers: marshal skills: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO peers (public_key, name, harness, skills, card, paired_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (public_key) DO UPDATE SET name = excluded.name, harness = excluded.harness,
	skills = excluded.skills, card = excluded.card`,
		sc.Card.PublicKey, sc.Card.Name, sc.Card.Harness, string(skills), string(raw),
		at.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("peers: store peer: %w", err)
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
