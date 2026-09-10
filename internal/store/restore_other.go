//go:build !linux && !windows

package store

import "os"

// Fail closed where an atomic no-replacement directory move is not implemented.
func activateRecoveryRoot(parent *os.Root, from, to string) error { return ErrBackup }
