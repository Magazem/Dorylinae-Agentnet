//go:build linux

package device

import "syscall"

// setParentDeathSignal makes Linux kill the command's leader if the daemon
// dies mid-run (review 40 M2), so a crash does not leave it running past its
// timeout while the restarted daemon reports it interrupted. Its own children
// stay in its process group and are not covered: see device.md §Running.
func setParentDeathSignal(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGKILL
}
