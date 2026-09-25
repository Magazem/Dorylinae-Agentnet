package worksession

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/experience"
)

// requestSummary reads this daemon's own request row for a work session, for
// the experience record's problem (Docs/protocol/experience.md "problem"):
// type, title and brief. ok is false only if no row exists (should not
// happen: OpenSession always creates one first).
func requestSummary(ctx context.Context, tx *sql.Tx, role, peer, requestID string) (typ, title, brief string, err error) {
	typ, ok, err := requestType(ctx, tx, role, peer, requestID)
	if err != nil || !ok {
		return "", "", "", err
	}
	dir := "out"
	if role == RoleWorker {
		dir = "in"
	}
	var body string
	if err := tx.QueryRowContext(ctx, `SELECT body FROM requests WHERE direction = ? AND peer = ? AND id = ?`, dir, peer, requestID).Scan(&body); err != nil {
		return typ, "", "", fmt.Errorf("worksession: read request body: %w", err)
	}
	var b struct {
		Title string `json:"title"`
		Brief string `json:"brief"`
	}
	_ = json.Unmarshal([]byte(body), &b)
	return typ, b.Title, b.Brief, nil
}

// sessionGrants reads sid's grants for the experience record's approach
// (Docs/protocol/experience.md "Never in the record": action, sensitivity
// and final state only, never a path, label or scope).
func sessionGrants(ctx context.Context, tx *sql.Tx, sid string) ([]experience.Grant, error) {
	rows, err := tx.QueryContext(ctx, `SELECT action, sensitive, state FROM grants WHERE session = ? ORDER BY created`, sid)
	if err != nil {
		return nil, fmt.Errorf("worksession: read grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []experience.Grant
	for rows.Next() {
		var g experience.Grant
		var sensitive int
		if err := rows.Scan(&g.Action, &sensitive, &g.State); err != nil {
			return nil, fmt.Errorf("worksession: scan grant: %w", err)
		}
		g.Sensitive = sensitive != 0
		out = append(out, g)
	}
	return out, rows.Err()
}

// verificationActor names who set a work session's verification value
// (Docs/protocol/experience.md "verification_by"): the human on the
// requester's side accepted it manually, or the worker claimed it.
func verificationActor(verification string) string {
	switch verification {
	case VerificationHumanAccepted:
		return RoleRequester
	case VerificationTestsPassed:
		return RoleWorker
	default:
		return ""
	}
}

// writeExperienceTx builds and stores row's experience record inside tx, in
// the same transaction that closes the session
// (Docs/protocol/experience.md §When and where). It reads row.result only for
// outcome accepted, so a quarantined-then-discarded, superseded or dropped
// result never reaches it: those closes always pass OutcomeCancelled.
// cancelledBy is a role or "timeout", used only when outcome is cancelled.
func (s *Store) writeExperienceTx(ctx context.Context, tx *sql.Tx, row storedRow, outcome, verification, verificationBy, cancelledBy string, now time.Time) (bytes int, truncated bool, err error) {
	typ, title, brief, err := requestSummary(ctx, tx, row.role, row.peer, row.requestID)
	if err != nil {
		return 0, false, err
	}
	grants, err := sessionGrants(ctx, tx, row.id)
	if err != nil {
		return 0, false, err
	}
	opened := parseWireTime(row.opened)
	age := 0
	if !opened.IsZero() {
		age = int(now.Sub(opened) / time.Second)
	}
	in := experience.Input{
		Session: row.id, Request: row.requestID, Role: row.role, Kind: SessionKindWork,
		Peer: row.peer, Team: row.teamID, Type: typ,
		ProblemTitle: title, ProblemBrief: brief,
		Grants: grants, Rounds: row.round,
		Verification: verification, VerificationBy: verificationBy,
		Outcome: outcome, AgeS: age, Opened: opened, Closed: now,
	}
	switch outcome {
	case OutcomeAccepted:
		if row.round > 1 && row.changes.Valid && row.changes.String != "" {
			in.Changes, in.LastChanges = row.changes.String, row.changes.String
			in.RoundsRejected = row.round - 1
		}
		if row.result.Valid && row.result.String != "" && row.resultRound.Valid && int(row.resultRound.Int64) == row.round {
			if stored, derr := decodeStoredResult(row.result.String); derr == nil {
				in.WorkedStatus, in.WorkedSummary = stored.Status, stored.Summary
			}
		}
	case OutcomeCancelled:
		in.CancelledBy = cancelledBy
		if row.round > 1 && row.changes.Valid && row.changes.String != "" {
			in.Changes = row.changes.String
		}
	}
	return experience.WriteTx(ctx, tx, in, now)
}

// auditExperience appends experience.write {session, role, bytes,
// truncated?} (Docs/protocol/experience.md §Audit), after commit.
func (s *Store) auditExperience(ctx context.Context, sid, role string, bytes int, truncated bool) {
	if s.Audit == nil {
		return
	}
	detail := map[string]any{"session": sid, "role": role, "bytes": bytes}
	if truncated {
		detail["truncated"] = true
	}
	_ = s.Audit.Append(ctx, "daemon", "experience.write", detail)
}
