package store

import (
	"path/filepath"
	"runtime"
	"strings"
)

// fileURI turns a filesystem path into a SQLite "file:" URI. A bare
// "file:" + path lets '?' and '#' end the path and '%xx' decode to other
// bytes, so the database opened at the wrong place or not at all (review 55
// R55-096). Only those three characters need escaping; SQLite takes the rest
// of the path literally. On Windows the drive form is file:/C:/dir/db.
func fileURI(path string) string {
	p := path
	if runtime.GOOS == "windows" {
		p = filepath.ToSlash(p)
		if len(p) >= 2 && p[1] == ':' {
			p = "/" + p
		}
	}
	p = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(p)
	if strings.HasPrefix(p, "//") {
		// An empty authority, so a UNC path is not read as a host name.
		return "file://" + p
	}
	return "file:" + p
}
