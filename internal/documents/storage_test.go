package documents

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type brokenReader struct{}

func (brokenReader) Read(p []byte) (int, error) { return 0, errors.New("broken") }
func TestStorageAtomicContainedImmutable(t *testing.T) {
	root := t.TempDir()
	s, e := NewStorage(root, 100)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := s.Put(strings.NewReader("document"))
	if e != nil {
		t.Fatal(e)
	}
	if len(a.Key) != 52 || strings.ContainsAny(a.Key, "./\\:") {
		t.Fatal(a)
	}
	f, e := s.Open(a.Key, a.SHA256, a.Size)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(f)
	_ = f.Close()
	if string(b) != "document" {
		t.Fatal(string(b))
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(root, a.Key))
		if info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
	}
	for _, key := range []string{"../secret", "/etc/passwd", `..\secret`, a.Key + ":stream", "."} {
		if f, e := s.Open(key, a.SHA256, a.Size); e == nil {
			_ = f.Close()
			t.Fatalf("accepted %q", key)
		}
	}
	for _, r := range []io.Reader{brokenReader{}, bytes.NewReader(make([]byte, 101))} {
		if _, e = s.Put(r); e == nil {
			t.Fatal("accepted failed write")
		}
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatal(entries)
	}
	if f, e := s.Open(a.Key, strings.Repeat("0", 64), a.Size); e == nil {
		_ = f.Close()
		t.Fatal("bad checksum accepted")
	}
	if f, e := s.Open(a.Key, a.SHA256, a.Size+1); e == nil {
		_ = f.Close()
		t.Fatal("bad size accepted")
	}
	if e = os.WriteFile(filepath.Join(root, a.Key), []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if f, e := s.Open(a.Key, a.SHA256, a.Size); e == nil {
		_ = f.Close()
		t.Fatal("tampering accepted")
	}
}
func TestStorageRejectSymlink(t *testing.T) {
	root := t.TempDir()
	s, e := NewStorage(root, 100)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	target := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Error(err)
	}
	key := strings.Repeat("A", 52)
	if e = os.Symlink(target, filepath.Join(root, key)); e != nil {
		t.Skipf("symlink privilege unavailable: %v", e)
	}
	if f, e := s.Open(key, strings.Repeat("0", 64), 6); e == nil {
		_ = f.Close()
		t.Fatal("symlink accepted")
	}
}
