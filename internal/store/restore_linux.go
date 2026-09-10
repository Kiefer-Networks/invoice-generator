package store

import (
	"os"

	"golang.org/x/sys/unix"
)

// RENAME_NOREPLACE closes the check/rename race, including empty directories.
func activateRecoveryRoot(parent *os.Root, from, to string) error {
	f, e := parent.Open(".")
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }() // Only retain the directory descriptor for Renameat2; the syscall reports activation failure.
	return unix.Renameat2(int(f.Fd()), from, int(f.Fd()), to, unix.RENAME_NOREPLACE)
}
