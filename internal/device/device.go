// Package device stores the own-device link of Docs/protocol/device.md (owner
// decision D13): the device_links, device_scopes and device_offers tables, the
// link id, the one-way hierarchy rules and the activation rule.
//
// device_links is the "device" trust. It is written only by the device link
// handlers (internal/daemon/device.go); no team, roster, pairing or peers
// verify code path touches it. Every method that changes state takes the
// caller's *sql.Tx, so the caller commits it together with its outbox rows and
// audit rows and never touches another connection inside the transaction
// (Docs/review/26-2.2a-review.md N1, Docs/review/27-2.1a-review.md C1).
package device

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Roles of this device in a link (Docs/protocol/device.md §Model).
const (
	RoleController = "controller"
	RoleHelper     = "helper"
)

// Link states (Docs/protocol/device.md §Tables). An intent is a link that is
// pending_approval or waiting; it becomes active when the peer's
// complementary offer is held too.
const (
	StatePendingApproval = "pending_approval"
	StateWaiting         = "waiting"
	StateActive          = "active"
	StateRevoked         = "revoked"
)

// Mail kinds (Docs/protocol/device.md §Kinds).
const (
	KindLink   = "device.link"
	KindUnlink = "device.unlink"
)

// IntentTTL is how long an intent, a pending approval and a kept offer live
// (Docs/protocol/device.md §Link flow).
const IntentTTL = 10 * time.Minute

// MaxHelpers is how many helpers a controller may have; a helper has one
// controller (Docs/protocol/device.md §One-way hierarchy).
const MaxHelpers = 8

const (
	linkIDDomain = "dorylinae-device-link-v1\n"
	// NonceLen is the length of a nonce in bytes (32 hex characters).
	NonceLen  = 16
	timeFmt   = "2006-01-02T15:04:05.000Z"
	linkPfx   = "l-"
	intentPfx = "i-"
)

// Errors of the hierarchy rules, mapped to IPC codes by the daemon.
var (
	// ErrCycle: the link would reverse an existing one or make a chain
	// (device_cycle).
	ErrCycle = errors.New("device: link would make a cycle or a chain")
	// ErrAlreadyLinked: a link or an intent with this peer already exists in the
	// same role.
	ErrAlreadyLinked = errors.New("device: a link with this peer already exists")
	// ErrLimit: a helper already has a controller, or a controller already has
	// MaxHelpers helpers.
	ErrLimit = errors.New("device: link limit reached")
	// ErrUnknownLink: no such link.
	ErrUnknownLink = errors.New("device: unknown link")
)

// Link is one row of device_links.
type Link struct {
	ID          string
	Peer        string
	Role        string // this device's role
	State       string
	Nonce       string
	PeerNonce   string
	Approval    string
	Created     time.Time
	Expires     time.Time // intents only
	ActivatedAt time.Time
	RevokedAt   time.Time
	Updated     time.Time
}

// Offer is a verified device.link body (Docs/protocol/device.md §Kinds).
type Offer struct {
	At         time.Time
	Controller string
	Helper     string
	Nonce      string
	Role       string // the sender's role
}

// ComplementaryRole returns the other role.
func ComplementaryRole(role string) string {
	if role == RoleController {
		return RoleHelper
	}
	return RoleController
}

// ValidRole reports whether role is controller or helper.
func ValidRole(role string) bool { return role == RoleController || role == RoleHelper }

// LinkID is the id both devices compute when a link becomes active:
// "l-" || hex(SHA-256("dorylinae-device-link-v1\n" || controller || "\n" ||
// helper || "\n" || nonce_c || "\n" || nonce_h)[0:16]), keys in wire form and
// nonces as their ASCII hex strings (Docs/protocol/device.md §Link flow).
func LinkID(controller, helper, nonceC, nonceH string) string {
	h := sha256.New()
	h.Write([]byte(linkIDDomain))
	h.Write([]byte(controller + "\n" + helper + "\n" + nonceC + "\n" + nonceH))
	return linkPfx + hex.EncodeToString(h.Sum(nil)[:16])
}

// NewNonce returns a random 16-byte nonce as 32 lowercase hex characters.
func NewNonce() string {
	var b [NonceLen]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// NewIntentID returns a fresh intent id, "i-" plus 32 hex characters.
func NewIntentID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return intentPfx + hex.EncodeToString(b[:])
}

// ValidNonce reports whether s is 32 lowercase hex characters.
func ValidNonce(s string) bool {
	if len(s) != 2*NonceLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ValidLinkID reports whether s is an active link id ("l-" plus 32 hex).
func ValidLinkID(s string) bool {
	return len(s) == len(linkPfx)+2*NonceLen && s[:len(linkPfx)] == linkPfx && ValidNonce(s[len(linkPfx):])
}

// Store reads and writes the device tables.
type Store struct {
	DB *sql.DB
	// Self is the own identity key, wire form.
	Self string
	// Now is the clock; nil uses time.Now. Tests inject one.
	Now func() time.Time
}

// Time returns the store's current time.
func (s *Store) Time() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func fmtTime(t time.Time) string { return t.UTC().Format(timeFmt) }

func parseTime(s sql.NullString) time.Time {
	if !s.Valid || s.String == "" {
		return time.Time{}
	}
	t, err := time.Parse(timeFmt, s.String)
	if err != nil {
		return time.Time{}
	}
	return t
}

const linkCols = `id, peer, role, state, nonce, peer_nonce, approval, created, expires, activated_at, revoked_at, updated`

type rowScanner interface{ Scan(dest ...any) error }

func scanLink(r rowScanner) (Link, error) {
	var l Link
	var peerNonce, appr, created, expires, activated, revoked, updated sql.NullString
	if err := r.Scan(&l.ID, &l.Peer, &l.Role, &l.State, &l.Nonce, &peerNonce, &appr, &created, &expires, &activated, &revoked, &updated); err != nil {
		return Link{}, err
	}
	l.PeerNonce = peerNonce.String
	l.Approval = appr.String
	l.Created = parseTime(created)
	l.Expires = parseTime(expires)
	l.ActivatedAt = parseTime(activated)
	l.RevokedAt = parseTime(revoked)
	l.Updated = parseTime(updated)
	return l, nil
}

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Store) controllerHelper(l Link) (controller, helper string) {
	if l.Role == RoleController {
		return s.Self, l.Peer
	}
	return l.Peer, s.Self
}

// ExpireTx revokes intents that lapsed (pending_approval and waiting rows
// older than IntentTTL, or past their expiry) and drops offers older than
// IntentTTL, so a lapsed intent no longer blocks a new link with the same
// peer or counts toward the hierarchy limits. It is called at the start of
// every operation that reads intents.
func (s *Store) ExpireTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	cut := fmtTime(now.Add(-IntentTTL))
	n := fmtTime(now)
	ids, err := collectIDs(ctx, tx, `SELECT id FROM device_links WHERE (state = 'waiting' AND expires <= ?) OR (state = 'pending_approval' AND created <= ?)`, n, cut)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE device_links SET state = 'revoked', revoked_at = ?, updated = ? WHERE id = ?`, n, n, id); err != nil {
			return fmt.Errorf("device: expire intent: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_offers WHERE received_at <= ?`, cut); err != nil {
		return fmt.Errorf("device: expire offers: %w", err)
	}
	return nil
}

func collectIDs(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("device: query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("device: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CheckHierarchyTx applies the one-way hierarchy to a device that wants role
// with peer (Docs/protocol/device.md §One-way hierarchy). Rows with id
// excludeID are ignored. Non-revoked rows (intents included) count.
func (s *Store) CheckHierarchyTx(ctx context.Context, tx *sql.Tx, peer, role, excludeID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, peer, role FROM device_links WHERE state IN ('pending_approval', 'waiting', 'active')`)
	if err != nil {
		return fmt.Errorf("device: hierarchy: %w", err)
	}
	defer func() { _ = rows.Close() }()
	controllers := 0
	for rows.Next() {
		var id, p, r string
		if err := rows.Scan(&id, &p, &r); err != nil {
			return fmt.Errorf("device: hierarchy: %w", err)
		}
		if id == excludeID {
			continue
		}
		switch {
		case p == peer && r != role:
			return ErrCycle // the reverse link
		case p == peer:
			return ErrAlreadyLinked
		case r != role:
			return ErrCycle // a helper cannot control and a controller cannot be controlled: depth 1
		case role == RoleHelper:
			return ErrLimit // one controller
		default:
			controllers++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if role == RoleController && controllers >= MaxHelpers {
		return ErrLimit
	}
	return nil
}

// PendingFor returns the pending_approval row for peer, if any.
func (s *Store) PendingFor(ctx context.Context, peer string) (Link, bool, error) {
	l, err := scanLink(s.DB.QueryRowContext(ctx, `SELECT `+linkCols+` FROM device_links WHERE peer = ? AND state = 'pending_approval'`, peer))
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, false, nil
	}
	if err != nil {
		return Link{}, false, fmt.Errorf("device: read: %w", err)
	}
	return l, true, nil
}

// CreateIntent checks the hierarchy and stores a pending_approval row for peer
// in role. A pending_approval row for the same peer that was never approved is
// replaced (the caller has rejected its approval), so a rejected attempt can be
// retried at once.
func (s *Store) CreateIntent(ctx context.Context, peer, role string) (Link, error) {
	now := s.Time()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Link{}, fmt.Errorf("device: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.ExpireTx(ctx, tx, now); err != nil {
		return Link{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE device_links SET state = 'revoked', revoked_at = ?, updated = ? WHERE peer = ? AND state = 'pending_approval'`,
		fmtTime(now), fmtTime(now), peer); err != nil {
		return Link{}, fmt.Errorf("device: replace pending: %w", err)
	}
	if err := s.CheckHierarchyTx(ctx, tx, peer, role, ""); err != nil {
		return Link{}, err
	}
	l := Link{ID: NewIntentID(), Peer: peer, Role: role, State: StatePendingApproval, Nonce: NewNonce(), Created: now, Updated: now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO device_links (id, peer, role, state, nonce, created, updated) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.Peer, l.Role, l.State, l.Nonce, fmtTime(now), fmtTime(now)); err != nil {
		return Link{}, fmt.Errorf("device: insert intent: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Link{}, fmt.Errorf("device: commit: %w", err)
	}
	return l, nil
}

// SetApproval records the approval id of a pending_approval row.
func (s *Store) SetApproval(ctx context.Context, id, approvalID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE device_links SET approval = ? WHERE id = ? AND state = 'pending_approval'`, approvalID, id)
	if err != nil {
		return fmt.Errorf("device: set approval: %w", err)
	}
	return nil
}

// Delete removes a row (used when the approval could not be created,
// Docs/review/26-2.2a-review.md N5).
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM device_links WHERE id = ? AND state = 'pending_approval'`, id)
	if err != nil {
		return fmt.Errorf("device: delete: %w", err)
	}
	return nil
}

// GetTx reads one row by id.
func (s *Store) GetTx(ctx context.Context, tx *sql.Tx, id string) (Link, error) {
	l, err := scanLink(tx.QueryRowContext(ctx, `SELECT `+linkCols+` FROM device_links WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrUnknownLink
	}
	if err != nil {
		return Link{}, fmt.Errorf("device: read: %w", err)
	}
	return l, nil
}

// List returns every row, oldest first, after lapsing stale intents.
func (s *Store) List(ctx context.Context) ([]Link, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("device: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.ExpireTx(ctx, tx, s.Time()); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+linkCols+` FROM device_links ORDER BY created, id`)
	if err != nil {
		return nil, fmt.Errorf("device: list: %w", err)
	}
	var out []Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("device: list: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("device: commit: %w", err)
	}
	return out, nil
}

// ActiveWith returns the active link with peer, if any.
func (s *Store) ActiveWith(ctx context.Context, peer string) (Link, bool, error) {
	l, err := scanLink(s.DB.QueryRowContext(ctx, `SELECT `+linkCols+` FROM device_links WHERE peer = ? AND state = 'active'`, peer))
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, false, nil
	}
	if err != nil {
		return Link{}, false, fmt.Errorf("device: read: %w", err)
	}
	return l, true, nil
}

// ApproveTx turns a pending_approval row into a waiting intent: created = now
// and expires = now + IntentTTL. The caller has checked the row is still
// pending_approval.
func (s *Store) ApproveTx(ctx context.Context, tx *sql.Tx, id string, now time.Time) (Link, error) {
	res, err := tx.ExecContext(ctx, `UPDATE device_links SET state = 'waiting', created = ?, expires = ?, updated = ? WHERE id = ? AND state = 'pending_approval'`,
		fmtTime(now), fmtTime(now.Add(IntentTTL)), fmtTime(now), id)
	if err != nil {
		return Link{}, fmt.Errorf("device: approve: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Link{}, ErrUnknownLink
	}
	return s.GetTx(ctx, tx, id)
}

// OfferGetTx returns the kept offer of peer, if it is younger than IntentTTL.
func (s *Store) OfferGetTx(ctx context.Context, tx *sql.Tx, peer string, now time.Time) (body string, ok bool, err error) {
	var received string
	err = tx.QueryRowContext(ctx, `SELECT body, received_at FROM device_offers WHERE peer = ?`, peer).Scan(&body, &received)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("device: read offer: %w", err)
	}
	at, perr := time.Parse(timeFmt, received)
	if perr != nil || !at.After(now.Add(-IntentTTL)) {
		return "", false, nil
	}
	return body, true, nil
}

// OfferPutTx keeps offer body for peer, replacing any earlier one: at most one
// offer per peer is kept.
func (s *Store) OfferPutTx(ctx context.Context, tx *sql.Tx, peer, body string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO device_offers (peer, body, received_at) VALUES (?, ?, ?)
ON CONFLICT (peer) DO UPDATE SET body = excluded.body, received_at = excluded.received_at`, peer, body, fmtTime(now))
	if err != nil {
		return fmt.Errorf("device: keep offer: %w", err)
	}
	return nil
}

// OfferDeleteTx drops the kept offer of peer.
func (s *Store) OfferDeleteTx(ctx context.Context, tx *sql.Tx, peer string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_offers WHERE peer = ?`, peer); err != nil {
		return fmt.Errorf("device: drop offer: %w", err)
	}
	return nil
}

// TryActivateTx activates the waiting intent with peer if offer completes it
// (Docs/protocol/device.md §Link flow step 3): a local intent exists with the
// complementary role, it has not expired, and the offer's "at" is not older
// than the intent's created minus IntentTTL. The hierarchy is checked again.
// It returns the active link, or false when nothing was activated. An intent
// that the hierarchy now refuses is revoked.
func (s *Store) TryActivateTx(ctx context.Context, tx *sql.Tx, peer string, offer Offer, now time.Time) (Link, bool, error) {
	intent, err := scanLink(tx.QueryRowContext(ctx, `SELECT `+linkCols+` FROM device_links WHERE peer = ? AND state = 'waiting'`, peer))
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, false, nil
	}
	if err != nil {
		return Link{}, false, fmt.Errorf("device: read intent: %w", err)
	}
	if intent.Role != ComplementaryRole(offer.Role) || !intent.Expires.After(now) || offer.At.Before(intent.Created.Add(-IntentTTL)) {
		return Link{}, false, nil
	}
	if err := s.CheckHierarchyTx(ctx, tx, peer, intent.Role, intent.ID); err != nil {
		if errors.Is(err, ErrCycle) || errors.Is(err, ErrLimit) || errors.Is(err, ErrAlreadyLinked) {
			if _, uerr := tx.ExecContext(ctx, `UPDATE device_links SET state = 'revoked', revoked_at = ?, updated = ? WHERE id = ?`,
				fmtTime(now), fmtTime(now), intent.ID); uerr != nil {
				return Link{}, false, fmt.Errorf("device: revoke refused intent: %w", uerr)
			}
			return Link{}, false, nil
		}
		return Link{}, false, err
	}
	nonceC, nonceH := intent.Nonce, offer.Nonce
	if intent.Role == RoleHelper {
		nonceC, nonceH = offer.Nonce, intent.Nonce
	}
	controller, helper := s.controllerHelper(intent)
	id := LinkID(controller, helper, nonceC, nonceH)
	if _, err := tx.ExecContext(ctx, `UPDATE device_links SET id = ?, state = 'active', peer_nonce = ?, expires = NULL, activated_at = ?, updated = ? WHERE id = ?`,
		id, offer.Nonce, fmtTime(now), fmtTime(now), intent.ID); err != nil {
		return Link{}, false, fmt.Errorf("device: activate: %w", err)
	}
	if err := s.OfferDeleteTx(ctx, tx, peer); err != nil {
		return Link{}, false, err
	}
	l, err := s.GetTx(ctx, tx, id)
	return l, err == nil, err
}

// RevokeForPeerTx revokes every link and intent with peer, deletes their
// scopes and the kept offer, all inside tx. It returns the rows that were not
// already revoked (before the change), so the caller can audit and name the
// active link. Nothing to revoke changes nothing.
func (s *Store) RevokeForPeerTx(ctx context.Context, tx *sql.Tx, peer string, now time.Time) ([]Link, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+linkCols+` FROM device_links WHERE peer = ? AND state <> 'revoked'`, peer)
	if err != nil {
		return nil, fmt.Errorf("device: revoke: %w", err)
	}
	var found []Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("device: revoke: %w", err)
		}
		found = append(found, l)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	for _, l := range found {
		if _, err := tx.ExecContext(ctx, `UPDATE device_links SET state = 'revoked', revoked_at = ?, updated = ? WHERE id = ?`, fmtTime(now), fmtTime(now), l.ID); err != nil {
			return nil, fmt.Errorf("device: revoke: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM device_scopes WHERE link = ?`, l.ID); err != nil {
			return nil, fmt.Errorf("device: revoke scope: %w", err)
		}
	}
	if err := s.OfferDeleteTx(ctx, tx, peer); err != nil {
		return nil, err
	}
	return found, nil
}
