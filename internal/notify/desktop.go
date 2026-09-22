package notify

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// showTimeout is the per-call timeout (Docs/protocol/notify.md §Desktop).
const showTimeout = 3 * time.Second

// Desktop shows a desktop notification through the OS-specific mechanism in
// desktop_darwin.go, desktop_linux.go, desktop_windows.go and
// desktop_other.go. The zero value is ready to use.
type Desktop struct{}

// Show displays a notification with title and body, bounded to a 3 s
// timeout. Peer-supplied text never reaches a shell or script source: it is
// passed as separate argv elements or through an API that takes plain
// strings (Docs/protocol/notify.md §Desktop).
func (d Desktop) Show(ctx context.Context, title, body string) error {
	ctx, cancel := context.WithTimeout(ctx, showTimeout)
	defer cancel()
	return showDesktop(ctx, title, body)
}

// run executes name with args, never through a shell. env, when non-nil, is
// appended to the child's environment. It is a variable so tests can
// substitute a fake exec runner that records argv and env instead of
// spawning a real process (Docs/review/11-phase1-tickets.md, 1.8a
// acceptance: "use a fake exec runner").
var run = func(ctx context.Context, name string, args []string, env []string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // name is a fixed per-OS constant; args are argv elements, never shell text
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.WaitDelay = 200 * time.Millisecond
	return cmd.Run()
}
