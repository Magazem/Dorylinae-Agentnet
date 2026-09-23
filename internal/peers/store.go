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
	"log/slog"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// ActionListSkip is the audit action for a peers row that List skipped.
const ActionListSkip = "peers.list_skip"

// Trust states, ordered by rank: relay < team < code < fingerprint
// (Docs/protocol/pairing.md, Docs/protocol/team.md).
const (
	TrustRelay       = "relay"
	TrustTeam        = "team"
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
	// Trust is how the key was confirmed: relay, team, code or fingerprint.
	Trust string `json:"trust"`
	// Fingerprint is fp(PublicKey) without spaces.
	Fingerprint string `json:"fingerprint"`
	// IntroducedBy is the key of the team owner who introduced the peer; nil
	// (JSON null) for a directly paired peer.
	IntroducedBy *string `json:"introduced_by"`
}

// AuditSink is the part of audit.Log that Store needs.
type AuditSink interface {
	Append(ctx context.Context, actor, action string, detail any) error
}

// Store reads and writes the peers table (created by the store migrations).
type Store struct {
	db    *sql.DB
	audit AuditSink

	// OnRemovedTx, if set, is called inside the transaction that deletes a
	// peer row, on every removal path (Remove and GCIntroduced), so a caller
	// can end everything that depended on this peer beyond what this package
	// knows about (for example grants and grant policies,
	// Docs/protocol/grant.md: "peers remove of the holder revokes all its
	// grants"). An error rolls the removal back. It must touch only tx
	// (Docs/review/27-2.1a-review.md C1; Docs/review/28-2.2c-review.md M4).
	OnRemovedTx func(ctx context.Context, tx *sql.Tx, key string) error
}

// NewStore returns a Store over a migrated database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// SetAudit sets where List reports the rows it skips. Nil means nowhere.
func (s *Store) SetAudit(a AuditSink) { s.audit = a }

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
// lowers the trust of a known key. A direct pairing clears introduced_by.
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
	introduced_by = NULL,
	trust = CASE WHEN (CASE peers.trust WHEN 'fingerprint' THEN 3 WHEN 'code' THEN 2 WHEN 'team' THEN 1 ELSE 0 END) >=
	                  (CASE excluded.trust WHEN 'fingerprint' THEN 3 WHEN 'code' THEN 2 WHEN 'team' THEN 1 ELSE 0 END)
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

// MergeMailboxKeysTx merges the verified canonical announcement ann of peer
// into peers.mailbox_keys inside tx, by the rule of Docs/protocol/mail.md
// §Peer storage. A peer that is no longer paired is ignored.
func MergeMailboxKeysTx(ctx context.Context, tx *sql.Tx, peer string, ann []byte) error {
	var old string
	switch err := tx.QueryRowContext(ctx, `SELECT mailbox_keys FROM peers WHERE public_key = ?`, peer).Scan(&old); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("peers: read mailbox_keys: %w", err)
	}
	keys, err := mergeMailboxKeys(old, ann)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE peers SET mailbox_keys = ? WHERE public_key = ?`, keys, peer); err != nil {
		return fmt.Errorf("peers: store mailbox_keys: %w", err)
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
		`SELECT public_key, name, harness, skills, paired_at, trust, introduced_by FROM peers ORDER BY paired_at, public_key`)
	if err != nil {
		return nil, fmt.Errorf("peers: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Peer{}
	var bad []string
	for rows.Next() {
		var (
			p      Peer
			skills string
			by     sql.NullString
		)
		if err := rows.Scan(&p.PublicKey, &p.Name, &p.Harness, &skills, &p.PairedAt, &p.Trust, &by); err != nil {
			return nil, fmt.Errorf("peers: scan: %w", err)
		}
		if err := json.Unmarshal([]byte(skills), &p.Skills); err != nil {
			return nil, fmt.Errorf("peers: decode skills: %w", err)
		}
		fp, err := envelope.KeyFingerprint(p.PublicKey)
		if err != nil {
			// One row with a non-canonical key must not break the whole list (review L6).
			bad = append(bad, p.PublicKey)
			continue
		}
		p.Fingerprint = fp
		if by.Valid {
			p.IntroducedBy = &by.String
		}
		if p.Skills == nil {
			p.Skills = []agentcard.Skill{}
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	for _, key := range bad {
		slog.Warn("peers: skipping row with a bad key", "event", "peers_bad_key", "key_len", len(key))
		if s.audit != nil {
			_ = s.audit.Append(ctx, "daemon", ActionListSkip, map[string]any{"peer": key})
		}
	}
	return out, nil
}

// SetTrust raises the trust of the peer with this key to trust. It never
// lowers it. Raising to code or fingerprint (a direct confirmation) clears
// introduced_by. It returns ErrNoPeer if the key is not paired.
func (s *Store) SetTrust(ctx context.Context, key, trust string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE peers SET trust = CASE
	WHEN (CASE trust WHEN 'fingerprint' THEN 3 WHEN 'code' THEN 2 WHEN 'team' THEN 1 ELSE 0 END) >=
	     (CASE ? WHEN 'fingerprint' THEN 3 WHEN 'code' THEN 2 WHEN 'team' THEN 1 ELSE 0 END) THEN trust ELSE ? END,
	introduced_by = CASE WHEN ? IN ('code', 'fingerprint') THEN NULL ELSE introduced_by END
WHERE public_key = ?`, trust, trust, trust, key)
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("peers: remove: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.removeTx(ctx, tx, key); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("peers: remove: %w", err)
	}
	return nil
}

// removeTx deletes the peer, fails its waiting outbox rows and runs
// OnRemovedTx, all inside tx.
func (s *Store) removeTx(ctx context.Context, tx *sql.Tx, key string) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM peers WHERE public_key = ?`, key)
	if err != nil {
		return fmt.Errorf("peers: remove: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoPeer
	}
	// Mail still waiting for this peer can never be delivered (mail.md §Outbox).
	if _, err := tx.ExecContext(ctx, `UPDATE outbox SET state = 'failed', error = 'unpaired', signed = NULL, frame = NULL,
next_attempt = NULL, updated = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE to_key = ? AND state IN ('queued','relayed')`, key); err != nil {
		return fmt.Errorf("peers: fail outbox rows: %w", err)
	}
	// Presence state is deleted with the peer (presence.md §Tables).
	if _, err := tx.ExecContext(ctx, `DELETE FROM presence_peers WHERE key = ?`, key); err != nil {
		return fmt.Errorf("peers: delete presence: %w", err)
	}
	if s.OnRemovedTx != nil {
		if err := s.OnRemovedTx(ctx, tx, key); err != nil {
			return err
		}
	}
	return nil
}

// Member is a verified team member as Introduce needs it: the signed card, the
// card as received, and the canonical mailbox announcement (nil for none).
type Member struct {
	Card    *agentcard.Signed
	Raw     []byte
	Mailbox []byte
}

// Introduce upserts member m, introduced by team owner owner, inside tx, by the
// rules of Docs/protocol/team.md §Introduced peers. A directly paired peer only
// gets its mailbox merged; its card, name and trust are left alone.
func (s *Store) Introduce(ctx context.Context, tx *sql.Tx, m Member, owner string, at time.Time) error {
	skills, err := json.Marshal(m.Card.Card.Skills)
	if err != nil {
		return fmt.Errorf("peers: marshal skills: %w", err)
	}
	key := m.Card.Card.PublicKey
	var (
		old string
		by  sql.NullString
	)
	switch err := tx.QueryRowContext(ctx, `SELECT mailbox_keys, introduced_by FROM peers WHERE public_key = ?`, key).Scan(&old, &by); {
	case errors.Is(err, sql.ErrNoRows):
		keys, err := mergeMailboxKeys("[]", m.Mailbox)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys, introduced_by)
VALUES (?, ?, ?, ?, ?, ?, 'team', ?, ?)`, key, m.Card.Card.Name, m.Card.Card.Harness, string(skills), string(m.Raw),
			at.UTC().Format(time.RFC3339), keys, owner); err != nil {
			return fmt.Errorf("peers: introduce: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("peers: introduce: %w", err)
	}
	keys, err := mergeMailboxKeys(old, m.Mailbox)
	if err != nil {
		return err
	}
	if !by.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE peers SET mailbox_keys = ? WHERE public_key = ?`, keys, key); err != nil {
			return fmt.Errorf("peers: introduce: %w", err)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE peers SET name = ?, harness = ?, skills = ?, card = ?, mailbox_keys = ? WHERE public_key = ?`,
		m.Card.Card.Name, m.Card.Card.Harness, string(skills), string(m.Raw), keys, key); err != nil {
		return fmt.Errorf("peers: introduce: %w", err)
	}
	return nil
}

// Removed describes a peer that GCIntroduced deleted, for the caller's audit
// entry (peer.remove, reason "team").
type Removed struct {
	PublicKey   string
	Name        string
	Fingerprint string
}

// GCIntroduced deletes, inside tx, every introduced peer (introduced_by not
// NULL) that is not a member of any team in state active, failing its waiting
// outbox rows as Remove does. A directly paired peer is never removed. Before
// the teams tables exist (migration 9) no team is active; if only one of them
// exists it fails and removes nothing. The caller writes the
// audit entries after commit.
func (s *Store) GCIntroduced(ctx context.Context, tx *sql.Tx) ([]Removed, error) {
	var tables int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('teams', 'team_members')`).Scan(&tables); err != nil {
		return nil, fmt.Errorf("peers: gc introduced: %w", err)
	}
	q := `SELECT public_key, name FROM peers WHERE introduced_by IS NOT NULL ORDER BY public_key`
	switch tables {
	case 0:
	case 2:
		q = `SELECT public_key, name FROM peers WHERE introduced_by IS NOT NULL AND NOT EXISTS (
	SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
	WHERE m.key = peers.public_key AND t.state = 'active') ORDER BY public_key`
	default:
		// Only one of the two tables: never read that as "no active team" and delete every introduced peer.
		return nil, errors.New("peers: gc introduced: teams schema incomplete")
	}
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("peers: gc introduced: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Removed{}
	for rows.Next() {
		var r Removed
		if err := rows.Scan(&r.PublicKey, &r.Name); err != nil {
			return nil, fmt.Errorf("peers: gc introduced: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	for i := range out {
		// A key that is not canonical cannot have been stored by Introduce; leave the fingerprint empty.
		out[i].Fingerprint, _ = envelope.KeyFingerprint(out[i].PublicKey)
		if err := s.removeTx(ctx, tx, out[i].PublicKey); err != nil {
			return nil, err
		}
	}
	return out, nil
}
