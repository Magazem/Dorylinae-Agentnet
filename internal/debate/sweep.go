package debate

import (
	"context"
	"fmt"
)

// Timeouts (Docs/protocol/debate.md §Timeouts). Only A enforces them: when
// the side whose slot is next (A's own agent included) misses the deadline,
// a missing slot 1 closes the debate cancelled (reason timeout, nothing to
// record) and any later slot closes it escalated (reason timeout, Decision
// with what exists). The reveal is automatic on A and cannot time out. B
// never closes on time.

// Sweep closes every initiator debate whose turn deadline has passed and
// returns how many it closed. The daemon runs it in its periodic sweep (at
// least once a minute; 3.1b wires it).
func (s *Store) Sweep(ctx context.Context) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT session FROM debates WHERE role = ? AND phase IN (?, ?, ?) AND turn_deadline IS NOT NULL`,
		RoleInitiator, PhasePositions, PhaseRounds, PhaseConverge)
	if err != nil {
		return 0, fmt.Errorf("debate: sweep: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		closed, err := s.SweepOne(ctx, id)
		if err != nil {
			// review 45 L2: one bad debate (a stored entry that no longer
			// parses) must not stop the sweep for every debate after it.
			if s.Log != nil {
				s.Log.Error("debate: sweep one failed, continuing", "session", id, "error", err)
			}
			continue
		}
		if closed {
			n++
		}
	}
	return n, nil
}

// SweepOne applies the timeout rule to one debate (s- or r- id): closed
// reports whether it closed it now. Respondent debates are never closed.
func (s *Store) SweepOne(ctx context.Context, id string) (closed bool, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("debate: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	r, err := resolve(ctx, tx, id)
	if err != nil {
		return false, err
	}
	now := s.now()
	if r.role != RoleInitiator || !r.open() || !r.turnDeadline.Valid || now.Before(parseWireTime(r.turnDeadline.String)) {
		return false, nil
	}
	tr, err := loadTranscript(ctx, tx, r.session)
	if err != nil {
		return false, err
	}
	t := Next(tr.metas(stateApplied), r.roundsMax)
	outcome := OutcomeEscalated
	if t.Slot == 1 && !t.Done {
		outcome = OutcomeCancelled
	}
	out, err := s.closeTx(ctx, tx, r, outcome, ReasonTimeout, "daemon", now)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("debate: commit: %w", err)
	}
	s.Outbox.Wake()
	out.run(ctx)
	return true, nil
}
