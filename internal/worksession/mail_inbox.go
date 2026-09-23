package worksession

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// blankResultMailTx blanks the signed plaintext of every mail_inbox row that
// carries the ws.result mail for (peer, sid, round), inside tx (Docs/review/
// 27-2.1a-review.md H1; D18). Discard and RequestChanges-from-quarantined
// delete the stored result unseen; without this, the verified plaintext the
// result was decoded from would still sit in mail_inbox indefinitely. The row
// itself (from_key, id, kind, created, received_at) is kept, only its signed
// column is cleared: mail_inbox is not consulted by dedupe (mail_seen is), so
// this never causes a resend or a re-apply.
//
// ws.result cannot decide to withhold its own mail_inbox row at receive time
// (unlike request.complete, review 27 H1 option (a)): whether a result will
// later be discarded or superseded without release is unknown until a human
// acts, in a separate transaction. So the row is matched afterwards, by
// decoding each candidate's stored plaintext and comparing its session and
// round to the one being dropped.
func blankResultMailTx(ctx context.Context, tx *sql.Tx, peer, sid string, round int) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, signed FROM mail_inbox WHERE from_key = ? AND kind = ? AND signed != ''`, peer, KindResult)
	if err != nil {
		return fmt.Errorf("worksession: scan mail_inbox: %w", err)
	}
	defer func() { _ = rows.Close() }()
	type match struct{ id string }
	var matches []match
	for rows.Next() {
		var id, signed string
		if err := rows.Scan(&id, &signed); err != nil {
			return fmt.Errorf("worksession: scan mail_inbox: %w", err)
		}
		s, r, ok := resultMailSessionRound(signed)
		if ok && s == sid && r == round {
			matches = append(matches, match{id: id})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("worksession: scan mail_inbox: %w", err)
	}
	for _, m := range matches {
		if _, err := tx.ExecContext(ctx,
			`UPDATE mail_inbox SET signed = '' WHERE from_key = ? AND id = ?`, peer, m.id); err != nil {
			return fmt.Errorf("worksession: blank mail_inbox: %w", err)
		}
	}
	return nil
}

// resultMailSessionRound reads the session and round members of a stored
// ws.result mail's plaintext (the {"msg":{"body":{...}}} shape mail_inbox.signed
// holds), without re-verifying it: it is already the daemon's own verified
// copy. ok is false for a plaintext that does not decode to that shape (never
// expected for a row of kind ws.result, handled defensively).
func resultMailSessionRound(signed string) (sid string, round int, ok bool) {
	var wrap struct {
		Msg struct {
			Body struct {
				Session string      `json:"session"`
				Round   json.Number `json:"round"`
			} `json:"body"`
		} `json:"msg"`
	}
	if err := json.Unmarshal([]byte(signed), &wrap); err != nil {
		return "", 0, false
	}
	if wrap.Msg.Body.Session == "" {
		return "", 0, false
	}
	r, err := wrap.Msg.Body.Round.Int64()
	if err != nil {
		return "", 0, false
	}
	return wrap.Msg.Body.Session, int(r), true
}
