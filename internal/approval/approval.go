// Package approval implements human approval of agent-triggered actions
// (Docs/protocol/approval.md). A one-time code is shown only on the desktop
// notification channel; the daemon keeps no code material anywhere durable,
// so a prompt-injected local agent using AgentNet's own interface cannot see
// or forge it.
package approval

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Kinds of a waiting action (Docs/protocol/approval.md §Object).
const (
	KindGrant        = "grant"
	KindGrantPolicy  = "grant_policy"
	KindRelease      = "release"
	KindAcceptResult = "accept_result"
	KindDeviceLink   = "device_link"
	KindDeviceScope  = "device_scope"
)

var validKinds = map[string]bool{
	KindGrant: true, KindGrantPolicy: true, KindRelease: true,
	KindAcceptResult: true, KindDeviceLink: true, KindDeviceScope: true,
}

// States (Docs/protocol/approval.md §Object).
const (
	StatePending  = "pending"
	StateApproved = "approved"
	StateRejected = "rejected"
	StateExpired  = "expired"
)

// Limits (Docs/protocol/approval.md §Object).
const (
	TTL             = 10 * time.Minute
	MaxAttempts     = 3
	MaxPending      = 5
	MaxPerHour      = 20
	MaxWrongPerDay  = 10
	WrongCodeWindow = 24 * time.Hour
)

const storeTimeFmt = "2006-01-02T15:04:05.000Z"

// Sentinel errors mapped to IPC error codes by the daemon
// (Docs/protocol/approval.md §IPC and CLI).
var (
	ErrUnknown     = errors.New("approval: unknown approval")
	ErrExpired     = errors.New("approval: expired")
	ErrLimit       = errors.New("approval: limit reached")
	ErrLocked      = errors.New("approval: locked")
	ErrUnavailable = errors.New("approval: notifier unavailable")
)

// BadCodeError is a wrong code; AttemptsLeft is how many attempts remain
// before the approval is rejected (Docs/protocol/approval.md §Flow).
type BadCodeError struct {
	AttemptsLeft int
}

func (e *BadCodeError) Error() string {
	return fmt.Sprintf("approval: bad code, %d attempt(s) left", e.AttemptsLeft)
}

// Action is registered with Create and run by Confirm. Precondition and
// Perform share the same *sql.Tx as the approval's own state transition, so
// the waiting action's change and the approval's decision commit or roll
// back together (Docs/protocol/approval.md §Flow, "in one transaction").
//
// Both hooks are expected to already return (or wrap) an *ipc.Error when
// they fail for a reason the caller wants reported verbatim (for example
// bad_state): approval.Confirm does not reinterpret these errors, it passes
// them through unchanged so the daemon's existing per-domain error mapping
// keeps working. A Precondition failure rejects the approval (reason
// "precondition") and drops the waiting action; a Perform failure rolls back
// the whole transaction, leaving the approval pending (the code was
// genuinely correct, so a caller may retry the same code).
type Action struct {
	// Precondition re-checks that the waiting action's preconditions still
	// hold, immediately before Perform. Nil skips the check.
	Precondition func(ctx context.Context, tx *sql.Tx) error
	// Perform does the waiting action and returns its own result. Nil means
	// there is nothing to do beyond the approval decision itself.
	Perform func(ctx context.Context, tx *sql.Tx) (any, error)
}

// Notifier shows and withdraws the one-time code as a desktop notification
// (Docs/protocol/approval.md §Delivering the code). Show must never let the
// code reach a child process's argument list. There is no fallback: a Show
// failure means the approval never existed (Create returns ErrUnavailable).
type Notifier interface {
	Show(ctx context.Context, id string, expires time.Time, title, body string) error
	// Remove best-effort withdraws the notification (Windows toast history);
	// other platforms no-op. Errors are not actionable and are ignored.
	Remove(ctx context.Context, id string)
}

// AuditSink is the part of audit.Log the store needs.
type AuditSink interface {
	Append(ctx context.Context, actor, action string, detail any) error
}

// View is the "approval view" of Docs/protocol/ipc.md-style responses
// (Docs/protocol/approval.md §IPC and CLI). It never carries the code.
type View struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Summary      string `json:"summary"`
	Created      string `json:"created"`
	Expires      string `json:"expires"`
	State        string `json:"state"`
	AttemptsLeft int    `json:"attempts_left"`
}

// live is the in-memory-only half of a pending approval: the check value and
// the registered action. It never touches SQLite (Docs/protocol/approval.md
// §Object, "the daemon keeps only a check value in memory"). Losing this map
// (a restart) is why every "pending" row is expired at start.
type live struct {
	mac    [sha256.Size]byte
	action Action
}

// Store persists approval metadata and audits every decision. The code
// check value and the registered Action live only in memory
// (Docs/protocol/approval.md §Object).
type Store struct {
	db       *sql.DB
	audit    AuditSink
	notifier Notifier
	settings *Settings
	now      func() time.Time

	key [32]byte // approval_key: crypto/rand at daemon start, never persisted

	mu      sync.Mutex
	pending map[string]*live
}

// NewStore builds a Store with a fresh approval_key. now defaults to
// time.Now. notifier may be nil only in tests that never call Create.
func NewStore(db *sql.DB, audit AuditSink, notifier Notifier, now func() time.Time) (*Store, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, fmt.Errorf("approval: generate approval_key: %w", err)
	}
	if now == nil {
		now = time.Now
	}
	return &Store{
		db: db, audit: audit, notifier: notifier, settings: NewSettings(db), now: now,
		key: key, pending: map[string]*live{},
	}, nil
}

// ExpireStale marks every approval left "pending" from a previous run as
// expired: the approval_key and in-memory code checks that run held are
// gone, so none of them can ever be confirmed (Docs/protocol/approval.md
// §Object). Call this once, right after NewStore, before serving IPC.
func (s *Store) ExpireStale(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, subject FROM approvals WHERE state = 'pending'`)
	if err != nil {
		return fmt.Errorf("approval: list stale: %w", err)
	}
	type stale struct{ id, kind, subject string }
	var list []stale
	for rows.Next() {
		var r stale
		if err := rows.Scan(&r.id, &r.kind, &r.subject); err != nil {
			_ = rows.Close()
			return fmt.Errorf("approval: scan stale: %w", err)
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_ = rows.Close()
	now := s.now().UTC().Format(storeTimeFmt)
	for _, r := range list {
		if _, err := s.db.ExecContext(ctx, `UPDATE approvals SET state = 'expired', decided = ? WHERE id = ?`, now, r.id); err != nil {
			return fmt.Errorf("approval: expire stale %s: %w", r.id, err)
		}
		if s.audit != nil {
			_ = s.audit.Append(ctx, "daemon", "approval.reject", map[string]string{
				"id": r.id, "kind": r.kind, "subject": r.subject, "reason": "expired",
			})
		}
	}
	s.pruneDecided(ctx, s.now())
	return nil
}

// newID returns "a-" plus 32 lowercase hex characters from crypto/rand.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "a-" + hex.EncodeToString(b[:]), nil
}

// newCode returns 6 decimal digits, uniform over 000000-999999, drawn from
// crypto/rand by rejection sampling (Docs/protocol/approval.md §Object).
func newCode() (string, error) {
	const modulus = 1000000
	// The largest multiple of modulus that fits in 24 bits, so v % modulus is uniform.
	const limit = (1 << 24) - (1<<24)%modulus
	var b [3]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		v := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
		if v < limit {
			return fmt.Sprintf("%06d", v%modulus), nil
		}
	}
}

// codeMAC computes the check value of Docs/protocol/approval.md §Object:
// HMAC-SHA256(approval_key, "dorylinae-approval-v2\n" || id || "\n" || code).
func codeMAC(key [32]byte, id, code string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte("dorylinae-approval-v2\n"))
	mac.Write([]byte(id))
	mac.Write([]byte("\n"))
	mac.Write([]byte(code))
	var out [sha256.Size]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// CodeMACHex is codeMAC, exported hex-encoded for the test vector
// (Docs/protocol/approval.md §Object, "Vector").
func CodeMACHex(key [32]byte, id, code string) string {
	m := codeMAC(key, id, code)
	return hex.EncodeToString(m[:])
}
