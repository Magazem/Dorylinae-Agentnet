//go:build darwin

package idle

import (
	"context"
	"time"
)

func query(ctx context.Context) (time.Duration, error) {
	out, err := runCommand(ctx, "/usr/sbin/ioreg", "-c", "IOHIDSystem", "-d", "4", "-r", "-k", "HIDIdleTime")
	if err != nil {
		return 0, err
	}
	return parseIoreg(out)
}
