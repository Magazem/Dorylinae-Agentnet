package relay

import (
	"database/sql"
	"errors"
	"time"
)

// Admin runs the operator's account commands (`relay admin account|group`)
// directly on the relay database, which a running relay may have open: the
// relay notices the commit within accountsWatchEvery and closes the live
// connections an unbind, deletion or suspension affects. Security events go
// to the journal, if one is given.
type Admin struct {
	db *sql.DB
	st *accountStore
}

// OpenAdmin opens the relay database at dbPath (applying pending relay
// migrations) for the admin commands. journal may be nil.
func OpenAdmin(dbPath string, journal *JournalWriter) (*Admin, error) {
	if dbPath == "" {
		return nil, errors.New("relay database path is empty")
	}
	db, err := openRelayDB(dbPath)
	if err != nil {
		return nil, err
	}
	return &Admin{db: db, st: &accountStore{db: db, now: time.Now, journal: journal}}, nil
}

// Close closes the database.
func (a *Admin) Close() error { return a.db.Close() }

// AccountSummary is one line of `relay admin account list`.
type AccountSummary struct {
	ID       string
	Provider string
	Display  string
	State    string
	Group    string
	Keys     int
	Created  time.Time
}

// AccountKey is a bound key of an account.
type AccountKey struct {
	Key     string
	Device  string
	OS      string
	BoundAt time.Time
	LastDay string
}

// Accounts lists every account, oldest first.
func (a *Admin) Accounts() ([]AccountSummary, error) {
	ctx, cancel := opCtx()
	defer cancel()
	rows, err := a.db.QueryContext(ctx, `
SELECT a.id, a.provider, a.display, a.state, COALESCE(a.group_id, ''), a.created,
	(SELECT COUNT(*) FROM account_keys k WHERE k.account_id = a.id)
FROM accounts a ORDER BY a.created, a.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AccountSummary
	for rows.Next() {
		var s AccountSummary
		var created int64
		if err := rows.Scan(&s.ID, &s.Provider, &s.Display, &s.State, &s.Group, &created, &s.Keys); err != nil {
			return nil, err
		}
		s.Created = time.UnixMilli(created)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Account returns one account and its bound keys.
func (a *Admin) Account(id string) (AccountSummary, []AccountKey, error) {
	ctx, cancel := opCtx()
	defer cancel()
	s := AccountSummary{ID: id}
	var created int64
	err := a.db.QueryRowContext(ctx, `SELECT provider, display, state, COALESCE(group_id, ''), created FROM accounts WHERE id = ?`, id).
		Scan(&s.Provider, &s.Display, &s.State, &s.Group, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountSummary{}, nil, ErrNoAccount
	}
	if err != nil {
		return AccountSummary{}, nil, err
	}
	s.Created = time.UnixMilli(created)
	rows, err := a.db.QueryContext(ctx, `SELECT key, device, os, bound_at, COALESCE(last_day, '') FROM account_keys WHERE account_id = ? ORDER BY bound_at`, id)
	if err != nil {
		return AccountSummary{}, nil, err
	}
	defer func() { _ = rows.Close() }()
	var keys []AccountKey
	for rows.Next() {
		var k AccountKey
		var bound int64
		if err := rows.Scan(&k.Key, &k.Device, &k.OS, &bound, &k.LastDay); err != nil {
			return AccountSummary{}, nil, err
		}
		k.BoundAt = time.UnixMilli(bound)
		keys = append(keys, k)
	}
	s.Keys = len(keys)
	return s, keys, rows.Err()
}

// Unbind removes key's binding.
func (a *Admin) Unbind(key string) error { return a.st.unbindKey(key) }

// SetAccountSuspended suspends or reactivates account id.
func (a *Admin) SetAccountSuspended(id string, suspend bool) error {
	return a.st.setAccountState(id, suspend)
}

// SetGroupSuspended suspends or reactivates quota group id.
func (a *Admin) SetGroupSuspended(id string, suspend bool) error {
	return a.st.setGroupState(id, suspend)
}

// DeleteAccount deletes account id with its bindings.
func (a *Admin) DeleteAccount(id string) error { return a.st.deleteAccount(id) }
