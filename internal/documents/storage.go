// Package documents manages immutable invoice artifacts.
package documents

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

var ErrIntegrity = errors.New("document integrity check failed")
var keyPattern = regexp.MustCompile(`^[A-Z2-7]{52}$`)

type Artifact struct {
	Key, SHA256 string
	Size        int64
}
type Storage struct {
	publicationBarrier func(*os.Root, string) error
	root               *os.Root
	max                int64
}

func NewStorage(path string, max int64) (*Storage, error) {
	if max <= 0 {
		return nil, errors.New("positive document limit required")
	}
	root, e := openProvisionedRoot(path)
	if e != nil {
		return nil, e
	}
	return &Storage{root: root, max: max, publicationBarrier: syncPublication}, nil
}
func (s *Storage) Close() error { return s.root.Close() }
func opaqueKey() (string, error) {
	var b [32]byte
	_, e := rand.Read(b[:])
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]), e
}

// Put never replaces an existing artifact. Root-relative operations stay contained
// even if parent directories are renamed during a request.
func (s *Storage) Put(r io.Reader) (a Artifact, err error) {
	key, e := opaqueKey()
	if e != nil {
		return a, e
	}
	tmp := "." + key + ".tmp"
	f, e := s.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return a, e
	}
	defer func() { f.Close(); s.root.Remove(tmp) }()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, s.max+1))
	if e != nil {
		return a, e
	}
	if n == 0 || n > s.max {
		return a, ErrIntegrity
	}
	if e = f.Sync(); e != nil {
		return a, e
	}
	if e = f.Close(); e != nil {
		return a, e
	}
	if _, e = s.root.Lstat(key); !os.IsNotExist(e) {
		return a, ErrIntegrity
	}
	if e = s.root.Rename(tmp, key); e != nil {
		return a, e
	}
	// A failed metadata flush has an uncertain persistence outcome. Keep the
	// renamed orphan for recovery, but never return publishable metadata.
	if e = s.publicationBarrier(s.root, key); e != nil {
		return a, e
	}
	return Artifact{Key: key, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}
func (s *Storage) Open(key, sum string, size int64) (*os.File, error) {
	if !keyPattern.MatchString(key) || size <= 0 || size > s.max {
		return nil, ErrIntegrity
	}
	info, e := s.root.Lstat(key)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrIntegrity
	}
	f, e := s.root.Open(key)
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Size() != size {
		return nil, ErrIntegrity
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, s.max+1))
	if e != nil || n != size || hex.EncodeToString(h.Sum(nil)) != sum {
		return nil, ErrIntegrity
	}
	if _, e = f.Seek(0, 0); e != nil {
		return nil, e
	}
	ok = true
	return f, nil
}

// Sweep removes only old, unreferenced artifacts and interrupted sibling writes.
// Call at startup before starting workers; the cutoff must predate all live jobs.
func (s *Storage) Sweep(before time.Time, referenced map[string]bool) error {
	dir, e := s.root.Open(".")
	if e != nil {
		return e
	}
	defer dir.Close()
	entries, e := dir.ReadDir(-1)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		key := entry.Name()
		tmp := strings.HasPrefix(key, ".") && strings.HasSuffix(key, ".tmp") && keyPattern.MatchString(strings.TrimSuffix(strings.TrimPrefix(key, "."), ".tmp"))
		if !keyPattern.MatchString(key) && !tmp {
			continue
		}
		if referenced[key] {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if info.Mode().IsRegular() && info.ModTime().Before(before) {
			if e = s.root.Remove(key); e != nil {
				return e
			}
		}
	}
	return nil
}
