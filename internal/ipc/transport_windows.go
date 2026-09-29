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

// currentUser returns the SID the pipe must be owned by. Tests replace it to
// play a pipe owned by another user, which one account cannot create.
var currentUser = func() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("lookup current user: %w", err)
	}
	return user.User.Sid, nil
}

// Listen creates a named pipe at endpoint owned by, and accessible only to,
// the current user. go-winio opens the first instance exclusively, so Listen
// fails when the name exists: ErrAlreadyRunning when our own daemon holds it,
// ErrForeignOwner when another user created it first (Docs/protocol/ipc.md
// §Endpoint).
func Listen(endpoint string) (net.Listener, error) {
	sid, err := currentUser()
	if err != nil {
		return nil, err
	}
	// Owner set explicitly: an elevated token would otherwise make
	// BUILTIN\Administrators the owner, and clients check the owner.
	sd := "O:" + sid.String() + "D:P(A;;GA;;;" + sid.String() + ")"
	ln, err := winio.ListenPipe(endpoint, &winio.PipeConfig{SecurityDescriptor: sd})
	if err == nil {
		return ln, nil
	}
	// An existing pipe name fails the exclusive first-instance create with
	// access denied, not a name collision. Ask the pipe who owns it.
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil, fmt.Errorf("listen on %s: %w", endpoint, err)
	}
	c, derr := Dial(context.Background(), endpoint)
	switch {
	case derr == nil:
		_ = c.Close()
		return nil, fmt.Errorf("%w: pipe %s is already in use", ErrAlreadyRunning, endpoint)
	case errors.Is(derr, ErrForeignOwner):
		return nil, fmt.Errorf("listen on %s: %w", endpoint, derr)
	default:
		// Busy or gone between the two calls: most likely our own daemon.
		return nil, fmt.Errorf("%w: pipe %s is in use (%w)", ErrAlreadyRunning, endpoint, derr)
	}
}

// Dial connects to the daemon. It returns ErrNotRunning when the pipe does not
// exist, and ErrForeignOwner, before anything is sent, when the pipe is not
// owned by the current user.
func Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	c, err := winio.DialPipeContext(ctx, endpoint)
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
			return nil, ErrNotRunning
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			// Our own pipe grants the current user full access.
			return nil, fmt.Errorf("%w: pipe %s denies the current user", ErrForeignOwner, endpoint)
		}
		return nil, fmt.Errorf("ipc dial: %w", err)
	}
	if err := checkOwner(c, endpoint); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// checkOwner reads the owner SID of the connected pipe from its handle. Only
// the creator can set it to the current user's SID (short of restore
// privilege), so a pipe squatted by another user fails here.
func checkOwner(c net.Conn, endpoint string) error {
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return fmt.Errorf("ipc dial: no handle for pipe %s", endpoint)
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("ipc dial: read owner of pipe %s: %w", endpoint, err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("%w: pipe %s has no readable owner", ErrForeignOwner, endpoint)
	}
	me, err := currentUser()
	if err != nil {
		return err
	}
	if !owner.Equals(me) {
		return fmt.Errorf("%w: pipe %s is owned by %s", ErrForeignOwner, endpoint, owner.String())
	}
	return nil
}
