package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

// stopPollInterval is how often StopWait re-checks whether the daemon has exited.
const stopPollInterval = 100 * time.Millisecond

// stopCallTimeout bounds the initial "shutdown" call itself, so a daemon that
// accepts the connection but never answers cannot make StopWait hang forever
// on top of its own timeout budget.
const stopCallTimeout = 2 * time.Second

// ErrStopTimeout means the daemon did not stop within StopWait's timeout.
var ErrStopTimeout = errors.New("daemon did not stop in time")

// ShutdownResult is the result of the "shutdown" IPC method
// (Docs/protocol/ipc.md §shutdown).
type ShutdownResult struct {
	OK bool `json:"ok"`
}

// StopWait asks the daemon at endpoint to shut down over IPC (the same path
// as Ctrl+C/SIGTERM: outbox flushed, database closed, audit row written) and
// waits up to timeout for it to stop answering. It returns ipc.ErrNotRunning
// if nothing was listening, or ErrStopTimeout if the daemon never exited.
func StopWait(ctx context.Context, endpoint string, timeout time.Duration) error {
	cctx, cancel := context.WithTimeout(ctx, stopCallTimeout)
	err := ipc.Call(cctx, endpoint, "shutdown", nil, new(ShutdownResult))
	cancel()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, err := ipc.Dial(dctx, endpoint)
		cancel()
		if err != nil {
			if errors.Is(err, ipc.ErrNotRunning) {
				return nil
			}
		} else {
			_ = conn.Close()
		}
		if time.Now().After(deadline) {
			return ErrStopTimeout
		}
		time.Sleep(stopPollInterval)
	}
}
