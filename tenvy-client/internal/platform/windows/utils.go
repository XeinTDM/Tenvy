//go:build windows

package windows

import (
	"syscall"
	"unsafe"

	"github.com/lxn/win"
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

func FindWindowForPID(pid uint32) win.HWND {
	var hwnd win.HWND
	cb := syscall.NewCallback(func(h win.HWND, l uintptr) uintptr {
		var windowPid uint32
		procGetWindowThreadProcessId.Call(uintptr(h), uintptr(unsafe.Pointer(&windowPid)))

		visible, _, _ := procIsWindowVisible.Call(uintptr(h))
		if windowPid == pid && visible != 0 {
			hwnd = h
			return 0 // stop
		}
		return 1 // continue
	})
	procEnumWindows.Call(cb, 0)
	return hwnd
}
