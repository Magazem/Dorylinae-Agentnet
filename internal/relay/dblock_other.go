//go:build !unix && !windows

package relay

import "os"

// No advisory locking on this platform: the stop-the-relay-first rule stands.
func lockFileExclusive(*os.File) error { return nil }
