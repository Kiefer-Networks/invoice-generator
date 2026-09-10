//go:build !production

package web

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDevelopmentReloadUsesDedicatedRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "templates"), 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "templates", "login.html")
	if err := os.WriteFile(file, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	read, closeRoot, err := developmentFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRoot()
	if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := read.ReadFile("templates/login.html")
	if err != nil || string(b) != "changed" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := read.ReadFile("../outside"); err == nil {
		t.Fatal("escaped dedicated assets root")
	}
}
