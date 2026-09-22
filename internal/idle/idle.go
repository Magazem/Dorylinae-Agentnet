// Package idle reports how long the user has been away from the keyboard and
// mouse, using a per-OS mechanism. It never returns an error: when the idle time
// cannot be determined the answer is "unknown", which presence reports as
// human: 2 (Docs/protocol/presence.md, Idle detection).
package idle

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

// Timeout is the hard limit for one Idle call.
const Timeout = time.Second

// Idle returns the time since the last user input. The bool is false when the
// idle time is unknown (unsupported platform, failed query, or timeout).
func Idle(ctx context.Context) (time.Duration, bool) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	d, err := query(ctx)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

var errNoMatch = errors.New("idle: no idle time in output")

// runCommand runs name with args, never through a shell, and returns stdout.
// It is a variable so tests can substitute a fake. A command that ignores the
// context deadline is abandoned after a short grace period.
var runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // name/args are fixed per-OS constants, never built from strings
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.WaitDelay = 100 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

var (
	ioregRe = regexp.MustCompile(`"HIDIdleTime"\s*=\s*(\d+)`)
	gdbusRe = regexp.MustCompile(`^\(\s*u?int(?:32|64)\s+(\d+)\s*,?\s*\)$`)
	msRe    = regexp.MustCompile(`^(\d+)$`)
)

// parseIoreg parses `ioreg -c IOHIDSystem` output; HIDIdleTime is nanoseconds.
func parseIoreg(out []byte) (time.Duration, error) {
	m := ioregRe.FindSubmatch(out)
	if m == nil {
		return 0, errNoMatch
	}
	n, err := strconv.ParseUint(string(m[1]), 10, 63)
	if err != nil {
		return 0, err
	}
	return time.Duration(n), nil
}

// parseGdbus parses a gdbus reply such as `(uint64 1234,)` or `(uint32 1234,)`
// (milliseconds).
func parseGdbus(out []byte) (time.Duration, error) {
	return parseMillis(gdbusRe, out)
}

// parseXprintidle parses xprintidle output, an integer in milliseconds.
func parseXprintidle(out []byte) (time.Duration, error) {
	return parseMillis(msRe, out)
}

func parseMillis(re *regexp.Regexp, out []byte) (time.Duration, error) {
	m := re.FindSubmatch(bytes.TrimSpace(out))
	if m == nil {
		return 0, errNoMatch
	}
	n, err := strconv.ParseUint(string(m[1]), 10, 63)
	if err != nil {
		return 0, err
	}
	if n > uint64(1<<62)/uint64(time.Millisecond) {
		return 0, errNoMatch
	}
	return time.Duration(n) * time.Millisecond, nil
}
