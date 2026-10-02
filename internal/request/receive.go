package request

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// Receiving side of Docs/protocol/request.md §Receiving: the wire idempotency
// check, the policy auto-decline (D5, unknown_team, not_team_member; the
// receiver-side urgency budget of step 4 is ticket 1.7) and storing the `in`
// row. Kind.Apply runs inside the mail dedupe transaction (Docs/protocol/mail.md);
// After runs once, after commit, to audit. Re-sending last_reply for a
// duplicate (step 2) is ticket 1.6a.

const maxAge = 30 * 24 * time.Hour

// emptyBody is the body stored for an auto-declined `in` row, which keeps no
// content (Docs/protocol/request.md §Receiving step 3).
const emptyBody = "{}"

// SubmitTx is the outbox capability the receiving path needs: sending the
// auto-decline reply inside the same transaction as the declined row
// (Docs/review/12-phase1-spec-review.md, the auto-decline atomicity rule).
type SubmitTx interface {
	SubmitTx(ctx context.Context, tx *sql.Tx, to, kind string, body any) (mail.Submitted, error)
	Wake()
}

// AuditSink is the part of audit.Log the Store needs.
type AuditSink interface {
	Append(ctx context.Context, actor, action string, detail any) error
}

// Store owns the receiving (and, in submit.go, the sending) path of the
// requests table (migration 11).
type Store struct {
	DB     *sql.DB
	Self   string // own identity key, wire form
	Outbox SubmitTx
	Audit  AuditSink // request.in, request.auto_decline; may be nil
	// Notify, when set, is called after commit for each lifecycle event of
	// Docs/protocol/notify.md §Triggers.
	Notify NotifyFunc

	// TeamActive reports whether teamID is a local team in state "active". It
	// is read through tx (not the connection pool): apply runs inside the mail
	// dedupe transaction, which already holds the daemon's one SQLite
	// connection (Docs/protocol/ipc.md §Endpoint), so a lookup through the
	// pool here would deadlock against it.
	TeamActive func(ctx context.Context, tx *sql.Tx, teamID string) (bool, error)
	// TeamHasMembers reports whether the (already active) team teamID has
	// both self and peer as members, read through tx (see TeamActive).
	TeamHasMembers func(ctx context.Context, tx *sql.Tx, teamID, peer string) (bool, error)
	// UnverifiedPeer reports the D5 refusal for a sender: its trust is
	// "relay" and the configured relay is not loopback. Read through tx (see
	// TeamActive). A read error fails the apply (retryable), never open.
	UnverifiedPeer func(tx *sql.Tx, peer string) (bool, error)

	// DisableSenderBudget skips the sender-side urgency budget in Submit, so
	// tests can exercise receiver-side enforcement of an unmodified urgency
	// (Docs/protocol/request.md §Urgency guards (1.7)).
	DisableSenderBudget bool

	// Sessions, when set, wires work sessions (2.1a,
	// Docs/protocol/work-session.md) into the request lifecycle: opening a
	// session on accept, the early-complete path and the request_complete
	// shorthand. nil is Phase 1 behaviour unchanged.
	Sessions SessionHooks

	// Debates, when set, wires debates into the request lifecycle
	// (Docs/protocol/debate.md). nil refuses a received debate request as
	// bad_body, which is what a Phase 2 daemon answers.
	Debates DebateHooks

	// Helper, when set, routes every new request that passed the receive
	// steps and was stored pending to the own-device helper
	// (Docs/protocol/device.md §Running (in-scope requests)). nil is the
	// normal inbox for everything.
	Helper HelperRouter

	// NewIDFunc, when set, replaces NewID for the ids Submit draws, so tests
	// can force a collision (R55-F20).
	NewIDFunc func() string

	Now func() time.Time
}

// HelperRouter decides, inside the receive transaction, whether a new pending
// request is in an own-device helper's scope (Docs/protocol/device.md
// §Running). It must read and write only through tx. When autoAccept is true
// the Store accepts the request in tx (actor daemon), which opens its work
// session. after, if non-nil, is called once after the commit, outside any
// transaction (for the device audit rows and to queue the run); it is never
// called when the transaction rolls back.
type HelperRouter interface {
	RouteTx(ctx context.Context, tx *sql.Tx, req *Request, now time.Time) (autoAccept bool, after func(context.Context), err error)
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func badBody(format string, a ...any) error {
	return fmt.Errorf("request: "+format+": %w", append(a, mail.ErrBadBody)...)
}

// applyOutcome is passed from Apply to After in op.Outcome, so it is dropped
// with op when the transaction fails (review 55 R55-017).
type applyOutcome struct {
	newRow          bool
	autoDecline     bool
	conflict        bool
	cancelled       bool   // stored cancelled via a tombstone (Docs/protocol/request.md §Cancel (OD-P1-11))
	code            string // decline_code, when autoDecline
	requestID       string
	peer            string
	teamID          string
	typ             string
	urgency         string
	urgencyDeclared string // set only when downgradedBy is set
	downgradedBy    string // "", "sender" or "receiver"
	title           string
	contextFiles    int // sizes only: context text is never audited
	contextBytes    int
	autoAccepted    bool                  // accepted by the own-device helper in the receive tx
	acceptAfter     func(context.Context) // audits the daemon's request.accept
	helperAfter     func(context.Context) // the helper's after-commit step
}

// Kind returns the receiver handler for kind "request".
func (s *Store) Kind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.apply, After: s.after}
}

func (s *Store) apply(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	raw, _ := op.Msg.Body["request"].(map[string]any)
	if raw == nil {
		return badBody("body must hold a \"request\" object")
	}
	for k := range op.Msg.Body {
		if k != "request" {
			return badBody("unknown top-level member %q", k)
		}
	}
	req, err := Decode(raw)
	if err != nil {
		return badBody("%s", err.Error())
	}
	if req.Type == TypeDebate && s.Debates == nil {
		return badBody("type must be review, task or question")
	}
	if req.From != op.Msg.From {
		return badBody("request.from does not match the mail sender")
	}
	if req.To != op.Msg.To {
		return badBody("request.to does not match the mail recipient")
	}
	if req.Created.After(op.Msg.Created) {
		return badBody("request.created is after the mail's created time")
	}
	canon, err := Canonical(req)
	if err != nil {
		return badBody("%s", err.Error())
	}
	if err := CheckSizeFor(req, canon); err != nil {
		return badBody("%s", err.Error())
	}
	hash := BodyHash(canon)

	// Wire idempotency (Docs/protocol/request.md §Receiving step 2).
	var existingHash string
	err = tx.QueryRowContext(ctx, `SELECT body_hash FROM requests WHERE direction = 'in' AND peer = ? AND id = ?`,
		op.Msg.From, req.ID).Scan(&existingHash)
	switch {
	case err == nil:
		out := &applyOutcome{requestID: req.ID, peer: op.Msg.From, teamID: req.Team, conflict: existingHash != hash}
		op.Outcome = out
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("request: read existing row: %w", err)
	}

	now := s.now()
	if req.Type == TypeDebate {
		// A debate id re-used after prune removed its debate but kept its
		// Decision (review 71b F8).
		decided, err := s.Debates.HasDecisionTx(ctx, tx, req.From, req.To, req.ID)
		if err != nil {
			return err
		}
		if decided {
			return badBody("debate request id already has a Decision")
		}
	}
	// A request id is unique only per sender, so refuse one that another row
	// here already has: our `out` row to any peer, or another peer's `in` row
	// (Docs/protocol/request.md §Receiving step 2, R55-F20).
	taken, err := idTakenTx(ctx, tx, req.ID, op.Msg.From)
	if err != nil {
		return err
	}
	if taken {
		return badBody("request id is already used here")
	}
	intro, err := introducerOf(ctx, tx, op.Msg.From)
	if err != nil {
		return err
	}
	if err := checkDailyCaps(ctx, tx, op.Msg.From, intro, now); err != nil {
		return err
	}
	tombstoned, err := s.consumeTombstone(ctx, tx, op, req, canon, hash, intro, now)
	if err != nil {
		return err
	}
	if tombstoned {
		out := &applyOutcome{requestID: req.ID, peer: op.Msg.From, teamID: req.Team, typ: req.Type, urgency: req.Urgency, newRow: true, cancelled: true}
		op.Outcome = out
		return nil
	}
	if now.Sub(req.Created) > maxAge {
		return badBody("request is older than 30 days and unknown here")
	}

	out := &applyOutcome{requestID: req.ID, peer: op.Msg.From, teamID: req.Team, typ: req.Type, urgency: req.Urgency, title: req.Title,
		contextFiles: len(req.Context), contextBytes: ContextBytes(req.Context)}

	// Policy auto-decline (Docs/protocol/request.md §Receiving step 3).
	code, err := s.declineCode(ctx, tx, req, intro, now)
	if err != nil {
		return err
	}
	if code != "" {
		if err := s.storeDeclined(ctx, tx, op, req, hash, code, intro, now); err != nil {
			return err
		}
		out.newRow, out.autoDecline, out.code = true, true, code
		op.Outcome = out
		return nil
	}

	urgency, declared, downgradedBy, err := s.receiverUrgency(ctx, tx, req, now)
	if err != nil {
		return err
	}
	var downgradedByArg any
	if downgradedBy != "" {
		downgradedByArg = downgradedBy
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO requests (
	direction, peer, id, team_id, type, urgency, urgency_declared, downgraded_by,
	body, body_hash, state, state_seq, created, received_at, mail_id, updated, introducer, introduced_at
) VALUES ('in', ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?, ?, ?, ?, ?)`,
		op.Msg.From, req.ID, req.Team, req.Type, urgency, declared, downgradedByArg,
		string(canon), hash, wireTime(req.Created), storeTime(now), op.Msg.ID, storeTime(now), intro.by, intro.at,
	); err != nil {
		return fmt.Errorf("request: insert in row: %w", err)
	}
	out.newRow = true
	out.urgency = urgency
	if downgradedBy != "" {
		out.urgencyDeclared = declared
		out.downgradedBy = downgradedBy
	}
	if req.Type == TypeDebate {
		// A debate is argued by an agent, never run by an own-device helper.
		if err := s.Debates.ReceivedTx(ctx, tx, req, now); err != nil {
			return err
		}
	} else if s.Helper != nil {
		accept, after, err := s.Helper.RouteTx(ctx, tx, req, now)
		if err != nil {
			return err
		}
		out.helperAfter = after
		if accept {
			acceptAfter, err := s.AutoAcceptInTx(ctx, tx, req.ID, op.Msg.From)
			if err != nil {
				return err
			}
			// The helper's runner owns this session's result
			// (Docs/protocol/work-session.md §Run sessions, R55-029).
			if s.Sessions != nil {
				if err := s.Sessions.MarkRunnerTx(ctx, tx, op.Msg.From, req.ID); err != nil {
					return err
				}
			}
			out.autoAccepted, out.acceptAfter = true, acceptAfter
		}
	}
	op.Outcome = out
	return nil
}

// receiverUrgency is Docs/protocol/request.md §Receiving step 4, §Urgency
// guards (1.7): if the sender already downgraded (urgency_declared present
// on the body), mirror that as downgraded_by "sender"; otherwise, for an
// arriving high or blocking request, apply the per-sender receiver-side
// budget, tamper-proof because it counts the receiver's own stored rows.
func (s *Store) receiverUrgency(ctx context.Context, tx *sql.Tx, req *Request, now time.Time) (urgency, declared, downgradedBy string, err error) {
	if req.UrgencyDeclared != "" {
		return req.Urgency, req.UrgencyDeclared, "sender", nil
	}
	if req.Urgency != UrgencyHigh && req.Urgency != UrgencyBlocking {
		return req.Urgency, req.Urgency, "", nil
	}
	limit, _ := budgetLimit(req.Urgency)
	cutoff := storeTime(now.Add(-urgencyBudgetWindow))
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM requests
		WHERE direction = 'in' AND peer = ? AND urgency = ? AND received_at >= ?
		  AND (decline_code IS NULL OR decline_code = 'user')`,
		req.From, req.Urgency, cutoff).Scan(&count); err != nil {
		return "", "", "", fmt.Errorf("request: receiver budget: %w", err)
	}
	if count >= limit {
		return UrgencyNormal, req.Urgency, "receiver", nil
	}
	return req.Urgency, req.Urgency, "", nil
}

// effectiveDeclared is urgency_declared for an `in` row: the body's value if
// present, else the body's urgency (Docs/protocol/request.md §Tables).
func effectiveDeclared(r *Request) string {
	if r.UrgencyDeclared != "" {
		return r.UrgencyDeclared
	}
	return r.Urgency
}

// declineCode returns the auto-decline code for req, or "" to accept it,
// checked in the order of Docs/protocol/request.md §Receiving step 3.
func (s *Store) declineCode(ctx context.Context, tx *sql.Tx, req *Request, intro introduction, now time.Time) (string, error) {
	code, err := s.policyDeclineCode(ctx, tx, req)
	if code != "" || err != nil {
		return code, err
	}
	full, err := openCapReached(ctx, tx, req.From, intro, now)
	if err != nil {
		return "", err
	}
	if full {
		return "inbox_full", nil
	}
	return "", nil
}

// policyDeclineCode is declineCode without the open caps, which are checked
// last.
func (s *Store) policyDeclineCode(ctx context.Context, tx *sql.Tx, req *Request) (string, error) {
	if s.UnverifiedPeer != nil {
		unverified, err := s.UnverifiedPeer(tx, req.From)
		if err != nil {
			return "", fmt.Errorf("request: peer trust: %w", err)
		}
		if unverified {
			return "unverified_peer", nil
		}
	}
	if s.TeamActive == nil {
		return "", nil
	}
	active, err := s.TeamActive(ctx, tx, req.Team)
	if err != nil {
		return "", fmt.Errorf("request: team active: %w", err)
	}
	if !active {
		return "unknown_team", nil
	}
	if s.TeamHasMembers == nil {
		return "", nil
	}
	ok, err := s.TeamHasMembers(ctx, tx, req.Team, req.From)
	if err != nil {
		return "", fmt.Errorf("request: team members: %w", err)
	}
	if !ok {
		return "not_team_member", nil
	}
	return "", nil
}

// storeDeclined stores the row as declined and sends the decline reply inside
// tx, so a crash cannot leave a declined row with no reply
// (Docs/protocol/request.md §Receiving step 3). The row keeps no content: body
// is {} and body_hash is the received request's, so a resend is still
// recognised (review 55 R55-018, O-093).
func (s *Store) storeDeclined(ctx context.Context, tx *sql.Tx, op *mail.Opened, req *Request, hash, code string, intro introduction, now time.Time) error {
	replyBody := map[string]any{"at": wireTime(now), "code": code, "request": req.ID, "seq": 1}
	lastReply, err := jsonObject(map[string]any{"kind": "request.decline", "body": replyBody})
	if err != nil {
		return fmt.Errorf("request: encode last_reply: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO requests (
	direction, peer, id, team_id, type, urgency, urgency_declared, downgraded_by,
	body, body_hash, state, state_seq, decline_code, created, received_at, mail_id,
	last_reply, last_reply_sent, updated, introducer, introduced_at
) VALUES ('in', ?, ?, ?, ?, ?, ?, NULL, ?, ?, 'declined', 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.Msg.From, req.ID, req.Team, req.Type, req.Urgency, effectiveDeclared(req),
		emptyBody, hash, code, wireTime(req.Created), storeTime(now), op.Msg.ID,
		lastReply, storeTime(now), storeTime(now), intro.by, intro.at,
	); err != nil {
		return fmt.Errorf("request: insert declined row: %w", err)
	}
	if s.Outbox != nil {
		if _, err := s.Outbox.SubmitTx(ctx, tx, op.Msg.From, "request.decline", replyBody); err != nil {
			return fmt.Errorf("request: submit decline: %w", err)
		}
	}
	return nil
}

// consumeTombstone stores the `in` row as cancelled and consumes the
// tombstone, if one exists for (op.Msg.From, req.ID)
// (Docs/protocol/request.md §Receiving step 2, "with no row, and a cancel
// tombstone exists").
func (s *Store) consumeTombstone(ctx context.Context, tx *sql.Tx, op *mail.Opened, req *Request, canon []byte, hash string, intro introduction, now time.Time) (bool, error) {
	var reason sql.NullString
	var receivedAt string
	err := tx.QueryRowContext(ctx, `SELECT reason, received_at FROM request_cancels WHERE peer = ? AND id = ?`, op.Msg.From, req.ID).
		Scan(&reason, &receivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("request: read tombstone: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM request_cancels WHERE peer = ? AND id = ?`, op.Msg.From, req.ID); err != nil {
		return false, fmt.Errorf("request: delete tombstone: %w", err)
	}
	// The row keeps no content when D5 or the team checks would have declined
	// the request (Docs/protocol/request.md §Receiving step 2, R55-163).
	body := string(canon)
	code, err := s.policyDeclineCode(ctx, tx, req)
	if err != nil {
		return false, err
	}
	if code != "" {
		body = emptyBody
	}
	at := parseStoreTime(receivedAt)
	replyBody := map[string]any{"at": wireTime(at), "request": req.ID, "seq": 1}
	lastReply, err := jsonObject(map[string]any{"kind": KindCancelled, "body": replyBody})
	if err != nil {
		return false, fmt.Errorf("request: encode last_reply: %w", err)
	}
	var reasonArg any
	if reason.Valid {
		reasonArg = reason.String
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO requests (
	direction, peer, id, team_id, type, urgency, urgency_declared, downgraded_by,
	body, body_hash, state, state_seq, state_at, reason, created, received_at, mail_id,
	last_reply, last_reply_sent, updated, introducer, introduced_at
) VALUES ('in', ?, ?, ?, ?, ?, ?, NULL, ?, ?, 'cancelled', 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.Msg.From, req.ID, req.Team, req.Type, req.Urgency, effectiveDeclared(req),
		body, hash, wireTime(at), reasonArg, wireTime(req.Created), storeTime(now), op.Msg.ID,
		lastReply, storeTime(at), storeTime(now), intro.by, intro.at,
	); err != nil {
		return false, fmt.Errorf("request: insert tombstoned row: %w", err)
	}
	return true, nil
}

// idTakenTx reports whether a requests row other than (in, from, id) has id:
// an `out` row to any peer or an `in` row from another peer (R55-F20). With
// from empty it reports any row with id.
func idTakenTx(ctx context.Context, tx *sql.Tx, id, from string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM requests WHERE id = ? AND NOT (direction = 'in' AND peer = ?) LIMIT 1`, id, from).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("request: check id: %w", err)
	}
	return true, nil
}

// introduction is the sender's introducer and introduction time, recorded on
// every `in` row it sends (Docs/protocol/request.md §Receiving, review 71b F2,
// owner decision D62).
type introduction struct {
	by sql.NullString // peers.introduced_by; NULL for a directly paired or unknown sender
	at sql.NullString // that peer's paired_at (its introduction), store format; NULL when by is
}

// introducerOf reads the sender's introduction. An introduced peer's
// paired_at is the local time the first roster naming it was stored
// (peers.Store.Introduce); a later roster keeps it, and a direct pairing
// clears introduced_by.
func introducerOf(ctx context.Context, tx *sql.Tx, peer string) (introduction, error) {
	var by, pairedAt sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT introduced_by, paired_at FROM peers WHERE public_key = ?`, peer).Scan(&by, &pairedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return introduction{}, fmt.Errorf("request: read introducer: %w", err)
	}
	if by.String == "" {
		return introduction{}, nil
	}
	at, err := time.Parse(time.RFC3339, pairedAt.String)
	if err != nil {
		return introduction{}, fmt.Errorf("request: read introducer: paired_at: %w", err)
	}
	return introduction{by: by, at: sql.NullString{String: storeTime(at), Valid: true}}, nil
}

// freshSince is the earliest introduction time that still counts towards the
// introducer caps at now (D62).
func freshSince(now time.Time) string { return storeTime(now.Add(-IntroducerFreshFor)) }

// freshAt reports whether the sender was introduced within IntroducerFreshFor
// of now, so the introducer caps apply to it (D62).
func (i introduction) freshAt(now time.Time) bool {
	return i.by.Valid && i.at.String >= freshSince(now)
}

// checkDailyCaps is the daily cap of Docs/protocol/request.md §Per-peer caps:
// `in` rows received in the last 24 h, in any state, per sender key and, for
// a freshly introduced sender, per introducer over its fresh keys only (D62).
// Over either, the mail is refused for a limit (mail.ErrLimit).
func checkDailyCaps(ctx context.Context, tx *sql.Tx, peer string, intro introduction, now time.Time) error {
	since := storeTime(now.Add(-24 * time.Hour))
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND peer = ? AND received_at >= ?`, peer, since).Scan(&n); err != nil {
		return fmt.Errorf("request: count daily rows: %w", err)
	}
	if n >= MaxNewPerPeerPerDay {
		return fmt.Errorf("request: sender over the daily cap: %w", mail.ErrLimit)
	}
	if !intro.freshAt(now) {
		return nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM requests
WHERE direction = 'in' AND introducer = ? AND introduced_at >= ? AND received_at >= ?`, intro.by.String, freshSince(now), since).Scan(&n); err != nil {
		return fmt.Errorf("request: count daily rows: %w", err)
	}
	if n >= MaxNewPerIntroducerPerDay {
		return fmt.Errorf("request: introducer over the daily cap: %w", mail.ErrLimit)
	}
	return nil
}

// openCapReached is the open cap of Docs/protocol/request.md §Per-peer caps:
// `in` rows in state pending, deferred or accepted, per sender key and, for a
// freshly introduced sender, per introducer over its fresh keys only (D62).
func openCapReached(ctx context.Context, tx *sql.Tx, peer string, intro introduction, now time.Time) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM requests
WHERE direction = 'in' AND peer = ? AND state IN ('pending', 'deferred', 'accepted')`, peer).Scan(&n); err != nil {
		return false, fmt.Errorf("request: count open rows: %w", err)
	}
	if n >= MaxOpenPerPeer {
		return true, nil
	}
	if !intro.freshAt(now) {
		return false, nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM requests
WHERE direction = 'in' AND introducer = ? AND introduced_at >= ? AND state IN ('pending', 'deferred', 'accepted')`,
		intro.by.String, freshSince(now)).Scan(&n); err != nil {
		return false, fmt.Errorf("request: count open rows: %w", err)
	}
	return n >= MaxOpenPerIntroducer, nil
}

// after audits the outcome, once, after a successful commit
// (Docs/protocol/request.md §Receiving, final paragraph).
func (s *Store) after(ctx context.Context, op *mail.Opened) {
	out, ok := op.Outcome.(*applyOutcome)
	if !ok {
		return
	}
	if s.Outbox != nil {
		s.Outbox.Wake()
	}
	if !out.newRow && !out.conflict {
		s.resubmitStale(ctx, "in", out.peer, out.requestID, s.now())
	}
	// An in-scope helper request was accepted by the daemon: nobody needs to
	// look at it, so there is no "received" notification.
	if out.newRow && !out.cancelled && !out.autoDecline && !out.autoAccepted && s.Notify != nil {
		s.Notify(ctx, EventReceived, NotifyInfo{
			Peer: out.peer, Type: out.typ, Urgency: out.urgency, Title: out.title,
			RequestID: out.requestID, State: "pending", TeamID: out.teamID,
		})
	}
	if s.Audit != nil {
		s.auditReceived(ctx, out)
	}
	if out.acceptAfter != nil {
		out.acceptAfter(ctx)
	}
	if out.helperAfter != nil {
		out.helperAfter(ctx)
	}
}

// auditReceived appends the receive outcome's audit row.
func (s *Store) auditReceived(ctx context.Context, out *applyOutcome) {
	switch {
	case out.cancelled:
		// Stored cancelled via a tombstone: no notification, and no audit
		// action is listed for this path beyond the cancel's own
		// request.cancel_in (already audited by the cancel that created the
		// tombstone).
	case out.conflict:
		_ = s.Audit.Append(ctx, "daemon", "request.conflict", map[string]any{"request": out.requestID, "peer": out.peer})
	case !out.newRow:
		_ = s.Audit.Append(ctx, "daemon", "request.duplicate", map[string]any{"request": out.requestID, "peer": out.peer})
	case out.autoDecline:
		_ = s.Audit.Append(ctx, "daemon", "request.auto_decline",
			map[string]any{"request": out.requestID, "peer": out.peer, "team": out.teamID, "code": out.code})
	default:
		detail := map[string]any{"request": out.requestID, "peer": out.peer, "team": out.teamID, "type": out.typ, "urgency": out.urgency}
		if out.downgradedBy != "" {
			detail["urgency_declared"] = out.urgencyDeclared
			detail["downgraded_by"] = out.downgradedBy
		}
		if out.contextFiles > 0 {
			detail["context_files"] = out.contextFiles
			detail["context_bytes"] = out.contextBytes
		}
		_ = s.Audit.Append(ctx, "daemon", "request.in", detail)
	}
}
