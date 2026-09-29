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

func checkLocal(p string) error {
	if len(p) >= 2 && os.IsPathSeparator(p[0]) && os.IsPathSeparator(p[1]) {
		return ErrRemote
	}
	vol := filepath.VolumeName(p)
	if len(vol) == 2 && vol[1] == ':' {
		root, err := windows.UTF16PtrFromString(vol + `\`)
		if err != nil {
			return err
		}
		// GetDriveType reads the drive table; it does not open the share.
		if windows.GetDriveType(root) == windows.DRIVE_REMOTE {
			return ErrRemote
		}
	}
	return nil
}

// finalPath returns the path Windows itself reports for the directory r
// (GetFinalPathNameByHandle, normalised, DOS volume name): junctions, mount
// points, subst drives and 8.3 names are resolved, and the case is the one
// on disk. A result on a network volume is ErrRemote.
func finalPath(r string) (string, error) {
	name, err := windows.UTF16PtrFromString(r)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(name, 0,
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
