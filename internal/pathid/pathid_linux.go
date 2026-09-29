package pathid

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// isMountPoint reports whether p is listed as a mount point in
// /proc/self/mountinfo, which also lists bind mounts inside one filesystem.
// Without /proc the device check in isRoot decides alone.
func isMountPoint(p string) (bool, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	points, err := mountPoints(f)
	if err != nil {
		return false, err
	}
	for _, m := range points {
		if m == p {
			return true, nil
		}
	}
	return false, nil
}

// mountPoints returns the mount point field (the fifth) of every line of a
// mountinfo file, with its octal escapes (\040 for a space) decoded.
func mountPoints(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 {
			return nil, errors.New("malformed mountinfo line")
		}
		out = append(out, unescapeMount(f[4]))
	}
	return out, sc.Err()
}

func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
