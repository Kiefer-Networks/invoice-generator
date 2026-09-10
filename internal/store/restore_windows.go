package store

import "os"

// Windows directory rename refuses an existing destination directory.
func activateRecoveryRoot(parent *os.Root, from, to string) error { return parent.Rename(from, to) }
