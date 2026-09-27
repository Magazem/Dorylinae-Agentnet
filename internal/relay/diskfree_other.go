//go:build !unix && !windows

package relay

import "errors"

// freeDiskSpace is unknown on this platform; the low-disk check is skipped.
func freeDiskSpace(string) (uint64, error) {
	return 0, errors.New("free disk space unknown on this platform")
}
