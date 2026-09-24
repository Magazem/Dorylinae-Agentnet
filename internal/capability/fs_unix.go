//go:build !windows

package capability

import "syscall"

// openNonblock keeps a file swapped for a FIFO between the Lstat and the open
// from blocking a fetch worker.
const openNonblock = syscall.O_NONBLOCK
