package request

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Sending side of Docs/protocol/request.md §Submitting steps 4-8. Steps 1-3
// (peer/team resolution, D5) are the caller's job: they need the peers and
// team stores, which this package does not depend on.

// ErrIdempotencyConflict is step 4: the same idempotency_key for this peer
// was given with different params.
var ErrIdempotencyConflict = errors.New("request: idempotency key already used with different params")

var idemKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// ValidIdempotencyKey reports whether s is a valid --idempotency-key.
func ValidIdempotencyKey(s string) bool { return idemKeyPattern.MatchString(s) }

// SubmitParams are the fields of Docs/protocol/ipc.md `request_submit`, with
// `to` and `team` already resolved to their keys/ids.
type SubmitParams struct {
	From, To, Team         string
	Type, Title, Brief     string
	Urgency, UrgencyReason string
	Artifacts              []Artifact
	RequestedGrant         *RequestedGrant
	Deadline               time.Time // zero: absent
	IdempotencyKey         string
	// ParamsHash is the caller-computed hash of Docs/protocol/request.md
	// §Submitting ("params_hash"), required when IdempotencyKey is set.
	ParamsHash string
}

// SubmitOutcome is enough to build the IPC "submit result"
// (Docs/protocol/request.md §Submit result); the caller adds team name,
// urgency_declared/urgency_note and peer presence.
type SubmitOutcome struct {
	Request   *Request
	MailID    string
	Status    string // "queued", or the outbox delivery state for a duplicate
	Duplicate bool
}

// Submit runs Docs/protocol/request.md §Submitting steps 4-8.
func (s *Store) Submit(ctx context.Context, p SubmitParams) (SubmitOutcome, error) {
	if p.IdempotencyKey != "" {
		row, ok, err := s.lookupIdem(ctx, p.To, p.IdempotencyKey)
		if err != nil {
			return SubmitOutcome{}, err
		}
		if ok {
			if row.paramsHash != p.ParamsHash {
				return SubmitOutcome{}, ErrIdempotencyConflict
			}
			return s.duplicateOutcome(ctx, row)
		}
	}

	now := s.now()
	req := &Request{
		V: 1, ID: NewID(), From: p.From, To: p.To, Team: p.Team, Type: p.Type,
		Title: p.Title, Brief: p.Brief, Urgency: p.Urgency, UrgencyReason: p.UrgencyReason,
		Artifacts: p.Artifacts, RequestedGrant: p.RequestedGrant, Deadline: p.Deadline,
		Created: now.UTC().Truncate(time.Second),
	}
	if err := Validate(req); err != nil {
		return SubmitOutcome{}, err
	}
	canon, err := Canonical(req)
	if err != nil {
		return SubmitOutcome{}, err
	}
	if err := CheckSize(canon); err != nil {
		return SubmitOutcome{}, err
	}
	hash := BodyHash(canon)

	outcome, retry, err := s.insertOut(ctx, p, req, canon, hash, now)
	if err != nil {
		return SubmitOutcome{}, err
	}
	if retry {
		row, ok, lerr := s.lookupIdem(ctx, p.To, p.IdempotencyKey)
		if lerr != nil {
			return SubmitOutcome{}, lerr
		}
		if !ok {
			return SubmitOutcome{}, fmt.Errorf("request: lost the race for idempotency key %q with no winner found", p.IdempotencyKey)
		}
		if row.paramsHash != p.ParamsHash {
			return SubmitOutcome{}, ErrIdempotencyConflict
		}
		return s.duplicateOutcome(ctx, row)
	}
	return outcome, nil
}

// insertOut stores the `out` row and the outbox row in one transaction. retry
// is true when a concurrent submit with the same idempotency key won the race
// (Docs/protocol/request.md §Submitting step 7).
func (s *Store) insertOut(ctx context.Context, p SubmitParams, req *Request, canon []byte, hash string, now time.Time) (outcome SubmitOutcome, retry bool, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SubmitOutcome{}, false, fmt.Errorf("request: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	sub, err := s.Outbox.SubmitTx(ctx, tx, p.To, "request", WireBody(req))
	if err != nil {
		return SubmitOutcome{}, false, err
	}
	var idemKey, paramsHash any
	if p.IdempotencyKey != "" {
		idemKey, paramsHash = p.IdempotencyKey, p.ParamsHash
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO requests (
	direction, peer, id, team_id, type, urgency, urgency_declared, downgraded_by,
	body, body_hash, state, state_seq, created, mail_id, idem_key, params_hash, updated
) VALUES ('out', ?, ?, ?, ?, ?, ?, NULL, ?, ?, 'pending', 0, ?, ?, ?, ?, ?)`,
		p.To, req.ID, req.Team, req.Type, req.Urgency, req.Urgency,
		string(canon), hash, wireTime(req.Created), sub.ID, idemKey, paramsHash, storeTime(now),
	)
	if err != nil {
		if p.IdempotencyKey != "" && isUniqueConflict(err) {
			return SubmitOutcome{}, true, nil
		}
		return SubmitOutcome{}, false, fmt.Errorf("request: insert out row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SubmitOutcome{}, false, fmt.Errorf("request: commit: %w", err)
	}
	s.Outbox.Wake()
	return SubmitOutcome{Request: req, MailID: sub.ID, Status: "queued"}, false, nil
}

func isUniqueConflict(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint")
}

type idemRow struct {
	id, mailID, body, paramsHash string
}

func (s *Store) lookupIdem(ctx context.Context, to, key string) (idemRow, bool, error) {
	var r idemRow
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, mail_id, body, params_hash FROM requests WHERE direction = 'out' AND peer = ? AND idem_key = ?`,
		to, key).Scan(&r.id, &r.mailID, &r.body, &r.paramsHash)
	if errors.Is(err, sql.ErrNoRows) {
		return idemRow{}, false, nil
	}
	if err != nil {
		return idemRow{}, false, fmt.Errorf("request: read idempotency row: %w", err)
	}
	return r, true, nil
}

func (s *Store) duplicateOutcome(ctx context.Context, row idemRow) (SubmitOutcome, error) {
	req, err := decodeStoredBody(row.body)
	if err != nil {
		return SubmitOutcome{}, err
	}
	status := "unknown"
	var st string
	err = s.DB.QueryRowContext(ctx, `SELECT state FROM outbox WHERE id = ?`, row.mailID).Scan(&st)
	switch {
	case err == nil:
		status = st
	case errors.Is(err, sql.ErrNoRows):
	default:
		return SubmitOutcome{}, fmt.Errorf("request: read outbox state: %w", err)
	}
	return SubmitOutcome{Request: req, MailID: row.mailID, Status: status, Duplicate: true}, nil
}

// decodeStoredBody parses a stored canonical request body back into a Request.
func decodeStoredBody(body string) (*Request, error) {
	v, err := agentcard.ParseStrict([]byte(body))
	if err != nil {
		return nil, fmt.Errorf("request: stored body: %w", err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("request: stored body is not an object")
	}
	return Decode(obj)
}
