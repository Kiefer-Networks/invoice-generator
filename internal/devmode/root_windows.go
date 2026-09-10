//go:build !production && windows

package devmode

import (
	"os"

	"golang.org/x/sys/windows"
)

func unsafePath(path string, info os.FileInfo) bool {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return true
	}
	attrs, e := windows.GetFileAttributes(p)
	if e != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return true
	}
	if info.IsDir() {
		return false
	}
	f, e := os.Open(path) // #nosec G304 -- Internal identity check of operator-selected development paths; the file handle is used only for link metadata.
	if e != nil {
		return true
	}
	defer func() { _ = f.Close() }() // Read-only input; reads and validation report their own errors.
	var metadata windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &metadata) != nil || metadata.NumberOfLinks != 1
}
