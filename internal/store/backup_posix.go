//go:build !windows

package store

import (
	"os"
	"syscall"
)

func protectedRecoveryKey(f *os.File) bool {
	i, e := f.Stat()
	if e != nil || i.Mode().Perm()&0077 != 0 {
		return false
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid())
}

func singleRecoveryLink(f *os.File) bool {
	i, e := f.Stat()
	if e != nil {
		return false
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && s.Nlink == 1
}
func protectRecoveryPath(path string, dir bool) error {
	mode := os.FileMode(0600)
	if dir {
		mode = 0700
	}
	return os.Chmod(path, mode)
}
func syncRecoveryDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
