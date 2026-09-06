package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("INVOICE_RECOVERY_TEST_CHILD") == "1" || os.Getenv("INVOICE_RECOVERY_SIGNAL_PID") != "" {
		os.Exit(m.Run())
	}
	home, e := os.UserHomeDir()
	if e != nil {
		panic(e)
	}
	root, e := os.MkdirTemp(home, ".invoice-server-tests-")
	if e != nil {
		panic(e)
	}
	os.Setenv("TMP", root)
	os.Setenv("TEMP", root)
	code := m.Run()
	os.RemoveAll(root)
	os.Exit(code)
}

func configureRecoverySignalChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x10, HideWindow: true}
}
func interruptRecoveryChild(cmd *exec.Cmd) error {
	sender := exec.Command(os.Args[0], "-test.run=^TestRecoverySignalSender$")
	sender.Env = append(os.Environ(), "INVOICE_RECOVERY_SIGNAL_PID="+strconv.Itoa(cmd.Process.Pid))
	sender.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return sender.Run()
}
func TestRecoverySignalSender(t *testing.T) {
	raw := os.Getenv("INVOICE_RECOVERY_SIGNAL_PID")
	if raw == "" {
		return
	}
	pid, e := strconv.Atoi(raw)
	if e != nil {
		os.Exit(2)
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	kernel.NewProc("FreeConsole").Call()
	ok, _, _ := kernel.NewProc("AttachConsole").Call(uintptr(pid))
	if ok == 0 {
		os.Exit(3)
	}
	ignore := syscall.NewCallback(func(event uint32) uintptr { return 1 })
	ok, _, _ = kernel.NewProc("SetConsoleCtrlHandler").Call(ignore, 1)
	if ok == 0 {
		os.Exit(5)
	}
	ok, _, _ = kernel.NewProc("GenerateConsoleCtrlEvent").Call(1, 0)
	if ok == 0 {
		os.Exit(4)
	}
	time.Sleep(100 * time.Millisecond)
	os.Exit(0)
}
