package documents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageRequiresProvisionedRootWithoutPartialCreation(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "missing", "nested", "documents")
	s, err := NewStorage(root, 100)
	if s != nil {
		s.Close()
	}
	if err == nil || s != nil || !strings.Contains(err.Error(), "provision") {
		t.Fatalf("unprovisioned root was opened: %v %v", s, err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed initialization left partial directories: %v %v", entries, err)
	}
}

func TestStorageRejectsRootTraversal(t *testing.T) {
	base := t.TempDir()
	for _, path := range []string{".", "relative-documents", base + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(base)} {
		s, err := NewStorage(path, 100)
		if s != nil {
			s.Close()
		}
		if err == nil || s != nil {
			t.Errorf("accepted non-absolute or traversing root %q", path)
		}
	}
}

func TestStorageRejectsAncestorSymlink(t *testing.T) {
	base, target := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(target, "documents"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
	s, err := NewStorage(filepath.Join(link, "documents"), 100)
	if s != nil {
		s.Close()
	}
	if err == nil || s != nil {
		t.Fatal("storage accepted a symlinked ancestor")
	}
}

func TestStorageRetryAfterExternalNestedProvisioning(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "nested", "documents")
	if s, err := NewStorage(root, 100); err == nil {
		s.Close()
		t.Fatal("missing root was silently created")
	}
	// The fixture stands in for deployment provisioning completed before startup.
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "operator-marker")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStorage(root, 100)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if got, err := os.ReadFile(marker); err != nil || string(got) != "preserve" {
		t.Fatal("initialization modified preprovisioned content", string(got), err)
	}
}
