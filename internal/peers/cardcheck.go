package peers

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// BadCard is a peers row whose stored card no longer verifies under the
// current Agent Card rules (review 68 OD-3).
type BadCard struct {
	PublicKey string
	// Fingerprint is fp(PublicKey) without spaces, or "" if the key itself
	// does not parse.
	Fingerprint string
	Reason      string
}

// MigrateCards is the stored-card migration of review 68 OD-3. It is
// idempotent and runs at every daemon open. For each peers row it re-reads
// card with the legacy parse, keeps exactly {card, signature}, and verifies
// that under the current rules against the row's public_key
// (agentcard.RescueStored). A card that verifies is rewritten when its
// canonical form differs from the stored bytes (for example a v1 row with
// relay-added top-level members). A card that does not verify is left as it
// is and returned: the row is never deleted or downgraded, and no other column
// changes. Only a database error is returned as an error.
func (s *Store) MigrateCards(ctx context.Context) ([]BadCard, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT public_key, card FROM peers ORDER BY public_key`)
	if err != nil {
		return nil, fmt.Errorf("peers: card migration: %w", err)
	}
	type rewrite struct{ key, card, old string }
	var fixes []rewrite
	var bad []BadCard
	for rows.Next() {
		var key, card string
		if err := rows.Scan(&key, &card); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("peers: card migration: %w", err)
		}
		canon, verr := agentcard.RescueStored([]byte(card), key)
		switch {
		case verr != nil:
			bad = append(bad, badCard(key, verr))
		case string(canon) != card:
			fixes = append(fixes, rewrite{key, string(canon), card})
		}
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("peers: card migration: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("peers: card migration: %w", err)
	}
	for _, f := range fixes {
		// Compare-and-set on the old bytes, so a concurrent re-pairing wins.
		if _, err := s.db.ExecContext(ctx, `UPDATE peers SET card = ? WHERE public_key = ? AND card = ?`, f.card, f.key, f.old); err != nil {
			return nil, fmt.Errorf("peers: card migration: %w", err)
		}
	}
	return bad, nil
}

// CheckStoredCards reports, without writing, the peers rows that
// MigrateCards would leave in place because their card does not verify. It
// works on a read-only handle (agentnet doctor).
func CheckStoredCards(ctx context.Context, db *sql.DB) ([]BadCard, error) {
	rows, err := db.QueryContext(ctx, `SELECT public_key, card FROM peers ORDER BY public_key`)
	if err != nil {
		return nil, fmt.Errorf("peers: check cards: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var bad []BadCard
	for rows.Next() {
		var key, card string
		if err := rows.Scan(&key, &card); err != nil {
			return nil, fmt.Errorf("peers: check cards: %w", err)
		}
		if _, verr := agentcard.RescueStored([]byte(card), key); verr != nil {
			bad = append(bad, badCard(key, verr))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("peers: check cards: %w", err)
	}
	return bad, nil
}

func badCard(key string, err error) BadCard {
	fp, _ := envelope.KeyFingerprint(key)
	return BadCard{PublicKey: key, Fingerprint: fp, Reason: err.Error()}
}
