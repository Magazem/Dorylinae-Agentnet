// Package logfile is a size-capped log file: when a write would push it past
// the limit, the current file is renamed to <path>.1 (replacing any earlier
// one) and a fresh file is started. One generation is kept, so the log never
// grows beyond about twice the limit.
package logfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

// MaxSize is the default size at which the log rotates: 1 MiB.
const MaxSize = 1 << 20

// Writer appends to a rotating log file. It is safe for concurrent use.
type Writer struct {
	path  string
	limit int64

	mu   sync.Mutex
	f    *os.File
	size int64
	// stuck is set after a failed rotation: further writes append to the
	// current file without retrying until the hard cap truncates it.
	stuck bool
}

// Open opens (creating if needed) the log at path for appending and rotates it
// once it would exceed limit bytes. A limit of zero or less means MaxSize.
func Open(path string, limit int64) (*Writer, error) {
	if limit <= 0 {
		limit = MaxSize
	}
	w := &Writer{path: path, limit: limit}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) open() error {
	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // operator-chosen log path
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("stat log file: %w", err)
	}
	w.f, w.size = f, fi.Size()
	return nil
}

// Write implements io.Writer. A single write is never split across files.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.limit {
		switch {
		case !w.stuck:
			// A failed rotation leaves the file reopened for appending; keep
			// logging into it and do not retry until the cap is reached.
			if err := w.rotate(); err != nil {
				if w.f == nil {
					return 0, err
				}
				w.stuck = true
			}
		case w.size+int64(len(p)) > 2*w.limit:
			// Rotation keeps failing (a viewer holds the log): enforce the
			// size cap by truncating in place, keeping the previous generation. The append handle
			// cannot truncate on Windows, so go through the path.
			if err := os.Truncate(w.path, 0); err == nil {
				w.size = 0
				w.stuck = false
			}
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate renames the log to <path>.1 and starts a new file. If a step fails
// (on Windows a log viewer without FILE_SHARE_DELETE makes the rename fail) it
// reopens the current file in append mode, so one failed rotation does not end
// logging for the process's lifetime (review 55 R55-093).
func (w *Writer) rotate() error {
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("close log file: %w", err)
	}
	w.f = nil
	if err := w.moveAside(); err != nil {
		if rerr := w.open(); rerr != nil {
			return fmt.Errorf("%w (reopening the log failed: %w)", err, rerr)
		}
		return err
	}
	return w.open()
}

func (w *Writer) moveAside() error {
	// Rename first: it replaces an existing .1 on Windows and POSIX, so a
	// failure (a viewer holds the log) leaves the previous generation intact.
	// Only if it fails with "exists" is the old .1 removed and the rename
	// retried, for filesystems that will not rename over an existing file.
	err := os.Rename(w.path, w.path+".1")
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrExist) || os.Remove(w.path+".1") != nil {
		return fmt.Errorf("rotate log file: %w", err)
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		return fmt.Errorf("rotate log file: %w", err)
	}
	return nil
}

// Close closes the file. Later writes fail with os.ErrClosed.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
