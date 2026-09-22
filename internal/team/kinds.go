package team

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

// The mail kinds of Docs/protocol/team.md §Kinds. Apply and After share the
// per-message outcome through a map keyed by the *mail.Opened pointer, which
// is unique to and stable across one Handle call (Apply runs, then After runs
// on the same pointer), so this needs no message-identity comparison.

// maxEpoch is the exclusive upper bound of team.epoch (2^53).
const maxEpoch = int64(1) << 53

func badBody(msg string) error { return fmt.Errorf("team: %s: %w", msg, mail.ErrBadBody) }

func (s *Store) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// ---- team.roster (owner -> each member) ----

type parsedMember struct {
	key     string
	added   string
	card    *agentcard.Signed
	cardRaw []byte
	mboxRaw []byte // nil if absent or expired (check 5)
}

type rosterOutcome struct {
	ignored  bool
	reason   string // "" for the ignore-silently case (no audit at all)
	teamID   string
	epoch    int64
	added    []string
	removed  []string
	state    string
	newPeers []string
	gc       []peers.Removed
}

var pendingRoster sync.Map // map[*mail.Opened]*rosterOutcome

// RosterKind returns the receiver handler for kind team.roster.
func (s *Store) RosterKind() mail.Kind {
	return mail.Kind{Apply: s.applyRoster, After: s.afterRoster}
}

func (s *Store) applyRoster(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	now := s.now()
	teamID, name, ownerKey, epoch, wireState, members, selfIn, err := parseRosterBody(op.Msg.Body, s.Self, now)
	if err != nil {
		return err
	}
	if ownerKey != op.Msg.From {
		return badBody("team.owner must equal msg.from")
	}

	var trust string
	if err := tx.QueryRowContext(ctx, `SELECT trust FROM peers WHERE public_key = ?`, ownerKey).Scan(&trust); err != nil {
		return fmt.Errorf("team: roster: owner trust: %w", err)
	}
	if trust != peers.TrustCode && trust != peers.TrustFingerprint {
		pendingRoster.Store(op, &rosterOutcome{ignored: true, reason: "owner_trust"})
		return nil
	}

	// A pending join admits only an active roster that lists self; check that
	// before consuming the row, so a roster the joiner would ignore anyway
	// does not burn its invite.
	joinable := selfIn && wireState == StateActive
	existing, gerr := getTx(ctx, tx, teamID)
	isNewTeam := errors.Is(gerr, ErrNotFound)
	switch {
	case isNewTeam:
		ok := false
		if joinable {
			var perr error
			if ok, perr = consumePendingJoin(ctx, tx, ownerKey, now); perr != nil {
				return perr
			}
		}
		if !ok {
			pendingRoster.Store(op, &rosterOutcome{ignored: true, reason: "not_invited"})
			return nil
		}
	case gerr != nil:
		return fmt.Errorf("team: roster: %w", gerr)
	case existing.Owner != ownerKey:
		pendingRoster.Store(op, &rosterOutcome{ignored: true, reason: "not_owner", teamID: existing.ID})
		return nil
	case epoch <= existing.Epoch:
		return nil // idempotent resend or reordering: ignored silently, no audit
	case existing.State != StateActive:
		ok := false
		if joinable {
			var perr error
			if ok, perr = consumePendingJoin(ctx, tx, ownerKey, now); perr != nil {
				return perr
			}
		}
		if !ok {
			pendingRoster.Store(op, &rosterOutcome{ignored: true, reason: existing.State, teamID: existing.ID})
			return nil
		}
	}

	oldMembers, err := membersTx(ctx, tx, teamID)
	if err != nil {
		return fmt.Errorf("team: roster: %w", err)
	}
	oldSet := make(map[string]bool, len(oldMembers))
	for _, m := range oldMembers {
		oldSet[m.Key] = true
	}
	newSet := make(map[string]bool, len(members))
	for _, m := range members {
		newSet[m.key] = true
	}
	var added, removedKeys []string
	for k := range newSet {
		if !oldSet[k] {
			added = append(added, k)
		}
	}
	for k := range oldSet {
		if !newSet[k] {
			removedKeys = append(removedKeys, k)
		}
	}

	var localState string
	switch {
	case selfIn && wireState == StateActive:
		localState = StateActive
	case !selfIn:
		localState = StateRemoved
	default:
		localState = StateDissolved
	}

	ts := stamp(now)
	if isNewTeam {
		if _, err := tx.ExecContext(ctx, `INSERT INTO teams (id, name, owner, epoch, state, created, updated) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			teamID, name, ownerKey, epoch, localState, ts, ts); err != nil {
			return fmt.Errorf("team: roster: insert team: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE teams SET name = ?, epoch = ?, state = ?, updated = ? WHERE id = ?`,
			name, epoch, localState, ts, teamID); err != nil {
			return fmt.Errorf("team: roster: update team: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = ?`, teamID); err != nil {
		return fmt.Errorf("team: roster: replace members: %w", err)
	}
	for _, m := range members {
		if err := addMemberTx(ctx, tx, teamID, m.key, mustParseWireTime(m.added)); err != nil {
			return err
		}
	}

	var newPeers []string
	for _, m := range members {
		if m.key == s.Self {
			continue
		}
		var one int
		isNewPeer := errors.Is(tx.QueryRowContext(ctx, `SELECT 1 FROM peers WHERE public_key = ?`, m.key).Scan(&one), sql.ErrNoRows)
		if err := s.peers.Introduce(ctx, tx, peers.Member{Card: m.card, Raw: m.cardRaw, Mailbox: m.mboxRaw}, ownerKey, now); err != nil {
			return fmt.Errorf("team: roster: introduce %s: %w", m.key, err)
		}
		if isNewPeer && m.mboxRaw != nil {
			newPeers = append(newPeers, m.key)
		}
	}

	// Teams/team_members must be written before GC in this same transaction
	// (review 14), or GC deletes the peers just introduced above.
	gcRemoved, err := s.peers.GCIntroduced(ctx, tx)
	if err != nil {
		return fmt.Errorf("team: roster: gc: %w", err)
	}
	// A peer inserted above and collected again in the same transaction (a
	// roster that leaves self with no active team) must not get a keys push.
	if len(gcRemoved) > 0 && len(newPeers) > 0 {
		gone := make(map[string]bool, len(gcRemoved))
		for _, r := range gcRemoved {
			gone[r.PublicKey] = true
		}
		kept := newPeers[:0]
		for _, k := range newPeers {
			if !gone[k] {
				kept = append(kept, k)
			}
		}
		newPeers = kept
	}

	pendingRoster.Store(op, &rosterOutcome{
		teamID: teamID, epoch: epoch, added: added, removed: removedKeys, state: localState,
		newPeers: newPeers, gc: gcRemoved,
	})
	return nil
}

func (s *Store) afterRoster(ctx context.Context, op *mail.Opened) {
	v, ok := pendingRoster.LoadAndDelete(op)
	if !ok {
		return
	}
	o := v.(*rosterOutcome)
	if o.ignored {
		if o.reason == "" {
			return
		}
		detail := map[string]any{"peer": op.Msg.From, "reason": o.reason}
		if o.teamID != "" {
			detail["team"] = o.teamID
		}
		s.audited(ctx, ActorDaemon, ActionRosterIgnored, detail)
		return
	}
	s.audited(ctx, ActorDaemon, ActionRosterApply, map[string]any{
		"team": o.teamID, "epoch": o.epoch, "added": o.added, "removed": o.removed, "state": o.state,
	})
	s.auditRemoved(ctx, o.gc)
	if s.Outbox == nil || s.Announcement == nil {
		return
	}
	for _, peer := range o.newPeers {
		ann, err := s.Announcement()
		if err != nil || ann == nil {
			continue
		}
		if _, err := s.Outbox.Submit(ctx, peer, "keys", map[string]any{"announcement": json.RawMessage(ann)}); err != nil {
			s.log().Warn("team: keys push failed", "event", "team_error", "error", err)
		}
	}
}

// parseRosterBody validates the strict shape and field rules of team.roster
// (Docs/protocol/team.md), steps before the owner-trust and team-lookup
// checks. Every failure here is mail.ErrBadBody.
func parseRosterBody(body map[string]any, self string, now time.Time) (teamID, name, owner string, epoch int64, state string, members []parsedMember, selfIn bool, err error) {
	if len(body) != 1 {
		return "", "", "", 0, "", nil, false, badBody("roster body must hold exactly team")
	}
	teamField, ok := body["team"].(map[string]any)
	if !ok {
		return "", "", "", 0, "", nil, false, badBody("roster team must be an object")
	}
	if len(teamField) != 6 {
		return "", "", "", 0, "", nil, false, badBody("roster team must hold exactly epoch, id, members, name, owner, state")
	}
	teamID, ok = teamField["id"].(string)
	if !ok || !ValidID(teamID) {
		return "", "", "", 0, "", nil, false, badBody("roster team.id")
	}
	name, ok = teamField["name"].(string)
	if !ok || !ValidName(name) {
		return "", "", "", 0, "", nil, false, badBody("roster team.name")
	}
	owner, ok = teamField["owner"].(string)
	if !ok {
		return "", "", "", 0, "", nil, false, badBody("roster team.owner")
	}
	if _, kerr := envelope.ParseKey(owner); kerr != nil {
		return "", "", "", 0, "", nil, false, badBody("roster team.owner: " + kerr.Error())
	}
	n, ok := teamField["epoch"].(json.Number)
	if !ok {
		return "", "", "", 0, "", nil, false, badBody("roster team.epoch")
	}
	epoch, everr := n.Int64()
	if everr != nil || epoch < 1 || epoch >= maxEpoch {
		return "", "", "", 0, "", nil, false, badBody("roster team.epoch out of range")
	}
	state, ok = teamField["state"].(string)
	if !ok || (state != StateActive && state != StateDissolved) {
		return "", "", "", 0, "", nil, false, badBody("roster team.state")
	}
	rawMembers, ok := teamField["members"].([]any)
	if !ok {
		return "", "", "", 0, "", nil, false, badBody("roster team.members")
	}
	maxLen, minLen := MaxMembers, 1
	if state == StateDissolved {
		minLen = 0
	}
	if len(rawMembers) < minLen || len(rawMembers) > maxLen {
		return "", "", "", 0, "", nil, false, badBody("roster team.members count")
	}
	seen := map[string]bool{}
	members = make([]parsedMember, 0, len(rawMembers))
	for _, raw := range rawMembers {
		m, merr := parseRosterMember(raw, now)
		if merr != nil {
			return "", "", "", 0, "", nil, false, merr
		}
		if seen[m.key] {
			return "", "", "", 0, "", nil, false, badBody("roster duplicate member key")
		}
		seen[m.key] = true
		members = append(members, m)
	}
	if state == StateActive && !seen[owner] {
		return "", "", "", 0, "", nil, false, badBody("roster active team must include the owner")
	}
	return teamID, name, owner, epoch, state, members, seen[self], nil
}

func parseRosterMember(raw any, now time.Time) (parsedMember, error) {
	entry, ok := raw.(map[string]any)
	if !ok || len(entry) != 4 {
		return parsedMember{}, badBody("roster member must hold exactly added, card, key, mailbox")
	}
	key, ok := entry["key"].(string)
	if !ok {
		return parsedMember{}, badBody("roster member.key")
	}
	if _, err := envelope.ParseKey(key); err != nil {
		return parsedMember{}, badBody("roster member.key: " + err.Error())
	}
	added, ok := entry["added"].(string)
	if !ok {
		return parsedMember{}, badBody("roster member.added")
	}
	if _, err := parseWireTime(added); err != nil {
		return parsedMember{}, badBody("roster member.added: " + err.Error())
	}
	cardRaw, err := agentcard.CanonicalValue(entry["card"])
	if err != nil {
		return parsedMember{}, badBody("roster member.card: " + err.Error())
	}
	sc, err := agentcard.Verify(cardRaw)
	if err != nil {
		return parsedMember{}, badBody("roster member.card: " + err.Error())
	}
	if sc.Card.PublicKey != key {
		return parsedMember{}, badBody("roster member.card.public_key does not match key")
	}
	var mboxRaw []byte
	if entry["mailbox"] != nil {
		canon, cerr := agentcard.CanonicalValue(entry["mailbox"])
		if cerr != nil {
			return parsedMember{}, badBody("roster member.mailbox: " + cerr.Error())
		}
		_, raw, perr := mail.ParseAnnouncement(canon, key, now)
		switch {
		case perr == nil:
			mboxRaw = raw
		case errors.Is(perr, mail.ErrAnnouncementExpired):
			mboxRaw = nil // check 5 failure: the owner's copy may be old, treated as null
		default:
			return parsedMember{}, badBody("roster member.mailbox: " + perr.Error())
		}
	}
	return parsedMember{key: key, added: added, card: sc, cardRaw: cardRaw, mboxRaw: mboxRaw}, nil
}

func mustParseWireTime(s string) time.Time {
	t, _ := parseWireTime(s)
	return t
}

func parseWireTime(s string) (time.Time, error) {
	t, err := time.Parse(wireTimeFmt, s)
	if err != nil || t.Format(wireTimeFmt) != s {
		return time.Time{}, errors.New("must be RFC 3339 UTC with Z, whole seconds")
	}
	return t, nil
}

// consumePendingJoin deletes the oldest live team_pending_joins row for
// owner, reporting whether one existed.
func consumePendingJoin(ctx context.Context, tx *sql.Tx, owner string, now time.Time) (bool, error) {
	var lookup string
	err := tx.QueryRowContext(ctx,
		`SELECT lookup FROM team_pending_joins WHERE owner_key = ? AND expires > ? ORDER BY created LIMIT 1`,
		owner, stamp(now)).Scan(&lookup)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("team: pending join: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_pending_joins WHERE owner_key = ? AND lookup = ?`, owner, lookup); err != nil {
		return false, fmt.Errorf("team: pending join: %w", err)
	}
	return true, nil
}

// ---- team.join (joiner -> owner) ----

type joinOutcome struct {
	succeeded bool
	reason    string
	teamID    string
	peer      string
	epoch     int64
}

var pendingJoin sync.Map // map[*mail.Opened]*joinOutcome

// JoinKind returns the receiver handler for kind team.join.
func (s *Store) JoinKind() mail.Kind {
	return mail.Kind{Apply: s.applyJoin, After: s.afterJoin}
}

func (s *Store) applyJoin(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	if len(op.Msg.Body) != 1 {
		return badBody("join body must hold exactly lookup")
	}
	raw, ok := op.Msg.Body["lookup"].(string)
	if !ok {
		return badBody("join lookup")
	}
	lookup, ok := envelope.NormalizePairLookup(raw)
	if !ok {
		return badBody("join lookup format")
	}
	peer := op.Msg.From
	now := s.now()

	var teamID string
	err := tx.QueryRowContext(ctx,
		`SELECT team_id FROM team_invites WHERE lookup = ? AND peer_key = ? AND used IS NULL AND expires > ?`,
		lookup, peer, stamp(now)).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		pendingJoin.Store(op, &joinOutcome{reason: "no_invite", peer: peer})
		return nil
	}
	if err != nil {
		return fmt.Errorf("team: join: %w", err)
	}

	t, gerr := getTx(ctx, tx, teamID)
	if gerr != nil && !errors.Is(gerr, ErrNotFound) {
		return fmt.Errorf("team: join: %w", gerr)
	}
	reason := ""
	already := false
	switch {
	case gerr != nil, t.State != StateActive, t.Owner != s.Self:
		reason = "inactive"
	default:
		var n, mine int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(key = ?), 0) FROM team_members WHERE team_id = ?`, peer, teamID).Scan(&n, &mine); err != nil {
			return fmt.Errorf("team: join: %w", err)
		}
		switch {
		case mine > 0:
			already = true
		case n >= MaxMembers:
			reason = "team_full"
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE team_invites SET used = ? WHERE lookup = ?`, stamp(now), lookup); err != nil {
		return fmt.Errorf("team: join: mark used: %w", err)
	}
	if reason != "" {
		pendingJoin.Store(op, &joinOutcome{reason: reason, peer: peer, teamID: teamID})
		return nil
	}
	var nt Team
	if already {
		// Re-invited while still on the owner's roster (e.g. its team.leave has
		// not arrived): a plain INSERT would fail the primary key on every
		// resend. Bump the epoch instead, so the roster that follows is newer
		// than the joiner's copy and its pending join admits it.
		nt, err = s.bumpEpochTx(ctx, tx, t, now)
	} else {
		nt, err = s.addMemberOwnedTx(ctx, tx, teamID, peer, now)
	}
	if err != nil {
		return fmt.Errorf("team: join: add member: %w", err)
	}
	pendingJoin.Store(op, &joinOutcome{succeeded: true, teamID: nt.ID, peer: peer, epoch: nt.Epoch})
	return nil
}

func (s *Store) afterJoin(ctx context.Context, op *mail.Opened) {
	v, ok := pendingJoin.LoadAndDelete(op)
	if !ok {
		return
	}
	o := v.(*joinOutcome)
	if !o.succeeded {
		detail := map[string]any{"peer": o.peer, "reason": o.reason}
		if o.teamID != "" {
			detail["team"] = o.teamID
		}
		s.audited(ctx, ActorDaemon, ActionJoinIgnored, detail)
		return
	}
	s.audited(ctx, ActorDaemon, ActionMemberAdd, map[string]any{"team": o.teamID, "peer": o.peer, "epoch": o.epoch})
	if err := s.Broadcast(ctx, o.teamID, nil); err != nil {
		s.log().Warn("team: roster broadcast failed", "event", "team_error", "error", err)
	}
}

// ---- team.leave (member -> owner) ----

type leaveOutcome struct {
	succeeded bool
	teamID    string
	peer      string
	epoch     int64
}

var pendingLeave sync.Map // map[*mail.Opened]*leaveOutcome

// LeaveKind returns the receiver handler for kind team.leave.
func (s *Store) LeaveKind() mail.Kind {
	return mail.Kind{Apply: s.applyLeave, After: s.afterLeave}
}

func (s *Store) applyLeave(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	if len(op.Msg.Body) != 1 {
		return badBody("leave body must hold exactly team")
	}
	teamID, ok := op.Msg.Body["team"].(string)
	if !ok || !ValidID(teamID) {
		return badBody("leave team")
	}
	peer := op.Msg.From
	now := s.now()

	t, err := getTx(ctx, tx, teamID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("team: leave: %w", err)
	}
	if errors.Is(err, ErrNotFound) || t.State != StateActive || t.Owner != s.Self || peer == t.Owner {
		pendingLeave.Store(op, &leaveOutcome{teamID: teamID, peer: peer})
		return nil
	}
	nt, rerr := s.removeMemberOwnedTx(ctx, tx, teamID, peer, now)
	if rerr != nil {
		if errors.Is(rerr, ErrNoSuchMember) {
			pendingLeave.Store(op, &leaveOutcome{teamID: teamID, peer: peer})
			return nil
		}
		return fmt.Errorf("team: leave: %w", rerr)
	}
	pendingLeave.Store(op, &leaveOutcome{succeeded: true, teamID: nt.ID, peer: peer, epoch: nt.Epoch})
	return nil
}

func (s *Store) afterLeave(ctx context.Context, op *mail.Opened) {
	v, ok := pendingLeave.LoadAndDelete(op)
	if !ok {
		return
	}
	o := v.(*leaveOutcome)
	if !o.succeeded {
		s.audited(ctx, ActorDaemon, ActionLeaveIgnored, map[string]any{"team": o.teamID, "peer": o.peer})
		return
	}
	s.audited(ctx, ActorDaemon, ActionMemberLeave, map[string]any{"team": o.teamID, "peer": o.peer, "epoch": o.epoch})
	if err := s.Broadcast(ctx, o.teamID, nil); err != nil {
		s.log().Warn("team: roster broadcast failed", "event", "team_error", "error", err)
	}
}
