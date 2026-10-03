package approval

import (
	"context"
	"sync"
	"time"
)

// WindowCheckTTL is how long a window check result is cached
// (Docs/protocol/approval.md §The approval window, "How the check works").
const WindowCheckTTL = 60 * time.Second

// windowCheckBudget is the time one background check may take: the Linux
// lookup is a D-Bus call, and status keeps the IPC 2-second rule.
const windowCheckBudget = time.Second

// UnavailableError is ErrUnavailable with the reason a window could not be
// shown. It matches ErrUnavailable. Window is false for a failing notifier.
// Fix is one of the fixed strings of WindowRunner.Check, "" when none is
// known, and never OS or D-Bus error text (R55-125, review 84b F9).
type UnavailableError struct {
	Window bool
	Fix    string
}

func (e *UnavailableError) Error() string { return ErrUnavailable.Error() }

// Is reports whether target is ErrUnavailable, so errors.Is matches it.
func (e *UnavailableError) Is(target error) bool {
	return target == ErrUnavailable
}

// FixBlockedByPolicy is the fix named when a Windows window process wrote the
// Constrained Language Mode error while opening.
const FixBlockedByPolicy = "PowerShell/WinForms blocked by policy"

// blockedHandle is implemented by a WindowHandle that can tell its window
// process was blocked by policy (Windows Constrained Language Mode).
type blockedHandle interface {
	BlockedByPolicy() bool
}

// windowCache is the cached result of WindowRunner.Check.
type windowCache struct {
	mu      sync.Mutex
	have    bool
	ok      bool
	fix     string
	at      time.Time
	running bool
}

// CheckWindow runs the window check synchronously and caches its result. The
// daemon calls it once at start; status uses WindowStatus. With no window
// (terminal mode) it does nothing.
func (s *Store) CheckWindow(ctx context.Context) {
	if s.window == nil {
		return
	}
	ok, fix := s.window.Check(ctx)
	s.wcache.mu.Lock()
	s.wcache.have, s.wcache.ok, s.wcache.fix, s.wcache.at = true, ok, fix, s.now()
	s.wcache.mu.Unlock()
}

// WindowStatus returns the cached window check: ok, the fix when it is not
// ok, and whether there is a window to check at all (false in terminal mode).
// It never waits for a check. When the cached value is older than
// WindowCheckTTL (or there is none) it starts one refresh in the background,
// at most one at a time, with a 1 s budget.
func (s *Store) WindowStatus() (ok bool, fix string, applicable bool) {
	if s.window == nil {
		return false, "", false
	}
	c := &s.wcache
	c.mu.Lock()
	stale := !c.have || s.now().Sub(c.at) >= WindowCheckTTL
	start := stale && !c.running
	if start {
		c.running = true
	}
	have, ok, fix := c.have, c.ok, c.fix
	c.mu.Unlock()
	if start {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), windowCheckBudget)
			defer cancel()
			s.CheckWindow(ctx)
			c.mu.Lock()
			c.running = false
			c.mu.Unlock()
		}()
	}
	if !have {
		return true, "", true // nothing known yet: do not report a fault
	}
	return ok, fix, true
}

// lastFix is the fix of the last check, "" if it was ok or none ran.
func (s *Store) lastFix() string {
	s.wcache.mu.Lock()
	defer s.wcache.mu.Unlock()
	if s.wcache.have && !s.wcache.ok {
		return s.wcache.fix
	}
	return ""
}

// windowUnavailable builds the error of a window that did not become ready.
func (s *Store) windowUnavailable(h WindowHandle) error {
	fix := s.lastFix()
	if b, ok := h.(blockedHandle); ok && b.BlockedByPolicy() {
		fix = FixBlockedByPolicy
	}
	return &UnavailableError{Window: true, Fix: fix}
}
