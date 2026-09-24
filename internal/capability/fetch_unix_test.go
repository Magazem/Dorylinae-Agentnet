//go:build !windows

package capability

import (
	"path/filepath"
	"syscall"
	"testing"
)

func makeJunction(t *testing.T, _, _ string) {
	t.Helper()
	t.Skip("no junctions on this OS")
}

func makeSpecialFiles(t *testing.T, root string) {
	t.Helper()
	if err := syscall.Mkfifo(filepath.Join(root, "fifo"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
}

func specialFileExpectations(t *testing.T, h *fetchHarness, tok []byte) {
	t.Helper()
	h.expectErr(tok, readOpts("fifo"), CodeNotRegular)
	e := h.call(tok, reqOpts{op: OpStat, path: "fifo"})[0]["entry"].(map[string]any)
	if e["type"] != "other" {
		t.Fatalf("stat fifo = %v", e)
	}
}
