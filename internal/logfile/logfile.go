// Package logfile is a size-capped log file: when a write would push it past
// the limit, the current file is renamed to <path>.1 (replacing any earlier
// one) and a fresh file is started. One generation is kept, so the log never
// grows beyond about twice the limit.
package logfile

import (
	"fmt"
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
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *Writer) rotate() error {
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("close log file: %w", err)
	}
	w.f = nil
	// Windows will not rename over an existing file on every filesystem.
	if err := os.Remove(w.path + ".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old log: %w", err)
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil {
		return fmt.Errorf("rotate log file: %w", err)
	}
	return w.open()
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
