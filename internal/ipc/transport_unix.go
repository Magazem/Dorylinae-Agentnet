//go:build !windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

const dialTimeout = 1 * time.Second

// Listen creates a Unix socket at endpoint with mode 0600. A stale socket file
// (nothing answering) is removed; a live one yields an error.
func Listen(endpoint string) (net.Listener, error) {
	if c, err := net.DialTimeout("unix", endpoint, dialTimeout); err == nil {
		_ = c.Close()
		return nil, fmt.Errorf("daemon already running on %s", endpoint)
	}
	if err := os.Remove(endpoint); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	// Create with a restrictive umask so the socket is never briefly accessible.
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", endpoint)
	syscall.Umask(old)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", endpoint, err)
	}
	return ln, nil
}

// Dial connects to the daemon. It returns ErrNotRunning when nothing listens.
func Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	d := net.Dialer{Timeout: dialTimeout}
	c, err := d.DialContext(ctx, "unix", endpoint)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, ErrNotRunning
		}
		return nil, fmt.Errorf("ipc dial: %w", err)
	}
	return c, nil
}
