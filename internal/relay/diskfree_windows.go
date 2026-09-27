//go:build windows

package relay

import "golang.org/x/sys/windows"

// freeDiskSpace reports the bytes available to this process on the volume
// holding dir.
func freeDiskSpace(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var avail uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, nil, nil); err != nil {
		return 0, err
	}
	return avail, nil
}
