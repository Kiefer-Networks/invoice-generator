package documents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrRootNotProvisioned = errors.New("document root must be provisioned and durably persisted before startup")

// ValidateRoot checks the existing storage path without creating directories.
// Provisioning must persist the directory and every ancestor before starting the
// server. Runtime artifact barriers cannot make a newly-created root durable.
func ValidateRoot(path string) error {
	root, err := openProvisionedRoot(path)
	if err != nil {
		return err
	}
	return root.Close()
}

func openProvisionedRoot(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("an absolute document root is required")
	}
	volume := filepath.VolumeName(path)
	parts := strings.Split(filepath.ToSlash(path[len(volume):]), "/")
	for _, part := range parts {
		if part == "." || part == ".." {
			return nil, errors.New("document root must not contain traversal components")
		}
	}
	root, err := os.OpenRoot(volume + string(os.PathSeparator))
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		info, err := root.Lstat(part)
		if err != nil {
			_ = root.Close() // Release the handle without replacing the validation result.
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("%w: %s", ErrRootNotProvisioned, path)
			}
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			_ = root.Close() // Release the handle without replacing the validation result.
			return nil, errors.New("document root and ancestors must be real directories")
		}
		next, err := root.OpenRoot(part)
		_ = root.Close() // Release the handle without replacing the validation result.
		if err != nil {
			return nil, err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			return nil, errors.Join(errors.New("document root changed while opening"), next.Close())
		}
		root = next
	}
	return root, nil
}
