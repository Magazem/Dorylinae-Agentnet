package device

import (
	"encoding/binary"
	"strings"
	"testing"
)

// aclBlob builds a system.posix_acl_access value from {tag, perm, id}
// entries.
func aclBlob(entries ...[3]uint32) []byte {
	b := binary.LittleEndian.AppendUint32(nil, aclXattrVersion)
	for _, e := range entries {
		b = binary.LittleEndian.AppendUint16(b, uint16(e[0])) //nolint:gosec // test tags fit
		b = binary.LittleEndian.AppendUint16(b, uint16(e[1])) //nolint:gosec // test perms fit
		b = binary.LittleEndian.AppendUint32(b, e[2])
	}
	return b
}

// Review 41 M2: a named user or group in a Linux access list that may write
// is someone else, unless the mask takes the write right away; this user,
// root and trusted groups are not.
func TestACLWriter(t *testing.T) {
	const self = 1000
	const userObj, groupObj, other = 0x01, 0x04, 0x20
	trusted := func(gid uint64) bool { return gid == 0 || gid == 50 }
	base := [][3]uint32{{userObj, 7, 0}, {groupObj, 5, 0}, {other, 5, 0}}
	with := func(extra ...[3]uint32) []byte { return aclBlob(append(append([][3]uint32{}, base...), extra...)...) }
	for _, tc := range []struct {
		name string
		acl  []byte
		want string
	}{
		{"minimal", with(), ""},
		{"other user rwx", with([3]uint32{aclUser, 7, 1234}, [3]uint32{aclMask, 7, 0}), "user id 1234"},
		{"other user masked", with([3]uint32{aclUser, 7, 1234}, [3]uint32{aclMask, 5, 0}), ""},
		{"other user read", with([3]uint32{aclUser, 5, 1234}, [3]uint32{aclMask, 7, 0}), ""},
		{"self", with([3]uint32{aclUser, 7, self}, [3]uint32{aclMask, 7, 0}), ""},
		{"root", with([3]uint32{aclUser, 7, 0}, [3]uint32{aclMask, 7, 0}), ""},
		{"untrusted group", with([3]uint32{aclGroup, 7, 1234}, [3]uint32{aclMask, 7, 0}), "group id 1234"},
		{"trusted group", with([3]uint32{aclGroup, 7, 50}, [3]uint32{aclMask, 7, 0}), ""},
		{"bad version", append([]byte{1, 0, 0, 0}, with()[4:]...), "unreadable"},
		{"truncated", with()[:9], "unreadable"},
	} {
		got := aclWriter(tc.acl, self, trusted)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: who = %q, want %q", tc.name, got, tc.want)
		}
	}
}
