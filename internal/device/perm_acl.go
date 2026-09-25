package device

import (
	"encoding/binary"
	"strconv"
)

// POSIX ACL entries as Linux stores them in the system.posix_acl_access
// extended attribute (linux/posix_acl_xattr.h): a 4-byte version (2), then
// 8-byte entries {tag uint16, perm uint16, id uint32}, little-endian.
const (
	aclXattrVersion = 2
	aclUser         = 0x02 // ACL_USER: a named user
	aclGroup        = 0x08 // ACL_GROUP: a named group
	aclMask         = 0x10 // ACL_MASK: caps named users and every group
	aclWrite        = 0x02
)

// aclWriter reads a Linux POSIX access ACL and reports who other than root,
// euid or a trusted group it lets write, or "" if nobody. The mode bits
// alone miss them: a named user's entry shows only as the group bits (the
// mask), which pass when the owning group is trusted (review 41 M2). A
// malformed ACL counts as someone.
func aclWriter(b []byte, euid uint64, groupOK func(gid uint64) bool) string {
	if len(b) < 4 || (len(b)-4)%8 != 0 || binary.LittleEndian.Uint32(b) != aclXattrVersion {
		return "an unreadable access list"
	}
	maskWrite := true
	for e := b[4:]; len(e) > 0; e = e[8:] {
		if binary.LittleEndian.Uint16(e) == aclMask {
			maskWrite = binary.LittleEndian.Uint16(e[2:])&aclWrite != 0
		}
	}
	if !maskWrite {
		return ""
	}
	for e := b[4:]; len(e) > 0; e = e[8:] {
		tag, perm, id := binary.LittleEndian.Uint16(e), binary.LittleEndian.Uint16(e[2:]), uint64(binary.LittleEndian.Uint32(e[4:]))
		if perm&aclWrite == 0 {
			continue
		}
		switch {
		case tag == aclUser && id != 0 && id != euid:
			return "user id " + strconv.FormatUint(id, 10) + " (access list)"
		case tag == aclGroup && !groupOK(id):
			return "the members of group id " + strconv.FormatUint(id, 10) + " (access list)"
		}
	}
	return ""
}
