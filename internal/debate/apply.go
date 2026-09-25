package debate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/decision"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Receiving the debate.* kinds (Docs/protocol/debate.md §Submitting entries,
// "Applying peer entries", §Commit-reveal, §Kinds). Apply runs inside the
// mail dedupe transaction and touches only tx; audits, notifications and
// the echo run after commit.

// pending holds each applied mail's after-commit work, keyed by the
// *mail.Opened pointer (as internal/request's pendingApply).
var pending sync.Map // map[*mail.Opened]afters

// EntryKind is the receiver Kind for debate.entry (both directions).
func (s *Store) EntryKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyEntry, After: s.after}
}

// RevealKind is the receiver Kind for debate.reveal (A -> B).
func (s *Store) RevealKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyReveal, After: s.after}
}

// CloseKind is the receiver Kind for debate.close (A -> B).
func (s *Store) CloseKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyClose, After: s.after}
}

func (s *Store) after(ctx context.Context, op *mail.Opened) {
	v, ok := pending.LoadAndDelete(op)
	if !ok {
		return
	}
	if s.Outbox != nil {
		s.Outbox.Wake()
	}
	v.(afters).run(ctx)
}

// ignore records an ignored body: it is applied nowhere, so its inbox copy is
// withheld (as an ignored ws.result, D18), and debate.ignored is audited.
// echo re-sends A's last_state (the 10-minute rule) so a B that missed a mail
// catches up.
func (s *Store) ignore(op *mail.Opened, out *afters, sid, kind, reason string, echo bool) {
	op.Withhold = true
	out.add(s.audit("daemon", "debate.ignored", map[string]any{"session": sid, "peer": op.Msg.From, "kind": kind, "reason": reason}))
	if echo {
		out.add(func(ctx context.Context) { s.echo(ctx, sid) })
	}
}

// bodyRow checks a body's session against the derived id for the pair and
// request (so a body cannot be moved to another debate) and finds the
// debate row. wantRole is the role this daemon must hold ("" for either).
// ok is false when no row exists (the caller ignores the body).
func bodyRow(ctx context.Context, tx *sql.Tx, op *mail.Opened, sid, reqID, wantRole string) (row, bool, error) {
	asA := worksession.DeriveID(op.Msg.To, op.Msg.From, reqID)
	asB := worksession.DeriveID(op.Msg.From, op.Msg.To, reqID)
	role := ""
	switch sid {
	case asA:
		role = RoleInitiator
	case asB:
		role = RoleRespondent
	default:
		return row{}, false, badBody("session is not the derived id for the pair and request")
	}
	if wantRole != "" && role != wantRole {
		return row{}, false, badBody("this kind is sent by the initiator only")
	}
	r, err := getRow(ctx, tx, sid)
	if errors.Is(err, ErrUnknownDebate) {
		return row{}, false, nil
	}
	if err != nil {
		return row{}, false, err
	}
	if r.role != role || r.peer != op.Msg.From || r.requestID != reqID {
		return row{}, false, badBody("session does not belong to this pair and request")
	}
	return r, true, nil
}

func (s *Store) applyEntry(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	b := op.Msg.Body
	if err := strictBody(b, "at", "entry", "kind", "request", "session", "slot"); err != nil {
		return err
	}
	at, reqID, sid, err := commonBody(b)
	if err != nil {
		return err
	}
	kind, _ := b["kind"].(string)
	if !ValidKind(kind) {
		return badBody("kind must be position, move, proposal or answer")
	}
	slot, err := intMember(b, "slot")
	if err != nil {
		return err
	}
	if slot < 1 || slot > MaxSlot {
		return badBody("slot must be 1-%d", MaxSlot)
	}
	r, ok, err := bodyRow(ctx, tx, op, sid, reqID, "")
	if err != nil {
		return err
	}
	var out afters
	if !ok {
		s.ignore(op, &out, sid, MailEntry, "unknown", false)
		pending.Store(op, out)
		return nil
	}
	e, canon, err := DecodeEntry(kind, b["entry"])
	if err != nil {
		return badBody("entry: %s", err.Error())
	}
	if r.role == RoleInitiator {
		err = s.applyEntryOnA(ctx, tx, op, r, slot, kind, e, canon, at, &out)
	} else {
		err = s.applyEntryOnB(ctx, tx, op, r, slot, kind, e, canon, at, &out)
	}
	if err != nil {
		return err
	}
	pending.Store(op, out)
	return nil
}

// applyEntryOnA applies B's entry on A: only in B's turn, at the next slot.
// A slot-1 position for a debate A still holds invited (the request pending
// or deferred) opens the session as if the accept had arrived first (review
// 43 M3). Applying slot 1 reveals slot 0 in the same transaction; applying
// the answer decides the outcome and starts the close.
func (s *Store) applyEntryOnA(ctx context.Context, tx *sql.Tx, op *mail.Opened, r row, slot int, kind string, e Entry, canon []byte, at string, out *afters) error {
	now := s.now()
	if r.phase == PhaseInvited {
		if slot != 1 || kind != KindPosition {
			s.ignore(op, out, r.session, MailEntry, "state", false)
			return nil
		}
		var teamID, state string
		err := tx.QueryRowContext(ctx, `SELECT team_id, state FROM requests WHERE direction = 'out' AND peer = ? AND id = ?`, r.peer, r.requestID).Scan(&teamID, &state)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && state != request.StatePending && state != request.StateDeferred) {
			s.ignore(op, out, r.session, MailEntry, "state", false)
			return nil
		}
		if err != nil {
			return fmt.Errorf("debate: read out request: %w", err)
		}
		if err := s.Sessions.OpenSession(ctx, tx, worksession.RoleRequester, r.peer, r.requestID, teamID, now); err != nil {
			return err
		}
		if r, err = getRow(ctx, tx, r.session); err != nil {
			return err
		}
	}
	if !r.open() {
		s.ignore(op, out, r.session, MailEntry, "closed", true)
		return nil
	}
	tr, err := loadTranscript(ctx, tx, r.session)
	if err != nil {
		return err
	}
	t := Next(tr.metas(stateApplied), r.roundsMax)
	if t.Done || t.Slot != slot || t.Author != RoleRespondent {
		s.ignore(op, out, r.session, MailEntry, "turn", true)
		return nil
	}
	if t.Kind != kind {
		return badBody("slot %d expects a %s", slot, t.Kind)
	}
	if err := checkEntryTargets(tr, e, RoleRespondent, slot, stateApplied); err != nil {
		return badBody("entry: %s", err.Error())
	}
	if err := insertEntry(ctx, tx, r.session, slot, RoleRespondent, kind, canon, at, stateApplied); err != nil {
		return err
	}
	tr[slot] = stored{slot: slot, author: RoleRespondent, kind: kind, at: at, state: stateApplied, canon: canon, entry: e}
	out.add(s.audit("daemon", "debate.entry_in", entryAudit(r.session, r.peer, slot, kind, canon, e)))
	if slot == 1 {
		fn, err := s.revealTx(ctx, tx, r, tr, now)
		if err != nil {
			return err
		}
		out.add(fn)
	}
	if _, err := s.advanceTx(ctx, tx, r, tr, now); err != nil {
		return err
	}
	if kind == KindAnswer {
		outcome, reason := OutcomeEscalated, ReasonRejected
		if a, ok := e.(*Answer); ok && a.Accept {
			outcome, reason = OutcomeAgreed, ReasonAccepted
		}
		if r, err = getRow(ctx, tx, r.session); err != nil {
			return err
		}
		closing, err := s.closeTx(ctx, tx, r, outcome, reason, "daemon", now)
		if err != nil {
			return err
		}
		*out = append(*out, closing...)
	}
	return nil
}

// applyEntryOnB applies A's entry on B in slot order: the next slot is
// applied (and any held ones after it), a later one is held as early until
// the missing slot arrives, a duplicate or an entry out of turn is ignored.
func (s *Store) applyEntryOnB(ctx context.Context, tx *sql.Tx, op *mail.Opened, r row, slot int, kind string, e Entry, canon []byte, at string, out *afters) error {
	switch {
	case r.phase == PhaseBroken || r.phase == PhaseClosed:
		s.ignore(op, out, r.session, MailEntry, "closed", false)
		return nil
	case !r.open():
		s.ignore(op, out, r.session, MailEntry, "state", false)
		return nil
	}
	tr, err := loadTranscript(ctx, tx, r.session)
	if err != nil {
		return err
	}
	if _, dup := tr[slot]; dup {
		s.ignore(op, out, r.session, MailEntry, "duplicate", false)
		return nil
	}
	states := turnStates(RoleRespondent)
	t := Next(tr.metas(states...), r.roundsMax)
	switch {
	case !t.Done && slot == t.Slot:
		if t.Author != RoleInitiator {
			s.ignore(op, out, r.session, MailEntry, "turn", false)
			return nil
		}
		if t.Kind != kind {
			return badBody("slot %d expects a %s", slot, t.Kind)
		}
		if err := checkEntryTargets(tr, e, RoleInitiator, slot, states...); err != nil {
			return badBody("entry: %s", err.Error())
		}
		if err := insertEntry(ctx, tx, r.session, slot, RoleInitiator, kind, canon, at, stateApplied); err != nil {
			return err
		}
		tr[slot] = stored{slot: slot, author: RoleInitiator, kind: kind, at: at, state: stateApplied, canon: canon, entry: e}
		out.add(s.audit("daemon", "debate.entry_in", entryAudit(r.session, r.peer, slot, kind, canon, e)))
		if err := s.drainTx(ctx, tx, r, tr, out); err != nil {
			return err
		}
		if _, err := s.advanceTx(ctx, tx, r, tr, s.now()); err != nil {
			return err
		}
		return s.retryIfHeld(ctx, tx, r, out)
	case !t.Done && slot >= 2 && slot > t.Slot:
		// Mail can overtake mail: hold it until the missing slot arrives
		// (its turn and targets are checked then).
		return insertEntry(ctx, tx, r.session, slot, RoleInitiator, kind, canon, at, stateEarly)
	default:
		s.ignore(op, out, r.session, MailEntry, "turn", false)
		return nil
	}
}

// drainTx applies held (early) entries on B while the next slot is A's and
// is held. A held entry that turns out wrong for its slot (kind or targets),
// or that claims a slot that is B's, is dropped and audited.
func (s *Store) drainTx(ctx context.Context, tx *sql.Tx, r row, tr transcript, out *afters) error {
	states := turnStates(RoleRespondent)
	for {
		t := Next(tr.metas(states...), r.roundsMax)
		if t.Done {
			return nil
		}
		e, ok := tr[t.Slot]
		if !ok || e.state != stateEarly {
			return nil
		}
		reason := ""
		switch {
		case t.Author != RoleInitiator:
			reason = "turn"
		case e.kind != t.Kind:
			reason = "kind"
		case checkEntryTargets(tr, e.entry, RoleInitiator, t.Slot, states...) != nil:
			reason = "targets"
		}
		if reason != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM debate_entries WHERE session = ? AND slot = ? AND state = ?`, r.session, t.Slot, stateEarly); err != nil {
				return fmt.Errorf("debate: drop held entry: %w", err)
			}
			delete(tr, t.Slot)
			out.add(s.audit("daemon", "debate.ignored", map[string]any{"session": r.session, "peer": r.peer, "kind": MailEntry, "reason": reason}))
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE debate_entries SET state = ? WHERE session = ? AND slot = ?`, stateApplied, r.session, t.Slot); err != nil {
			return fmt.Errorf("debate: apply held entry: %w", err)
		}
		e.state = stateApplied
		tr[t.Slot] = e
		out.add(s.audit("daemon", "debate.entry_in", entryAudit(r.session, r.peer, t.Slot, e.kind, e.canon, e.entry)))
	}
}

// applyReveal checks A's reveal on B (§Commit-reveal): the nonce is 64
// lowercase hex, the position is canonical and passes the schema, and the
// commitment recomputed from the request stored at receipt (the row's
// commitment, the derived session and A = msg.from), from nothing in the
// reveal but nonce and position, matches. On a match slot 0 is stored;
// otherwise the debate is broken. A reveal while B's own position is not yet
// committed to B's outbox is never stored, nor its inbox copy.
func (s *Store) applyReveal(ctx context.Context, tx *sql.Tx, op *mail.Opened) (err error) {
	b := op.Msg.Body
	if err = strictBody(b, "at", "nonce", "position", "request", "session"); err != nil {
		return err
	}
	for _, k := range []string{"nonce", "position"} {
		if _, ok := b[k]; !ok {
			return badBody("%s is required", k)
		}
	}
	at, reqID, sid, err := commonBody(b)
	if err != nil {
		return err
	}
	r, ok, err := bodyRow(ctx, tx, op, sid, reqID, RoleRespondent)
	if err != nil {
		return err
	}
	var out afters
	defer func() {
		if err == nil {
			pending.Store(op, out)
		}
	}()
	if !ok {
		s.ignore(op, &out, sid, MailReveal, "unknown", false)
		return nil
	}
	if r.phase != PhasePositions {
		reason := "state"
		switch r.phase {
		case PhaseBroken, PhaseClosed:
			reason = "closed"
		case PhaseRounds, PhaseConverge:
			reason = "duplicate"
		}
		s.ignore(op, &out, r.session, MailReveal, reason, false)
		return nil
	}
	tr, err := loadTranscript(ctx, tx, r.session)
	if err != nil {
		return err
	}
	states := turnStates(RoleRespondent)
	if t := Next(tr.metas(states...), r.roundsMax); t.Slot != 0 {
		s.ignore(op, &out, r.session, MailReveal, "turn", false)
		return nil
	}
	now := s.now()
	nonce, nonceOK := b["nonce"].(string)
	canon, cerr := agentcard.CanonicalValue(b["position"])
	good := nonceOK && ValidNonce(nonce) && cerr == nil
	var pos Entry
	if good {
		var perr error
		pos, perr = ParseEntry(KindPosition, canon)
		good = perr == nil
	}
	if good {
		a, _ := r.keys(s.Self)
		good = Commitment(r.session, a, nonce, canon) == r.commitment
	}
	if !good {
		op.Withhold = true
		return s.brokenTx(ctx, tx, r, now, &out)
	}
	if err := insertEntry(ctx, tx, r.session, 0, RoleInitiator, KindPosition, canon, at, stateApplied); err != nil {
		return err
	}
	tr[0] = stored{slot: 0, author: RoleInitiator, kind: KindPosition, at: at, state: stateApplied, canon: canon, entry: pos}
	out.add(s.audit("daemon", "debate.reveal_in", map[string]any{"session": r.session, "peer": r.peer, "ok": true}))
	if err := s.drainTx(ctx, tx, r, tr, &out); err != nil {
		return err
	}
	if _, err = s.advanceTx(ctx, tx, r, tr, now); err != nil {
		return err
	}
	return s.retryIfHeld(ctx, tx, r, &out)
}

// retryIfHeld re-checks a close B holds for a missing A entry or constraint
// after something arrived (decision.md §Signing step 2: "A held close is
// re-checked on every arrival"). On an open debate close_body is only ever a
// held close.
func (s *Store) retryIfHeld(ctx context.Context, tx *sql.Tx, r row, out *afters) error {
	cur, err := getRow(ctx, tx, r.session)
	if err != nil {
		return err
	}
	if cur.role != RoleRespondent || !cur.open() || !cur.closeBody.Valid {
		return nil
	}
	return s.retryHeldClose(ctx, tx, cur, out)
}

// brokenTx is B's answer to a bad reveal (§Commit-reveal, review 43 L3): in
// one transaction the debate becomes broken (no more entries, no Decision),
// a ws.cancel goes to A, B's work-session mirror closes cancelled and B
// completes the request through the Phase 1 path ("session cancelled"). The
// experience record joins this transaction in 3.7.
func (s *Store) brokenTx(ctx context.Context, tx *sql.Tx, r row, now time.Time, out *afters) error {
	if _, err := tx.ExecContext(ctx, `UPDATE debates SET phase = ?, turn_deadline = NULL, updated = ? WHERE session = ?`,
		PhaseBroken, storeTime(now), r.session); err != nil {
		return fmt.Errorf("debate: broken: %w", err)
	}
	if _, err := s.Outbox.SubmitTx(ctx, tx, r.peer, worksession.KindCancel,
		map[string]any{"at": wireTime(now), "request": r.requestID, "session": r.session}); err != nil {
		return err
	}
	out.add(s.audit("daemon", "debate.reveal_in", map[string]any{"session": r.session, "peer": r.peer, "ok": false}))
	out.add(s.audit("daemon", "debate.reveal_bad", map[string]any{"session": r.session, "peer": r.peer}))
	return s.closeMirrorTx(ctx, tx, r, worksession.OutcomeCancelled, "session cancelled", EventBroken, now, out)
}

// closeMirrorTx closes B's work-session mirror and completes B's request
// through the Phase 1 path with note and no result (§Kinds: "B then completes
// the request").
func (s *Store) closeMirrorTx(ctx context.Context, tx *sql.Tx, r row, wsOutcome, note, event string, now time.Time, out *afters) error {
	afterWS, err := s.Sessions.CloseDebateTx(ctx, tx, r.session, wsOutcome, now)
	if err != nil {
		return err
	}
	out.add(afterWS)
	fn, err := s.Requests.CompleteInTx(ctx, tx, r.peer, r.requestID, note, nil)
	var bse *request.BadStateError
	switch {
	case errors.As(err, &bse):
		// Already completed (or never accepted): nothing left to complete.
	case err != nil:
		return fmt.Errorf("debate: complete request: %w", err)
	default:
		out.add(fn)
	}
	if event != "" {
		out.add(s.event(event, r.session, r.peer, r.requestID))
	}
	return nil
}

var constraintIDPattern = regexp.MustCompile(`^c-[0-9a-f]{32}$`)

// validClose lists the reasons each outcome allows.
var validClose = map[string]map[string]bool{
	OutcomeAgreed:    {ReasonAccepted: true},
	OutcomeEscalated: {ReasonRejected: true, ReasonTimeout: true},
	OutcomeCancelled: {ReasonCancelled: true, ReasonTimeout: true},
}

// applyClose applies A's debate.close on B. A cancelled close ends B's
// mirror at once. An agreed or escalated close carries A's decision hash and
// signature, which B checks against its own derivation before it signs
// (applyCloseOnB). A close for a debate B already closed (abandoned) or
// broke is stored for the record and changes nothing.
func (s *Store) applyClose(ctx context.Context, tx *sql.Tx, op *mail.Opened) (err error) {
	b := op.Msg.Body
	if err = strictBody(b, "at", "constraints", "decision", "entries", "outcome", "reason", "request", "session", "sig"); err != nil {
		return err
	}
	_, reqID, sid, err := commonBody(b)
	if err != nil {
		return err
	}
	list, ok := b["constraints"].([]any)
	if !ok {
		return badBody("constraints must be an array")
	}
	for _, v := range list {
		if id, ok := v.(string); !ok || !constraintIDPattern.MatchString(id) {
			return badBody("constraints must hold constraint ids")
		}
	}
	entries, err := intMember(b, "entries")
	if err != nil {
		return err
	}
	if entries < 0 || entries > MaxSlot+1 {
		return badBody("entries must be 0-%d", MaxSlot+1)
	}
	outcome, _ := b["outcome"].(string)
	reason, _ := b["reason"].(string)
	if !validClose[outcome][reason] {
		return badBody("outcome and reason do not match")
	}
	for _, k := range []string{"decision", "sig"} {
		if v, ok := b[k]; ok {
			if str, ok := v.(string); !ok || str == "" {
				return badBody("%s must be a non-empty string", k)
			}
		}
	}
	_, hasDecision := b["decision"]
	_, hasSig := b["sig"]
	if hasDecision != hasSig || (outcome == OutcomeCancelled) == hasDecision {
		return badBody("an agreed or escalated close carries decision and sig, a cancelled one neither")
	}
	if d, _ := b["decision"].(string); hasDecision && !hex64.MatchString(d) {
		return badBody("decision must be 64 lowercase hex characters")
	}
	r, found, err := bodyRow(ctx, tx, op, sid, reqID, RoleRespondent)
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
		out.add(s.audit("daemon", "debate.ignored", map[string]any{"session": sid, "peer": op.Msg.From, "kind": MailClose, "reason": "unknown"}))
		return nil
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("debate: encode close: %w", err)
	}
	now := s.now()
	if r.phase == PhaseBroken || r.phase == PhaseClosed {
		if !r.closeBody.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE debates SET close_body = ?, updated = ? WHERE session = ?`, string(raw), storeTime(now), r.session); err != nil {
				return fmt.Errorf("debate: store close: %w", err)
			}
		}
		out.add(s.audit("daemon", "debate.ignored", map[string]any{"session": r.session, "peer": r.peer, "kind": MailClose, "reason": "closed"}))
		return nil
	}
	if !r.open() {
		out.add(s.audit("daemon", "debate.ignored", map[string]any{"session": r.session, "peer": r.peer, "kind": MailClose, "reason": "state"}))
		return nil
	}
	return s.applyCloseOnB(ctx, tx, r, b, &out)
}

// applyCloseOnB applies a checked close to B's open mirror (decision.md
// §Signing step 2). A cancelled close ends the mirror at once. For an agreed
// or escalated close, B first checks the claimed outcome against its own
// transcript (closeMatches, review 45 H1: in front of everything else, so B
// never signs an outcome its transcript contradicts), then A's entries
// count (closeGap): a claim B can reject at once is refused (refuseOnB); a
// close that overtook an A-authored entry or constraint is held in
// close_body until it arrives (review 43 H2; re-checked on every arrival,
// retryHeldClose). Then B derives the Decision itself and checks A's hash
// and signature; on success it signs, stores the Decision signed, closes its
// mirror and sends debate.sign. B's own entries A did not count are late
// (not in the Decision), and so are B's own constraints the close does not
// list.
func (s *Store) applyCloseOnB(ctx context.Context, tx *sql.Tx, r row, b map[string]any, out *afters) error {
	outcome, _ := b["outcome"].(string)
	reason, _ := b["reason"].(string)
	at, _ := b["at"].(string)
	entries, err := intMember(b, "entries")
	if err != nil {
		return err
	}
	listed := listedConstraints(b)
	raw, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("debate: encode close: %w", err)
	}
	now := s.now()
	tr, err := loadTranscript(ctx, tx, r.session)
	if err != nil {
		return err
	}
	if !closeMatches(tr, outcome, reason) {
		// A cannot fabricate the outcome (§Security considerations, "One
		// writer", review 45 H1): a claim B's own transcript contradicts is
		// refused before any derivation, and never signed.
		return s.refuseOnB(ctx, tx, r, tr, b, raw, "outcome", out)
	}
	var canon []byte
	if outcome != OutcomeCancelled {
		// A cancelled close has no Decision to hold for: it ends B's mirror
		// at once (review 46 L2), so a lost A mail cannot keep it open.
		refuse, hold := closeGap(tr, r.roundsMax, entries)
		if refuse != "" {
			return s.refuseOnB(ctx, tx, r, tr, b, raw, refuse, out)
		}
		if !hold {
			missing, err := missingConstraints(ctx, tx, r.session, listed)
			if err != nil {
				return err
			}
			hold = len(missing) > 0
		}
		if hold {
			if _, err := tx.ExecContext(ctx, `UPDATE debates SET close_body = ?, updated = ? WHERE session = ?`, string(raw), storeTime(now), r.session); err != nil {
				return fmt.Errorf("debate: hold close: %w", err)
			}
			return nil
		}
		canon, err = s.deriveTx(ctx, tx, r, tr, entries, listed, outcome, reason, at)
		if errors.Is(err, decision.ErrInconsistent) {
			return s.refuseOnB(ctx, tx, r, tr, b, raw, "outcome", out)
		}
		if errors.Is(err, decision.ErrClosedBeforeOpened) {
			// Review 47 M1: both would sign a record verify step 5 rejects.
			return s.refuseOnB(ctx, tx, r, tr, b, raw, "time", out)
		}
		if err != nil {
			return err
		}
		claimed, _ := b["decision"].(string)
		sigA, _ := b["sig"].(string)
		switch {
		case decision.Hash(canon) != claimed:
			return s.refuseOnB(ctx, tx, r, tr, b, raw, "hash", out)
		case !decision.VerifySignature(r.peer, canon, sigA):
			return s.refuseOnB(ctx, tx, r, tr, b, raw, "signature", out)
		}
		if err := s.signOnB(ctx, tx, r, canon, sigA, now, out); err != nil {
			return err
		}
		for slot, e := range tr {
			if e.author == RoleRespondent && slot >= entries {
				out.add(s.audit("daemon", "debate.ignored", map[string]any{"session": r.session, "peer": r.peer, "kind": MailEntry, "reason": "late"}))
			}
		}
	}
	if err := markLateTx(ctx, tx, r.session, listed); err != nil {
		return err
	}
	if err := setClosed(ctx, tx, r.session, outcome, reason, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE debates SET close_body = ? WHERE session = ?`, string(raw), r.session); err != nil {
		return fmt.Errorf("debate: store close: %w", err)
	}
	out.add(s.audit("daemon", "debate.close_in", map[string]any{
		"session": r.session, "peer": r.peer, "outcome": outcome, "reason": reason, "entries": entries,
	}))
	wsOutcome, note, event := worksession.OutcomeAccepted, "debate agreed", EventAgreed
	switch outcome {
	case OutcomeEscalated:
		note, event = "debate escalated", EventEscalated
	case OutcomeCancelled:
		wsOutcome, note, event = worksession.OutcomeCancelled, "session cancelled", ""
	}
	return s.closeMirrorTx(ctx, tx, r, wsOutcome, note, event, now, out)
}

// closeMatches checks A's close against B's own transcript: agreed needs B's
// answer accepting, escalated/rejected B's answer refusing, and
// escalated/timeout B's position (a timeout after both positions exist). A
// cancelled close is always possible (A's cancel, B's ws.cancel, a timeout
// before B's position reached A).
func closeMatches(tr transcript, outcome, reason string) bool {
	var ans *Answer
	for _, e := range tr {
		if e.author == RoleRespondent && e.state == stateSent {
			if a, ok := e.entry.(*Answer); ok {
				ans = a
			}
		}
	}
	switch {
	case outcome == OutcomeAgreed:
		return ans != nil && ans.Accept
	case outcome == OutcomeEscalated && reason == ReasonRejected:
		return ans != nil && !ans.Accept
	case outcome == OutcomeEscalated:
		e, ok := tr[1]
		return ok && e.state == stateSent
	}
	return true
}

// echo re-sends the initiator's last_state for sid if it was last sent over
// 10 minutes ago (§Submitting entries, "The echo"). Best effort, after
// commit, in its own transaction.
func (s *Store) echo(ctx context.Context, sid string) {
	r, err := getRow(ctx, s.DB, sid)
	if err != nil || r.role != RoleInitiator || !r.lastState.Valid {
		return
	}
	now := s.now()
	if r.lastStateSent.Valid && now.Sub(parseStoreTime(r.lastStateSent.String)) < echoInterval {
		return
	}
	var payload struct {
		Kind string         `json:"kind"`
		Body map[string]any `json:"body"`
	}
	if json.Unmarshal([]byte(r.lastState.String), &payload) != nil {
		return
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := s.Outbox.SubmitTx(ctx, tx, r.peer, payload.Kind, payload.Body); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE debates SET last_state_sent = ? WHERE session = ?`, storeTime(now), sid); err != nil {
		return
	}
	if tx.Commit() == nil {
		s.Outbox.Wake()
	}
}

// strictBody refuses any member outside allowed.
func strictBody(b map[string]any, allowed ...string) error {
	for k := range b {
		found := false
		for _, a := range allowed {
			if k == a {
				found = true
				break
			}
		}
		if !found {
			return badBody("unknown member %q", k)
		}
	}
	return nil
}

// commonBody decodes at, request and session, present in every debate body.
func commonBody(b map[string]any) (at, reqID, sid string, err error) {
	at, ok := b["at"].(string)
	if t, perr := time.Parse(timeFmt, at); !ok || perr != nil || t.Format(timeFmt) != at {
		return "", "", "", badBody("at must be RFC 3339 UTC with Z and whole seconds")
	}
	reqID, ok = b["request"].(string)
	if !ok || !request.ValidID(reqID) {
		return "", "", "", badBody("request must be a request id")
	}
	sid, ok = b["session"].(string)
	if !ok || !worksession.ValidID(sid) {
		return "", "", "", badBody("session must be a session id")
	}
	return at, reqID, sid, nil
}

func intMember(b map[string]any, k string) (int, error) {
	n, ok := b[k].(json.Number)
	if !ok {
		return 0, badBody("%s must be an integer", k)
	}
	i, err := n.Int64()
	if err != nil || i < -1<<31 || i > 1<<31-1 {
		return 0, badBody("%s must be an integer", k)
	}
	return int(i), nil
}
