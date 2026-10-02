//go:build windows

package ipc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// Review 60b F8b-04: the old pipe name is pinned to a vector computed outside
// the code (sha256 of the dir as spelled, first 8 bytes), so a changed
// formula, such as lower-casing the dir, fails.
func TestLegacyEndpointVector(t *testing.T) {
	const want = `\\.\pipe\dorylinae-be041a3b321a2fa8`
	if got := legacyEndpoint(`C:\Users\Alice\AppData\Roaming\dorylinae`); got != want {
		t.Fatalf("legacyEndpoint = %s, want %s", got, want)
	}
}

// busyPipe creates one instance of name and connects a client to it,
// so every further dial finds the pipe busy until the test ends. More
// instances are allowed, so creating one more fails as a name collision.
func busyPipe(t *testing.T, name string) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := windows.CreateNamedPipe(p, windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT, windows.PIPE_UNLIMITED_INSTANCES, 512, 512, 0, nil)
	if err != nil {
		t.Fatalf("CreateNamedPipe: %v", err)
	}
	cli, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		_ = windows.CloseHandle(srv)
		t.Fatalf("connect to %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = windows.CloseHandle(cli)
		_ = windows.CloseHandle(srv)
	})
}

// Review 60 F4, pinned by 60b F8b-04: a pipe that exists but does not answer
// is not reported as our daemon.
func TestListenBusyPipeOwnerUnknown(t *testing.T) {
	ep := `\\.\pipe\dorylinae-test-busy-` + strings.ReplaceAll(t.Name(), "/", "-")
	busyPipe(t, ep)
	ln, err := Listen(ep)
	if err == nil {
		_ = ln.Close()
		t.Fatal("Listen on a busy pipe succeeded")
	}
	if errors.Is(err, ErrAlreadyRunning) || errors.Is(err, ErrForeignOwner) || !strings.Contains(err.Error(), "owner unknown") {
		t.Fatalf("Listen: %v, want owner unknown", err)
	}
}

// Review 60b F8b-02: an old pipe name that is in use refuses the start even
// when it does not answer as ours: an older daemon started elevated (its pipe
// is owned by BUILTIN\Administrators), or one that is busy. The refusal names
// how to stop the older daemon (F8b-03), and the instance lock is released.
func TestLockInstanceRefusesUnansweredLegacyPipe(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hold    func(t *testing.T, ep string)
		foreign bool
	}{
		{"elevated", func(t *testing.T, ep string) {
			ln, err := winio.ListenPipe(ep, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;SY)"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ln.Close() })
		}, true},
		{"busy", busyPipe, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.hold(t, legacyEndpoint(dir))
			l, err := LockInstance(dir)
			if err == nil {
				_ = l.Close()
				t.Fatal("LockInstance beside an unanswered old pipe succeeded")
			}
			if errors.Is(err, ErrForeignOwner) != tc.foreign || errors.Is(err, ErrAlreadyRunning) {
				t.Fatalf("LockInstance: %v", err)
			}
			if !strings.Contains(err.Error(), `"agentnetd stop"`) {
				t.Fatalf("LockInstance: %v, want how to stop the older agentnetd", err)
			}
			// The refusal released the instance lock.
			f, err := os.OpenFile(filepath.Join(dir, InstanceLock), os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped)); err != nil {
				t.Fatalf("instance lock still held after the refusal: %v", err)
			}
		})
	}
}
