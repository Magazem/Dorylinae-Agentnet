//go:build unix && !linux

package device

import "syscall"

// setParentDeathSignal does nothing: only Linux has a parent-death signal
// (review 40 M2).
func setParentDeathSignal(*syscall.SysProcAttr) {}
