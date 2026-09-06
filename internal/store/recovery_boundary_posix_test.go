//go:build !windows

package store

import (
	"os"
	"testing"
)

func makeRecoveryParentUntrusted(t *testing.T, path string) {
	t.Helper()
	if e := os.Chmod(path, 0777); e != nil {
		t.Fatal(e)
	}
}

func createRecoverySymlinkAlias(t *testing.T, source, alias string) string {
	t.Helper()
	if e := os.Symlink(source, alias); e != nil {
		t.Fatal(e)
	}
	return alias
}
