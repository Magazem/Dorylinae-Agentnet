//go:build !windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const dialTimeout = 1 * time.Second

// expectedUID returns the uid the socket and the daemon must run as. Tests
// replace it to play a socket held by another user.
var expectedUID = os.Geteuid

// Listen creates a Unix socket at endpoint with mode 0600. It holds an
// exclusive lock on endpoint+".lock" for the listener's lifetime, so two
// daemons starting at once cannot both find the socket stale. A stale socket
// file (nothing answering) owned by the current user is removed; a live one
// yields ErrAlreadyRunning, and one held by another user ErrForeignOwner.
func Listen(endpoint string) (net.Listener, error) {
	lock, err := lockEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	ln, err := listenLocked(endpoint)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	return &lockedListener{Listener: ln, lock: lock}, nil
}

// LockInstance takes the exclusive lock on dir/InstanceLock, held until the
// returned Closer is closed. The daemon takes it before opening the database,
// so a losing second daemon does not migrate the DB under the running one
// (review 55 C28-03). A held lock yields ErrAlreadyRunning.
func LockInstance(dir string) (io.Closer, error) {
	return lockFile(filepath.Join(dir, InstanceLock))
}

func lockEndpoint(endpoint string) (*os.File, error) { return lockFile(endpoint + ".lock") }

func lockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // in the 0700 config dir
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	if fi, err := f.Stat(); err == nil && !ownedByMe(fi) {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w: lock file is owned by another user", path, ErrForeignOwner)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s is locked by another agentnetd", ErrAlreadyRunning, path)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f, nil
}

func listenLocked(endpoint string) (net.Listener, error) {
	// A daemon that predates the lock file still answers on the socket.
	if c, err := Dial(context.Background(), endpoint); err == nil {
		_ = c.Close()
		return nil, fmt.Errorf("%w: socket %s is already in use", ErrAlreadyRunning, endpoint)
	} else if errors.Is(err, ErrForeignOwner) {
		return nil, fmt.Errorf("listen on %s: %w", endpoint, err)
	}
	fi, err := os.Lstat(endpoint)
	switch {
	case err == nil:
		if !ownedByMe(fi) {
			return nil, fmt.Errorf("listen on %s: %w: socket file is owned by another user", endpoint, ErrForeignOwner)
		}
		if err := os.Remove(endpoint); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("stat %s: %w", endpoint, err)
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

func ownedByMe(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == expectedUID()
}

// lockedListener releases the lock after the socket is closed (and unlinked).
type lockedListener struct {
	net.Listener
	lock *os.File
}

func (l *lockedListener) Close() error {
	err := l.Listener.Close()
	_ = l.lock.Close()
	return err
}

// Dial connects to the daemon. It returns ErrNotRunning when nothing listens,
// and ErrForeignOwner, before anything is sent, when the process serving the
// socket runs as another user.
func Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	d := net.Dialer{Timeout: dialTimeout}
	c, err := d.DialContext(ctx, "unix", endpoint)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, ErrNotRunning
		}
		return nil, fmt.Errorf("ipc dial: %w", err)
	}
	if err := checkPeer(c, endpoint); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func checkPeer(c net.Conn, endpoint string) error {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("ipc dial: %s is not a unix socket", endpoint)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return fmt.Errorf("ipc dial: %w", err)
	}
	var uid int
	var perr error
	if err := raw.Control(func(fd uintptr) { uid, perr = peerUID(int(fd)) }); err != nil {
		return fmt.Errorf("ipc dial: %w", err)
	}
	switch {
	case perr != nil:
		return fmt.Errorf("ipc dial: read peer of %s: %w", endpoint, perr)
	case uid != expectedUID():
		return fmt.Errorf("%w: socket %s is served by uid %d", ErrForeignOwner, endpoint, uid)
	}
	return nil
}
