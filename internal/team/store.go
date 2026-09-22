package team

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

// AuditSink is the part of audit.Log that Store needs.
type AuditSink interface {
	Append(ctx context.Context, actor, action string, detail any) error
}

// Outbox is the part of mail.Outbox that Store needs to send roster and keys
// mail (satisfied by *mail.Outbox).
type Outbox interface {
	Submit(ctx context.Context, to, kind string, body any) (mail.Submitted, error)
}

// Actors, mirroring internal/audit.
const (
	ActorDaemon = "daemon"
	ActorCLI    = "cli"
)

// Audit actions, Docs/protocol/team.md §Audit.
const (
	ActionCreate        = "team.create"
	ActionMemberAdd     = "team.member_add"
	ActionMemberRemove  = "team.member_remove"
	ActionMemberLeave   = "team.member_leave"
	ActionLeave         = "team.leave"
	ActionRename        = "team.rename"
	ActionDelete        = "team.delete"
	ActionRosterApply   = "team.roster_apply"
	ActionRosterIgnored = "team.roster_ignored"
	ActionJoinIgnored   = "team.join_ignored"
	ActionLeaveIgnored  = "team.leave_ignored"
	ActionPeerRemove    = "peer.remove"
	ActionInvite        = "team.invite"
	ActionJoinSent      = "team.join"
)

// inviteTTL is how long a team_invites or team_pending_joins row lives
// (Docs/protocol/team.md §Operations: Invite, Join).
const inviteTTL = 24 * time.Hour

// Store owns the teams, team_members, team_invites and team_pending_joins
// tables (migration 9) and the kind handlers of Docs/protocol/team.md §Kinds.
type Store struct {
	db    *sql.DB
	peers *peers.Store
	audit AuditSink

	// Self is this daemon's own identity key, wire form.
	Self string
	// OwnCard returns the canonical signed Agent Card envelope of this daemon
	// ({"card":...,"signature":...}), as agentcard.Verify accepts.
	OwnCard func() ([]byte, error)
	// Announcement returns this daemon's current signed mailbox announcement,
	// or nil with no error if it has none yet.
	Announcement func() ([]byte, error)
	// Outbox sends application mail. Required to broadcast rosters and push
	// keys to newly introduced peers; nil disables sending (validation and
	// local state changes still work, for tests that do not need mail out).
	Outbox Outbox
	Now    func() time.Time
	Log    *slog.Logger
	// OnMembersChanged, if set, is called after any operation that may change
	// which peers share an active team with self (add, remove, leave, delete,
	// and the roster/join/leave mail kinds). The presence sender (1.2c) uses
	// it to resend the visible set and goodbye peers that left it.
	OnMembersChanged func()
}

// NewStore returns a Store over a migrated database.
func NewStore(db *sql.DB, ps *peers.Store, self string) *Store {
	return &Store{db: db, peers: ps, Self: self}
}

// SetAudit sets where Store reports audit events.
func (s *Store) SetAudit(a AuditSink) { s.audit = a }

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func stamp(t time.Time) string { return t.UTC().Format(storeTimeFmt) }

func (s *Store) audited(ctx context.Context, actor, action string, detail any) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Append(ctx, actor, action, detail)
}

// changed notifies OnMembersChanged, if set.
func (s *Store) changed() {
	if s.OnMembersChanged != nil {
		s.OnMembersChanged()
	}
}

// Get returns the team by id.
func (s *Store) Get(ctx context.Context, id string) (Team, error) {
	return getTx(ctx, s.db, id)
}

func getTx(ctx context.Context, q querier, id string) (Team, error) {
	var t Team
	err := q.QueryRowContext(ctx, `SELECT id, name, owner, epoch, state, created, updated FROM teams WHERE id = ?`, id).
		Scan(&t.ID, &t.Name, &t.Owner, &t.Epoch, &t.State, &t.Created, &t.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Team{}, ErrNotFound
	}
	if err != nil {
		return Team{}, fmt.Errorf("team: get: %w", err)
	}
	return t, nil
}

// querier is satisfied by both *sql.DB and *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Members returns the members of team id, oldest added first.
func (s *Store) Members(ctx context.Context, id string) ([]Member, error) {
	return membersTx(ctx, s.db, id)
}

func membersTx(ctx context.Context, q querier, id string) ([]Member, error) {
	rows, err := q.QueryContext(ctx, `SELECT key, added FROM team_members WHERE team_id = ? ORDER BY added, key`, id)
	if err != nil {
		return nil, fmt.Errorf("team: members: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.Key, &m.Added); err != nil {
			return nil, fmt.Errorf("team: members: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// OwnedTeamsWithMember returns the active teams owned by self that key
// currently belongs to (used by the daemon to cascade a `peers remove` of a
// team member into that team's roster, Docs/cli/peers.md §peers remove).
func (s *Store) OwnedTeamsWithMember(ctx context.Context, key string) ([]Team, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.name, t.owner, t.epoch, t.state, t.created, t.updated
		FROM teams t JOIN team_members tm ON tm.team_id = t.id
		WHERE tm.key = ? AND t.owner = ? AND t.state = 'active'`, key, s.Self)
	if err != nil {
		return nil, fmt.Errorf("team: owned teams with member: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Team{}
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.Owner, &t.Epoch, &t.State, &t.Created, &t.Updated); err != nil {
			return nil, fmt.Errorf("team: owned teams with member: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// List returns every local team, oldest created first.
func (s *Store) List(ctx context.Context) ([]Team, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, owner, epoch, state, created, updated FROM teams ORDER BY created, id`)
	if err != nil {
		return nil, fmt.Errorf("team: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Team{}
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.Owner, &t.Epoch, &t.State, &t.Created, &t.Updated); err != nil {
			return nil, fmt.Errorf("team: list: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Create makes a new local team owned by self: epoch 1, state active,
// members = [self]. Nothing is sent. The name must not equal another active
// local team's name (ErrExists).
func (s *Store) Create(ctx context.Context, name string, now time.Time) (Team, error) {
	if !ValidName(name) {
		return Team{}, errors.New("team: invalid name")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, fmt.Errorf("team: create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams WHERE name = ? AND state = 'active'`, name).Scan(&n); err != nil {
		return Team{}, fmt.Errorf("team: create: %w", err)
	}
	if n > 0 {
		return Team{}, ErrExists
	}
	id := NewID()
	ts := stamp(now)
	if _, err := tx.ExecContext(ctx, `INSERT INTO teams (id, name, owner, epoch, state, created, updated) VALUES (?, ?, ?, 1, 'active', ?, ?)`,
		id, name, s.Self, ts, ts); err != nil {
		return Team{}, fmt.Errorf("team: create: %w", err)
	}
	if err := addMemberTx(ctx, tx, id, s.Self, now); err != nil {
		return Team{}, err
	}
	if err := tx.Commit(); err != nil {
		return Team{}, fmt.Errorf("team: create: %w", err)
	}
	s.audited(ctx, ActorCLI, ActionCreate, map[string]string{"team": id, "name": name})
	return Team{ID: id, Name: name, Owner: s.Self, Epoch: 1, State: StateActive, Created: ts, Updated: ts}, nil
}

func addMemberTx(ctx context.Context, tx *sql.Tx, teamID, key string, added time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO team_members (team_id, key, added) VALUES (?, ?, ?)`,
		teamID, key, added.UTC().Format(wireTimeFmt)); err != nil {
		return fmt.Errorf("team: add member: %w", err)
	}
	return nil
}

// AddMember is the owner op backing team.join and (later) team_invite
// completion: it bumps the epoch and adds key as a member. now is used both
// as members[].added and the epoch's updated stamp. It returns ErrNotFound,
// ErrNotOwner (team not owned by self), or ErrFull.
func (s *Store) AddMember(ctx context.Context, teamID, key string, now time.Time) (Team, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, fmt.Errorf("team: add member: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	t, err := s.addMemberOwnedTx(ctx, tx, teamID, key, now)
	if err != nil {
		return Team{}, err
	}
	if err := tx.Commit(); err != nil {
		return Team{}, fmt.Errorf("team: add member: %w", err)
	}
	s.audited(ctx, ActorDaemon, ActionMemberAdd, map[string]any{"team": teamID, "peer": key, "epoch": t.Epoch})
	s.changed()
	return t, nil
}

func (s *Store) addMemberOwnedTx(ctx context.Context, tx *sql.Tx, teamID, key string, now time.Time) (Team, error) {
	t, err := getTx(ctx, tx, teamID)
	if err != nil {
		return Team{}, err
	}
	if t.Owner != s.Self {
		return Team{}, ErrNotOwner
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_members WHERE team_id = ?`, teamID).Scan(&n); err != nil {
		return Team{}, fmt.Errorf("team: add member: %w", err)
	}
	if n >= MaxMembers {
		return Team{}, ErrFull
	}
	if err := addMemberTx(ctx, tx, teamID, key, now); err != nil {
		return Team{}, err
	}
	return s.bumpEpochTx(ctx, tx, t, now)
}

// bumpEpochTx increments t's epoch and stamps updated, inside tx.
func (s *Store) bumpEpochTx(ctx context.Context, tx *sql.Tx, t Team, now time.Time) (Team, error) {
	t.Epoch++
	ts := stamp(now)
	if _, err := tx.ExecContext(ctx, `UPDATE teams SET epoch = ?, updated = ? WHERE id = ?`, t.Epoch, ts, t.ID); err != nil {
		return Team{}, fmt.Errorf("team: bump epoch: %w", err)
	}
	t.Updated = ts
	return t, nil
}

// RemoveMember is the owner op backing team_remove (1.1c) and team.leave
// (member-initiated): it bumps the epoch and drops key from the roster. The
// owner itself may not be removed.
func (s *Store) RemoveMember(ctx context.Context, teamID, key string, now time.Time) (Team, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, fmt.Errorf("team: remove member: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	t, err := s.removeMemberOwnedTx(ctx, tx, teamID, key, now)
	if err != nil {
		return Team{}, err
	}
	if err := tx.Commit(); err != nil {
		return Team{}, fmt.Errorf("team: remove member: %w", err)
	}
	s.changed()
	return t, nil
}

func (s *Store) removeMemberOwnedTx(ctx context.Context, tx *sql.Tx, teamID, key string, now time.Time) (Team, error) {
	t, err := getTx(ctx, tx, teamID)
	if err != nil {
		return Team{}, err
	}
	if t.Owner != s.Self {
		return Team{}, ErrNotOwner
	}
	if key == t.Owner {
		return Team{}, ErrOwnerCannotLeave
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = ? AND key = ?`, teamID, key)
	if err != nil {
		return Team{}, fmt.Errorf("team: remove member: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Team{}, ErrNoSuchMember
	}
	return s.bumpEpochTx(ctx, tx, t, now)
}

// Rename changes the team's display name and bumps the epoch.
func (s *Store) Rename(ctx context.Context, teamID, name string, now time.Time) (Team, error) {
	if !ValidName(name) {
		return Team{}, errors.New("team: invalid name")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, fmt.Errorf("team: rename: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	t, err := getTx(ctx, tx, teamID)
	if err != nil {
		return Team{}, err
	}
	if t.Owner != s.Self {
		return Team{}, ErrNotOwner
	}
	t.Epoch++
	t.Name = name
	ts := stamp(now)
	if _, err := tx.ExecContext(ctx, `UPDATE teams SET name = ?, epoch = ?, updated = ? WHERE id = ?`, name, t.Epoch, ts, teamID); err != nil {
		return Team{}, fmt.Errorf("team: rename: %w", err)
	}
	t.Updated = ts
	if err := tx.Commit(); err != nil {
		return Team{}, fmt.Errorf("team: rename: %w", err)
	}
	s.audited(ctx, ActorCLI, ActionRename, map[string]any{"team": teamID, "name": name, "epoch": t.Epoch})
	return t, nil
}

// Delete dissolves the team: state = dissolved, epoch += 1. Local
// team_members rows are kept for display (team list --all).
func (s *Store) Delete(ctx context.Context, teamID string, now time.Time) (Team, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, fmt.Errorf("team: delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	t, err := getTx(ctx, tx, teamID)
	if err != nil {
		return Team{}, err
	}
	if t.Owner != s.Self {
		return Team{}, ErrNotOwner
	}
	t.Epoch++
	t.State = StateDissolved
	ts := stamp(now)
	if _, err := tx.ExecContext(ctx, `UPDATE teams SET state = 'dissolved', epoch = ?, updated = ? WHERE id = ?`, t.Epoch, ts, teamID); err != nil {
		return Team{}, fmt.Errorf("team: delete: %w", err)
	}
	t.Updated = ts
	if err := tx.Commit(); err != nil {
		return Team{}, fmt.Errorf("team: delete: %w", err)
	}
	s.audited(ctx, ActorCLI, ActionDelete, map[string]any{"team": teamID, "epoch": t.Epoch})
	s.changed()
	return t, nil
}

// Leave sets the local state of teamID to left (self is a non-owner member),
// and garbage-collects introduced peers that no longer share an active team.
// It does not send team.leave; the caller (1.1c) does that through the
// outbox after this returns. ErrOwnerCannotLeave if self owns the team.
func (s *Store) Leave(ctx context.Context, teamID string, now time.Time) (Team, []peers.Removed, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, nil, fmt.Errorf("team: leave: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	t, err := getTx(ctx, tx, teamID)
	if err != nil {
		return Team{}, nil, err
	}
	if t.Owner == s.Self {
		return Team{}, nil, ErrOwnerCannotLeave
	}
	t.State = StateLeft
	ts := stamp(now)
	if _, err := tx.ExecContext(ctx, `UPDATE teams SET state = 'left', updated = ? WHERE id = ?`, ts, teamID); err != nil {
		return Team{}, nil, fmt.Errorf("team: leave: %w", err)
	}
	t.Updated = ts
	removed, err := s.peers.GCIntroduced(ctx, tx)
	if err != nil {
		return Team{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Team{}, nil, fmt.Errorf("team: leave: %w", err)
	}
	s.audited(ctx, ActorCLI, ActionLeave, map[string]any{"team": teamID})
	s.auditRemoved(ctx, removed)
	s.changed()
	return t, removed, nil
}

func (s *Store) auditRemoved(ctx context.Context, removed []peers.Removed) {
	for _, r := range removed {
		s.audited(ctx, ActorDaemon, ActionPeerRemove, map[string]string{
			"peer": r.PublicKey, "name": r.Name, "fingerprint": r.Fingerprint, "reason": "team",
		})
	}
}

// OwnerRemoved handles `peers remove` of a key that owns local teams
// (Docs/protocol/team.md §Removing the owner as a peer): every such team with
// local state active is set to left, and introduced peers with no remaining
// active team are collected. It does not touch the peers table itself; the
// caller removes the owner's peer row separately. No team.leave is sent
// (there is no longer a mailbox key to send it to).
func (s *Store) OwnerRemoved(ctx context.Context, ownerKey string, now time.Time) ([]string, []peers.Removed, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("team: owner removed: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM teams WHERE owner = ? AND state = 'active'`, ownerKey)
	if err != nil {
		return nil, nil, fmt.Errorf("team: owner removed: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, nil, fmt.Errorf("team: owner removed: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, err
	}
	_ = rows.Close()
	ts := stamp(now)
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE teams SET state = 'left', updated = ? WHERE id = ?`, ts, id); err != nil {
			return nil, nil, fmt.Errorf("team: owner removed: %w", err)
		}
	}
	removed, err := s.peers.GCIntroduced(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("team: owner removed: %w", err)
	}
	for _, id := range ids {
		s.audited(ctx, ActorCLI, ActionLeave, map[string]any{"team": id, "reason": "owner_removed"})
	}
	s.auditRemoved(ctx, removed)
	if len(ids) > 0 {
		s.changed()
	}
	return ids, removed, nil
}

// Broadcast sends the current roster of teamID to every current member
// (owner's view) via Outbox. If includeRemoved is set, it also sends the
// owner-only roster to those keys (removal/dissolution).
func (s *Store) Broadcast(ctx context.Context, teamID string, includeRemoved []string) error {
	if s.Outbox == nil {
		return nil
	}
	full, ownerOnly, recipients, err := s.rosterSnapshot(ctx, teamID, len(includeRemoved) > 0)
	if err != nil {
		return err
	}
	var errs []error
	for _, key := range recipients {
		if _, err := s.Outbox.Submit(ctx, key, "team.roster", full); err != nil {
			errs = append(errs, fmt.Errorf("team: roster to %s: %w", key, err))
		}
	}
	for _, key := range includeRemoved {
		if _, err := s.Outbox.Submit(ctx, key, "team.roster", ownerOnly); err != nil {
			errs = append(errs, fmt.Errorf("team: roster to %s: %w", key, err))
		}
	}
	return errors.Join(errs...)
}

// rosterSnapshot reads team teamID in one read transaction and builds its full
// roster body, the owner-only body (if wantOwnerOnly), and the keys the full
// roster goes to, so every recipient gets the same epoch and member list. The
// transaction ends before the caller submits anything (the database has one
// connection). A member with no peers row (its peer entry was removed on the
// owner's side) can be neither described (no card) nor reached (no mailbox
// key); it is left out of the body and the recipients rather than failing the
// broadcast for every member.
func (s *Store) rosterSnapshot(ctx context.Context, teamID string, wantOwnerOnly bool) (full, ownerOnly map[string]any, recipients []string, err error) {
	// Announcement may write the database (key rotation): call it before the
	// read transaction takes the only connection.
	var selfMbox []byte
	if s.Announcement != nil {
		selfMbox, _ = s.Announcement()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("team: broadcast: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	t, err := getTx(ctx, tx, teamID)
	if err != nil {
		return nil, nil, nil, err
	}
	members, err := membersTx(ctx, tx, teamID)
	if err != nil {
		return nil, nil, nil, err
	}
	listed := make([]Member, 0, len(members))
	for _, m := range members {
		if m.Key != s.Self {
			var one int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM peers WHERE public_key = ?`, m.Key).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				s.log().Warn("team: member has no peer entry, left out of the roster", "event", "team_error", "team", teamID, "peer", m.Key)
				continue
			}
			if err != nil {
				return nil, nil, nil, fmt.Errorf("team: broadcast: %w", err)
			}
			recipients = append(recipients, m.Key)
		}
		listed = append(listed, m)
	}
	if full, err = s.rosterBody(ctx, tx, t, listed, true, selfMbox); err != nil {
		return nil, nil, nil, err
	}
	if wantOwnerOnly {
		if ownerOnly, err = s.rosterBody(ctx, tx, t, nil, false, selfMbox); err != nil {
			return nil, nil, nil, err
		}
	}
	return full, ownerOnly, recipients, nil
}

// rosterBody builds the wire body of a team.roster mail. full sends the
// complete membership (every member's card and mailbox); when full is false
// the roster carries only the owner (Docs/protocol/team.md: "a roster sent to
// a key that is not a member lists only the owner").
func (s *Store) rosterBody(ctx context.Context, q querier, t Team, members []Member, full bool, selfMbox []byte) (map[string]any, error) {
	list := members
	if !full {
		var added string
		err := q.QueryRowContext(ctx, `SELECT added FROM team_members WHERE team_id = ? AND key = ?`, t.ID, t.Owner).Scan(&added)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if ct, perr := time.Parse(storeTimeFmt, t.Created); perr == nil {
				added = ct.Format(wireTimeFmt)
			}
		case err != nil:
			return nil, fmt.Errorf("team: owner added: %w", err)
		}
		list = []Member{{Key: t.Owner, Added: added}}
	}
	out := make([]any, 0, len(list))
	for _, m := range list {
		entry, err := s.memberEntry(ctx, q, m, selfMbox)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	wireState := StateActive
	if t.State == StateDissolved {
		wireState = StateDissolved
	}
	return map[string]any{
		"team": map[string]any{
			"epoch":   json.Number(fmt.Sprintf("%d", t.Epoch)),
			"id":      t.ID,
			"members": out,
			"name":    t.Name,
			"owner":   t.Owner,
			"state":   wireState,
		},
	}, nil
}

func (s *Store) memberEntry(ctx context.Context, q querier, m Member, selfMbox []byte) (map[string]any, error) {
	var cardRaw, mboxRaw []byte
	var err error
	if m.Key == s.Self {
		if s.OwnCard == nil {
			return nil, errors.New("team: own card is not available")
		}
		if cardRaw, err = s.OwnCard(); err != nil {
			return nil, fmt.Errorf("team: own card: %w", err)
		}
		mboxRaw = selfMbox
	} else {
		var cardStr string
		if err := q.QueryRowContext(ctx, `SELECT card FROM peers WHERE public_key = ?`, m.Key).Scan(&cardStr); err != nil {
			return nil, fmt.Errorf("team: peer card for %s: %w", m.Key, err)
		}
		cardRaw = []byte(cardStr)
		var raw string
		if err := q.QueryRowContext(ctx, `SELECT mailbox_keys FROM peers WHERE public_key = ?`, m.Key).Scan(&raw); err == nil {
			var anns []json.RawMessage
			if json.Unmarshal([]byte(raw), &anns) == nil && len(anns) > 0 {
				mboxRaw = anns[0]
			}
		}
	}
	card, err := agentcard.ParseStrict(cardRaw)
	if err != nil {
		return nil, fmt.Errorf("team: card for %s: %w", m.Key, err)
	}
	var mbox any
	if mboxRaw != nil {
		if mbox, err = agentcard.ParseStrict(mboxRaw); err != nil {
			return nil, fmt.Errorf("team: mailbox for %s: %w", m.Key, err)
		}
	}
	return map[string]any{
		"added":   m.Added,
		"card":    card,
		"key":     m.Key,
		"mailbox": mbox,
	}, nil
}

// RecordInvite writes (or replaces) the team_invites row for lookup (owner
// side, Docs/protocol/team.md §Operations: Invite): a fresh 24 h TTL,
// unused. "A lookup is reused by the relay only after its pairing ends; if a
// new invite completes with a lookup still in team_invites, the old row is
// replaced" (team.md §Tables).
func (s *Store) RecordInvite(ctx context.Context, lookup, teamID, pairingID, peerKey string, now time.Time) error {
	if err := s.pruneInvites(ctx, now); err != nil {
		return err
	}
	created, expires := stamp(now), stamp(now.Add(inviteTTL))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO team_invites (lookup, team_id, pairing_id, peer_key, created, expires, used)
VALUES (?, ?, ?, ?, ?, ?, NULL)
ON CONFLICT (lookup) DO UPDATE SET team_id = excluded.team_id, pairing_id = excluded.pairing_id,
	peer_key = excluded.peer_key, created = excluded.created, expires = excluded.expires, used = NULL`,
		lookup, teamID, pairingID, peerKey, created, expires); err != nil {
		return fmt.Errorf("team: record invite: %w", err)
	}
	return nil
}

func (s *Store) pruneInvites(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM team_invites WHERE expires < ?`, stamp(now)); err != nil {
		return fmt.Errorf("team: prune invites: %w", err)
	}
	return nil
}

// RecordPendingJoin writes (or refreshes) the team_pending_joins row for
// (ownerKey, lookup) (joiner side, Docs/protocol/team.md §Operations: Join):
// a fresh 24 h TTL. Two invites from one owner can be pending at once (the
// primary key includes lookup).
func (s *Store) RecordPendingJoin(ctx context.Context, ownerKey, lookup string, now time.Time) error {
	if err := s.prunePendingJoins(ctx, now); err != nil {
		return err
	}
	created, expires := stamp(now), stamp(now.Add(inviteTTL))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO team_pending_joins (owner_key, lookup, created, expires)
VALUES (?, ?, ?, ?)
ON CONFLICT (owner_key, lookup) DO UPDATE SET created = excluded.created, expires = excluded.expires`,
		ownerKey, lookup, created, expires); err != nil {
		return fmt.Errorf("team: record pending join: %w", err)
	}
	return nil
}

func (s *Store) prunePendingJoins(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM team_pending_joins WHERE expires < ?`, stamp(now)); err != nil {
		return fmt.Errorf("team: prune pending joins: %w", err)
	}
	return nil
}

// ResyncMember resends the roster of teamID to a single peer: the full
// roster if it is a current member, otherwise the owner-only roster
// (Docs/protocol/presence.md §Receiving step 7). It is the presence sender's
// roster resync, one recipient at a time rather than Broadcast's fan-out.
func (s *Store) ResyncMember(ctx context.Context, teamID, peer string) error {
	if s.Outbox == nil {
		return nil
	}
	full, ownerOnly, recipients, err := s.rosterSnapshot(ctx, teamID, true)
	if err != nil {
		return err
	}
	body := ownerOnly
	for _, r := range recipients {
		if r == peer {
			body = full
			break
		}
	}
	if body == nil {
		return nil
	}
	_, err = s.Outbox.Submit(ctx, peer, "team.roster", body)
	return err
}
