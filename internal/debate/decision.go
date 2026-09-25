package debate

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/decision"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// The Decision of a debate (Docs/protocol/decision.md, ticket 3.3a): derived
// by each daemon from its own tables at close, signed by both identity keys
// through one round trip (debate.close carries A's hash and signature,
// debate.sign carries B's), and stored in the decisions table (migration 20).

// MailSign is B's answer to an agreed or escalated close (B -> A).
const MailSign = "debate.sign"

// decisions.state.
const (
	DecisionAwaitingPeer = "awaiting_peer"
	DecisionSigned       = "signed"
	DecisionPeerRefused  = "peer_refused"
)

// refusedMismatch is debate.sign's refused value (decision.md §Signing).
const refusedMismatch = "mismatch"

var decisionIDPattern = regexp.MustCompile(`^d-[0-9a-f]{32}$`)

// DecisionSchema is the validator set decision.Verify needs: the same
// validators the daemon applies to entries, titles, topics and constraints.
func DecisionSchema() decision.Schema {
	return decision.Schema{
		Entry: func(kind string, canon []byte) error {
			_, err := ParseEntry(kind, canon)
			return err
		},
		Title:      request.ValidateTitle,
		Topic:      ValidateTopic,
		Constraint: ValidateConstraintText,
	}
}

// deriveTx builds canonical(decision) from this side's tables (decision.md
// §Derivation): the stored request, the entries at slots < entries (applied
// ones, and on B its own sent ones), the listed constraints, and the close's
// outcome, reason and at. It uses no local name, clock or configuration.
func (s *Store) deriveTx(ctx context.Context, tx *sql.Tx, r row, tr transcript, entries int, listed []string, outcome, reason, at string) ([]byte, error) {
	dir := "in"
	if r.role == RoleInitiator {
		dir = "out"
	}
	var body string
	if err := tx.QueryRowContext(ctx, `SELECT body FROM requests WHERE direction = ? AND peer = ? AND id = ?`, dir, r.peer, r.requestID).Scan(&body); err != nil {
		return nil, fmt.Errorf("debate: read request for the Decision: %w", err)
	}
	var es []decision.Entry
	for slot, e := range tr {
		if slot < entries && (e.state == stateApplied || e.state == stateSent) {
			es = append(es, decision.Entry{Slot: slot, Author: e.author, Kind: e.kind, Canon: e.canon})
		}
	}
	var cs []decision.Constraint
	for _, id := range listed {
		var c decision.Constraint
		err := tx.QueryRowContext(ctx, `SELECT id, author, at, text FROM debate_constraints WHERE session = ? AND id = ?`, r.session, id).
			Scan(&c.ID, &c.Author, &c.At, &c.Text)
		if errors.Is(err, sql.ErrNoRows) {
			continue // B's own record on a refusal; the caller checked otherwise
		}
		if err != nil {
			return nil, fmt.Errorf("debate: read constraint: %w", err)
		}
		cs = append(cs, c)
	}
	a, b := r.keys(s.Self)
	return decision.Derive(decision.Input{
		Session: r.session, Initiator: a, Respondent: b, Request: []byte(body), RoundsMax: r.roundsMax,
		Entries: es, Count: entries, Constraints: cs, Outcome: outcome, Reason: reason, Closed: at,
	})
}

// sign signs canon with the daemon's identity key (OD-P3-5: automatic, no
// human step), clearing the key copy after use.
func (s *Store) sign(canon []byte) (string, error) {
	if s.Priv == nil {
		return "", errors.New("debate: no identity key to sign the Decision")
	}
	priv, err := s.Priv()
	if err != nil {
		return "", fmt.Errorf("debate: identity key: %w", err)
	}
	defer clear(priv)
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("debate: identity key has the wrong size")
	}
	// The key must be the one the Decision names (participants): a keystore
	// that no longer matches the card would sign something the peer refuses
	// as "signature" (review 47 L3; relayclient's keystoreSigner checks the
	// same).
	if base64.RawURLEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)) != s.Self {
		return "", errors.New("debate: stored identity key does not match the agent card")
	}
	return decision.Sign(priv, canon), nil
}

// insertDecision stores this side's Decision row.
func insertDecision(ctx context.Context, tx *sql.Tx, r row, canon []byte, hash, sigI, sigR, peerHash, state string, now time.Time) error {
	null := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decisions (id, session, role, peer, decision, hash, sig_initiator, sig_respondent, peer_hash, state, created, updated)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		decision.ID(r.session), r.session, r.role, r.peer, string(canon), hash, null(sigI), null(sigR), null(peerHash), state,
		storeTime(now), storeTime(now)); err != nil {
		return fmt.Errorf("debate: store Decision: %w", err)
	}
	return nil
}

// createAudit is decision.create's detail: ids, the hash and sizes, never
// content (decision.md §Audit).
func (s *Store) createAudit(r row, outcome, hash string, bytes int, signedBy ...string) func(context.Context) {
	return s.audit("daemon", "decision.create", map[string]any{
		"id": decision.ID(r.session), "session": r.session, "peer": r.peer, "outcome": outcome,
		"hash": hash, "bytes": bytes, "signed_by": signedBy,
	})
}

// decideTx is A's part of decision.md §Signing step 1, inside closeTx: derive
// the Decision, sign it, store it awaiting_peer and add the hash and the
// signature to the close body. It returns the loaded transcript and the
// Decision hash so closeTx can reuse them for the experience record (written
// before B signs, Docs/protocol/experience.md "acceptance").
func (s *Store) decideTx(ctx context.Context, tx *sql.Tx, r row, entries int, listed []string, outcome, reason string, body map[string]any, now time.Time, out *afters) (transcript, string, error) {
	tr, err := loadTranscript(ctx, tx, r.session)
	if err != nil {
		return nil, "", err
	}
	at, _ := body["at"].(string)
	// closed is never before opened (review 47 M1): if this clock went back
	// since the request was created, the close carries the request's
	// created instead, so B does not refuse and verify step 5 holds.
	var opened string
	if err := tx.QueryRowContext(ctx, `SELECT created FROM requests WHERE direction = 'out' AND peer = ? AND id = ?`, r.peer, r.requestID).Scan(&opened); err != nil {
		return nil, "", fmt.Errorf("debate: read request for the Decision: %w", err)
	}
	if at < opened { // both whole-second UTC wire times: string order is time order
		at = opened
		body["at"] = at
	}
	canon, err := s.deriveTx(ctx, tx, r, tr, entries, listed, outcome, reason, at)
	if err != nil {
		return nil, "", err
	}
	sig, err := s.sign(canon)
	if err != nil {
		return nil, "", err
	}
	hash := decision.Hash(canon)
	if err := insertDecision(ctx, tx, r, canon, hash, sig, "", "", DecisionAwaitingPeer, now); err != nil {
		return nil, "", err
	}
	body["decision"], body["sig"] = hash, sig
	out.add(s.createAudit(r, outcome, hash, len(canon), RoleInitiator))
	return tr, hash, nil
}

// closeGap compares A's entries count with B's transcript (decision.md
// §Signing step 2). refuse names a claim B can reject at once: fewer than
// two entries, an A-authored entry B holds at a slot A did not count (A
// cannot cut its own applied entries), or a B-authored slot A counted that B
// never sent (review 43 H2). hold is an A-authored slot B lacks (the close
// overtook it).
func closeGap(tr transcript, roundsMax, entries int) (refuse string, hold bool) {
	if entries < 2 {
		return "entries", false
	}
	for slot, e := range tr {
		if e.author == RoleInitiator && slot >= entries {
			return "cut", false
		}
	}
	t := Next(tr.metas(stateApplied, stateSent), roundsMax)
	switch {
	case t.Slot >= entries:
		return "", false
	case t.Done:
		return "entries", false // A counts slots after the answer
	case t.Author == RoleRespondent:
		return "unsent", false
	}
	return "", true
}

// signOnB is B's success path of decision.md §Signing step 2 (the caller has
// checked the close): sign, store the Decision with both signatures, and
// queue debate.sign. It returns B's signature.
func (s *Store) signOnB(ctx context.Context, tx *sql.Tx, r row, canon []byte, sigA string, now time.Time, out *afters) error {
	sigB, err := s.sign(canon)
	if err != nil {
		return err
	}
	hash := decision.Hash(canon)
	if err := insertDecision(ctx, tx, r, canon, hash, sigA, sigB, "", DecisionSigned, now); err != nil {
		return err
	}
	if _, err := s.Outbox.SubmitTx(ctx, tx, r.peer, MailSign, map[string]any{
		"at": wireTime(now), "decision": hash, "request": r.requestID, "session": r.session, "sig": sigB,
	}); err != nil {
		return err
	}
	outcome := ""
	if o, err := decisionOutcome(canon); err == nil {
		outcome = o
	}
	out.add(s.createAudit(r, outcome, hash, len(canon), RoleInitiator, RoleRespondent))
	return nil
}

// refuseOnB is B's mismatch path (decision.md §Signing, "If B refuses"): B
// stores its own derivation (with the outcome its own transcript gives) and
// A's claimed hash, sets peer_refused, breaks the debate, closes its mirror
// cancelled, notifies debate.broken and sends debate.sign with refused. B
// never signs. why is the audited reason (content-free).
func (s *Store) refuseOnB(ctx context.Context, tx *sql.Tx, r row, tr transcript, b map[string]any, raw []byte, why string, out *afters) error {
	now := s.now()
	claimed, _ := b["decision"].(string)
	entries, _ := intMember(b, "entries")
	at, _ := b["at"].(string)
	listed := listedConstraints(b)
	// B's own record: its transcript up to A's count, the outcome rule 6
	// gives for it. None exists without both positions.
	own := ""
	var answer *Answer
	for slot, e := range tr {
		if slot < entries && (e.state == stateApplied || e.state == stateSent) {
			if a, ok := e.entry.(*Answer); ok {
				answer = a
			}
		}
	}
	outcome, reason := decision.Expected(answer != nil, answer != nil && answer.Accept)
	canon, err := s.deriveTx(ctx, tx, r, tr, entries, listed, outcome, reason, at)
	switch {
	case err == nil:
		own = decision.Hash(canon)
		if err := insertDecision(ctx, tx, r, canon, own, "", "", claimed, DecisionPeerRefused, now); err != nil {
			return err
		}
	case errors.Is(err, decision.ErrIncomplete), errors.Is(err, decision.ErrClosedBeforeOpened):
		// B holds no record (no position, or a close before the request
		// was created): the refusal carries the hash of the empty message,
		// which no Decision has.
		own = decision.Hash(nil)
	default:
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE debates SET phase = ?, turn_deadline = NULL, close_body = ?, updated = ? WHERE session = ?`,
		PhaseBroken, string(raw), storeTime(now), r.session); err != nil {
		return fmt.Errorf("debate: refuse close: %w", err)
	}
	if _, err := s.Outbox.SubmitTx(ctx, tx, r.peer, MailSign, map[string]any{
		"at": wireTime(now), "decision": own, "refused": refusedMismatch, "request": r.requestID, "session": r.session,
	}); err != nil {
		return err
	}
	out.add(s.audit("daemon", "decision.refuse", map[string]any{
		"id": decision.ID(r.session), "session": r.session, "peer": r.peer, "reason": why,
	}))
	return s.closeMirrorTx(ctx, tx, r, worksession.OutcomeCancelled, "session cancelled", EventBroken, OutcomeCancelled, tr, RoleInitiator, nil, now, out)
}

// decisionOutcome reads the outcome member of a canonical Decision.
func decisionOutcome(canon []byte) (string, error) {
	v, err := entryValue(canon)
	if err != nil {
		return "", err
	}
	o, _ := v.(map[string]any)
	s, _ := o["outcome"].(string)
	return s, nil
}

func listedConstraints(b map[string]any) []string {
	var listed []string
	if list, ok := b["constraints"].([]any); ok {
		for _, v := range list {
			if id, ok := v.(string); ok {
				listed = append(listed, id)
			}
		}
	}
	return listed
}

// SignKind is the receiver Kind for debate.sign (B -> A).
func (s *Store) SignKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applySign, After: s.after}
}

// applySign is decision.md §Signing step 3 on A: B's signature over A's own
// message for the hash A stored makes the Decision signed and the debate
// closed. A refusal, another hash or a bad signature makes it peer_refused
// (B's hash stored as peer_hash, decision.refuse audited). Either way A's
// debate leaves closing: A already decided the outcome.
func (s *Store) applySign(ctx context.Context, tx *sql.Tx, op *mail.Opened) (err error) {
	b := op.Msg.Body
	if err = strictBody(b, "at", "decision", "refused", "request", "session", "sig"); err != nil {
		return err
	}
	_, reqID, sid, err := commonBody(b)
	if err != nil {
		return err
	}
	claimed, ok := b["decision"].(string)
	if !ok || !hex64.MatchString(claimed) {
		return badBody("decision must be 64 lowercase hex characters")
	}
	refused, hasRefused := b["refused"]
	sig, hasSig := b["sig"].(string)
	if _, present := b["sig"]; present && (!hasSig || sig == "") {
		return badBody("sig must be a non-empty string")
	}
	if hasRefused == hasSig {
		return badBody("a debate.sign carries sig or refused, not both")
	}
	if hasRefused && refused != refusedMismatch {
		return badBody("refused must be %q", refusedMismatch)
	}
	r, found, err := bodyRow(ctx, tx, op, sid, reqID, RoleInitiator)
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
		s.ignore(op, &out, sid, MailSign, "unknown", false)
		return nil
	}
	var canon, hash, state string
	err = tx.QueryRowContext(ctx, `SELECT decision, hash, state FROM decisions WHERE session = ?`, r.session).Scan(&canon, &hash, &state)
	if errors.Is(err, sql.ErrNoRows) {
		s.ignore(op, &out, r.session, MailSign, "state", false)
		return nil
	}
	if err != nil {
		return fmt.Errorf("debate: read Decision: %w", err)
	}
	if r.phase != PhaseClosing || state != DecisionAwaitingPeer {
		s.ignore(op, &out, r.session, MailSign, "closed", false)
		return nil
	}
	now := s.now()
	outcome, err := decisionOutcome([]byte(canon))
	if err != nil {
		return fmt.Errorf("debate: stored Decision: %w", err)
	}
	if err := setClosed(ctx, tx, r.session, outcome, r.reason.String, now); err != nil {
		return err
	}
	why := ""
	switch {
	case hasRefused:
		why = refusedMismatch
	case claimed != hash:
		why = "hash"
	case !decision.VerifySignature(r.peer, []byte(canon), sig):
		why = "signature"
	}
	if why != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE decisions SET state = ?, peer_hash = ?, updated = ? WHERE session = ?`,
			DecisionPeerRefused, claimed, storeTime(now), r.session); err != nil {
			return fmt.Errorf("debate: refuse Decision: %w", err)
		}
		out.add(s.audit("daemon", "decision.refuse", map[string]any{
			"id": decision.ID(r.session), "session": r.session, "peer": r.peer, "reason": why,
		}))
		out.add(s.event(EventBroken, r.session, r.peer, r.requestID))
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE decisions SET state = ?, sig_respondent = ?, updated = ? WHERE session = ?`,
		DecisionSigned, sig, storeTime(now), r.session); err != nil {
		return fmt.Errorf("debate: store signature: %w", err)
	}
	out.add(s.audit("daemon", "decision.sign_in", map[string]any{"id": decision.ID(r.session), "session": r.session, "peer": r.peer}))
	return nil
}

// DecisionRecord is a stored Decision (the decisions row).
type DecisionRecord struct {
	ID, Session, Role, Peer string
	Decision                []byte // canonical
	Hash                    string
	SigInitiator            string
	SigRespondent           string
	PeerHash                string
	State                   string
	Created, Updated        string
}

// Decision returns the stored Decision of a debate (s- or r- id, or its d- id).
func (s *Store) Decision(ctx context.Context, id string) (DecisionRecord, error) {
	q := `SELECT id, session, role, peer, decision, hash, sig_initiator, sig_respondent, peer_hash, state, created, updated FROM decisions WHERE `
	var sc *sql.Row
	if decisionIDPattern.MatchString(id) {
		sc = s.DB.QueryRowContext(ctx, q+`id = ?`, id)
	} else {
		r, err := resolve(ctx, s.DB, id)
		if err != nil {
			return DecisionRecord{}, err
		}
		sc = s.DB.QueryRowContext(ctx, q+`session = ?`, r.session)
	}
	var d DecisionRecord
	var canon string
	var sigI, sigR, peerHash sql.NullString
	err := sc.Scan(&d.ID, &d.Session, &d.Role, &d.Peer, &canon, &d.Hash, &sigI, &sigR, &peerHash, &d.State, &d.Created, &d.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return DecisionRecord{}, ErrUnknownDecision
	}
	if err != nil {
		return DecisionRecord{}, fmt.Errorf("debate: read Decision: %w", err)
	}
	d.Decision, d.SigInitiator, d.SigRespondent, d.PeerHash = []byte(canon), sigI.String, sigR.String, peerHash.String
	return d, nil
}

// DecisionList returns every stored Decision, newest first, optionally
// narrowed by state and peer (Docs/protocol/decision.md §IPC, decision_list).
func (s *Store) DecisionList(ctx context.Context, state, peer string) ([]DecisionRecord, error) {
	q := `SELECT id, session, role, peer, decision, hash, sig_initiator, sig_respondent, peer_hash, state, created, updated FROM decisions WHERE 1=1`
	var args []any
	if state != "" {
		q += ` AND state = ?`
		args = append(args, state)
	}
	if peer != "" {
		q += ` AND peer = ?`
		args = append(args, peer)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY created DESC, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("debate: list decisions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DecisionRecord
	for rows.Next() {
		var d DecisionRecord
		var canon string
		var sigI, sigR, peerHash sql.NullString
		if err := rows.Scan(&d.ID, &d.Session, &d.Role, &d.Peer, &canon, &d.Hash, &sigI, &sigR, &peerHash, &d.State, &d.Created, &d.Updated); err != nil {
			return nil, fmt.Errorf("debate: list decisions: %w", err)
		}
		d.Decision, d.SigInitiator, d.SigRespondent, d.PeerHash = []byte(canon), sigI.String, sigR.String, peerHash.String
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// DecisionOutcome reads the outcome member of a stored canonical Decision
// (exported for the daemon's decision_list/decision_show views).
func DecisionOutcome(canon []byte) (string, error) {
	return decisionOutcome(canon)
}

// DecisionBySession returns the stored Decision of a session, if any, found
// reporting false when the debate has none yet (open, or cancelled).
func (s *Store) DecisionBySession(ctx context.Context, session string) (DecisionRecord, bool, error) {
	d, err := s.Decision(ctx, session)
	if errors.Is(err, ErrUnknownDecision) {
		return DecisionRecord{}, false, nil
	}
	if err != nil {
		return DecisionRecord{}, false, err
	}
	return d, true, nil
}
