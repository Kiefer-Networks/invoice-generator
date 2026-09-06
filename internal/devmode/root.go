package devmode

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
)

const rootMarker = "INVOICE LOCAL DEVELOPMENT v1\n"

type Secrets struct{ Client, Session, Transaction string }

// ValidateRoot refuses symlink/junction ancestors and existing unmarked folders.
// A production directory therefore cannot be repurposed by a mistaken flag.
func ValidateRoot(root string) error {
	if !filepath.IsAbs(root) || filepath.Dir(root) == root {
		return errors.New("development root must be a dedicated absolute directory")
	}
	for path := root; ; path = filepath.Dir(path) {
		info, e := os.Lstat(path)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && (!info.IsDir() || unsafePath(path, info)) {
			return errors.New("development root ancestry must contain real directories")
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	entries, e := os.ReadDir(root)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if len(entries) == 0 {
		return nil
	}
	info, e := os.Lstat(filepath.Join(root, ".development-only"))
	if e != nil || !info.Mode().IsRegular() || unsafePath(filepath.Join(root, ".development-only"), info) {
		return errors.New("development root is not marked for synthetic data")
	}
	data, e := os.ReadFile(filepath.Join(root, ".development-only"))
	if e != nil || string(data) != rootMarker {
		return errors.New("development root marker is invalid")
	}
	for _, name := range []string{"development.sqlite", "development.sqlite-wal", "development.sqlite-shm", "documents", "secrets"} {
		info, e := os.Lstat(filepath.Join(root, name))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && unsafePath(filepath.Join(root, name), info) {
			return errors.New("development paths cannot be symlinks")
		}
	}
	return nil
}
func PrepareRoot(root string) (Secrets, error) {
	if e := ValidateRoot(root); e != nil {
		return Secrets{}, e
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return Secrets{}, e
	}
	marker := filepath.Join(root, ".development-only")
	if _, e := os.Stat(marker); os.IsNotExist(e) {
		if e = os.WriteFile(marker, []byte(rootMarker), 0600); e != nil {
			return Secrets{}, e
		}
	}
	for _, name := range []string{"secrets", "documents"} {
		if e := os.MkdirAll(filepath.Join(root, name), 0700); e != nil {
			return Secrets{}, e
		}
	}
	out := Secrets{filepath.Join(root, "secrets", "oidc-client"), filepath.Join(root, "secrets", "session-key"), filepath.Join(root, "secrets", "transaction-key")}
	for i, path := range []string{out.Client, out.Session, out.Transaction} {
		info, e := os.Lstat(path)
		if e == nil {
			if !info.Mode().IsRegular() || unsafePath(path, info) {
				return Secrets{}, errors.New("invalid development secret")
			}
			continue
		}
		if !os.IsNotExist(e) {
			return Secrets{}, e
		}
		data := []byte(ClientSecret)
		if i > 0 {
			key := make([]byte, 32)
			if _, e = rand.Read(key); e != nil {
				return Secrets{}, e
			}
			data = []byte(base64.RawStdEncoding.EncodeToString(key))
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return Secrets{}, e
		}
		_, e = f.Write(data)
		closeErr := f.Close()
		if e != nil {
			return Secrets{}, e
		}
		if closeErr != nil {
			return Secrets{}, closeErr
		}
	}
	return out, nil
}
