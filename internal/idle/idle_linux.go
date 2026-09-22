//go:build linux

package idle

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

type step struct {
	parse func([]byte) (time.Duration, error)
	name  string
	args  []string
}

// query tries Mutter, then the freedesktop ScreenSaver, then xprintidle; the
// first success wins.
func query(ctx context.Context) (time.Duration, error) {
	steps := []step{
		{parseGdbus, "gdbus", []string{"call", "--session", "--dest", "org.gnome.Mutter.IdleMonitor",
			"--object-path", "/org/gnome/Mutter/IdleMonitor/Core", "--method", "org.gnome.Mutter.IdleMonitor.GetIdletime"}},
		{parseGdbus, "gdbus", []string{"call", "--session", "--dest", "org.freedesktop.ScreenSaver",
			"--object-path", "/org/freedesktop/ScreenSaver", "--method", "org.freedesktop.ScreenSaver.GetSessionIdleTime"}},
	}
	if os.Getenv("DISPLAY") != "" {
		if _, err := exec.LookPath("xprintidle"); err == nil {
			steps = append(steps, step{parseXprintidle, "xprintidle", nil})
		}
	}
	err := errors.New("idle: no mechanism available")
	for _, s := range steps {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		out, runErr := runCommand(ctx, s.name, s.args...)
		if runErr != nil {
			err = runErr
			continue
		}
		d, parseErr := s.parse(out)
		if parseErr != nil {
			err = parseErr
			continue
		}
		return d, nil
	}
	return 0, err
}
