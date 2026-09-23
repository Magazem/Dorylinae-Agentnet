package approval

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Create validates a new approval, shows it on the notifier and, only on
// success, persists and returns it (Docs/protocol/approval.md §Flow). The
// plaintext code is dropped after this call returns; only its HMAC check
// value stays in memory (Docs/protocol/approval.md §Object).
func (s *Store) Create(ctx context.Context, kind, subject, summary string, action Action) (View, error) {
	if !validKinds[kind] {
		return View{}, fmt.Errorf("approval: unknown kind %q", kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	locked, err := s.settings.Locked(ctx, now)
	if err != nil {
		return View{}, err
	}
	if locked {
		return View{}, ErrLocked
	}
	if len(s.pending) >= MaxPending {
		s.auditLimit(ctx, "pending")
		return View{}, ErrLimit
	}
	hourly, err := s.countCreatedSince(ctx, now.Add(-time.Hour))
	if err != nil {
		return View{}, err
	}
	if hourly >= MaxPerHour {
		s.auditLimit(ctx, "hourly")
		return View{}, ErrLimit
	}

	id, err := newID()
	if err != nil {
		return View{}, err
	}
	code, err := newCode()
	if err != nil {
		return View{}, err
	}
	mac := codeMAC(s.key, id, code)
	expires := now.Add(TTL)

	if s.notifier == nil {
		return View{}, ErrUnavailable
	}
	body := summary + " Code " + code
	if err := s.notifier.Show(ctx, id, expires, "AgentNet approval", body); err != nil {
		return View{}, ErrUnavailable
	}

	created := now.UTC().Format(storeTimeFmt)
	expiresStr := expires.UTC().Format(storeTimeFmt)
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO approvals (id, kind, subject, summary, created, expires, attempts, state)
VALUES (?, ?, ?, ?, ?, ?, 0, 'pending')`,
		id, kind, subject, summary, created, expiresStr); err != nil {
		return View{}, fmt.Errorf("approval: insert: %w", err)
	}
	s.pending[id] = &live{mac: mac, action: action}
	if s.audit != nil {
		_ = s.audit.Append(ctx, "cli", "approval.create", map[string]string{"id": id, "kind": kind, "subject": subject})
	}
	return View{ID: id, Kind: kind, Summary: summary, Created: created, Expires: expiresStr, State: StatePending, AttemptsLeft: MaxAttempts}, nil
}

func (s *Store) auditLimit(ctx context.Context, reason string) {
	if s.audit != nil {
		_ = s.audit.Append(ctx, "daemon", "approval.limit", map[string]string{"reason": reason})
	}
}

func (s *Store) countCreatedSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM approvals WHERE created >= ?`, since.UTC().Format(storeTimeFmt)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("approval: count hourly: %w", err)
	}
	return n, nil
}

// Confirm checks id and code and, on success, in one transaction re-checks
// the waiting action's preconditions and performs it
// (Docs/protocol/approval.md §Flow). The returned value is the action's
// result with an "approval" field merged in by the daemon layer; the caller
// of Confirm gets the bare action result.
func (s *Store) Confirm(ctx context.Context, id, code string) (any, error) {
	s.mu.Lock()
	entry, ok := s.pending[id]
	if !ok {
		s.mu.Unlock()
		return nil, s.unknownOrExpired(ctx, id)
	}
	now := s.now()
	expired, expires, err := s.checkExpiry(ctx, id, now)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if expired {
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ErrExpired
	}
	_ = expires

	want := codeMAC(s.key, id, code)
	if !hmac.Equal(want[:], entry.mac[:]) {
		attemptsLeft, rejected, err := s.recordBadCode(ctx, id)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		if rejected {
			delete(s.pending, id)
		}
		locked, lerr := s.settings.recordWrongCode(ctx, now)
		s.mu.Unlock()
		if lerr != nil {
			return nil, lerr
		}
		if locked {
			s.lockAll(ctx)
		}
		return nil, &BadCodeError{AttemptsLeft: attemptsLeft}
	}

	// Code is correct: run the action inside one transaction with the
	// approval's own state change (Docs/protocol/approval.md §Flow).
	action := entry.action
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("approval: begin confirm: %w", err)
	}
	decided := now.UTC().Format(storeTimeFmt)
	if _, err := tx.ExecContext(ctx, `UPDATE approvals SET state = 'approved', decided = ? WHERE id = ?`, decided, id); err != nil {
		_ = tx.Rollback()
		s.mu.Unlock()
		return nil, fmt.Errorf("approval: mark approved: %w", err)
	}
	if action.Precondition != nil {
		if err := action.Precondition(ctx, tx); err != nil {
			_ = tx.Rollback()
			delete(s.pending, id)
			s.mu.Unlock()
			s.rejectRow(ctx, id, "precondition")
			return nil, err
		}
	}
	var result any
	if action.Perform != nil {
		result, err = action.Perform(ctx, tx)
		if err != nil {
			_ = tx.Rollback()
			s.mu.Unlock()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("approval: commit confirm: %w", err)
	}
	delete(s.pending, id)
	s.mu.Unlock()
	if s.audit != nil {
		kind, subject := s.kindSubject(ctx, id)
		_ = s.audit.Append(ctx, "cli", "approval.approve", map[string]string{"id": id, "kind": kind, "subject": subject})
	}
	if s.notifier != nil {
		s.notifier.Remove(ctx, id)
	}
	return result, nil
}

// checkExpiry reports whether id's stored expiry is at or before now, and
// marks it expired (audited) when it is.
func (s *Store) checkExpiry(ctx context.Context, id string, now time.Time) (expired bool, expires time.Time, err error) {
	var expiresStr string
	if err := s.db.QueryRowContext(ctx, `SELECT expires FROM approvals WHERE id = ?`, id).Scan(&expiresStr); err != nil {
		return false, time.Time{}, fmt.Errorf("approval: read expiry: %w", err)
	}
	expires, err = time.Parse(storeTimeFmt, expiresStr)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("approval: parse expiry: %w", err)
	}
	if !now.Before(expires) {
		decided := now.UTC().Format(storeTimeFmt)
		if _, err := s.db.ExecContext(ctx, `UPDATE approvals SET state = 'expired', decided = ? WHERE id = ?`, decided, id); err != nil {
			return false, expires, fmt.Errorf("approval: expire: %w", err)
		}
		if s.audit != nil {
			kind, subject := s.kindSubject(ctx, id)
			_ = s.audit.Append(ctx, "daemon", "approval.reject", map[string]string{"id": id, "kind": kind, "subject": subject, "reason": "expired"})
		}
		return true, expires, nil
	}
	return false, expires, nil
}

// recordBadCode bumps id's attempt counter, rejecting it once attempts
// reach MaxAttempts (Docs/protocol/approval.md §Object). Caller holds s.mu.
func (s *Store) recordBadCode(ctx context.Context, id string) (attemptsLeft int, rejected bool, err error) {
	var attempts int
	if err := s.db.QueryRowContext(ctx, `SELECT attempts FROM approvals WHERE id = ?`, id).Scan(&attempts); err != nil {
		return 0, false, fmt.Errorf("approval: read attempts: %w", err)
	}
	attempts++
	attemptsLeft = MaxAttempts - attempts
	if attemptsLeft < 0 {
		attemptsLeft = 0
	}
	rejected = attempts >= MaxAttempts
	state := StatePending
	var decided any
	if rejected {
		state = StateRejected
		decided = s.now().UTC().Format(storeTimeFmt)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE approvals SET attempts = ?, state = ?, decided = ? WHERE id = ?`, attempts, state, decided, id); err != nil {
		return 0, false, fmt.Errorf("approval: write attempts: %w", err)
	}
	kind, subject := s.kindSubject(ctx, id)
	if s.audit != nil {
		_ = s.audit.Append(ctx, "daemon", "approval.bad_code", map[string]any{"id": id, "attempts_left": attemptsLeft})
		if rejected {
			_ = s.audit.Append(ctx, "daemon", "approval.reject", map[string]string{"id": id, "kind": kind, "subject": subject, "reason": "attempts"})
		}
	}
	return attemptsLeft, rejected, nil
}

// lockAll rejects every currently pending approval and shows a desktop
// warning (Docs/protocol/approval.md §Object, the 10th wrong code in 24 h).
func (s *Store) lockAll(ctx context.Context) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.pending))
	for id := range s.pending {
		ids = append(ids, id)
	}
	for _, id := range ids {
		delete(s.pending, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.rejectRow(ctx, id, "locked")
	}
	if s.audit != nil {
		_ = s.audit.Append(ctx, "daemon", "approval.locked", map[string]int{"wrong_codes": MaxWrongPerDay})
	}
	if s.notifier != nil {
		_ = s.notifier.Show(ctx, "lock-"+s.now().UTC().Format(storeTimeFmt), s.now().Add(time.Minute), "AgentNet",
			"approval codes were guessed wrongly 10 times; approvals are locked for up to 24 h")
	}
}

// rejectRow marks id rejected with reason and audits it. Used for reasons
// other than a wrong-code count (attempts is audited alongside its state
// change directly in recordBadCode).
func (s *Store) rejectRow(ctx context.Context, id, reason string) {
	decided := s.now().UTC().Format(storeTimeFmt)
	kind, subject := s.kindSubject(ctx, id)
	_, _ = s.db.ExecContext(ctx, `UPDATE approvals SET state = 'rejected', decided = ? WHERE id = ?`, decided, id)
	if s.audit != nil {
		_ = s.audit.Append(ctx, "daemon", "approval.reject", map[string]string{"id": id, "kind": kind, "subject": subject, "reason": reason})
	}
	if s.notifier != nil {
		s.notifier.Remove(ctx, id)
	}
}

func (s *Store) kindSubject(ctx context.Context, id string) (kind, subject string) {
	_ = s.db.QueryRowContext(ctx, `SELECT kind, subject FROM approvals WHERE id = ?`, id).Scan(&kind, &subject)
	return
}

// unknownOrExpired distinguishes "never existed" from "expired in a previous
// run" for an id with no in-memory entry.
func (s *Store) unknownOrExpired(ctx context.Context, id string) error {
	var state string
	err := s.db.QueryRowContext(ctx, `SELECT state FROM approvals WHERE id = ?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnknown
	}
	if err != nil {
		return fmt.Errorf("approval: lookup: %w", err)
	}
	if state == StateExpired {
		return ErrExpired
	}
	return ErrUnknown
}

// Reject rejects a pending approval with reason "user"
// (Docs/protocol/approval.md §Flow, `agentnet approve --reject`).
func (s *Store) Reject(ctx context.Context, id string) (View, error) {
	s.mu.Lock()
	if _, ok := s.pending[id]; !ok {
		s.mu.Unlock()
		return View{}, s.unknownOrExpired(ctx, id)
	}
	delete(s.pending, id)
	s.mu.Unlock()
	s.rejectRow(ctx, id, "user")
	return s.Show(ctx, id)
}

// Show returns the current view of id, whatever its state.
func (s *Store) Show(ctx context.Context, id string) (View, error) {
	var v View
	var attempts int
	err := s.db.QueryRowContext(ctx, `SELECT id, kind, summary, created, expires, state, attempts FROM approvals WHERE id = ?`, id).
		Scan(&v.ID, &v.Kind, &v.Summary, &v.Created, &v.Expires, &v.State, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return View{}, ErrUnknown
	}
	if err != nil {
		return View{}, fmt.Errorf("approval: show: %w", err)
	}
	v.AttemptsLeft = MaxAttempts - attempts
	if v.AttemptsLeft < 0 {
		v.AttemptsLeft = 0
	}
	return v, nil
}

// List returns every pending approval, oldest first, never a code
// (Docs/protocol/approval.md §IPC and CLI).
func (s *Store) List(ctx context.Context) ([]View, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, summary, created, expires, state, attempts FROM approvals WHERE state = 'pending' ORDER BY created`)
	if err != nil {
		return nil, fmt.Errorf("approval: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []View
	for rows.Next() {
		var v View
		var attempts int
		if err := rows.Scan(&v.ID, &v.Kind, &v.Summary, &v.Created, &v.Expires, &v.State, &attempts); err != nil {
			return nil, fmt.Errorf("approval: scan: %w", err)
		}
		v.AttemptsLeft = MaxAttempts - attempts
		if v.AttemptsLeft < 0 {
			v.AttemptsLeft = 0
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
