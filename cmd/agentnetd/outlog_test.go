package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/logfile"
	"github.com/Magazem/Dorylinae-Agentnet/internal/service"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// OD-F14-3 (e), R55-F14 (Docs/review/72-r55-f14-spec.md §5 test 8): at start,
// a launchd stdout/stderr file over 1 MiB is renamed to .1; anything else is
// left alone. run fails on the missing --relay-ca after the check, so no
// daemon starts.
func TestOutLogRotatedAtStart(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		size    int
		rotated bool
	}{
		{"over 1 MiB", service.LaunchdOutFileName, logfile.MaxSize + 1, true},
		{"exactly 1 MiB", service.LaunchdOutFileName, logfile.MaxSize, false},
		{"another name", "other.log", logfile.MaxSize + 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.rotated && runtime.GOOS == "windows" {
				t.Skip("Windows cannot rename a file open without FILE_SHARE_DELETE; only launchd (macOS) uses this file")
			}
			home := testutil.TempDir(t)
			path := filepath.Join(home, c.file)
			if err := os.WriteFile(path, bytes.Repeat([]byte("x"), c.size), 0o600); err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(home, c.file+".1")
			if err := os.WriteFile(old, []byte("older generation"), 0o600); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // a file this test created in its temp dir
			if err != nil {
				t.Fatal(err)
			}
			code := run(context.Background(), []string{"--home", home, "--relay-ca", filepath.Join(home, "missing.pem")}, io.Discard, f)
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if code == 0 {
				t.Fatal("run succeeded; want the --relay-ca failure")
			}
			_, errMain := os.Stat(path)
			one, errOne := os.ReadFile(old) //nolint:gosec // a file this test created in its temp dir
			if errOne != nil {
				t.Fatal(errOne)
			}
			if c.rotated {
				if !os.IsNotExist(errMain) {
					t.Fatalf("%s still exists (err %v)", c.file, errMain)
				}
				if len(one) <= c.size || string(one[:c.size]) != string(bytes.Repeat([]byte("x"), c.size)) {
					t.Fatalf(".1 holds %d bytes, want the rotated file plus the error line", len(one))
				}
				return
			}
			if errMain != nil {
				t.Fatalf("%s: %v", c.file, errMain)
			}
			if string(one) != "older generation" {
				t.Fatalf(".1 changed: %q", one)
			}
		})
	}
}
