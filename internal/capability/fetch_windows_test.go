//go:build windows

package capability

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func makeJunction(t *testing.T, target, link string) {
	t.Helper()
	//nolint:gosec // test helper: fixed command, paths are test temp dirs
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v: %s", err, out)
	}
}

func makeSpecialFiles(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "real-dir")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	makeJunction(t, dir, filepath.Join(root, "junction"))
}

func specialFileExpectations(t *testing.T, h *fetchHarness, tok []byte) {
	t.Helper()
	// A junction inside the directory is refused as a symlink, also as an
	// intermediate component, and 8.3 aliases are refused by shape.
	h.expectErr(tok, readOpts("junction"), CodeSymlink)
	h.expectErr(tok, readOpts("junction/x"), CodeSymlink)
	h.expectErr(tok, reqOpts{op: OpList, path: "junction"}, CodeSymlink)
	h.expectErr(tok, readOpts("GIT~1/config"), CodeBadPath)
	h.expectErr(tok, readOpts("PROGRA~1"), CodeBadPath)
}
