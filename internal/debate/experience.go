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
// experience record's team and problem (title and topic/brief,
// Docs/protocol/experience.md "for a debate: title and topic").
func (s *Store) requestSummary(ctx context.Context, tx *sql.Tx, r row) (typ, teamID, title, brief string, err error) {
	dir := "in"
	if r.role == RoleInitiator {
		dir = "out"
	}
	var body string
	if err := tx.QueryRowContext(ctx, `SELECT type, team_id, body FROM requests WHERE direction = ? AND peer = ? AND id = ?`, dir, r.peer, r.requestID).
		Scan(&typ, &teamID, &body); err != nil {
		return "", "", "", "", fmt.Errorf("debate: read request for the experience record: %w", err)
	}
	var b struct {
		Title string `json:"title"`
		Brief string `json:"brief"`
	}
	_ = json.Unmarshal([]byte(body), &b)
	return typ, teamID, b.Title, b.Brief, nil
}

// coverFunc selects the transcript entries an experience record covers
// (Docs/protocol/experience.md §Covered debate entries, R55-170).
type coverFunc func(slot int, e stored) bool

// coverApplied is A's cover: the entries A's close counts.
func coverApplied(_ int, e stored) bool { return e.state == stateApplied }

// coverAppliedOrSent is B's cover when it applied no close (abandon, bad
// reveal).
func coverAppliedOrSent(_ int, e stored) bool {
	return e.state == stateApplied || e.state == stateSent
}

// coverBelow is B's cover when it applies A's close (signed or refused):
// applied or sent entries at slots below the close's entries. A B entry A
// cut is late and not covered.
func coverBelow(entries int) coverFunc {
	return func(slot int, e stored) bool { return slot < entries && coverAppliedOrSent(slot, e) }
}

// covered is the part of tr that cover selects.
func (tr transcript) covered(cover coverFunc) transcript {
	out := transcript{}
	for slot, e := range tr {
		if cover(slot, e) {
			out[slot] = e
		}
	}
	return out
}

// claimOf returns the claim of the position at slot (0 = initiator, 1 =
// respondent) in a covered transcript, "" if it is not covered.
func claimOf(tr transcript, slot int) string {
	e, ok := tr[slot]
	if !ok {
		return ""
	}
	p, ok := e.entry.(*Position)
	if !ok {
		return ""
	}
	return p.Claim
}

// decisionFacts reads final_agreement.decision and the number of
// remaining_disagreement items (0 when absent) from a stored canonical
// Decision, so the record matches the Decision by construction (R55-170,
// OD-F29-6).
func decisionFacts(canon []byte) (agreed string, remaining int) {
	if len(canon) == 0 {
		return "", 0
	}
	v, err := entryValue(canon)
	if err != nil {
		return "", 0
	}
	d, _ := v.(map[string]any)
	if fa, ok := d["final_agreement"].(map[string]any); ok {
		agreed, _ = fa["decision"].(string)
	}
	if list, ok := d["remaining_disagreement"].([]any); ok {
		remaining = len(list)
	}
	return agreed, remaining
}

// writeExperienceTx builds and stores r's experience record inside tx, in the
// same transaction that closes the debate session on either side
// (Docs/protocol/experience.md §When and where): closeTx on the initiator,
// closeMirrorTx on the respondent. cover selects the entries the record
// covers. cancelledBy and cause apply only when outcome is cancelled. dec is
// the stored Decision this side refers to (nil when it has none) and
// decCanon its canonical bytes, the source of worked and of the disagreement
// count.
func (s *Store) writeExperienceTx(ctx context.Context, tx *sql.Tx, r row, tr transcript, cover coverFunc, outcome, cancelledBy, cause string, dec *experience.Decision, decCanon []byte, now time.Time) (bytes int, truncated bool, err error) {
	typ, teamID, title, brief, err := s.requestSummary(ctx, tx, r)
	if err != nil {
		return 0, false, err
	}
	cov := tr.covered(cover)
	created := parseStoreTime(r.created)
	age := int(now.Sub(created) / time.Second)
	if age < 0 {
		age = 0
	}
	in := experience.Input{
		Session: r.session, Request: r.requestID, Role: r.role, Kind: experience.KindDebate,
		Peer: r.peer, Team: teamID, Type: typ,
		ProblemTitle: title, ProblemBrief: brief,
		InitiatorClaim: claimOf(cov, 0), RespondentClaim: claimOf(cov, 1),
		RoundsUsed:   RoundsUsed(cov.metas(stateApplied, stateSent)),
		Verification: experience.VerificationNone,
		Outcome:      outcome, AgeS: age, Opened: created, Closed: now,
		Decision: dec,
	}
	agreed, remaining := decisionFacts(decCanon)
	switch outcome {
	case OutcomeAgreed:
		in.WorkedDecision = agreed
	case OutcomeEscalated:
		in.RemainingDisagreementPoints = remaining
	case OutcomeCancelled:
		in.CancelledBy, in.Cause = cancelledBy, cause
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
