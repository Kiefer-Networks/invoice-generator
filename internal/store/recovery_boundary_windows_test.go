package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// Junctions exercise Windows reparse alias rejection without requiring the
// optional symbolic-link privilege. POSIX exercises actual file symlinks.
func createRecoverySymlinkAlias(t *testing.T, source, alias string) string {
	t.Helper()
	if e := os.Symlink(source, alias); e == nil {
		return alias
	}
	junction := alias + "-junction"
	cmd := exec.Command("cmd", "/c", "mklink", "/J", junction, filepath.Dir(source)) // #nosec G204 -- Fixed integration-test command; variable arguments are generated fixture paths or IDs, never request data.
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("create junction: %v %s", e, output)
	}
	t.Cleanup(func() { _ = os.Remove(junction) })
	return filepath.Join(junction, filepath.Base(source))
}

// Recovery tests need a trusted ancestor chain. Some packaged Windows hosts
// grant app capabilities write access to AppData, so their default TEMP cannot
// exercise a successful hardened recovery. Provision a private test root without
// changing the user's existing directory permissions.
func TestMain(m *testing.M) {
	home, e := os.UserHomeDir()
	if e != nil {
		panic(e)
	}
	root, e := os.MkdirTemp(home, ".invoice-store-tests-")
	if e != nil {
		panic(e)
	}
	if e = protectRecoveryPath(root, true); e != nil {
		panic(e)
	}
	if err := os.Setenv("TMP", root); err != nil {
		panic(err)
	}
	if err := os.Setenv("TEMP", root); err != nil {
		panic(err)
	}
	code := m.Run()
	if err := os.RemoveAll(root); err != nil {
		panic(err)
	}
	os.Exit(code)
}

func makeRecoveryParentUntrusted(t *testing.T, path string) {
	t.Helper()
	sd, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
}
