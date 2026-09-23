package capability

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Direction values of a grants row (Docs/protocol/grant.md §Tables).
const (
	DirectionIssued = "issued" // this daemon is the grantor
	DirectionHeld   = "held"   // this daemon is the holder
)

// State values of a grants row.
const (
	StatePendingApproval = "pending_approval"
	StateActive          = "active"
	StateRevoked         = "revoked"
)

// Revoke reasons.
const (
	ReasonUser          = "user"
	ReasonSessionClosed = "session_closed"
	ReasonPeerRemoved   = "peer_removed"
)

const storeTimeFmt = "2006-01-02T15:04:05.000Z"

// ErrUnknownGrant is returned when a grant id does not exist.
var ErrUnknownGrant = errors.New("capability: unknown grant")

// Record is one grants row (Docs/protocol/grant.md §Tables). Path is set
// only for issued (grantor-side) rows: the local path is never sent to the
// holder and never appears on the held side.
type Record struct {
	ID        string
	Direction string
	Peer      string
	Session   string
	Action    string
	Label     string
	Path      string
	Branch    string
	Scope     string
	Sensitive bool
	Nbf       time.Time
	Exp       time.Time
	Token     string // canonical(token), the wire form
	State     string
	Approval  string
	Policy    string
	RevokedAt time.Time
	Reason    string
	Created   time.Time
	Updated   time.Time
}

// Store owns the grants table (migration 16) and the grant_policies table
// (migration 15, extended by migration 16).
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// NewGrantID returns "g-" plus 32 lowercase hex characters (also exported as
// NewID in token.go; kept here as an alias for readability at call sites
// that only import the store half).
func NewGrantID() string { return NewID() }

func fmtTime(t time.Time) string { return t.UTC().Format(storeTimeFmt) }

func parseStoreTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(storeTimeFmt, s)
	return t
}

// grantColumns is the column list shared by every SELECT against grants, in
// scanGrant's order.
const grantColumns = `id, direction, peer, session, action, label, path, branch, scope,
	sensitive, nbf, exp, token, state, approval, policy, revoked_at, reason, created, updated`

type grantScanner interface{ Scan(dest ...any) error }

func scanGrant(sc grantScanner) (Record, error) {
	var r Record
	var path, branch, scope, approval, policy, revokedAt, reason sql.NullString
	var nbf, exp, created, updated string
	var sensitive int
	err := sc.Scan(&r.ID, &r.Direction, &r.Peer, &r.Session, &r.Action, &r.Label, &path, &branch, &scope,
		&sensitive, &nbf, &exp, &r.Token, &r.State, &approval, &policy, &revokedAt, &reason, &created, &updated)
	if err != nil {
		return Record{}, err
	}
	r.Path = path.String
	r.Branch = branch.String
	r.Scope = scope.String
	r.Sensitive = sensitive != 0
	r.Nbf = parseStoreTime(nbf)
	r.Exp = parseStoreTime(exp)
	r.Approval = approval.String
	r.Policy = policy.String
	r.RevokedAt = parseStoreTime(revokedAt.String)
	r.Reason = reason.String
	r.Created = parseStoreTime(created)
	r.Updated = parseStoreTime(updated)
	return r, nil
}

// execer is satisfied by both *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// InsertPending inserts rec as a pending_approval row, outside any
// transaction (the approval it waits on, if any, is created next by the
// caller; approval.Store.Create is not itself transactional). If the
// caller's subsequent approval.Create fails, it must call Delete to drop
// this row (Docs/review/26-2.2a-review.md N5).
func (s *Store) InsertPending(ctx context.Context, rec Record) error {
	rec.State = StatePendingApproval
	return s.insert(ctx, s.DB, rec)
}

// InsertActiveTx inserts rec directly as active, inside tx (used when a
// policy matches at issuance: no approval is needed,
// Docs/protocol/grant.md §Issuance step 7).
func (s *Store) InsertActiveTx(ctx context.Context, tx *sql.Tx, rec Record) error {
	rec.State = StateActive
	return s.insert(ctx, tx, rec)
}

// InsertHeldTx inserts a held (holder-side) row, active, inside tx (the
// grant mail apply, Docs/protocol/grant.md §Kinds).
func (s *Store) InsertHeldTx(ctx context.Context, tx *sql.Tx, rec Record) error {
	rec.Direction = DirectionHeld
	rec.State = StateActive
	return s.insert(ctx, tx, rec)
}

func (s *Store) insert(ctx context.Context, ex execer, rec Record) error {
	now := s.now()
	if rec.Created.IsZero() {
		rec.Created = now
	}
	rec.Updated = now
	sensitive := 0
	if rec.Sensitive {
		sensitive = 1
	}
	_, err := ex.ExecContext(ctx, `
INSERT INTO grants (id, direction, peer, session, action, label, path, branch, scope, sensitive,
	nbf, exp, token, state, approval, policy, revoked_at, reason, created, updated)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.Direction, rec.Peer, rec.Session, rec.Action, rec.Label,
		nullIfEmpty(rec.Path), nullIfEmpty(rec.Branch), nullIfEmpty(rec.Scope), sensitive,
		fmtTime(rec.Nbf), fmtTime(rec.Exp), rec.Token, rec.State, nullIfEmpty(rec.Approval), nullIfEmpty(rec.Policy),
		nil, nil, fmtTime(rec.Created), fmtTime(rec.Updated))
	if err != nil {
		return fmt.Errorf("capability: insert grant: %w", err)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Delete removes a grant row outright. Used only to undo InsertPending when
// the approval that would have owned it could not be created.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM grants WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("capability: delete grant: %w", err)
	}
	return nil
}

// Get reads one grant by id.
func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+grantColumns+` FROM grants WHERE id = ?`, id)
	r, err := scanGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrUnknownGrant
	}
	if err != nil {
		return Record{}, fmt.Errorf("capability: get grant: %w", err)
	}
	return r, nil
}

// GetTx is Get, read through tx (for use inside an approval Precondition or
// another caller-owned transaction; never touch DB outside the given tx from
// inside one, Docs/review/27-2.1a-review.md C1).
func (s *Store) GetTx(ctx context.Context, tx *sql.Tx, id string) (Record, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+grantColumns+` FROM grants WHERE id = ?`, id)
	r, err := scanGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrUnknownGrant
	}
	if err != nil {
		return Record{}, fmt.Errorf("capability: get grant: %w", err)
	}
	return r, nil
}

// ListFilter narrows List.
type ListFilter struct {
	Session   string // "" = any
	Direction string // "" = any
	State     string // "" = any
}

// List returns grants matching f, newest first.
func (s *Store) List(ctx context.Context, f ListFilter) ([]Record, error) {
	q := `SELECT ` + grantColumns + ` FROM grants WHERE 1=1`
	var args []any
	if f.Session != "" {
		q += ` AND session = ?`
		args = append(args, f.Session)
	}
	if f.Direction != "" {
		q += ` AND direction = ?`
		args = append(args, f.Direction)
	}
	if f.State != "" {
		q += ` AND state = ?`
		args = append(args, f.State)
	}
	q += ` ORDER BY created DESC`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("capability: list grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Record
	for rows.Next() {
		r, err := scanGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("capability: scan grant: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ActivateTx marks a pending_approval row active, inside tx (the approval
// Perform step, Docs/protocol/grant.md §Issuance step 7). It returns the
// updated record; the caller sends the grant mail itself (Outbox.SubmitTx)
// so it can build the mail body from the token.
func (s *Store) ActivateTx(ctx context.Context, tx *sql.Tx, id string, now time.Time) (Record, error) {
	rec, err := s.GetTx(ctx, tx, id)
	if err != nil {
		return Record{}, err
	}
	if rec.State != StatePendingApproval {
		return Record{}, fmt.Errorf("capability: grant %s is %s, not pending_approval", id, rec.State)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE grants SET state = ?, updated = ? WHERE id = ?`, StateActive, fmtTime(now), id); err != nil {
		return Record{}, fmt.Errorf("capability: activate grant: %w", err)
	}
	rec.State = StateActive
	rec.Updated = now
	return rec, nil
}

// RevokeTx marks one grant revoked inside tx, idempotent (a second revoke of
// an already-revoked row is a no-op, not an error). Returns whether the
// state actually changed here.
func (s *Store) RevokeTx(ctx context.Context, tx *sql.Tx, id, reason string, now time.Time) (changed bool, err error) {
	res, err := tx.ExecContext(ctx, `UPDATE grants SET state = ?, revoked_at = ?, reason = ?, updated = ?
WHERE id = ? AND state <> ?`, StateRevoked, fmtTime(now), reason, fmtTime(now), id, StateRevoked)
	if err != nil {
		return false, fmt.Errorf("capability: revoke grant: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RevokeForSessionTx revokes every non-revoked grant of session sid inside
// tx (Docs/protocol/grant.md §Session end: "all its grants end in the same
// transaction ... including rows still pending_approval"). It returns the
// ids it revoked, for the caller to audit.
func (s *Store) RevokeForSessionTx(ctx context.Context, tx *sql.Tx, sid, reason string, now time.Time) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM grants WHERE session = ? AND state <> ?`, sid, StateRevoked)
	if err != nil {
		return nil, fmt.Errorf("capability: list session grants: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	for _, id := range ids {
		if _, err := s.RevokeTx(ctx, tx, id, reason, now); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// RevokeForPeer revokes every non-revoked grant with this peer, in its own
// transaction (Docs/protocol/grant.md, "peers remove of the holder revokes
// all its grants"). Not nested in another transaction: called from the
// peers.Store.OnRemoved hook, itself run after the peer row's own
// transaction has committed.
func (s *Store) RevokeForPeer(ctx context.Context, peer, reason string, now time.Time) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("capability: begin revoke for peer: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM grants WHERE peer = ? AND state <> ?`, peer, StateRevoked)
	if err != nil {
		return nil, fmt.Errorf("capability: list peer grants: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	for _, id := range ids {
		if _, err := s.RevokeTx(ctx, tx, id, reason, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("capability: commit revoke for peer: %w", err)
	}
	return ids, nil
}

// FindHeldTx looks up a held row by (id, peer) for the grant.revoke apply
// path: "find the held row with this id and peer = msg.from" (Docs/protocol/
// grant.md §Kinds). ok is false for an unknown id or a row held from another
// grantor, which must leave nothing changed.
func (s *Store) FindHeldTx(ctx context.Context, tx *sql.Tx, id, peer string) (rec Record, ok bool, err error) {
	rec, err = s.GetTx(ctx, tx, id)
	if errors.Is(err, ErrUnknownGrant) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	if rec.Direction != DirectionHeld || rec.Peer != peer {
		return Record{}, false, nil
	}
	return rec, true, nil
}

// ---- Policies (grant_policies, migration 15 extended by migration 16) ----

// Policy is one grant_policies row (Docs/protocol/grant.md §Policies).
type Policy struct {
	ID          string
	Peer        string
	Action      string
	Path        string // resolved local path
	Branch      string // git.read only
	Scope       string // "" = the whole resource
	Public      bool
	MaxExpiresS int
	Until       time.Time
	Approval    string
	Created     time.Time
}

// MaxPolicies is the cap of Docs/protocol/grant.md §Policies.
const MaxPolicies = 50

// PolicyCount returns how many policies exist (expired ones included; they
// are pruned lazily by PolicyList/PolicyMatch).
func (s *Store) PolicyCount(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM grant_policies`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("capability: count policies: %w", err)
	}
	return n, nil
}

const policyColumns = `id, peer, action, path, branch, scope, public, max_ttl, until, approval, created`

func scanPolicy(sc grantScanner) (Policy, error) {
	var p Policy
	var branch, scope, until, approval sql.NullString
	var maxTTL sql.NullInt64
	var public int
	var created string
	err := sc.Scan(&p.ID, &p.Peer, &p.Action, &p.Path, &branch, &scope, &public, &maxTTL, &until, &approval, &created)
	if err != nil {
		return Policy{}, err
	}
	p.Branch = branch.String
	p.Scope = scope.String
	p.Public = public != 0
	p.MaxExpiresS = int(maxTTL.Int64)
	p.Until = parseStoreTime(until.String)
	p.Approval = approval.String
	p.Created = parseStoreTime(created)
	return p, nil
}

// PolicyInsertTx inserts a new policy inside tx (adding a policy needs a
// human approval, Docs/protocol/grant.md §Policies, so this is called from
// the approval Action's Perform).
func (s *Store) PolicyInsertTx(ctx context.Context, tx *sql.Tx, p Policy) error {
	public := 0
	if p.Public {
		public = 1
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO grant_policies (id, peer, action, path, branch, scope, public, max_ttl, until, approval, created)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		// scope is NOT NULL on this table (migration 15 predates the NULL =
		// "whole resource" convention grants.scope uses): "" already means
		// the whole resource here, so it is stored as-is, not NULL.
		p.ID, p.Peer, p.Action, p.Path, nullIfEmpty(p.Branch), p.Scope, public,
		p.MaxExpiresS, fmtTime(p.Until), p.Approval, fmtTime(p.Created))
	if err != nil {
		return fmt.Errorf("capability: insert policy: %w", err)
	}
	return nil
}

// PolicyList returns every non-expired policy, oldest first, pruning expired
// ones first (Docs/protocol/grant.md §Policies, "an ended policy matches
// nothing and is pruned").
func (s *Store) PolicyList(ctx context.Context) ([]Policy, error) {
	if err := s.pruneExpiredPolicies(ctx); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+policyColumns+` FROM grant_policies ORDER BY created`)
	if err != nil {
		return nil, fmt.Errorf("capability: list policies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Policy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("capability: scan policy: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) pruneExpiredPolicies(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM grant_policies WHERE until < ?`, fmtTime(s.now()))
	if err != nil {
		return fmt.Errorf("capability: prune policies: %w", err)
	}
	return nil
}

// ErrUnknownPolicy is returned when a policy id does not exist.
var ErrUnknownPolicy = errors.New("capability: unknown policy")

// PolicyDelete removes a policy. Removing a policy needs no approval
// (Docs/protocol/grant.md §Policies).
func (s *Store) PolicyDelete(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM grant_policies WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("capability: delete policy: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUnknownPolicy
	}
	return nil
}

// PolicyDeleteForPeerTx removes every policy of peer inside tx (Docs/protocol/
// grant.md §Policies, "Policies also end with the peer (peers remove deletes
// them)").
func (s *Store) PolicyDeleteForPeerTx(ctx context.Context, tx *sql.Tx, peer string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM grant_policies WHERE peer = ?`, peer)
	if err != nil {
		return fmt.Errorf("capability: delete peer policies: %w", err)
	}
	return nil
}

// MatchParams describes a would-be grant for policy matching.
type MatchParams struct {
	Peer      string
	Action    string
	Path      string // resolved local path
	Branch    string
	Scope     string // "" = whole resource
	Sensitive bool
	ExpiresS  int
	Now       time.Time
}

// Match returns the first policy that covers mp (Docs/protocol/grant.md
// §Policies): same peer, action, exact resolved path, exact branch (for
// git.read), the grant's scope inside the policy's scope by segments, the
// policy's public flag consistent with mp.Sensitive ("--public in the policy
// covers only --public grants"), the grant's expiry within the policy's cap,
// and the policy not past its until. Expired policies are pruned first.
func (s *Store) Match(ctx context.Context, mp MatchParams) (*Policy, error) {
	if err := s.pruneExpiredPolicies(ctx); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+policyColumns+` FROM grant_policies WHERE peer = ? AND action = ? ORDER BY created`, mp.Peer, mp.Action)
	if err != nil {
		return nil, fmt.Errorf("capability: match policies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("capability: scan policy: %w", err)
		}
		if policyCovers(p, mp) {
			return &p, nil
		}
	}
	return nil, rows.Err()
}

func policyCovers(p Policy, mp MatchParams) bool {
	if p.Path != mp.Path {
		return false
	}
	if mp.Action == ActionGitRead && p.Branch != mp.Branch {
		return false
	}
	if !scopeWithin(mp.Scope, p.Scope) {
		return false
	}
	// "--public in the policy covers only --public grants": a public policy
	// matches only a non-sensitive grant; a policy without --public covers
	// any sensitivity (grant.md: "sensitive grants may be covered by a
	// policy").
	if p.Public && mp.Sensitive {
		return false
	}
	if mp.ExpiresS > p.MaxExpiresS {
		return false
	}
	if !mp.Now.Before(p.Until) {
		return false
	}
	return true
}

// scopeWithin reports whether child is inside (or equal to) parent, by path
// segments, never by string prefix (Docs/protocol/grant.md §Paths). "" means
// the whole resource, which only "" is inside.
func scopeWithin(child, parent string) bool {
	if parent == "" {
		return true // "" means the whole resource: everything is inside it
	}
	if child == "" {
		return false // child is the whole resource, wider than a scoped policy
	}
	pseg := strings.Split(parent, "/")
	cseg := strings.Split(child, "/")
	if len(cseg) < len(pseg) {
		return false
	}
	for i, s := range pseg {
		if cseg[i] != s {
			return false
		}
	}
	return true
}

// NewPolicyID returns "p-" plus 32 lowercase hex characters.
func NewPolicyID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "p-" + hex.EncodeToString(b)
}
