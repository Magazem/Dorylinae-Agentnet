//go:build !unix

package relay

// matchOwner is a no-op: file ownership is not carried over on this platform.
func matchOwner(string, string) {}
