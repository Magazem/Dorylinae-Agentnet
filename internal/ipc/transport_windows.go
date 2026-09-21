//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const dialTimeout = 1 * time.Second

// Listen creates a named pipe at endpoint accessible only to the current user.
// go-winio opens the first instance exclusively, so a second daemon on the same
// endpoint fails here.
func Listen(endpoint string) (net.Listener, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("lookup current user: %w", err)
	}
	sd := "D:P(A;;GA;;;" + user.User.Sid.String() + ")"
	ln, err := winio.ListenPipe(endpoint, &winio.PipeConfig{SecurityDescriptor: sd})
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", endpoint, err)
	}
	return ln, nil
}

// Dial connects to the daemon. It returns ErrNotRunning when the pipe does not exist.
func Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	c, err := winio.DialPipeContext(ctx, endpoint)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return nil, ErrNotRunning
		}
		return nil, fmt.Errorf("ipc dial: %w", err)
	}
	return c, nil
}
