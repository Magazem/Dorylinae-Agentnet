package daemon_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 60b F8b-04 (review 55 C28-03): a second daemon for a home whose
// instance lock is held stops before it opens, creates or migrates the
// database.
func TestRunStopsBeforeStoreWhenLocked(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file") // never touch the real keychain from tests
	p, err := paths.In(filepath.Join(testutil.TempDir(t), "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	lock, err := ipc.LockInstance(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := daemon.Run(ctx, p, nil); !errors.Is(err, ipc.ErrAlreadyRunning) {
		t.Fatalf("Run: %v, want ErrAlreadyRunning", err)
	}
	if _, err := os.Stat(p.DB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database touched by the losing daemon: %v", err)
	}
}
