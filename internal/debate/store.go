package debate

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Mail kinds (Docs/protocol/debate.md §Kinds). debate.constraint is in
// constraint.go, debate.sign in decision.go.
const (
	MailEntry  = "debate.entry"
	MailReveal = "debate.reveal"
	MailClose  = "debate.close"
)

// Outcomes and reasons (debates.outcome, debates.reason).
const (
	OutcomeAgreed    = "agreed"
	OutcomeEscalated = "escalated"
	OutcomeCancelled = "cancelled"

	ReasonAccepted  = "accepted"
	ReasonRejected  = "rejected"
	ReasonTimeout   = "timeout"
	ReasonCancelled = "cancelled"
	ReasonAbandoned = "abandoned"
)

// Entry states (debate_entries.state).
const (
	stateCommitted = "committed" // the initiator's slot 0 before the reveal
	stateApplied   = "applied"
	stateSent      = "sent" // the respondent's own entries
	stateEarly     = "early"
	// stateLate is a view-only state: B's own entry at a slot A's close did
	// not count (never stored; see buildView).
	stateLate = "late"
)

// Notification events (Docs/protocol/debate.md §Notifications), passed to
// Store.OnEvent. 3.1b wires them to internal/notify.
const (
	EventAgreed    = "debate.agreed"
	EventEscalated = "debate.escalated"
	EventBroken    = "debate.broken"
)

// echoInterval is the 10-minute rule of the last_state echo.
const echoInterval = 10 * time.Minute

// SubmitTx is the outbox capability the store needs: sending debate mail in
// the same transaction as the rows it belongs to.
type SubmitTx interface {
	SubmitTx(ctx context.Context, tx *sql.Tx, to, kind string, body any) (mail.Submitted, error)
	Wake()
}

// AuditSink is the part of audit.Log the Store needs.
type AuditSink interface {
	Append(ctx context.Context, actor, action string, detail any) error
}

// Store owns the debates and debate_entries tables (migration 19) and the
// debate.* mail kinds. It implements request.DebateHooks and
// worksession.DebateHooks.
type Store struct {
	DB       *sql.DB
	Self     string
	Outbox   SubmitTx
	Audit    AuditSink // may be nil
	Requests *request.Store
	Sessions *worksession.Store

	// Priv loads the daemon's identity key, which signs Decisions
	// (Docs/protocol/decision.md §Signing, OD-P3-5). The copy is cleared
	// after use.
	Priv func() (ed25519.PrivateKey, error)

	// PeerQuarantine reports, through tx, the peer-wide quarantine clause
	// from this side: this daemon issued a sensitive grant to peer whose exp
	// is later than now - 7 d (capability.Store.PeerQuarantineHoldsTx).
	// Checked when A starts a debate and when B accepts one
	// (Docs/protocol/debate.md §Quarantine interplay). nil never holds.
	PeerQuarantine func(ctx context.Context, tx *sql.Tx, peer string) (bool, error)

	// OnEvent, if set, is called after commit with a content-free
	// notification event (EventAgreed, EventEscalated, EventBroken) and ids.
	OnEvent func(ctx context.Context, event, sid, peer, requestID string)

	// Log receives errors from Sweep that do not stop it (review 45 L2): one
	// bad debate is logged and skipped, never lost silently. Nil discards
	// them.
	Log *slog.Logger

	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

const timeFmt = "2006-01-02T15:04:05Z"

func wireTime(t time.Time) string  { return t.UTC().Truncate(time.Second).Format(timeFmt) }
func storeTime(t time.Time) string { return t.UTC().Format(mail.StoreTimeFmt) }

func parseWireTime(s string) time.Time {
	t, _ := time.Parse(timeFmt, s)
	return t
}

func parseStoreTime(s string) time.Time {
	t, _ := time.Parse(mail.StoreTimeFmt, s)
	return t
}

// row is one debates row.
type row struct {
	session, role, peer, requestID string
	roundsMax, turnTimeoutS        int
	commitment                     string
	nonce                          sql.NullString
	phase                          string
	nextSlot                       int
	turnDeadline                   sql.NullString
	outcome, reason                sql.NullString
	lastState, lastStateSent       sql.NullString
	closeBody                      sql.NullString
	created, updated               string
}

const debateColumns = `session, role, peer, request_id, rounds_max, turn_timeout_s, commitment, nonce, phase,
	next_slot, turn_deadline, outcome, reason, last_state, last_state_sent, close_body, created, updated`

type scanner interface{ Scan(dest ...any) error }

func scanRow(sc scanner) (row, error) {
	var r row
	err := sc.Scan(&r.session, &r.role, &r.peer, &r.requestID, &r.roundsMax, &r.turnTimeoutS, &r.commitment, &r.nonce,
		&r.phase, &r.nextSlot, &r.turnDeadline, &r.outcome, &r.reason, &r.lastState, &r.lastStateSent, &r.closeBody,
		&r.created, &r.updated)
	return r, err
}

// queryer is *sql.DB or *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func getRow(ctx context.Context, q queryer, sid string) (row, error) {
	r, err := scanRow(q.QueryRowContext(ctx, `SELECT `+debateColumns+` FROM debates WHERE session = ?`, sid))
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, ErrUnknownDebate
	}
	if err != nil {
		return row{}, fmt.Errorf("debate: read row: %w", err)
	}
	return r, nil
}

func getRowByRequest(ctx context.Context, q queryer, role, peer, requestID string) (row, error) {
	r, err := scanRow(q.QueryRowContext(ctx, `SELECT `+debateColumns+` FROM debates WHERE role = ? AND peer = ? AND request_id = ?`, role, peer, requestID))
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, ErrUnknownDebate
	}
	if err != nil {
		return row{}, fmt.Errorf("debate: read row: %w", err)
	}
	return r, nil
}

// resolve finds a debate by its session id (s-) or its request id (r-).
// Request ids are unique per sender, so a peer can send a debate request that
// reuses the id of one this daemon sent it (review 45 L1): two rows matching
// an r- id is an ambiguity, reported the same way request_* does, not
// silently resolved to the older row.
func resolve(ctx context.Context, q queryer, id string) (row, error) {
	switch {
	case worksession.ValidID(id):
		return getRow(ctx, q, id)
	case request.ValidID(id):
		rows, err := q.QueryContext(ctx, `SELECT `+debateColumns+` FROM debates WHERE request_id = ? ORDER BY created`, id)
		if err != nil {
			return row{}, fmt.Errorf("debate: read row: %w", err)
		}
		defer func() { _ = rows.Close() }()
		var matches []row
		for rows.Next() {
			r, err := scanRow(rows)
			if err != nil {
				return row{}, fmt.Errorf("debate: scan row: %w", err)
			}
			matches = append(matches, r)
		}
		if err := rows.Err(); err != nil {
			return row{}, fmt.Errorf("debate: read row: %w", err)
		}
		switch len(matches) {
		case 0:
			return row{}, ErrUnknownDebate
		case 1:
			return matches[0], nil
		default:
			return row{}, request.ErrAmbiguousRequest
		}
	}
	return row{}, ErrUnknownDebate
}

// open reports whether the debate is in a phase where entries flow.
func (r row) open() bool {
	return r.phase == PhasePositions || r.phase == PhaseRounds || r.phase == PhaseConverge
}

// initiatorKey and respondentKey are the two identity keys of r from self's
// point of view.
func (r row) keys(self string) (a, b string) {
	if r.role == RoleInitiator {
		return self, r.peer
	}
	return r.peer, self
}

// stored is one debate_entries row, with its entry parsed.
type stored struct {
	slot                    int
	author, kind, at, state string
	canon                   []byte
	entry                   Entry
}

// transcript is a debate's entries by slot (every state).
type transcript map[int]stored

func loadTranscript(ctx context.Context, q queryer, sid string) (transcript, error) {
	rows, err := q.QueryContext(ctx, `SELECT slot, author, kind, entry, at, state FROM debate_entries WHERE session = ?`, sid)
	if err != nil {
		return nil, fmt.Errorf("debate: read entries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	tr := transcript{}
	for rows.Next() {
		var e stored
		var raw string
		if err := rows.Scan(&e.slot, &e.author, &e.kind, &raw, &e.at, &e.state); err != nil {
			return nil, fmt.Errorf("debate: scan entry: %w", err)
		}
		e.canon = []byte(raw)
		parsed, err := ParseEntry(e.kind, e.canon)
		if err != nil {
			return nil, fmt.Errorf("debate: stored entry %d: %w", e.slot, err)
		}
		e.entry = parsed
		tr[e.slot] = e
	}
	return tr, rows.Err()
}

// metas is the turn engine's view of tr: the entries whose state is in
// states.
func (tr transcript) metas(states ...string) map[int]Meta {
	out := map[int]Meta{}
	for s, e := range tr {
		for _, st := range states {
			if e.state == st {
				m := Meta{Author: e.author, Kind: e.kind}
				if mv, ok := e.entry.(*Move); ok {
					m.Challenges = len(mv.Challenges)
				}
				out[s] = m
				break
			}
		}
	}
	return out
}

// turnStates are the entry states that count for the turn engine on each
// side (§Submitting entries: on B a sent entry counts as applied).
func turnStates(role string) []string {
	if role == RoleInitiator {
		return []string{stateApplied}
	}
	return []string{stateApplied, stateSent}
}

// current returns author's current position before slot: its opening
// position, replaced by each revision in its moves before slot
// (§Targets: "its latest revision at the time of the move"). nil if the
// opening position is not known yet.
func (tr transcript) current(author string, before int, states ...string) *Position {
	open := 1
	if author == RoleInitiator {
		open = 0
	}
	e, ok := tr[open]
	if !ok || !hasState(e, states) {
		return nil
	}
	pos, _ := e.entry.(*Position)
	slots := make([]int, 0, len(tr))
	for s := range tr {
		slots = append(slots, s)
	}
	sort.Ints(slots)
	for _, s := range slots {
		if s < 2 || s >= before {
			continue
		}
		e := tr[s]
		if e.author != author || !hasState(e, states) {
			continue
		}
		if mv, ok := e.entry.(*Move); ok && mv.Revision != nil {
			pos = mv.Revision
		}
	}
	return pos
}

func hasState(e stored, states []string) bool {
	for _, st := range states {
		if e.state == st {
			return true
		}
	}
	return false
}

// other is the side that is not role.
func other(role string) string {
	if role == RoleInitiator {
		return RoleRespondent
	}
	return RoleInitiator
}

// checkEntryTargets checks a move's targets against the other side's current
// position in tr (§Targets). Non-moves pass.
func checkEntryTargets(tr transcript, e Entry, author string, slot int, states ...string) error {
	mv, ok := e.(*Move)
	if !ok {
		return nil
	}
	pos := tr.current(other(author), slot, states...)
	if pos == nil {
		return fieldErr("challenges", "the other side's position is not known yet")
	}
	return CheckTargets(mv, pos)
}

// entryValue is canon (a canonical entry) as a generic JSON value for a
// mail body, which the outbox re-encodes canonically.
func entryValue(canon []byte) (any, error) {
	return agentcard.ParseStrict(canon)
}

func jsonObject(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// entryAudit is the content-free audit detail of an entry: sizes, counts and
// a boolean (§Audit, debate.entry).
func entryAudit(sid, peer string, slot int, kind string, canon []byte, e Entry) map[string]any {
	d := map[string]any{"session": sid, "peer": peer, "slot": slot, "kind": kind, "bytes": len(canon)}
	if mv, ok := e.(*Move); ok {
		d["challenges"] = len(mv.Challenges)
		d["revision"] = mv.Revision != nil
	}
	return d
}

// afters collects after-commit callbacks.
type afters []func(context.Context)

func (a *afters) add(fn func(context.Context)) {
	if fn != nil {
		*a = append(*a, fn)
	}
}

func (a afters) run(ctx context.Context) {
	for _, fn := range a {
		fn(ctx)
	}
}

func (s *Store) audit(actor, action string, detail map[string]any) func(context.Context) {
	return func(ctx context.Context) {
		if s.Audit != nil {
			_ = s.Audit.Append(ctx, actor, action, detail)
		}
	}
}

func (s *Store) event(event, sid, peer, requestID string) func(context.Context) {
	return func(ctx context.Context) {
		if s.OnEvent != nil {
			s.OnEvent(ctx, event, sid, peer, requestID)
		}
	}
}

// constraintIDs are the sorted ids of the active constraints of sid, the
// close's "constraints" (3.4 fills the table).
func constraintIDs(ctx context.Context, tx *sql.Tx, sid string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM debate_constraints WHERE session = ? AND state = 'active' ORDER BY id`, sid)
	if err != nil {
		return nil, fmt.Errorf("debate: read constraints: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// fieldPrefix renames a FieldError's field under prefix, for errors of an
// entry nested in IPC params ("debate.position.claim").
func fieldPrefix(err error, prefix string) error {
	var fe *FieldError
	if errors.As(err, &fe) {
		return &FieldError{Field: strings.TrimSuffix(prefix+"."+fe.Field, "."), Reason: fe.Reason}
	}
	return err
}
