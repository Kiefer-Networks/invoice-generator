//go:build !windows

package store

import (
	"os"
	"syscall"
)

func trustedRecoveryDirectory(f *os.File) bool {
	i, e := f.Stat()
	if e != nil || !i.IsDir() || i.Mode().Perm()&0022 != 0 {
		return false
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && int64(s.Uid) == int64(os.Geteuid())
}

func trustedRecoveryAncestor(f *os.File) bool {
	i, e := f.Stat()
	if e != nil || !i.IsDir() {
		return false
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || (s.Uid != 0 && int64(s.Uid) != int64(os.Geteuid())) {
		return false
	}
	return i.Mode().Perm()&0022 == 0 || i.Mode()&os.ModeSticky != 0
}

func protectedRecoveryKey(f *os.File) bool {
	i, e := f.Stat()
	if e != nil || i.Mode().Perm()&0077 != 0 {
		return false
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && int64(s.Uid) == int64(os.Geteuid())
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
	f, e := os.Open(path) // #nosec G304 -- Internal durability barrier for a validated recovery directory, never an HTTP path.
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }() // Sync below reports durability errors; closing a read-only directory adds no writes.
	return f.Sync()
}
