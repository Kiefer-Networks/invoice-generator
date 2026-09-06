package store

import (
	"golang.org/x/sys/unix"
	"os"
)

// RENAME_NOREPLACE closes the check/rename race, including empty directories.
func activateRecoveryRoot(parent *os.Root, from, to string) error {
	f, e := parent.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return unix.Renameat2(int(f.Fd()), from, int(f.Fd()), to, unix.RENAME_NOREPLACE)
}
