package main

import (
	"fmt"
	"io"
	"os"
)

// maxScopeFileBytes bounds device scope --from-file: a scope is a small JSON
// document.
const maxScopeFileBytes = 256 << 10

// readBounded reads at most max bytes of the --*-from-file argument path:
// "-" reads stdin, anything else must be a regular file (after symlinks), so
// a FIFO or device cannot block the CLI (review 55 R55-087). A longer input
// is refused without reading it all. what names the thing for the over-limit
// error, e.g. "the brief".
func readBounded(path string, stdin io.Reader, max int64, what string) ([]byte, error) {
	name := path
	r := stdin
	if path == "-" {
		name = "stdin"
	} else {
		f, err := os.Open(path) //nolint:gosec // the path is a user-supplied CLI flag, as intended
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		fi, err := f.Stat()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("read %s: not a regular file", path)
		}
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("read %s: over %d bytes; %s is limited", name, max, what)
	}
	return b, nil
}
