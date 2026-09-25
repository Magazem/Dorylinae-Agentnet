package debate

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// Human constraints (Docs/protocol/debate.md §Human constraints, ticket 3.4).
// A constraint is added through a local human approval (OD-P3-3): the daemon
// prepares it at debate_constrain (PrepareConstraint), and only the
// approval's Perform stores and sends it (AddConstraintTx). Nothing is stored
// before the approval, so a rejected or expired one leaves no trace.

// MailConstraint carries one constraint to the peer (both directions).
const MailConstraint = "debate.constraint"

// EventConstraint notifies the peer's side of a received constraint.
const EventConstraint = "debate.constraint"

// Limits (§Human constraints, "Limits"; review 43 H2).
const (
	// MaxActiveConstraints is the sender's limit: active constraints per
	// debate, both sides together (constraint_limit).
	MaxActiveConstraints = 10
	// maxStoredConstraints bounds what a receiver stores per debate, every
	// state; beyond it a mail is ignored (a misbehaving peer).
	maxStoredConstraints = 20
)

// debate_constraints.state.
const (
	ConstraintActive = "active"
	ConstraintLate   = "late"   // B's own constraint that A's close did not list
	ConstraintExcess = "excess" // an 11th received on A: never listed, never shown
)

// ErrConstraintLimit is returned when the debate already holds MaxActiveConstraints active
// constraints (constraint_limit).
var ErrConstraintLimit = errors.New("debate: the debate already has 10 constraints")

// PendingConstraint is a constraint waiting for its approval: nothing of it is
// stored until AddConstraintTx.
type PendingConstraint struct {
	Session   string
	RequestID string
	Peer      string
	Role      string // the author's role: this side's
	ID        string // c-<32 hex>
	Text      string
}

// newConstraintID returns "c-" + 32 hex from crypto/rand.
func newConstraintID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("debate: constraint id: %w", err)
	}
	return "c-" + hex.EncodeToString(b[:]), nil
}

// constraintPhase reports whether a constraint may be added in phase.
func constraintPhase(phase string) bool {
	return phase == PhasePositions || phase == PhaseRounds || phase == PhaseConverge
}

// checkConstrainable is the state check shared by debate_constrain and the
// approval's precondition: the debate is in positions, rounds or converge
// (bad_state) and holds fewer than 10 active constraints (constraint_limit).
func checkConstrainable(ctx context.Context, q queryer, r row) error {
	if !constraintPhase(r.phase) {
		return &BadStateError{Phase: r.phase, Msg: fmt.Sprintf("%s is %s: constraints are added in positions, rounds or converge", r.session, r.phase)}
	}
	n, err := countConstraints(ctx, q, r.session, ConstraintActive)
	if err != nil {
		return err
	}
	if n >= MaxActiveConstraints {
		return ErrConstraintLimit
	}
	return nil
}

// PrepareConstraint checks a debate_constrain call: the text (bad_request,
// FieldError "text"), the debate (unknown_session), its phase (bad_state) and
// the limit (constraint_limit). It stores nothing.
func (s *Store) PrepareConstraint(ctx context.Context, id, text string) (PendingConstraint, error) {
	if err := ValidateConstraintText(text); err != nil {
		return PendingConstraint{}, err
	}
	r, err := resolve(ctx, s.DB, id)
	if err != nil {
		return PendingConstraint{}, err
	}
	if err := checkConstrainable(ctx, s.DB, r); err != nil {
		return PendingConstraint{}, err
	}
	cid, err := newConstraintID()
	if err != nil {
		return PendingConstraint{}, err
	}
	return PendingConstraint{Session: r.session, RequestID: r.requestID, Peer: r.peer, Role: r.role, ID: cid, Text: text}, nil
}

// ConstraintPreconditionTx re-checks pc at approval_confirm: the debate is
// still in positions, rounds or converge with room for one more constraint.
func (s *Store) ConstraintPreconditionTx(ctx context.Context, tx *sql.Tx, pc PendingConstraint) error {
	r, err := getRow(ctx, tx, pc.Session)
	if err != nil {
		return err
	}
	return checkConstrainable(ctx, tx, r)
}

// AddConstraintTx stores pc active and sends debate.constraint to the peer,
// in tx (the approval's transaction). at is the approval time in whole
// seconds, stored exactly as sent so both sides order constraints alike.
// The caller audits debate.constraint in the same transaction and calls
// Outbox.Wake after commit.
func (s *Store) AddConstraintTx(ctx context.Context, tx *sql.Tx, pc PendingConstraint, approvalID string) error {
	at := wireTime(s.now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO debate_constraints (session, id, author, text, at, state, approval) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		pc.Session, pc.ID, pc.Role, pc.Text, at, ConstraintActive, approvalID); err != nil {
		return fmt.Errorf("debate: store constraint: %w", err)
	}
	body := map[string]any{"at": at, "id": pc.ID, "request": pc.RequestID, "session": pc.Session, "text": pc.Text}
	if _, err := s.Outbox.SubmitTx(ctx, tx, pc.Peer, MailConstraint, body); err != nil {
		return err
	}
	return nil
}

// ConstraintKind is the receiver Kind for debate.constraint (both
// directions).
func (s *Store) ConstraintKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyConstraint, After: s.after}
}

// applyConstraint stores a peer's constraint (author = msg.from's role). A
// never adds one after it left converge (ignored "closed"), and stores one
// beyond the 10 active as excess (ignored "limit"); B stores every valid one
// active, because A's close decides the set (review 43 H2), and then re-checks
// a close it holds for missing constraints. Either side stores at most 20.
func (s *Store) applyConstraint(ctx context.Context, tx *sql.Tx, op *mail.Opened) (err error) {
	b := op.Msg.Body
	if err = strictBody(b, "at", "id", "request", "session", "text"); err != nil {
		return err
	}
	at, reqID, sid, err := commonBody(b)
	if err != nil {
		return err
	}
	cid, ok := b["id"].(string)
	if !ok || !constraintIDPattern.MatchString(cid) {
		return badBody("id must be a constraint id")
	}
	text, ok := b["text"].(string)
	if !ok {
		return badBody("text must be a string")
	}
	if verr := ValidateConstraintText(text); verr != nil {
		return badBody("%s", verr.Error())
	}
	r, found, err := bodyRow(ctx, tx, op, sid, reqID, "")
	if err != nil {
		return err
	}
	var out afters
	defer func() {
		if err == nil {
			pending.Store(op, out)
		}
	}()
	if !found {
		s.ignore(op, &out, sid, MailConstraint, "unknown", false)
		return nil
	}
	if r.phase == PhaseClosing || r.phase == PhaseClosed || r.phase == PhaseBroken {
		s.ignore(op, &out, r.session, MailConstraint, "closed", false)
		return nil
	}
	var dup int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM debate_constraints WHERE session = ? AND id = ?`, r.session, cid).Scan(&dup); err != nil {
		return fmt.Errorf("debate: read constraint: %w", err)
	}
	if dup > 0 {
		s.ignore(op, &out, r.session, MailConstraint, "duplicate", false)
		return nil
	}
	total, err := countConstraints(ctx, tx, r.session, "")
	if err != nil {
		return err
	}
	if total >= maxStoredConstraints {
		s.ignore(op, &out, r.session, MailConstraint, "limit", false)
		return nil
	}
	state := ConstraintActive
	if r.role == RoleInitiator {
		active, err := countConstraints(ctx, tx, r.session, ConstraintActive)
		if err != nil {
			return err
		}
		if active >= MaxActiveConstraints {
			state = ConstraintExcess
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO debate_constraints (session, id, author, text, at, state) VALUES (?, ?, ?, ?, ?, ?)`,
		r.session, cid, other(r.role), text, at, state); err != nil {
		return fmt.Errorf("debate: store constraint: %w", err)
	}
	if state == ConstraintExcess {
		// Kept so the record of what arrived is complete, but not shown to
		// agents and never listed in the close.
		s.ignore(op, &out, r.session, MailConstraint, "limit", false)
		return nil
	}
	out.add(s.audit("daemon", "debate.constraint_in", map[string]any{"session": r.session, "peer": r.peer, "id": cid}))
	out.add(s.event(EventConstraint, r.session, r.peer, r.requestID))
	if r.role == RoleRespondent && r.closeBody.Valid {
		return s.retryHeldClose(ctx, tx, r, &out)
	}
	return nil
}

// countConstraints counts sid's constraints in state ("" for every state).
func countConstraints(ctx context.Context, q queryer, sid, state string) (int, error) {
	var n int
	var err error
	if state == "" {
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM debate_constraints WHERE session = ?`, sid).Scan(&n)
	} else {
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM debate_constraints WHERE session = ? AND state = ?`, sid, state).Scan(&n)
	}
	if err != nil {
		return 0, fmt.Errorf("debate: count constraints: %w", err)
	}
	return n, nil
}

// missingConstraints returns the ids in list that sid does not hold in any
// state. B stores its own constraints before sending them, so a missing id
// is one A authored whose mail the close overtook: B holds the close until
// it arrives (decision.md §Signing step 2, review 43 H2).
func missingConstraints(ctx context.Context, tx *sql.Tx, sid string, list []string) ([]string, error) {
	var missing []string
	for _, id := range list {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM debate_constraints WHERE session = ? AND id = ?`, sid, id).Scan(&n); err != nil {
			return nil, fmt.Errorf("debate: read constraint: %w", err)
		}
		if n == 0 {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

// markLateTx marks B's active constraints that A's close did not list as
// late (§Human constraints, "Ordering"): they reached A after the close, or
// A kept them as excess. They are in neither Decision.
func markLateTx(ctx context.Context, tx *sql.Tx, sid string, listed []string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM debate_constraints WHERE session = ? AND state = ?`, sid, ConstraintActive)
	if err != nil {
		return fmt.Errorf("debate: read constraints: %w", err)
	}
	in := map[string]bool{}
	for _, id := range listed {
		in[id] = true
	}
	var late []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		if !in[id] {
			late = append(late, id)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range late {
		if _, err := tx.ExecContext(ctx, `UPDATE debate_constraints SET state = ? WHERE session = ? AND id = ?`, ConstraintLate, sid, id); err != nil {
			return fmt.Errorf("debate: mark constraint late: %w", err)
		}
	}
	return nil
}

// retryHeldClose re-applies a close B holds for missing constraints, after
// one arrived. The stored body was checked when it first arrived.
func (s *Store) retryHeldClose(ctx context.Context, tx *sql.Tx, r row, out *afters) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(r.closeBody.String)))
	dec.UseNumber()
	var b map[string]any
	if err := dec.Decode(&b); err != nil {
		return fmt.Errorf("debate: held close: %w", err)
	}
	return s.applyCloseOnB(ctx, tx, r, b, out)
}

// ViewConstraint is one constraint in a debate view (§IPC, "constraints").
type ViewConstraint struct {
	ID     string
	Author string
	At     string
	Text   string
	State  string // active or late
}

// countVisibleConstraints counts sid's constraints an agent may see (active
// and late, never excess), for the list view (review 46 L7: List() keeps
// counts, not the texts).
func countVisibleConstraints(ctx context.Context, q queryer, sid string) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM debate_constraints WHERE session = ? AND state IN (?, ?)`,
		sid, ConstraintActive, ConstraintLate).Scan(&n); err != nil {
		return 0, fmt.Errorf("debate: count constraints: %w", err)
	}
	return n, nil
}

// loadConstraints returns sid's constraints an agent may see, ordered by
// (at, id): every state but excess (§Human constraints, "Limits").
func loadConstraints(ctx context.Context, q queryer, sid string) ([]ViewConstraint, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, author, at, text, state FROM debate_constraints WHERE session = ? AND state IN (?, ?) ORDER BY at, id`,
		sid, ConstraintActive, ConstraintLate)
	if err != nil {
		return nil, fmt.Errorf("debate: read constraints: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ViewConstraint
	for rows.Next() {
		var c ViewConstraint
		if err := rows.Scan(&c.ID, &c.Author, &c.At, &c.Text, &c.State); err != nil {
			return nil, fmt.Errorf("debate: scan constraint: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
