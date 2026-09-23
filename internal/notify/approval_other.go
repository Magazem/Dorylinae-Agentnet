//go:build !windows && !linux && !darwin

package notify

import (
	"context"
	"errors"
	"time"
)

func showApproval(context.Context, string, time.Time, string, string) error {
	return errors.New("notify: approval notifications are not supported on this platform")
}

func removeApproval(context.Context, string) {}
