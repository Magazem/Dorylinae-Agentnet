package request

import (
	"database/sql"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// States (Docs/protocol/request.md §State machine).
const (
	StatePending   = "pending"
	StateAccepted  = "accepted"
	StateDeclined  = "declined"
	StateDeferred  = "deferred"
	StateCompleted = "completed"
	StateCancelled = "cancelled"
)

// Lifecycle mail kinds (Docs/protocol/request.md §Lifecycle).
const (
	KindAccept    = "request.accept"
	KindDecline   = "request.decline"
	KindDefer     = "request.defer"
	KindComplete  = "request.complete"
	KindCancelled = "request.cancelled"
	KindCancel    = "request.cancel"
)

// requestColumns is the column list shared by every SELECT against requests,
// in scanRow's order.
const requestColumns = `direction, peer, id, team_id, type, urgency, urgency_declared, downgraded_by,
	body, body_hash, state, state_seq, state_at, deferred_until, decline_code, reason, note,
	first_response, first_response_at, created, received_at, mail_id, last_reply, last_reply_sent,
	idem_key, params_hash, cancel, cancel_at, cancel_mail_id, result, updated`

// storedRow is one requests row, scanned generically for both in and out
// rows (Docs/protocol/request.md §Tables).
type storedRow struct {
	direction, peer, id, teamID, typ, urgency, urgencyDeclared string
	downgradedBy                                               sql.NullString
	body, bodyHash                                             string
	state                                                      string
	stateSeq                                                   int
	stateAt                                                    sql.NullString
	deferredUntil                                              sql.NullString
	declineCode                                                sql.NullString
	reason                                                     sql.NullString
	note                                                       sql.NullString
	firstResponse                                              sql.NullString
	firstResponseAt                                            sql.NullString
	created                                                    string
	receivedAt                                                 sql.NullString
	mailID                                                     string
	lastReply                                                  sql.NullString
	lastReplySent                                              sql.NullString
	idemKey                                                    sql.NullString
	paramsHash                                                 sql.NullString
	cancel                                                     sql.NullString
	cancelAt                                                   sql.NullString
	cancelMailID                                               sql.NullString
	result                                                     sql.NullString
	updated                                                    string
}

type scanner interface{ Scan(dest ...any) error }

func scanRow(sc scanner) (storedRow, error) {
	var r storedRow
	err := sc.Scan(&r.direction, &r.peer, &r.id, &r.teamID, &r.typ, &r.urgency, &r.urgencyDeclared,
		&r.downgradedBy, &r.body, &r.bodyHash, &r.state, &r.stateSeq, &r.stateAt, &r.deferredUntil,
		&r.declineCode, &r.reason, &r.note, &r.firstResponse, &r.firstResponseAt, &r.created,
		&r.receivedAt, &r.mailID, &r.lastReply, &r.lastReplySent, &r.idemKey, &r.paramsHash,
		&r.cancel, &r.cancelAt, &r.cancelMailID, &r.result, &r.updated)
	return r, err
}

func parseWireTime(s string) time.Time {
	t, _ := time.Parse(timeFmt, s)
	return t
}

func parseStoreTime(s string) time.Time {
	t, _ := time.Parse(mail.StoreTimeFmt, s)
	return t
}

func nullWireTime(s sql.NullString) time.Time {
	if !s.Valid || s.String == "" {
		return time.Time{}
	}
	return parseWireTime(s.String)
}

// View is a fully decoded request row: the stored request object plus the
// lifecycle state, for IPC's "request view" (Docs/protocol/ipc.md §Requests).
type View struct {
	ID              string
	Direction       string // "in" or "out"
	Peer            string // the other party's identity key
	TeamID          string
	Type            string
	Title           string
	Brief           string
	Urgency         string
	UrgencyDeclared string
	DowngradedBy    string // "", "sender" or "receiver"
	UrgencyReason   string
	Artifacts       []Artifact
	RequestedGrant  *RequestedGrant
	Context         []ContextFile
	Run             *Run
	Deadline        time.Time
	Created         time.Time
	ReceivedAt      time.Time // in only

	State         string
	StateAt       time.Time
	DeferredUntil time.Time
	DeclineCode   string
	Reason        string
	Note          string

	Cancel string // "", "requested" or "refused" (out only)

	Result      *Result
	ResultBytes int
	OutputBytes int

	// ReplyMailID is set only by accept/decline/defer/complete: the id of
	// the lifecycle mail just sent (IPC result "mail_id").
	ReplyMailID string

	MailID   string
	Delivery string // out only: the outbox state of MailID, or "unknown"

	// Priority, Due and UrgencyNote are set only by InboxList (Docs/protocol/request.md
	// §Inbox (1.6)); zero/false/"" elsewhere.
	Priority    int
	Due         bool
	UrgencyNote string
}

// toView decodes a storedRow into a View. It does not read the outbox
// (Delivery is left "").
func toView(r storedRow) (View, error) {
	req, err := decodeStoredBody(r.body)
	if err != nil {
		return View{}, err
	}
	v := View{
		ID: r.id, Direction: r.direction, Peer: r.peer, TeamID: r.teamID, Type: r.typ,
		Title: req.Title, Brief: req.Brief, Urgency: r.urgency, UrgencyDeclared: r.urgencyDeclared,
		UrgencyReason: req.UrgencyReason, Artifacts: req.Artifacts, RequestedGrant: req.RequestedGrant, Context: req.Context, Run: req.Run,
		Deadline: req.Deadline, Created: parseWireTime(r.created), MailID: r.mailID,
		State: r.state, StateAt: nullWireTime(r.stateAt), DeferredUntil: nullWireTime(r.deferredUntil),
	}
	if r.downgradedBy.Valid {
		v.DowngradedBy = r.downgradedBy.String
	}
	if r.receivedAt.Valid {
		v.ReceivedAt = parseStoreTime(r.receivedAt.String)
	}
	if r.declineCode.Valid {
		v.DeclineCode = r.declineCode.String
	}
	if r.reason.Valid {
		v.Reason = r.reason.String
	}
	if r.note.Valid {
		v.Note = r.note.String
	}
	if r.cancel.Valid {
		v.Cancel = r.cancel.String
	}
	if r.result.Valid && r.result.String != "" {
		res, err := decodeStoredResult(r.result.String)
		if err != nil {
			return View{}, err
		}
		v.Result = res
		v.ResultBytes = len(r.result.String)
		v.OutputBytes = OutputBytes(res.Output)
	}
	return v, nil
}

func decodeStoredResult(body string) (*Result, error) {
	v, err := agentcard.ParseStrict([]byte(body))
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, badBody("stored result is not an object")
	}
	return DecodeResult(obj)
}
