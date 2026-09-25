//go:build !windows && !linux

package device

// checkACL does nothing outside Linux: macOS and BSD access lists are not
// read (Docs/protocol/device.md §Program ownership, review 41 L4).
func checkACL(string, uint64, func(uint64) bool) error { return nil }
