//go:build !windows && !darwin && !linux

package idle

import (
	"context"
	"errors"
	"time"
)

func query(context.Context) (time.Duration, error) {
	return 0, errors.New("idle: unsupported platform")
}
