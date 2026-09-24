//go:build unix

package device

import (
	"os/exec"
	"sync"
	"syscall"
)

// processTree is a started command in its own process group, so killing the
// group ends every process it started that stayed in the group.
type processTree struct {
	pgid int
	mu   sync.Mutex
	done bool
}

// startTree starts cmd as the leader of a new process group.
func startTree(cmd *exec.Cmd) (*processTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &processTree{pgid: cmd.Process.Pid}, nil
}

// kill sends SIGKILL to the whole group. A group whose processes have all
// exited no longer exists (ESRCH), and its id cannot be reused while any
// member lives.
func (t *processTree) kill() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.done && t.pgid > 0 {
		_ = syscall.Kill(-t.pgid, syscall.SIGKILL)
	}
}

// close ends the tree's use; nothing is held open on Unix.
func (t *processTree) close() {
	t.mu.Lock()
	t.done = true
	t.mu.Unlock()
}
