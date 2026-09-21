package logfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // path under testutil.TempDir(t)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRotatesToDotOne(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "agentnetd.log")
	w, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	for _, s := range []string{"aaaaaa", "bbbbbb", "cccccc"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(t, path); got != "cccccc" {
		t.Errorf("current = %q", got)
	}
	if got := read(t, path+".1"); got != "bbbbbb" {
		t.Errorf(".1 = %q, want only the previous generation", got)
	}
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Error("unexpected second generation")
	}
}

func TestAppendsAcrossOpensAndCountsExistingSize(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "agentnetd.log")
	w, err := Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("123456"))
	_ = w.Close()

	w, err = Open(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	_, _ = w.Write([]byte("7890"))
	if got := read(t, path); got != "1234567890" {
		t.Errorf("file = %q; a write that fits must not rotate", got)
	}
	_, _ = w.Write([]byte("x"))
	if read(t, path) != "x" || read(t, path+".1") != "1234567890" {
		t.Error("existing size was not counted toward rotation")
	}
}

func TestOversizeWriteIsNotSplit(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "agentnetd.log")
	w, err := Open(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if n, err := w.Write([]byte("0123456789")); err != nil || n != 10 {
		t.Fatalf("write = %d, %v", n, err)
	}
	if got := read(t, path); got != "0123456789" {
		t.Errorf("file = %q", got)
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	w, err := Open(filepath.Join(testutil.TempDir(t), "x.log"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("a")); !errors.Is(err, os.ErrClosed) {
		t.Errorf("err = %v", err)
	}
}
