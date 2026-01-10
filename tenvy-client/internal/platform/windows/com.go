//go:build windows

package windows

import (
	"syscall"
	"unsafe"
)

type IUnknown struct {
	Vtbl *uintptr
}

func (u *IUnknown) Call(method int, args ...uintptr) (uintptr, uintptr, error) {
	vtable := *u.Vtbl
	proc := *(*uintptr)(unsafe.Pointer(vtable + uintptr(method)*unsafe.Sizeof(uintptr(0))))

	fullArgs := make([]uintptr, len(args)+1)
	fullArgs[0] = uintptr(unsafe.Pointer(u))
	copy(fullArgs[1:], args)

	return syscall.SyscallN(proc, fullArgs...)
}

func (u *IUnknown) QueryInterface(iid *GUID, ppv unsafe.Pointer) uintptr {
	ret, _, _ := u.Call(0, uintptr(unsafe.Pointer(iid)), uintptr(ppv))
	return ret
}

func (u *IUnknown) AddRef() uint32 {
	ret, _, _ := u.Call(1)
	return uint32(ret)
}

func (u *IUnknown) Release() uint32 {
	ret, _, _ := u.Call(2)
	return uint32(ret)
}

type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}
