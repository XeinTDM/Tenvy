//go:build !windows

package filemanagerengine

import (
	"io/fs"
	"strings"
)

func isHiddenFile(info fs.FileInfo, _ string) bool {
	name := info.Name()
	return strings.HasPrefix(name, ".")
}
