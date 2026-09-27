//go:build !unix

package relay_test

import "math"

// openFilesLimit: no per-process descriptor limit that matters here.
func openFilesLimit() uint64 { return math.MaxUint64 }
