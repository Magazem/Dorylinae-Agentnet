package pathid

import (
	"os"
	"strings"
	"testing"
)

// Linux mount points (review 61 I4): /proc is another filesystem, and
// /proc/self/root is another spelling of "/".
func TestIsRootLinuxMounts(t *testing.T) {
	if _, err := os.Stat("/proc/self/mountinfo"); err != nil {
		t.Skipf("no /proc: %v", err)
	}
	if ok, err := IsRoot("/proc"); err != nil || !ok {
		t.Errorf("IsRoot(/proc) = %v, %v; want true", ok, err)
	}
	r, err := Resolve("/proc/self/root")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := IsRoot(r); err != nil || !ok {
		t.Errorf("IsRoot(Resolve(/proc/self/root) = %q) = %v, %v; want true", r, ok, err)
	}
	if ok, err := IsRoot("/proc/self"); err != nil || ok {
		t.Errorf("IsRoot(/proc/self) = %v, %v; want false", ok, err)
	}
}

// A bind mount inside one filesystem has no device change; it is found in
// mountinfo, whose octal escapes are decoded.
func TestMountPoints(t *testing.T) {
	const info = `22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
40 22 8:1 /home/u/src /srv/bind rw,relatime shared:1 - ext4 /dev/sda1 rw
41 22 8:1 /data /mnt/my\040disk rw - ext4 /dev/sda1 rw
`
	got, err := mountPoints(strings.NewReader(info))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/", "/srv/bind", "/mnt/my disk"}
	if len(got) != len(want) {
		t.Fatalf("mountPoints = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mountPoints = %q, want %q", got, want)
		}
	}
	if _, err := mountPoints(strings.NewReader("short line\n")); err == nil {
		t.Error("a malformed line was accepted")
	}
}
