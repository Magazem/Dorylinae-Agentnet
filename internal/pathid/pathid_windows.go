package pathid

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// GetFinalPathNameByHandle flags (both zero; x/sys/windows does not name them).
const (
	fileNameNormalized = 0x0
	volumeNameDOS      = 0x0
)

// getDriveType and createFile are the Windows calls; tests replace them to
// prove that a refused path reaches neither.
var (
	getDriveType = windows.GetDriveType
	createFile   = windows.CreateFile
)

// checkLocal allows a path with no volume name, or whose volume name is a
// drive letter ("X:") that is not a network drive. Every other volume name
// (\\host\share, \\?\, \\.\, \??\UNC\host\share, \??\C:) is refused
// lexically, before any call (review 61 F7-S2).
func checkLocal(p string) error {
	if len(p) >= 2 && os.IsPathSeparator(p[0]) && os.IsPathSeparator(p[1]) {
		return ErrRemote
	}
	// \??\ is the NT object-manager prefix; refuse it whatever VolumeName
	// makes of it.
	if len(p) >= 3 && os.IsPathSeparator(p[0]) && p[1] == '?' && p[2] == '?' {
		return ErrRemote
	}
	vol := filepath.VolumeName(p)
	if vol == "" {
		return nil
	}
	if len(vol) != 2 || vol[1] != ':' || !isDriveLetter(vol[0]) {
		return ErrRemote
	}
	root, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return err
	}
	// GetDriveType reads the drive table; it does not open the share.
	if getDriveType(root) == windows.DRIVE_REMOTE {
		return ErrRemote
	}
	return nil
}

func isDriveLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// isVolumeGUIDPath reports whether t is \\?\Volume{GUID}\ or a path under
// it: the target Windows gives a folder mount point.
func isVolumeGUIDPath(t string) bool {
	const prefix = `\\?\Volume{`
	n := len(prefix) + len("00000000-0000-0000-0000-000000000000") + 1
	if len(t) < n || !hasPrefixFold(t, prefix) || t[n-1] != '}' {
		return false
	}
	for _, c := range t[len(prefix) : n-1] {
		if c != '-' && !isHexDigit(c) {
			return false
		}
	}
	return len(t) == n || t[n] == '\\'
}

func isHexDigit(c rune) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// evalLinks leaves p to finalPath, whose CreateFile follows symbolic links
// and junctions alike: filepath.EvalSymlinks on go1.23+ cannot resolve a
// path through a junction (review 61 F7-S4).
func evalLinks(p string) (string, error) { return p, nil }

// finalPath returns the path Windows itself reports for the directory r
// (GetFinalPathNameByHandle, normalised, DOS volume name): junctions, mount
// points, subst drives and 8.3 names are resolved, and the case is the one
// on disk. A result on a network volume is ErrRemote.
func finalPath(r string) (string, error) {
	name, err := windows.UTF16PtrFromString(r)
	if err != nil {
		return "", err
	}
	h, err := createFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "open", Path: r, Err: err}
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), fileNameNormalized|volumeNameDOS) //nolint:gosec // a path length, far below 2^32
		if err != nil {
			return "", &os.PathError{Op: "GetFinalPathNameByHandle", Path: r, Err: err}
		}
		if int(n) < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]uint16, n+1)
	}
	s := windows.UTF16ToString(buf)
	switch {
	case hasPrefixFold(s, `\\?\UNC\`):
		return "", ErrRemote
	case strings.HasPrefix(s, `\\?\`):
		s = s[len(`\\?\`):]
	}
	if v := filepath.VolumeName(s); len(v) != 2 || v[1] != ':' {
		return "", errors.New("the directory has no drive-letter path")
	}
	if err := checkLocal(s); err != nil {
		return "", err
	}
	return filepath.Clean(s), nil
}

func isRoot(p string) (bool, error) {
	p = filepath.Clean(p)
	if v := filepath.VolumeName(p); v != "" && (p == v || p == v+`\`) {
		return true, nil
	}
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return false, err
	}
	buf := make([]uint16, windows.MAX_PATH+1)
	if len(p)+2 > len(buf) {
		buf = make([]uint16, len(p)+2)
	}
	if err := windows.GetVolumePathName(name, &buf[0], uint32(len(buf))); err != nil { //nolint:gosec // a path length, far below 2^32
		return false, err
	}
	return strings.EqualFold(filepath.Clean(windows.UTF16ToString(buf)), p), nil
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
