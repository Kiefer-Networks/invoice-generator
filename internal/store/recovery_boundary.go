package store

import (
	"context"
	"os"
	"path/filepath"
	"sync"
)

// SQLite requires a pathname for WAL discovery. Keep the verified file and
// directory open, require an owner-controlled parent, and bind pathname identity
// before/after SQLite access. A same-user process already has authority over the
// application; nevertheless replacement at these boundaries fails closed.
type recoveryDatabase struct {
	path string
	root *os.Root
	file *os.File
}

func bindRecoveryDatabase(path string, missing bool) (*recoveryDatabase, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrBackup
	}
	root, e := safeRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	b := &recoveryDatabase{path: path, root: root}
	info, e := root.Lstat(filepath.Base(path))
	if os.IsNotExist(e) && missing {
		return b, nil
	}
	if e != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		root.Close()
		return nil, ErrBackup
	}
	b.file, e = root.Open(filepath.Base(path))
	if e != nil {
		root.Close()
		return nil, e
	}
	opened, e := b.file.Stat()
	if e != nil || !os.SameFile(info, opened) {
		b.close()
		return nil, ErrBackup
	}
	if e = b.check(); e != nil {
		b.close()
		return nil, e
	}
	return b, nil
}
func (b *recoveryDatabase) close() {
	if b.file != nil {
		b.file.Close()
	}
	b.root.Close()
}
func sameRecoveryDirectory(path string, root *os.Root) error {
	current, e := safeRoot(path)
	if e != nil {
		return e
	}
	defer current.Close()
	a, e := root.Stat(".")
	if e != nil {
		return e
	}
	z, e := current.Stat(".")
	if e != nil || !os.SameFile(a, z) {
		return ErrBackup
	}
	return nil
}
func (b *recoveryDatabase) check() error {
	if e := sameRecoveryDirectory(filepath.Dir(b.path), b.root); e != nil {
		return e
	}
	name := filepath.Base(b.path)
	info, e := b.root.Lstat(name)
	if b.file == nil {
		if !os.IsNotExist(e) {
			return ErrBackup
		}
		return nil
	}
	opened, e2 := b.file.Stat()
	if e != nil || e2 != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, opened) || !singleRecoveryLink(b.file) {
		return ErrBackup
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		i, e := b.root.Lstat(name + suffix)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil || !i.Mode().IsRegular() || i.Mode()&os.ModeSymlink != 0 {
			return ErrBackup
		}
		f, e := b.root.Open(name + suffix)
		if e != nil {
			return e
		}
		j, e := f.Stat()
		valid := e == nil && os.SameFile(i, j) && singleRecoveryLink(f)
		f.Close()
		if !valid {
			return ErrBackup
		}
	}
	return nil
}

type recoveryStage struct {
	path, name   string
	root, parent *os.Root
	info         os.FileInfo
	published    bool
}

func newRecoveryStage(parent *os.Root, parentPath, prefix string) (*recoveryStage, error) {
	if e := sameRecoveryDirectory(parentPath, parent); e != nil {
		return nil, e
	}
	path, e := privateTemp(parentPath, prefix)
	if e != nil {
		return nil, e
	}
	root, e := parent.OpenRoot(filepath.Base(path))
	if e != nil {
		return nil, e
	}
	i, e := root.Stat(".")
	if e != nil {
		root.Close()
		return nil, e
	}
	s := &recoveryStage{path: path, name: filepath.Base(path), root: root, parent: parent, info: i}
	if e = s.check(); e != nil {
		s.cleanup()
		return nil, e
	}
	return s, nil
}
func (s *recoveryStage) check() error {
	if e := sameRecoveryDirectory(filepath.Dir(s.path), s.parent); e != nil {
		return e
	}
	i, e := s.parent.Lstat(s.name)
	if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || !os.SameFile(i, s.info) {
		return ErrBackup
	}
	return sameRecoveryDirectory(s.path, s.root)
}
func (s *recoveryStage) cleanup() {
	if s.published {
		s.root.Close()
		return
	}
	// Remove only our held directory's contents. Never recursively delete a
	// substitute installed at the original pathname after validation.
	f, e := s.root.Open(".")
	if e == nil {
		entries, e := f.ReadDir(-1)
		f.Close()
		if e == nil {
			for _, entry := range entries {
				_ = s.root.RemoveAll(entry.Name())
			}
		}
	}
	s.root.Close()
	if i, e := s.parent.Lstat(s.name); e == nil && os.SameFile(i, s.info) {
		_ = s.parent.Remove(s.name)
	}
}

func acquireRecoveryService(path string) (*recoveryDatabase, func(), error) {
	path, e := filepath.Abs(path)
	if e != nil {
		return nil, nil, ErrBackup
	}
	b, e := bindRecoveryDatabase(path, true)
	if e != nil {
		return nil, nil, e
	}
	name := filepath.Base(path) + ".service-lock"
	f, e := b.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		b.close()
		return nil, nil, ErrBackup
	}
	lockInfo, e := f.Stat()
	f.Close()
	if e != nil {
		b.close()
		return nil, nil, e
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			if i, e := b.root.Lstat(name); e == nil && os.SameFile(i, lockInfo) {
				b.root.Remove(name)
			}
			b.close()
		})
	}
	if e = b.check(); e != nil {
		release()
		return nil, nil, e
	}
	return b, release, nil
}

// OpenService binds the normal writable service to its verified database and
// lock. The caller closes the Store before releasing the lease.
func OpenService(ctx context.Context, path string) (*Store, func(), error) {
	return openServiceWithHook(ctx, path, nil)
}

func openServiceWithHook(ctx context.Context, path string, afterLease func()) (*Store, func(), error) {
	b, release, e := acquireRecoveryService(path)
	if e != nil {
		return nil, nil, e
	}
	if afterLease != nil {
		afterLease()
	}
	if b.file == nil {
		b.file, e = b.root.OpenFile(filepath.Base(b.path), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if e != nil {
			release()
			return nil, nil, e
		}
	}
	if e = b.check(); e != nil {
		release()
		return nil, nil, e
	}
	db, e := Open(ctx, b.path)
	if e != nil {
		release()
		return nil, nil, e
	}
	if e = b.check(); e != nil {
		db.Close()
		release()
		return nil, nil, e
	}
	return db, release, nil
}
