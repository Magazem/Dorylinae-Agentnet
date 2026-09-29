//go:build !windows && !darwin && !linux

package pathid

// isMountPoint has no mount table to read here; the device check in isRoot
// decides alone.
func isMountPoint(string) (bool, error) { return false, nil }
