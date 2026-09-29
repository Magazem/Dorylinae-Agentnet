package ipc_test

import (
	"errors"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

// Review 60 F2 / review 55 C28-03: one instance lock per config dir, taken
// before the database is opened, on every OS.
func TestLockInstanceOneHolder(t *testing.T) {
	dir := t.TempDir()
	first, err := ipc.LockInstance(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := ipc.LockInstance(dir); !errors.Is(err, ipc.ErrAlreadyRunning) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second LockInstance: %v, want ErrAlreadyRunning", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := ipc.LockInstance(dir)
	if err != nil {
		t.Fatalf("LockInstance after release: %v", err)
	}
	_ = again.Close()
}
