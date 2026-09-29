package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// endpoint names the pipe after the current user's SID and the canonical,
// case-folded dir: two spellings of one dir share a pipe, and two users with
// the same dir spelling never collide by accident (Docs/protocol/ipc.md
// §Endpoint). The name is not a secret; the pipe owner check is what keeps
// another user's pipe out.
func endpoint(dir string) (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("lookup current user: %w", err)
	}
	sum := sha256.Sum256([]byte(user.User.Sid.String() + "\x00" + strings.ToLower(Canonical(dir))))
	return `\\.\pipe\dorylinae-` + hex.EncodeToString(sum[:8]), nil
}

// resolveExisting returns the final path of the existing file or dir p, as
// Windows reports it for an open handle.
func resolveExisting(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]uint16, windows.MAX_PATH)
	for {
		// Flags 0: FILE_NAME_NORMALIZED | VOLUME_NAME_DOS. buf holds at most
		// the length Windows asked for, a uint32.
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0) //nolint:gosec // see above
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			s := windows.UTF16ToString(buf[:n])
			if rest, ok := strings.CutPrefix(s, `\\?\UNC\`); ok {
				return `\\` + rest, nil
			}
			return strings.TrimPrefix(s, `\\?\`), nil
		}
		buf = make([]uint16, n)
	}
}
