// Package experience builds and stores the private experience record
// (Docs/protocol/experience.md): a local, daemon-assembled snapshot of one
// closed session, written in the same transaction that closes it. Nothing in
// Phase 3 reads it back: no IPC method, no CLI command, no mail kind. Change
// the spec first.
package experience

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// Kinds (Docs/protocol/experience.md §Record, "kind").
const (
	KindWork   = "work"
	KindDebate = "debate"
)

// MaxRecordBytes caps len(canonical(record)) (Docs/protocol/experience.md
// §Record).
const MaxRecordBytes = 65536

const recordVersion = 1

// Outcome values this package matches on directly (never imported from
// internal/worksession or internal/debate, which would cycle back here).
const (
	outcomeAccepted = "accepted"
	outcomeAgreed   = "agreed"
)

// Grant is one approach.grants item: action, sensitivity and final state
// only, never a path, label or scope (Docs/protocol/experience.md "Never in
// the record").
type Grant struct {
	Action    string
	Sensitive bool
	State     string
}

// Decision refers to a debate's stored Decision (the decisions table),
// present only when one was derived (Docs/protocol/experience.md
// "acceptance").
type Decision struct {
	ID, Hash string
}

// Input holds every field the builder may draw on
// (Docs/protocol/experience.md §Record). Optional members are left zero when
// they do not apply to this session's kind or outcome; Build omits them.
// Callers pass only data this side already holds for the session: never file
// contents, paths, grant labels/scopes, helper commands, argv, output,
// context file texts, approval codes or tokens.
type Input struct {
	Session, Request, Role, Kind, Peer, Team, Type string

	ProblemTitle, ProblemBrief string

	// Work approach.
	Grants  []Grant
	Rounds  int
	Changes string // the last changes text, present only when Rounds > 1

	// Debate approach.
	InitiatorClaim, RespondentClaim string
	RoundsUsed                      int

	// What worked: the accepted result's status/summary (work), or the
	// debate's outcome (debate, agreed only).
	WorkedStatus, WorkedSummary string
	WorkedDecision              string

	// What failed.
	RoundsRejected              int    // work, accepted: rounds - 1 (0 means absent)
	LastChanges                 string // work: the same text as approach.changes
	HasRemainingDisagreement    bool   // debate, escalated
	RemainingDisagreementPoints int
	CancelledBy                 string // any cancelled close: a role value, or "timeout"

	Verification, VerificationBy string

	Outcome  string
	AgeS     int
	Decision *Decision

	Opened, Closed time.Time
}

const wireFmt = "2006-01-02T15:04:05Z"

func wireTime(t time.Time) string { return t.UTC().Truncate(time.Second).Format(wireFmt) }

func num(n int) json.Number { return json.Number(strconv.Itoa(n)) }

// value returns the record's canonical-form value at drop level 0-3
// (Docs/protocol/experience.md §Record, the truncation order:
// approach.changes, failed.last_changes, problem.brief).
func value(in Input, level int) map[string]any {
	m := map[string]any{
		"v": num(recordVersion), "session": in.Session, "request": in.Request,
		"role": in.Role, "kind": in.Kind, "peer": in.Peer, "type": in.Type,
	}
	if in.Team != "" {
		m["team"] = in.Team
	}

	problem := map[string]any{}
	if in.ProblemTitle != "" {
		problem["title"] = in.ProblemTitle
	}
	if in.ProblemBrief != "" && level < 3 {
		problem["brief"] = in.ProblemBrief
	}
	m["problem"] = problem

	approach := map[string]any{}
	if in.Kind == KindDebate {
		positions := map[string]any{}
		if in.InitiatorClaim != "" {
			positions["initiator"] = in.InitiatorClaim
		}
		if in.RespondentClaim != "" {
			positions["respondent"] = in.RespondentClaim
		}
		if len(positions) > 0 {
			approach["positions"] = positions
		}
		approach["rounds_used"] = num(in.RoundsUsed)
	} else {
		if len(in.Grants) > 0 {
			gs := make([]any, 0, len(in.Grants))
			for _, g := range in.Grants {
				gs = append(gs, map[string]any{"action": g.Action, "sensitive": g.Sensitive, "state": g.State})
			}
			approach["grants"] = gs
		}
		approach["rounds"] = num(in.Rounds)
		if in.Changes != "" && level < 1 {
			approach["changes"] = in.Changes
		}
	}
	m["approach"] = approach

	// "worked" only ever reflects a clean success: an accepted work session
	// or an agreed debate. A caller must never set WorkedStatus/WorkedDecision
	// for any other outcome, but the builder also refuses to include it then,
	// so a mistaken caller cannot resurrect a discarded or replaced result
	// (Docs/protocol/experience.md "Never in the record").
	switch {
	case in.Kind == KindWork && in.Outcome == outcomeAccepted && in.WorkedStatus != "":
		w := map[string]any{"status": in.WorkedStatus, "verification": in.Verification}
		if in.WorkedSummary != "" {
			w["summary"] = in.WorkedSummary
		}
		m["worked"] = w
	case in.Kind == KindDebate && in.Outcome == outcomeAgreed && in.WorkedDecision != "":
		m["worked"] = map[string]any{"decision": in.WorkedDecision}
	}

	failed := map[string]any{}
	switch {
	case in.CancelledBy != "":
		failed["cancelled_by"] = in.CancelledBy
	case in.Kind == KindDebate && in.HasRemainingDisagreement:
		failed["remaining_disagreement_points"] = num(in.RemainingDisagreementPoints)
	case in.Kind == KindWork && in.RoundsRejected > 0:
		failed["rounds_rejected"] = num(in.RoundsRejected)
		if in.LastChanges != "" && level < 2 {
			failed["last_changes"] = in.LastChanges
		}
	}
	if len(failed) > 0 {
		m["failed"] = failed
	}

	m["verification"] = in.Verification
	if in.VerificationBy != "" {
		m["verification_by"] = in.VerificationBy
	}

	acceptance := map[string]any{"outcome": in.Outcome, "age_s": num(in.AgeS)}
	if in.Decision != nil {
		acceptance["decision"] = map[string]any{"id": in.Decision.ID, "hash": in.Decision.Hash}
	}
	m["acceptance"] = acceptance

	m["opened"] = wireTime(in.Opened)
	m["closed"] = wireTime(in.Closed)

	if level > 0 {
		m["truncated"] = true
	}
	return m
}

// Build returns canonical(record) (Docs/protocol/experience.md §Record),
// capped at MaxRecordBytes: members are dropped in the order
// approach.changes, failed.last_changes, problem.brief until it fits, adding
// "truncated": true once any member was dropped.
func Build(in Input) (canon []byte, truncated bool, err error) {
	for level := 0; level <= 3; level++ {
		c, err := agentcard.CanonicalValue(value(in, level))
		if err != nil {
			return nil, false, fmt.Errorf("experience: canonical form: %w", err)
		}
		if len(c) <= MaxRecordBytes || level == 3 {
			return c, level > 0, nil
		}
	}
	panic("unreachable")
}

// WriteTx builds and inserts the (session, role) experience record inside
// tx, in the same transaction that closes the session
// (Docs/protocol/experience.md §When and where): a crash never leaves a
// closed session without its record, and a rolled-back close leaves no
// record. bytes and truncated are for the caller's experience.write audit
// row (never the record's content).
func WriteTx(ctx context.Context, tx *sql.Tx, in Input, now time.Time) (bytes int, truncated bool, err error) {
	canon, truncated, err := Build(in)
	if err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO experience_records (session, role, record, created) VALUES (?, ?, ?, ?)`,
		in.Session, in.Role, string(canon), now.UTC().Format(mail.StoreTimeFmt)); err != nil {
		return 0, false, fmt.Errorf("experience: store record: %w", err)
	}
	return len(canon), truncated, nil
}
