//go:build !production && windows

package devmode

import (
	"golang.org/x/sys/windows"
	"os"
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
	f, e := os.Open(path)
	if e != nil {
		return true
	}
	defer f.Close()
	var metadata windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &metadata) != nil || metadata.NumberOfLinks != 1
}
