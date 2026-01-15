//go:build windows

package filemanagerengine

import (
	"io/fs"
	"strings"
	"syscall"
)

func isHiddenFile(info fs.FileInfo, path string) bool {
	name := info.Name()
	if strings.HasPrefix(name, ".") {
		return true
	}
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return data.FileAttributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
	}
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attrs, err := syscall.GetFileAttributes(ptr)
	if err != nil {
		return false
	}
	return attrs&syscall.FILE_ATTRIBUTE_HIDDEN != 0
}
