package debate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// View is a decoded debate for debate_show and debate_list (the IPC shapes
// of Docs/protocol/debate.md §IPC are built from it by 3.1b). Transcript
// holds the entries the local agent may see, in slot order: never an
// entry held early, and on B never slot 0 before the reveal.
type View struct {
	Session      string
	Role         string
	Peer         string
	RequestID    string
	Phase        string
	Outcome      string // closed, or decided in closing
	Reason       string
	RoundsMax    int
	RoundsUsed   int
	TurnTimeoutS int
	Turn         string // "you", "peer" or "none"
	Expect       string // the next entry kind when Turn is "you"
	NextSlot     int
	Deadline     time.Time // zero when none
	Waiting      string    // "reveal", "entry" or "signature"
	Transcript   []ViewEntry
	// Constraints are the human constraints an agent may see, ordered by
	// (at, id): active and late, never excess (3.4). Get fills this; List
	// fills ConstraintCount instead (review 46 L7: the list view keeps
	// counts, not the texts).
	Constraints     []ViewConstraint
	ConstraintCount int
}

// ViewEntry is one transcript entry. Entry is its canonical JSON.
type ViewEntry struct {
	Slot   int
	Author string
	Kind   string
	At     string
	State  string
	Entry  json.RawMessage
}

// Get resolves a debate by session (s-) or request (r-) id.
func (s *Store) Get(ctx context.Context, id string) (View, error) {
	r, err := resolve(ctx, s.DB, id)
	if err != nil {
		return View{}, err
	}
	tr, err := loadTranscript(ctx, s.DB, r.session)
	if err != nil {
		return View{}, err
	}
	v := buildView(r, tr)
	if v.Constraints, err = loadConstraints(ctx, s.DB, r.session); err != nil {
		return View{}, err
	}
	return v, nil
}

// List returns every debate, newest first, optionally narrowed by phase and
// peer.
func (s *Store) List(ctx context.Context, phase, peer string) ([]View, error) {
	q := `SELECT ` + debateColumns + ` FROM debates WHERE 1=1`
	var args []any
	if phase != "" {
		q += ` AND phase = ?`
		args = append(args, phase)
	}
	if peer != "" {
		q += ` AND peer = ?`
		args = append(args, peer)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY created DESC, session`, args...)
	if err != nil {
		return nil, fmt.Errorf("debate: list: %w", err)
	}
	var rs []row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		rs = append(rs, r)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]View, 0, len(rs))
	for _, r := range rs {
		tr, err := loadTranscript(ctx, s.DB, r.session)
		if err != nil {
			return nil, err
		}
		v := buildView(r, tr)
		// The list view keeps counts only (§IPC), so it never loads the
		// texts (review 46 L7).
		if v.ConstraintCount, err = countVisibleConstraints(ctx, s.DB, r.session); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func buildView(r row, tr transcript) View {
	v := View{
		Session: r.session, Role: r.role, Peer: r.peer, RequestID: r.requestID, Phase: r.phase,
		RoundsMax: r.roundsMax, TurnTimeoutS: r.turnTimeoutS, Turn: "none", NextSlot: r.nextSlot,
	}
	if r.outcome.Valid {
		v.Outcome = r.outcome.String
	}
	if r.reason.Valid {
		v.Reason = r.reason.String
	}
	if r.turnDeadline.Valid {
		v.Deadline = parseWireTime(r.turnDeadline.String)
	}
	metas := tr.metas(turnStates(r.role)...)
	v.RoundsUsed = RoundsUsed(metas)
	slots := make([]int, 0, len(tr))
	for sl, e := range tr {
		if e.state == stateEarly {
			continue
		}
		slots = append(slots, sl)
	}
	sort.Ints(slots)
	// On a closed B, its own entries at slots A's close did not count are
	// late: A never applied them and they are in no Decision (decision.md
	// §Signing step 2).
	late := -1
	if r.role == RoleRespondent && r.phase == PhaseClosed && r.closeBody.Valid {
		var cb struct {
			Entries *int `json:"entries"`
		}
		if json.Unmarshal([]byte(r.closeBody.String), &cb) == nil && cb.Entries != nil {
			late = *cb.Entries
		}
	}
	for _, sl := range slots {
		e := tr[sl]
		state := e.state
		if late >= 0 && sl >= late && e.author == RoleRespondent {
			state = stateLate
		}
		v.Transcript = append(v.Transcript, ViewEntry{Slot: sl, Author: e.author, Kind: e.kind, At: e.at, State: state, Entry: json.RawMessage(e.canon)})
	}
	switch {
	case r.open():
		t := Next(metas, r.roundsMax)
		switch {
		case t.Done:
			v.Waiting = "signature"
		case t.Slot == 0:
			v.Waiting = "reveal"
		case t.Author == r.role:
			v.Turn, v.Expect = "you", t.Kind
		default:
			v.Turn, v.Waiting = "peer", "entry"
		}
	case r.phase == PhaseClosing:
		v.Waiting = "signature"
		// The decided outcome is in the close A sent (outcome stays NULL
		// until closed, the table's CHECK).
		var ls struct {
			Body struct {
				Outcome string `json:"outcome"`
			} `json:"body"`
		}
		if r.lastState.Valid && json.Unmarshal([]byte(r.lastState.String), &ls) == nil {
			v.Outcome = ls.Body.Outcome
		}
	}
	return v
}
