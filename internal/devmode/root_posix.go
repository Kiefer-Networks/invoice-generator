//go:build !windows

package devmode

import (
	"os"
	"syscall"
)

func unsafePath(_ string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	if info.Mode().IsRegular() {
		stat, ok := info.Sys().(*syscall.Stat_t)
		return !ok || stat.Nlink != 1
	}
	return !info.IsDir()
}
