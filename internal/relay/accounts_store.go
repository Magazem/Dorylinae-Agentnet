package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// Accounts storage, relay migration R2 (Docs/protocol/accounts.md "Relay
// storage"). The quota_* tables are created here with accounts and filled
// by ticket 4.3a (Docs/protocol/invites.md). accounts.group_id names the
// account's quota group; quota_group_members carries how and when it joined,
// and 4.3a keeps the two in step. Times are unix milliseconds, last_day a
// UTC date.
const relayMigrationR2 = `
CREATE TABLE IF NOT EXISTS accounts (
	id       TEXT PRIMARY KEY,
	provider TEXT NOT NULL CHECK (provider IN ('github', 'email')),
	subject  TEXT NOT NULL,
	display  TEXT NOT NULL,
	state    TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'suspended')),
	created  INTEGER NOT NULL,
	group_id TEXT NULL,
	UNIQUE (provider, subject)
);
CREATE INDEX IF NOT EXISTS accounts_by_group ON accounts (group_id);
CREATE TABLE IF NOT EXISTS account_keys (
	key        TEXT PRIMARY KEY,
	account_id TEXT NOT NULL,
	device     TEXT NOT NULL,
	os         TEXT NOT NULL,
	bound_at   INTEGER NOT NULL,
	last_day   TEXT NULL
);
CREATE INDEX IF NOT EXISTS account_keys_by_account ON account_keys (account_id);
CREATE TABLE IF NOT EXISTS bind_requests (
	ref        TEXT PRIMARY KEY,
	key        TEXT NOT NULL,
	code_hash  TEXT NOT NULL UNIQUE,
	device     TEXT NOT NULL,
	os         TEXT NOT NULL,
	created    INTEGER NOT NULL,
	expires    INTEGER NOT NULL,
	state      TEXT NOT NULL CHECK (state IN ('pending', 'confirmed', 'denied', 'cancelled', 'expired')),
	account_id TEXT NULL
);
CREATE INDEX IF NOT EXISTS bind_requests_by_key ON bind_requests (key, state);
CREATE INDEX IF NOT EXISTS bind_requests_by_age ON bind_requests (expires);
CREATE TABLE IF NOT EXISTS web_sessions (
	id_hash          TEXT PRIMARY KEY,
	account_id       TEXT NULL,
	csrf             TEXT NOT NULL,
	created          INTEGER NOT NULL,
	expires          INTEGER NOT NULL,
	oauth_state_hash TEXT NULL
);
CREATE TABLE IF NOT EXISTS quota_groups (
	id              TEXT PRIMARY KEY,
	seats           INTEGER NOT NULL,
	wave            INTEGER NOT NULL DEFAULT 0,
	state           TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'suspended', 'closed')),
	contact_account TEXT NULL,
	created         INTEGER NOT NULL,
	note            TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS quota_group_members (
	group_id   TEXT NOT NULL,
	account_id TEXT NOT NULL UNIQUE,
	joined     INTEGER NOT NULL,
	via        TEXT NOT NULL CHECK (via IN ('invite', 'pairing', 'operator'))
);
CREATE INDEX IF NOT EXISTS quota_group_members_by_group ON quota_group_members (group_id);
`

// Account limits (Docs/protocol/accounts.md).
const (
	// MaxKeysPerAccount is how many keys one account may have bound.
	MaxKeysPerAccount = 4
	bindTTL           = 10 * time.Minute
	bindCodeDomain    = "dorylinae-bind-code-v1\n"
	// bindKeep is how long finished or expired bind requests are kept
	// before the sweep deletes them.
	bindKeep = 24 * time.Hour
	idChars  = 20 // random characters of an acc_ / bnd_ id (100 bits)
)

// Security journal events written by accounts (Docs/protocol/relay-hosted.md
// §3). Fields carry account or quota group ids and short key prefixes only.
const (
	journalUnbind        = "unbind"
	journalAccountDelete = "account_delete"
	journalSuspend       = "suspend"
	journalUnsuspend     = "unsuspend"
)

// Errors of the account operations.
var (
	// ErrBindInvalid is a user code or bind ref that is unknown, expired,
	// already used or cancelled.
	ErrBindInvalid = errors.New("bind code is invalid, expired or already used")
	// ErrAccountKeysFull is a confirm for an account that already has
	// MaxKeysPerAccount keys: one must be unbound first.
	ErrAccountKeysFull = errors.New("the account already has 4 bound keys; unbind one first")
	// ErrAlreadyBound is a key that is bound already (logout first).
	ErrAlreadyBound = errors.New("the key is already bound to an account")
	// ErrAccountSuspended is an operation on a suspended account.
	ErrAccountSuspended = errors.New("the account is suspended")
	// ErrNoAccount is an unknown account, key or quota group id.
	ErrNoAccount = errors.New("no such account, key or quota group")
	// ErrProviderDisabled is a sign-in provider this relay does not accept.
	ErrProviderDisabled = errors.New("sign-in provider not enabled on this relay")
)

// binding is a bound key's account as the routing check sees it.
type binding struct {
	account   string
	display   string
	group     string // "" without an active quota group
	suspended bool   // the account or its quota group is suspended
}

// accountStore runs the account operations against the relay database. The
// relay and `relay admin` both use it; journal (may be nil) receives the
// security events.
type accountStore struct {
	db      *sql.DB
	now     func() time.Time
	journal *JournalWriter
}

func opCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), queueOpTimeout)
}

// randomID returns prefix followed by idChars random Crockford base32 characters.
func randomID(prefix string) (string, error) {
	raw := make([]byte, idChars)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i, b := range raw {
		raw[i] = envelope.PairAlphabet[b&31]
	}
	return prefix + strings.ToLower(string(raw)), nil
}

func hashBindCode(code string) string {
	h := sha256.Sum256([]byte(bindCodeDomain + code))
	return hex.EncodeToString(h[:])
}

func (st *accountStore) record(event string, fields map[string]string) error {
	if st.journal == nil {
		return nil
	}
	if err := st.journal.Append(event, fields); err != nil {
		return fmt.Errorf("security journal: %w", err)
	}
	return nil
}

// loadBindings reads every bound key with its account's state.
func (st *accountStore) loadBindings() (map[string]binding, error) {
	ctx, cancel := opCtx()
	defer cancel()
	rows, err := st.db.QueryContext(ctx, `
SELECT k.key, a.id, a.display, a.state, COALESCE(g.id, ''), COALESCE(g.state, '')
FROM account_keys k JOIN accounts a ON a.id = k.account_id
LEFT JOIN quota_groups g ON g.id = a.group_id`)
	if err != nil {
		return nil, fmt.Errorf("read bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]binding{}
	for rows.Next() {
		var key, acc, display, state, group, groupState string
		if err := rows.Scan(&key, &acc, &display, &state, &group, &groupState); err != nil {
			return nil, fmt.Errorf("read bindings: %w", err)
		}
		b := binding{account: acc, display: display, suspended: state == "suspended" || groupState == "suspended"}
		if groupState == "active" {
			b.group = group
		}
		out[key] = b
	}
	return out, rows.Err()
}

// ensureAccount returns the id of the account (provider, subject), creating
// it if needed, and records its current display.
func (st *accountStore) ensureAccount(provider, subject, display string) (string, error) {
	if provider != "github" && provider != "email" {
		return "", ErrProviderDisabled
	}
	subject = strings.TrimSpace(subject)
	if provider == "email" {
		subject = strings.ToLower(subject)
	}
	if subject == "" || display == "" {
		return "", errors.New("account subject and display are required")
	}
	ctx, cancel := opCtx()
	defer cancel()
	id, err := randomID("acc_")
	if err != nil {
		return "", err
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO accounts (id, provider, subject, display, created) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (provider, subject) DO UPDATE SET display = excluded.display`,
		id, provider, subject, display, st.now().UnixMilli()); err != nil {
		return "", fmt.Errorf("store account: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE provider = ? AND subject = ?`, provider, subject).Scan(&id); err != nil {
		return "", fmt.Errorf("read account: %w", err)
	}
	return id, tx.Commit()
}

// createBind stores a new pending bind for key, replacing any pending one
// (at most one pending bind per key), and returns its ref and user code.
func (st *accountStore) createBind(key, device, osName string) (ref, code string, expires time.Time, err error) {
	ctx, cancel := opCtx()
	defer cancel()
	now := st.now()
	expires = now.Add(bindTTL)
	for range 5 {
		if ref, err = randomID("bnd_"); err != nil {
			return "", "", time.Time{}, err
		}
		if code, err = envelope.NewBindCode(); err != nil {
			return "", "", time.Time{}, err
		}
		err = func() error {
			tx, err := st.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.ExecContext(ctx, `UPDATE bind_requests SET state = 'cancelled' WHERE key = ? AND state = 'pending'`, key); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO bind_requests (ref, key, code_hash, device, os, created, expires, state) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending')`,
				ref, key, hashBindCode(code), device, osName, now.UnixMilli(), expires.UnixMilli()); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err == nil {
			return ref, code, expires, nil
		}
		if !strings.Contains(err.Error(), "UNIQUE") { // a code or ref collision is retried
			break
		}
	}
	return "", "", time.Time{}, fmt.Errorf("store bind request: %w", err)
}

// bindStatus reports the state of key's bind ref, with a pending bind past
// its expiry reported (and stored) as expired. An unknown ref, or one of
// another key, is "expired".
func (st *accountStore) bindStatus(ref, key string) (state string, expires time.Time, err error) {
	ctx, cancel := opCtx()
	defer cancel()
	var exp int64
	err = st.db.QueryRowContext(ctx, `SELECT state, expires FROM bind_requests WHERE ref = ? AND key = ?`, ref, key).Scan(&state, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return "expired", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, err
	}
	expires = time.UnixMilli(exp)
	if state == "pending" && !st.now().Before(expires) {
		if _, err := st.db.ExecContext(ctx, `UPDATE bind_requests SET state = 'expired' WHERE ref = ? AND state = 'pending'`, ref); err != nil {
			return "", time.Time{}, err
		}
		state = "expired"
	}
	return state, expires, nil
}

// cancelBind drops key's pending bind ref; anything else is ignored.
func (st *accountStore) cancelBind(ref, key string) error {
	ctx, cancel := opCtx()
	defer cancel()
	_, err := st.db.ExecContext(ctx, `UPDATE bind_requests SET state = 'cancelled' WHERE ref = ? AND key = ? AND state = 'pending'`, ref, key)
	return err
}

// BindRequest is a pending bind as the confirm page shows it
// (Docs/protocol/accounts.md "Confirm page").
type BindRequest struct {
	Ref         string
	Key         string
	Fingerprint string // fp(key), as agentnet identity prints it (formatted)
	Device      string
	OS          string
	Created     time.Time
	Expires     time.Time
}

// pendingByCode finds the pending, unexpired bind whose user code is code.
func (st *accountStore) pendingByCode(code string) (BindRequest, error) {
	norm, ok := envelope.NormalizeBindCode(code)
	if !ok {
		return BindRequest{}, ErrBindInvalid
	}
	ctx, cancel := opCtx()
	defer cancel()
	var r BindRequest
	var created, expires int64
	err := st.db.QueryRowContext(ctx, `SELECT ref, key, device, os, created, expires FROM bind_requests WHERE code_hash = ? AND state = 'pending' AND expires > ?`,
		hashBindCode(norm), st.now().UnixMilli()).Scan(&r.Ref, &r.Key, &r.Device, &r.OS, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return BindRequest{}, ErrBindInvalid
	}
	if err != nil {
		return BindRequest{}, err
	}
	r.Created, r.Expires = time.UnixMilli(created), time.UnixMilli(expires)
	if fp, err := envelope.KeyFingerprint(r.Key); err == nil {
		r.Fingerprint = envelope.FormatFingerprint(fp)
	}
	return r, nil
}

// confirmBind binds the key of pending bind ref to account acc.
func (st *accountStore) confirmBind(ref, acc string) (key string, err error) {
	ctx, cancel := opCtx()
	defer cancel()
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	now := st.now()
	var device, osName string
	err = tx.QueryRowContext(ctx, `SELECT key, device, os FROM bind_requests WHERE ref = ? AND state = 'pending' AND expires > ?`,
		ref, now.UnixMilli()).Scan(&key, &device, &osName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrBindInvalid
	}
	if err != nil {
		return "", err
	}
	var state, groupState string
	err = tx.QueryRowContext(ctx, `SELECT a.state, COALESCE(g.state, '') FROM accounts a LEFT JOIN quota_groups g ON g.id = a.group_id WHERE a.id = ?`, acc).Scan(&state, &groupState)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoAccount
	}
	if err != nil {
		return "", err
	}
	if state == "suspended" || groupState == "suspended" {
		return "", ErrAccountSuspended
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_keys WHERE key = ?`, key).Scan(&n); err != nil {
		return "", err
	}
	if n > 0 {
		return "", ErrAlreadyBound
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_keys WHERE account_id = ?`, acc).Scan(&n); err != nil {
		return "", err
	}
	if n >= MaxKeysPerAccount {
		return "", ErrAccountKeysFull
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_keys (key, account_id, device, os, bound_at) VALUES (?, ?, ?, ?, ?)`,
		key, acc, device, osName, now.UnixMilli()); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bind_requests SET state = 'confirmed', account_id = ? WHERE ref = ?`, acc, ref); err != nil {
		return "", err
	}
	return key, tx.Commit()
}

// denyBind ends pending bind ref as denied.
func (st *accountStore) denyBind(ref string) error {
	ctx, cancel := opCtx()
	defer cancel()
	res, err := st.db.ExecContext(ctx, `UPDATE bind_requests SET state = 'denied' WHERE ref = ? AND state = 'pending'`, ref)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBindInvalid
	}
	return nil
}

// unbindKey deletes key's binding and journals it.
func (st *accountStore) unbindKey(key string) error {
	ctx, cancel := opCtx()
	defer cancel()
	var acc string
	err := st.db.QueryRowContext(ctx, `DELETE FROM account_keys WHERE key = ? RETURNING account_id`, key).Scan(&acc)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoAccount
	}
	if err != nil {
		return err
	}
	return st.record(journalUnbind, map[string]string{"account": acc, "key": short(key)})
}

// setAccountState suspends (or reactivates) account acc and journals it.
func (st *accountStore) setAccountState(acc string, suspend bool) error {
	if err := execAccountState(st.db, acc, suspend); err != nil {
		return err
	}
	return st.record(suspendEvent(suspend), map[string]string{"account": acc})
}

// setGroupState suspends (or reactivates) quota group g and journals it.
// A closed group stays closed.
func (st *accountStore) setGroupState(g string, suspend bool) error {
	if err := execGroupState(st.db, g, suspend); err != nil {
		return err
	}
	return st.record(suspendEvent(suspend), map[string]string{"group": g})
}

// deleteAccount removes account acc, its bindings, bind requests, web
// sessions and group membership, closes its quota group if it was the last
// member, and journals it.
func (st *accountStore) deleteAccount(acc string) error {
	if err := execDeleteAccount(st.db, acc); err != nil {
		return err
	}
	return st.record(journalAccountDelete, map[string]string{"account": acc})
}

// touchKey records that key connected on day (UTC date), once a day.
func (st *accountStore) touchKey(key string, day string) error {
	ctx, cancel := opCtx()
	defer cancel()
	_, err := st.db.ExecContext(ctx, `UPDATE account_keys SET last_day = ? WHERE key = ? AND (last_day IS NULL OR last_day <> ?)`, day, key, day)
	return err
}

// prune deletes bind requests that expired more than bindKeep ago.
func (st *accountStore) prune() error {
	ctx, cancel := opCtx()
	defer cancel()
	_, err := st.db.ExecContext(ctx, `DELETE FROM bind_requests WHERE expires < ?`, st.now().Add(-bindKeep).UnixMilli())
	return err
}

func suspendEvent(suspend bool) string {
	if suspend {
		return journalSuspend
	}
	return journalUnsuspend
}

// The statements below are shared by the live operations and the journal
// replay, which must not journal again.

func execAccountState(db *sql.DB, acc string, suspend bool) error {
	ctx, cancel := opCtx()
	defer cancel()
	state := "active"
	if suspend {
		state = "suspended"
	}
	res, err := db.ExecContext(ctx, `UPDATE accounts SET state = ? WHERE id = ?`, state, acc)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoAccount
	}
	return nil
}

func execGroupState(db *sql.DB, g string, suspend bool) error {
	ctx, cancel := opCtx()
	defer cancel()
	from, to := "suspended", "active"
	if suspend {
		from, to = "active", "suspended"
	}
	var state string
	err := db.QueryRowContext(ctx, `SELECT state FROM quota_groups WHERE id = ?`, g).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoAccount
	}
	if err != nil {
		return err
	}
	if state != from {
		return nil // already there, or closed
	}
	_, err = db.ExecContext(ctx, `UPDATE quota_groups SET state = ? WHERE id = ?`, to, g)
	return err
}

func execDeleteAccount(db *sql.DB, acc string) error {
	ctx, cancel := opCtx()
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var group sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT group_id FROM accounts WHERE id = ?`, acc).Scan(&group)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoAccount
	}
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM account_keys WHERE account_id = ?`,
		`DELETE FROM bind_requests WHERE account_id = ?`,
		`DELETE FROM web_sessions WHERE account_id = ?`,
		`DELETE FROM quota_group_members WHERE account_id = ?`,
		`DELETE FROM accounts WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, acc); err != nil {
			return err
		}
	}
	if group.Valid && group.String != "" {
		var left int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE group_id = ?`, group.String).Scan(&left); err != nil {
			return err
		}
		if left == 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE quota_groups SET state = 'closed' WHERE id = ?`, group.String); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Journal replay (relay restore --replay-journal): each handler re-applies
// one event to the restored database. A target that no longer exists is not
// an error (the backup may predate it).
func init() {
	ignoreGone := func(err error) error {
		if errors.Is(err, ErrNoAccount) {
			return nil
		}
		return err
	}
	RegisterJournalHandler(journalUnbind, func(db *sql.DB, e JournalEntry) error {
		acc, key := e.Fields["account"], e.Fields["key"]
		if acc == "" || key == "" {
			return errors.New("unbind entry lacks account or key")
		}
		ctx, cancel := opCtx()
		defer cancel()
		_, err := db.ExecContext(ctx, `DELETE FROM account_keys WHERE account_id = ? AND substr(key, 1, ?) = ?`, acc, len(key), key)
		return err
	})
	RegisterJournalHandler(journalAccountDelete, func(db *sql.DB, e JournalEntry) error {
		return ignoreGone(execDeleteAccount(db, e.Fields["account"]))
	})
	for _, suspend := range []bool{true, false} {
		RegisterJournalHandler(suspendEvent(suspend), func(db *sql.DB, e JournalEntry) error {
			if g := e.Fields["group"]; g != "" {
				return ignoreGone(execGroupState(db, g, suspend))
			}
			return ignoreGone(execAccountState(db, e.Fields["account"], suspend))
		})
	}
}
