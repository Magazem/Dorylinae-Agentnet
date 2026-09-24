package approval

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ResolveTag finds the single pending approval whose id starts with tag, a
// full id or a prefix of at least "a-" + 6 hex characters
// (Docs/protocol/approval.md §Headless machines, "<tag> is the full id or a
// prefix of at least a- + 6 hex (an ambiguous prefix is refused with a
// message and uses no attempt)").
func (s *Store) ResolveTag(tag string) (id string, err error) {
	const minLen = len("a-") + 6
	if len(tag) < minLen {
		return "", ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var match string
	count := 0
	for pid, e := range s.pending {
		if !e.reserved && strings.HasPrefix(pid, tag) {
			match = pid
			count++
		}
	}
	switch count {
	case 0:
		return "", ErrUnknown
	case 1:
		return match, nil
	default:
		return "", ErrAmbiguousTag
	}
}

// Create validates a new approval and, in desktop mode, opens the approval
// window before the code exists (Docs/protocol/approval.md §The approval
// window, "Ready check"). Only once the window is ready (or, in terminal
// mode, always) is the code generated and shown; only once that succeeds is
// the approval persisted and returned. The plaintext code is dropped after
// this call returns; only its HMAC check value stays in memory
// (Docs/protocol/approval.md §Object).
func (s *Store) Create(ctx context.Context, kind, subject, summary string, action Action) (View, error) {
	if !validKinds[kind] {
		return View{}, fmt.Errorf("approval: unknown kind %q", kind)
	}
	s.mu.Lock()
	now := s.now()
	locked, err := s.settings.Locked(ctx, now)
	if err != nil {
		s.mu.Unlock()
		return View{}, err
	}
	if locked {
		s.mu.Unlock()
		return View{}, ErrLocked
	}
	if err := s.sweepExpiredLocked(ctx, now); err != nil {
		s.mu.Unlock()
		return View{}, err
	}
	s.pruneDecided(ctx, now)
	if len(s.pending) >= MaxPending {
		s.mu.Unlock()
		s.auditLimit(ctx, "pending")
		return View{}, ErrLimit
	}
	hourly, err := s.countCreatedSince(ctx, now.Add(-time.Hour))
	if err != nil {
		s.mu.Unlock()
		return View{}, err
	}
	if hourly >= MaxPerHour {
		s.mu.Unlock()
		s.auditLimit(ctx, "hourly")
		return View{}, ErrLimit
	}
	id, err := newID()
	if err != nil {
		s.mu.Unlock()
		return View{}, err
	}
	// Reserve the slot now, so a concurrent Create counts it toward
	// MaxPending even while the window's ready wait (which can take seconds)
	// runs outside s.mu (Docs/protocol/approval.md §The approval window,
	// "Locking").
	if s.closed {
		s.mu.Unlock()
		return View{}, ErrUnavailable
	}
	s.pending[id] = &live{action: action, reserved: true}
	s.mu.Unlock()

	expires := now.Add(TTL)
	var handle WindowHandle
	if s.window != nil {
		handle, err = s.window.Start(ctx, id, tagOf(id), kind, summary, expires)
		if err != nil || !handle.Ready(ctx) {
			s.dropReserved(id)
			if handle != nil {
				handle.Kill()
			}
			return View{}, ErrUnavailable
		}
		s.mu.Lock()
		if entry, ok := s.pending[id]; ok {
			entry.handle = handle
		}
		s.mu.Unlock()
	}

	code, err := newCode()
	if err != nil {
		s.dropReserved(id)
		if handle != nil {
			handle.Kill()
		}
		return View{}, err
	}
	mac := codeMAC(s.key, id, code)

	if s.notifier == nil {
		s.dropReserved(id)
		if handle != nil {
			handle.Kill()
		}
		return View{}, ErrUnavailable
	}
	title, body := deliveryText(s.window != nil, tagOf(id), summary, code)
	// A notifier failure after the window is ready kills the window and
	// stores nothing (review 29, M4): there is never a window without a code
	// or a code without a window.
	if err := s.notifier.Show(ctx, id, expires, title, body); err != nil {
		s.dropReserved(id)
		if handle != nil {
			handle.Kill()
		}
		return View{}, ErrUnavailable
	}

	created := now.UTC().Format(storeTimeFmt)
	expiresStr := expires.UTC().Format(storeTimeFmt)
	// The row is written and the slot finished under s.mu, and only if the
	// reservation survived: a lockout (dropAllLocked) or Close during the
	// ready wait removes it, and then nothing is stored (review 30, H1).
	s.mu.Lock()
	entry, ok := s.pending[id]
	if !ok || s.closed {
		delete(s.pending, id)
		s.mu.Unlock()
		if handle != nil {
			handle.Kill()
		}
		s.notifier.Remove(ctx, id)
		if locked, lerr := s.settings.Locked(ctx, s.now()); lerr == nil && locked {
			return View{}, ErrLocked
		}
		return View{}, ErrUnavailable
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO approvals (id, kind, subject, summary, created, expires, attempts, state)
VALUES (?, ?, ?, ?, ?, ?, 0, 'pending')`,
		id, kind, subject, summary, created, expiresStr); err != nil {
		delete(s.pending, id)
		s.mu.Unlock()
		if handle != nil {
			handle.Kill()
		}
		s.notifier.Remove(ctx, id)
		return View{}, fmt.Errorf("approval: insert: %w", err)
	}
	entry.mac = mac
	entry.reserved = false
	entry.timer = time.AfterFunc(TTL, func() { s.expireNow(id) })
	s.mu.Unlock()
	if s.audit != nil {
		_ = s.audit.Append(ctx, "cli", "approval.create", map[string]string{"id": id, "kind": kind, "subject": subject})
	}
	if handle != nil {
		s.startWatch(id, handle)
	}
	return View{
		ID: id, Kind: kind, Summary: summary, Created: created, Expires: expiresStr,
		State: StatePending, AttemptsLeft: MaxAttempts, Window: s.windowState(id),
	}, nil
}

// deliveryText builds the title/body of the code notification
// (Docs/protocol/approval.md §Delivering the code, §Headless machines). The
// two modes use different fixed wording.
func deliveryText(desktop bool, tag, summary, code string) (title, body string) {
	if desktop {
		title = fmt.Sprintf("AgentNet code %s for approval %s", code, tag)
		body = summary + fmt.Sprintf(" Type this code only into the AgentNet approval window %s. "+
			"AgentNet never asks for it in a terminal, a chat or an agent.", tag)
		return title, body
	}
	title = "AgentNet approval " + tag
	body = fmt.Sprintf(`%s. Code %s. Type "%s %s" to approve or "reject %s" to reject.`, summary, code, tag, code, tag)
	return title, body
}

// dropReserved removes id's reserved (possibly not-yet-coded) pending slot,
// used when Create fails after reserving it.
func (s *Store) dropReserved(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
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

// Confirm checks id and code from the daemon's own terminal stdin
// (Docs/protocol/approval.md §Headless machines) and, on success, in one
// transaction re-checks the waiting action's preconditions and performs it
// (§Flow). It is also used directly by tests. The desktop window path uses
// the unexported confirm with via "window" instead, so its outcome can
// trigger a reopened window.
func (s *Store) Confirm(ctx context.Context, id, code string) (any, error) {
	return s.confirm(ctx, id, code, "terminal")
}

// confirm is Confirm's implementation, tagging the audit trail with via
// ("window" or "terminal") and clearing any window handle so a caller that
// wants to reopen the window can (Docs/protocol/approval.md §Audit).
func (s *Store) confirm(ctx context.Context, id, code, via string) (any, error) {
	s.mu.Lock()
	entry, ok := s.pending[id]
	if !ok || entry.reserved {
		s.mu.Unlock()
		return nil, s.unknownOrExpired(ctx, id)
	}
	now := s.now()
	expired, _, err := s.checkExpiryLocked(ctx, id, now)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if expired {
		handle := entry.handle
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(s.pending, id)
		s.mu.Unlock()
		if handle != nil {
			handle.Kill()
		}
		if s.notifier != nil {
			s.notifier.Remove(ctx, id)
		}
		return nil, ErrExpired
	}

	want := codeMAC(s.key, id, code)
	if !hmac.Equal(want[:], entry.mac[:]) {
		handle := entry.handle
		entry.handle = nil // the dialog already exited after sending this answer
		attemptsLeft, rejected, err := s.recordBadCode(ctx, id, via)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		var rejectedHandle WindowHandle
		if rejected {
			if entry.timer != nil {
				entry.timer.Stop()
			}
			delete(s.pending, id)
			rejectedHandle = handle
		}
		locked, lerr := s.settings.recordWrongCode(ctx, now)
		var lockedEntries []lockedEntry
		if lerr == nil && locked {
			// Drop every pending entry before releasing s.mu, so no other
			// Confirm can test a code between the 10th wrong code and the
			// lock (review 26, M-2).
			lockedEntries = s.dropAllLocked()
		}
		s.mu.Unlock()
		if rejectedHandle != nil {
			rejectedHandle.Kill()
		}
		if lerr != nil {
			return nil, lerr
		}
		if locked {
			s.lockAll(ctx, lockedEntries)
		}
		return nil, &BadCodeError{AttemptsLeft: attemptsLeft}
	}

	// Code is correct: run the action inside one transaction with the
	// approval's own state change, still under s.mu (Docs/protocol/approval.md
	// §Flow; review 26, "Races": Confirm/Reject/Create are serialised by
	// Store.mu, and the state change plus the action share one sql.Tx, so
	// there is no TOCTOU).
	action := entry.action
	handle := entry.handle
	entry.handle = nil
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
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
			if handle != nil {
				handle.Kill()
			}
			s.rejectRow(ctx, id, "precondition", via)
			return nil, err
		}
	}
	var result any
	if action.Perform != nil {
		result, err = action.Perform(ctx, tx)
		if err != nil {
			_ = tx.Rollback()
			// A Perform error leaves the approval pending (the code was
			// genuinely correct): restart the timer so a caller can decide
			// whether to reopen the window with a retry message.
			if e, ok := s.pending[id]; ok {
				// A real-wall-clock timer, deliberately not derived from the
				// (possibly fake, in tests) now: it exists only as a
				// production safety net alongside the lazy sweep, same as
				// Create's initial timer.
				e.timer = time.AfterFunc(TTL, func() { s.expireNow(id) })
			}
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
	if handle != nil {
		handle.Kill()
	}
	if s.audit != nil {
		kind, subject := s.kindSubject(ctx, id)
		_ = s.audit.Append(ctx, "cli", "approval.approve", map[string]string{"id": id, "kind": kind, "subject": subject, "via": via})
	}
	if s.notifier != nil {
		s.notifier.Remove(ctx, id)
	}
	if ac, ok := result.(AfterCommitter); ok {
		ac.AfterCommit(ctx)
	}
	return result, nil
}

// checkExpiryLocked reports whether id's stored expiry is at or before now,
// and marks it expired (audited) when it is. Caller holds s.mu; it is
// released and reacquired around the (rare) DB write.
func (s *Store) checkExpiryLocked(ctx context.Context, id string, now time.Time) (expired bool, expires time.Time, err error) {
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
// reach MaxAttempts (Docs/protocol/approval.md §Object). Caller does not
// hold s.mu.
func (s *Store) recordBadCode(ctx context.Context, id, via string) (attemptsLeft int, rejected bool, err error) {
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
		_ = s.audit.Append(ctx, "daemon", "approval.bad_code", map[string]any{"id": id, "attempts_left": attemptsLeft, "via": via})
		if rejected {
			_ = s.audit.Append(ctx, "daemon", "approval.reject", map[string]string{"id": id, "kind": kind, "subject": subject, "reason": "attempts", "via": via})
		}
	}
	return attemptsLeft, rejected, nil
}

// lockedEntry is what dropAllLocked hands to lockAll: enough to reject the
// row and kill any open window without holding s.mu.
type lockedEntry struct {
	id     string
	handle WindowHandle
}

// dropAllLocked removes every in-memory pending entry, stops its timer and
// returns enough of each to finish outside the lock. Caller holds s.mu.
func (s *Store) dropAllLocked() []lockedEntry {
	out := make([]lockedEntry, 0, len(s.pending))
	for id, e := range s.pending {
		if e.timer != nil {
			e.timer.Stop()
		}
		if e.watchCancel != nil {
			e.watchCancel()
		}
		if e.reserved {
			// No row yet: Create sees the slot gone, kills its window and
			// stores nothing (review 30, H1).
			continue
		}
		out = append(out, lockedEntry{id: id, handle: e.handle})
	}
	s.pending = map[string]*live{}
	return out
}

// sweepExpiredLocked marks every in-memory pending approval whose expiry has
// passed as expired (audited, notification withdrawn, window killed) and
// forgets it, so expired approvals neither count toward MaxPending nor show
// in List (review 26, M-1). Caller holds s.mu.
func (s *Store) sweepExpiredLocked(ctx context.Context, now time.Time) error {
	for id, e := range s.pending {
		if e.reserved {
			continue // no row yet (review 30, H1)
		}
		expired, _, err := s.checkExpiryLocked(ctx, id, now)
		if err != nil {
			return err
		}
		if expired {
			if e.timer != nil {
				e.timer.Stop()
			}
			if e.watchCancel != nil {
				e.watchCancel()
			}
			handle := e.handle
			delete(s.pending, id)
			if handle != nil {
				handle.Kill()
			}
			if s.notifier != nil {
				s.notifier.Remove(ctx, id)
			}
		}
	}
	return nil
}

// expireNow is the per-approval timer's callback (Docs/protocol/approval.md
// §The approval window, "a per-approval timer, not only the lazy sweep").
func (s *Store) expireNow(id string) {
	ctx := context.Background()
	s.mu.Lock()
	entry, ok := s.pending[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	if entry.watchCancel != nil {
		entry.watchCancel()
	}
	handle := entry.handle
	delete(s.pending, id)
	s.mu.Unlock()
	if handle != nil {
		handle.Kill()
	}
	now := s.now()
	decided := now.UTC().Format(storeTimeFmt)
	kind, subject := s.kindSubject(ctx, id)
	res, err := s.db.ExecContext(ctx, `UPDATE approvals SET state = 'expired', decided = ? WHERE id = ? AND state = 'pending'`, decided, id)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // already decided by a concurrent Confirm/Reject
	}
	if s.audit != nil {
		_ = s.audit.Append(ctx, "daemon", "approval.reject", map[string]string{"id": id, "kind": kind, "subject": subject, "reason": "expired"})
	}
	if s.notifier != nil {
		s.notifier.Remove(ctx, id)
	}
}

// decidedRetention is how long decided rows are kept (Docs/protocol/approval.md
// §Tables, "Decided rows are pruned after 30 days").
const decidedRetention = 30 * 24 * time.Hour

// pruneDecided best-effort deletes decided rows older than decidedRetention.
func (s *Store) pruneDecided(ctx context.Context, now time.Time) {
	cutoff := now.Add(-decidedRetention).UTC().Format(storeTimeFmt)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM approvals WHERE state <> 'pending' AND decided IS NOT NULL AND decided < ?`, cutoff)
}

// lockAll rejects the approvals (already dropped from memory under s.mu),
// kills any window each had open, and shows a desktop warning
// (Docs/protocol/approval.md §Object, the 10th wrong code in 24 h).
func (s *Store) lockAll(ctx context.Context, entries []lockedEntry) {
	for _, e := range entries {
		if e.handle != nil {
			e.handle.Kill()
		}
		s.rejectRow(ctx, e.id, "locked", "")
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
func (s *Store) rejectRow(ctx context.Context, id, reason, via string) {
	decided := s.now().UTC().Format(storeTimeFmt)
	kind, subject := s.kindSubject(ctx, id)
	_, _ = s.db.ExecContext(ctx, `UPDATE approvals SET state = 'rejected', decided = ? WHERE id = ?`, decided, id)
	if s.audit != nil {
		detail := map[string]string{"id": id, "kind": kind, "subject": subject, "reason": reason}
		if via != "" {
			detail["via"] = via
		}
		_ = s.audit.Append(ctx, "daemon", "approval.reject", detail)
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

// Reject rejects a pending approval (Docs/protocol/approval.md §Flow), tagged
// with via ("ipc" for approval_reject/--reject, "window" for the window's
// own Reject button, "terminal" for `reject <tag>` on the daemon's stdin).
// Any caller may reject: rejecting only removes access.
func (s *Store) Reject(ctx context.Context, id, via string) (View, error) {
	s.mu.Lock()
	entry, ok := s.pending[id]
	if !ok || entry.reserved {
		s.mu.Unlock()
		return View{}, s.unknownOrExpired(ctx, id)
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	if entry.watchCancel != nil {
		entry.watchCancel()
	}
	handle := entry.handle
	delete(s.pending, id)
	s.mu.Unlock()
	if handle != nil {
		handle.Kill()
	}
	s.rejectRow(ctx, id, "user", via)
	return s.Show(ctx, id)
}

// RejectSubjects rejects every still-pending approval whose subject is one of
// subjects, with reason "precondition" (Docs/protocol/grant.md §Session end:
// closing a session rejects its pending grant approvals; review 28 L8). It is
// called by the daemon after the closing transaction committed, never from
// inside a hook (review 26 N4). Approvals a concurrent confirm has already
// reserved are left to that confirm, whose Precondition fails.
func (s *Store) RejectSubjects(ctx context.Context, subjects []string) {
	if len(subjects) == 0 {
		return
	}
	var ids []string
	for _, subj := range subjects {
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM approvals WHERE state = 'pending' AND subject = ?`, subj)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		_ = rows.Close()
	}
	for _, id := range ids {
		s.mu.Lock()
		entry, ok := s.pending[id]
		if !ok || entry.reserved {
			s.mu.Unlock()
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		if entry.watchCancel != nil {
			entry.watchCancel()
		}
		handle := entry.handle
		delete(s.pending, id)
		s.mu.Unlock()
		if handle != nil {
			handle.Kill()
		}
		s.rejectRow(ctx, id, "precondition", "")
	}
}

// OpenWindow reopens the approval window for a pending id, a no-op if one is
// already open, both cases audited (Docs/protocol/approval.md §IPC and CLI,
// "approval_open"). In terminal mode it returns ErrTerminalMode.
func (s *Store) OpenWindow(ctx context.Context, id string) (View, error) {
	if s.window == nil {
		return View{}, ErrTerminalMode
	}
	s.mu.Lock()
	entry, ok := s.pending[id]
	if !ok || entry.reserved {
		s.mu.Unlock()
		return View{}, s.unknownOrExpired(ctx, id)
	}
	alreadyOpen := entry.handle != nil || entry.opening
	s.mu.Unlock()
	if s.audit != nil {
		_ = s.audit.Append(ctx, "cli", "approval.open", map[string]string{"id": id})
	}
	if alreadyOpen {
		return s.Show(ctx, id)
	}
	s.reopen(ctx, id, "")
	return s.Show(ctx, id)
}

// reopen opens a fresh window for a still-pending id, showing message (if
// any) in addition to the stored summary (Docs/protocol/approval.md §The
// approval window, "Outcome": "reopens the window with ..."). Best-effort:
// a failure here leaves the approval pending with no window, retryable
// through approval_open. It is a no-op while another window for id is open
// or being opened: at most one window per approval (review 30, M3).
func (s *Store) reopen(ctx context.Context, id, message string) {
	if s.window == nil {
		return
	}
	s.mu.Lock()
	entry, ok := s.pending[id]
	if !ok || entry.reserved || entry.opening || entry.handle != nil || s.closed {
		s.mu.Unlock()
		return
	}
	entry.opening = true
	s.mu.Unlock()
	handle := s.startWindow(ctx, id, message)

	s.mu.Lock()
	entry, ok = s.pending[id]
	if ok {
		entry.opening = false
	}
	if handle == nil {
		s.mu.Unlock()
		return
	}
	if !ok || s.closed {
		s.mu.Unlock()
		handle.Kill()
		return
	}
	entry.handle = handle
	s.mu.Unlock()
	s.startWatch(id, handle)
}

// startWindow starts and waits for a window for id, or returns nil.
func (s *Store) startWindow(ctx context.Context, id, message string) WindowHandle {
	view, err := s.Show(ctx, id)
	if err != nil || view.State != StatePending {
		return nil
	}
	expires, err := time.Parse(storeTimeFmt, view.Expires)
	if err != nil {
		return nil
	}
	summary := view.Summary
	if message != "" {
		summary = view.Summary + " " + message
	}
	handle, err := s.window.Start(ctx, id, tagOf(id), view.Kind, summary, expires)
	if err != nil || !handle.Ready(ctx) {
		if handle != nil {
			handle.Kill()
		}
		return nil
	}
	return handle
}

// startWatch runs a goroutine that waits for handle's answer and acts on it:
// approve with a 6-digit code confirms (reopening on a wrong code or a
// Perform error), a malformed approve reopens with a prompt, reject rejects,
// and dismiss leaves the approval pending with no window
// (Docs/protocol/approval.md §The approval window, "Answer format",
// "Outcome").
func (s *Store) startWatch(id string, handle WindowHandle) {
	wctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	entry, ok := s.pending[id]
	if !ok || entry.handle != handle {
		s.mu.Unlock()
		cancel()
		return
	}
	entry.watchCancel = cancel
	s.mu.Unlock()
	go func() {
		kind, code, err := handle.Answer(wctx)
		if err != nil {
			return // killed elsewhere (decide/expiry/lockout/stop)
		}
		s.onWindowAnswer(id, handle, kind, code)
	}()
}

func (s *Store) onWindowAnswer(id string, handle WindowHandle, kind, code string) {
	ctx := context.Background()
	// The dialog has exited after its one answer: forget it first, so the
	// reopen below (or a later approval_open) may start the next window.
	s.mu.Lock()
	if entry, ok := s.pending[id]; ok && entry.handle == handle {
		entry.handle = nil
		entry.watchCancel = nil
	}
	s.mu.Unlock()
	if kind != "dismiss" {
		handle.Kill() // a decision always ends the dialog, even a slow-exiting one
	}

	switch kind {
	case "reject":
		if _, err := s.Reject(ctx, id, "window"); err == nil {
			s.notifyOutcome(ctx, id, "Rejected")
		}
	case "approve":
		if !isSixDigits(code) {
			s.reopen(ctx, id, `Enter the 6-digit code from the notification`)
			return
		}
		_, err := s.confirm(ctx, id, code, "window")
		var bce *BadCodeError
		switch {
		case err == nil:
			s.notifyOutcome(ctx, id, "Approved")
		case errors.As(err, &bce):
			if s.stillPending(id) {
				s.reopen(ctx, id, fmt.Sprintf("Wrong code, %d attempts left", bce.AttemptsLeft))
			} else {
				s.notifyOutcome(ctx, id, "Rejected: too many wrong codes")
			}
		case s.stillPending(id):
			// A Perform error leaves the approval pending: retry or reject.
			s.reopen(ctx, id, "Could not complete, try again or reject")
		default:
			s.notifyOutcome(ctx, id, fmt.Sprintf("Not approved: %v", err))
		}
	default: // dismiss: pending with no window until expiry or approval_open
	}
}

func (s *Store) stillPending(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pending[id]
	return ok
}

// isSixDigits reports whether v is exactly 6 ASCII digits
// (Docs/protocol/approval.md §The approval window, "Answer format": "An
// approve whose value is not exactly 6 ASCII digits is not counted as a
// wrong code").
func isSixDigits(v string) bool {
	if len(v) != 6 {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// notifyOutcome shows a short desktop notification without a code
// (Docs/protocol/approval.md §The approval window, "Outcome"). Best-effort.
func (s *Store) notifyOutcome(ctx context.Context, id, text string) {
	if s.notifier == nil {
		return
	}
	_ = s.notifier.Show(ctx, "outcome-"+id, s.now().Add(time.Minute), "AgentNet", text)
}

// windowState is the View.Window field for id: "terminal" (no window at
// all), or "open"/"closed" in desktop mode (Docs/protocol/approval.md §IPC
// and CLI).
func (s *Store) windowState(id string) string {
	if s.window == nil {
		return "terminal"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.pending[id]; ok && e.handle != nil {
		return "open"
	}
	return "closed"
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
	v.Window = s.windowState(id)
	return v, nil
}

// List returns every pending approval, oldest first, never a code
// (Docs/protocol/approval.md §IPC and CLI).
func (s *Store) List(ctx context.Context) ([]View, error) {
	s.mu.Lock()
	err := s.sweepExpiredLocked(ctx, s.now())
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
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
		v.Window = s.windowState(v.ID)
		out = append(out, v)
	}
	return out, rows.Err()
}
