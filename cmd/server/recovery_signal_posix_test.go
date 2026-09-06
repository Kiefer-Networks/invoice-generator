//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func configureRecoverySignalChild(cmd *exec.Cmd) {}
func interruptRecoveryChild(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGTERM) }
