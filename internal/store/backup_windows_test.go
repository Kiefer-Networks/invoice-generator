package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestBackupRejectsBroadWindowsKeyACL(t *testing.T) {
	_, o := backupFixture(t)
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{5}, 32), 0600); err != nil {
		t.Error(err)
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
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
	o.KeyFile = path
	o.Passphrase = nil
	if _, e = Backup(context.Background(), o); e == nil {
		t.Fatal("world-readable key accepted")
	}
}

func TestBackupWindowsOutputIsProtected(t *testing.T) {
	_, o := backupFixture(t)
	if _, e := Backup(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(o.Output)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if !protectedRecoveryKey(f) {
		t.Fatal("archive ACL grants other users access")
	}
}
