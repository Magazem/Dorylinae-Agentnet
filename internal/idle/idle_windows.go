//go:build windows

package idle

import (
	"context"
	"errors"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procLastInput    = user32.NewProc("GetLastInputInfo")
	procGetTickCount = kernel32.NewProc("GetTickCount")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

func query(context.Context) (time.Duration, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil { //nolint:gosec // os.Getpid is always non-negative
		return 0, err
	}
	if session == 0 {
		return 0, errors.New("idle: not an interactive session")
	}
	info := lastInputInfo{cbSize: uint32(unsafe.Sizeof(lastInputInfo{}))}
	if r, _, err := procLastInput.Call(uintptr(unsafe.Pointer(&info))); r == 0 { //nolint:gosec // required by the GetLastInputInfo syscall ABI
		return 0, err
	}
	tick, _, _ := procGetTickCount.Call()
	return time.Duration(uint32(tick)-info.dwTime) * time.Millisecond, nil //nolint:gosec // both are GetTickCount-scale ms; uint32 wrap is intended
}
