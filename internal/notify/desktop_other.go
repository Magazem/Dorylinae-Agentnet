//go:build !darwin && !linux && !windows

package notify

import (
	"context"
	"errors"
)

// showDesktop is unsupported on any other GOOS. CI cross-compiles only
// darwin, linux and windows (Docs/review/11-phase1-tickets.md 1.8a).
func showDesktop(context.Context, string, string) error {
	return errors.New("notify: desktop notifications are not supported on this platform")
}
