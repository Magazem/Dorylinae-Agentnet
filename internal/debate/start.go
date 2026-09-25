package debate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Defaults of the IPC debate member (Docs/protocol/debate.md §Request type
// debate, OD-P3-7).
const (
	DefaultRounds       = 2
	DefaultTurnTimeoutS = 3600
)

// StartParams are a debate submit (request_submit with type debate).
type StartParams struct {
	// Submit is the request, resolved as for every request_submit; Type must
	// be request.TypeDebate. Its Prepare and InTx are set by Start.
	Submit request.SubmitParams
	// Position is A's opening position as a generic JSON value (as parsed
	// from the IPC params). It is committed, never sent before the reveal.
	Position any
	// Rounds and TurnTimeoutS; 0 selects the default.
	Rounds       int
	TurnTimeoutS int
}

// StartOutcome is request.SubmitOutcome plus the derived session id.
type StartOutcome struct {
	request.SubmitOutcome
	Session string
}

// Start submits a debate request (Docs/protocol/debate.md §Request type
// debate, §Commit-reveal): the daemon draws the nonce, computes the
// commitment and sends only the commitment in the request; in the same
// transaction it stores the initiator's debates row (invited) and the
// committed position at slot 0. It refuses with ErrQuarantineActive while
// the peer-wide quarantine clause holds for the peer. An idempotent retry
// returns the first request and its commitment; the caller's params hash
// covers the position, rounds and turn timeout (review 43 L11).
//
// Position errors are *FieldError under "debate.position" or
// *TooLargeError (entry_too_large).
func (s *Store) Start(ctx context.Context, p StartParams) (StartOutcome, error) {
	if p.Submit.Type != request.TypeDebate {
		return StartOutcome{}, fieldErr("type", "must be debate")
	}
	if p.Rounds == 0 {
		p.Rounds = DefaultRounds
	}
	if p.TurnTimeoutS == 0 {
		p.TurnTimeoutS = DefaultTurnTimeoutS
	}
	if p.Rounds < request.MinDebateRounds || p.Rounds > request.MaxDebateRounds {
		return StartOutcome{}, fieldErr("debate.rounds", "must be %d-%d", request.MinDebateRounds, request.MaxDebateRounds)
	}
	if p.TurnTimeoutS < request.MinDebateTurnTimeout || p.TurnTimeoutS > request.MaxDebateTurnTimeout {
		return StartOutcome{}, fieldErr("debate.turn_timeout_s", "must be %d-%d", request.MinDebateTurnTimeout, request.MaxDebateTurnTimeout)
	}
	if p.Position == nil {
		return StartOutcome{}, fieldErr("debate.position", "is required")
	}
	_, canon, err := DecodeEntry(KindPosition, p.Position)
	if err != nil {
		return StartOutcome{}, fieldPrefix(err, "debate.position")
	}
	var sid, nonce string
	sp := p.Submit
	sp.Prepare = func(req *request.Request) error {
		sid = worksession.DeriveID(req.From, req.To, req.ID)
		nonce = NewNonce()
		req.Debate = &request.DebateMember{
			Commitment: Commitment(sid, req.From, nonce, canon), Rounds: p.Rounds, TurnTimeoutS: p.TurnTimeoutS,
		}
		return nil
	}
	sp.InTx = func(ctx context.Context, tx *sql.Tx, req *request.Request, now time.Time) error {
		if err := s.checkQuarantine(ctx, tx, req.To); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO debates (session, role, peer, request_id, rounds_max, turn_timeout_s, commitment, nonce, phase, next_slot, created, updated)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			sid, RoleInitiator, req.To, req.ID, p.Rounds, p.TurnTimeoutS, req.Debate.Commitment, nonce, PhaseInvited,
			storeTime(now), storeTime(now)); err != nil {
			return fmt.Errorf("debate: insert debate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO debate_entries (session, slot, author, kind, entry, at, state) VALUES (?, 0, ?, ?, ?, ?, ?)`,
			sid, RoleInitiator, KindPosition, string(canon), wireTime(now), stateCommitted); err != nil {
			return fmt.Errorf("debate: store committed position: %w", err)
		}
		return nil
	}
	out, err := s.Requests.Submit(ctx, sp)
	if err != nil {
		return StartOutcome{}, err
	}
	sid = worksession.DeriveID(out.Request.From, out.Request.To, out.Request.ID)
	if !out.Duplicate {
		s.audit("cli", "debate.start", map[string]any{
			"session": sid, "request": out.Request.ID, "peer": out.Request.To,
			"rounds": p.Rounds, "turn_timeout_s": p.TurnTimeoutS, "position_bytes": len(canon),
		})(ctx)
	}
	return StartOutcome{SubmitOutcome: out, Session: sid}, nil
}

func (s *Store) checkQuarantine(ctx context.Context, tx *sql.Tx, peer string) error {
	if s.PeerQuarantine == nil {
		return nil
	}
	q, err := s.PeerQuarantine(ctx, tx, peer)
	if err != nil {
		return err
	}
	if q {
		return ErrQuarantineActive
	}
	return nil
}

// ReceivedTx implements request.DebateHooks: B stores its debate row
// (invited, with A's commitment) in the transaction that stores the received
// request.
func (s *Store) ReceivedTx(ctx context.Context, tx *sql.Tx, req *request.Request, now time.Time) error {
	sid := worksession.DeriveID(req.From, req.To, req.ID)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO debates (session, role, peer, request_id, rounds_max, turn_timeout_s, commitment, phase, next_slot, created, updated)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		sid, RoleRespondent, req.From, req.ID, req.Debate.Rounds, req.Debate.TurnTimeoutS, req.Debate.Commitment,
		PhaseInvited, storeTime(now), storeTime(now)); err != nil {
		return fmt.Errorf("debate: insert received debate: %w", err)
	}
	return nil
}

// EndedTx implements request.DebateHooks: a debate request declined or
// cancelled before accept closes its debate row cancelled (A's committed
// position is never sent).
func (s *Store) EndedTx(ctx context.Context, tx *sql.Tx, direction, peer, id string, now time.Time) error {
	role := RoleRespondent
	if direction == "out" {
		role = RoleInitiator
	}
	r, err := getRowByRequest(ctx, tx, role, peer, id)
	if errors.Is(err, ErrUnknownDebate) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.phase != PhaseInvited {
		return nil
	}
	return setClosed(ctx, tx, r.session, OutcomeCancelled, ReasonCancelled, now)
}

func setClosed(ctx context.Context, tx *sql.Tx, sid, outcome, reason string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE debates SET phase = ?, outcome = ?, reason = ?, turn_deadline = NULL, updated = ? WHERE session = ?`,
		PhaseClosed, outcome, reason, storeTime(now), sid); err != nil {
		return fmt.Errorf("debate: close: %w", err)
	}
	return nil
}

// OpenedTx implements worksession.DebateHooks: the accept of a debate request
// opened its session (on B through its own accept, on A through the accept
// mirror or an overtaking slot-1 entry). The debate moves from invited to
// positions and its first turn deadline starts. On B the accept is refused
// with ErrQuarantineActive while the peer-wide quarantine clause holds from
// B's side (§Quarantine interplay). Idempotent: a debate past invited is left
// alone.
func (s *Store) OpenedTx(ctx context.Context, tx *sql.Tx, wsRole, peer, requestID, sid string, now time.Time) error {
	role := RoleInitiator
	if wsRole == worksession.RoleWorker {
		role = RoleRespondent
	}
	r, err := getRow(ctx, tx, sid)
	if err != nil {
		return err
	}
	if r.role != role || r.peer != peer || r.requestID != requestID {
		return fmt.Errorf("debate: session %s does not match its debate row", sid)
	}
	if r.phase != PhaseInvited {
		return nil
	}
	if role == RoleRespondent {
		if err := s.checkQuarantine(ctx, tx, peer); err != nil {
			return err
		}
	}
	deadline := now.Add(time.Duration(r.turnTimeoutS) * time.Second)
	if _, err := tx.ExecContext(ctx, `UPDATE debates SET phase = ?, next_slot = 1, turn_deadline = ?, updated = ? WHERE session = ?`,
		PhasePositions, wireTime(deadline), storeTime(now), sid); err != nil {
		return fmt.Errorf("debate: open: %w", err)
	}
	return nil
}
