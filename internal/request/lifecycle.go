package request

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Recipient-side state machine (Docs/protocol/request.md §State machine),
// the sender mirror, and request_show/request_list. Cancel and resend are in
// cancel.go.

const maxDeferAhead = 90 * 24 * time.Hour

var (
	allowedAcceptDeclineDefer = map[string]bool{StatePending: true, StateDeferred: true}
	allowedComplete           = map[string]bool{StateAccepted: true}
)

// queryer is satisfied by both *sql.DB and *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func getRow(ctx context.Context, q queryer, direction, peer, id string) (storedRow, error) {
	row := q.QueryRowContext(ctx, `SELECT `+requestColumns+` FROM requests WHERE direction = ? AND peer = ? AND id = ?`, direction, peer, id)
	r, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return storedRow{}, ErrUnknownRequest
	}
	if err != nil {
		return storedRow{}, fmt.Errorf("request: read row: %w", err)
	}
	return r, nil
}

// findInRow resolves an `in` row by id, optionally narrowed by from
// (Docs/protocol/ipc.md §Requests, "ambiguous_request").
func (s *Store) findInRow(ctx context.Context, q queryer, id, from string) (storedRow, error) {
	if from != "" {
		return getRow(ctx, q, "in", from, id)
	}
	rows, err := q.QueryContext(ctx, `SELECT `+requestColumns+` FROM requests WHERE direction = 'in' AND id = ?`, id)
	if err != nil {
		return storedRow{}, fmt.Errorf("request: read in rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var found []storedRow
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return storedRow{}, fmt.Errorf("request: scan in row: %w", err)
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		return storedRow{}, err
	}
	switch len(found) {
	case 0:
		return storedRow{}, ErrUnknownRequest
	case 1:
		return found[0], nil
	default:
		return storedRow{}, ErrAmbiguousRequest
	}
}

// findOutRow resolves an `out` row by id (out rows are effectively unique by
// id alone: the id is generated locally).
func (s *Store) findOutRow(ctx context.Context, q queryer, id string) (storedRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+requestColumns+` FROM requests WHERE direction = 'out' AND id = ?`, id)
	if err != nil {
		return storedRow{}, fmt.Errorf("request: read out rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return storedRow{}, ErrUnknownRequest
	}
	r, err := scanRow(rows)
	if err != nil {
		return storedRow{}, fmt.Errorf("request: scan out row: %w", err)
	}
	return r, rows.Err()
}

func ageSeconds(row storedRow, now time.Time) int64 {
	if !row.receivedAt.Valid {
		return 0
	}
	d := now.Sub(parseStoreTime(row.receivedAt.String))
	if d < 0 {
		return 0
	}
	return int64(d.Seconds())
}

func (s *Store) auditLifecycle(ctx context.Context, actor, action string, row storedRow, seq int, extra map[string]any) {
	if s.Audit == nil {
		return
	}
	detail := map[string]any{
		"request": row.id, "peer": row.peer, "team": row.teamID, "type": row.typ, "urgency": row.urgency,
		"seq": seq, "age_s": ageSeconds(row, s.now()),
	}
	for k, v := range extra {
		detail[k] = v
	}
	_ = s.Audit.Append(ctx, actor, action, detail)
}

// transitionBuild is returned by a build func: the outbound kind and body,
// an extra "col = ?, ..." SQL fragment (or "") with its args, the
// first_response value to set if not already set ("" for none), and the new
// state.
type transitionBuild struct {
	kind          string
	body          map[string]any
	extraSet      string
	extraArgs     []any
	firstResponse string
	newState      string
}

// transition runs the shared part of accept/decline/defer/complete: load the
// `in` row inside one transaction, check it is in an allowed state, apply
// build's changes, store the lifecycle mail as last_reply and submit it
// (Docs/protocol/request.md §State machine).
func (s *Store) transition(ctx context.Context, id, from string, allowed map[string]bool,
	build func(_ storedRow, now time.Time, seq int) (transitionBuild, error),
) (View, storedRow, int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return View{}, storedRow{}, 0, fmt.Errorf("request: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row, seq, replyMailID, err := s.transitionTx(ctx, tx, id, from, allowed, build)
	if err != nil {
		return View{}, storedRow{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return View{}, storedRow{}, 0, fmt.Errorf("request: commit: %w", err)
	}
	s.Outbox.Wake()

	newRow, err := getRow(ctx, s.DB, "in", row.peer, row.id)
	if err != nil {
		return View{}, storedRow{}, 0, err
	}
	v, err := toView(newRow)
	if err != nil {
		return View{}, storedRow{}, 0, err
	}
	v.ReplyMailID = replyMailID
	return v, row, seq, nil
}

// transitionTx is transition inside the caller's transaction: it neither
// commits nor wakes the outbox, and reads and writes only through tx. It
// returns the row as it was before the change, the new seq and the id of the
// lifecycle mail it submitted.
func (s *Store) transitionTx(ctx context.Context, tx *sql.Tx, id, from string, allowed map[string]bool,
	build func(_ storedRow, now time.Time, seq int) (transitionBuild, error),
) (storedRow, int, string, error) {
	row, err := s.findInRow(ctx, tx, id, from)
	if err != nil {
		return storedRow{}, 0, "", err
	}
	if !allowed[row.state] {
		return storedRow{}, 0, "", &BadStateError{State: row.state, Msg: fmt.Sprintf("%s is %s", id, row.state)}
	}
	now := s.now()
	seq := row.stateSeq + 1
	tb, err := build(row, now, seq)
	if err != nil {
		return storedRow{}, 0, "", err
	}

	lastReply, err := jsonObject(map[string]any{"kind": tb.kind, "body": tb.body})
	if err != nil {
		return storedRow{}, 0, "", fmt.Errorf("request: encode last_reply: %w", err)
	}

	setSQL := `state = ?, state_seq = ?, state_at = ?, last_reply = ?, last_reply_sent = ?, updated = ?`
	args := []any{tb.newState, seq, wireTime(now), lastReply, storeTime(now), storeTime(now)}
	if tb.extraSet != "" {
		setSQL += ", " + tb.extraSet
		args = append(args, tb.extraArgs...)
	}
	if tb.firstResponse != "" && !row.firstResponse.Valid {
		setSQL += `, first_response = ?, first_response_at = ?`
		args = append(args, tb.firstResponse, storeTime(now))
	}
	args = append(args, row.peer, row.id)
	if _, err := tx.ExecContext(ctx, `UPDATE requests SET `+setSQL+` WHERE direction = 'in' AND peer = ? AND id = ?`, args...); err != nil {
		return storedRow{}, 0, "", fmt.Errorf("request: update in row: %w", err)
	}
	sub, err := s.Outbox.SubmitTx(ctx, tx, row.peer, tb.kind, tb.body)
	if err != nil {
		return storedRow{}, 0, "", err
	}
	if tb.newState == StateDeclined && row.typ == TypeDebate && s.Debates != nil {
		if err := s.Debates.EndedTx(ctx, tx, "in", row.peer, row.id, now); err != nil {
			return storedRow{}, 0, "", err
		}
	}
	if tb.kind == KindAccept && s.Sessions != nil {
		if err := s.Sessions.OpenSession(ctx, tx, "worker", row.peer, row.id, row.teamID, now); err != nil {
			return storedRow{}, 0, "", err
		}
	}
	return row, seq, sub.ID, nil
}

// AcceptInTx is Accept inside the caller's transaction, for the one-step
// answer of a question (Docs/protocol/consult.md §Answering): the request
// moves to accepted, request.accept is queued and the worker-role session
// opens, all in tx. onlyType, when set, refuses any other request type with a
// *BadStateError. It returns the peer and the after-commit function that
// audits request.accept (the audit log shares the daemon's one SQLite
// connection, which tx holds); the caller wakes the outbox after commit.
func (s *Store) AcceptInTx(ctx context.Context, tx *sql.Tx, id, from, onlyType string) (peer string, after func(context.Context), err error) {
	return s.acceptInTx(ctx, tx, id, from, onlyType, "cli")
}

// AutoAcceptInTx is AcceptInTx done by the daemon itself (actor "daemon"),
// for an own-device helper's in-scope request, in the receive transaction
// (Docs/protocol/device.md §Running: "auto-accepted (request.accept,
// first_response = accept, actor daemon), and its work session opens").
func (s *Store) AutoAcceptInTx(ctx context.Context, tx *sql.Tx, id, from string) (after func(context.Context), err error) {
	_, after, err = s.acceptInTx(ctx, tx, id, from, "", "daemon")
	return after, err
}

func (s *Store) acceptInTx(ctx context.Context, tx *sql.Tx, id, from, onlyType, actor string) (peer string, after func(context.Context), err error) {
	row, seq, _, err := s.transitionTx(ctx, tx, id, from, allowedAcceptDeclineDefer, func(row storedRow, now time.Time, seq int) (transitionBuild, error) {
		if onlyType != "" && row.typ != onlyType {
			return transitionBuild{}, &BadStateError{State: row.state, Msg: fmt.Sprintf("%s is a %s request and %s: accept it first", id, row.typ, row.state)}
		}
		body := map[string]any{"at": wireTime(now), "request": id, "seq": seq}
		return transitionBuild{kind: KindAccept, body: body, firstResponse: "accept", newState: StateAccepted}, nil
	})
	if err != nil {
		return "", nil, err
	}
	return row.peer, func(ctx context.Context) {
		s.auditLifecycle(ctx, actor, "request.accept", row, seq, nil)
	}, nil
}

// Accept runs request_accept (Docs/protocol/ipc.md §Requests).
func (s *Store) Accept(ctx context.Context, id, from string) (View, error) {
	v, old, seq, err := s.transition(ctx, id, from, allowedAcceptDeclineDefer, func(_ storedRow, now time.Time, seq int) (transitionBuild, error) {
		body := map[string]any{"at": wireTime(now), "request": id, "seq": seq}
		return transitionBuild{kind: KindAccept, body: body, firstResponse: "accept", newState: StateAccepted}, nil
	})
	if err != nil {
		return View{}, err
	}
	s.auditLifecycle(ctx, "cli", "request.accept", old, seq, nil)
	return v, nil
}

// Decline runs request_decline. reason is required, 1-500 code points, no
// control characters (Docs/protocol/request.md §Lifecycle Kinds).
func (s *Store) Decline(ctx context.Context, id, from, reason string) (View, error) {
	if err := checkCodePoints("reason", reason, 1, 500, ""); err != nil {
		return View{}, err
	}
	v, old, seq, err := s.transition(ctx, id, from, allowedAcceptDeclineDefer, func(_ storedRow, now time.Time, seq int) (transitionBuild, error) {
		body := map[string]any{"at": wireTime(now), "code": "user", "reason": reason, "request": id, "seq": seq}
		return transitionBuild{
			kind: KindDecline, body: body, firstResponse: "decline", newState: StateDeclined,
			extraSet: `decline_code = ?, reason = ?`, extraArgs: []any{"user", reason},
		}, nil
	})
	if err != nil {
		return View{}, err
	}
	s.auditLifecycle(ctx, "cli", "request.decline", old, seq, map[string]any{"code": "user"})
	return v, nil
}

// Defer runs request_defer. until must be in the future and at most 90 days
// away (Docs/protocol/request.md §Lifecycle Kinds).
func (s *Store) Defer(ctx context.Context, id, from string, until time.Time) (View, error) {
	v, old, seq, err := s.transition(ctx, id, from, allowedAcceptDeclineDefer, func(_ storedRow, now time.Time, seq int) (transitionBuild, error) {
		if !until.After(now) {
			return transitionBuild{}, fieldErr("until", "must be in the future")
		}
		if until.After(now.Add(maxDeferAhead)) {
			return transitionBuild{}, fieldErr("until", "must be at most 90 days from now")
		}
		body := map[string]any{"at": wireTime(now), "request": id, "seq": seq, "until": wireTime(until)}
		return transitionBuild{
			kind: KindDefer, body: body, firstResponse: "defer", newState: StateDeferred,
			extraSet: `deferred_until = ?`, extraArgs: []any{wireTime(until)},
		}, nil
	})
	if err != nil {
		return View{}, err
	}
	s.auditLifecycle(ctx, "cli", "request.defer", old, seq, map[string]any{"until": wireTime(until)})
	return v, nil
}

// Complete runs request_complete. result, if given, is validated by
// ValidateComplete before the transaction, and the whole body by
// CheckCompleteSize inside it (Docs/protocol/request.md §Result payload (D14)).
func (s *Store) Complete(ctx context.Context, id, from, note string, result *Result) (View, error) {
	if err := ValidateComplete(note, result); err != nil {
		return View{}, err
	}
	if s.Sessions != nil {
		row, err := s.findInRow(ctx, s.DB, id, from)
		if err != nil {
			return View{}, err
		}
		if ok, serr := s.Sessions.CompleteShorthand(ctx, row.peer, row.id, note, result); ok {
			if serr != nil {
				return View{}, serr
			}
			return toView(row)
		}
	}
	var resultBytes, outputBytes int
	var resultCanon []byte
	if result != nil {
		var err error
		resultCanon, err = CanonicalResult(result)
		if err != nil {
			return View{}, err
		}
		resultBytes = ResultBytes(resultCanon)
		outputBytes = OutputBytes(result.Output)
	}
	v, old, seq, err := s.transition(ctx, id, from, allowedComplete, func(_ storedRow, now time.Time, seq int) (transitionBuild, error) {
		// The total cap is checked on the body actually sent: its seq (which
		// grows with every defer) and at are only known here.
		canon, err := completeBodyCanonical(id, seq, now, note, result)
		if err != nil {
			return transitionBuild{}, err
		}
		if err := CheckCompleteSize(canon); err != nil {
			return transitionBuild{}, err
		}
		body := map[string]any{"at": wireTime(now), "request": id, "seq": seq}
		if note != "" {
			body["note"] = note
		}
		if result != nil {
			body["result"] = resultWire(result)
		}
		extraSet := `note = ?, result = ?`
		var noteArg, resultArg any
		if note != "" {
			noteArg = note
		}
		if result != nil {
			resultArg = string(resultCanon)
		}
		return transitionBuild{
			kind: KindComplete, body: body, newState: StateCompleted,
			extraSet: extraSet, extraArgs: []any{noteArg, resultArg},
		}, nil
	})
	if err != nil {
		return View{}, err
	}
	extra := map[string]any{}
	if result != nil {
		extra["result_bytes"] = resultBytes
		extra["output_bytes"] = outputBytes
		extra["artifacts"] = len(result.Artifacts)
	}
	s.auditLifecycle(ctx, "cli", "request.complete", old, seq, extra)
	return v, nil
}

// completeBodyCanonical returns canonical(complete body): the body of
// request.complete, for the total-size cap (Docs/protocol/request.md
// §Result payload (D14)), built with the seq and at that will be sent.
func completeBodyCanonical(id string, seq int, at time.Time, note string, result *Result) ([]byte, error) {
	body := map[string]any{"at": wireTime(at), "request": id, "seq": jsonInt(seq)}
	if note != "" {
		body["note"] = note
	}
	if result != nil {
		body["result"] = resultCanonicalValue(result)
	}
	return canonicalMap(body)
}
