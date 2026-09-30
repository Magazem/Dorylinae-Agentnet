//go:build windows

package ipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
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
		return pipeListener{ln}, nil
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
		// Busy or gone between the two calls. The daemon holds LockInstance
		// before it listens, so this is not known to be our own daemon.
		return nil, fmt.Errorf("listen on %s: pipe is in use but did not answer, owner unknown: %w", endpoint, derr)
	}
}

// closeRetry is how long pipeListener.Close waits before it asks again.
const closeRetry = 50 * time.Millisecond

// pipeListener repeats Close until go-winio's listener has stopped. In
// go-winio v0.6.2 a close request that races a pending ConnectNamedPipe can be
// consumed without stopping the listener: when the aborted connect reports
// ERROR_NO_DATA (a client came and went) the listener loops back to wait for a
// client and Accept never returns; ERROR_OPERATION_ABORTED is handed to Accept
// and Close waits forever. Either way Serve, and so the daemon, would not stop
// (CI flake of TestSecondListenerRefused). A second close request reaches the
// listener wherever it waits. Fixed upstream in microsoft/go-winio#388, not
// yet released.
type pipeListener struct{ net.Listener }

func (l pipeListener) Close() error {
	done := make(chan struct{})
	go func() {
		_ = l.Listener.Close()
		close(done)
	}()
	t := time.NewTicker(closeRetry)
	defer t.Stop()
	for {
		select {
		case <-done:
			return nil
		case <-t.C:
			// Returns once the listener has stopped, like the first call.
			go func() { _ = l.Listener.Close() }()
		}
	}
}

// LockInstance takes an exclusive lock on dir\InstanceLock, held until the
// returned Closer is closed. The pipe name is not a lock of its own: it
// changed in review 55 (R55-088), and the daemon takes this lock before it
// opens the database, so a losing second daemon does not migrate the DB
// under the running one (review 55 C28-03). A held lock, or a daemon from
// before the lock answering on the old pipe name, yields ErrAlreadyRunning.
func LockInstance(dir string) (io.Closer, error) {
	path := filepath.Join(dir, InstanceLock)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // in our own config dir
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
	if err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, fmt.Errorf("%w: %s is locked by another agentnetd", ErrAlreadyRunning, path)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	old := legacyEndpoint(dir)
	if c, err := Dial(context.Background(), old); err == nil {
		_ = c.Close()
		_ = f.Close()
		return nil, fmt.Errorf("%w: an older agentnetd serves pipe %s", ErrAlreadyRunning, old)
	}
	return f, nil
}

// legacyEndpoint is the pipe name before review 55 (R55-088): a hash of the
// absolute dir as spelled.
func legacyEndpoint(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return `\\.\pipe\dorylinae-` + hex.EncodeToString(sum[:8])
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
