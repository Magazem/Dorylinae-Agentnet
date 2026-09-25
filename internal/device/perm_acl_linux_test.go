//go:build linux

package device

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 41 M2: a program whose access list lets another user write it is
// refused, although its mode bits alone (0770, group = the owning group)
// would not say who.
func TestCheckProgramOwnerLinuxACL(t *testing.T) {
	dir := testutil.PrivateDir(t)
	prog := filepath.Join(dir, "tool")
	if err := os.WriteFile(prog, []byte("x"), 0o700); err != nil { //nolint:gosec // an executable test file
		t.Fatal(err)
	}
	if err := CheckProgramOwner(prog); err != nil {
		t.Fatalf("a private program: %v", err)
	}
	someone := uint32(os.Geteuid()) + 54321 //nolint:gosec // user ids are never negative
	acl := aclBlob([3]uint32{0x01, 7, 0}, [3]uint32{aclUser, 7, someone}, [3]uint32{0x04, 0, 0},
		[3]uint32{aclMask, 7, 0}, [3]uint32{0x20, 0, 0})
	if err := unix.Lsetxattr(prog, "system.posix_acl_access", acl, 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("no access lists on this filesystem: %v", err)
		}
		t.Fatal(err)
	}
	var we *WritableError
	if err := CheckProgramOwner(prog); !errors.As(err, &we) || we.Path != prog || !strings.Contains(we.Who, "access list") {
		t.Fatalf("a program another user may write through its access list: %v", err)
	}
}
