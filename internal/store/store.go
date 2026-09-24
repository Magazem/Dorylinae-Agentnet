// Package store wraps the SQLite persistence layer and its schema migrations.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go driver, keeps cross-compilation static
)

// migration is one forward-only schema step. Versions are consecutive from 1.
type migration struct {
	version int
	name    string
	sql     string
}

// migrations is the ordered schema history. Never edit an applied entry; append.
var migrations = []migration{
	{1, "audit_events", `
CREATE TABLE audit_events (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	ts     TEXT NOT NULL,
	actor  TEXT NOT NULL,
	action TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(detail))
);
CREATE INDEX audit_events_ts ON audit_events (ts);
CREATE TRIGGER audit_events_no_update BEFORE UPDATE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit_events is append-only'); END;
CREATE TRIGGER audit_events_no_delete BEFORE DELETE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit_events is append-only'); END;
`},
	{2, "peers", `
CREATE TABLE peers (
	public_key TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	harness    TEXT NOT NULL,
	skills     TEXT NOT NULL CHECK (json_valid(skills)),
	card       TEXT NOT NULL CHECK (json_valid(card)),
	paired_at  TEXT NOT NULL
);
`},
	{3, "peers_trust", `
ALTER TABLE peers ADD COLUMN trust TEXT NOT NULL DEFAULT 'relay'
	CHECK (trust IN ('relay', 'code', 'fingerprint'));
ALTER TABLE peers ADD COLUMN mailbox_keys TEXT NOT NULL DEFAULT '[]'
	CHECK (json_valid(mailbox_keys));
`},
	{4, "pair_used_codes", `
CREATE TABLE pair_used_codes (
	hash    BLOB PRIMARY KEY,
	used_at INTEGER NOT NULL
);
`},
	{5, "mail_seen_inbox", `
CREATE TABLE mail_seen (
	from_key    TEXT NOT NULL,
	id          TEXT NOT NULL,
	received_at TEXT NOT NULL,
	PRIMARY KEY (from_key, id)
) WITHOUT ROWID;
CREATE INDEX mail_seen_received ON mail_seen (received_at);
CREATE TABLE mail_inbox (
	from_key    TEXT NOT NULL,
	id          TEXT NOT NULL,
	kind        TEXT NOT NULL,
	created     TEXT NOT NULL,
	received_at TEXT NOT NULL,
	signed      TEXT NOT NULL,
	PRIMARY KEY (from_key, id)
);
`},
	{6, "mailbox_keys_own", `
CREATE TABLE mailbox_keys_own (
	key_id       TEXT PRIMARY KEY,
	pub          TEXT NOT NULL,
	created      TEXT NOT NULL,
	not_after    TEXT NOT NULL,
	retired      TEXT,
	deleted      TEXT,
	announcement TEXT NOT NULL CHECK (json_valid(announcement))
);
`},
	{7, "outbox", `
CREATE TABLE outbox (
	id           TEXT PRIMARY KEY,
	to_key       TEXT NOT NULL,
	kind         TEXT NOT NULL,
	created      TEXT NOT NULL,
	key_id       TEXT,
	signed       TEXT,
	frame        TEXT,
	state        TEXT NOT NULL CHECK (state IN ('queued','relayed','delivered','expired','failed')),
	attempts     INTEGER NOT NULL DEFAULT 0,
	next_attempt TEXT,
	updated      TEXT NOT NULL,
	error        TEXT
);
CREATE INDEX outbox_due ON outbox (state, next_attempt);
`},
	// Rebuild: SQLite cannot change a CHECK. Safe inside the migration transaction
	// because through migration 7 nothing refers to peers; re-check sqlite_master
	// if a later migration adds such a reference before this one.
	{8, "peers_trust_team", `
CREATE TABLE peers_new (
	public_key    TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	harness       TEXT NOT NULL,
	skills        TEXT NOT NULL CHECK (json_valid(skills)),
	card          TEXT NOT NULL CHECK (json_valid(card)),
	paired_at     TEXT NOT NULL,
	trust         TEXT NOT NULL DEFAULT 'relay'
	              CHECK (trust IN ('relay', 'team', 'code', 'fingerprint')),
	mailbox_keys  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(mailbox_keys)),
	introduced_by TEXT
);
INSERT INTO peers_new (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys)
	SELECT public_key, name, harness, skills, card, paired_at, trust, mailbox_keys FROM peers;
DROP TABLE peers;
ALTER TABLE peers_new RENAME TO peers;
`},
	{9, "teams", `
CREATE TABLE teams (
    id      TEXT PRIMARY KEY,                       -- t-<32 hex>
    name    TEXT NOT NULL,
    owner   TEXT NOT NULL,
    epoch   INTEGER NOT NULL,
    state   TEXT NOT NULL CHECK (state IN ('active', 'left', 'removed', 'dissolved')),
    created TEXT NOT NULL,
    updated TEXT NOT NULL
);
CREATE TABLE team_members (
    team_id TEXT NOT NULL,
    key     TEXT NOT NULL,
    added   TEXT NOT NULL,
    PRIMARY KEY (team_id, key)
) WITHOUT ROWID;
CREATE INDEX team_members_key ON team_members (key);
CREATE TABLE team_invites (                         -- owner side
    lookup     TEXT PRIMARY KEY,
    team_id    TEXT NOT NULL,
    pairing_id TEXT NOT NULL,
    peer_key   TEXT NOT NULL,                       -- written on pair.complete
    created    TEXT NOT NULL,
    expires    TEXT NOT NULL,
    used       TEXT
);
CREATE TABLE team_pending_joins (                   -- joiner side
    owner_key TEXT NOT NULL,
    lookup    TEXT NOT NULL,
    created   TEXT NOT NULL,
    expires   TEXT NOT NULL,
    PRIMARY KEY (owner_key, lookup)                 -- two invites from one owner can be pending
) WITHOUT ROWID;
`},
	{10, "presence", `
CREATE TABLE presence_peers (
    key        TEXT PRIMARY KEY,
    boot       TEXT NOT NULL,
    seq        INTEGER NOT NULL,
    created    TEXT NOT NULL,                  -- msg.created of the accepted message
    state      TEXT NOT NULL CHECK (state IN ('online', 'offline')),
    agent      INTEGER NOT NULL CHECK (agent IN (0, 1)),
    human      INTEGER NOT NULL CHECK (human IN (0, 1, 2)),
    interval   INTEGER NOT NULL,
    last_rx    TEXT NOT NULL,                  -- receiver clock = last seen
    last_agent TEXT,
    last_human TEXT
) WITHOUT ROWID;
CREATE TABLE settings (
    key     TEXT PRIMARY KEY,                  -- 'presence.mode', 'presence.human', 'notify.*'
    value   TEXT NOT NULL CHECK (json_valid(value)),
    updated TEXT NOT NULL
);
`},
	{11, "requests", `
CREATE TABLE requests (
	direction         TEXT NOT NULL CHECK (direction IN ('in', 'out')),
	peer              TEXT NOT NULL,              -- in: sender; out: recipient
	id                TEXT NOT NULL,              -- r-<32 hex>
	team_id           TEXT NOT NULL,
	type              TEXT NOT NULL CHECK (type IN ('review', 'task', 'question')),
	urgency           TEXT NOT NULL CHECK (urgency IN ('low', 'normal', 'high', 'blocking')),
	urgency_declared  TEXT NOT NULL CHECK (urgency_declared IN ('low', 'normal', 'high', 'blocking')),
	downgraded_by     TEXT CHECK (downgraded_by IN ('sender', 'receiver')),
	body              TEXT NOT NULL CHECK (json_valid(body)),   -- canonical request object
	body_hash         TEXT NOT NULL,
	state             TEXT NOT NULL CHECK (state IN ('pending', 'accepted', 'declined', 'deferred', 'completed', 'cancelled')),
	state_seq         INTEGER NOT NULL DEFAULT 0,
	state_at          TEXT,
	deferred_until    TEXT,
	decline_code      TEXT,
	reason            TEXT,                       -- decline reason, or the sender's cancel reason
	note              TEXT,
	first_response    TEXT CHECK (first_response IN ('accept', 'decline', 'defer')),
	first_response_at TEXT,
	created           TEXT NOT NULL,              -- request.created
	received_at       TEXT,                       -- in only
	mail_id           TEXT NOT NULL,              -- out: current carrying mail; in: first mail
	last_reply        TEXT CHECK (last_reply IS NULL OR json_valid(last_reply)),  -- in: {"kind","body"}
	last_reply_sent   TEXT,                       -- in: last echo time
	idem_key          TEXT,                       -- out only
	params_hash       TEXT,                       -- out only
	cancel            TEXT CHECK (cancel IN ('requested', 'refused')),  -- out only
	cancel_at         TEXT,                       -- out only
	cancel_mail_id    TEXT,                       -- out only: current request.cancel mail
	updated           TEXT NOT NULL,
	PRIMARY KEY (direction, peer, id)
);
CREATE UNIQUE INDEX requests_idem ON requests (peer, idem_key)
	WHERE direction = 'out' AND idem_key IS NOT NULL;
CREATE INDEX requests_state ON requests (direction, state);
CREATE INDEX requests_peer_time ON requests (direction, peer, received_at);

CREATE TABLE request_cancels (
	peer        TEXT NOT NULL,                    -- sender
	id          TEXT NOT NULL,                    -- r-<32 hex>
	reason      TEXT,
	received_at TEXT NOT NULL,                    -- pruned after 31 d
	PRIMARY KEY (peer, id)
);
`},
	{12, "requests_result", `
ALTER TABLE requests ADD COLUMN result TEXT
	CHECK (result IS NULL OR json_valid(result));  -- canonical(result); in and out rows
`},
	{13, "webhook_queue", `
CREATE TABLE webhook_queue (
    id           TEXT PRIMARY KEY,                 -- w-<32 hex>
    event        TEXT NOT NULL,
    body         TEXT NOT NULL,                    -- exact JSON bytes to send
    state        TEXT NOT NULL CHECK (state IN ('pending', 'sent', 'failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    next_attempt TEXT,
    created      TEXT NOT NULL,
    updated      TEXT NOT NULL,
    status       INTEGER,                          -- last HTTP status
    error        TEXT
);
CREATE INDEX webhook_queue_due ON webhook_queue (state, next_attempt);
`},
	{14, "work_sessions", `
CREATE TABLE work_sessions (
    id            TEXT PRIMARY KEY,                  -- s-<32 hex>, derived
    role          TEXT NOT NULL CHECK (role IN ('requester', 'worker')),
    peer          TEXT NOT NULL,                     -- the other party
    request_id    TEXT NOT NULL,                     -- r-<32 hex>
    team_id       TEXT NOT NULL,
    state         TEXT NOT NULL CHECK (state IN ('open', 'awaiting_result', 'quarantined', 'closed')),
    outcome       TEXT CHECK (outcome IN ('accepted', 'cancelled')),
    seq           INTEGER NOT NULL DEFAULT 0,        -- A: last ws.state sent; B: last applied
    round         INTEGER NOT NULL DEFAULT 1,
    result        TEXT CHECK (result IS NULL OR json_valid(result)),  -- canonical result of the current round
    result_round  INTEGER,
    verification  TEXT CHECK (verification IN ('none', 'tests_passed', 'human_accepted')),
    changes       TEXT,                              -- last request-changes text (content)
    cancel        TEXT CHECK (cancel IN ('requested', 'refused')),     -- B only
    released      INTEGER NOT NULL DEFAULT 0,        -- A: 1 once the current round was released
    last_state    TEXT CHECK (last_state IS NULL OR json_valid(last_state)),  -- A: {"kind","body"}
    last_state_sent TEXT,
    opened        TEXT NOT NULL,
    state_at      TEXT NOT NULL,
    closed        TEXT,
    updated       TEXT NOT NULL,
    CHECK ((state = 'closed') = (outcome IS NOT NULL))
);
CREATE INDEX work_sessions_state ON work_sessions (state);
CREATE UNIQUE INDEX work_sessions_request ON work_sessions (role, peer, request_id);
`},
	{15, "approvals", `
CREATE TABLE approvals (
    id        TEXT PRIMARY KEY,                 -- a-<32 hex>
    kind      TEXT NOT NULL CHECK (kind IN ('grant','grant_policy','release','accept_result','device_link','device_scope')),
    subject   TEXT NOT NULL,
    summary   TEXT NOT NULL,                    -- the text shown (local data); no code material
    created   TEXT NOT NULL,
    expires   TEXT NOT NULL,
    attempts  INTEGER NOT NULL DEFAULT 0,
    state     TEXT NOT NULL CHECK (state IN ('pending','approved','rejected','expired')),
    decided   TEXT
);
CREATE INDEX approvals_state ON approvals (state, expires);
CREATE TABLE grant_policies (
    id        TEXT PRIMARY KEY,                 -- p-<32 hex>
    peer      TEXT NOT NULL,
    action    TEXT NOT NULL,
    scope     TEXT NOT NULL,
    public    INTEGER NOT NULL DEFAULT 0 CHECK (public IN (0, 1)),
    max_ttl   INTEGER,                          -- seconds; NULL = no cap beyond the grant default
    until     TEXT,                             -- NULL = no expiry on the policy itself
    created   TEXT NOT NULL
);
CREATE INDEX grant_policies_peer ON grant_policies (peer, action);
`},
	// grant_policies (migration 15, 2.2a) predates the policy matching rules of
	// grant.md §Policies (2.2c): it has no resolved local path, branch or
	// approval id, and "until" is nullable. This migration adds them without
	// touching the applied migration 15. Application code (internal/capability)
	// always writes a non-NULL path, approval and until from here on; the
	// DEFAULT only satisfies SQLite's ADD COLUMN NOT NULL requirement for any
	// pre-existing row (none exist before 2.2c ships).
	{16, "grants", `
CREATE TABLE grants (
    id          TEXT PRIMARY KEY,                -- g-<32 hex>
    direction   TEXT NOT NULL CHECK (direction IN ('issued', 'held')),
    peer        TEXT NOT NULL,                   -- holder (issued) / grantor (held)
    session     TEXT NOT NULL,
    action      TEXT NOT NULL CHECK (action IN ('fs.read', 'git.read')),
    label       TEXT NOT NULL,
    path        TEXT,                            -- issued only: resolved local path
    branch      TEXT,
    scope       TEXT,
    sensitive   INTEGER NOT NULL,
    nbf         TEXT NOT NULL,
    exp         TEXT NOT NULL,
    token       TEXT NOT NULL,                   -- canonical token
    state       TEXT NOT NULL CHECK (state IN ('pending_approval', 'active', 'revoked')),
    approval    TEXT,
    policy      TEXT,
    revoked_at  TEXT,
    reason      TEXT CHECK (reason IN ('user', 'session_closed', 'peer_removed')),
    created     TEXT NOT NULL,
    updated     TEXT NOT NULL
);
CREATE INDEX grants_session ON grants (session);
ALTER TABLE grant_policies ADD COLUMN path TEXT NOT NULL DEFAULT '';
ALTER TABLE grant_policies ADD COLUMN branch TEXT;
ALTER TABLE grant_policies ADD COLUMN approval TEXT NOT NULL DEFAULT '';
`},
	// Own-device link (Docs/protocol/device.md §Tables, 2.D1). device_links is
	// the "device" trust of D13: only the device link handlers write it.
	{17, "device_links", `
CREATE TABLE device_links (
    id           TEXT PRIMARY KEY,               -- l-<32 hex>; intents use i-<32 hex> until active
    peer         TEXT NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('controller', 'helper')),   -- this device's role
    state        TEXT NOT NULL CHECK (state IN ('pending_approval', 'waiting', 'active', 'revoked')),
    nonce        TEXT NOT NULL,                  -- own nonce (32 hex)
    peer_nonce   TEXT,
    approval     TEXT,
    created      TEXT NOT NULL,
    expires      TEXT,                           -- intents only
    activated_at TEXT,
    revoked_at   TEXT,
    updated      TEXT NOT NULL
);
CREATE UNIQUE INDEX device_links_peer ON device_links (peer) WHERE state IN ('pending_approval', 'waiting', 'active');

CREATE TABLE device_scopes (                     -- helper only (2.D2)
    link     TEXT PRIMARY KEY,
    scope    TEXT NOT NULL CHECK (json_valid(scope)),   -- canonical, with resolved paths
    expires  TEXT NOT NULL,
    approval TEXT NOT NULL,
    created  TEXT NOT NULL
);

CREATE TABLE device_offers (                     -- received offers waiting for a local intent
    peer        TEXT PRIMARY KEY,
    body        TEXT NOT NULL CHECK (json_valid(body)),
    received_at TEXT NOT NULL                    -- dropped after 10 min
);
`},
}

// Store is an open SQLite database with migrations applied.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One writer keeps SQLITE_BUSY out of the daemon's simple access pattern.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// DB exposes the handle for packages that own tables (e.g. audit).
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this binary (%d)", current, len(migrations))
	}
	for _, m := range migrations[current:] {
		if err := s.apply(ctx, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO migrations (version, name, applied_at) VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		m.version, m.name); err != nil {
		return err
	}
	return tx.Commit()
}
