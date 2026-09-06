package devmode

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDevRootRejectsExistingProductionDirectory(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "production.sqlite"), []byte("untouched"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := PrepareRoot(root); e == nil {
		t.Fatal("unmarked production root accepted")
	}
	data, e := os.ReadFile(filepath.Join(root, "production.sqlite"))
	if e != nil || string(data) != "untouched" {
		t.Fatal("production data changed")
	}
}

func TestDevRootRejectsHardlinkedSecrets(t *testing.T) {
	root := filepath.Join(t.TempDir(), "dev")
	secrets, e := PrepareRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	original := filepath.Join(t.TempDir(), "production-key")
	if e = os.WriteFile(original, []byte("must not be read"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(secrets.Session); e != nil {
		t.Fatal(e)
	}
	if e = os.Link(original, secrets.Session); e != nil {
		t.Fatal(e)
	}
	if _, e = PrepareRoot(root); e == nil {
		t.Fatal("hardlinked production secret accepted")
	}
}
func TestDevRootRejectsLinkedSecretAndDocumentDirectories(t *testing.T) {
	for _, name := range []string{"secrets", "documents"} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "dev")
			if _, e := PrepareRoot(root); e != nil {
				t.Fatal(e)
			}
			// Move only the freshly created fixture directory, never a computed ancestor.
			path := filepath.Join(root, name)
			if e := os.Rename(path, path+"-original"); e != nil {
				t.Fatal(e)
			}
			target := t.TempDir()
			if e := os.Symlink(target, path); e != nil {
				if runtime.GOOS != "windows" {
					t.Fatal(e)
				}
				if output, e := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput(); e != nil {
					t.Fatalf("create fixture junction: %v %s", e, output)
				}
			}
			if _, e := PrepareRoot(root); e == nil {
				t.Fatal("linked fixture directory accepted")
			}
		})
	}
}
