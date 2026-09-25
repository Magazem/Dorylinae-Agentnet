package debate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/experience"
)

// requestSummary reads this side's own request row for r, for the
// experience record's problem (title and topic/brief,
// Docs/protocol/experience.md "for a debate: title and topic").
func (s *Store) requestSummary(ctx context.Context, tx *sql.Tx, r row) (typ, title, brief string, err error) {
	dir := "in"
	if r.role == RoleInitiator {
		dir = "out"
	}
	var body string
	if err := tx.QueryRowContext(ctx, `SELECT type, body FROM requests WHERE direction = ? AND peer = ? AND id = ?`, dir, r.peer, r.requestID).
		Scan(&typ, &body); err != nil {
		return "", "", "", fmt.Errorf("debate: read request for the experience record: %w", err)
	}
	var b struct {
		Title string `json:"title"`
		Brief string `json:"brief"`
	}
	_ = json.Unmarshal([]byte(body), &b)
	return typ, b.Title, b.Brief, nil
}

// claimOf returns the claim of the position at slot (0 = initiator, 1 =
// respondent), "" if it was never applied.
func claimOf(tr transcript, slot int) string {
	e, ok := tr[slot]
	if !ok || (e.state != stateApplied && e.state != stateSent) {
		return ""
	}
	p, ok := e.entry.(*Position)
	if !ok {
		return ""
	}
	return p.Claim
}

// proposalDecision returns A's proposed Agreement.Decision text, "" if no
// proposal was ever applied.
func proposalDecision(tr transcript) string {
	for _, e := range tr {
		if p, ok := e.entry.(*Proposal); ok {
			return p.Agreement.Decision
		}
	}
	return ""
}

// remainingDisagreement returns B's answer's remaining-disagreement count,
// ok false if no answer was ever applied.
func remainingDisagreement(tr transcript) (n int, ok bool) {
	for _, e := range tr {
		if a, isAnswer := e.entry.(*Answer); isAnswer {
			return len(a.RemainingDisagreement), true
		}
	}
	return 0, false
}

// writeExperienceTx builds and stores r's experience record inside tx, in the
// same transaction that closes the debate session on either side
// (Docs/protocol/experience.md §When and where): closeTx on the initiator,
// closeMirrorTx on the respondent. cancelledBy applies only when outcome is
// cancelled; dec is the stored Decision (nil for a cancelled debate, which
// has none).
func (s *Store) writeExperienceTx(ctx context.Context, tx *sql.Tx, r row, tr transcript, outcome, cancelledBy string, dec *experience.Decision, now time.Time) (bytes int, truncated bool, err error) {
	typ, title, brief, err := s.requestSummary(ctx, tx, r)
	if err != nil {
		return 0, false, err
	}
	created := parseStoreTime(r.created)
	age := int(now.Sub(created) / time.Second)
	if age < 0 {
		age = 0
	}
	in := experience.Input{
		Session: r.session, Request: r.requestID, Role: r.role, Kind: experience.KindDebate,
		Peer: r.peer, Type: typ,
		ProblemTitle: title, ProblemBrief: brief,
		InitiatorClaim: claimOf(tr, 0), RespondentClaim: claimOf(tr, 1),
		RoundsUsed: RoundsUsed(tr.metas(stateApplied, stateSent)),
		Outcome:    outcome, AgeS: age, Opened: created, Closed: now,
		Decision: dec,
	}
	switch outcome {
	case OutcomeAgreed:
		in.WorkedDecision = proposalDecision(tr)
		if in.WorkedDecision == "" {
			in.WorkedDecision = outcome
		}
	case OutcomeEscalated:
		if n, ok := remainingDisagreement(tr); ok {
			in.HasRemainingDisagreement, in.RemainingDisagreementPoints = true, n
		}
	case OutcomeCancelled:
		in.CancelledBy = cancelledBy
	}
	return experience.WriteTx(ctx, tx, in, now)
}

// auditExperience is the after-commit experience.write audit row
// (Docs/protocol/experience.md §Audit): ids and sizes, never content.
func (s *Store) auditExperience(sid, role string, bytes int, truncated bool) func(context.Context) {
	detail := map[string]any{"session": sid, "role": role, "bytes": bytes}
	if truncated {
		detail["truncated"] = true
	}
	return s.audit("daemon", "experience.write", detail)
}
